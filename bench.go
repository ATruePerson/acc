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
	"sync"
	"sync/atomic"
	"time"
)

// ---------- bench targets ----------

// benchTarget is one model configuration under test: a persona identity,
// which variant (primary or fallback) of that persona's alias, and how to
// resolve it from the live config.
type benchTarget struct {
	Identity      string
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
	{Identity: "opus", Variant: "primary", AliasKey: "anthropic/claude-opus", FallbackIndex: -1},
	{Identity: "opus", Variant: "fallback", AliasKey: "anthropic/claude-opus", FallbackIndex: 1},
	{Identity: "sonnet", Variant: "primary", AliasKey: "anthropic/claude-sonnet", FallbackIndex: -1},
	{Identity: "sonnet", Variant: "fallback", AliasKey: "anthropic/claude-sonnet", FallbackIndex: 0},
	{Identity: "haiku", Variant: "primary", AliasKey: "anthropic/claude-haiku", FallbackIndex: -1},
	{Identity: "fable/mythos", Variant: "primary", AliasKey: "anthropic/claude-fable", FallbackIndex: -1},
	{Identity: "fable/mythos", Variant: "fallback", AliasKey: "anthropic/claude-fable", FallbackIndex: 0},
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
// cross-matrix).
var benchPrompts = []benchPrompt{
	{ID: "reasoning-1", Category: "reasoning", Text: "Three boxes sit on a table. One contains only apples, one contains only oranges, and one contains both apples and oranges. All three boxes are mislabeled. You can reach into one box and pull out one piece of fruit without looking inside. How do you determine the correct labels for all three boxes? Explain your reasoning step by step."},
	{ID: "reasoning-2", Category: "reasoning", Text: "A bat and a ball cost $1.10 in total. The bat costs $1.00 more than the ball. How much does the ball cost? Think carefully, then explain the correct answer and why the intuitive answer is wrong."},
	{ID: "logic-1", Category: "logic", Text: "All cats are mammals. All mammals are warm-blooded. Some warm-blooded animals are not cats. Which of the following conclusions are valid? (a) All cats are warm-blooded. (b) Some warm-blooded animals are not mammals. (c) If something is not warm-blooded, it cannot be a cat. For each, say valid or invalid and why."},
	{ID: "logic-2", Category: "logic", Text: "If it rains, the ground gets wet. The ground is wet. Does that mean it rained? Explain why or why not, and name the logical fallacy if there is one."},
	{ID: "math-1", Category: "math", Text: "A train leaves Station A traveling toward Station B at 80 km/h. Another train leaves Station B traveling toward Station A at 100 km/h. The stations are 360 km apart. At the same time, a bird flies from Station A toward Station B at 150 km/h. When it meets the second train, it turns around instantly and flies back toward the first, repeating this pattern until the trains meet. How far does the bird travel in total? Solve it."},
	{ID: "math-2", Category: "math", Text: "What is the value of the infinite sum 1 + 1/2 + 1/4 + 1/8 + 1/16 + ...? Explain why this sum converges and what it converges to, using reasoning a high school student could follow."},
	{ID: "science-1", Category: "science", Text: "Explain why the sky appears blue during the day but appears red or orange at sunrise and sunset. Use physics concepts — Rayleigh scattering, light wavelength, and atmospheric path length — in your explanation."},
	{ID: "science-2", Category: "science", Text: "If you have a sealed balloon filled with air at room temperature and you place it in a freezer, what happens to its volume? Use the ideal gas law to explain the change in volume, pressure, and what would happen if the balloon were instead made of a rigid material."},
}

// ---------- model calling ----------

// benchMaxAttempts is how many times a single bench call will try before
// giving up, and benchBackoff is the wait before each retry. benchBackoff
// is a var so tests can swap in a zero-wait version instead of sleeping
// real seconds.
const benchMaxAttempts = 4

var benchBackoff = func(attempt int) time.Duration {
	return time.Duration(1<<attempt) * time.Second // 2s, 4s, 8s
}

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
	var b []byte
	var status int
	for attempt := 1; attempt <= benchMaxAttempts; attempt++ {
		req, reqErr := http.NewRequestWithContext(ctx, "POST", prov.BaseURL+"/chat/completions", bytes.NewReader(body))
		if reqErr != nil {
			return "", 0, 0, 0, fmt.Errorf("build request: %w", reqErr)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+prov.APIKey)

		resp, doErr := httpClient.Do(req)
		if doErr != nil {
			return "", 0, 0, time.Since(start).Milliseconds(), fmt.Errorf("upstream: %w", doErr)
		}
		b, _ = io.ReadAll(resp.Body)
		status = resp.StatusCode
		resp.Body.Close()

		// A rate-limit (429) or transient provider error (503) is worth waiting
		// out — under a wide concurrent run the shared provider (esp. NVIDIA)
		// bursts past its limit, but recovers in seconds. Retry with exponential
		// backoff; any other status (success or a real 4xx) returns immediately.
		if (status == 429 || status == 503) && attempt < benchMaxAttempts {
			select {
			case <-ctx.Done():
				return "", 0, 0, time.Since(start).Milliseconds(), ctx.Err()
			case <-time.After(benchBackoff(attempt)):
			}
			continue
		}
		break
	}
	latencyMs = time.Since(start).Milliseconds()

	if status >= 400 {
		return "", 0, 0, latencyMs, fmt.Errorf("upstream %d: %s", status, truncate(string(b), 300))
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
// sibling favorably. It runs on Gemini, NOT NVIDIA — most contestants use
// NVIDIA, so keeping the judge (one call per job) off that provider stops
// the judge and the models from starving the same rate limit. No
// reasoning_effort: a 1-10 score with a one-line rationale doesn't need
// it, and leaving it off avoids any provider param-compat risk.
var judgeRoute = Route{Provider: "gemini", Model: "models/gemini-3.1-pro-preview"}

// judgeMaxTokens must be generous: gemini-3.1-pro is a thinking model that
// spends most of a small budget on hidden reasoning before emitting any
// visible text. At 200 it burned ~192 on thinking and returned a truncated
// `{"score":5` with no closing brace, failing every parse. 2000 leaves
// ample room for the reasoning plus the full one-line-rationale JSON.
const judgeMaxTokens = 2000

// categoryRubric is the per-category grading instruction appended to every
// judge prompt. The 1-10 scale stays constant across categories so scores
// compare cleanly in the summary table.
var categoryRubric = map[string]string{
	"reasoning": "Score on correctness of the final answer, logical soundness of the step-by-step reasoning, and whether it identifies common pitfalls. A wrong final answer scores 1-3 even if the reasoning path is interesting.",
	"logic":     "Score on correctness of the logical analysis, proper identification of valid/invalid conclusions, and clear explanation of why. Missing the fallacy or incorrectly classifying valid vs invalid scores 1-3.",
	"math":      "Score on mathematical accuracy, clarity of derivation, and ability to explain concepts accessibly. A numerically wrong answer scores 1-4 regardless of how good the explanation sounds.",
	"science":   "Score on factual accuracy, correct use of scientific concepts, and clarity of explanation. False or misleading physics scores 1-3.",
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

	text, _, _, _, callErr := callModel(ctx, httpClient, cfg, judgeRoute, judgePrompt, judgeMaxTokens)
	if callErr != nil {
		return judgeResult{}, fmt.Errorf("judge call failed: %w", callErr)
	}
	if res, parseErr := parseJudgeJSON(text); parseErr == nil {
		return res, nil
	}

	text2, _, _, _, callErr2 := callModel(ctx, httpClient, cfg, judgeRoute, judgePrompt, judgeMaxTokens)
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
var benchCategories = []string{"reasoning", "logic", "math", "science"}

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
		fmt.Print("\n  (first run with new categories — no history to diff against)\n")
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
