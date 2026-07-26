package main

import (
	"strings"
	"testing"
)

func TestSanitizeCodexConfigStripsModelReasoningEffort(t *testing.T) {
	// Original config with duplicate model_reasoning_effort that caused TOML parse error
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

	sanitized := sanitizeCodexConfig(original, true)

	// Should only have one model_reasoning_effort line (the ACC-owned one)
	count := strings.Count(sanitized, "model_reasoning_effort")
	if count != 1 {
		t.Fatalf("Expected exactly 1 model_reasoning_effort line, got %d:\n%s", count, sanitized)
	}

	// Should not contain the original subscription value
	if strings.Contains(sanitized, "model_reasoning_effort = \"medium\"") {
		t.Fatalf("Sanitized config still contains original model_reasoning_effort value:\n%s", sanitized)
	}

	// Should contain the ACC-owned line
	if !strings.Contains(sanitized, "model_reasoning_effort = \"\"") {
		t.Fatalf("Sanitized config missing ACC-owned model_reasoning_effort line:\n%s", sanitized)
	}
}

func TestRenderCodexACCConfigWithEmptyEffortDoesNotProduceInvalidTOML(t *testing.T) {
	// Clean base config (no duplicates)
	base := `service_tier = "default"
personality = "friendly"`

	// Render with empty effort (this was causing "reasoning_effort must not be empty" error)
	rendered := renderCodexACCConfig(base, "/Users/kabir/.codex/acc-models.json", "http://127.0.0.1:9999/v1", "nvidia/nvidia~snemotron-3-ultra-550b-a55b", "")

	// Should NOT contain model_reasoning_effort line at all when empty
	if strings.Contains(rendered, "model_reasoning_effort") {
		t.Errorf("Rendered config should not contain model_reasoning_effort when empty, got:\n%s", rendered)
	}
}

func TestRenderCodexACCConfigWithNonEmptyEffortIncludesLine(t *testing.T) {
	base := `service_tier = "default"
personality = "friendly"`

	// Render with non-empty effort
	rendered := renderCodexACCConfig(base, "/Users/kabir/.codex/acc-models.json", "http://127.0.0.1:9999/v1", "nvidia/nvidia~snemotron-3-ultra-550b-a55b", "medium")

	if !strings.Contains(rendered, "model_reasoning_effort = \"medium\"") {
		t.Errorf("Rendered config missing model_reasoning_effort line:\n%s", rendered)
	}
}

func TestGeneratedConfigIsAlwaysValidTOML(t *testing.T) {
	testCases := []struct {
		name     string
		effort   string
		hasAcc   bool
		model    string
		catalog  string
		baseURL  string
	}{
		{
			name:  "empty effort with ACC",
			effort: "",
			hasAcc: true,
			model:  "nvidia/nvidia~snemotron-3-ultra-550b-a55b",
			catalog: "/Users/kabir/.codex/acc-models.json",
			baseURL: "http://127.0.0.1:9999/v1",
		},
		{
			name:  "non-empty effort with ACC",
			effort: "high",
			hasAcc: true,
			model:  "nvidia/nvidia~snemotron-3-ultra-550b-a55b",
			catalog: "/Users/kabir/.codex/acc-models.json",
			baseURL: "http://127.0.0.1:9999/v1",
		},
		{
			name:  "empty effort without ACC (subscription)",
			effort: "",
			hasAcc: false,
			model:  "",
			catalog: "",
			baseURL:  "",
		},
	}

	for _, tc := range testCases {
		base := `service_tier = "default"
personality = "friendly"`

		var rendered string
		if tc.hasAcc {
			rendered = renderCodexACCConfig(base, tc.catalog, tc.baseURL, tc.model, tc.effort)
		} else {
			// For subscription mode, we'd use sanitizeCodexConfig directly
			rendered = sanitizeCodexConfig(base, true)
		}

		if err := validateCodexConfigText(rendered); err != nil {
			t.Errorf("%s: generated invalid TOML: %v\nConfig:\n%s", tc.name, err, rendered)
		}
	}
}