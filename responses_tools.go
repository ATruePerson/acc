package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// responseToolTranslation carries the lossless mapping between a Responses
// custom tool and the single-string function wrapper used by Chat Completions
// upstreams. The original definition remains available for the response path.
type responseToolTranslation struct {
	customByBridge map[string]customToolDefinition
	customByName   map[string]customToolDefinition
}

type customToolDefinition struct {
	Name        string
	Description string
	Format      json.RawMessage
	Extra       map[string]json.RawMessage
	Raw         json.RawMessage
	BridgeName  string
}

func newResponseToolTranslation() *responseToolTranslation {
	return &responseToolTranslation{
		customByBridge: map[string]customToolDefinition{},
		customByName:   map[string]customToolDefinition{},
	}
}

func translateResponseTools(req *ResponsesRequest, route Route) (*responseToolTranslation, []OpenAITool, error) {
	translation := newResponseToolTranslation()
	if len(req.Tools) == 0 {
		return translation, nil, nil
	}
	if route.Toolcalling != nil && !*route.Toolcalling {
		return nil, nil, fmt.Errorf("backend %s/%s does not support tool calls", route.Provider, route.Model)
	}

	tools := make([]OpenAITool, 0, len(req.Tools))
	for _, tool := range req.Tools {
		switch tool.Type {
		case "", "function":
			fn := tool.Function
			if tool.Name != "" {
				fn = ResponsesFunction{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters}
			}
			if fn.Name == "" {
				continue
			}
			tools = append(tools, OpenAITool{
				Type: "function",
				Function: OpenAIFunction{
					Name:        fn.Name,
					Description: fn.Description,
					Parameters:  fn.Parameters,
					Strict:      tool.Strict,
				},
			})
		case "custom":
			definition, err := customToolDefinitionFor(tool, route)
			if err != nil {
				return nil, nil, err
			}
			if _, exists := translation.customByName[definition.Name]; exists {
				return nil, nil, fmt.Errorf("duplicate custom tool name %q", definition.Name)
			}
			translation.customByBridge[definition.BridgeName] = definition
			translation.customByName[definition.Name] = definition
			strict := true
			tools = append(tools, OpenAITool{
				Type: "function",
				Function: OpenAIFunction{
					Name:        definition.BridgeName,
					Description: customToolBridgeDescription(definition),
					Parameters:  json.RawMessage(`{"type":"object","properties":{"input":{"type":"string","description":"Raw custom-tool input. Pass the source exactly, without quotes, markdown fences, or JSON wrapping."}},"required":["input"],"additionalProperties":false}`),
					Strict:      &strict,
				},
			})
		default:
			if isHostedResponsesTool(tool.Type) {
				return nil, nil, fmt.Errorf("backend %s/%s does not support hosted tool %q through Chat Completions", route.Provider, route.Model, tool.Type)
			}
			return nil, nil, fmt.Errorf("backend %s/%s does not support tool type %q", route.Provider, route.Model, tool.Type)
		}
	}
	return translation, tools, nil
}

func isHostedResponsesTool(toolType string) bool {
	switch toolType {
	case "web_search", "file_search", "computer_use_preview", "computer", "image_generation", "code_interpreter", "shell", "apply_patch", "mcp", "tool_search":
		return true
	default:
		return false
	}
}

func customToolDefinitionFor(tool ResponsesTool, route Route) (customToolDefinition, error) {
	if tool.Name == "" {
		return customToolDefinition{}, fmt.Errorf("backend %s/%s cannot bridge unnamed custom tool", route.Provider, route.Model)
	}
	if len(tool.Format) > 0 && string(tool.Format) != "null" {
		var format struct {
			Type       string `json:"type"`
			Syntax     string `json:"syntax"`
			Definition string `json:"definition"`
		}
		if err := json.Unmarshal(tool.Format, &format); err != nil {
			return customToolDefinition{}, fmt.Errorf("backend %s/%s cannot bridge custom tool %q: invalid format: %w", route.Provider, route.Model, tool.Name, err)
		}
		if format.Type != "grammar" || (format.Syntax != "lark" && format.Syntax != "regex") || format.Definition == "" {
			return customToolDefinition{}, fmt.Errorf("backend %s/%s cannot bridge custom tool %q format through Chat Completions", route.Provider, route.Model, tool.Name)
		}
	}
	raw := tool.Raw
	if len(raw) == 0 {
		raw, _ = json.Marshal(tool)
	}
	hash := sha256.Sum256(raw)
	bridgeName := fmt.Sprintf("acc_custom_%x", hash[:10])
	return customToolDefinition{
		Name: tool.Name, Description: tool.Description, Format: append(json.RawMessage(nil), tool.Format...),
		Extra: tool.Extra, Raw: append(json.RawMessage(nil), raw...), BridgeName: bridgeName,
	}, nil
}

func customToolBridgeDescription(definition customToolDefinition) string {
	extra, _ := json.Marshal(definition.Extra)
	return fmt.Sprintf("%s\n\nACC custom-tool bridge for %q. The original tool accepts one raw string, so put that exact string in the required input field. Do not quote it a second time and do not wrap it in Markdown.\n\nOriginal format: %s\nOriginal forward-compatible fields: %s", definition.Description, definition.Name, definition.Format, extra)
}

func (t *responseToolTranslation) customForBridge(name string) (customToolDefinition, bool) {
	definition, ok := t.customByBridge[name]
	return definition, ok
}

func (t *responseToolTranslation) customForName(name string) (customToolDefinition, bool) {
	definition, ok := t.customByName[name]
	return definition, ok
}

func customToolArguments(input string) string {
	b, _ := json.Marshal(map[string]string{"input": input})
	return string(b)
}

func customToolInput(arguments string) (string, error) {
	var wrapped struct {
		Input string `json:"input"`
	}
	if err := json.Unmarshal([]byte(arguments), &wrapped); err != nil {
		return "", fmt.Errorf("custom bridge returned invalid arguments: %w", err)
	}
	if wrapped.Input == "" {
		return "", fmt.Errorf("custom bridge returned no raw input")
	}
	return wrapped.Input, nil
}

func responseToolOutputContent(output json.RawMessage) json.RawMessage {
	if len(output) == 0 || string(output) == "null" {
		return jsonString("")
	}
	return append(json.RawMessage(nil), output...)
}

func appendChatToolCall(messages *[]OpenAIMessage, toolCall OpenAIToolCall) {
	for idx := len(*messages) - 1; idx >= 0; idx-- {
		if (*messages)[idx].Role == "assistant" {
			(*messages)[idx].ToolCalls = append((*messages)[idx].ToolCalls, toolCall)
			return
		}
	}
	*messages = append(*messages, OpenAIMessage{Role: "assistant", ToolCalls: []OpenAIToolCall{toolCall}})
}
