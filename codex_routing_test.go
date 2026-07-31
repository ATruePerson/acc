package main

import (
	"testing"

	"github.com/ATruePerson/acc/codex"
)

func TestRouteForUsesExactRequestedCodexModel(t *testing.T) {
	s := testServer(codex.TestConfig())

	cases := []struct {
		selected string
		want     string
	}{
		{"nvidia/z-ai~sglm-5.2", "z-ai/glm-5.2"},
		{"opencode/big-pickle", "big-pickle"},
		{"nvidia/stepfun-ai~sstep-3.7-flash", "stepfun-ai/step-3.7-flash"},
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
	s := testServer(codex.TestConfig())
	if _, err := s.routeFor("gpt-5.6-unknown"); err == nil {
		t.Fatal("expected unregistered Codex model to be rejected")
	}
}

func TestResponseModelChainAcceptsProviderModelIDs(t *testing.T) {
	s := testServer(codex.TestConfig())
	cases := []struct {
		model string
	}{
		{"nvidia/z-ai~sglm-5.2"},
		{"opencode/big-pickle"},
		{"nvidia/stepfun-ai~sstep-3.7-flash"},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			chain, err := s.responseModelChain(tc.model)
			if err != nil {
				t.Fatal(err)
			}
			if len(chain) == 0 || chain[0].ID != tc.model {
				t.Fatalf("resolved chain = %+v, want primary %q", chain, tc.model)
			}
		})
	}
}

func TestResponseModelChainRejectsBareCodexModelIDs(t *testing.T) {
	s := testServer(codex.TestConfig())
	for _, bare := range []string{"sol", "terra", "luna", "unknown"} {
		t.Run(bare, func(t *testing.T) {
			_, err := s.responseModelChain(bare)
			if err == nil {
				t.Fatalf("bare model ID %q should be rejected", bare)
			}
		})
	}
}

func TestResponseModelChainReturnsExactlyOneModel(t *testing.T) {
	cfg := codex.TestConfig()
	s := testServer(cfg)
	chain, err := s.responseModelChain("nvidia/z-ai~sglm-5.2")
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 1 {
		t.Fatalf("chain = %+v, want exactly one model", chain)
	}
}

func TestConfiguredSelectedCodexModelsRouteExactly(t *testing.T) {
	cfg, err := loadConfig(".")
	if err != nil {
		t.Fatal(err)
	}
	s := testServer(cfg)
	want := map[string]struct {
		provider string
		model    string
		images   bool
	}{
		"nvidia/nvidia~snemotron-3-ultra-550b-a55b": {provider: "nvidia", model: "nvidia/nemotron-3-ultra-550b-a55b"},
		"openrouter/poolside~slaguna-s-2.1:free":    {provider: "openrouter", model: "poolside/laguna-s-2.1:free"},
		"nvidia/stepfun-ai~sstep-3.7-flash":         {provider: "nvidia", model: "stepfun-ai/step-3.7-flash", images: true},
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
		_ = chain
	}
}

func TestResponsesRealModelIDRoutesExactly(t *testing.T) {
	s := testServer(codex.TestConfig())
	chain, err := s.responseModelChain("nvidia/z-ai~sglm-5.2")
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 1 || chain[0].Route.Provider != "nvidia" || chain[0].Route.Model != "z-ai/glm-5.2" {
		t.Fatalf("exact real-model chain = %+v", chain)
	}
}
