package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamTextTranslation(t *testing.T) {
	openaiSSE := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Hel"}}]}`,
		`data: {"choices":[{"delta":{"content":"lo"}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"completion_tokens":2}}`,
		`data: [DONE]`,
	}, "\n\n")

	w := httptest.NewRecorder()
	_, _ = streamTranslate(w, strings.NewReader(openaiSSE), "claude-opus-4-8")
	out := w.Body.String()

	for _, want := range []string{
		"event: message_start",
		"event: content_block_start",
		`"text":"Hel","type":"text_delta"`,
		`"text":"lo","type":"text_delta"`,
		"event: content_block_stop",
		`"stop_reason":"end_turn"`,
		"event: message_stop",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in stream output:\n%s", want, out)
		}
	}
}
