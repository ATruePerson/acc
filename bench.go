package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
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
