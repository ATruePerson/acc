package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// translateFromResponses converts a Responses API request into an OpenAI request.
func translateFromResponses(req *ResponsesRequest, route Route, cfg *Config) (*OpenAIRequest, error) {
	or, _, err := translateFromResponsesWithTools(req, route, cfg)
	return or, err
}

// translateFromResponsesWithTools also returns the custom-tool mapping needed
// to restore native Responses items after an OpenAI Chat Completions upstream.
func translateFromResponsesWithTools(req *ResponsesRequest, route Route, cfg *Config) (*OpenAIRequest, *responseToolTranslation, error) {
	maxTokens := req.MaxOutputTokens
	if maxTokens == 0 {
		maxTokens = req.MaxTokens
	}
	or := &OpenAIRequest{
		Model:             route.Model,
		MaxTokens:         maxTokens,
		Stream:            req.Stream,
		Temperature:       req.Temperature,
		TopP:              req.TopP,
		ParallelToolCalls: req.ParallelToolCalls,
		ToolChoice:        req.ToolChoice,
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

	system := req.Instructions
	prepend := ""
	if cfg != nil {
		prepend = cfg.SystemPrepend
	}
	if prepend != "" {
		if system != "" {
			system = prepend + "\n\n" + system
		} else {
			system = prepend
		}
	}
	if system != "" {
		or.Messages = append(or.Messages, OpenAIMessage{Role: "system", Content: jsonString(system)})
	}

	// Determine reasoning effort
	if req.Reasoning != nil && req.Reasoning.Effort != "" {
		or.ReasoningEffort = req.Reasoning.Effort
	} else if route.ReasoningEffort != "" {
		or.ReasoningEffort = route.ReasoningEffort
	}
	if req.Stream {
		or.StreamOptions = &StreamOptions{IncludeUsage: true}
	}

	translation, tools, err := translateResponseTools(req, route)
	if err != nil {
		return nil, nil, err
	}
	or.Tools = tools

	// Translate input -> messages
	if len(req.Input) > 0 {
		var inputStr string
		if err := json.Unmarshal(req.Input, &inputStr); err == nil {
			// Simple string input
			or.Messages = append(or.Messages, OpenAIMessage{
				Role:    "user",
				Content: jsonString(inputStr),
			})
		} else {
			var items []ResponsesItem
			if err := json.Unmarshal(req.Input, &items); err != nil {
				return nil, nil, fmt.Errorf("bad input: %w", err)
			}

			for _, item := range items {
				switch item.Type {
				case "message":
					content, err := responsesContentToChat(item.Content)
					if err != nil {
						return nil, nil, fmt.Errorf("bad message content: %w", err)
					}
					or.Messages = append(or.Messages, OpenAIMessage{
						Role:    item.Role,
						Content: content,
					})
				case "reasoning":
					if len(item.Summary) == 0 {
						continue
					}
					var summary strings.Builder
					for _, part := range item.Summary {
						if part.Text == "" {
							continue
						}
						if summary.Len() > 0 {
							summary.WriteString("\n")
						}
						summary.WriteString(part.Text)
					}
					if summary.Len() > 0 {
						or.Messages = append(or.Messages, OpenAIMessage{
							Role:             "assistant",
							ReasoningContent: jsonString(summary.String()),
						})
					}
				case "function_call":
					id := item.CallID
					if id == "" {
						id = item.ID
					}
					var thoughtSig string
					if parts := strings.SplitN(id, "__thought__", 2); len(parts) == 2 {
						id = parts[0]
						thoughtSig = parts[1]
					}
					functionName := item.Name
					if item.Namespace != "" {
						definition, ok := translation.namespaceForQualifiedName(item.Namespace, item.Name)
						if !ok {
							return nil, nil, fmt.Errorf("namespace tool call %q.%q has no matching definition", item.Namespace, item.Name)
						}
						functionName = definition.BridgeName
					}
					toolCall := OpenAIToolCall{
						ID:   id,
						Type: "function",
						Function: OpenAIFuncCall{
							Name:             functionName,
							Arguments:        item.Arguments,
							ThoughtSignature: thoughtSig,
						},
					}
					appendChatToolCall(&or.Messages, toolCall)
				case "function_call_output":
					id := item.CallID
					if parts := strings.SplitN(item.CallID, "__thought__", 2); len(parts) == 2 {
						id = parts[0]
					}
					// Translate to a message with role="tool"
					or.Messages = append(or.Messages, OpenAIMessage{
						Role:       "tool",
						ToolCallID: id,
						Content:    responseToolOutputContent(item.Output),
					})
				case "custom_tool_call":
					definition, ok := translation.customForName(item.Name)
					if !ok {
						return nil, nil, fmt.Errorf("custom tool call %q has no matching custom tool definition", item.Name)
					}
					id := item.CallID
					if id == "" {
						id = item.ID
					}
					appendChatToolCall(&or.Messages, OpenAIToolCall{
						ID: id, Type: "function",
						Function: OpenAIFuncCall{Name: definition.BridgeName, Arguments: customToolArguments(item.Input)},
					})
				case "custom_tool_call_output":
					or.Messages = append(or.Messages, OpenAIMessage{
						Role:       "tool",
						ToolCallID: item.CallID,
						Content:    responseToolOutputContent(item.Output),
					})
				default:
					return nil, nil, fmt.Errorf("unsupported Responses input item type %q", item.Type)
				}
			}
		}
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

	return or, translation, nil
}

func responsesContentToChat(raw json.RawMessage) (json.RawMessage, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return jsonString(text), nil
	}

	var parts []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text,omitempty"`
		ImageURL json.RawMessage `json:"image_url,omitempty"`
		Detail   string          `json:"detail,omitempty"`
		FileID   string          `json:"file_id,omitempty"`
		FileData string          `json:"file_data,omitempty"`
		Filename string          `json:"filename,omitempty"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, err
	}

	var out []OpenAIContentPart
	for _, part := range parts {
		switch part.Type {
		case "input_text", "output_text", "text":
			out = append(out, OpenAIContentPart{Type: "text", Text: part.Text})
		case "input_image", "image_url":
			detail := part.Detail
			var url string
			if err := json.Unmarshal(part.ImageURL, &url); err != nil {
				var image struct {
					URL    string `json:"url"`
					Detail string `json:"detail,omitempty"`
				}
				if json.Unmarshal(part.ImageURL, &image) == nil {
					url = image.URL
					if detail == "" {
						detail = image.Detail
					}
				}
			}
			if url != "" {
				out = append(out, OpenAIContentPart{Type: "image_url", ImageURL: &OpenAIImageURL{URL: url}, Detail: detail})
			} else {
				return nil, fmt.Errorf("image input has no URL or data URI")
			}
		case "input_file", "file":
			out = append(out, OpenAIContentPart{Type: "file", File: &OpenAIFile{
				FileID: part.FileID, FileData: part.FileData, Filename: part.Filename,
			}})
		default:
			return nil, fmt.Errorf("unsupported Responses content part type %q", part.Type)
		}
	}
	if len(out) == 1 && out[0].Type == "text" {
		return jsonString(out[0].Text), nil
	}
	encoded, err := json.Marshal(out)
	return encoded, err
}

// translateToResponses converts a non-streaming OpenAI response back to Responses API format.
func translateToResponses(or *OpenAIResponse, model string) *ResponsesResponse {
	resp, _ := translateToResponsesWithTools(or, model, newResponseToolTranslation())
	return resp
}

func translateToResponsesWithTools(or *OpenAIResponse, model string, translation *responseToolTranslation) (*ResponsesResponse, error) {
	resp := &ResponsesResponse{
		ID:        "resp_" + randID(),
		Object:    "response",
		CreatedAt: time.Now().Unix(),
		Status:    "completed",
		Model:     model,
		Output:    []ResponsesItem{},
	}
	if or.Usage != nil {
		resp.Usage = &ResponsesUsage{
			InputTokens:  or.Usage.PromptTokens,
			OutputTokens: or.Usage.CompletionTokens,
			TotalTokens:  or.Usage.PromptTokens + or.Usage.CompletionTokens,
		}
	}
	if len(or.Choices) > 0 {
		ch := or.Choices[0]
		if ch.Message != nil {
			if reasoning := decodeStringContent(ch.Message.ReasoningContent); reasoning != "" {
				resp.Output = append(resp.Output, ResponsesItem{
					ID: "rs_" + randID(), Type: "reasoning", Status: "completed",
					Summary: []ResponsesSummary{{Type: "summary_text", Text: reasoning}},
				})
			}
			// If there's content, add message item
			txt := decodeStringContent(ch.Message.Content)
			if txt != "" {
				content, _ := json.Marshal([]map[string]any{{
					"type": "output_text", "text": txt, "annotations": []any{},
				}})
				resp.Output = append(resp.Output, ResponsesItem{
					ID:      "item_" + randID(),
					Type:    "message",
					Status:  "completed",
					Role:    "assistant",
					Content: content,
				})
			}
			// Restore native custom calls from ACC's single-string bridge while
			// leaving ordinary function/MCP calls untouched.
			for _, tc := range ch.Message.ToolCalls {
				callID := tc.ID
				thoughtSig := tc.Function.ThoughtSignature
				if tc.ExtraContent != nil && tc.ExtraContent.Google != nil && tc.ExtraContent.Google.ThoughtSignature != "" {
					thoughtSig = tc.ExtraContent.Google.ThoughtSignature
				}
				if thoughtSig != "" {
					callID += "__thought__" + thoughtSig
				}
				if definition, ok := translation.customForBridge(tc.Function.Name); ok {
					input, err := customToolInput(tc.Function.Arguments)
					if err != nil {
						return nil, fmt.Errorf("restore custom tool %q: %w", definition.Name, err)
					}
					resp.Output = append(resp.Output, ResponsesItem{
						ID: "ctc_" + randID(), Type: "custom_tool_call", Status: "completed",
						CallID: callID, Name: definition.Name, Input: input,
					})
					continue
				}
				if definition, ok := translation.namespaceForBridge(tc.Function.Name); ok {
					resp.Output = append(resp.Output, ResponsesItem{
						ID: "fc_" + randID(), Type: "function_call", Status: "completed",
						CallID: callID, Namespace: definition.Namespace, Name: definition.Name, Arguments: tc.Function.Arguments,
					})
					continue
				}
				resp.Output = append(resp.Output, ResponsesItem{
					ID:        "fc_" + randID(),
					Type:      "function_call",
					Status:    "completed",
					CallID:    callID,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}
		}
	}
	return resp, nil
}

// executeUpstream runs the request against a single route with same-model retries capped at 2 attempts.
func (s *server) executeUpstream(
	ctx context.Context,
	or *OpenAIRequest,
	routes []resolvedModel,
	cfg *Config,
	logit func(routeModel string, status, in, out int, effort string),
	w http.ResponseWriter,
) (*http.Response, resolvedModel, error) {
	// Use only the first route - no fallbacks
	if len(routes) == 0 {
		return nil, resolvedModel{}, fmt.Errorf("no routes available")
	}

	activeRoute := routes[0]
	currentRoute := activeRoute.Route
	runtime, runtimeErr := resolveProviderRuntime(ctx, cfg, s.auth, currentRoute.Provider, false)
	if runtimeErr != nil {
		httpErr(w, 500, runtimeErr.Error())
		logit(currentRoute.Model, 500, 0, 0, "")
		return nil, resolvedModel{}, runtimeErr
	}

	requestForRoute, err := requestWithACCPersona(or, currentRoute)
	if err != nil {
		httpErr(w, 500, "prepare request: "+err.Error())
		return nil, resolvedModel{}, err
	}
	requestForRoute.Model = currentRoute.Model
	if currentRoute.Temperature != nil {
		requestForRoute.Temperature = currentRoute.Temperature
	}
	if currentRoute.TopP != nil {
		requestForRoute.TopP = currentRoute.TopP
	}
	requestForRoute.MaxTokens = boundedOutputTokens(or.MaxTokens, currentRoute.MaxTokens)
	requestForRoute.ReasoningEffort = ""
	effortExtra, err := applyReasoningTarget(requestForRoute, activeRoute, requestedReasoningEffort)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		logit(currentRoute.Model, http.StatusBadRequest, 0, 0, "")
		return nil, resolvedModel{}, err
	}

	body, _ := json.Marshal(requestForRoute)
	body = mergeRouteExtraBody(body, currentRoute.ExtraBody)
	body = mergeRouteExtraBody(body, effortExtra)

	// Same-model retries capped at 2 attempts for transient failures
	maxAttempts := 2

	var resp *http.Response
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := s.limiter.Wait(ctx, currentRoute.Provider); err != nil {
			httpErr(w, 504, fmt.Sprintf("rate limiter interrupted for %s/%s: %v", currentRoute.Provider, currentRoute.Model, err))
			logit(currentRoute.Model, 504, 0, 0, requestForRoute.ReasoningEffort)
			return nil, resolvedModel{}, err
		}

		resp, err = doProviderRequestWithBody(ctx, s.http, s.auth, runtime, requestForRoute, body)
		if err != nil {
			if attempt == maxAttempts {
				httpErr(w, 502, "upstream: "+err.Error())
				logit(currentRoute.Model, 502, 0, 0, requestForRoute.ReasoningEffort)
				return nil, resolvedModel{}, err
			}
			log.Printf("upstream connection failed for %s/%s, retrying (%d/%d): %v", currentRoute.Provider, currentRoute.Model, attempt, maxAttempts, err)
			continue
		}

		if resp.StatusCode == 503 && attempt < maxAttempts {
			// Exponential backoff with jitter for 503 errors
			baseInt := 1 << attempt
			base := float64(baseInt)
			// Add 0-50% jitter
			jitter := base * 0.5 * (float64(time.Now().UnixNano()%1000) / 1000.0)
			sleepSecs := base + jitter
			if sleepSecs > 30 {
				sleepSecs = 30
			}
			sleepDuration := time.Duration(sleepSecs * float64(time.Second))

			log.Printf("upstream 503 for model=%s/%s: retrying in %v (attempt %d/%d)", currentRoute.Provider, currentRoute.Model, sleepDuration.Round(100*time.Millisecond), attempt, maxAttempts)
			resp.Body.Close()

			select {
			case <-ctx.Done():
				log.Printf("client disconnected during retry backoff")
				return nil, resolvedModel{}, ctx.Err()
			case <-time.After(sleepDuration):
			}
			continue
		}
		break // got a response (success or non-503 error)
	}

	if resp == nil {
		// This shouldn't happen, but just in case
		httpErr(w, 502, "upstream: no response")
		logit(currentRoute.Model, 502, 0, 0, requestForRoute.ReasoningEffort)
		return nil, resolvedModel{}, fmt.Errorf("upstream: no response")
	}

	// On provider failures, return real upstream error (no fallback)
	shouldFallback := resp.StatusCode == 429 || resp.StatusCode >= 500
	var failureBody []byte
	if !shouldFallback && resp.StatusCode == 400 {
		failureBody, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(failureBody))
		if recoverableProvider400(failureBody) {
			shouldFallback = true
		}
	}
	if shouldFallback {
		status := resp.StatusCode
		b := failureBody
		if b == nil {
			b, _ = io.ReadAll(resp.Body)
		}
		resp.Body.Close()
		// Return real upstream error instead of falling back
		log.Printf("upstream %d on %s/%s: %s", status, currentRoute.Provider, currentRoute.Model, truncate(string(b), 200))
		logit(currentRoute.Model, status, 0, 0, requestForRoute.ReasoningEffort)
		msg := fmt.Sprintf("upstream %s/%s: %s", currentRoute.Provider, currentRoute.Model, truncate(string(b), 300))
		switch {
		case resp.StatusCode == 429:
			msg = fmt.Sprintf("🪫 You're out of free usage on %s right now (rate-limited / quota hit). Wait a bit, or switch to another model.", currentRoute.Model)
		case resp.StatusCode >= 500:
			msg = fmt.Sprintf("⚠️ %s (provider %s) is down right now — server error %d. Try again in a moment or switch models.", currentRoute.Model, currentRoute.Provider, resp.StatusCode)
		}
		httpErr(w, resp.StatusCode, msg)
		return nil, resolvedModel{}, fmt.Errorf("upstream error: %s", msg)
	}

	// Time-to-first-token guard (streaming only): a route that returns 200
	// but emits no token within firstTokenTimeout is treated as stalled.
	if requestForRoute.Stream && resp != nil && resp.StatusCode < 400 {
		reader, timedOut := awaitFirstByte(resp.Body, firstTokenTimeout)
		if timedOut {
			resp.Body.Close()
			resp = nil
			log.Printf("no token from %s/%s within %s", currentRoute.Provider, currentRoute.Model, firstTokenTimeout)
			logit(currentRoute.Model, 504, 0, 0, requestForRoute.ReasoningEffort)
			httpErr(w, 504, fmt.Sprintf("⌛ %s gave no response in time. Try again or switch models.", currentRoute.Model))
			return nil, resolvedModel{}, fmt.Errorf("upstream timeout")
		}
		resp.Body = io.NopCloser(reader)
	}

	return resp, activeRoute, nil
}

// streamTranslateResponses rewrites OpenAI stream chunk payloads to Responses API SSE stream format.
func streamTranslateResponses(w http.ResponseWriter, body io.Reader, model string, translations ...*responseToolTranslation) (int, int) {
	return streamTranslateResponsesWithCompletion(w, body, model, nil, translations...)
}

func streamTranslateResponsesWithCompletion(w http.ResponseWriter, body io.Reader, model string, onCompletion func(*ResponsesResponse), translations ...*responseToolTranslation) (int, int) {
	translation := newResponseToolTranslation()
	if len(translations) > 0 && translations[0] != nil {
		translation = translations[0]
	}
	flusher, _ := w.(http.Flusher)
	sequence := 0
	send := func(event string, data any) {
		if payload, ok := data.(map[string]any); ok {
			payload["sequence_number"] = sequence
			sequence++
		}
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	respID := "resp_" + randID()
	send("response.created", map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id": respID, "object": "response", "status": "in_progress",
			"model": model, "output": []any{},
		},
	})
	send("response.in_progress", map[string]any{
		"type": "response.in_progress",
		"response": map[string]any{
			"id": respID, "object": "response", "status": "in_progress",
			"model": model, "output": []any{},
		},
	})

	var (
		itemID          string
		textOpen        = false
		accumulatedText string
		reasoningOpen   = false
		reasoningID     string
		reasoningText   string
		reasoningIndex  = -1
		nextIndex       = 0
		textIndex       = -1
		toolBlocks      = map[int]string{} // map tc.Index -> toolItemID
		toolIndexMap    = map[int]int{}    // map tc.Index -> outputIndex
		toolCallIDs     = map[int]string{}
		toolNames       = map[int]string{}
		toolArgs        = map[int]string{}
		toolCustom      = map[int]*customToolDefinition{}
		toolNamespace   = map[int]*namespaceToolDefinition{}
		toolOrder       []int
		completedItems  []any
		inputTokens     = 0
		outputTokens    = 0
		sawDone         = false
	)

	ensureReasoningCreated := func() {
		if reasoningOpen {
			return
		}
		reasoningID = "rs_" + randID()
		reasoningIndex = nextIndex
		nextIndex++
		reasoningOpen = true
		send("response.output_item.added", map[string]any{
			"type": "response.output_item.added", "response_id": respID,
			"output_index": reasoningIndex,
			"item":         map[string]any{"id": reasoningID, "type": "reasoning", "status": "in_progress", "summary": []any{}},
		})
	}
	closeReasoning := func() {
		if !reasoningOpen {
			return
		}
		item := map[string]any{
			"id": reasoningID, "type": "reasoning", "status": "completed",
			"summary": []any{map[string]any{"type": "summary_text", "text": reasoningText}},
		}
		send("response.reasoning_summary_text.done", map[string]any{
			"type": "response.reasoning_summary_text.done", "response_id": respID,
			"output_index": reasoningIndex, "item_id": reasoningID, "text": reasoningText,
		})
		send("response.output_item.done", map[string]any{
			"type": "response.output_item.done", "response_id": respID,
			"output_index": reasoningIndex, "item": item,
		})
		completedItems = append(completedItems, item)
		reasoningOpen = false
		reasoningID = ""
		reasoningText = ""
	}

	ensureMessageCreated := func() {
		closeReasoning()
		if !textOpen && itemID == "" {
			itemID = "item_" + randID()
			textIndex = nextIndex
			nextIndex++
			send("response.output_item.added", map[string]any{
				"type":         "response.output_item.added",
				"response_id":  respID,
				"output_index": textIndex,
				"item": map[string]any{
					"id":      itemID,
					"type":    "message",
					"status":  "in_progress",
					"role":    "assistant",
					"content": []any{},
				},
			})
			send("response.content_part.added", map[string]any{
				"type": "response.content_part.added", "response_id": respID,
				"output_index": textIndex, "content_index": 0, "item_id": itemID,
				"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
			})
			textOpen = true
		}
	}

	closeMessage := func() {
		if textOpen && itemID != "" {
			send("response.output_text.done", map[string]any{
				"type":          "response.output_text.done",
				"response_id":   respID,
				"output_index":  textIndex,
				"content_index": 0,
				"item_id":       itemID,
				"text":          accumulatedText,
			})
			send("response.content_part.done", map[string]any{
				"type": "response.content_part.done", "response_id": respID,
				"output_index": textIndex, "content_index": 0, "item_id": itemID,
				"part": map[string]any{"type": "output_text", "text": accumulatedText, "annotations": []any{}},
			})
			messageItem := map[string]any{
				"id": itemID, "type": "message", "status": "completed", "role": "assistant",
				"content": []any{map[string]any{
					"type": "output_text", "text": accumulatedText, "annotations": []any{},
				}},
			}
			send("response.output_item.done", map[string]any{
				"type":         "response.output_item.done",
				"response_id":  respID,
				"output_index": textIndex,
				"item":         messageItem,
			})
			completedItems = append(completedItems, messageItem)
			textOpen = false
			itemID = ""
			accumulatedText = ""
		}
	}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			sawDone = true
			break
		}

		var chunk OpenAIResponse
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			if chunk.Usage.PromptTokens > 0 {
				inputTokens = chunk.Usage.PromptTokens
			}
			if chunk.Usage.CompletionTokens > 0 {
				outputTokens = chunk.Usage.CompletionTokens
			}
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]
		if ch.Delta == nil {
			continue
		}

		// Text delta
		if txt := decodeStringContent(ch.Delta.Content); txt != "" {
			ensureMessageCreated()
			accumulatedText += txt
			send("response.output_text.delta", map[string]any{
				"type":          "response.output_text.delta",
				"response_id":   respID,
				"output_index":  textIndex,
				"content_index": 0,
				"item_id":       itemID,
				"delta":         txt,
			})
		}
		if reasoning := decodeStringContent(ch.Delta.ReasoningContent); reasoning != "" {
			ensureReasoningCreated()
			reasoningText += reasoning
			send("response.reasoning_summary_text.delta", map[string]any{
				"type": "response.reasoning_summary_text.delta", "response_id": respID,
				"output_index": reasoningIndex, "item_id": reasoningID, "delta": reasoning,
			})
		}

		// Tool call deltas
		for _, tc := range ch.Delta.ToolCalls {
			toolItemID, exists := toolBlocks[tc.Index]
			if !exists {
				closeReasoning()
				closeMessage()
				toolItemID = "fc_" + randID()
				toolBlocks[tc.Index] = toolItemID
				toolIndexMap[tc.Index] = nextIndex
				toolOrder = append(toolOrder, tc.Index)
				nextIndex++
				toolCallIDs[tc.Index] = tc.ID
				if tc.Function.Name != "" {
					toolNames[tc.Index] = tc.Function.Name
				}
				if definition, ok := translation.customForBridge(toolNames[tc.Index]); ok {
					copy := definition
					toolCustom[tc.Index] = &copy
				}
				if definition, ok := translation.namespaceForBridge(toolNames[tc.Index]); ok {
					copy := definition
					toolNamespace[tc.Index] = &copy
				}
				itemType := "function_call"
				itemFields := map[string]any{
					"id": toolItemID, "type": itemType, "status": "in_progress",
					"call_id": toolCallIDs[tc.Index], "name": toolNames[tc.Index], "arguments": "",
				}
				if custom := toolCustom[tc.Index]; custom != nil {
					itemType = "custom_tool_call"
					itemFields = map[string]any{
						"id": toolItemID, "type": itemType, "status": "in_progress",
						"call_id": toolCallIDs[tc.Index], "name": custom.Name, "input": "",
					}
				}
				if namespace := toolNamespace[tc.Index]; namespace != nil {
					itemFields["namespace"] = namespace.Namespace
					itemFields["name"] = namespace.Name
				}
				send("response.output_item.added", map[string]any{
					"type":         "response.output_item.added",
					"response_id":  respID,
					"output_index": toolIndexMap[tc.Index],
					"item":         itemFields,
				})
			}
			if tc.ID != "" {
				toolCallIDs[tc.Index] = tc.ID
			}
			if tc.Function.Name != "" {
				toolNames[tc.Index] = tc.Function.Name
				if definition, ok := translation.customForBridge(tc.Function.Name); ok {
					copy := definition
					toolCustom[tc.Index] = &copy
				}
				if definition, ok := translation.namespaceForBridge(tc.Function.Name); ok {
					copy := definition
					toolNamespace[tc.Index] = &copy
				}
			}
			if tc.Function.Arguments != "" {
				toolArgs[tc.Index] += tc.Function.Arguments
				if toolCustom[tc.Index] == nil {
					send("response.function_call_arguments.delta", map[string]any{
						"type":         "response.function_call_arguments.delta",
						"response_id":  respID,
						"output_index": toolIndexMap[tc.Index],
						"item_id":      toolItemID,
						"delta":        tc.Function.Arguments,
					})
				}
			}
		}
	}
	scanErr := scanner.Err()
	if scanErr != nil {
		log.Printf("responses streaming scan: %v", scanErr)
	}

	closeMessage()
	closeReasoning()

	// Done for tools
	for _, idx := range toolOrder {
		toolItemID := toolBlocks[idx]
		if custom := toolCustom[idx]; custom != nil {
			input, err := customToolInput(toolArgs[idx])
			if err != nil {
				log.Printf("responses custom stream restore for %s: %v", custom.Name, err)
				input = ""
			}
			send("response.custom_tool_call_input.delta", map[string]any{
				"type":         "response.custom_tool_call_input.delta",
				"response_id":  respID,
				"output_index": toolIndexMap[idx],
				"item_id":      toolItemID,
				"delta":        input,
			})
			send("response.custom_tool_call_input.done", map[string]any{
				"type":         "response.custom_tool_call_input.done",
				"response_id":  respID,
				"output_index": toolIndexMap[idx],
				"item_id":      toolItemID,
				"name":         custom.Name,
				"input":        input,
			})
			toolItem := map[string]any{
				"id": toolItemID, "type": "custom_tool_call", "status": "completed",
				"call_id": toolCallIDs[idx], "name": custom.Name, "input": input,
			}
			send("response.output_item.done", map[string]any{
				"type":         "response.output_item.done",
				"response_id":  respID,
				"output_index": toolIndexMap[idx],
				"item":         toolItem,
			})
			completedItems = append(completedItems, toolItem)
			continue
		}
		name := toolNames[idx]
		namespace := ""
		if definition := toolNamespace[idx]; definition != nil {
			name = definition.Name
			namespace = definition.Namespace
		}
		argumentsDone := map[string]any{
			"type":         "response.function_call_arguments.done",
			"response_id":  respID,
			"output_index": toolIndexMap[idx],
			"item_id":      toolItemID,
			"name":         name,
			"arguments":    toolArgs[idx],
		}
		toolItem := map[string]any{
			"id": toolItemID, "type": "function_call", "status": "completed",
			"call_id": toolCallIDs[idx], "name": name, "arguments": toolArgs[idx],
		}
		if namespace != "" {
			argumentsDone["namespace"] = namespace
			toolItem["namespace"] = namespace
		}
		send("response.function_call_arguments.done", argumentsDone)
		send("response.output_item.done", map[string]any{
			"type":         "response.output_item.done",
			"response_id":  respID,
			"output_index": toolIndexMap[idx],
			"item":         toolItem,
		})
		completedItems = append(completedItems, toolItem)
	}

	status := "completed"
	event := "response.completed"
	if scanErr != nil || !sawDone {
		status = "incomplete"
		event = "response.incomplete"
	}
	completedResponse := &ResponsesResponse{
		ID: "", Object: "response", CreatedAt: time.Now().Unix(), Model: model,
		Status: status, Output: make([]ResponsesItem, 0),
		Usage: &ResponsesUsage{InputTokens: inputTokens, OutputTokens: outputTokens, TotalTokens: inputTokens + outputTokens},
	}
	responseOutput, _ := json.Marshal(completedItems)
	_ = json.Unmarshal(responseOutput, &completedResponse.Output)
	if status == "incomplete" {
		completedResponse.IncompleteDetails = map[string]any{"reason": "upstream_stream_ended"}
	}
	send(event, map[string]any{
		"type": event,
		"response": map[string]any{
			"id": respID, "object": "response", "model": model,
			"status": status, "output": completedItems,
			"usage": map[string]any{
				"input_tokens": inputTokens, "output_tokens": outputTokens,
				"total_tokens": inputTokens + outputTokens,
			},
		},
	})
	completedResponse.ID = respID
	if onCompletion != nil {
		onCompletion(completedResponse)
	}

	return inputTokens, outputTokens
}

// handleResponses implements the Responses API door HTTP endpoint.
func (s *server) handleResponses(w http.ResponseWriter, r *http.Request) {
	s.reloadIfChanged()
	cfg := s.cfg.Load()
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		httpErr(w, 400, "read body: "+err.Error())
		return
	}

	var req ResponsesRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		httpErr(w, 400, "parse request: "+err.Error())
		return
	}

	logit := func(routeModel string, status, in, out int, effort string) {
		AddTUILog(LogEntry{
			Timestamp: time.Now(),
			Model:     req.Model,
			Route:     routeModel,
			Status:    status,
			TokensIn:  in,
			TokensOut: out,
			Budget:    0,
			Effort:    effort,
			CostUSD:   costFor(routeModel, in, out, cfg),
		})
	}
	if err := s.applyPreviousResponse(&req); err != nil {
		httpErr(w, http.StatusNotFound, err.Error())
		logit("error", http.StatusNotFound, 0, 0, "")
		return
	}
	requestForSizing, _ := json.Marshal(req)

	routes, err := s.responseModelChain(req.Model)
	if err != nil {
		httpErr(w, 400, err.Error())
		logit("error", 400, 0, 0, "")
		return
	}
	// Codex resolves exactly one provider/model per request. Reject any chain
	// that includes fallback or image-fallback entries.
	for _, r := range routes[1:] {
		if r.Fallback || r.ImageOnly {
			httpErr(w, 400, fmt.Sprintf("Codex model %q must not configure fallback chains", req.Model))
			logit("error", 400, 0, 0, "")
			return
		}
	}
	routes, err = selectResponseModelChainForInput(&req, routes, estimateResponsesInputTokens(requestForSizing))
	if err != nil {
		httpErr(w, 400, err.Error())
		logit("error", 400, 0, 0, "")
		return
	}

	primary := routes[0]
	if err := validateResponseCapabilities(&req, primary); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		logit(primary.Route.Model, http.StatusBadRequest, 0, 0, "")
		return
	}
	requestedEffort := ""
	if req.Reasoning != nil {
		requestedEffort = req.Reasoning.Effort
	}
	if err := validateRequestedEffort(primary, requestedEffort); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		logit(primary.Route.Model, http.StatusBadRequest, 0, 0, "")
		return
	}

	or, translation, err := translateFromResponsesWithTools(&req, primary.Route, cfg)
	if err != nil {
		httpErr(w, 400, "translate: "+err.Error())
		logit(primary.Route.Model, 400, 0, 0, "")
		return
	}
	// executeUpstream applies each concrete route's overrides. Restore the
	// client controls here so a fallback cannot inherit primary-only settings.
	or.Temperature = req.Temperature
	or.TopP = req.TopP
	or.MaxTokens = req.MaxOutputTokens
	if or.MaxTokens == 0 {
		or.MaxTokens = req.MaxTokens
	}

	resp, activeRoute, err := s.executeUpstream(r.Context(), or, routes, cfg, logit, w)
	if err != nil {
		return
	}
	setBackendHeaders(w, req.Model, activeRoute, requestedEffort)
	log.Printf("responses: model=%s -> %s/%s effort=%s fallback=%t", req.Model, activeRoute.Route.Provider, activeRoute.Route.Model, backendEffort(activeRoute, requestedEffort), activeRoute.Fallback)
	defer func() {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
	}()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		log.Printf("upstream %d for model=%s->%s/%s: %s", resp.StatusCode, req.Model, activeRoute.Route.Provider, activeRoute.Route.Model, truncate(string(b), 500))
		msg := fmt.Sprintf("upstream %s/%s: %s", activeRoute.Route.Provider, activeRoute.Route.Model, truncate(string(b), 300))
		switch {
		case resp.StatusCode == 429:
			msg = fmt.Sprintf("You're out of free usage on %s right now", activeRoute.Route.Model)
		case resp.StatusCode >= 500:
			msg = fmt.Sprintf("%s (provider %s) returned server error %d", activeRoute.Route.Model, activeRoute.Route.Provider, resp.StatusCode)
		}
		httpErr(w, resp.StatusCode, msg)
		logit(activeRoute.Route.Model, resp.StatusCode, 0, 0, or.ReasoningEffort)
		return
	}

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		inTokens, outTokens := streamTranslateResponsesWithCompletion(w, resp.Body, req.Model, func(response *ResponsesResponse) {
			if responsesShouldStore(&req) {
				s.rememberResponse(response)
			}
		}, translation)
		logit(activeRoute.Route.Model, resp.StatusCode, inTokens, outTokens, or.ReasoningEffort)
		return
	}

	var oresp OpenAIResponse
	b, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(b, &oresp); err != nil {
		httpErr(w, 502, "parse upstream: "+err.Error())
		logit(activeRoute.Route.Model, 502, 0, 0, or.ReasoningEffort)
		return
	}

	out, err := translateToResponsesWithTools(&oresp, req.Model, translation)
	if err != nil {
		httpErr(w, 502, "translate upstream response: "+err.Error())
		logit(activeRoute.Route.Model, 502, 0, 0, or.ReasoningEffort)
		return
	}
	if responsesShouldStore(&req) {
		s.rememberResponse(out)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)

	tokensIn, tokensOut := 0, 0
	if oresp.Usage != nil {
		tokensIn = oresp.Usage.PromptTokens
		tokensOut = oresp.Usage.CompletionTokens
	}
	logit(activeRoute.Route.Model, resp.StatusCode, tokensIn, tokensOut, or.ReasoningEffort)
}

func responsesShouldStore(req *ResponsesRequest) bool {
	return req == nil || req.Store == nil || *req.Store
}

// estimateResponsesInputTokens uses three payload bytes per token. This keeps a
// safety margin for dense JSON, code, and three-byte UTF-8 text without the
// false 2x overcount that rejected valid compacted Codex threads.
func estimateResponsesInputTokens(raw []byte) int {
	return (len(raw) + 2) / 3
}

// boundedOutputTokens preserves a client's smaller output request while still
// enforcing the concrete route's maximum. A zero means "use the other value".
func boundedOutputTokens(requested, routeLimit int) int {
	switch {
	case requested <= 0:
		return routeLimit
	case routeLimit <= 0 || requested <= routeLimit:
		return requested
	default:
		return routeLimit
	}
}

func setBackendHeaders(w http.ResponseWriter, requestedModel string, active resolvedModel, requestedEffort string) {
	w.Header().Set("X-ACC-Requested-Model", requestedModel)
	w.Header().Set("X-ACC-Backend-Provider", active.Route.Provider)
	w.Header().Set("X-ACC-Backend-Model", active.Route.Model)
	w.Header().Set("X-ACC-Fallback", fmt.Sprintf("%t", active.Fallback))
	w.Header().Set("X-ACC-Capability-Reroute", fmt.Sprintf("%t", active.CapabilityReroute))
	if requestedEffort != "" {
		w.Header().Set("X-ACC-Requested-Effort", requestedEffort)
	}
	w.Header().Set("X-ACC-Backend-Effort", backendEffort(active, requestedEffort))
}

func backendEffort(active resolvedModel, requested string) string {
	actual := active.Route.ReasoningEffort
	if requested != "" {
		actual = requested
		if active.Capability.DisplayName != "" {
			actual = active.Capability.Reasoning[requested].Effort
		}
	}
	if actual == "" {
		return "none"
	}
	return actual
}
