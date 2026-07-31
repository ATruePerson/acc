package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersonaMarkdownDrivesACCIdentity(t *testing.T) {
	persona := accPersonaForRuntime("nvidia", "z-ai/glm-5.2", personaRuntimeClaudeCode)
	if !strings.Contains(persona, "Kabir's Second Brain") {
		t.Fatalf("missing identity:\n%s", persona)
	}
	if !strings.Contains(persona, "nvidia/z-ai/glm-5.2") {
		t.Fatalf("missing backend label:\n%s", persona)
	}
	if !strings.Contains(persona, "Claude Code runtime/tool adapter") {
		t.Fatalf("wrong runtime adapter:\n%s", persona)
	}
	if strings.Contains(persona, "Codex runtime/tool adapter") {
		t.Fatalf("codex adapter leaked into claude runtime:\n%s", persona)
	}
	if strings.Contains(persona, "{{backend}}") {
		t.Fatalf("backend placeholder left unsubstituted:\n%s", persona)
	}
}

func TestLoadConfigResolvesAliasSystemPrependFiles(t *testing.T) {
	dir := t.TempDir()
	prompts := filepath.Join(dir, "system_prompts")
	if err := os.MkdirAll(prompts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prompts, "Fable"), []byte("I am Claude Fable 5."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prompts, "persona.md"), []byte("## Core behavior\nTest persona {{backend}}\n\n## Runtime: codex\nCodex adapter\n\n## Runtime: claude-code\nClaude adapter\n\n## Runtime: generic\nGeneric adapter\n\n## Personal instructions\nPersonal\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(dir, "config.json")
	body := `{
  "port": 9999,
  "providers": {"openrouter": {"base_url": "https://example.com", "api_key": "x"}},
  "routes": {},
  "alias_routes": {
    "fable": {
      "provider": "openrouter",
      "model": "poolside/laguna-s-2.1:free",
      "system_prepend": "@system_prompts/Fable"
    }
  },
  "effort": {"high": {"budget": 1, "reasoning": "high"}}
}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { setPersonaFilePath("") })
	got := cfg.AliasRoutes["fable"].SystemPrepend
	if got != "I am Claude Fable 5." {
		t.Fatalf("system_prepend = %q", got)
	}
	persona := accPersonaForRuntime("openrouter", "x", personaRuntimeCodex)
	if !strings.Contains(persona, "Test persona openrouter/x") {
		t.Fatalf("disk persona.md not used:\n%s", persona)
	}
}
