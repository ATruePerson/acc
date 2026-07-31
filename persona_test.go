package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	accclaude "github.com/ATruePerson/acc/claude"
)

func TestPersonaMarkdownDrivesACCIdentity(t *testing.T) {
	persona := accclaude.AccPersonaForRuntime("nvidia", "z-ai/glm-5.2", accclaude.PersonaRuntimeClaudeCode)
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
	if err := writeDefaultSplitConfig(dir); err != nil {
		t.Fatal(err)
	}
	// Override fable prompt with a tiny marker for the assertion.
	if err := os.WriteFile(filepath.Join(dir, "claude", "system_prompts", "Fable"), []byte("I am Claude Fable 5."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "system_prompts", "persona.md"), []byte("## Core behavior\nTest persona {{backend}}\n\n## Runtime: codex\nCodex adapter\n\n## Runtime: claude-code\nClaude adapter\n\n## Runtime: generic\nGeneric adapter\n\n## Personal instructions\nPersonal\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfig(filepath.Join(dir, providersFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { accclaude.SetPersonaFilePath("") })
	got := cfg.AliasRoutes["fable"].SystemPrepend
	if got != "I am Claude Fable 5." {
		t.Fatalf("system_prepend = %q", got)
	}
	persona := accclaude.AccPersonaForRuntime("openrouter", "x", accclaude.PersonaRuntimeCodex)
	if !strings.Contains(persona, "Test persona openrouter/x") {
		t.Fatalf("disk persona.md not used:\n%s", persona)
	}
}
