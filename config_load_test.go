package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSplitConfigMergesClaudeAndCodex(t *testing.T) {
	cfg, err := loadConfig(".")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9999 {
		t.Fatalf("port = %d", cfg.Port)
	}
	if len(cfg.Providers) == 0 {
		t.Fatal("providers missing")
	}
	if cfg.AliasRoutes["opus"].Model != "z-ai/glm-5.2" {
		t.Fatalf("opus = %+v", cfg.AliasRoutes["opus"])
	}
	if !strings.Contains(cfg.AliasRoutes["fable"].SystemPrepend, "Claude Fable 5") {
		t.Fatalf("fable prompt not loaded: %q", trunc(cfg.AliasRoutes["fable"].SystemPrepend, 80))
	}
	if len(cfg.Models) != 3 {
		t.Fatalf("models = %d", len(cfg.Models))
	}
	if err := validateConfig(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestWriteDefaultSplitConfigRoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := writeDefaultSplitConfig(root); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(filepath.Join(root, providersFileName))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AliasRoutes["haiku"].Model == "" || cfg.Models["codex-step-3.7-flash"].Model == "" {
		t.Fatalf("incomplete defaults: aliases=%v models=%v", cfg.AliasRoutes, cfg.Models)
	}
	if _, err := os.Stat(filepath.Join(root, "claude", "system_prompts", "Opus")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "system_prompts", "persona.md")); err != nil {
		t.Fatal(err)
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
