# Model Benchmark Tool Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `acc bench` — a CLI subcommand that runs a fixed prompt set against every configured persona (Opus/Sonnet/Haiku/Fable-Mythos) and their fallback models, judges each response for quality with a free unused model, and reports scores with a diff against the previous run.

**Architecture:** One new file, `bench.go`, in the existing flat root-package layout. Runs in-process (no proxy daemon required) by reusing the existing `Route`/`Config` types and `translateRequest` to build each outgoing call, then POSTing directly to the provider — the same path `handleMessages` already uses, just without the HTTP server in front of it. A capped worker pool (5 concurrent) runs the 7 configs × 8 prompts = 56 generation jobs, each immediately followed by one judge call. Results append to `bench_runs.jsonl` (same pattern as the existing `test_runs.jsonl`) and a full-detail markdown report per run.

**Tech Stack:** Go 1.26.4, standard library only (no new dependencies) — `net/http`, `encoding/json`, `sync`/`sync/atomic` for the worker pool.

Full design rationale lives in `docs/superpowers/specs/2026-07-01-model-benchmark-design.md` — read that first if anything below seems under-explained.

## Global Constraints

- Go module `github.com/ATruePerson/acc`, go 1.26.4 (see `go.mod`) — stdlib only, no new dependencies.
- Worker pool capped at exactly 5 concurrent jobs.
- Judge model is fixed: `Route{Provider: "nvidia", Model: "z-ai/glm-5.1", ReasoningEffort: "high"}` — free, not a contestant in any tested category.
- Judge score scale is 1-10 (integer). A score outside that range is treated as a parse failure.
- A malformed judge reply is retried exactly once; if the retry also fails to parse, the job records `error` and is not counted as scored. One bad job must never abort the run.
- `run_id` format is `YYYYMMDD-HHMMSS` (Go layout `20060102-150405`, local time, no colons) — used both as a JSONL field and as the markdown report filename, so it must stay filesystem-safe.
- The JSONL variant field is named `variant` (values `"primary"`/`"fallback"`), never `config` — avoids colliding with "config" meaning `config.json` elsewhere in this codebase's vocabulary.
- Full prompt/response text is never written to `bench_runs.jsonl` (`ResponseText` field is `json:"-"`) — only to the per-run markdown report. Keeps the JSONL lightweight, matching `test_runs.jsonl`'s existing metrics-only style.
- Config is loaded via `defaultConfigPath()` (`~/.config/acc/config.json`), matching the existing `cmdDoctor`/`cmdModels`/`cmdClaude` convention in `cli.go` — not the repo's local `config.json` copy.
- `bench_runs.jsonl` and `bench_runs/` are runtime artifacts and must be gitignored, same as `test_runs.jsonl`.
- No new files beyond `bench.go` and `bench_test.go` — this is a single-concern feature, matching the one-file-per-concern pattern already used by `translate.go`, `stream.go`, `tui.go`, `dashboard.go`.

---

### Task 1: Bench data (targets, prompts) + routeForTarget

**Files:**
- Create: `bench.go`
- Test: `bench_test.go`

**Interfaces:**
- Produces: `type benchTarget struct { Identity, Category, Variant, AliasKey string; FallbackIndex int }`, `var benchTargets []benchTarget` (7 entries), `type benchPrompt struct { ID, Category, Text string }`, `var benchPrompts []benchPrompt` (8 entries), `func routeForTarget(cfg *Config, t benchTarget) (Route, error)`.
- Consumes: existing `Route`/`Config` types from `types.go` (already in this package — no import needed, same `package main`).

- [ ] **Step 1: Create `bench.go` with package, imports, and the data + function for this task**

```go
package main

import (
	"fmt"
)

// ---------- bench targets ----------

// benchTarget is one model configuration under test: a persona identity,
// the task category it's compared on, which variant (primary or fallback)
// of that persona's alias, and how to resolve it from the live config.
type benchTarget struct {
	Identity      string
	Category      string
	Variant       string // "primary" or "fallback"
	AliasKey      string
	FallbackIndex int // -1 selects the primary route, >=0 selects Fallbacks[i]
}

// benchTargets is the full cross-matrix test matrix: every persona's
// primary and (where configured) fallback model, read live from
// config.json at run time so a config edit (e.g. a temperature tweak) is
// picked up on the next `acc bench` run with no code change. fable and
// mythos are byte-identical in config.json today, so only "fable" is
// tested, labeled "fable/mythos" — see the design doc for why.
var benchTargets = []benchTarget{
	{Identity: "opus", Category: "coding", Variant: "primary", AliasKey: "anthropic/claude-opus", FallbackIndex: -1},
	{Identity: "opus", Category: "coding", Variant: "fallback", AliasKey: "anthropic/claude-opus", FallbackIndex: 0},
	{Identity: "sonnet", Category: "creative", Variant: "primary", AliasKey: "anthropic/claude-sonnet", FallbackIndex: -1},
	{Identity: "sonnet", Category: "creative", Variant: "fallback", AliasKey: "anthropic/claude-sonnet", FallbackIndex: 0},
	{Identity: "haiku", Category: "quick", Variant: "primary", AliasKey: "anthropic/claude-haiku", FallbackIndex: -1},
	{Identity: "fable/mythos", Category: "fiction", Variant: "primary", AliasKey: "anthropic/claude-fable", FallbackIndex: -1},
	{Identity: "fable/mythos", Category: "fiction", Variant: "fallback", AliasKey: "anthropic/claude-fable", FallbackIndex: 0},
}

// routeForTarget resolves a benchTarget to a standalone Route with
// Fallbacks cleared, so calling it never triggers the live proxy's
// automatic fallback-chaining — each variant is tested in isolation.
func routeForTarget(cfg *Config, t benchTarget) (Route, error) {
	r, ok := cfg.Aliases[t.AliasKey]
	if !ok {
		return Route{}, fmt.Errorf("alias %q not found in config", t.AliasKey)
	}
	if t.FallbackIndex >= 0 {
		if t.FallbackIndex >= len(r.Fallbacks) {
			return Route{}, fmt.Errorf("alias %q has no fallback[%d]", t.AliasKey, t.FallbackIndex)
		}
		r = r.Fallbacks[t.FallbackIndex]
	}
	r.Fallbacks = nil
	return r, nil
}

// ---------- bench prompts ----------

// benchPrompt is one fixed test prompt. Text is locked — the point of a
// repeatable benchmark is a stable, comparable prompt set across runs.
type benchPrompt struct {
	ID       string
	Category string
	Text     string
}

// benchPrompts is the full fixed prompt set: 2 prompts per category x 4
// categories = 8. Every benchTarget is tested against all 8 (full
// cross-matrix), not just its own category's prompts.
var benchPrompts = []benchPrompt{
	{ID: "coding-1", Category: "coding", Text: "Write a Go function `parseDuration(s string) (int, error)` that parses strings like '1h30m', '45m', '2h' into total seconds. Handle invalid input with a clear error. No external libraries."},
	{ID: "coding-2", Category: "coding", Text: "Find and fix the bug in this Go function, explaining the mistake in one sentence:\n\n```go\nfunc lastN(items []int, n int) []int {\n    if n > len(items) {\n        n = len(items)\n    }\n    return items[len(items)-n : len(items)-1]\n}\n```"},
	{ID: "creative-1", Category: "creative", Text: "Write the opening paragraph of a story: a soldier returns to a village that no longer remembers the war he fought in."},
	{ID: "creative-2", Category: "creative", Text: "Write a tense dialogue exchange between two characters who both want the same thing but can't say so directly."},
	{ID: "quick-1", Category: "quick", Text: "Summarize this in 2 sentences: 'The city council voted 6-3 Tuesday night to approve a new transit line connecting the eastern suburbs to downtown, with construction expected to begin in early 2027 and finish by 2030. The $340 million project will add four new stations and is funded through a mix of state grants and a local sales tax increase approved by voters last year. Supporters say it will cut commute times by up to 25 minutes for an estimated 40,000 daily riders, while opponents have raised concerns about construction disruption to small businesses along the route. The council also approved a separate measure to expand bus service in the interim.'"},
	{ID: "quick-2", Category: "quick", Text: "If a train leaves at 3:15pm going 60mph and another leaves the same station at 3:45pm going 90mph in the same direction, when does the second train catch the first?"},
	{ID: "fiction-1", Category: "fiction", Text: "Continue this scene in the same voice: 'The Ranger paused at the treeline, where the bark had gone the color of old bruises. No birds called here, and the silence had a texture, like held breath.'"},
	{ID: "fiction-2", Category: "fiction", Text: "Describe, in-world, what wakes in the dark places between the roots of the world tree when it has not fed in a hundred years."},
}
```

- [ ] **Step 2: Create `bench_test.go` with the failing test**

```go
package main

import "testing"

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
```

- [ ] **Step 3: Run the tests, verify they pass**

Run: `go test -run 'TestRouteForTarget|TestBenchTargetsAndPromptsShape' -v .`
Expected: both tests PASS (this task writes the implementation directly rather than red-green, since `benchTarget`/`benchPrompt` are new types with no prior broken version — but run it now to confirm the file compiles and the logic is right before moving on).

- [ ] **Step 4: Commit**

```bash
git add bench.go bench_test.go
git commit -m "feat(bench): add target/prompt data and routeForTarget resolution"
```

---

### Task 2: callModel — build, send, and parse one model call

**Files:**
- Modify: `bench.go`
- Modify: `bench_test.go`

**Interfaces:**
- Consumes: `Route`, `Config`, `Provider`, `AnthropicRequest`, `AnthropicMessage`, `OpenAIResponse` (all `types.go`); `translateRequest(ar *AnthropicRequest, route Route, cfg *Config) (*OpenAIRequest, error)`, `jsonString(s string) json.RawMessage`, `decodeStringContent(raw json.RawMessage) string`, `truncate(s string, n int) string` (all `translate.go`/`main.go`, same package).
- Produces: `func callModel(ctx context.Context, httpClient *http.Client, cfg *Config, route Route, promptText string, maxTokens int) (responseText string, tokensIn, tokensOut int, latencyMs int64, err error)`.

- [ ] **Step 1: Add imports and `callModel` to `bench.go`**

Replace the import block at the top of `bench.go` (currently just `import ("fmt")`) with:

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)
```

Append to `bench.go`:

```go
// ---------- model calling ----------

// callModel sends one prompt through route exactly as the live proxy
// would (same translateRequest + ExtraBody merge), but calls the upstream
// provider directly — no running proxy daemon required. Always
// non-streaming, so the response is a plain JSON OpenAIResponse.
func callModel(ctx context.Context, httpClient *http.Client, cfg *Config, route Route, promptText string, maxTokens int) (responseText string, tokensIn, tokensOut int, latencyMs int64, err error) {
	ar := &AnthropicRequest{
		Model:     route.Model,
		MaxTokens: maxTokens,
		Messages:  []AnthropicMessage{{Role: "user", Content: jsonString(promptText)}},
		Stream:    false,
	}

	or, err := translateRequest(ar, route, cfg)
	if err != nil {
		return "", 0, 0, 0, fmt.Errorf("translate: %w", err)
	}

	body, err := json.Marshal(or)
	if err != nil {
		return "", 0, 0, 0, fmt.Errorf("marshal request: %w", err)
	}
	if len(route.ExtraBody) > 0 {
		var merged map[string]any
		if err := json.Unmarshal(body, &merged); err == nil {
			for k, v := range route.ExtraBody {
				merged[k] = v
			}
			if newBody, err := json.Marshal(merged); err == nil {
				body = newBody
			}
		}
	}

	prov, ok := cfg.Providers[route.Provider]
	if !ok {
		return "", 0, 0, 0, fmt.Errorf("unknown provider: %s", route.Provider)
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "POST", prov.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", 0, 0, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+prov.APIKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", 0, 0, 0, fmt.Errorf("upstream: %w", err)
	}
	defer resp.Body.Close()
	latencyMs = time.Since(start).Milliseconds()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", 0, 0, latencyMs, fmt.Errorf("upstream %d: %s", resp.StatusCode, truncate(string(b), 300))
	}

	var oresp OpenAIResponse
	if err := json.Unmarshal(b, &oresp); err != nil {
		return "", 0, 0, latencyMs, fmt.Errorf("parse upstream: %w", err)
	}

	if len(oresp.Choices) > 0 && oresp.Choices[0].Message != nil {
		responseText = decodeStringContent(oresp.Choices[0].Message.Content)
	}
	if oresp.Usage != nil {
		tokensIn = oresp.Usage.PromptTokens
		tokensOut = oresp.Usage.CompletionTokens
	}
	return responseText, tokensIn, tokensOut, latencyMs, nil
}
```

- [ ] **Step 2: Add the failing tests to `bench_test.go`**

Replace the import block at the top of `bench_test.go` (currently the single line `import "testing"`) with:

```go
import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)
```

Append:

```go
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
```

- [ ] **Step 3: Run the tests, verify they pass**

Run: `go test -run TestCallModel -v .`
Expected: `TestCallModel`, `TestCallModelUpstreamError`, `TestCallModelUnknownProvider` all PASS.

- [ ] **Step 4: Commit**

```bash
git add bench.go bench_test.go
git commit -m "feat(bench): add callModel to send one request straight to a provider"
```

---

### Task 3: Judge — parseJudgeJSON + judgeResponse

**Files:**
- Modify: `bench.go`
- Modify: `bench_test.go`

**Interfaces:**
- Consumes: `callModel` (Task 2).
- Produces: `type judgeResult struct { Score int; Rationale string }`, `var judgeRoute Route`, `var categoryRubric map[string]string`, `func buildJudgePrompt(category, prompt, response string) (string, error)`, `func parseJudgeJSON(text string) (judgeResult, error)`, `func judgeResponse(ctx context.Context, httpClient *http.Client, cfg *Config, category, prompt, response string) (judgeResult, error)`.

- [ ] **Step 1: Add imports and judge code to `bench.go`**

Add `"strings"` to the import block.

Append to `bench.go`:

```go
// ---------- judging ----------

// judgeRoute is the fixed judge model: free, and deliberately not a
// contestant in any tested category, to avoid a model grading itself or a
// sibling favorably.
var judgeRoute = Route{Provider: "nvidia", Model: "z-ai/glm-5.1", ReasoningEffort: "high"}

// categoryRubric is the per-category grading instruction appended to every
// judge prompt. The 1-10 scale stays constant across categories so scores
// compare cleanly in the summary table.
var categoryRubric = map[string]string{
	"coding":   "Score on correctness (does the logic work), idiomatic Go style, edge-case handling. Code that wouldn't compile or is wrong scores 1-3.",
	"creative": "Score on voice/tone, prose craft, originality. Grammatically fine but flat or generic prose scores 4-6.",
	"quick":    "Score on factual/logical accuracy and conciseness. A wordy but correct answer scores lower than a tight correct one.",
	"fiction":  "Score on consistency with a dark-fantasy register, immersion, and avoiding flat/translated-sounding phrasing.",
}

type judgeResult struct {
	Score     int
	Rationale string
}

func buildJudgePrompt(category, prompt, response string) (string, error) {
	rubric, ok := categoryRubric[category]
	if !ok {
		return "", fmt.Errorf("no rubric for category %q", category)
	}
	return fmt.Sprintf(
		"You are grading an AI model's response for quality.\nTask category: %s\nOriginal prompt: %s\nResponse to grade: %s\n%s\nRespond with ONLY a JSON object: {\"score\": <integer 1-10>, \"rationale\": \"<1-2 sentence explanation>\"}",
		category, prompt, response, rubric,
	), nil
}

// parseJudgeJSON extracts {"score":N,"rationale":"..."} from a judge reply,
// tolerating surrounding prose or markdown code fences around the object.
func parseJudgeJSON(text string) (judgeResult, error) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start == -1 || end == -1 || end < start {
		return judgeResult{}, fmt.Errorf("no JSON object found in judge reply")
	}
	var raw struct {
		Score     int    `json:"score"`
		Rationale string `json:"rationale"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &raw); err != nil {
		return judgeResult{}, fmt.Errorf("invalid judge JSON: %w", err)
	}
	if raw.Score < 1 || raw.Score > 10 {
		return judgeResult{}, fmt.Errorf("judge score %d out of range 1-10", raw.Score)
	}
	return judgeResult{Score: raw.Score, Rationale: raw.Rationale}, nil
}

// judgeResponse grades one generation response. A malformed judge reply is
// retried once before giving up — one bad judge call must not lose the
// whole job's result, just this one job's score.
func judgeResponse(ctx context.Context, httpClient *http.Client, cfg *Config, category, prompt, response string) (judgeResult, error) {
	judgePrompt, err := buildJudgePrompt(category, prompt, response)
	if err != nil {
		return judgeResult{}, err
	}

	text, _, _, _, callErr := callModel(ctx, httpClient, cfg, judgeRoute, judgePrompt, 200)
	if callErr != nil {
		return judgeResult{}, fmt.Errorf("judge call failed: %w", callErr)
	}
	if res, parseErr := parseJudgeJSON(text); parseErr == nil {
		return res, nil
	}

	text2, _, _, _, callErr2 := callModel(ctx, httpClient, cfg, judgeRoute, judgePrompt, 200)
	if callErr2 != nil {
		return judgeResult{}, fmt.Errorf("judge retry call failed: %w", callErr2)
	}
	res2, parseErr2 := parseJudgeJSON(text2)
	if parseErr2 != nil {
		return judgeResult{}, fmt.Errorf("judge_parse_failed: %w", parseErr2)
	}
	return res2, nil
}
```

- [ ] **Step 2: Add the failing tests to `bench_test.go`**

Append:

```go
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
```

- [ ] **Step 3: Run the tests, verify they pass**

Run: `go test -run 'TestParseJudgeJSON|TestJudgeResponse' -v .`
Expected: all subtests and all four top-level tests PASS.

- [ ] **Step 4: Commit**

```bash
git add bench.go bench_test.go
git commit -m "feat(bench): add judge scoring with one retry on malformed replies"
```

---

### Task 4: benchJobResult + runBenchJob

**Files:**
- Modify: `bench.go`
- Modify: `bench_test.go`

**Interfaces:**
- Consumes: `routeForTarget` (Task 1), `callModel` (Task 2), `judgeResponse` (Task 3).
- Produces: `type benchJobResult struct {...}` (JSON tags: `run_id`, `timestamp`, `identity`, `variant`, `model`, `provider`, `category`, `prompt_id`, `score` (`*int`), `rationale` (omitempty), `latency_ms`, `tokens_in`, `tokens_out`, `error` (omitempty), `ResponseText` as `json:"-"`), `type benchJob struct { Target benchTarget; Prompt benchPrompt }`, `func allBenchJobs() []benchJob`, `func runBenchJob(ctx context.Context, httpClient *http.Client, cfg *Config, runID string, job benchJob) benchJobResult`.

- [ ] **Step 1: Add result types and `runBenchJob` to `bench.go`**

Append to `bench.go`:

```go
// ---------- results ----------

// benchJobResult is one completed (config, prompt) job. ResponseText is
// excluded from JSON (json:"-") so it never lands in bench_runs.jsonl —
// full text only goes in the per-run markdown report.
type benchJobResult struct {
	RunID        string `json:"run_id"`
	Timestamp    string `json:"timestamp"`
	Identity     string `json:"identity"`
	Variant      string `json:"variant"`
	Model        string `json:"model"`
	Provider     string `json:"provider"`
	Category     string `json:"category"`
	PromptID     string `json:"prompt_id"`
	Score        *int   `json:"score"`
	Rationale    string `json:"rationale,omitempty"`
	LatencyMs    int64  `json:"latency_ms"`
	TokensIn     int    `json:"tokens_in"`
	TokensOut    int    `json:"tokens_out"`
	Error        string `json:"error,omitempty"`
	ResponseText string `json:"-"`
}

type benchJob struct {
	Target benchTarget
	Prompt benchPrompt
}

// allBenchJobs is the full cross-matrix: every target against every
// prompt, regardless of the prompt's category — 7 targets x 8 prompts.
func allBenchJobs() []benchJob {
	var jobs []benchJob
	for _, t := range benchTargets {
		for _, p := range benchPrompts {
			jobs = append(jobs, benchJob{Target: t, Prompt: p})
		}
	}
	return jobs
}

// runBenchJob runs one (target, prompt) pair end to end: resolve the
// route, generate, then judge. Any failure at any step is captured in
// result.Error and returned (never panics, never aborts the caller's loop)
// so one bad job can't take down the rest of the run.
func runBenchJob(ctx context.Context, httpClient *http.Client, cfg *Config, runID string, job benchJob) benchJobResult {
	result := benchJobResult{
		RunID:    runID,
		Identity: job.Target.Identity,
		Variant:  job.Target.Variant,
		Category: job.Prompt.Category,
		PromptID: job.Prompt.ID,
	}

	route, err := routeForTarget(cfg, job.Target)
	if err != nil {
		result.Timestamp = time.Now().Format(time.RFC3339)
		result.Error = err.Error()
		return result
	}
	result.Model = route.Model
	result.Provider = route.Provider

	responseText, tokensIn, tokensOut, latencyMs, err := callModel(ctx, httpClient, cfg, route, job.Prompt.Text, 4096)
	result.Timestamp = time.Now().Format(time.RFC3339)
	result.LatencyMs = latencyMs
	result.TokensIn = tokensIn
	result.TokensOut = tokensOut
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.ResponseText = responseText

	jr, err := judgeResponse(ctx, httpClient, cfg, job.Prompt.Category, job.Prompt.Text, responseText)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	score := jr.Score
	result.Score = &score
	result.Rationale = jr.Rationale
	return result
}
```

- [ ] **Step 2: Add the failing tests to `bench_test.go`**

Add this helper near the top of `bench_test.go` (after imports), used by this and later tasks:

```go
func intPtr(n int) *int { return &n }
```

Append:

```go
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
		Providers: map[string]Provider{"nvidia": {BaseURL: srv.URL, APIKey: "k"}},
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
```

- [ ] **Step 3: Run the tests, verify they pass**

Run: `go test -run 'TestAllBenchJobsCount|TestRunBenchJob' -v .`
Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add bench.go bench_test.go
git commit -m "feat(bench): add benchJobResult and runBenchJob end-to-end job runner"
```

---

### Task 5: Reporting — history/diff math + table/markdown builders

**Files:**
- Modify: `bench.go`
- Modify: `bench_test.go`

**Interfaces:**
- Consumes: `benchJobResult`, `benchJob` (Task 4).
- Produces: `func loadBenchHistory(path string) ([]benchJobResult, error)`, `func mostRecentRunID(results []benchJobResult, excludeRunID string) string`, `func avgScoreFor(results []benchJobResult, identity, variant, category string) (float64, bool)`, `func filterByRunID(results []benchJobResult, runID string) []benchJobResult`, `func buildDiffLines(history, current []benchJobResult, currentRunID string) []string`, `var benchCategories []string`, `func buildSummaryTable(results []benchJobResult) string`, `func buildMarkdownReport(runID string, jobs []benchJob, results []benchJobResult) string`, `func writeMarkdownReport(runID string, jobs []benchJob, results []benchJobResult) (string, error)`.

- [ ] **Step 1: Add imports and reporting code to `bench.go`**

Add `"os"` and `"path/filepath"` to the import block.

Append to `bench.go`:

```go
// ---------- history & diff ----------

// loadBenchHistory reads bench_runs.jsonl, skipping any corrupt line
// rather than failing the whole load. A missing file is not an error — it
// just means there's no history yet (first run).
func loadBenchHistory(path string) ([]benchJobResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var results []benchJobResult
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var r benchJobResult
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		results = append(results, r)
	}
	return results, nil
}

// mostRecentRunID returns the lexicographically greatest run_id in results
// that isn't excludeRunID. Run IDs are YYYYMMDD-HHMMSS, so string order is
// chronological order. Returns "" if no other run exists.
func mostRecentRunID(results []benchJobResult, excludeRunID string) string {
	best := ""
	for _, r := range results {
		if r.RunID == excludeRunID {
			continue
		}
		if r.RunID > best {
			best = r.RunID
		}
	}
	return best
}

// avgScoreFor averages the score of every scored (non-error) result
// matching identity+variant+category. ok is false when nothing matches.
func avgScoreFor(results []benchJobResult, identity, variant, category string) (avg float64, ok bool) {
	sum, count := 0, 0
	for _, r := range results {
		if r.Identity == identity && r.Variant == variant && r.Category == category && r.Score != nil {
			sum += *r.Score
			count++
		}
	}
	if count == 0 {
		return 0, false
	}
	return float64(sum) / float64(count), true
}

func filterByRunID(results []benchJobResult, runID string) []benchJobResult {
	var out []benchJobResult
	for _, r := range results {
		if r.RunID == runID {
			out = append(out, r)
		}
	}
	return out
}

// buildDiffLines compares current results against the most recent prior
// run in history and returns one formatted line per (identity, variant,
// category) cell present with a scored average in both runs. Returns nil
// when there's no prior run to compare against.
func buildDiffLines(history []benchJobResult, current []benchJobResult, currentRunID string) []string {
	previousRunID := mostRecentRunID(history, currentRunID)
	if previousRunID == "" {
		return nil
	}
	previous := filterByRunID(history, previousRunID)

	var lines []string
	seen := map[string]bool{}
	for _, r := range current {
		key := r.Identity + "/" + r.Variant + " " + r.Category
		if seen[key] {
			continue
		}
		seen[key] = true
		curAvg, curOK := avgScoreFor(current, r.Identity, r.Variant, r.Category)
		prevAvg, prevOK := avgScoreFor(previous, r.Identity, r.Variant, r.Category)
		if !curOK || !prevOK {
			continue
		}
		delta := curAvg - prevAvg
		sign := "+"
		if delta < 0 {
			sign = ""
		}
		lines = append(lines, fmt.Sprintf("%-30s %.1f -> %.1f (%s%.1f)", key, prevAvg, curAvg, sign, delta))
	}
	return lines
}

// ---------- table & markdown report ----------

// benchCategories is the fixed column order for the summary table.
var benchCategories = []string{"coding", "creative", "quick", "fiction"}

// buildSummaryTable renders an identity/variant x category average-score
// table as plain text, in first-seen order of identity/variant.
func buildSummaryTable(results []benchJobResult) string {
	type cell struct {
		sum   int
		count int
	}
	table := map[string]map[string]*cell{}
	var order []string
	seen := map[string]bool{}

	for _, r := range results {
		key := r.Identity + "/" + r.Variant
		if !seen[key] {
			seen[key] = true
			order = append(order, key)
		}
		if table[key] == nil {
			table[key] = map[string]*cell{}
		}
		if table[key][r.Category] == nil {
			table[key][r.Category] = &cell{}
		}
		if r.Score != nil {
			c := table[key][r.Category]
			c.sum += *r.Score
			c.count++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n  %-22s", "")
	for _, cat := range benchCategories {
		fmt.Fprintf(&b, "%-12s", cat)
	}
	fmt.Fprintln(&b)
	for _, key := range order {
		fmt.Fprintf(&b, "  %-22s", key)
		for _, cat := range benchCategories {
			c := table[key][cat]
			if c == nil || c.count == 0 {
				fmt.Fprintf(&b, "%-12s", "-")
				continue
			}
			fmt.Fprintf(&b, "%-12s", fmt.Sprintf("%.1f", float64(c.sum)/float64(c.count)))
		}
		fmt.Fprintln(&b)
	}
	return b.String()
}

// buildMarkdownReport renders the full per-job detail (prompt, response,
// score, rationale) for one run as a markdown string. jobs and results
// must be the same length and index-aligned (as produced by cmdBench).
func buildMarkdownReport(runID string, jobs []benchJob, results []benchJobResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Bench run %s\n\n", runID)
	for i, r := range results {
		fmt.Fprintf(&b, "## %s/%s · %s\n\n", r.Identity, r.Variant, r.PromptID)
		fmt.Fprintf(&b, "**Model:** %s (%s)\n\n", r.Model, r.Provider)
		fmt.Fprintf(&b, "**Prompt:**\n\n%s\n\n", jobs[i].Prompt.Text)
		if r.Error != "" {
			fmt.Fprintf(&b, "**Error:** %s\n\n---\n\n", r.Error)
			continue
		}
		fmt.Fprintf(&b, "**Response:**\n\n%s\n\n", r.ResponseText)
		fmt.Fprintf(&b, "**Score:** %d/10 — %s\n\n---\n\n", *r.Score, r.Rationale)
	}
	return b.String()
}

func writeMarkdownReport(runID string, jobs []benchJob, results []benchJobResult) (string, error) {
	if err := os.MkdirAll("bench_runs", 0755); err != nil {
		return "", err
	}
	path := filepath.Join("bench_runs", runID+".md")
	if err := os.WriteFile(path, []byte(buildMarkdownReport(runID, jobs, results)), 0644); err != nil {
		return "", err
	}
	return path, nil
}
```

- [ ] **Step 2: Add the failing tests to `bench_test.go`**

Replace the import block at the top of `bench_test.go` (currently `context, net/http, net/http/httptest, testing`) with:

```go
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)
```

Append:

```go
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
```

- [ ] **Step 3: Run the tests, verify they pass**

Run: `go test -run 'TestBenchJobResultJSONShape|TestAvgScoreFor|TestMostRecentRunID|TestBuildDiffLines|TestLoadBenchHistory|TestBuildSummaryTable|TestBuildMarkdownReport' -v .`
Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add bench.go bench_test.go
git commit -m "feat(bench): add history diff, summary table, and markdown report builders"
```

---

### Task 6: cmdBench orchestration (worker pool)

**Files:**
- Modify: `bench.go`

**Interfaces:**
- Consumes: `allBenchJobs`, `runBenchJob` (Task 4); `loadBenchHistory`, `buildDiffLines`, `buildSummaryTable`, `writeMarkdownReport` (Task 5); existing `defaultEnvPath()`, `defaultConfigPath()`, `loadDotenv(path string)`, `loadConfig(path string) (*Config, error)` from `cli.go`/`main.go`.
- Produces: `func cmdBench()`.

No dedicated unit test for this task — it's pure orchestration (goroutines, real stdout, real file I/O against hardcoded `~/.config/acc/...` paths) wiring together pieces that are each already tested. Per the design spec's own testing section, this function is verified by actually running it (Task 8), not mocked.

- [ ] **Step 1: Add imports and `cmdBench` to `bench.go`**

Add `"sync"` and `"sync/atomic"` to the import block.

Append to `bench.go`:

```go
// ---------- orchestration ----------

const benchConcurrency = 5

// cmdBench runs the full cross-matrix benchmark: every benchTarget against
// every benchPrompt, capped at benchConcurrency jobs in flight at once.
// Results stream to bench_runs.jsonl as each job finishes (not batched at
// the end, so a mid-run crash doesn't lose already-finished work), then a
// summary table, a diff against the previous run, and a full markdown
// report are printed/written.
func cmdBench() {
	loadDotenv(defaultEnvPath())
	cfg, err := loadConfig(defaultConfigPath())
	if err != nil {
		fmt.Printf("  No config found. Run `acc setup` first. (%v)\n", err)
		return
	}

	runID := time.Now().Format("20060102-150405")
	jobs := allBenchJobs()

	history, err := loadBenchHistory("bench_runs.jsonl")
	if err != nil {
		fmt.Printf("  Could not read bench_runs.jsonl history: %v\n", err)
		history = nil
	}

	jsonlFile, err := os.OpenFile("bench_runs.jsonl", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("  Could not open bench_runs.jsonl: %v\n", err)
		return
	}
	defer jsonlFile.Close()

	httpClient := &http.Client{Timeout: 5 * time.Minute}
	results := make([]benchJobResult, len(jobs))

	fmt.Printf("\n  acc bench — run %s, %d jobs (%d concurrent)\n\n", runID, len(jobs), benchConcurrency)

	var wg sync.WaitGroup
	sem := make(chan struct{}, benchConcurrency)
	var completed atomic.Int32
	var mu sync.Mutex

	for i, job := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, job benchJob) {
			defer wg.Done()
			defer func() { <-sem }()

			result := runBenchJob(context.Background(), httpClient, cfg, runID, job)
			results[i] = result

			mu.Lock()
			defer mu.Unlock()
			n := completed.Add(1)
			line, _ := json.Marshal(result)
			jsonlFile.Write(append(line, '\n'))

			status := "ERROR: " + result.Error
			if result.Error == "" {
				status = fmt.Sprintf("score %d/10", *result.Score)
			}
			fmt.Printf("  [%d/%d] %s/%s · %s ... %dms, %s\n",
				n, len(jobs), result.Identity, result.Variant, result.PromptID, result.LatencyMs, status)
		}(i, job)
	}
	wg.Wait()

	fmt.Print(buildSummaryTable(results))

	diffLines := buildDiffLines(history, results, runID)
	if len(diffLines) == 0 {
		fmt.Print("\n  (first run — no history to diff against)\n")
	} else {
		fmt.Print("\n  vs previous run:\n\n")
		for _, line := range diffLines {
			fmt.Printf("  %s\n", line)
		}
	}

	reportPath, err := writeMarkdownReport(runID, jobs, results)
	if err != nil {
		fmt.Printf("\n  Could not write markdown report: %v\n", err)
		return
	}
	fmt.Printf("\n  Full report: %s\n\n", reportPath)
}
```

- [ ] **Step 2: Build to confirm it compiles**

Run: `go build ./...`
Expected: builds with no errors. `cmdBench` isn't called from anywhere yet (that's Task 7), so an "unused function" concern doesn't apply in Go — but confirm there are no type errors or unused imports.

- [ ] **Step 3: Run the full test suite to confirm nothing broke**

Run: `go test -race ./...`
Expected: PASS (same set of tests as Tasks 1-5, nothing new — this step just confirms `cmdBench` compiling alongside everything else didn't break anything).

- [ ] **Step 4: Commit**

```bash
git add bench.go
git commit -m "feat(bench): add cmdBench worker-pool orchestration"
```

---

### Task 7: Wire the `bench` subcommand into the CLI

**Files:**
- Modify: `cli.go:39-58` (the `dispatch` function and `printHelp`)
- Modify: `.gitignore`

**Interfaces:**
- Consumes: `cmdBench()` (Task 6).
- Produces: `acc bench` becomes a recognized subcommand.

- [ ] **Step 1: Add the `bench` case to `dispatch` in `cli.go`**

In `cli.go`, the current `dispatch` function (lines 39-58) reads:

```go
func dispatch(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[1] {
	case "setup", "init":
		cmdSetup()
	case "doctor", "check":
		cmdDoctor()
	case "models", "list":
		cmdModels()
	case "claude", "run":
		cmdClaude(args[2:])
	case "help", "--help", "-h":
		printHelp()
	default:
		return false
	}
	return true
}
```

Change it to:

```go
func dispatch(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[1] {
	case "setup", "init":
		cmdSetup()
	case "doctor", "check":
		cmdDoctor()
	case "models", "list":
		cmdModels()
	case "bench":
		cmdBench()
	case "claude", "run":
		cmdClaude(args[2:])
	case "help", "--help", "-h":
		printHelp()
	default:
		return false
	}
	return true
}
```

- [ ] **Step 2: Update `printHelp` in `cli.go`**

The current `printHelp` function (lines 60-73) reads:

```go
func printHelp() {
	fmt.Print(`acc — point Claude Code at cheaper models

Usage:
  acc                 Start the proxy (use -tui for the dashboard)
  acc setup           Interactive first-time setup (keys + config)
  acc doctor          Test that your provider keys work
  acc models          List the model names you can use
  acc claude [args]   Start the proxy and launch Claude Code through it
  acc help            Show this help

First time? Run:  acc setup
`)
}
```

Change it to:

```go
func printHelp() {
	fmt.Print(`acc — point Claude Code at cheaper models

Usage:
  acc                 Start the proxy (use -tui for the dashboard)
  acc setup           Interactive first-time setup (keys + config)
  acc doctor          Test that your provider keys work
  acc models          List the model names you can use
  acc bench           Benchmark every persona + fallback, judged for quality
  acc claude [args]   Start the proxy and launch Claude Code through it
  acc help            Show this help

First time? Run:  acc setup
`)
}
```

- [ ] **Step 3: Add bench runtime artifacts to `.gitignore`**

Read the current `.gitignore` (in the repo root) and find this block:

```
# Runtime metrics log
test_runs.jsonl
```

Change it to:

```
# Runtime metrics log
test_runs.jsonl
bench_runs.jsonl
bench_runs/
```

- [ ] **Step 4: Build and run the full test suite**

Run: `make lint`
Expected: `gofmt` reports no unformatted files, `go vet ./...` clean, `go build ./...` succeeds, `go test -race ./...` PASS.

- [ ] **Step 5: Verify the subcommand is recognized**

Run: `go run . bench --help 2>&1 | head -5` is not applicable (bench takes no flags) — instead run: `go run . help`
Expected: output includes the line `acc bench           Benchmark every persona + fallback, judged for quality`.

- [ ] **Step 6: Commit**

```bash
git add cli.go .gitignore
git commit -m "feat(bench): wire acc bench into the CLI dispatch and help text"
```

---

### Task 8: Build, full test suite, and a real benchmark run

**Files:** none (verification only).

This task has no code changes — it's the checkpoint that confirms Tasks 1-7 actually work together against the real, live config and providers, and produces the first real answer to "which model wins which category."

- [ ] **Step 1: Run the full lint/build/test gate**

Run: `make lint`
Expected: clean gofmt, clean vet, successful build, all tests PASS (race detector on).

- [ ] **Step 2: Build the binary**

Run: `make build`
Expected: produces `./acc` with no errors.

- [ ] **Step 3: Run the real benchmark**

Run: `./acc bench`
Expected: prints `acc bench — run <id>, 56 jobs (5 concurrent)`, then 56 progress lines (`[n/56] identity/variant · prompt-id ... Nms, score N/10` or an `ERROR:` line), then the summary table, then either `(first run — no history to diff against)` or a diff block, then `Full report: bench_runs/<id>.md`. Total wall time roughly 4-8 minutes (Opus's primary, nemotron-3-ultra-550b, is the long pole — CLAUDE.md notes it can take 1-2+ min per call).

If any job errors, that's expected and fine as long as it's a small minority — the whole point of the per-job error handling in Task 4 is that one bad call (rate limit, timeout, a provider hiccup) doesn't take down the other 55. A large fraction of errors (e.g. every NVIDIA call failing) signals a real problem — stop and check `~/.config/acc/.env` has working keys (`acc doctor` is the fast way to check) before re-running.

- [ ] **Step 4: Inspect the output with True**

Run: `cat bench_runs/<the run's id>.md | head -100` and show the printed summary table from Step 3.

Report back: the summary table (which identity/variant scores best per category), anything that errored and why, and the full path to the markdown report so True can read the rest himself. This is the actual deliverable of "test the models with it" — don't just confirm the tool runs, confirm what it found.

- [ ] **Step 5: Decide on commit**

`bench_runs.jsonl` and `bench_runs/<id>.md` are gitignored (Task 7) and won't show up in `git status` as untracked-to-commit — nothing to stage from this task. If True wants to keep a particular run's report outside the gitignored runtime path (e.g. to share or archive one good baseline run), that's a separate explicit ask, not assumed here.

---

## Self-Review Notes

**Spec coverage:** every section of `2026-07-01-model-benchmark-design.md` maps to a task — Motivation/Scope → Task 1 (full cross-matrix data), Test matrix → Task 1 (`benchTargets`), Prompts → Task 1 (`benchPrompts`, exact locked text, no placeholders), Judging → Task 3, Architecture → Tasks 2/6, Concurrency → Task 6 (`benchConcurrency = 5`), Output (JSONL/terminal/markdown) → Tasks 4-6, CLI usage → Task 7, Testing → Tasks 1-5 each carry their own tests, "test the models with it" (the user's explicit go-ahead) → Task 8.

**Placeholder scan:** no TBD/TODO markers; every prompt is the exact locked text from the spec; every code block is complete and compiles as part of its task, not sketched.

**Type consistency:** `benchTarget`, `benchPrompt`, `benchJob`, `benchJobResult`, `judgeResult` are defined once (Tasks 1/3/4) and referenced identically by field name in every later task — checked `routeForTarget`/`callModel`/`judgeResponse`/`runBenchJob`/`buildDiffLines`/`buildSummaryTable`/`buildMarkdownReport` all match the signatures declared in their producing task's Interfaces block.
