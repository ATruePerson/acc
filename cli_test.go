package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFindCodexDesktopAppUsesExistingChatGPTApp(t *testing.T) {
	home := t.TempDir()
	app := filepath.Join(home, "Applications", "ChatGPT.app")
	if err := os.MkdirAll(app, 0755); err != nil {
		t.Fatal(err)
	}

	got, err := findCodexDesktopAppFor("darwin", home)
	if err != nil {
		t.Fatal(err)
	}
	if got != app {
		t.Fatalf("app = %q, want %q", got, app)
	}
}

func TestCodexOpenArgsOpenExistingAppWithoutInstaller(t *testing.T) {
	app := "/Applications/ChatGPT.app"
	path := "/Users/kabir/acc"
	want := []string{"-a", app, path}
	if got := codexOpenArgs(app, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("open args = %#v, want %#v", got, want)
	}
}

func TestChooseCodexModel(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"2\n", codexTerraID},
		{"Sol\n", codexSolID},
		{"terra\n", codexTerraID},
		{"LUNA\n", codexLunaID},
	}

	for _, tc := range cases {
		t.Run(strings.TrimSpace(tc.input), func(t *testing.T) {
			var out bytes.Buffer
			got, err := chooseCodexModel(strings.NewReader(tc.input), &out)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("choice %q = %q, want %q", tc.input, got, tc.want)
			}
			if !strings.Contains(out.String(), "Terra") || !strings.Contains(out.String(), "Sonnet") {
				t.Fatalf("picker does not explain family mapping:\n%s", out.String())
			}
		})
	}
}

func TestDetachedProxyCommandStartsIndependentSession(t *testing.T) {
	cmd := detachedProxyCommand("/tmp/acc-proxy")
	if len(cmd.Args) != 2 || cmd.Args[0] != "nohup" || cmd.Args[1] != "/tmp/acc-proxy" {
		t.Fatalf("command args = %q, want nohup /tmp/acc-proxy", cmd.Args)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatal("detached proxy must start in its own session")
	}
}

func TestProxyExecutablePrefersManagedSibling(t *testing.T) {
	dir := t.TempDir()
	command := filepath.Join(dir, "acc")
	proxy := filepath.Join(dir, "acc-proxy")
	if err := os.WriteFile(proxy, []byte("proxy"), 0755); err != nil {
		t.Fatal(err)
	}
	if got := proxyExecutable(command); got != proxy {
		t.Fatalf("proxy executable = %q, want %q", got, proxy)
	}
}

func TestCodexModelCatalogHasNamedModels(t *testing.T) {
	var catalog struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(codexModelCatalogJSON(), &catalog); err != nil {
		t.Fatal(err)
	}
	want := []struct{ slug, display string }{
		{"openai/codex-5.6-sol", "Codex 5.6 Sol"},
		{"openai/codex-5.6-terra", "Codex 5.6 Terra"},
		{"openai/codex-5.6-luna", "Codex 5.6 Luna"},
	}
	if len(catalog.Models) != len(want) {
		t.Fatalf("got %d models, want %d", len(catalog.Models), len(want))
	}
	for i := range want {
		if catalog.Models[i].Slug != want[i].slug || catalog.Models[i].DisplayName != want[i].display {
			t.Errorf("model[%d] = %+v, want slug=%q display=%q", i, catalog.Models[i], want[i].slug, want[i].display)
		}
	}
}

func TestConfigureAndRestoreCodexApp(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	catalogPath := filepath.Join(dir, "acc-models.json")
	restorePath := filepath.Join(dir, "restore.json")
	original := "sandbox_mode = \"workspace-write\"\nmodel = \"gpt-subscription\"\n\n[features]\nplugins = true\n"
	if err := os.WriteFile(configPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}

	err := configureCodexApp(configPath, catalogPath, restorePath, "http://localhost:9999/v1", "openai/codex-5.6-sol")
	if err != nil {
		t.Fatal(err)
	}
	configured, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(configured)
	for _, required := range []string{
		`sandbox_mode = "workspace-write"`,
		`model = "openai/codex-5.6-sol"`,
		`model_provider = "acc"`,
		`model_catalog_json = "` + catalogPath + `"`,
		`[features]`,
		`plugins = true`,
		`[model_providers.acc]`,
		`base_url = "http://localhost:9999/v1"`,
		`wire_api = "responses"`,
	} {
		if !strings.Contains(text, required) {
			t.Errorf("configured file missing %q:\n%s", required, text)
		}
	}
	if strings.Contains(text, `model = "gpt-subscription"`) {
		t.Fatalf("old root model was not replaced:\n%s", text)
	}
	if err := configureCodexApp(configPath, catalogPath, restorePath, "http://localhost:9999/v1", "openai/codex-5.6-terra"); err != nil {
		t.Fatal(err)
	}
	switched, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(switched), `model = "openai/codex-5.6-terra"`) {
		t.Fatalf("second launch did not switch models:\n%s", switched)
	}

	if err := restoreCodexApp(configPath, catalogPath, restorePath); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != original {
		t.Fatalf("restore changed original config:\ngot:\n%s\nwant:\n%s", restored, original)
	}
	if _, err := os.Stat(catalogPath); !os.IsNotExist(err) {
		t.Fatalf("generated catalog still exists after restore: %v", err)
	}
	if _, err := os.Stat(restorePath); !os.IsNotExist(err) {
		t.Fatalf("restore state still exists after restore: %v", err)
	}
}

func TestRenderEnvSortedAndPrivate(t *testing.T) {
	out := renderEnv(map[string]string{
		"OPENCODE_API_KEY":   "ock",
		"NVIDIA_NIM_API_KEY": "nvk",
	})
	if !strings.Contains(out, "NVIDIA_NIM_API_KEY=nvk") {
		t.Fatalf("missing nvidia key:\n%s", out)
	}
	// sorted: NVIDIA before OPENCODE
	if strings.Index(out, "NVIDIA_NIM_API_KEY") > strings.Index(out, "OPENCODE_API_KEY") {
		t.Fatalf("keys not sorted:\n%s", out)
	}
}

func TestDefaultConfigIsValidAndLoads(t *testing.T) {
	if !json.Valid([]byte(defaultConfigJSON)) {
		t.Fatal("defaultConfigJSON is not valid JSON")
	}
	var c Config
	if err := json.Unmarshal([]byte(defaultConfigJSON), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Port == 0 || len(c.Providers) == 0 || len(c.Routes) == 0 {
		t.Fatalf("default config missing essentials: %+v", c)
	}
	// every route must point at a defined provider
	if err := validateConfig(&c); err != nil {
		t.Fatalf("default config fails validation: %v", err)
	}
}

func TestKnownProvidersHaveEnvVars(t *testing.T) {
	for _, p := range knownProviders() {
		if p.Key == "" || p.EnvVar == "" || p.BaseURL == "" {
			t.Errorf("incomplete provider: %+v", p)
		}
	}
}
