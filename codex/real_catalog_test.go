package codex

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodexCatalogUsesProviderPrefixedRealModelsAndNoAliases(t *testing.T) {
	cfg := TestConfig()
	models := NamedModels(cfg)
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
		if EncodeCodexSlug(model.Route.Provider, model.Route.Model) != model.ID {
			t.Fatalf("model %q hides route %+v", model.ID, model.Route)
		}
	}
	b := string(ModelCatalogJSON(cfg))
	for _, forbidden := range []string{`"slug": "opus"`, `"slug": "sonnet"`, `"slug": "haiku"`, "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		if strings.Contains(b, forbidden) {
			t.Fatalf("Codex catalog leaked alias %q: %s", forbidden, b)
		}
	}
}

func TestCodexCatalogKeepsSameUpstreamModelDistinctAcrossProviders(t *testing.T) {
	cfg := TestConfig()
	cfg.Providers["other"] = Provider{BaseURL: "https://other.test", APIKey: "other-key"}
	cfg.Models["other-glm"] = ModelCapability{
		Provider: "other", Model: "z-ai/glm-5.2", Enabled: true, StreamingSupport: true,
	}
	ids := map[string]bool{}
	for _, model := range NamedModels(cfg) {
		ids[model.ID] = true
	}
	if !ids["nvidia/z-ai~sglm-5.2"] || !ids["other/z-ai~sglm-5.2"] {
		t.Fatalf("provider-distinct IDs missing: %+v", ids)
	}
}

func TestCodexCatalogNeverSerializesProviderSecrets(t *testing.T) {
	cfg := TestConfig()
	cfg.Providers["nvidia"] = Provider{BaseURL: "https://nvidia.test", APIKey: "top-secret-key"}
	b, err := json.Marshal(ModelCatalogEntries(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "top-secret-key") {
		t.Fatalf("catalog leaked provider credential: %s", b)
	}
}
