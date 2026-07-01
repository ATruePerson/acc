package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fastBackoff swaps the retry wait to zero for the duration of a test, so
// retry paths don't sleep real seconds, then restores it.
func fastBackoff(t *testing.T) {
	t.Helper()
	orig := benchBackoff
	benchBackoff = func(int) time.Duration { return 0 }
	t.Cleanup(func() { benchBackoff = orig })
}

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
	fastBackoff(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"degraded"}`))
	}))
	defer srv.Close()

	cfg := &Config{Providers: map[string]Provider{"fake": {BaseURL: srv.URL, APIKey: "k"}}}
	route := Route{Provider: "fake", Model: "fake-model"}

	_, _, _, _, err := callModel(context.Background(), srv.Client(), cfg, route, "hi", 100)
	if err == nil {
		t.Fatal("expected error for sustained 503 upstream response")
	}
}

func TestCallModelRetriesOn429ThenSucceeds(t *testing.T) {
	fastBackoff(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"status":429,"title":"Too Many Requests"}`))
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"recovered"}}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
	}))
	defer srv.Close()

	cfg := &Config{Providers: map[string]Provider{"fake": {BaseURL: srv.URL, APIKey: "k"}}}
	route := Route{Provider: "fake", Model: "fake-model"}

	text, _, _, _, err := callModel(context.Background(), srv.Client(), cfg, route, "hi", 100)
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if text != "recovered" {
		t.Errorf("text = %q, want %q", text, "recovered")
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3 (two 429s then success)", got)
	}
}

func TestCallModelGivesUpAfterMaxAttempts(t *testing.T) {
	fastBackoff(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"status":429}`))
	}))
	defer srv.Close()

	cfg := &Config{Providers: map[string]Provider{"fake": {BaseURL: srv.URL, APIKey: "k"}}}
	route := Route{Provider: "fake", Model: "fake-model"}

	_, _, _, _, err := callModel(context.Background(), srv.Client(), cfg, route, "hi", 100)
	if err == nil {
		t.Fatal("expected error after exhausting retries on sustained 429")
	}
	if got := calls.Load(); got != benchMaxAttempts {
		t.Errorf("calls = %d, want %d (benchMaxAttempts)", got, benchMaxAttempts)
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

	cfg := &Config{Providers: map[string]Provider{"gemini": {BaseURL: srv.URL, APIKey: "k"}}}

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

	cfg := &Config{Providers: map[string]Provider{"gemini": {BaseURL: srv.URL, APIKey: "k"}}}
	_, err := judgeResponse(context.Background(), srv.Client(), cfg, "coding", "prompt", "response")
	if err == nil {
		t.Fatal("expected error after two unparseable judge replies")
	}
}

func intPtr(n int) *int { return &n }

func TestAllBenchJobsCount(t *testing.T) {
	jobs := allBenchJobs()
	want := len(benchTargets) * len(benchPrompts)
	if len(jobs) != want {
		t.Errorf("len(allBenchJobs()) = %d, want %d (%d targets x %d prompts)", len(jobs), want, len(benchTargets), len(benchPrompts))
	}
}

func TestRunBenchJobSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"score\":8,\"rationale\":\"good\"}"}}],"usage":{"prompt_tokens":10,"completion_tokens":20}}`))
	}))
	defer srv.Close()

	cfg := &Config{
		Providers: map[string]Provider{
			"nvidia": {BaseURL: srv.URL, APIKey: "k"}, // generation target
			"gemini": {BaseURL: srv.URL, APIKey: "k"}, // judge target
		},
		Aliases: map[string]Route{
			"anthropic/claude-haiku": {Provider: "nvidia", Model: "test-model"},
		},
	}
	job := benchJob{
		Target: benchTarget{Identity: "haiku", Category: "quick", Variant: "primary", AliasKey: "anthropic/claude-haiku", FallbackIndex: -1},
		Prompt: benchPrompt{ID: "quick-1", Category: "quick", Text: "summarize this"},
	}

	result := runBenchJob(context.Background(), srv.Client(), cfg, "20260701-120000", job)
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Score == nil || *result.Score != 8 {
		t.Errorf("score = %v, want 8", result.Score)
	}
	if result.ResponseText == "" {
		t.Error("expected ResponseText to be populated")
	}
	if result.Model != "test-model" || result.Provider != "nvidia" {
		t.Errorf("model/provider = %s/%s, want test-model/nvidia", result.Model, result.Provider)
	}
}

func TestRunBenchJobBadTargetNeverPanics(t *testing.T) {
	cfg := &Config{Aliases: map[string]Route{}}
	job := benchJob{
		Target: benchTarget{Identity: "ghost", AliasKey: "anthropic/claude-ghost", FallbackIndex: -1},
		Prompt: benchPrompt{ID: "coding-1", Category: "coding", Text: "x"},
	}
	result := runBenchJob(context.Background(), http.DefaultClient, cfg, "20260701-120000", job)
	if result.Error == "" {
		t.Error("expected error for unresolvable target")
	}
	if result.Score != nil {
		t.Error("expected nil score for a job that never reached generation")
	}
}

func TestBenchJobResultJSONShape(t *testing.T) {
	r := benchJobResult{
		RunID: "20260701-100000", Timestamp: "2026-07-01T10:00:00Z",
		Identity: "opus", Variant: "primary", Model: "nemotron-3-ultra-550b-a55b", Provider: "nvidia",
		Category: "coding", PromptID: "coding-1", Score: intPtr(8), Rationale: "solid",
		LatencyMs: 1500, TokensIn: 50, TokensOut: 100,
		ResponseText: "this should never appear in JSONL",
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	if strings.Contains(s, "this should never appear in JSONL") {
		t.Error("ResponseText leaked into JSON output, expected json:\"-\" to exclude it")
	}
	if strings.Contains(s, `"error"`) {
		t.Error("empty error field should be omitted, not present")
	}
	var roundTrip benchJobResult
	if err := json.Unmarshal(b, &roundTrip); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if roundTrip.Score == nil || *roundTrip.Score != 8 {
		t.Errorf("score round-trip failed: %+v", roundTrip.Score)
	}
}

func TestAvgScoreFor(t *testing.T) {
	results := []benchJobResult{
		{Identity: "opus", Variant: "primary", Category: "coding", Score: intPtr(8)},
		{Identity: "opus", Variant: "primary", Category: "coding", Score: intPtr(6)},
		{Identity: "opus", Variant: "primary", Category: "creative", Score: intPtr(4)},
		{Identity: "opus", Variant: "primary", Category: "coding", Score: nil, Error: "timeout"},
	}
	avg, ok := avgScoreFor(results, "opus", "primary", "coding")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if avg != 7.0 {
		t.Errorf("avg = %v, want 7.0", avg)
	}
	if _, ok := avgScoreFor(results, "opus", "primary", "fiction"); ok {
		t.Error("expected ok=false for category with no results")
	}
}

func TestMostRecentRunID(t *testing.T) {
	results := []benchJobResult{
		{RunID: "20260601-100000"},
		{RunID: "20260615-100000"},
		{RunID: "20260701-100000"},
	}
	if got := mostRecentRunID(results, "20260701-100000"); got != "20260615-100000" {
		t.Errorf("got %q, want %q", got, "20260615-100000")
	}
	if got := mostRecentRunID(nil, "20260701-100000"); got != "" {
		t.Errorf("got %q, want empty for no history", got)
	}
}

func TestBuildDiffLines(t *testing.T) {
	history := []benchJobResult{
		{RunID: "20260615-100000", Identity: "sonnet", Variant: "primary", Category: "creative", Score: intPtr(7)},
	}
	current := []benchJobResult{
		{RunID: "20260701-100000", Identity: "sonnet", Variant: "primary", Category: "creative", Score: intPtr(8)},
	}
	lines := buildDiffLines(history, current, "20260701-100000")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "7.0 -> 8.0 (+1.0)") {
		t.Errorf("line = %q, missing expected delta", lines[0])
	}

	if lines := buildDiffLines(nil, current, "20260701-100000"); lines != nil {
		t.Errorf("expected nil lines for no history, got %v", lines)
	}
}

func TestLoadBenchHistorySkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bench_runs.jsonl")
	content := "{\"run_id\":\"a\",\"identity\":\"opus\",\"variant\":\"primary\",\"category\":\"coding\",\"score\":8}\n" +
		"not valid json\n" +
		"{\"run_id\":\"a\",\"identity\":\"sonnet\",\"variant\":\"primary\",\"category\":\"creative\",\"score\":7}\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	results, err := loadBenchHistory(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
}

func TestLoadBenchHistoryMissingFile(t *testing.T) {
	results, err := loadBenchHistory("/nonexistent/path/bench_runs.jsonl")
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results, got %v", results)
	}
}

func TestBuildSummaryTable(t *testing.T) {
	results := []benchJobResult{
		{Identity: "opus", Variant: "primary", Category: "coding", Score: intPtr(8)},
		{Identity: "opus", Variant: "primary", Category: "coding", Score: intPtr(6)},
		{Identity: "haiku", Variant: "primary", Category: "quick", Score: intPtr(9)},
	}
	out := buildSummaryTable(results)
	if !strings.Contains(out, "opus/primary") {
		t.Error("missing opus/primary row")
	}
	if !strings.Contains(out, "7.0") {
		t.Error("missing expected average 7.0 for opus/primary coding")
	}
	if !strings.Contains(out, "9.0") {
		t.Error("missing expected average 9.0 for haiku/primary quick")
	}
}

func TestBuildMarkdownReport(t *testing.T) {
	jobs := []benchJob{
		{Target: benchTarget{Identity: "opus", Variant: "primary"}, Prompt: benchPrompt{ID: "coding-1", Text: "write a thing"}},
	}
	results := []benchJobResult{
		{Identity: "opus", Variant: "primary", PromptID: "coding-1", Model: "nemotron-3-ultra-550b-a55b", Provider: "nvidia", Score: intPtr(8), Rationale: "good", ResponseText: "func foo() {}"},
	}
	out := buildMarkdownReport("20260701-100000", jobs, results)
	if !strings.Contains(out, "write a thing") {
		t.Error("missing prompt text")
	}
	if !strings.Contains(out, "func foo() {}") {
		t.Error("missing response text")
	}
	if !strings.Contains(out, "8/10") {
		t.Error("missing score")
	}
}

func TestBuildMarkdownReportError(t *testing.T) {
	jobs := []benchJob{
		{Target: benchTarget{Identity: "opus", Variant: "primary"}, Prompt: benchPrompt{ID: "coding-1", Text: "write a thing"}},
	}
	results := []benchJobResult{
		{Identity: "opus", Variant: "primary", PromptID: "coding-1", Error: "upstream 503: degraded"},
	}
	out := buildMarkdownReport("20260701-100000", jobs, results)
	if !strings.Contains(out, "upstream 503: degraded") {
		t.Error("missing error text in report")
	}
}
