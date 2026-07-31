package codex

import (
	"strings"
	"testing"
)

func TestSanitizeCodexConfigStripsRootACCBlock(t *testing.T) {
	// Config with a stale ACC-owned block plus a leftover subscription effort.
	original := `model_reasoning_effort = "medium"
service_tier = "default"
personality = "friendly"

# BEGIN ACC CODEX OWNED
model = "nvidia/nvidia~snemotron-3-ultra-550b-a55b"
model_reasoning_effort = ""
model_provider = "acc"
model_catalog_json = "/Users/kabir/.codex/acc-models.json"
web_search = "disabled"
# END ACC CODEX OWNED`

	sanitized := sanitizeCodexConfig(original, true, true)

	if strings.Contains(sanitized, "BEGIN ACC CODEX OWNED") || strings.Contains(sanitized, "END ACC CODEX OWNED") {
		t.Fatalf("Sanitized config still contains ACC ownership markers:\n%s", sanitized)
	}
	if strings.Contains(sanitized, "model_reasoning_effort") {
		t.Fatalf("Sanitized config still contains model_reasoning_effort:\n%s", sanitized)
	}
	if strings.Contains(sanitized, "model_provider") || strings.Contains(sanitized, "model_catalog_json") {
		t.Fatalf("Sanitized config still contains ACC routing keys:\n%s", sanitized)
	}
	if !strings.Contains(sanitized, `service_tier = "default"`) || !strings.Contains(sanitized, `personality = "friendly"`) {
		t.Fatalf("Sanitized config dropped unrelated user settings:\n%s", sanitized)
	}
}

func TestRenderCodexACCConfigRewritesStaleOwnedBlock(t *testing.T) {
	original := `service_tier = "default"
personality = "friendly"

# BEGIN ACC CODEX OWNED
model = "old/model"
model_reasoning_effort = ""
model_provider = "acc"
model_catalog_json = "/old/catalog.json"
web_search = "disabled"
# END ACC CODEX OWNED`

	rendered := renderCodexACCConfig(
		original,
		"/Users/kabir/.codex/acc-models.json",
		"http://127.0.0.1:9999/v1",
		"nvidia/nvidia~snemotron-3-ultra-550b-a55b",
		"medium",
	)

	if count := strings.Count(rendered, "BEGIN ACC CODEX OWNED"); count != 1 {
		t.Fatalf("Expected exactly 1 ACC owned block, got %d:\n%s", count, rendered)
	}
	if strings.Contains(rendered, "old/model") || strings.Contains(rendered, "/old/catalog.json") {
		t.Fatalf("Rendered config kept stale ACC values:\n%s", rendered)
	}
	if !strings.Contains(rendered, `model = "nvidia/nvidia~snemotron-3-ultra-550b-a55b"`) {
		t.Fatalf("Rendered config missing new model:\n%s", rendered)
	}
	if !strings.Contains(rendered, `model_reasoning_effort = "medium"`) {
		t.Fatalf("Rendered config missing effort:\n%s", rendered)
	}
	if err := validateCodexConfigText(rendered); err != nil {
		t.Fatalf("Rendered config is invalid TOML: %v\n%s", err, rendered)
	}
}

func TestRenderCodexACCConfigWithEmptyEffortDoesNotProduceInvalidTOML(t *testing.T) {
	base := `service_tier = "default"
personality = "friendly"`

	rendered := renderCodexACCConfig(base, "/Users/kabir/.codex/acc-models.json", "http://127.0.0.1:9999/v1", "nvidia/nvidia~snemotron-3-ultra-550b-a55b", "")

	if strings.Contains(rendered, "model_reasoning_effort") {
		t.Errorf("Rendered config should not contain model_reasoning_effort when empty, got:\n%s", rendered)
	}
}

func TestRenderCodexACCConfigWithNonEmptyEffortIncludesLine(t *testing.T) {
	base := `service_tier = "default"
personality = "friendly"`

	rendered := renderCodexACCConfig(base, "/Users/kabir/.codex/acc-models.json", "http://127.0.0.1:9999/v1", "nvidia/nvidia~snemotron-3-ultra-550b-a55b", "medium")

	if !strings.Contains(rendered, "model_reasoning_effort = \"medium\"") {
		t.Errorf("Rendered config missing model_reasoning_effort line:\n%s", rendered)
	}
}

func TestGeneratedConfigIsAlwaysValidTOML(t *testing.T) {
	testCases := []struct {
		name    string
		effort  string
		hasAcc  bool
		model   string
		catalog string
		baseURL string
	}{
		{
			name:    "empty effort with ACC",
			effort:  "",
			hasAcc:  true,
			model:   "nvidia/nvidia~snemotron-3-ultra-550b-a55b",
			catalog: "/Users/kabir/.codex/acc-models.json",
			baseURL: "http://127.0.0.1:9999/v1",
		},
		{
			name:    "non-empty effort with ACC",
			effort:  "high",
			hasAcc:  true,
			model:   "nvidia/nvidia~snemotron-3-ultra-550b-a55b",
			catalog: "/Users/kabir/.codex/acc-models.json",
			baseURL: "http://127.0.0.1:9999/v1",
		},
		{
			name:   "empty effort without ACC (subscription)",
			effort: "",
			hasAcc: false,
			model:  "",
			catalog: "",
			baseURL: "",
		},
	}

	for _, tc := range testCases {
		base := `service_tier = "default"
personality = "friendly"`

		var rendered string
		if tc.hasAcc {
			rendered = renderCodexACCConfig(base, tc.catalog, tc.baseURL, tc.model, tc.effort)
		} else {
			rendered = sanitizeCodexSubscriptionConfig(base)
		}

		if err := validateCodexConfigText(rendered); err != nil {
			t.Errorf("%s: generated invalid TOML: %v\nConfig:\n%s", tc.name, err, rendered)
		}
	}
}
