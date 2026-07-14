package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRouteForUsesSelectedCodexModelWhenDesktopSendsGPT54(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{Routes: map[string]Route{
		"opus":   {Provider: "test", Model: "sol-upstream"},
		"sonnet": {Provider: "test", Model: "terra-upstream"},
		"haiku":  {Provider: "test", Model: "luna-upstream"},
	}}
	s := testServer(cfg)

	cases := []struct {
		selected string
		client   string
		want     string
	}{
		{codexSolID, "gpt-5.4", "sol-upstream"},
		{codexTerraID, "gpt-5.4-mini", "terra-upstream"},
		{codexLunaID, "gpt-5.4", "luna-upstream"},
	}

	for _, tc := range cases {
		t.Run(tc.selected, func(t *testing.T) {
			config := "model = \"" + tc.selected + "\"\nmodel_provider = \"acc\"\n"
			if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(config), 0600); err != nil {
				t.Fatal(err)
			}

			route, err := s.routeFor(tc.client)
			if err != nil {
				t.Fatal(err)
			}
			if route.Model != tc.want {
				t.Fatalf("route model = %q, want %q", route.Model, tc.want)
			}
		})
	}
}

func TestRouteForDoesNotTreatInactiveCodexConfigAsASelectedModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte("model = \""+codexTerraID+"\"\nmodel_provider = \"openai\"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	s := testServer(&Config{Routes: map[string]Route{"sonnet": {Model: "terra-upstream"}}})
	if _, err := s.routeFor("gpt-5.4"); err == nil {
		t.Fatal("expected an unrecognized gpt-5.4 model when ACC is not active")
	}
}
