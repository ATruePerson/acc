package main

import (
	"github.com/ATruePerson/acc/claude"
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// buildProviderRequest is the only place that applies provider authentication.
// Keeping it here makes it harder for credentials to leak into catalogs, logs,
// request bodies, or command output.
func buildProviderRequest(ctx context.Context, runtime providerRuntime, request *OpenAIRequest) (*http.Request, error) {
	return buildProviderRequestWithBody(ctx, runtime, request, nil)
}

func buildProviderRequestWithBody(ctx context.Context, runtime providerRuntime, request *OpenAIRequest, openAIBody []byte) (*http.Request, error) {
	var (
		body     []byte
		endpoint string
		err      error
	)
	switch runtime.Adapter {
	case "", providerAdapterOpenAIChat:
		if openAIBody != nil {
			body = openAIBody
		} else {
			body, err = json.Marshal(request)
		}
		endpoint = strings.TrimRight(runtime.BaseURL, "/") + "/chat/completions"
	case providerAdapterAnthropic:
		body, err = marshalAnthropicRequest(request, runtime.OAuth)
		endpoint = strings.TrimRight(runtime.BaseURL, "/") + "/v1/messages"
	default:
		return nil, fmt.Errorf("unsupported provider adapter %q", runtime.Adapter)
	}
	if err != nil {
		return nil, err
	}
	upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	upstream.Header.Set("Content-Type", "application/json")
	if runtime.Adapter == providerAdapterAnthropic {
		upstream.Header.Set("anthropic-version", "2023-06-01")
		upstream.Header.Set("x-api-key", runtime.APIKey)
	} else {
		token := runtime.BearerToken
		if token == "" {
			token = runtime.APIKey
		}
		upstream.Header.Set("Authorization", "Bearer "+token)
	}
	return upstream, nil
}

// doProviderRequest performs at most one OAuth refresh-and-replay after a 401.
func doProviderRequest(ctx context.Context, client *http.Client, auth *authManager, runtime providerRuntime, request *OpenAIRequest) (*http.Response, error) {
	return doProviderRequestWithBody(ctx, client, auth, runtime, request, nil)
}

func doProviderRequestWithBody(ctx context.Context, client *http.Client, auth *authManager, runtime providerRuntime, request *OpenAIRequest, openAIBody []byte) (*http.Response, error) {
	for replay := 0; replay < 2; replay++ {
		upstream, err := buildProviderRequestWithBody(ctx, runtime, request, openAIBody)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(upstream)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusUnauthorized || !runtime.OAuth || replay == 1 {
			if err := normalizeProviderResponse(resp, runtime.Adapter, request.Stream); err != nil {
				resp.Body.Close()
				return nil, err
			}
			return resp, nil
		}
		resp.Body.Close()
		token, err := auth.AccessToken(ctx, runtime.ID, true)
		if err != nil {
			return nil, err
		}
		runtime.BearerToken = token
	}
	return nil, fmt.Errorf("provider request replay exhausted")
}

type anthropicWireRequest struct {
	Model       string                 `json:"model"`
	MaxTokens   int                    `json:"max_tokens"`
	System      string                 `json:"system,omitempty"`
	Messages    []anthropicWireMessage `json:"messages"`
	Stream      bool                   `json:"stream,omitempty"`
	Tools       []AnthropicTool        `json:"tools,omitempty"`
	Temperature *float64               `json:"temperature,omitempty"`
	TopP        *float64               `json:"top_p,omitempty"`
}

type anthropicWireMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

func marshalAnthropicRequest(request *OpenAIRequest, oauth bool) ([]byte, error) {
	wire := anthropicWireRequest{
		Model: request.Model, MaxTokens: request.MaxTokens, Stream: request.Stream,
		Temperature: request.Temperature, TopP: request.TopP,
	}
	if wire.MaxTokens <= 0 {
		wire.MaxTokens = 4096
	}
	for _, tool := range request.Tools {
		name := tool.Function.Name
		if oauth {
			name = anthropicOAuthToolName(name)
		}
		wire.Tools = append(wire.Tools, AnthropicTool{Name: name, Description: tool.Function.Description, InputSchema: tool.Function.Parameters})
	}
	for _, message := range request.Messages {
		if message.Role == "system" {
			text, err := openAIContentText(message.Content)
			if err != nil {
				return nil, err
			}
			if wire.System != "" {
				wire.System += "\n\n"
			}
			wire.System += text
			continue
		}
		converted, err := openAIMessageToAnthropic(message)
		if err != nil {
			return nil, err
		}
		wire.Messages = append(wire.Messages, converted...)
	}
	return json.Marshal(wire)
}

var anthropicBuiltinTools = map[string]bool{"web_search": true, "code_execution": true, "text_editor": true, "computer": true}

func anthropicOAuthToolName(name string) string {
	lower := strings.ToLower(name)
	if anthropicBuiltinTools[lower] || strings.HasPrefix(lower, "custom_") {
		return name
	}
	return "custom_" + name
}

func restoreAnthropicOAuthToolName(name string) string {
	return strings.TrimPrefix(name, "custom_")
}

func openAIMessageToAnthropic(message OpenAIMessage) ([]anthropicWireMessage, error) {
	if message.Role == "tool" {
		content := []map[string]any{{"type": "tool_result", "tool_use_id": message.ToolCallID, "content": claude.DecodeStringContent(message.Content)}}
		return []anthropicWireMessage{{Role: "user", Content: content}}, nil
	}
	role := message.Role
	if role != "assistant" {
		role = "user"
	}
	parts, err := openAIContentToAnthropic(message.Content)
	if err != nil {
		return nil, err
	}
	for _, tool := range message.ToolCalls {
		var input any = map[string]any{}
		if tool.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(tool.Function.Arguments), &input); err != nil {
				return nil, fmt.Errorf("tool %s arguments: %w", tool.Function.Name, err)
			}
		}
		parts = append(parts, map[string]any{"type": "tool_use", "id": tool.ID, "name": tool.Function.Name, "input": input})
	}
	if len(parts) == 1 && parts[0]["type"] == "text" && len(message.ToolCalls) == 0 {
		return []anthropicWireMessage{{Role: role, Content: parts[0]["text"]}}, nil
	}
	return []anthropicWireMessage{{Role: role, Content: parts}}, nil
}

func openAIContentText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var parts []OpenAIContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", err
	}
	var out []string
	for _, part := range parts {
		if part.Text != "" {
			out = append(out, part.Text)
		}
	}
	return strings.Join(out, "\n"), nil
}

func openAIContentToAnthropic(raw json.RawMessage) ([]map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if text == "" {
			return nil, nil
		}
		return []map[string]any{{"type": "text", "text": text}}, nil
	}
	var parts []OpenAIContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "text", "input_text", "output_text":
			out = append(out, map[string]any{"type": "text", "text": part.Text})
		case "image_url", "input_image":
			if part.ImageURL == nil || !strings.HasPrefix(part.ImageURL.URL, "data:") {
				return nil, fmt.Errorf("Anthropic image input requires a data URL")
			}
			mediaType, data, ok := strings.Cut(strings.TrimPrefix(part.ImageURL.URL, "data:"), ";base64,")
			if !ok || mediaType == "" {
				return nil, fmt.Errorf("invalid image data URL")
			}
			if _, err := base64.StdEncoding.DecodeString(data); err != nil {
				return nil, fmt.Errorf("invalid image base64: %w", err)
			}
			out = append(out, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": mediaType, "data": data}})
		default:
			return nil, fmt.Errorf("unsupported Anthropic content part %q", part.Type)
		}
	}
	return out, nil
}

func normalizeProviderResponse(resp *http.Response, adapter string, stream bool) error {
	if adapter != providerAdapterAnthropic || resp.StatusCode >= 400 {
		return nil
	}
	if stream {
		resp.Body = anthropicStreamToOpenAI(resp.Body)
		resp.Header.Set("Content-Type", "text/event-stream")
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	converted, err := anthropicResponseToOpenAI(body)
	if err != nil {
		return err
	}
	resp.Body = io.NopCloser(bytes.NewReader(converted))
	resp.ContentLength = int64(len(converted))
	return nil
}

func anthropicResponseToOpenAI(body []byte) ([]byte, error) {
	var wire struct {
		Content []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			Thinking string          `json:"thinking"`
			ID       string          `json:"id"`
			Name     string          `json:"name"`
			Input    json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("parse Anthropic response: %w", err)
	}
	message := &OpenAIMessage{Role: "assistant"}
	var texts, thinking []string
	for _, block := range wire.Content {
		switch block.Type {
		case "text":
			texts = append(texts, block.Text)
		case "thinking":
			thinking = append(thinking, block.Thinking)
		case "tool_use":
			message.ToolCalls = append(message.ToolCalls, OpenAIToolCall{ID: block.ID, Type: "function", Function: OpenAIFuncCall{Name: block.Name, Arguments: string(block.Input)}})
		}
	}
	if len(texts) > 0 {
		message.Content = claude.JSONString(strings.Join(texts, ""))
	}
	if len(thinking) > 0 {
		message.ReasoningContent = claude.JSONString(strings.Join(thinking, ""))
	}
	finish := map[string]string{"end_turn": "stop", "max_tokens": "length", "tool_use": "tool_calls"}[wire.StopReason]
	response := OpenAIResponse{
		Choices: []OpenAIChoice{{Message: message, FinishReason: finish}},
		Usage:   &OpenAIUsage{PromptTokens: wire.Usage.InputTokens, CompletionTokens: wire.Usage.OutputTokens},
	}
	return json.Marshal(response)
}

func anthropicStreamToOpenAI(source io.ReadCloser) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		defer source.Close()
		defer writer.Close()
		scanner := bufio.NewScanner(source)
		scanner.Buffer(make([]byte, 64*1024), 2<<20)
		toolIndexes := map[int]bool{}
		inputTokens, outputTokens := 0, 0
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" || data == "[DONE]" {
				continue
			}
			var event map[string]any
			if json.Unmarshal([]byte(data), &event) != nil {
				continue
			}
			delta := OpenAIMessage{}
			finish := ""
			switch event["type"] {
			case "message_start":
				message, _ := event["message"].(map[string]any)
				usage, _ := message["usage"].(map[string]any)
				inputTokens = int(numberValue(usage["input_tokens"]))
				continue
			case "content_block_start":
				index := int(numberValue(event["index"]))
				block, _ := event["content_block"].(map[string]any)
				if block["type"] == "tool_use" {
					toolIndexes[index] = true
					delta.ToolCalls = []OpenAIToolCall{{Index: index, ID: stringValue(block["id"]), Type: "function", Function: OpenAIFuncCall{Name: stringValue(block["name"])}}}
				}
			case "content_block_delta":
				index := int(numberValue(event["index"]))
				part, _ := event["delta"].(map[string]any)
				switch part["type"] {
				case "text_delta":
					delta.Content = claude.JSONString(stringValue(part["text"]))
				case "thinking_delta":
					delta.ReasoningContent = claude.JSONString(stringValue(part["thinking"]))
				case "input_json_delta":
					if toolIndexes[index] {
						delta.ToolCalls = []OpenAIToolCall{{Index: index, Function: OpenAIFuncCall{Arguments: stringValue(part["partial_json"])}}}
					}
				}
			case "message_delta":
				part, _ := event["delta"].(map[string]any)
				finish = map[string]string{"end_turn": "stop", "max_tokens": "length", "tool_use": "tool_calls"}[stringValue(part["stop_reason"])]
				usage, _ := event["usage"].(map[string]any)
				outputTokens = int(numberValue(usage["output_tokens"]))
			case "message_stop":
				usageChunk := OpenAIResponse{Choices: []OpenAIChoice{}, Usage: &OpenAIUsage{PromptTokens: inputTokens, CompletionTokens: outputTokens}}
				encoded, _ := json.Marshal(usageChunk)
				_, _ = fmt.Fprintf(writer, "data: %s\n\n", encoded)
				_, _ = io.WriteString(writer, "data: [DONE]\n\n")
				continue
			default:
				continue
			}
			chunk := OpenAIResponse{Choices: []OpenAIChoice{{Delta: &delta, FinishReason: finish}}}
			encoded, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(writer, "data: %s\n\n", encoded)
		}
		if err := scanner.Err(); err != nil {
			_ = writer.CloseWithError(err)
		}
	}()
	return reader
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func numberValue(value any) float64 {
	number, _ := value.(float64)
	return number
}
