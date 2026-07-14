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
	maxTokens := req.MaxOutputTokens
	if maxTokens == 0 {
		maxTokens = req.MaxTokens
	}
	or := &OpenAIRequest{
		Model:       route.Model,
		MaxTokens:   maxTokens,
		Stream:      req.Stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
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
	if route.SystemPrepend != "" {
		prepend = route.SystemPrepend
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
	if or.ReasoningEffort != "" {
		or.ReasoningEffort = sanitizeReasoningEffort(route.Provider, or.ReasoningEffort)
	}

	if req.Stream {
		or.StreamOptions = &StreamOptions{IncludeUsage: true}
	}

	// Codex sends Responses tools in the flat form. Keep accepting the older
	// nested function form so existing clients do not break.
	if route.Toolcalling == nil || *route.Toolcalling {
		for _, t := range req.Tools {
			fn := t.Function
			strict := t.Strict
			if t.Name != "" {
				fn = ResponsesFunction{Name: t.Name, Description: t.Description, Parameters: t.Parameters}
			}
			if fn.Name == "" {
				continue
			}
			or.Tools = append(or.Tools, OpenAITool{
				Type: "function",
				Function: OpenAIFunction{
					Name:        fn.Name,
					Description: fn.Description,
					Parameters:  fn.Parameters,
					Strict:      strict,
				},
			})
		}
	}

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
				return nil, fmt.Errorf("bad input: %w", err)
			}

			for _, item := range items {
				switch item.Type {
				case "message":
					content, err := responsesContentToChat(item.Content)
					if err != nil {
						return nil, fmt.Errorf("bad message content: %w", err)
					}
					or.Messages = append(or.Messages, OpenAIMessage{
						Role:    item.Role,
						Content: content,
					})
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
					// Add this function call to the last assistant message, if any
					toolCall := OpenAIToolCall{
						ID:   id,
						Type: "function",
						Function: OpenAIFuncCall{
							Name:             item.Name,
							Arguments:        item.Arguments,
							ThoughtSignature: thoughtSig,
						},
					}
					// Look for the last assistant message
					var lastAssistant *OpenAIMessage
					for idx := len(or.Messages) - 1; idx >= 0; idx-- {
						if or.Messages[idx].Role == "assistant" {
							lastAssistant = &or.Messages[idx]
							break
						}
					}
					if lastAssistant != nil {
						lastAssistant.ToolCalls = append(lastAssistant.ToolCalls, toolCall)
					} else {
						// Create assistant message
						or.Messages = append(or.Messages, OpenAIMessage{
							Role:      "assistant",
							ToolCalls: []OpenAIToolCall{toolCall},
						})
					}
				case "function_call_output":
					id := item.CallID
					if parts := strings.SplitN(item.CallID, "__thought__", 2); len(parts) == 2 {
						id = parts[0]
					}
					// Translate to a message with role="tool"
					or.Messages = append(or.Messages, OpenAIMessage{
						Role:       "tool",
						ToolCallID: id,
						Content:    jsonString(item.Output),
					})
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

	return or, nil
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
			var url string
			if err := json.Unmarshal(part.ImageURL, &url); err != nil {
				var image OpenAIImageURL
				if json.Unmarshal(part.ImageURL, &image) == nil {
					url = image.URL
				}
			}
			if url != "" {
				out = append(out, OpenAIContentPart{Type: "image_url", ImageURL: &OpenAIImageURL{URL: url}})
			}
		default:
			if part.Text != "" {
				out = append(out, OpenAIContentPart{Type: "text", Text: part.Text})
			}
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
			// If there are tool calls, add function_call items
			for _, tc := range ch.Message.ToolCalls {
				callID := tc.ID
				thoughtSig := tc.Function.ThoughtSignature
				if tc.ExtraContent != nil && tc.ExtraContent.Google != nil && tc.ExtraContent.Google.ThoughtSignature != "" {
					thoughtSig = tc.ExtraContent.Google.ThoughtSignature
				}
				if thoughtSig != "" {
					callID += "__thought__" + thoughtSig
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
	return resp
}

// executeUpstream runs the request retry/fallback loop against upstreams.
func (s *server) executeUpstream(
	ctx context.Context,
	or *OpenAIRequest,
	routes []Route,
	cfg *Config,
	logit func(routeModel string, status, in, out int, effort string),
	w http.ResponseWriter,
) (*http.Response, Route, error) {
	var (
		resp        *http.Response
		activeRoute Route
	)

	for ri, currentRoute := range routes {
		activeRoute = currentRoute
		prov, ok := cfg.Providers[currentRoute.Provider]
		if !ok {
			if ri == len(routes)-1 {
				httpErr(w, 500, "unknown provider: "+currentRoute.Provider)
				logit(currentRoute.Model, 500, 0, 0, "")
				return nil, Route{}, fmt.Errorf("unknown provider: %s", currentRoute.Provider)
			}
			log.Printf("unknown provider %q for route %d, trying fallback", currentRoute.Provider, ri)
			continue
		}

		// Update request with current route details
		or.Model = currentRoute.Model
		if or.ReasoningEffort != "" {
			or.ReasoningEffort = sanitizeReasoningEffort(currentRoute.Provider, or.ReasoningEffort)
		} else if currentRoute.ReasoningEffort != "" {
			or.ReasoningEffort = sanitizeReasoningEffort(currentRoute.Provider, currentRoute.ReasoningEffort)
		}

		body, _ := json.Marshal(or)

		for attempt := 1; attempt <= 10; attempt++ {
			upstream, err := http.NewRequestWithContext(ctx, "POST", prov.BaseURL+"/chat/completions", bytes.NewReader(body))
			if err != nil {
				httpErr(w, 500, err.Error())
				logit(currentRoute.Model, 500, 0, 0, or.ReasoningEffort)
				return nil, Route{}, err
			}
			upstream.Header.Set("Content-Type", "application/json")
			upstream.Header.Set("Authorization", "Bearer "+prov.APIKey)

			if err := s.limiter.Wait(ctx, currentRoute.Provider); err != nil {
				httpErr(w, 504, fmt.Sprintf("rate limiter interrupted for %s/%s: %v", currentRoute.Provider, currentRoute.Model, err))
				logit(currentRoute.Model, 504, 0, 0, or.ReasoningEffort)
				return nil, Route{}, err
			}

			resp, err = s.http.Do(upstream)
			if err != nil {
				if ri == len(routes)-1 {
					httpErr(w, 502, "upstream: "+err.Error())
					logit(currentRoute.Model, 502, 0, 0, or.ReasoningEffort)
					return nil, Route{}, err
				}
				log.Printf("upstream connection failed for %s/%s, trying fallback: %v", currentRoute.Provider, currentRoute.Model, err)
				break
			}

			if resp.StatusCode == 503 && attempt < 10 {
				// Exponential backoff with jitter
				baseInt := 1 << attempt
				base := float64(baseInt)
				jitter := base * 0.5 * (float64(time.Now().UnixNano()%1000) / 1000.0)
				sleepSecs := base + jitter
				if sleepSecs > 30 {
					sleepSecs = 30
				}
				sleepDuration := time.Duration(sleepSecs * float64(time.Second))

				log.Printf("upstream 503 for model=%s/%s: retrying in %v (attempt %d/10)", currentRoute.Provider, currentRoute.Model, sleepDuration.Round(100*time.Millisecond), attempt)
				resp.Body.Close()

				select {
				case <-ctx.Done():
					log.Printf("client disconnected during retry backoff")
					return nil, Route{}, ctx.Err()
				case <-time.After(sleepDuration):
				}
				continue
			}
			break
		}

		if resp == nil {
			continue
		}

		// On 429 or 5xx, try next fallback. Also treat a NVIDIA NIM "DEGRADED" 400
		// (model node disabled upstream, not a bad request) as failover-worthy.
		shouldFallback := resp.StatusCode == 429 || resp.StatusCode >= 500
		var degradedBody []byte
		if !shouldFallback && resp.StatusCode == 400 {
			degradedBody, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(degradedBody))
			if bytes.Contains(degradedBody, []byte("DEGRADED")) || bytes.Contains(degradedBody, []byte("cannot be invoked")) {
				shouldFallback = true
			}
		}
		if shouldFallback && ri < len(routes)-1 {
			status := resp.StatusCode
			b := degradedBody
			if b == nil {
				b, _ = io.ReadAll(resp.Body)
			}
			resp.Body.Close()
			resp = nil
			log.Printf("upstream %d on %s/%s, falling back: %s", status, currentRoute.Provider, currentRoute.Model, truncate(string(b), 200))
			logit(currentRoute.Model, status, 0, 0, or.ReasoningEffort)
			continue
		}

		// Time-to-first-token guard (streaming only): a route that returns 200
		// but emits no token within firstTokenTimeout is treated as stalled.
		// Fall back if a route remains, otherwise fail — never hang.
		if or.Stream && resp != nil && resp.StatusCode < 400 {
			reader, timedOut := awaitFirstByte(resp.Body, firstTokenTimeout)
			if timedOut {
				resp.Body.Close()
				resp = nil
				log.Printf("no token from %s/%s within %s", currentRoute.Provider, currentRoute.Model, firstTokenTimeout)
				logit(currentRoute.Model, 504, 0, 0, or.ReasoningEffort)
				if ri < len(routes)-1 {
					continue
				}
				httpErr(w, 504, fmt.Sprintf("⌛ %s and its fallback gave no response in time. Try again or switch models.", or.Model))
				return nil, Route{}, fmt.Errorf("timeout on all routes")
			}
			resp.Body = io.NopCloser(reader)
		}

		break
	}

	if resp == nil {
		return nil, Route{}, fmt.Errorf("all routes failed")
	}

	return resp, activeRoute, nil
}

// streamTranslateResponses rewrites OpenAI stream chunk payloads to Responses API SSE stream format.
func streamTranslateResponses(w http.ResponseWriter, body io.Reader, model string) (int, int) {
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
		nextIndex       = 0
		textIndex       = -1
		toolBlocks      = map[int]string{} // map tc.Index -> toolItemID
		toolIndexMap    = map[int]int{}    // map tc.Index -> outputIndex
		toolCallIDs     = map[int]string{}
		toolNames       = map[int]string{}
		toolArgs        = map[int]string{}
		toolOrder       []int
		completedItems  []any
		inputTokens     = 0
		outputTokens    = 0
	)

	ensureMessageCreated := func() {
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

		// Tool call deltas
		for _, tc := range ch.Delta.ToolCalls {
			toolItemID, exists := toolBlocks[tc.Index]
			if !exists {
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
				send("response.output_item.added", map[string]any{
					"type":         "response.output_item.added",
					"response_id":  respID,
					"output_index": toolIndexMap[tc.Index],
					"item": map[string]any{
						"id":        toolItemID,
						"type":      "function_call",
						"status":    "in_progress",
						"call_id":   toolCallIDs[tc.Index],
						"name":      toolNames[tc.Index],
						"arguments": "",
					},
				})
			}
			if tc.ID != "" {
				toolCallIDs[tc.Index] = tc.ID
			}
			if tc.Function.Name != "" {
				toolNames[tc.Index] = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				toolArgs[tc.Index] += tc.Function.Arguments
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
	if err := scanner.Err(); err != nil {
		log.Printf("responses streaming scan: %v", err)
	}

	closeMessage()

	// Done for tools
	for _, idx := range toolOrder {
		toolItemID := toolBlocks[idx]
		send("response.function_call_arguments.done", map[string]any{
			"type":         "response.function_call_arguments.done",
			"response_id":  respID,
			"output_index": toolIndexMap[idx],
			"item_id":      toolItemID,
			"name":         toolNames[idx],
			"arguments":    toolArgs[idx],
		})
		toolItem := map[string]any{
			"id": toolItemID, "type": "function_call", "status": "completed",
			"call_id": toolCallIDs[idx], "name": toolNames[idx], "arguments": toolArgs[idx],
		}
		send("response.output_item.done", map[string]any{
			"type":         "response.output_item.done",
			"response_id":  respID,
			"output_index": toolIndexMap[idx],
			"item":         toolItem,
		})
		completedItems = append(completedItems, toolItem)
	}

	send("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": respID, "object": "response", "model": model,
			"status": "completed", "output": completedItems,
			"usage": map[string]any{
				"input_tokens": inputTokens, "output_tokens": outputTokens,
				"total_tokens": inputTokens + outputTokens,
			},
		},
	})

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

	route, err := s.routeFor(req.Model)
	if err != nil {
		httpErr(w, 400, err.Error())
		logit("error", 400, 0, 0, "")
		return
	}

	routes := append([]Route{route}, route.Fallbacks...)

	or, err := translateFromResponses(&req, route, cfg)
	if err != nil {
		httpErr(w, 400, "translate: "+err.Error())
		logit(route.Model, 400, 0, 0, "")
		return
	}

	resp, activeRoute, err := s.executeUpstream(r.Context(), or, routes, cfg, logit, w)
	if err != nil {
		return
	}
	defer func() {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
	}()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		log.Printf("upstream %d for model=%s->%s/%s: %s", resp.StatusCode, req.Model, activeRoute.Provider, activeRoute.Model, truncate(string(b), 500))
		msg := fmt.Sprintf("upstream %s/%s: %s", activeRoute.Provider, activeRoute.Model, truncate(string(b), 300))
		switch {
		case resp.StatusCode == 429:
			msg = fmt.Sprintf("🪫 You're out of free usage on %s right now (rate-limited / quota hit). Wait a bit, or switch to another model.", activeRoute.Model)
		case resp.StatusCode >= 500:
			msg = fmt.Sprintf("⚠️ %s (provider %s) is down right now — server error %d. Try again in a moment or switch models.", activeRoute.Model, activeRoute.Provider, resp.StatusCode)
		}
		httpErr(w, resp.StatusCode, msg)
		logit(activeRoute.Model, resp.StatusCode, 0, 0, or.ReasoningEffort)
		return
	}

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		inTokens, outTokens := streamTranslateResponses(w, resp.Body, req.Model)
		logit(activeRoute.Model, resp.StatusCode, inTokens, outTokens, or.ReasoningEffort)
		return
	}

	var oresp OpenAIResponse
	b, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(b, &oresp); err != nil {
		httpErr(w, 502, "parse upstream: "+err.Error())
		logit(activeRoute.Model, 502, 0, 0, or.ReasoningEffort)
		return
	}

	out := translateToResponses(&oresp, req.Model)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)

	tokensIn, tokensOut := 0, 0
	if oresp.Usage != nil {
		tokensIn = oresp.Usage.PromptTokens
		tokensOut = oresp.Usage.CompletionTokens
	}
	logit(activeRoute.Model, resp.StatusCode, tokensIn, tokensOut, or.ReasoningEffort)
}
