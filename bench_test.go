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
