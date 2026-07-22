package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExecuteUpstreamFallsBackWhenProviderRejectsItsOwnRequest(t *testing.T) {
	primary := Route{Provider: "opencode", Model: "big-pickle"}
	fallback := Route{
		Provider:  "nvidia",
		Model:     "nvidia/backup",
		Reasoning: map[string]ReasoningTarget{"high": {Effort: "high"}},
		ExtraBody: map[string]any{
			"chat_template_kwargs": map[string]any{"enable_thinking": true},
		},
	}
	cfg := &Config{Providers: map[string]Provider{
		"opencode": {BaseURL: "https://opencode.test", APIKey: "primary"},
		"nvidia":   {BaseURL: "https://nvidia.test", APIKey: "fallback"},
	}}
	s := testServer(cfg)

	calls := 0
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewBufferString(`{"error":{"message":"Error from provider (Console): Upstream request failed"}}`)),
			}, nil
		}

		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != fallback.Model {
			t.Fatalf("fallback model = %q, want %q", body["model"], fallback.Model)
		}
		if body["reasoning_effort"] != "high" {
			t.Fatalf("fallback reasoning effort = %q, want high", body["reasoning_effort"])
		}
		if _, ok := body["chat_template_kwargs"]; !ok {
			t.Fatal("fallback request is missing its route-level extra body")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(`{"choices":[]}`))}, nil
	}}}

	resp, route, err := s.executeUpstream(
		context.Background(),
		&OpenAIRequest{Model: primary.Model, Messages: []OpenAIMessage{{Role: "user", Content: jsonString("hi")}}, ReasoningEffort: "high"},
		[]resolvedModel{
			{ID: "primary", Capability: ModelCapability{DisplayName: "Primary", Reasoning: map[string]ReasoningTarget{"high": {Effort: "high"}}}, Route: primary},
			{ID: "fallback", Capability: ModelCapability{DisplayName: "Fallback", Reasoning: map[string]ReasoningTarget{"high": {Effort: "high"}}}, Route: fallback, Fallback: true},
		},
		cfg,
		func(string, int, int, int, string) {},
		httptest.NewRecorder(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || route.Route.Provider != fallback.Provider || route.Route.Model != fallback.Model {
		t.Fatalf("got status=%d route=%+v, want 200 fallback", resp.StatusCode, route)
	}
	if calls != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls)
	}
}

func TestHandleMessagesFallsBackOnUpstreamConnectionFailure(t *testing.T) {
	primary := Route{Provider: "nvidia", Model: "z-ai/glm-5.2", ReasoningEffort: "high", ReasoningLocked: true}
	fallback := Route{Provider: "openrouter", Model: "nvidia/nemotron-3-ultra-550b-a55b:free", ReasoningEffort: "high", ReasoningLocked: true}
	cfg := &Config{
		Providers: map[string]Provider{
			"nvidia":     {BaseURL: "https://nvidia.test", APIKey: "primary"},
			"openrouter": {BaseURL: "https://openrouter.test", APIKey: "fallback"},
		},
		AliasRoutes: map[string]Route{"opus": {Provider: primary.Provider, Model: primary.Model, ReasoningEffort: primary.ReasoningEffort, ReasoningLocked: true, Fallbacks: []Route{fallback}}},
	}
	s := testServer(cfg)
	calls := 0
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("primary timed out")
		}
		if req.URL.Host != "openrouter.test" {
			t.Fatalf("fallback request host = %q", req.URL.Host)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"OPUS_OK"}}]}`)),
		}, nil
	}}}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"opus","max_tokens":64,"messages":[{"role":"user","content":"Reply exactly OPUS_OK"}]}`))
	resp := httptest.NewRecorder()
	s.handleMessages(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "OPUS_OK") {
		t.Fatalf("fallback response missing expected text: %s", resp.Body.String())
	}
	if calls != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls)
	}
}
