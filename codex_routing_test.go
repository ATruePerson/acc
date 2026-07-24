package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestRouteForUsesExactRequestedCodexModel(t *testing.T) {
	s := testServer(codexTestConfig())

	cases := []struct {
		selected string
		want     string
	}{
		{codexOpusID, "z-ai/glm-5.2"},
		{codexSonnetID, "big-pickle"},
		{codexHaikuID, "stepfun-ai/step-3.7-flash"},
	}

	for _, tc := range cases {
		t.Run(tc.selected, func(t *testing.T) {
			route, err := s.routeFor(tc.selected)
			if err != nil {
				t.Fatal(err)
			}
			if route.Model != tc.want {
				t.Fatalf("route model = %q, want %q", route.Model, tc.want)
			}
		})
	}
}

func TestRouteForRejectsUnregisteredCodexModel(t *testing.T) {
	s := testServer(codexTestConfig())
	if _, err := s.routeFor("gpt-5.6-unknown"); err == nil {
		t.Fatal("expected unregistered Codex model to be rejected")
	}
}

func TestResponseModelChainAcceptsLegacyCodexIDs(t *testing.T) {
	s := testServer(codexTestConfig())
	cases := []struct {
		legacy  string
		current string
	}{
		{"opus", codexOpusID},
		{"sonnet", codexSonnetID},
		{"haiku", codexHaikuID},
		{"openai/codex-5.6-sol", codexOpusID},
	}
	for _, tc := range cases {
		t.Run(tc.legacy, func(t *testing.T) {
			chain, err := s.responseModelChain(tc.legacy)
			if err != nil {
				t.Fatal(err)
			}
			if len(chain) == 0 || chain[0].ID != tc.current {
				t.Fatalf("resolved chain = %+v, want primary %q", chain, tc.current)
			}
		})
	}
}

func TestNormalizeLegacyResponsesRequestMapsOldHighEffort(t *testing.T) {
	req := &ResponsesRequest{
		Model:     "opus",
		Reasoning: &ResponsesReasoning{Effort: "high"},
	}
	normalizeLegacyResponsesRequest(req)
	if req.Model != codexOpusID || req.Reasoning.Effort != "max" {
		t.Fatalf("normalized request = %+v, want model=%q effort=max", req, codexOpusID)
	}
}

func TestConfiguredSelectedCodexModelsHaveNoFallbackChains(t *testing.T) {
	raw, err := os.ReadFile("config.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	s := testServer(&cfg)
	want := map[string]struct {
		provider string
		model    string
		images   bool
	}{
		"nvidia/nvidia/nemotron-3-ultra-550b-a55b": {provider: "nvidia", model: "nvidia/nemotron-3-ultra-550b-a55b"},
		"openrouter/poolside/laguna-s-2.1:free":    {provider: "openrouter", model: "poolside/laguna-s-2.1:free"},
		"nvidia/stepfun-ai/step-3.7-flash":         {provider: "nvidia", model: "stepfun-ai/step-3.7-flash", images: true},
	}
	for selected, expected := range want {
		chain, err := s.responseModelChain(selected)
		if err != nil {
			t.Fatalf("%s: %v", selected, err)
		}
		if len(chain) != 1 {
			t.Fatalf("%s chain = %+v, want exactly one explicitly selected model", selected, chain)
		}
		if chain[0].Route.Provider != expected.provider || chain[0].Route.Model != expected.model {
			t.Fatalf("%s route = %s/%s, want %s/%s", selected, chain[0].Route.Provider, chain[0].Route.Model, expected.provider, expected.model)
		}
		if chain[0].Capability.ImageInputSupport != expected.images {
			t.Fatalf("%s image support = %v, want %v", selected, chain[0].Capability.ImageInputSupport, expected.images)
		}
		if chain[0].Fallback || chain[0].ImageOnly {
			t.Fatalf("%s was unexpectedly marked as an internal fallback: %+v", selected, chain[0])
		}
	}
}
