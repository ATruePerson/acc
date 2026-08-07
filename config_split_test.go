package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigReadsSplitLayoutWhenLegacyFileIsMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "claude"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "codex"), 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"providers.json":                       `{"port":9999,"providers":{"test":{"base_url":"https://example.test","api_key":"${TEST_KEY}"}}}`,
		filepath.Join("claude", "config.json"): `{"alias_routes":{"sonnet":{"provider":"test","model":"model"}}}`,
		filepath.Join("codex", "config.json"):  `{"models":{"codex-test":{"provider":"test","model":"model"}}}`,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("TEST_KEY", "secret")
	cfg, err := loadConfig(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9999 || cfg.Providers["test"].APIKey != "secret" {
		t.Fatalf("split provider config not loaded: %+v", cfg)
	}
	if cfg.AliasRoutes["sonnet"].Model != "model" || cfg.Models["codex-test"].Model != "model" {
		t.Fatalf("split route/model config not loaded: %+v", cfg)
	}
}
