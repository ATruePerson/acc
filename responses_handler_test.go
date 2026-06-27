package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockTripper struct {
	fn func(*http.Request) (*http.Response, error)
}

func (m *mockTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return m.fn(r)
}

func TestTranslateFromResponses(t *testing.T) {
	cfg := testCfg()
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
	or, err := translateFromResponses(req, route, cfg)
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
	if resp.Output[0].Type != "message" || string(resp.Output[0].Content) != `"Hello!"` {
		t.Errorf("first item should be message with Hello!, got %+v", resp.Output[0])
	}
	if resp.Output[1].Type != "function_call" || resp.Output[1].Name != "get_weather" {
		t.Errorf("second item should be function_call with get_weather, got %+v", resp.Output[1])
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 10 {
		t.Errorf("usage prompt tokens = %v, want 10", resp.Usage)
	}
}

func TestHandleResponses_nonstream(t *testing.T) {
	cfg := &Config{
		Providers: map[string]Provider{
			"nvidia": {BaseURL: "https://api.nvidia.com", APIKey: "test"},
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
	if resp.Output[0].Type != "message" || string(resp.Output[0].Content) != `"Hi there!"` {
		t.Errorf("unexpected output: %+v", resp.Output[0])
	}
}
