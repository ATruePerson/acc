package main

import (
	"github.com/ATruePerson/acc/claude"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExecuteUpstreamReturnsProvider400Error(t *testing.T) {
	primary := Route{Provider: "opencode", Model: "big-pickle"}
	cfg := &Config{Providers: map[string]Provider{
		"opencode": {BaseURL: "https://opencode.test", APIKey: "primary"},
	}}
	s := testServer(cfg)

	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(`{"error":{"message":"Error from provider (Console): Upstream request failed"}}`)),
		}, nil
	}}}

	_, _, err := s.executeUpstream(
		context.Background(),
		&OpenAIRequest{Model: primary.Model, Messages: []OpenAIMessage{{Role: "user", Content: claude.JSONString("hi")}}},
		[]resolvedModel{
			{ID: "primary", Route: primary},
		},
		cfg,
		func(string, int, int, int, string) {},
		httptest.NewRecorder(),
	)
	if err == nil {
		t.Fatal("expected error from provider 400, got nil")
	}
	if !strings.Contains(err.Error(), "upstream") {
		t.Fatalf("error = %q, want upstream error", err.Error())
	}
}

func TestValidateNoLegacyRoutingKeysCatchesFallbackKeys(t *testing.T) {
	configWithFallbacks := []byte(`{"routes":{"default":{"fallbacks":[{"provider":"x","model":"y"}]}}}`)
	if err := validateNoLegacyRoutingKeys(configWithFallbacks); err == nil {
		t.Fatal("expected error for config with fallbacks, got nil")
	}
	configWithFallbackModel := []byte(`{"models":{"codex-opus":{"fallback_model":"other"}}}`)
	if err := validateNoLegacyRoutingKeys(configWithFallbackModel); err == nil {
		t.Fatal("expected error for config with fallback_model, got nil")
	}
	configWithImageModel := []byte(`{"models":{"codex-opus":{"image_model":"vision-model"}}}`)
	if err := validateNoLegacyRoutingKeys(configWithImageModel); err == nil {
		t.Fatal("expected error for config with image_model, got nil")
	}
	// Also test that clean config passes (no error for valid entries)
	cleanConfig := []byte(`{"routes":{"test":{"provider":"x","model":"y"}},"models":{"m":{"provider":"x","model":"m"}}}`)
	if err := validateNoLegacyRoutingKeys(cleanConfig); err != nil {
		t.Fatalf("clean config should pass validation, got: %v", err)
	}

	// Fallbacks with mixed case in route
	configMixedRoute := []byte(`{"routes":{"d":{"Fallbacks":[{"p":"x","m":"y"}]}}}`)
	if err := validateNoLegacyRoutingKeys(configMixedRoute); err == nil {
		t.Fatal("expected error for route with mixed-case Fallbacks, got nil")
	}
	// Fallback_models (plural) in routes
	configFallbackModels := []byte(`{"routes":{"d":{"fallback_models":[{"p":"x","m":"y"}]}}}`)
	if err := validateNoLegacyRoutingKeys(configFallbackModels); err == nil {
		t.Fatal("expected error for config with fallback_models, got nil")
	}
	// Fallback_models (plural) in alias_routes
	configAliasRouteFallbackModels := []byte(`{"alias_routes":{"d":{"fallback_models":[{"p":"x","m":"y"}]}}}`)
	if err := validateNoLegacyRoutingKeys(configAliasRouteFallbackModels); err == nil {
		t.Fatal("expected error for alias_route with fallback_models, got nil")
	}
	// image_fallback_models in models
	configImageFallback := []byte(`{"models":{"m":{"image_fallback_models":["other"]}}}`)
	if err := validateNoLegacyRoutingKeys(configImageFallback); err == nil {
		t.Fatal("expected error for config with image_fallback_models, got nil")
	}
	// Mixed-case fallback_model in aliases
	configAliasMixed := []byte(`{"aliases":{"x":{"Fallback_Model":"other"}}}`)
	if err := validateNoLegacyRoutingKeys(configAliasMixed); err == nil {
		t.Fatal("expected error for alias with mixed-case Fallback_Model, got nil")
	}
	// ALL CAPS fallback key in alias_routes
	configAliasRouteAllCaps := []byte(`{"alias_routes":{"d":{"FALLBACKS":[{"p":"x","m":"y"}]}}}`)
	if err := validateNoLegacyRoutingKeys(configAliasRouteAllCaps); err == nil {
		t.Fatal("expected error for alias_route with ALL CAPS FALLBACKS, got nil")
	}
}

func TestExecuteUpstreamReturnsRateLimitError(t *testing.T) {
	route := Route{Provider: "nvidia", Model: "z-ai/glm-5.2"}
	cfg := &Config{Providers: map[string]Provider{
		"nvidia": {BaseURL: "https://nvidia.test", APIKey: "k"},
	}}
	s := testServer(cfg)
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(`{"error":"rate_limited"}`)),
		}, nil
	}}}

	_, _, err := s.executeUpstream(
		context.Background(),
		&OpenAIRequest{Model: route.Model, Messages: []OpenAIMessage{{Role: "user", Content: claude.JSONString("hi")}}},
		[]resolvedModel{{ID: "route", Route: route}},
		cfg,
		func(string, int, int, int, string) {},
		httptest.NewRecorder(),
	)
	if err == nil {
		t.Fatal("expected error from 429, got nil")
	}
	if !strings.Contains(err.Error(), "rate") && !strings.Contains(err.Error(), "429") {
		t.Fatalf("error = %q, want rate-limit message", err.Error())
	}
}

func TestExecuteUpstreamReturnsServerError(t *testing.T) {
	route := Route{Provider: "nvidia", Model: "z-ai/glm-5.2"}
	cfg := &Config{Providers: map[string]Provider{
		"nvidia": {BaseURL: "https://nvidia.test", APIKey: "k"},
	}}
	s := testServer(cfg)
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(`{"error":"overloaded"}`)),
		}, nil
	}}}

	_, _, err := s.executeUpstream(
		context.Background(),
		&OpenAIRequest{Model: route.Model, Messages: []OpenAIMessage{{Role: "user", Content: claude.JSONString("hi")}}},
		[]resolvedModel{{ID: "route", Route: route}},
		cfg,
		func(string, int, int, int, string) {},
		httptest.NewRecorder(),
	)
	if err == nil {
		t.Fatal("expected error from 503, got nil")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Fatalf("error = %q, want 503 server-down message", err.Error())
	}
}

func TestHandleMessagesReturnsErrorOnUpstreamConnectionFailure(t *testing.T) {
	primary := Route{Provider: "nvidia", Model: "z-ai/glm-5.2", ReasoningEffort: "high", ReasoningLocked: true}
	cfg := &Config{
		Providers: map[string]Provider{
			"nvidia": {BaseURL: "https://nvidia.test", APIKey: "primary"},
		},
		AliasRoutes: map[string]Route{"opus": {Provider: primary.Provider, Model: primary.Model, ReasoningEffort: primary.ReasoningEffort, ReasoningLocked: true}},
	}
	s := testServer(cfg)
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("primary timed out")
	}}}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"opus","max_tokens":64,"messages":[{"role":"user","content":"Reply exactly OPUS_OK"}]}`))
	resp := httptest.NewRecorder()
	s.handleMessages(resp, req)
	if resp.Code == http.StatusOK {
		t.Fatalf("expected error status, got 200: body = %s", resp.Body.String())
	}
	if resp.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: body = %s", resp.Code, resp.Body.String())
	}
}
