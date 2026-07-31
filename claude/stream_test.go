package claude

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

type errReader struct {
	err error
}

func (r *errReader) Read(p []byte) (int, error) {
	return 0, r.err
}

func TestStreamTextTranslation(t *testing.T) {
	openaiSSE := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Hel"}}]}`,
		`data: {"choices":[{"delta":{"content":"lo"}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"completion_tokens":2}}`,
		`data: [DONE]`,
	}, "\n\n")

	w := httptest.NewRecorder()
	_, _, _ = streamTranslate(w, strings.NewReader(openaiSSE), "claude-opus-4-8")
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

func TestStreamTranslateEmptyBodyEmitsNoTerminalEvents(t *testing.T) {
	w := httptest.NewRecorder()
	_, _, _ = streamTranslate(w, strings.NewReader(""), "test-model")
	out := w.Body.String()

	if !strings.Contains(out, "event: message_start") {
		t.Fatal("expected message_start even on empty body")
	}
	if !strings.Contains(out, "event: error") {
		t.Fatal("expected error event on empty body")
	}
	if !strings.Contains(out, "empty response") {
		t.Fatal("expected error message mentioning empty response")
	}

	for _, unwanted := range []string{
		"content_block_start", "content_block_delta", "content_block_stop",
		"message_delta", "message_stop",
	} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("unwanted event %q on empty body:\n%s", unwanted, out)
		}
	}
}

func TestStreamTranslateScannerErrorEmitsNoTerminalEvents(t *testing.T) {
	partial := `data: {"choices":[{"delta":{"content":"partial"}}]}` + "\n\n"
	body := io.MultiReader(
		strings.NewReader(partial),
		&errReader{err: errors.New("simulated scanner failure")},
	)

	w := httptest.NewRecorder()
	_, _, _ = streamTranslate(w, body, "test-model")
	out := w.Body.String()

	if !strings.Contains(out, "event: message_start") {
		t.Fatal("expected message_start on partial body")
	}
	if !strings.Contains(out, "content_block_start") {
		t.Fatal("expected content_block_start for partial data")
	}
	if !strings.Contains(out, `"text":"partial"`) {
		t.Fatal("expected partial text delta")
	}
	if !strings.Contains(out, "event: error") {
		t.Fatal("expected error event after scanner error")
	}
	if !strings.Contains(out, "simulated scanner failure") {
		t.Fatal("expected scanner error message in error event")
	}

	for _, unwanted := range []string{
		"message_delta", "message_stop",
	} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("unwanted terminal event %q after scanner error:\n%s", unwanted, out)
		}
	}
}

func TestStreamTranslatePartialThenEOF(t *testing.T) {
	openaiSSE := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"partial"}}]}`,
		`data: {"choices":[{"delta":{"content":" output"}}]}`,
	}, "\n\n")
	w := httptest.NewRecorder()
	_, _, _ = streamTranslate(w, strings.NewReader(openaiSSE), "test-model")
	out := w.Body.String()

	if !strings.Contains(out, "event: message_start") {
		t.Fatal("expected message_start")
	}
	if !strings.Contains(out, `"text":"partial"`) {
		t.Fatal("expected partial text delta")
	}
	if !strings.Contains(out, `"text":" output"`) {
		t.Fatal("expected second text delta")
	}
	if !strings.Contains(out, "event: error") {
		t.Fatal("expected error event after incomplete EOF")
	}
	if !strings.Contains(out, "unexpectedly") {
		t.Fatal("expected error message about unexpected end")
	}

	for _, unwanted := range []string{
		"message_delta", "message_stop",
	} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("unwanted terminal event %q on partial-then-EOF:\n%s", unwanted, out)
		}
	}
}

func TestStreamTranslateUsageOnlyThenDONE(t *testing.T) {
	openaiSSE := strings.Join([]string{
		`data: {"choices":[{"delta":{}}],"usage":{"prompt_tokens":5,"completion_tokens":10}}`,
		`data: [DONE]`,
	}, "\n\n")
	w := httptest.NewRecorder()
	_, _, _ = streamTranslate(w, strings.NewReader(openaiSSE), "test-model")
	out := w.Body.String()

	if !strings.Contains(out, "event: message_start") {
		t.Fatal("expected message_start")
	}
	if !strings.Contains(out, "event: error") {
		t.Fatal("expected error event for usage-only stream")
	}
	if !strings.Contains(out, "no usable output") {
		t.Fatal("expected error message about no usable output")
	}

	for _, unwanted := range []string{
		"content_block_start", "content_block_delta",
		"message_delta", "message_stop",
	} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("unwanted event %q on usage-only stream:\n%s", unwanted, out)
		}
	}
}
