package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type codexLifecycleTestPaths struct {
	Home     string
	Config   string
	Catalog  string
	Baseline string
	Restart  string
	Auth     string
}

func newCodexLifecycleTestPaths(t *testing.T) codexLifecycleTestPaths {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexDir := filepath.Join(home, ".codex")
	accStateDir := filepath.Join(home, ".config", "acc")
	if err := os.MkdirAll(codexDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(accStateDir, 0700); err != nil {
		t.Fatal(err)
	}
	baseline := filepath.Join(accStateDir, "codex-restore.json")
	return codexLifecycleTestPaths{
		Home:     home,
		Config:   filepath.Join(codexDir, "config.toml"),
		Catalog:  filepath.Join(codexDir, "acc-models.json"),
		Baseline: baseline,
		Restart:  codexRestartPathForBaseline(baseline),
		Auth:     filepath.Join(codexDir, "auth.json"),
	}
}

func writeTestFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func startTestCodexConfig(t *testing.T, paths codexLifecycleTestPaths) {
	t.Helper()
	if err := configureCodexApp(paths.Config, paths.Catalog, paths.Baseline, "http://127.0.0.1:9999/v1", "nvidia/z-ai~sglm-5.2", codexTestConfig()); err != nil {
		t.Fatal(err)
	}
}

func TestCleanSubscriptionConfigStartThenRestore(t *testing.T) {
	paths := newCodexLifecycleTestPaths(t)
	original := []byte("approval_policy = \"on-request\"\n" +
		"sandbox_mode = \"workspace-write\"\n" +
		"web_search = \"live\"\n\n" +
		"[projects.\"/Users/kabir/acc\"]\ntrust_level = \"trusted\"\n\n" +
		"[mcp_servers.obsidian]\ncommand = \"obsidian-mcp\"\n")
	auth := []byte(`{"tokens":"subscription-secret"}`)
	writeTestFile(t, paths.Config, original)
	writeTestFile(t, paths.Auth, auth)

	startTestCodexConfig(t, paths)
	configured := readTestFile(t, paths.Config)
	if strings.Count(string(configured), "web_search =") != 1 || !strings.Contains(string(configured), `web_search = "disabled"`) {
		t.Fatalf("start did not exclusively own web_search while ACC was active:\n%s", configured)
	}
	if routing := inspectCodexRouting(string(configured)); routing.Mode != "ACC" || routing.Provider != "acc" {
		t.Fatalf("routing after start = %+v", routing)
	}
	if status := codexBaselineStatus(paths.Baseline, string(configured)); status != "Valid" {
		t.Fatalf("baseline status = %s", status)
	}

	result, err := restoreCodexAppDetailed(paths.Config, paths.Catalog, paths.Baseline, paths.Restart)
	if err != nil {
		t.Fatal(err)
	}
	if result.RecoveryMode {
		t.Fatal("clean start should not require recovery restore")
	}
	if got := readTestFile(t, paths.Config); string(got) != string(original) {
		t.Fatalf("restored config changed unrelated bytes:\n--- got ---\n%s\n--- want ---\n%s", got, original)
	}
	if got := readTestFile(t, paths.Auth); string(got) != string(auth) {
		t.Fatalf("authentication changed: %s", got)
	}
	if _, err := os.Stat(paths.Catalog); !os.IsNotExist(err) {
		t.Fatalf("generated catalog should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(paths.Baseline); err != nil {
		t.Fatalf("durable baseline was removed: %v", err)
	}
}

func TestExistingOpenCodexConfigStartThenRestoreToSubscription(t *testing.T) {
	paths := newCodexLifecycleTestPaths(t)
	openCodexCatalog := filepath.Join(paths.Home, ".codex", "opencodex-models.json")
	original := "# normal subscription preferences\n" +
		"model = \"nvidia/old-model\"\n" +
		"model_provider = \"opencodex\"\n" +
		"model_catalog_json = \"" + openCodexCatalog + "\"\n" +
		"openai_base_url = \"http://127.0.0.1:10100/v1\"\n" +
		"approval_policy = \"on-request\"\n\n" +
		"# Auto-injected by opencodex\n" +
		"[model_providers.opencodex]\n" +
		"name = \"OpenCodex\"\n" +
		"base_url = \"http://127.0.0.1:10100/v1\"\n\n" +
		"[projects.\"/Users/kabir/acc\"]\n" +
		"trust_level = \"trusted\"\n"
	writeTestFile(t, paths.Config, []byte(original))
	writeTestFile(t, openCodexCatalog, []byte(`{"models":[{"slug":"nvidia/old-model"}]}`))

	startTestCodexConfig(t, paths)
	started := string(readTestFile(t, paths.Config))
	if strings.Contains(started, "10100") || strings.Contains(started, "model_providers.opencodex") {
		t.Fatalf("OpenCodex routing survived start:\n%s", started)
	}
	if inspectCodexRouting(started).Mode != "ACC" {
		t.Fatalf("start did not activate ACC:\n%s", started)
	}

	result, err := restoreCodexAppDetailed(paths.Config, paths.Catalog, paths.Baseline, paths.Restart)
	if err != nil {
		t.Fatal(err)
	}
	if result.RecoveryMode {
		t.Fatal("baseline created by start should be used")
	}
	restored := string(readTestFile(t, paths.Config))
	if err := validateSubscriptionCodexConfig(restored); err != nil {
		t.Fatalf("restore did not produce subscription config: %v\n%s", err, restored)
	}
	for _, forbidden := range []string{"10100", "9999", "model_catalog_json", "model_providers.acc", "model_providers.opencodex", "nvidia/old-model"} {
		if strings.Contains(restored, forbidden) {
			t.Fatalf("restored config still contains active custom routing %q:\n%s", forbidden, restored)
		}
	}
	for _, preserved := range []string{`approval_policy = "on-request"`, `[projects."/Users/kabir/acc"]`, `trust_level = "trusted"`} {
		if !strings.Contains(restored, preserved) {
			t.Fatalf("restore lost unrelated setting %q:\n%s", preserved, restored)
		}
	}
	if _, err := os.Stat(openCodexCatalog); !os.IsNotExist(err) {
		t.Fatalf("known OpenCodex-generated catalog should be removed, stat err=%v", err)
	}
}

func TestRecoveryRestoreFromExistingACCConfigWithoutBaseline(t *testing.T) {
	paths := newCodexLifecycleTestPaths(t)
	accConfig := `# BEGIN ACC CODEX OWNED
model = "nvidia/z-ai~sglm-5.2"
model_provider = "acc"
model_catalog_json = "` + paths.Catalog + `"
web_search = "disabled"
# END ACC CODEX OWNED

approval_policy = "on-request"

# ACC CODEX OWNED PROVIDER
[model_providers.acc]
name = "ACC"
base_url = "http://127.0.0.1:9999/v1"
wire_api = "responses"
`
	writeTestFile(t, paths.Config, []byte(accConfig))
	writeTestFile(t, paths.Catalog, []byte(`{"models":[{"slug":"nvidia/z-ai~sglm-5.2"}]}`))

	result, err := restoreCodexAppDetailed(paths.Config, paths.Catalog, paths.Baseline, paths.Restart)
	if err != nil {
		t.Fatal(err)
	}
	if !result.RecoveryMode {
		t.Fatal("missing baseline should use recovery mode")
	}
	restored := string(readTestFile(t, paths.Config))
	if err := validateSubscriptionCodexConfig(restored); err != nil {
		t.Fatalf("recovery restore failed: %v\n%s", err, restored)
	}
	if !strings.Contains(restored, `approval_policy = "on-request"`) {
		t.Fatalf("recovery lost unrelated setting:\n%s", restored)
	}
	if _, err := os.Stat(paths.Catalog); !os.IsNotExist(err) {
		t.Fatalf("recovery should remove generated ACC catalog, stat err=%v", err)
	}
	if status := codexBaselineStatus(paths.Baseline, restored); status != "Valid" {
		t.Fatalf("recovery baseline status = %s", status)
	}
}

func TestRepeatedStartPreservesBaselineAndIsIdempotent(t *testing.T) {
	paths := newCodexLifecycleTestPaths(t)
	writeTestFile(t, paths.Config, []byte("approval_policy = \"on-request\"\n"))
	startTestCodexConfig(t, paths)
	baselineBefore := readTestFile(t, paths.Baseline)
	configBefore := readTestFile(t, paths.Config)
	if err := clearCodexRestartRequired(paths.Restart); err != nil {
		t.Fatal(err)
	}

	tx, err := beginConfigureCodexApp(paths.Config, paths.Catalog, paths.Baseline, paths.Restart, "http://127.0.0.1:9999/v1", "nvidia/z-ai~sglm-5.2", codexTestConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	result := tx.Commit()
	if result.BaselineCreated {
		t.Fatal("repeated start overwrote the valid baseline")
	}
	if result.RestartRequired {
		t.Fatal("identical repeated start should not rewrite active files")
	}
	if got := readTestFile(t, paths.Baseline); string(got) != string(baselineBefore) {
		t.Fatal("repeated start changed the baseline")
	}
	if got := readTestFile(t, paths.Config); string(got) != string(configBefore) {
		t.Fatal("repeated start changed the active config")
	}
	if fileExists(paths.Restart) {
		t.Fatal("idempotent repeated start recreated the restart marker")
	}
}

func TestRepeatedRestoreIsIdempotent(t *testing.T) {
	paths := newCodexLifecycleTestPaths(t)
	original := []byte("approval_policy = \"on-request\"\n")
	writeTestFile(t, paths.Config, original)
	startTestCodexConfig(t, paths)
	if _, err := restoreCodexAppDetailed(paths.Config, paths.Catalog, paths.Baseline, paths.Restart); err != nil {
		t.Fatal(err)
	}
	if err := clearCodexRestartRequired(paths.Restart); err != nil {
		t.Fatal(err)
	}
	result, err := restoreCodexAppDetailed(paths.Config, paths.Catalog, paths.Baseline, paths.Restart)
	if err != nil {
		t.Fatal(err)
	}
	if result.RestartRequired {
		t.Fatal("second restore should be a no-op")
	}
	if fileExists(paths.Restart) {
		t.Fatal("no-op restore recreated restart marker")
	}
	if got := readTestFile(t, paths.Config); string(got) != string(original) {
		t.Fatalf("second restore changed config: %s", got)
	}
}

func TestMixedMCPTrustAndHarmlessOpenCodexTextArePreserved(t *testing.T) {
	paths := newCodexLifecycleTestPaths(t)
	original := []byte("# opencodex is mentioned only in documentation\n" +
		"approval_policy = \"on-request\"\n" +
		"sandbox_mode = \"workspace-write\"\n\n" +
		"[mcp_servers.docs]\n" +
		"command = \"/Users/kabir/tools/opencodex-helper\"\n\n" +
		"[projects.\"/Users/kabir/Second Brain\"]\n" +
		"trust_level = \"trusted\"\n")
	writeTestFile(t, paths.Config, original)
	if legacyOpenCodexDetected(string(original)) {
		t.Fatal("harmless comment or MCP path was treated as active OpenCodex routing")
	}
	startTestCodexConfig(t, paths)
	if _, err := restoreCodexAppDetailed(paths.Config, paths.Catalog, paths.Baseline, paths.Restart); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, paths.Config); string(got) != string(original) {
		t.Fatalf("mixed unrelated config was not byte-for-byte preserved:\n%s", got)
	}
}

func TestCatalogOriginallyExistedIsRestored(t *testing.T) {
	paths := newCodexLifecycleTestPaths(t)
	originalCatalog := []byte(`{"custom":"preexisting-file"}`)
	writeTestFile(t, paths.Config, []byte("approval_policy = \"on-request\"\n"))
	writeTestFile(t, paths.Catalog, originalCatalog)
	startTestCodexConfig(t, paths)
	if got := readTestFile(t, paths.Catalog); string(got) == string(originalCatalog) {
		t.Fatal("start did not replace the managed catalog")
	}
	if _, err := restoreCodexAppDetailed(paths.Config, paths.Catalog, paths.Baseline, paths.Restart); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, paths.Catalog); string(got) != string(originalCatalog) {
		t.Fatalf("preexisting catalog was not restored: %s", got)
	}
}

func TestCatalogOriginallyMissingIsDeleted(t *testing.T) {
	paths := newCodexLifecycleTestPaths(t)
	writeTestFile(t, paths.Config, []byte("approval_policy = \"on-request\"\n"))
	startTestCodexConfig(t, paths)
	if _, err := os.Stat(paths.Catalog); err != nil {
		t.Fatalf("start did not create catalog: %v", err)
	}
	if _, err := restoreCodexAppDetailed(paths.Config, paths.Catalog, paths.Baseline, paths.Restart); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.Catalog); !os.IsNotExist(err) {
		t.Fatalf("generated catalog remains after restore, stat err=%v", err)
	}
}

func TestFailedConfigWriteRollsBackEveryMutation(t *testing.T) {
	paths := newCodexLifecycleTestPaths(t)
	original := []byte("approval_policy = \"on-request\"\n")
	writeTestFile(t, paths.Config, original)
	oldWrite := codexWriteFile
	codexWriteFile = func(path string, data []byte, mode os.FileMode) error {
		if filepath.Clean(path) == filepath.Clean(paths.Config) {
			return errors.New("injected config write failure")
		}
		return atomicWriteFile(path, data, mode)
	}
	defer func() { codexWriteFile = oldWrite }()

	err := configureCodexApp(paths.Config, paths.Catalog, paths.Baseline, "http://127.0.0.1:9999/v1", "nvidia/z-ai~sglm-5.2", codexTestConfig())
	if err == nil || !strings.Contains(err.Error(), "injected config write failure") {
		t.Fatalf("configure error = %v", err)
	}
	if got := readTestFile(t, paths.Config); string(got) != string(original) {
		t.Fatalf("config was not rolled back: %s", got)
	}
	for _, path := range []string{paths.Catalog, paths.Baseline, paths.Restart} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("rollback left %s behind, stat err=%v", path, statErr)
		}
	}
}

func TestDoctorIgnoresHarmlessOpenCodexTextAndInactiveProvider(t *testing.T) {
	config := `# opencodex appears only in a comment
approval_policy = "on-request"

[mcp_servers.docs]
command = "/Users/kabir/tools/opencodex-helper"

[model_providers.opencodex_archive]
base_url = "http://127.0.0.1:10100/v1"
`
	routing := inspectCodexRouting(config)
	if routing.Mode != "Subscription" || routing.ActiveOpenCodex || legacyOpenCodexDetected(config) {
		t.Fatalf("inactive OpenCodex text/provider was treated as active routing: %+v", routing)
	}
}

func TestDoctorRoutingParserCatchesActivePort10100(t *testing.T) {
	config := `model_provider = "bridge"

[model_providers.bridge]
base_url = "http://127.0.0.1:10100/v1"
`
	routing := inspectCodexRouting(config)
	if routing.Mode != "OpenCodex" || !legacyOpenCodexDetected(config) {
		t.Fatalf("active 10100 routing was missed: %+v", routing)
	}
}

func TestRestoreRemovesProviderPrefixedRootModel(t *testing.T) {
	paths := newCodexLifecycleTestPaths(t)
	writeTestFile(t, paths.Config, []byte("model = \"google/gemini-custom\"\napproval_policy = \"on-request\"\n"))
	result, err := restoreCodexAppDetailed(paths.Config, paths.Catalog, paths.Baseline, paths.Restart)
	if err != nil {
		t.Fatal(err)
	}
	if !result.RecoveryMode {
		t.Fatal("restore without a prior baseline should report recovery mode")
	}
	restored := string(readTestFile(t, paths.Config))
	if strings.Contains(restored, "google/gemini-custom") || strings.Contains(restored, "model =") {
		t.Fatalf("provider-prefixed root model remains:\n%s", restored)
	}
	if !strings.Contains(restored, `approval_policy = "on-request"`) {
		t.Fatalf("unrelated setting was lost:\n%s", restored)
	}
}
