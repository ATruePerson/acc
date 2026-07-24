package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuildAnthropicProviderRequestPreservesToolsAndImages(t *testing.T) {
	req := &OpenAIRequest{
		Model: "claude-test", MaxTokens: 321,
		Messages: []OpenAIMessage{
			{Role: "system", Content: jsonString("system rules")},
			{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]`)},
		},
		Tools: []OpenAITool{{Type: "function", Function: OpenAIFunction{Name: "read_file", Description: "read", Parameters: json.RawMessage(`{"type":"object"}`)}}},
	}
	runtime := providerRuntime{ID: "anthropic", BaseURL: "https://api.anthropic.test", Adapter: providerAdapterAnthropic, APIKey: "secret"}
	upstream, err := buildProviderRequest(context.Background(), runtime, req)
	if err != nil {
		t.Fatal(err)
	}
	if upstream.URL.String() != "https://api.anthropic.test/v1/messages" {
		t.Fatalf("url = %s", upstream.URL)
	}
	if upstream.Header.Get("x-api-key") != "secret" || upstream.Header.Get("Authorization") != "" {
		t.Fatalf("wrong Anthropic auth headers: %#v", upstream.Header)
	}
	body, _ := io.ReadAll(upstream.Body)
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["system"] != "system rules" || got["max_tokens"] != float64(321) {
		t.Fatalf("lost Anthropic request fields: %s", body)
	}
	if len(got["tools"].([]any)) != 1 {
		t.Fatalf("lost tool: %s", body)
	}
	messages := got["messages"].([]any)
	content := messages[0].(map[string]any)["content"].([]any)
	if content[1].(map[string]any)["type"] != "image" {
		t.Fatalf("image was not converted: %s", body)
	}
}

func TestNormalizeAnthropicResponseProducesOpenAIShape(t *testing.T) {
	body := `{"id":"msg_1","content":[{"type":"thinking","thinking":"why"},{"type":"text","text":"done"},{"type":"tool_use","id":"tool_1","name":"shell","input":{"cmd":"pwd"}}],"stop_reason":"tool_use","usage":{"input_tokens":4,"output_tokens":7}}`
	resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
	if err := normalizeProviderResponse(resp, providerAdapterAnthropic, false); err != nil {
		t.Fatal(err)
	}
	var got OpenAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	msg := got.Choices[0].Message
	if decodeStringContent(msg.Content) != "done" || decodeStringContent(msg.ReasoningContent) != "why" {
		t.Fatalf("content conversion failed: %#v", msg)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "shell" || msg.ToolCalls[0].Function.Arguments != `{"cmd":"pwd"}` {
		t.Fatalf("tool conversion failed: %#v", msg.ToolCalls)
	}
	if got.Usage == nil || got.Usage.PromptTokens != 4 || got.Usage.CompletionTokens != 7 {
		t.Fatalf("usage conversion failed: %#v", got.Usage)
	}
}

func TestAnthropicOAuthToolNamesRoundTripWithoutTouchingBuiltins(t *testing.T) {
	for _, name := range []string{"web_search", "code_execution", "text_editor", "computer"} {
		if got := anthropicOAuthToolName(name); got != name {
			t.Fatalf("builtin %q became %q", name, got)
		}
	}
	for _, name := range []string{"mcp__obsidian__read", "apply_patch"} {
		wire := anthropicOAuthToolName(name)
		if wire != "custom_"+name || restoreAnthropicOAuthToolName(wire) != name {
			t.Fatalf("tool round trip %q -> %q -> %q", name, wire, restoreAnthropicOAuthToolName(wire))
		}
	}
}

func TestAnthropicStreamConversionIncludesTextToolsReasoningAndUsage(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":3}}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"why"}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"ok"}}`,
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"t1","name":"shell"}}`,
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\\\"cmd\\\":"}}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`,
		`data: {"type":"message_stop"}`,
	}, "\n\n")
	out, err := io.ReadAll(anthropicStreamToOpenAI(io.NopCloser(strings.NewReader(sse))))
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{`"reasoning_content":"why"`, `"content":"ok"`, `"name":"shell"`, `"prompt_tokens":3`, `"completion_tokens":5`, "data: [DONE]"} {
		if !strings.Contains(text, want) {
			t.Fatalf("stream missing %q:\n%s", want, text)
		}
	}
}

type rotatingTestDriver struct{ calls atomic.Int32 }

func (d *rotatingTestDriver) Refresh(_ context.Context, old authCredential) (authCredential, error) {
	d.calls.Add(1)
	old.AccessToken = "fresh-token"
	old.ExpiresAt = time.Now().Add(time.Hour)
	return old, nil
}

func TestDoProviderRequestReplaysOAuth401ExactlyOnce(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer fresh-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()

	store := newMemoryCredentialStore()
	_ = store.Save(context.Background(), authCredential{Provider: "xai", AccessToken: "stale-token", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)})
	driver := &rotatingTestDriver{}
	manager := newAuthManager(store, map[string]authDriver{"xai": driver})
	runtime := providerRuntime{ID: "xai", BaseURL: server.URL, Adapter: providerAdapterOpenAIChat, BearerToken: "stale-token", OAuth: true}

	resp, err := doProviderRequest(context.Background(), http.DefaultClient, manager, runtime, &OpenAIRequest{Model: "grok", Messages: []OpenAIMessage{{Role: "user", Content: jsonString("hi")}}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || requests.Load() != 2 || driver.calls.Load() != 1 {
		t.Fatalf("status=%d requests=%d refreshes=%d", resp.StatusCode, requests.Load(), driver.calls.Load())
	}
}

func TestDoProviderRequestDoesNotLoopOnSecondOAuth401(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	store := newMemoryCredentialStore()
	_ = store.Save(context.Background(), authCredential{Provider: "kimi", AccessToken: "stale", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour)})
	driver := &rotatingTestDriver{}
	manager := newAuthManager(store, map[string]authDriver{"kimi": driver})
	runtime := providerRuntime{ID: "kimi", BaseURL: server.URL, Adapter: providerAdapterOpenAIChat, BearerToken: "stale", OAuth: true}
	resp, err := doProviderRequest(context.Background(), http.DefaultClient, manager, runtime, &OpenAIRequest{Model: "kimi", Messages: []OpenAIMessage{{Role: "user", Content: jsonString("hi")}}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || requests.Load() != 2 || driver.calls.Load() != 1 {
		t.Fatalf("status=%d requests=%d refreshes=%d", resp.StatusCode, requests.Load(), driver.calls.Load())
	}
}
