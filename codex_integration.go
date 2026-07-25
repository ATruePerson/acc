package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type codexProcessOwnership struct {
	PID        int       `json:"pid"`
	Executable string    `json:"executable"`
	StartedAt  time.Time `json:"started_at"`
}

func codexFrontGatewayURL(cfg *Config) string {
	return fmt.Sprintf("http://127.0.0.1:%d/v1", cfg.Port)
}

func codexPaths() (configPath, catalogPath, restorePath string, err error) {
	codexDir, err := codexHomeDir()
	if err != nil {
		return "", "", "", err
	}
	return filepath.Join(codexDir, "config.toml"), filepath.Join(codexDir, "acc-models.json"), filepath.Join(accDir(), "codex-restore.json"), nil
}

func codexPIDPath() string { return filepath.Join(accDir(), "codex-service.json") }

func codexRestartPath() string { return filepath.Join(accDir(), "codex-restart-required") }

func loadCodexRuntime() (*Config, *authManager, error) {
	loadDotenv(defaultEnvPath())
	cfg, err := loadConfig(defaultConfigPath())
	if err != nil {
		return nil, nil, fmt.Errorf("load ACC config: %w", err)
	}
	if err := validateConfig(cfg); err != nil {
		return nil, nil, fmt.Errorf("validate ACC config: %w", err)
	}
	auth, authErr := newDefaultAuthManager()
	if authErr != nil {
		// Configured API-key providers remain usable even when native OAuth storage
		// is unavailable (for example on non-macOS hosts).
		auth.storeName = "unavailable: " + authErr.Error()
	}
	return cfg, auth, nil
}

func defaultCodexModelFor(cfg *Config, auth *authManager) (string, error) {
	models := codexNamedModelsWithAuth(cfg, auth)
	if len(models) == 0 {
		return "", fmt.Errorf("no real Codex models are available")
	}
	return models[0].ID, nil
}

func configureNativeCodex(cfg *Config, auth *authManager, model string) error {
	if model == "" {
		var err error
		model, err = defaultCodexModelFor(cfg, auth)
		if err != nil {
			return err
		}
	}
	configPath, catalogPath, restorePath, err := codexPaths()
	if err != nil {
		return err
	}
	return configureCodexAppWithAuth(configPath, catalogPath, restorePath, codexFrontGatewayURL(cfg), model, cfg, auth)
}

func refreshAvailableProviderCatalogs(cfg *Config, auth *authManager) []string {
	client := &http.Client{Timeout: 15 * time.Second}
	var warnings []string
	for _, provider := range []string{"kimi", "xai", "anthropic"} {
		if !providerConfigured(cfg, auth, provider) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := refreshProviderModelCache(ctx, client, cfg, auth, provider)
		cancel()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s model refresh: %v", provider, err))
		}
	}
	return warnings
}

func codexIntegrationStatus(cfg *Config, auth *authManager) map[string]any {
	configPath, _, restorePath, _ := codexPaths()
	config, _ := os.ReadFile(configPath)
	routing := inspectCodexRouting(string(config))
	processRunning := ownedCodexProcessRunning(codexPIDPath())
	return map[string]any{
		"mode":                  routing.Mode,
		"acc_process":           ternary(processRunning, "Running", "Stopped"),
		"codex_endpoint":        routing.Endpoint,
		"active_model_provider": routing.Provider,
		"active_catalog":        routing.Catalog,
		"subscription_baseline": codexBaselineStatus(restorePath, string(config)),
		"restart_chatgpt":       ternary(codexRestartRequired(codexRestartPath()), "Yes", "No"),
	}
}

func ternary[T any](condition bool, yes, no T) T {
	if condition {
		return yes
	}
	return no
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func codexRestartRequired(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if runtime.GOOS != "darwin" {
		return true
	}
	output, err := exec.Command("ps", "-axo", "lstart=,command=").Output()
	if err != nil {
		return true
	}
	latest := time.Time{}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		command := strings.Join(fields[5:], " ")
		if !strings.Contains(command, "/ChatGPT.app/") && !strings.Contains(command, "/Codex.app/") {
			continue
		}
		started, parseErr := time.ParseInLocation("Mon Jan _2 15:04:05 2006", strings.Join(fields[:5], " "), time.Local)
		if parseErr == nil && started.After(latest) {
			latest = started
		}
	}
	if !latest.IsZero() && latest.After(info.ModTime()) {
		_ = clearCodexRestartRequired(path)
		return false
	}
	return true
}

func printCodexStatus() {
	// Status only inspects Codex lifecycle state. It remains usable even when
	// ACC's provider configuration is missing or currently invalid.
	status := codexIntegrationStatus(nil, nil)
	fmt.Printf("  Mode: %s\n", status["mode"])
	fmt.Printf("  ACC process: %s\n", status["acc_process"])
	fmt.Printf("  Codex endpoint: %s\n", status["codex_endpoint"])
	fmt.Printf("  Active model provider: %s\n", status["active_model_provider"])
	fmt.Printf("  Active catalog: %s\n", status["active_catalog"])
	fmt.Printf("  Subscription baseline: %s\n", status["subscription_baseline"])
	fmt.Printf("  Restart ChatGPT required: %s\n", status["restart_chatgpt"])
}

func cmdCodexLifecycle(args []string) {
	command := args[0]
	model := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--model", "-m":
			if i+1 >= len(args) {
				fmt.Println("  Missing model after", args[i])
				return
			}
			i++
			model = args[i]
		default:
			fmt.Printf("  Unknown `acc codex %s` argument %q\n", command, args[i])
			return
		}
	}

	switch command {
	case "status":
		printCodexStatus()
		return
	case "restore", "remove":
		stopped, err := stopOwnedCodexProcess(codexPIDPath())
		if err != nil {
			fmt.Printf("  Could not stop the ACC-managed Codex service: %v\n", err)
			return
		}
		result, err := restoreCodexSettingsDetailed()
		if err != nil {
			fmt.Printf("  Could not restore Codex subscription mode: %v\n", err)
			return
		}
		if result.RecoveryMode {
			fmt.Println("  Restored subscription mode using a newly constructed sanitized recovery baseline.")
		} else {
			fmt.Println("  Restored the durable sanitized subscription baseline.")
		}
		if stopped {
			fmt.Println("  Stopped the ACC process owned by `acc codex start`.")
		}
		fmt.Printf("  Subscription authentication files: %s\n", ternary(result.AuthUnchanged, "Unchanged", "Verification failed"))
		fmt.Printf("  Restart ChatGPT required: %s. Fully quit and reopen ChatGPT Desktop before using Codex.\n", ternary(result.RestartRequired || codexRestartRequired(codexRestartPath()), "Yes", "No"))
		return
	case "stop":
		stopped, err := stopOwnedCodexProcess(codexPIDPath())
		if err != nil {
			fmt.Printf("  Could not stop the ACC-managed Codex service: %v\n", err)
			return
		}
		if stopped {
			fmt.Println("  Stopped the ACC process started by `acc codex start`.")
		} else {
			fmt.Println("  No ACC-owned Codex process is running. Other ACC processes were left alone.")
		}
		return
	}

	cfg, auth, err := loadCodexRuntime()
	if err != nil {
		fmt.Printf("  Could not prepare ACC: %v\n", err)
		return
	}
	if command == "doctor" {
		runCodexDoctor(os.Stdout, cfg, auth)
		return
	}
	if command != "setup" && command != "start" {
		fmt.Printf("  Unknown Codex command %q\n", command)
		return
	}
	for _, warning := range refreshAvailableProviderCatalogs(cfg, auth) {
		fmt.Printf("  Warning: %s. Using cached/static catalog data.\n", warning)
	}
	if model == "" {
		model, err = defaultCodexModelFor(cfg, auth)
		if err != nil {
			fmt.Printf("  Could not choose a Codex model: %v\n", err)
			return
		}
	}
	if !isCodexModelWithAuth(cfg, auth, model) {
		fmt.Printf("  Unknown or unavailable real model %q. Run `acc models`.\n", model)
		return
	}
	configPath, catalogPath, restorePath, err := codexPaths()
	if err != nil {
		fmt.Printf("  Could not locate Codex settings: %v\n", err)
		return
	}
	tx, err := beginConfigureCodexApp(configPath, catalogPath, restorePath, codexRestartPath(), codexFrontGatewayURL(cfg), model, cfg, auth)
	if err != nil {
		fmt.Printf("  Could not configure Codex: %v\n", err)
		return
	}
	if command == "setup" {
		result := tx.Commit()
		fmt.Printf("  Codex now points directly to ACC with %s. Nothing was started.\n", model)
		fmt.Printf("  Subscription baseline: %s. Restart ChatGPT required: %s.\n", ternary(result.BaselineCreated, "Created", "Preserved"), ternary(result.RestartRequired, "Yes", "No"))
		return
	}

	base := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	_, newlyStarted, startErr := startOwnedCodexProcess(base)
	if startErr != nil {
		_ = tx.Rollback()
		fmt.Printf("  Could not start ACC-owned Codex service: %v\n", startErr)
		return
	}
	fail := func(message string) {
		if newlyStarted {
			_, _ = stopOwnedCodexProcess(codexPIDPath())
		}
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			fmt.Printf("  %s Rollback also failed: %v\n", message, rollbackErr)
			return
		}
		fmt.Println("  " + message + " Pre-command Codex files were restored.")
	}
	if err := validateCodexLoopbackBaseURL(codexFrontGatewayURL(cfg)); err != nil {
		fail("ACC endpoint is not loopback-only.")
		return
	}
	if !responsesEndpointReady(base) {
		fail(fmt.Sprintf("%s/v1/responses did not pass its compatibility probe.", base))
		return
	}
	configBody, configErr := os.ReadFile(configPath)
	catalogBody, catalogErr := os.ReadFile(catalogPath)
	routing := inspectCodexRouting(string(configBody))
	validCatalog, _ := validateCodexCatalog(catalogBody)
	if configErr != nil || catalogErr != nil || routing.Mode != "ACC" || routing.Provider != "acc" || !validCatalog || !catalogHasCodexModel(catalogBody, model) {
		fail("ACC configuration verification failed.")
		return
	}
	result := tx.Commit()
	fmt.Printf("  Codex is using ACC directly at %s (%s). OpenCodex was not started.\n", codexFrontGatewayURL(cfg), model)
	fmt.Printf("  Subscription baseline: %s. Restart ChatGPT required: %s.\n", ternary(result.BaselineCreated, "Created", "Preserved"), ternary(result.RestartRequired || codexRestartRequired(codexRestartPath()), "Yes", "No"))
}

func responsesEndpointReady(base string) bool {
	request, _ := http.NewRequest(http.MethodPost, strings.TrimRight(base, "/")+"/v1/responses", strings.NewReader(`{"model":"__acc_probe__","input":"probe"}`))
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	return response.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(string(body)), "model")
}

func runCodexDoctor(out io.Writer, cfg *Config, auth *authManager) bool {
	configPath, _, restorePath, _ := codexPaths()
	config, configErr := os.ReadFile(configPath)
	if os.IsNotExist(configErr) {
		configErr = nil
		config = nil
	}
	routing := inspectCodexRouting(string(config))
	ok := true
	check := func(name string, pass bool, detail string) {
		mark := "OK"
		if !pass {
			mark, ok = "FAIL", false
		}
		fmt.Fprintf(out, "  %-4s %-26s %s\n", mark, name, detail)
	}
	check("Codex config syntax", configErr == nil && validateCodexConfigText(string(config)) == nil, configPath)
	check("active routing mode", routing.Mode == "ACC" || routing.Mode == "Subscription", routing.Mode)
	check("legacy OpenCodex routing", !routing.ActiveOpenCodex, "active port 10100/provider routing absent")
	loopbackOK := routing.Mode != "ACC" || validateCodexLoopbackBaseURL(routing.Endpoint) == nil
	check("loopback binding", loopbackOK, routing.Endpoint)

	base := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	running := proxyAlive(base)
	owned := ownedCodexProcessRunning(codexPIDPath())
	if routing.Mode == "ACC" {
		check("ACC endpoint", running, base)
		check("Responses compatibility", running && responsesEndpointReady(base), base+"/v1/responses")
		activeCatalog := resolveCodexPath(routing.Catalog, configPath)
		catalog, catalogErr := os.ReadFile(activeCatalog)
		validCatalog, catalogDetail := validateCodexCatalog(catalog)
		check("real-model catalog", catalogErr == nil && validCatalog, catalogDetail)
		check("ACC process ownership", owned, codexPIDPath())
	} else {
		check("ACC endpoint", true, "not required in subscription mode")
		check("Responses compatibility", true, "not required in subscription mode")
		check("real-model catalog", routing.Catalog == "Built-in", routing.Catalog)
		check("ACC process ownership", !owned, "no owned process expected")
	}
	check("stream/tool translation", codexTransportSelfTest(), "local deterministic conversion")
	baseline := codexBaselineStatus(restorePath, string(config))
	check("subscription baseline", baseline == "Valid" || baseline == "Recoverable", baseline)
	check("port ownership", routing.Mode != "ACC" || running && owned, strconv.Itoa(cfg.Port))
	storeReady := auth != nil && auth.store != nil
	storeName := "unavailable"
	if auth != nil {
		storeName = auth.storeName
	}
	check("credential store", storeReady, storeName)
	for _, provider := range []string{"kimi", "xai", "anthropic"} {
		state := "login required"
		if providerConfigured(cfg, auth, provider) {
			state = "ready"
		}
		fmt.Fprintf(out, "  INFO auth %-21s %s\n", provider, state)
	}
	return ok
}

func validateCodexCatalog(body []byte) (bool, string) {
	var catalog struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	if len(body) == 0 || json.Unmarshal(body, &catalog) != nil {
		return false, "missing or malformed JSON"
	}
	seenSlugs := map[string]bool{}
	seenModels := map[string]bool{}
	for _, model := range catalog.Models {
		lower := strings.ToLower(model.Slug)
		if seenSlugs[model.Slug] || lower == "opus" || lower == "sonnet" || lower == "haiku" {
			return false, "duplicate, ambiguous, or forbidden model ID: " + model.Slug
		}
		provider, upstreamModel, ok := decodeCodexSlug(model.Slug)
		if !ok {
			return false, "malformed slug (must contain exactly one slash with valid encoding): " + model.Slug
		}
		if provider == "" || upstreamModel == "" {
			return false, "empty provider or upstream model in slug: " + model.Slug
		}
		modelKey := provider + "\x00" + upstreamModel
		if seenModels[modelKey] {
			return false, "duplicate encoded model (two different slugs decode to same provider/model): " + model.Slug
		}
		seenSlugs[model.Slug] = true
		seenModels[modelKey] = true
	}
	if len(seenSlugs) == 0 {
		return false, "catalog has no models"
	}
	return true, fmt.Sprintf("%d unique provider-prefixed models", len(seenSlugs))
}

func codexTransportSelfTest() bool {
	req := &ResponsesRequest{Model: "test", Input: json.RawMessage(`"hello"`), Stream: true, Tools: []ResponsesTool{{Type: "function", Name: "read", Parameters: json.RawMessage(`{"type":"object"}`)}}}
	or, _, err := translateFromResponsesWithTools(req, Route{Provider: "test", Model: "real"}, &Config{})
	return err == nil && or.Stream && len(or.Tools) == 1 && len(or.Messages) == 1
}

func restoreCodexSettings() error {
	_, err := restoreCodexSettingsDetailed()
	return err
}

func restoreCodexSettingsDetailed() (codexRestoreResult, error) {
	configPath, catalogPath, restorePath, err := codexPaths()
	if err != nil {
		return codexRestoreResult{}, err
	}
	return restoreCodexAppDetailed(configPath, catalogPath, restorePath, codexRestartPath())
}

func removeNativeCodexSettings() error {
	_, err := restoreCodexSettingsDetailed()
	return err
}

func ownedCodexProcessRunning(path string) bool {
	ownership, err := readCodexProcessOwnership(path)
	return err == nil && processMatchesOwnership(ownership)
}

func readCodexProcessOwnership(path string) (codexProcessOwnership, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return codexProcessOwnership{}, err
	}
	var ownership codexProcessOwnership
	if json.Unmarshal(body, &ownership) != nil || ownership.PID <= 1 || ownership.Executable == "" {
		return codexProcessOwnership{}, fmt.Errorf("invalid Codex service ownership file")
	}
	return ownership, nil
}

func processExists(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func processMatchesOwnership(ownership codexProcessOwnership) bool {
	if !processExists(ownership.PID) {
		return false
	}
	output, err := exec.Command("ps", "-p", strconv.Itoa(ownership.PID), "-o", "command=").Output()
	if err != nil {
		return false
	}
	command := strings.TrimSpace(string(output))
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	actual, _ := filepath.EvalSymlinks(fields[0])
	expected, _ := filepath.EvalSymlinks(ownership.Executable)
	return fields[0] == ownership.Executable || actual != "" && expected != "" && actual == expected
}

func startOwnedCodexProcess(base string) (codexProcessOwnership, bool, error) {
	path := codexPIDPath()
	if ownership, err := readCodexProcessOwnership(path); err == nil {
		if processMatchesOwnership(ownership) {
			if proxyAlive(base) {
				return ownership, false, nil
			}
			if err := stopOwnedProcess(ownership); err != nil {
				return codexProcessOwnership{}, false, err
			}
			_ = os.Remove(path)
		} else if processExists(ownership.PID) {
			return codexProcessOwnership{}, false, fmt.Errorf("PID %d is alive but no longer matches the recorded ACC executable; refusing to replace or kill it", ownership.PID)
		} else {
			_ = os.Remove(path)
		}
	} else if !os.IsNotExist(err) {
		return codexProcessOwnership{}, false, err
	}
	if proxyAlive(base) {
		return codexProcessOwnership{}, false, fmt.Errorf("an unowned process is already serving %s; stop it before `acc codex start`", base)
	}
	pid, executable, err := startProxyDetachedWithPID()
	if err != nil {
		return codexProcessOwnership{}, false, err
	}
	ownership := codexProcessOwnership{PID: pid, Executable: executable, StartedAt: time.Now().UTC()}
	encoded, _ := json.MarshalIndent(ownership, "", "  ")
	if err := atomicWriteFile(path, append(encoded, '\n'), 0600); err != nil {
		_ = syscall.Kill(pid, syscall.SIGTERM)
		return codexProcessOwnership{}, false, fmt.Errorf("record ACC process ownership: %w", err)
	}
	if !waitForProxy(base, 10*time.Second) {
		_ = stopOwnedProcess(ownership)
		_ = os.Remove(path)
		return codexProcessOwnership{}, false, fmt.Errorf("ACC did not become healthy at %s", base)
	}
	return ownership, true, nil
}

func stopOwnedCodexProcess(path string) (bool, error) {
	ownership, err := readCodexProcessOwnership(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !processExists(ownership.PID) {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return false, err
		}
		return false, nil
	}
	if !processMatchesOwnership(ownership) {
		return false, fmt.Errorf("PID %d no longer matches the recorded ACC executable; refusing to kill it", ownership.PID)
	}
	if err := stopOwnedProcess(ownership); err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return true, err
	}
	return true, nil
}

func stopOwnedProcess(ownership codexProcessOwnership) error {
	if err := syscall.Kill(ownership.PID, syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(ownership.PID, 0); err != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("ACC process %d did not stop after SIGTERM", ownership.PID)
}
