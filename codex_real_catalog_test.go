package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodexCatalogUsesProviderPrefixedRealModelsAndNoAliases(t *testing.T) {
	cfg := codexTestConfig()
	models := codexNamedModels(cfg)
	if len(models) != 3 {
		t.Fatalf("real catalog models = %d, want 3: %+v", len(models), models)
	}
	want := map[string]bool{
		"nvidia/z-ai~sglm-5.2":              true,
		"opencode/big-pickle":               true,
		"nvidia/stepfun-ai~sstep-3.7-flash": true,
	}
	for _, model := range models {
		if !want[model.ID] {
			t.Fatalf("unexpected model ID %q", model.ID)
		}
		if encodeCodexSlug(model.Route.Provider, model.Route.Model) != model.ID {
			t.Fatalf("model %q hides route %+v", model.ID, model.Route)
		}
	}
	b := string(codexModelCatalogJSON(cfg))
	for _, forbidden := range []string{`"slug": "opus"`, `"slug": "sonnet"`, `"slug": "haiku"`, "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		if strings.Contains(b, forbidden) {
			t.Fatalf("Codex catalog leaked alias %q: %s", forbidden, b)
		}
	}
}

func TestCodexCatalogKeepsSameUpstreamModelDistinctAcrossProviders(t *testing.T) {
	cfg := codexTestConfig()
	cfg.Providers["other"] = Provider{BaseURL: "https://other.test", APIKey: "other-key"}
	cfg.Models["other-glm"] = ModelCapability{
		Provider: "other", Model: "z-ai/glm-5.2", Enabled: true, StreamingSupport: true,
	}
	ids := map[string]bool{}
	for _, model := range codexNamedModels(cfg) {
		ids[model.ID] = true
	}
	if !ids["nvidia/z-ai~sglm-5.2"] || !ids["other/z-ai~sglm-5.2"] {
		t.Fatalf("provider-distinct IDs missing: %+v", ids)
	}
}

func TestResponsesRealModelIDRoutesExactlyWithoutClaudeFallback(t *testing.T) {
	s := testServer(codexTestConfig())
	chain, err := s.responseModelChain("nvidia/z-ai~sglm-5.2")
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 1 || chain[0].Route.Provider != "nvidia" || chain[0].Route.Model != "z-ai/glm-5.2" || chain[0].Fallback {
		t.Fatalf("exact real-model chain = %+v", chain)
	}
}

func TestCodexCatalogNeverSerializesProviderSecrets(t *testing.T) {
	cfg := codexTestConfig()
	cfg.Providers["nvidia"] = Provider{BaseURL: "https://nvidia.test", APIKey: "top-secret-key"}
	b, err := json.Marshal(codexModelCatalogEntries(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "top-secret-key") {
		t.Fatalf("catalog leaked provider credential: %s", b)
	}
}
