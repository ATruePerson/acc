package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// firstTokenTimeout is how long a route has to emit its first response byte
// before the proxy abandons it and tries the next fallback. A reasoning model
// that goes silent (stalled / overloaded) trips this; a model that streams
// promptly does not. Package var so tests can shorten it.
var firstTokenTimeout = 15 * time.Second

// awaitFirstByte blocks until the first byte is readable from src or d passes.
// On success it returns a reader that re-emits that first byte followed by the
// rest of src, so no streamed data is lost. On timeout it returns (nil, true);
// the caller must Close the underlying body to unblock the pending read.
func awaitFirstByte(src io.Reader, d time.Duration) (io.Reader, bool) {
	type res struct {
		n   int
		err error
	}
	buf := make([]byte, 1)
	ch := make(chan res, 1) // buffered so the goroutine never leaks on timeout
	go func() {
		n, err := io.ReadFull(src, buf)
		ch <- res{n, err}
	}()

	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case got := <-ch:
		if got.n == 0 {
			return src, false // EOF before any byte; let the caller drain it
		}
		return io.MultiReader(bytes.NewReader(buf[:got.n]), src), false
	case <-timer.C:
		return nil, true
	}
}

// streamTranslate reads an OpenAI SSE stream and rewrites it as an
// Anthropic SSE stream onto w, flushing each event so Claude Code renders
// token-by-token (the "native feel").
func streamTranslate(w http.ResponseWriter, body io.Reader, model string) (int, int, int) {
	flusher, _ := w.(http.Flusher)
	send := func(event string, data map[string]any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	msgID := "msg_" + randID()
	send("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": msgID, "type": "message", "role": "assistant",
			"model": model, "content": []any{},
			"stop_reason": nil,
			"usage":       map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})

	textOpen := false
	nextIndex := 0
	textIndex := -1
	// map openai tool_call index -> anthropic block index
	toolBlocks := map[int]int{}
	stopReason := "end_turn"
	inputTokens := 0
	outputTokens := 0
	reasoningTokens := 0

	closeText := func() {
		if textOpen {
			send("content_block_stop", map[string]any{"type": "content_block_stop", "index": textIndex})
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
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil && len(chunk.Choices[0].Delta.ToolCalls) > 0 {
			log.Printf("stream chunk tool_calls: %s", payload)
		}
		if chunk.Usage != nil {
			if chunk.Usage.PromptTokens > 0 {
				inputTokens = chunk.Usage.PromptTokens
			}
			if chunk.Usage.CompletionTokens > 0 {
				outputTokens = chunk.Usage.CompletionTokens
			}
			if r := chunk.Usage.reasoningTokens(); r > 0 {
				reasoningTokens = r
			}
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]
		switch ch.FinishReason {
		case "tool_calls":
			stopReason = "tool_use"
		case "length":
			stopReason = "max_tokens"
		}
		if ch.Delta == nil {
			continue
		}

		// text delta
		if txt := decodeStringContent(ch.Delta.Content); txt != "" {
			if !textOpen {
				textIndex = nextIndex
				nextIndex++
				send("content_block_start", map[string]any{
					"type": "content_block_start", "index": textIndex,
					"content_block": map[string]any{"type": "text", "text": ""},
				})
				textOpen = true
			}
			send("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": textIndex,
				"delta": map[string]any{"type": "text_delta", "text": txt},
			})
		}

		// tool call deltas
		for _, tc := range ch.Delta.ToolCalls {
			bi, ok := toolBlocks[tc.Index]
			if !ok {
				closeText()
				bi = nextIndex
				nextIndex++
				toolBlocks[tc.Index] = bi
				thoughtSig := tc.Function.ThoughtSignature
				if tc.ExtraContent != nil && tc.ExtraContent.Google != nil && tc.ExtraContent.Google.ThoughtSignature != "" {
					thoughtSig = tc.ExtraContent.Google.ThoughtSignature
				}
				id := tc.ID
				if thoughtSig != "" {
					id = fmt.Sprintf("%s__thought__%s", tc.ID, thoughtSig)
				}
				send("content_block_start", map[string]any{
					"type": "content_block_start", "index": bi,
					"content_block": map[string]any{
						"type": "tool_use", "id": id, "name": tc.Function.Name,
						"input": map[string]any{},
					},
				})
			}
			if tc.Function.Arguments != "" {
				send("content_block_delta", map[string]any{
					"type": "content_block_delta", "index": bi,
					"delta": map[string]any{"type": "input_json_delta", "partial_json": tc.Function.Arguments},
				})
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("stream scan error: %v", err)
	}

	closeText()
	for _, bi := range toolBlocks {
		send("content_block_stop", map[string]any{"type": "content_block_stop", "index": bi})
	}
	send("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil},
		"usage": map[string]any{"input_tokens": inputTokens, "output_tokens": outputTokens},
	})
	send("message_stop", map[string]any{"type": "message_stop"})
	return inputTokens, outputTokens, reasoningTokens
}
