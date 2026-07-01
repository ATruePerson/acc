package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
