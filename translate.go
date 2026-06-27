package main

import (
	"encoding/json"
	"fmt"
	"strings"
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
		TopP:        ar.TopP,
	}

	// Apply route-level overrides if specified in config.json
	if route.Temperature != nil {
		or.Temperature = route.Temperature
	}
	if route.TopP != nil {
		or.TopP = route.TopP
	}
	if route.MaxTokens > 0 {
		or.MaxTokens = route.MaxTokens
	}

	// system prompt -> leading system message (with optional prepend)
	sys := decodeSystem(ar.System)
	prepend := cfg.SystemPrepend
	if route.SystemPrepend != "" {
		prepend = route.SystemPrepend // per-route overrides the global prepend
	}
	if prepend != "" {
		sys = prepend + "\n\n" + sys
	}
	if sys != "" {
		or.Messages = append(or.Messages, OpenAIMessage{
			Role:    "system",
			Content: jsonString(sys),
		})
	}

	for _, m := range ar.Messages {
		msgs, err := translateMessage(m, route.Vision)
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
	if or.ReasoningEffort != "" {
		or.ReasoningEffort = sanitizeReasoningEffort(route.Provider, or.ReasoningEffort)
	}

	if ar.Stream {
		or.StreamOptions = &StreamOptions{IncludeUsage: true}
	}

	if route.Provider == "gemini" {
		for i := range or.Messages {
			for j := range or.Messages[i].ToolCalls {
				tc := &or.Messages[i].ToolCalls[j]
				thoughtSig := tc.Function.ThoughtSignature
				tc.Function.ThoughtSignature = ""
				if thoughtSig == "" {
					thoughtSig = "skip_thought_signature_validator"
				}
				tc.ExtraContent = &OpenAIExtraContent{
					Google: &OpenAIGoogleExtra{
						ThoughtSignature: thoughtSig,
					},
				}
			}
		}
	}

	return or, nil
}

// translateMessage turns one Anthropic message into one or more OpenAI
// messages (tool_result blocks become separate role:"tool" messages).
func translateMessage(m AnthropicMessage, vision bool) ([]OpenAIMessage, error) {
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
			if !vision {
				// Fail loud rather than silently dropping the image and letting a
				// text-only model answer blind. The caller must pick a vision model.
				return nil, fmt.Errorf("this model is text-only and cannot see images — switch to a vision model (e.g. gemini-pro / gemini-flash)")
			}
			if b.Source != nil && b.Source.Type == "base64" {
				url := fmt.Sprintf("data:%s;base64,%s", b.Source.MediaType, b.Source.Data)
				parts = append(parts, OpenAIContentPart{
					Type:     "image_url",
					ImageURL: &OpenAIImageURL{URL: url},
				})
			}
		case "tool_use":
			id := b.ID
			var thoughtSig string
			if parts := strings.SplitN(b.ID, "__thought__", 2); len(parts) == 2 {
				id = parts[0]
				thoughtSig = parts[1]
			}
			toolCalls = append(toolCalls, OpenAIToolCall{
				ID:   id,
				Type: "function",
				Function: OpenAIFuncCall{
					Name:             b.Name,
					Arguments:        string(b.Input),
					ThoughtSignature: thoughtSig,
				},
			})
		case "tool_result":
			id := b.ToolUseID
			if parts := strings.SplitN(b.ToolUseID, "__thought__", 2); len(parts) == 2 {
				id = parts[0]
			}
			// flush as its own tool message
			out = append(out, OpenAIMessage{
				Role:       "tool",
				ToolCallID: id,
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
		out = append(out, msg)
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
				thoughtSig := tc.Function.ThoughtSignature
				if tc.ExtraContent != nil && tc.ExtraContent.Google != nil && tc.ExtraContent.Google.ThoughtSignature != "" {
					thoughtSig = tc.ExtraContent.Google.ThoughtSignature
				}
				id := tc.ID
				if thoughtSig != "" {
					id = fmt.Sprintf("%s__thought__%s", tc.ID, thoughtSig)
				}
				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    id,
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

// costFor estimates the USD cost of a request from configured per-1M-token
// pricing. Returns 0 for free or unpriced models.
func costFor(model string, in, out int, cfg *Config) float64 {
	if cfg == nil {
		return 0
	}
	p, ok := cfg.Pricing[model]
	if !ok {
		return 0
	}
	return float64(in)/1e6*p.InputPer1M + float64(out)/1e6*p.OutputPer1M
}

func jsonString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func sanitizeReasoningEffort(provider string, effort string) string {
	if effort == "" {
		return ""
	}
	switch provider {
	case "opencode":
		// opencode expects one of high, low, medium, max, xhigh
		switch effort {
		case "low", "medium", "high", "max", "xhigh":
			return effort
		case "ultracode":
			return "max" // fallback to max
		default:
			return "high"
		}
	case "nvidia", "gemini", "zai", "openrouter":
		// standard OpenAI/Nvidia/etc usually expects low, medium, high
		switch effort {
		case "low", "medium", "high":
			return effort
		case "max", "xhigh", "ultracode":
			return "high" // fallback to high
		default:
			return "high"
		}
	default:
		return effort
	}
}
