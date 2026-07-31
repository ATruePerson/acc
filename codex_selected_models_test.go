package main

import (
	"reflect"
	"testing"

	"github.com/ATruePerson/acc/codex"
)

func TestDefaultCodexSelectorContainsOnlyChosenModels(t *testing.T) {
	cfg, err := loadConfig(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Models) != 3 {
		t.Fatalf("default model registry has %d entries, want exactly 3", len(cfg.Models))
	}
	for id, capability := range cfg.Models {
		if !capability.Enabled {
			t.Fatalf("selected model %q is disabled", id)
		}
		if capability.CatalogVisible != nil && !*capability.CatalogVisible {
			t.Fatalf("selected model %q is hidden", id)
		}
		_ = capability
	}
	models := codex.NamedModels(cfg)
	var slugs []string
	for _, model := range models {
		slugs = append(slugs, model.ID)
	}
	want := []string{
		"nvidia/nvidia~snemotron-3-ultra-550b-a55b",
		"openrouter/poolside~slaguna-s-2.1:free",
		"nvidia/stepfun-ai~sstep-3.7-flash",
	}
	if !reflect.DeepEqual(slugs, want) {
		t.Fatalf("selector models = %v, want %v", slugs, want)
	}
	if cfg.Models["codex-nemotron-ultra-550b"].ImageInputSupport {
		t.Fatal("NVIDIA Nemotron Ultra is text-only and must not advertise image input")
	}
	if cfg.Models["codex-laguna-s-2.1-free"].ImageInputSupport {
		t.Fatal("Laguna S 2.1 must remain text-only until its provider confirms vision")
	}
	if !cfg.Models["codex-step-3.7-flash"].ImageInputSupport {
		t.Fatal("Step 3.7 Flash should advertise its verified image input support")
	}
}
