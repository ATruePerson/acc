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
	or := &OpenAIRequest{
		Model:       route.Model,
		MaxTokens:   req.MaxTokens,
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

	// Translate tools
	for _, t := range req.Tools {
		or.Tools = append(or.Tools, OpenAITool{
			Type: "function",
			Function: OpenAIFunction{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			},
		})
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
					or.Messages = append(or.Messages, OpenAIMessage{
						Role:    item.Role,
						Content: item.Content,
					})
				case "function_call":
					id := item.ID
					var thoughtSig string
					if parts := strings.SplitN(item.ID, "__thought__", 2); len(parts) == 2 {
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

// translateToResponses converts a non-streaming OpenAI response back to Responses API format.
func translateToResponses(or *OpenAIResponse, model string) *ResponsesResponse {
	resp := &ResponsesResponse{
		ID:        "resp_" + randID(),
		CreatedAt: time.Now().Unix(),
		Model:     model,
		Output:    []ResponsesItem{},
	}
	if or.Usage != nil {
		resp.Usage = or.Usage
	}
	if len(or.Choices) > 0 {
		ch := or.Choices[0]
		if ch.Message != nil {
			// If there's content, add message item
			txt := decodeStringContent(ch.Message.Content)
			if txt != "" {
				resp.Output = append(resp.Output, ResponsesItem{
					ID:      "item_" + randID(),
					Type:    "message",
					Role:    "assistant",
					Content: ch.Message.Content,
				})
			} else if len(ch.Message.Content) > 0 && string(ch.Message.Content) != "null" {
				resp.Output = append(resp.Output, ResponsesItem{
					ID:      "item_" + randID(),
					Type:    "message",
					Role:    "assistant",
					Content: ch.Message.Content,
				})
			}
			// If there are tool calls, add function_call items
			for _, tc := range ch.Message.ToolCalls {
				resp.Output = append(resp.Output, ResponsesItem{
					ID:        tc.ID,
					Type:      "function_call",
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

		// On 429 or 5xx, try next fallback
		if (resp.StatusCode == 429 || resp.StatusCode >= 500) && ri < len(routes)-1 {
			status := resp.StatusCode
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			resp = nil
			log.Printf("upstream %d on %s/%s, falling back: %s", status, currentRoute.Provider, currentRoute.Model, truncate(string(b), 200))
			logit(currentRoute.Model, status, 0, 0, or.ReasoningEffort)
			continue
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
	send := func(event string, data any) {
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
			"id":     respID,
			"model":  model,
			"output": []any{},
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
		toolNames       = map[int]string{}
		toolArgs        = map[int]string{}
		inputTokens     = 0
		outputTokens    = 0
	)

	ensureMessageCreated := func() {
		if !textOpen && itemID == "" {
			itemID = "item_" + randID()
			textIndex = nextIndex
			nextIndex++
			send("response.output_item.created", map[string]any{
				"type":         "response.output_item.created",
				"response_id":  respID,
				"output_index": textIndex,
				"item": map[string]any{
					"id":      itemID,
					"type":    "message",
					"role":    "assistant",
					"content": []any{},
				},
			})
			textOpen = true
		}
	}

	closeMessage := func() {
		if textOpen && itemID != "" {
			send("response.output_text.done", map[string]any{
				"type":         "response.output_text.done",
				"response_id":  respID,
				"output_index": textIndex,
				"item_id":      itemID,
				"text":         accumulatedText,
			})
			send("response.output_item.done", map[string]any{
				"type":         "response.output_item.done",
				"response_id":  respID,
				"output_index": textIndex,
				"item": map[string]any{
					"id":   itemID,
					"type": "message",
					"role": "assistant",
					"content": []any{
						map[string]any{
							"type": "text",
							"text": accumulatedText,
						},
					},
				},
			})
			textOpen = false
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
				"type":         "response.output_text.delta",
				"response_id":  respID,
				"output_index": textIndex,
				"item_id":      itemID,
				"delta":        txt,
			})
		}

		// Tool call deltas
		for _, tc := range ch.Delta.ToolCalls {
			toolItemID, exists := toolBlocks[tc.Index]
			if !exists {
				closeMessage()
				toolItemID = "item_" + randID()
				toolBlocks[tc.Index] = toolItemID
				toolIndexMap[tc.Index] = nextIndex
				nextIndex++
				if tc.Function.Name != "" {
					toolNames[tc.Index] = tc.Function.Name
				}
				send("response.output_item.created", map[string]any{
					"type":         "response.output_item.created",
					"response_id":  respID,
					"output_index": toolIndexMap[tc.Index],
					"item": map[string]any{
						"id":        toolItemID,
						"type":      "function_call",
						"name":      toolNames[tc.Index],
						"arguments": "",
					},
				})
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

	closeMessage()

	// Done for tools
	for idx, toolItemID := range toolBlocks {
		send("response.function_call_arguments.done", map[string]any{
			"type":         "response.function_call_arguments.done",
			"response_id":  respID,
			"output_index": toolIndexMap[idx],
			"item_id":      toolItemID,
			"name":         toolNames[idx],
			"arguments":    toolArgs[idx],
		})
		send("response.output_item.done", map[string]any{
			"type":         "response.output_item.done",
			"response_id":  respID,
			"output_index": toolIndexMap[idx],
			"item": map[string]any{
				"id":        toolItemID,
				"type":      "function_call",
				"name":      toolNames[idx],
				"arguments": toolArgs[idx],
			},
		})
	}

	send("response.done", map[string]any{
		"type": "response.done",
		"response": map[string]any{
			"id":     respID,
			"model":  model,
			"status": "completed",
			"usage": map[string]any{
				"prompt_tokens":     inputTokens,
				"completion_tokens": outputTokens,
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
