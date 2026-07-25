package main

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestDefaultCodexSelectorContainsOnlyChosenModels(t *testing.T) {
	body, err := os.ReadFile("config.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(body, &cfg); err != nil {
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
		if capability.FallbackModel != "" || len(capability.FallbackModels) != 0 || capability.ImageModel != "" || len(capability.ImageFallbackModels) != 0 {
			t.Fatalf("selected model %q unexpectedly owns hidden fallback models", id)
		}
	}
	models := codexNamedModels(&cfg)
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
