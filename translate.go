package main

import (
	"encoding/json"
	"fmt"
)

// translateRequest converts an Anthropic /v1/messages request into an
// OpenAI /v1/chat/completions request. This is where the screenshot bug
// dies: image blocks become data: URLs instead of being dropped.
func translateRequest(ar *AnthropicRequest, route Route, cfg *Config) (*OpenAIRequest, error) {
	or := &OpenAIRequest{
		Model:       route.Model,
		MaxTokens:   ar.MaxTokens,
		Stream:      ar.Stream,
		Temperature: ar.Temperature,
	}

	// system prompt -> leading system message (with optional prepend)
	sys := decodeSystem(ar.System)
	if cfg.SystemPrepend != "" {
		sys = cfg.SystemPrepend + "\n\n" + sys
	}
	if sys != "" {
		or.Messages = append(or.Messages, OpenAIMessage{
			Role:    "system",
			Content: jsonString(sys),
		})
	}

	for _, m := range ar.Messages {
		msgs, err := translateMessage(m)
		if err != nil {
			return nil, err
		}
		or.Messages = append(or.Messages, msgs...)
	}

	// tools
	for _, t := range ar.Tools {
		or.Tools = append(or.Tools, OpenAITool{
			Type: "function",
			Function: OpenAIFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	// effort: map thinking budget -> reasoning_effort bucket
	if ar.Thinking != nil && ar.Thinking.BudgetTokens > 0 {
		or.ReasoningEffort = bucketForBudget(ar.Thinking.BudgetTokens, cfg)
	} else if route.ReasoningEffort != "" {
		or.ReasoningEffort = route.ReasoningEffort
	}

	if ar.Stream {
		or.StreamOptions = &StreamOptions{IncludeUsage: true}
	}
	return or, nil
}

// translateMessage turns one Anthropic message into one or more OpenAI
// messages (tool_result blocks become separate role:"tool" messages).
func translateMessage(m AnthropicMessage) ([]OpenAIMessage, error) {
	// content can be a plain string
	var asString string
	if err := json.Unmarshal(m.Content, &asString); err == nil {
		return []OpenAIMessage{{Role: m.Role, Content: jsonString(asString)}}, nil
	}

	var blocks []AnthropicBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return nil, fmt.Errorf("bad content: %w", err)
	}

	var parts []OpenAIContentPart
	var toolCalls []OpenAIToolCall
	var out []OpenAIMessage

	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, OpenAIContentPart{Type: "text", Text: b.Text})
		case "image":
			if b.Source != nil && b.Source.Type == "base64" {
				url := fmt.Sprintf("data:%s;base64,%s", b.Source.MediaType, b.Source.Data)
				parts = append(parts, OpenAIContentPart{
					Type:     "image_url",
					ImageURL: &OpenAIImageURL{URL: url},
				})
			}
		case "tool_use":
			toolCalls = append(toolCalls, OpenAIToolCall{
				ID:   b.ID,
				Type: "function",
				Function: OpenAIFuncCall{
					Name:      b.Name,
					Arguments: string(b.Input),
				},
			})
		case "tool_result":
			// flush as its own tool message
			out = append(out, OpenAIMessage{
				Role:       "tool",
				ToolCallID: b.ToolUseID,
				Content:    jsonString(decodeToolResult(b.Content)),
			})
		}
	}

	// assistant/user text+image message
	if len(parts) > 0 || len(toolCalls) > 0 {
		msg := OpenAIMessage{Role: m.Role, ToolCalls: toolCalls}
		if len(parts) == 1 && parts[0].Type == "text" {
			msg.Content = jsonString(parts[0].Text) // simple string form
		} else if len(parts) > 0 {
			raw, _ := json.Marshal(parts)
			msg.Content = raw
		}
		out = append([]OpenAIMessage{msg}, out...)
	}
	return out, nil
}

// translateResponse converts a non-streaming OpenAI response into an
// Anthropic message response.
func translateResponse(or *OpenAIResponse, model string) map[string]any {
	var content []map[string]any
	stopReason := "end_turn"

	if len(or.Choices) > 0 {
		ch := or.Choices[0]
		if ch.Message != nil {
			if txt := decodeStringContent(ch.Message.Content); txt != "" {
				content = append(content, map[string]any{"type": "text", "text": txt})
			}
			for _, tc := range ch.Message.ToolCalls {
				var input json.RawMessage = []byte(tc.Function.Arguments)
				if len(input) == 0 {
					input = []byte("{}")
				}
				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Function.Name,
					"input": input,
				})
			}
		}
		if ch.FinishReason == "tool_calls" {
			stopReason = "tool_use"
		} else if ch.FinishReason == "length" {
			stopReason = "max_tokens"
		}
	}
	if content == nil {
		content = []map[string]any{}
	}

	usage := map[string]any{"input_tokens": 0, "output_tokens": 0}
	if or.Usage != nil {
		usage["input_tokens"] = or.Usage.PromptTokens
		usage["output_tokens"] = or.Usage.CompletionTokens
	}

	return map[string]any{
		"id":          "msg_" + randID(),
		"type":        "message",
		"role":        "assistant",
		"model":       model,
		"content":     content,
		"stop_reason": stopReason,
		"usage":       usage,
	}
}

// ---------- helpers ----------

func decodeSystem(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []AnthropicBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		out := ""
		for _, b := range blocks {
			if b.Type == "text" {
				out += b.Text + "\n"
			}
		}
		return out
	}
	return ""
}

func decodeToolResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []AnthropicBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		out := ""
		for _, b := range blocks {
			if b.Type == "text" {
				out += b.Text
			}
		}
		return out
	}
	return string(raw)
}

func decodeStringContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

func bucketForBudget(budget int, cfg *Config) string {
	// pick the configured bucket whose budget is closest at or below.
	best := "low"
	bestBudget := -1
	for _, e := range cfg.Effort {
		if e.Budget <= budget && e.Budget > bestBudget {
			bestBudget = e.Budget
			best = e.Reasoning
		}
	}
	if bestBudget == -1 {
		return "low"
	}
	return best
}

func jsonString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}
