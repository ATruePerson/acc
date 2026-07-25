package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCodexExperimentalNoticeIsExplicit(t *testing.T) {
	for _, word := range []string{"experimental", "work in progress", "restore"} {
		if !strings.Contains(strings.ToLower(codexExperimentalNotice), word) {
			t.Fatalf("Codex warning is missing %q: %s", word, codexExperimentalNotice)
		}
	}
}

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

func TestCodexOfflineDefaultIsRealProviderModel(t *testing.T) {
	if defaultCodexModel != "nvidia/z-ai/glm-5.2" {
		t.Fatalf("default Codex model = %q", defaultCodexModel)
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

func TestDetachedProxyCommandPinsConfigAndEnvPaths(t *testing.T) {
	cmd := detachedProxyCommand("/tmp/acc-proxy", "-config", "/tmp/config.json", "-env", "/tmp/.env")
	want := []string{"nohup", "/tmp/acc-proxy", "-config", "/tmp/config.json", "-env", "/tmp/.env"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("command args = %q, want %q", cmd.Args, want)
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
	if err := json.Unmarshal(codexModelCatalogJSON(codexTestConfig()), &catalog); err != nil {
		t.Fatal(err)
	}
	want := []struct{ slug, display string }{
		{"nvidia/z-ai/glm-5.2", "z-ai/glm-5.2 (nvidia)"},
		{"opencode/big-pickle", "big-pickle (opencode)"},
		{"nvidia/stepfun-ai/step-3.7-flash", "stepfun-ai/step-3.7-flash (nvidia)"},
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

func TestCodexCatalogDoesNotAdvertiseHostedWebSearch(t *testing.T) {
	var catalog struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(codexModelCatalogJSON(codexTestConfig()), &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) == 0 {
		t.Fatal("catalog has no models")
	}
	for _, model := range catalog.Models {
		if _, ok := model["web_search_tool_type"]; ok {
			t.Fatalf("catalog must not advertise provider-hosted web search: %s", model["web_search_tool_type"])
		}
	}
}

func TestConfigureAndRestoreCodexApp(t *testing.T) {
	cfg := codexTestConfig()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	catalogPath := filepath.Join(dir, "acc-models.json")
	restorePath := filepath.Join(dir, "restore.json")
	original := "sandbox_mode = \"workspace-write\"\nmodel = \"gpt-subscription\"\n\n[features]\nplugins = true\n"
	if err := os.WriteFile(configPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}

	err := configureCodexApp(configPath, catalogPath, restorePath, "http://localhost:9999/v1", "nvidia/z-ai/glm-5.2", cfg)
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
		`model = "nvidia/z-ai/glm-5.2"`,
		`model_provider = "acc"`,
		`model_catalog_json = "` + catalogPath + `"`,
		`web_search = "disabled"`,
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
	if err := configureCodexApp(configPath, catalogPath, restorePath, "http://localhost:9999/v1", "opencode/big-pickle", cfg); err != nil {
		t.Fatal(err)
	}
	switched, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(switched), `model = "opencode/big-pickle"`) {
		t.Fatalf("second launch did not switch models:\n%s", switched)
	}

	if err := restoreCodexApp(configPath, catalogPath, restorePath); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	restoredText := string(restored)
	for _, preserved := range []string{
		`sandbox_mode = "workspace-write"`,
		`[features]`,
		`plugins = true`,
	} {
		if !strings.Contains(restoredText, preserved) {
			t.Fatalf("sanitized restore lost %q:\n%s", preserved, restoredText)
		}
	}
	for _, forbidden := range []string{
		`model = "gpt-subscription"`,
		`model_provider = "acc"`,
		`model_catalog_json`,
		`[model_providers.acc]`,
		`http://localhost:9999/v1`,
	} {
		if strings.Contains(restoredText, forbidden) {
			t.Fatalf("sanitized restore retained %q:\n%s", forbidden, restoredText)
		}
	}
	if _, err := os.Stat(catalogPath); !os.IsNotExist(err) {
		t.Fatalf("generated catalog still exists after restore: %v", err)
	}
	baseline, err := readCodexBaseline(restorePath)
	if err != nil {
		t.Fatalf("durable baseline missing or unreadable: %v", err)
	}
	if err := validateCodexBaseline(baseline); err != nil {
		t.Fatalf("durable baseline invalid: %v", err)
	}
	if string(baseline.RawConfig.Data) != original {
		t.Fatalf("raw subscription snapshot changed:\ngot:\n%s\nwant:\n%s", baseline.RawConfig.Data, original)
	}
	if string(baseline.SanitizedConfig.Data) != restoredText {
		t.Fatalf("restore did not use the stored sanitized baseline:\ngot:\n%s\nwant:\n%s", restoredText, baseline.SanitizedConfig.Data)
	}
	if err := restoreCodexApp(configPath, catalogPath, restorePath); err != nil {
		t.Fatalf("repeated restore failed: %v", err)
	}
	repeated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(repeated) != restoredText {
		t.Fatalf("repeated restore was not idempotent:\ngot:\n%s\nwant:\n%s", repeated, restoredText)
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
	if c.Port == 0 || len(c.Providers) == 0 {
		t.Fatalf("default config missing essentials: %+v", c)
	}
	if c.Models["acc-minimax-m3"].ToolCallSupport {
		t.Fatal("default config must not claim MiniMax M3 supports Codex tool calls")
	}
	// every route must point at a defined provider
	if err := validateConfig(&c); err != nil {
		t.Fatalf("default config fails validation: %v", err)
	}
}

func TestDefaultConfigExposesOnlyProviderPrefixedRealModels(t *testing.T) {
	b, err := os.ReadFile("config.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}

	visible := codexNamedModels(&cfg)
	if len(visible) == 0 {
		t.Fatal("user-facing catalog is empty")
	}
	seen := map[string]bool{}
	for _, model := range visible {
		if !strings.Contains(model.ID, "/") || seen[model.ID] {
			t.Fatalf("catalog has ambiguous or duplicate real ID %q", model.ID)
		}
		seen[model.ID] = true
		for _, forbidden := range []string{"opus", "sonnet", "haiku", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
			if strings.EqualFold(model.ID, forbidden) {
				t.Fatalf("forbidden alias in catalog: %q", model.ID)
			}
		}
	}
}

func TestKnownProvidersHaveEnvVars(t *testing.T) {
	for _, p := range knownProviders() {
		if p.Key == "" || p.EnvVar == "" || p.BaseURL == "" {
			t.Errorf("incomplete provider: %+v", p)
		}
	}
}
