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

func TestConfiguredSolTerraLunaChainsMatchTheirRoles(t *testing.T) {
	raw, err := os.ReadFile("config.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	s := testServer(&cfg)
	wantChains := map[string][]string{
		codexOpusID:   {codexOpusID, "acc-openrouter-nemotron-ultra", "acc-gemini-3.5-flash", "acc-minimax-m3"},
		codexSonnetID: {codexSonnetID, "acc-openrouter-hy3", "acc-gemini-3.5-flash", "acc-nemotron-super"},
		codexHaikuID:  {codexHaikuID, "acc-nemotron-super", "acc-minimax-m3"},
	}
	for model, want := range wantChains {
		chain, err := s.responseModelChain(model)
		if err != nil {
			t.Fatal(err)
		}
		if len(chain) != len(want) {
			t.Fatalf("%s chain length = %d, want %d: %+v", model, len(chain), len(want), chain)
		}
		for i := range want {
			if chain[i].ID != want[i] {
				t.Fatalf("%s chain[%d] = %s, want %s", model, i, chain[i].ID, want[i])
			}
		}
	}

	imageWithTools := &ResponsesRequest{
		Model: codexOpusID,
		Input: json.RawMessage(`[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}]`),
		Tools: []ResponsesTool{{Type: "function", Name: "inspect_image", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}
	chain, _ := s.responseModelChain(codexOpusID)
	selected, err := selectResponseModelChain(imageWithTools, chain)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].ID != "acc-gemini-3.5-flash" {
		t.Fatalf("Sol image+tool route = %+v, want Gemini only", selected)
	}
}
