package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRouteForTarget(t *testing.T) {
	cfg := &Config{
		Aliases: map[string]Route{
			"anthropic/claude-opus": {
				Provider: "nvidia", Model: "nemotron-3-ultra-550b-a55b",
				Fallbacks: []Route{
					{Provider: "nvidia", Model: "deepseek-v4-pro"},
				},
			},
			"anthropic/claude-haiku": {
				Provider: "gemini", Model: "models/gemini-3.1-flash-lite",
			},
		},
	}

	cases := []struct {
		name      string
		target    benchTarget
		wantModel string
		wantErr   bool
	}{
		{"primary", benchTarget{AliasKey: "anthropic/claude-opus", FallbackIndex: -1}, "nemotron-3-ultra-550b-a55b", false},
		{"fallback", benchTarget{AliasKey: "anthropic/claude-opus", FallbackIndex: 0}, "deepseek-v4-pro", false},
		{"no fallback configured", benchTarget{AliasKey: "anthropic/claude-haiku", FallbackIndex: 0}, "", true},
		{"unknown alias", benchTarget{AliasKey: "anthropic/claude-ghost", FallbackIndex: -1}, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := routeForTarget(cfg, c.target)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got route %+v", r)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if r.Model != c.wantModel {
				t.Errorf("model = %q, want %q", r.Model, c.wantModel)
			}
			if r.Fallbacks != nil {
				t.Errorf("expected Fallbacks cleared on returned route, got %+v", r.Fallbacks)
			}
		})
	}
}

func TestBenchTargetsAndPromptsShape(t *testing.T) {
	if len(benchTargets) != 7 {
		t.Errorf("len(benchTargets) = %d, want 7", len(benchTargets))
	}
	if len(benchPrompts) != 8 {
		t.Errorf("len(benchPrompts) = %d, want 8", len(benchPrompts))
	}
	categories := map[string]int{}
	for _, p := range benchPrompts {
		categories[p.Category]++
	}
	for _, cat := range []string{"coding", "creative", "quick", "fiction"} {
		if categories[cat] != 2 {
			t.Errorf("category %q has %d prompts, want 2", cat, categories[cat])
		}
	}
}

func TestCallModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello world"}}],"usage":{"prompt_tokens":12,"completion_tokens":34}}`))
	}))
	defer srv.Close()

	cfg := &Config{
		Providers: map[string]Provider{
			"fake": {BaseURL: srv.URL, APIKey: "test-key"},
		},
	}
	route := Route{Provider: "fake", Model: "fake-model"}

	text, tokensIn, tokensOut, latencyMs, err := callModel(context.Background(), srv.Client(), cfg, route, "hi", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "hello world" {
		t.Errorf("text = %q, want %q", text, "hello world")
	}
	if tokensIn != 12 || tokensOut != 34 {
		t.Errorf("tokens = %d/%d, want 12/34", tokensIn, tokensOut)
	}
	if latencyMs < 0 {
		t.Errorf("latencyMs = %d, want >= 0", latencyMs)
	}
}

func TestCallModelUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"degraded"}`))
	}))
	defer srv.Close()

	cfg := &Config{Providers: map[string]Provider{"fake": {BaseURL: srv.URL, APIKey: "k"}}}
	route := Route{Provider: "fake", Model: "fake-model"}

	_, _, _, _, err := callModel(context.Background(), srv.Client(), cfg, route, "hi", 100)
	if err == nil {
		t.Fatal("expected error for 503 upstream response")
	}
}

func TestCallModelUnknownProvider(t *testing.T) {
	cfg := &Config{Providers: map[string]Provider{}}
	route := Route{Provider: "ghost", Model: "m"}
	_, _, _, _, err := callModel(context.Background(), http.DefaultClient, cfg, route, "hi", 100)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestParseJudgeJSON(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		wantScore int
		wantErr   bool
	}{
		{"clean json", `{"score": 8, "rationale": "solid"}`, 8, false},
		{"fenced", "```json\n{\"score\": 7, \"rationale\": \"ok\"}\n```", 7, false},
		{"prose wrapper", `Here you go: {"score": 9, "rationale": "great"} hope that helps`, 9, false},
		{"malformed", `{"score": 8, "rationale"`, 0, true},
		{"score too high", `{"score": 11, "rationale": "x"}`, 0, true},
		{"score too low", `{"score": 0, "rationale": "x"}`, 0, true},
		{"no json", `sorry, I cannot grade this`, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := parseJudgeJSON(c.text)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", r)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if r.Score != c.wantScore {
				t.Errorf("score = %d, want %d", r.Score, c.wantScore)
			}
		})
	}
}

func TestJudgeResponseRetriesOnceOnParseFailure(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"not json"}}]}`))
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"score\":6,\"rationale\":\"fine\"}"}}]}`))
	}))
	defer srv.Close()

	cfg := &Config{Providers: map[string]Provider{"nvidia": {BaseURL: srv.URL, APIKey: "k"}}}

	res, err := judgeResponse(context.Background(), srv.Client(), cfg, "coding", "prompt", "response")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Score != 6 {
		t.Errorf("score = %d, want 6", res.Score)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (one retry)", calls)
	}
}

func TestJudgeResponseUnknownCategory(t *testing.T) {
	cfg := &Config{}
	_, err := judgeResponse(context.Background(), http.DefaultClient, cfg, "unknown-category", "p", "r")
	if err == nil {
		t.Fatal("expected error for unknown category")
	}
}

func TestJudgeResponseGivesUpAfterTwoBadReplies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"still not json"}}]}`))
	}))
	defer srv.Close()

	cfg := &Config{Providers: map[string]Provider{"nvidia": {BaseURL: srv.URL, APIKey: "k"}}}
	_, err := judgeResponse(context.Background(), srv.Client(), cfg, "coding", "prompt", "response")
	if err == nil {
		t.Fatal("expected error after two unparseable judge replies")
	}
}
