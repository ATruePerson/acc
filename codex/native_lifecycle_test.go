package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexGatewayPointsDirectlyToACC(t *testing.T) {
	got := codexFrontGatewayURL(&Config{Port: 8080})
	if got != "http://127.0.0.1:8080/v1" {
		t.Fatalf("gateway = %q", got)
	}
	if strings.Contains(strings.ToLower(got), "opencodex") || strings.Contains(got, "10100") {
		t.Fatalf("legacy bridge leaked into direct gateway: %s", got)
	}
}

func TestRenderAndRemoveOwnedCodexConfigPreservesUnrelatedTOML(t *testing.T) {
	original := `approval_policy = "on-request"
sandbox_mode = "workspace-write"

[projects."/home/user/project"]
trust_level = "trusted"

[mcp_servers.obsidian]
command = "obsidian-mcp"
`
	configured := renderCodexConfig(original, "/tmp/catalog.json", "http://127.0.0.1:8080/v1", "nvidia/real~model")
	for _, want := range []string{accCodexRootBegin, accCodexRootEnd, accCodexProvider, `model = "nvidia/real~model"`, `wire_api = "responses"`} {
		if !strings.Contains(configured, want) {
			t.Fatalf("configured text missing %q:\n%s", want, configured)
		}
	}
	for _, preserved := range []string{`approval_policy = "on-request"`, `[projects."/home/user/project"]`, `trust_level = "trusted"`, `[mcp_servers.obsidian]`} {
		if !strings.Contains(configured, preserved) {
			t.Fatalf("unrelated TOML was lost: %q\n%s", preserved, configured)
		}
	}
	removed := removeACCFromCodexConfig(configured)
	if strings.Contains(removed, "model_providers.acc") || strings.Contains(removed, accCodexRootBegin) {
		t.Fatalf("ACC-owned config remains:\n%s", removed)
	}
	for _, preserved := range []string{`approval_policy = "on-request"`, `[projects."/home/user/project"]`, `[mcp_servers.obsidian]`} {
		if !strings.Contains(removed, preserved) {
			t.Fatalf("remove lost unrelated TOML %q:\n%s", preserved, removed)
		}
	}
}

func TestConfigureCreatesTimestampedBackupAndSecretFreeCatalog(t *testing.T) {
	cfg := codexTestConfig()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	catalogPath := filepath.Join(dir, "acc-models.json")
	restorePath := filepath.Join(dir, "restore.json")
	if err := os.WriteFile(configPath, []byte("approval_policy = \"on-request\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := configureCodexApp(configPath, catalogPath, restorePath, "http://127.0.0.1:8080/v1", "nvidia/z-ai~sglm-5.2", cfg); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(configPath + ".acc-backup-*")
	if len(backups) == 0 {
		t.Fatal("configure did not create a timestamped config backup")
	}
	catalog, _ := os.ReadFile(catalogPath)
	if !json.Valid(catalog) {
		t.Fatalf("invalid catalog: %s", catalog)
	}
	for _, secret := range []string{"nvidia-key", "opencode-key", "openrouter-key", "api_key", "access_token", "refresh_token"} {
		if strings.Contains(strings.ToLower(string(catalog)), strings.ToLower(secret)) {
			t.Fatalf("catalog leaked %q: %s", secret, catalog)
		}
	}
}

func TestLegacyOpenCodexDetectionUsesActiveRoutingOnly(t *testing.T) {
	for _, legacy := range []string{
		`openai_base_url = "http://127.0.0.1:10100/v1"`,
		"model_provider = \"opencodex\"\n\n[model_providers.opencodex]\nbase_url = \"http://127.0.0.1:10100/v1\"\n",
	} {
		if !legacyOpenCodexDetected(legacy) {
			t.Fatalf("missed active legacy config: %s", legacy)
		}
	}
	for _, harmless := range []string{
		`# OpenCodex owned`,
		"[mcp_servers.docs]\ncommand = \"/home/user/.local/bin/helper\"\n",
		`base_url = "http://127.0.0.1:8080/v1"`,
	} {
		if legacyOpenCodexDetected(harmless) {
			t.Fatalf("inactive text was mistaken for OpenCodex routing: %s", harmless)
		}
	}
}

func TestRenderMigratesLegacyOpenCodexRootWithoutDeletingUnrelatedProvider(t *testing.T) {
	original := `openai_base_url = "http://127.0.0.1:10100/v1"
model = "old"

[model_providers.other]
base_url = "https://provider.example/v1"
`
	configured := renderCodexConfig(original, "/tmp/catalog.json", "http://127.0.0.1:9999/v1", "nvidia/real~model")
	if strings.Contains(configured, "10100") {
		t.Fatalf("legacy OpenCodex root remained:\n%s", configured)
	}
	if !strings.Contains(configured, "[model_providers.other]") || !strings.Contains(configured, "https://provider.example/v1") {
		t.Fatalf("unrelated provider was removed:\n%s", configured)
	}
}

func TestValidateCodexCatalogRejectsAliasesDuplicatesAndAmbiguousIDs(t *testing.T) {
	valid := []byte(`{"models":[{"slug":"xai/grok"},{"slug":"kimi/k2"}]}`)
	if ok, _ := validateCodexCatalog(valid); !ok {
		t.Fatal("valid real-model catalog was rejected")
	}
	for _, invalid := range [][]byte{
		[]byte(`{"models":[{"slug":"opus"}]}`),
		[]byte(`{"models":[{"slug":"xai/grok"},{"slug":"xai/grok"}]}`),
		[]byte(`{"models":[{"slug":"grok"}]}`),
	} {
		if ok, _ := validateCodexCatalog(invalid); ok {
			t.Fatalf("invalid catalog accepted: %s", invalid)
		}
	}
}

func TestInvalidOwnershipFileCannotKillAProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ownership.json")
	if err := os.WriteFile(path, []byte(`{"pid":1,"executable":""}`), 0600); err != nil {
		t.Fatal(err)
	}
	if stopped, err := stopOwnedCodexProcess(path); err == nil || stopped {
		t.Fatalf("stopped=%v err=%v", stopped, err)
	}
}
