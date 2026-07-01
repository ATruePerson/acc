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
