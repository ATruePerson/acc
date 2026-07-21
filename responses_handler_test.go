package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type mockTripper struct {
	fn func(*http.Request) (*http.Response, error)
}

func TestUpstreamHTTPClientTimesOutWaitingForHeaders(t *testing.T) {
	oldTimeout := firstTokenTimeout
	firstTokenTimeout = 40 * time.Millisecond
	defer func() { firstTokenTimeout = oldTimeout }()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	started := time.Now()
	resp, err := newUpstreamHTTPClient().Get(upstream.URL)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("request unexpectedly waited for delayed response headers")
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("header timeout took %s; want under 200ms", elapsed)
	}
}

func (m *mockTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return m.fn(r)
}

func TestTranslateFromResponses(t *testing.T) {
	req := &ResponsesRequest{
		Model: "anthropic/claude-kimi",
		Input: json.RawMessage(`[{"type":"message","role":"user","content":"hello"},{"type":"function_call","id":"call_1","name":"get_weather","arguments":"{}"}]`),
		Tools: []ResponsesTool{
			{
				Type: "function",
				Function: ResponsesFunction{
					Name:        "get_weather",
					Description: "get weather",
					Parameters:  json.RawMessage(`{}`),
				},
			},
		},
	}
	route := Route{Model: "moonshotai/kimi-k2.6"}
	or, err := translateFromResponses(req, route, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(or.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(or.Messages))
	}
	if or.Messages[0].Role != "user" {
		t.Errorf("first message should be user, got %s", or.Messages[0].Role)
	}
	if or.Messages[1].Role != "assistant" || len(or.Messages[1].ToolCalls) != 1 {
		t.Errorf("second message should be assistant with tool calls, got %+v", or.Messages[1])
	}
	if or.Messages[1].ToolCalls[0].ID != "call_1" {
		t.Errorf("expected tool call ID call_1, got %s", or.Messages[1].ToolCalls[0].ID)
	}
}

func TestTranslateFromResponsesCodexShape(t *testing.T) {
	var req ResponsesRequest
	raw := `{
		"model":"anthropic/claude-opus",
		"instructions":"You are a coding agent.",
		"max_output_tokens":1234,
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"tools":[{"type":"function","name":"shell","description":"run a command","parameters":{"type":"object"},"strict":false}]
	}`
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatal(err)
	}

	or, err := translateFromResponses(&req, Route{Model: "upstream-model"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if or.MaxTokens != 1234 {
		t.Fatalf("max tokens = %d, want 1234", or.MaxTokens)
	}
	if len(or.Messages) != 2 || or.Messages[0].Role != "system" || or.Messages[1].Role != "user" {
		t.Fatalf("unexpected messages: %+v", or.Messages)
	}
	if got := decodeStringContent(or.Messages[0].Content); got != "You are a coding agent." {
		t.Fatalf("system message = %q", got)
	}
	if got := decodeStringContent(or.Messages[1].Content); got != "hello" {
		t.Fatalf("user message = %q", got)
	}
	if len(or.Tools) != 1 || or.Tools[0].Function.Name != "shell" {
		t.Fatalf("flat Codex tool was not translated: %+v", or.Tools)
	}
}

func TestTranslateToResponses(t *testing.T) {
	oresp := &OpenAIResponse{
		Choices: []OpenAIChoice{
			{
				Message: &OpenAIMessage{
					Role:    "assistant",
					Content: jsonString("Hello!"),
					ToolCalls: []OpenAIToolCall{
						{
							ID: "call_2",
							Function: OpenAIFuncCall{
								Name:      "get_weather",
								Arguments: "{}",
							},
						},
					},
				},
			},
		},
		Usage: &OpenAIUsage{
			PromptTokens:     10,
			CompletionTokens: 20,
		},
	}
	resp := translateToResponses(oresp, "claude-kimi")
	if resp.Model != "claude-kimi" {
		t.Errorf("model = %s, want claude-kimi", resp.Model)
	}
	if len(resp.Output) != 2 {
		t.Fatalf("expected 2 output items, got %d", len(resp.Output))
	}
	if resp.Output[0].Type != "message" || !strings.Contains(string(resp.Output[0].Content), `"type":"output_text"`) || !strings.Contains(string(resp.Output[0].Content), `"text":"Hello!"`) {
		t.Errorf("first item should be message with Hello!, got %+v", resp.Output[0])
	}
	if resp.Output[1].Type != "function_call" || resp.Output[1].Name != "get_weather" || resp.Output[1].CallID != "call_2" {
		t.Errorf("second item should be function_call with get_weather, got %+v", resp.Output[1])
	}
	if resp.Usage == nil || resp.Usage.InputTokens != 10 {
		t.Errorf("usage input tokens = %v, want 10", resp.Usage)
	}
}

func TestStreamTranslateResponsesUsesCodexEvents(t *testing.T) {
	openaiSSE := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Hi"}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_7","type":"function","function":{"name":"shell","arguments":"{}"}}]}}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`,
		`data: [DONE]`,
	}, "\n\n")

	w := httptest.NewRecorder()
	in, out := streamTranslateResponses(w, strings.NewReader(openaiSSE), "anthropic/claude-opus")
	body := w.Body.String()
	for _, want := range []string{
		"event: response.created",
		"event: response.in_progress",
		"event: response.output_item.added",
		"event: response.content_part.added",
		"event: response.output_text.delta",
		"event: response.content_part.done",
		"event: response.output_item.done",
		"event: response.completed",
		`"call_id":"call_7"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in stream:\n%s", want, body)
		}
	}
	for _, unwanted := range []string{"response.output_item.created", "event: response.done"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("unexpected legacy event %q in stream:\n%s", unwanted, body)
		}
	}
	if in != 3 || out != 4 {
		t.Fatalf("usage = %d/%d, want 3/4", in, out)
	}
}

func TestHandleResponses_nonstream(t *testing.T) {
	cfg := &Config{
		Providers: map[string]Provider{
			"cloudflare": {BaseURL: "https://api.cloudflare.com", APIKey: "test"},
		},
		Routes: map[string]Route{
			"kimi": {Provider: "nvidia", Model: "moonshotai/kimi-k2.6"},
		},
	}
	s := testServer(cfg)
	s.http = &http.Client{
		Transport: &mockTripper{
			fn: func(req *http.Request) (*http.Response, error) {
				if !strings.Contains(req.URL.Path, "/chat/completions") {
					t.Errorf("unexpected path: %s", req.URL.Path)
				}
				oresp := OpenAIResponse{
					Choices: []OpenAIChoice{
						{
							Message: &OpenAIMessage{
								Role:    "assistant",
								Content: jsonString("Hi there!"),
							},
						},
					},
					Usage: &OpenAIUsage{PromptTokens: 5, CompletionTokens: 5},
				}
				b, _ := json.Marshal(oresp)
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(bytes.NewReader(b)),
					Header:     make(http.Header),
				}, nil
			},
		},
	}

	reqBody := `{"model":"anthropic/claude-kimi","input":[{"type":"message","role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(reqBody))
	w := httptest.NewRecorder()

	s.handleResponses(w, req)

	if w.Code != 200 {
		t.Fatalf("expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	var resp ResponsesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp.Output) != 1 {
		t.Fatalf("expected 1 output item, got %d", len(resp.Output))
	}
	if resp.Output[0].Type != "message" || !strings.Contains(string(resp.Output[0].Content), `"text":"Hi there!"`) {
		t.Errorf("unexpected output: %+v", resp.Output[0])
	}
}

func TestHandleModelsCodexShape(t *testing.T) {
	s := testServer(codexTestConfig())
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("User-Agent", "codex_cli_rs/0.144.2")
	w := httptest.NewRecorder()

	s.handleModels(w, req)

	var body struct {
		Models []struct {
			Slug             string `json:"slug"`
			BaseInstructions string `json:"base_instructions"`
		} `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Models) != 3 || body.Models[0].Slug != "haiku" || body.Models[0].BaseInstructions == "" {
		t.Fatalf("unexpected Codex models response: %s", w.Body.String())
	}
}
