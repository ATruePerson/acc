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
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", "", err
	}
	return filepath.Join(home, ".codex", "config.toml"), filepath.Join(home, ".codex", "acc-models.json"), filepath.Join(accDir(), "codex-restore.json"), nil
}

func codexPIDPath() string { return filepath.Join(accDir(), "codex-service.json") }

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
	base := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	configPath, catalogPath, restorePath, _ := codexPaths()
	config, _ := os.ReadFile(configPath)
	catalog, catalogErr := os.ReadFile(catalogPath)
	models := codexNamedModelsWithAuth(cfg, auth)
	providers := map[string]string{}
	availableProviders := map[string]bool{}
	for _, model := range models {
		availableProviders[model.Route.Provider] = true
	}
	for _, provider := range []string{"kimi", "xai", "anthropic"} {
		if providerConfigured(cfg, auth, provider) {
			providers[provider] = "ready"
		} else {
			providers[provider] = "login required"
		}
	}
	return map[string]any{
		"acc_running":                proxyAlive(base),
		"listening_address":          fmt.Sprintf("127.0.0.1:%d", cfg.Port),
		"responses_url":              codexFrontGatewayURL(cfg) + "/responses",
		"loopback_only":              true,
		"codex_configured":           strings.Contains(string(config), `model_provider = "acc"`) && strings.Contains(string(config), codexFrontGatewayURL(cfg)),
		"catalog_valid":              catalogErr == nil && json.Valid(catalog),
		"provider_authentication":    providers,
		"available_provider_count":   len(availableProviders),
		"available_real_model_count": len(models),
		"legacy_opencodex_detected":  legacyOpenCodexDetected(string(config)),
		"backup_available":           fileExists(restorePath),
		"managed_service_running":    ownedCodexProcessRunning(codexPIDPath()),
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func printCodexStatus() {
	cfg, auth, err := loadCodexRuntime()
	if err != nil {
		fmt.Printf("  Codex status failed: %v\n", err)
		return
	}
	body, _ := json.MarshalIndent(codexIntegrationStatus(cfg, auth), "", "  ")
	fmt.Println(string(body))
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
	case "restore":
		if err := restoreCodexSettings(); err != nil {
			fmt.Printf("  Could not restore Codex settings: %v\n", err)
			return
		}
		fmt.Println("  Restored the exact previous Codex configuration. Current settings were timestamp-backed up first.")
		return
	case "remove":
		if err := removeNativeCodexSettings(); err != nil {
			fmt.Printf("  Could not remove ACC's Codex settings: %v\n", err)
			return
		}
		fmt.Println("  Removed only ACC-owned Codex settings. History, ACC, OpenCodex, and provider credentials were preserved.")
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
	if err := configureNativeCodex(cfg, auth, model); err != nil {
		fmt.Printf("  Could not configure Codex: %v\n", err)
		return
	}
	if command == "setup" {
		fmt.Printf("  Codex now points directly to ACC with %s. Nothing was started.\n", model)
		return
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	if !proxyAlive(base) {
		pid, executable, startErr := startProxyDetachedWithPID()
		if startErr != nil {
			fmt.Printf("  Could not start ACC: %v\n", startErr)
			return
		}
		ownership := codexProcessOwnership{PID: pid, Executable: executable, StartedAt: time.Now().UTC()}
		encoded, _ := json.MarshalIndent(ownership, "", "  ")
		if err := atomicWriteFile(codexPIDPath(), append(encoded, '\n'), 0600); err != nil {
			_ = syscall.Kill(pid, syscall.SIGTERM)
			fmt.Printf("  ACC started but ownership could not be recorded: %v\n", err)
			return
		}
		if !waitForProxy(base, 10*time.Second) {
			_ = stopOwnedProcess(ownership)
			_ = os.Remove(codexPIDPath())
			fmt.Printf("  ACC did not become healthy at %s\n", base)
			return
		}
	}
	if !responsesEndpointReady(base) {
		fmt.Printf("  ACC is running, but %s/v1/responses did not pass its compatibility probe.\n", base)
		return
	}
	fmt.Printf("  Codex is using ACC directly at %s (%s). OpenCodex was not started.\n", codexFrontGatewayURL(cfg), model)
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
	configPath, catalogPath, restorePath, _ := codexPaths()
	config, configErr := os.ReadFile(configPath)
	catalog, catalogErr := os.ReadFile(catalogPath)
	ok := true
	check := func(name string, pass bool, detail string) {
		mark := "OK"
		if !pass {
			mark, ok = "FAIL", false
		}
		fmt.Fprintf(out, "  %-4s %-26s %s\n", mark, name, detail)
	}
	check("Codex config syntax", configErr == nil && validateCodexConfigText(string(config)) == nil, configPath)
	base := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	running := proxyAlive(base)
	check("ACC endpoint", running, base)
	check("Responses compatibility", running && responsesEndpointReady(base), base+"/v1/responses")
	check("loopback binding", strings.Contains(string(config), "127.0.0.1") && !strings.Contains(string(config), "0.0.0.0"), "127.0.0.1 only")
	validCatalog, catalogDetail := validateCodexCatalog(catalog)
	check("real-model catalog", catalogErr == nil && validCatalog, catalogDetail)
	check("stream/tool translation", codexTransportSelfTest(), "local deterministic conversion")
	check("configuration backup", fileExists(restorePath), restorePath)
	check("legacy OpenCodex routing", !legacyOpenCodexDetected(string(config)), "port 10100 and OpenCodex markers absent")
	check("port ownership", !running || proxyAlive(base), strconv.Itoa(cfg.Port))
	storeReady := auth != nil && auth.store != nil
	check("credential store", storeReady, auth.storeName)
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
	seen := map[string]bool{}
	for _, model := range catalog.Models {
		lower := strings.ToLower(model.Slug)
		if seen[model.Slug] || !strings.Contains(model.Slug, "/") || lower == "opus" || lower == "sonnet" || lower == "haiku" {
			return false, "duplicate, ambiguous, or forbidden model ID: " + model.Slug
		}
		seen[model.Slug] = true
	}
	if len(seen) == 0 {
		return false, "catalog has no models"
	}
	return true, fmt.Sprintf("%d unique provider-prefixed models", len(seen))
}

func codexTransportSelfTest() bool {
	req := &ResponsesRequest{Model: "test", Input: json.RawMessage(`"hello"`), Stream: true, Tools: []ResponsesTool{{Type: "function", Name: "read", Parameters: json.RawMessage(`{"type":"object"}`)}}}
	or, _, err := translateFromResponsesWithTools(req, Route{Provider: "test", Model: "real"}, &Config{})
	return err == nil && or.Stream && len(or.Tools) == 1 && len(or.Messages) == 1
}

func restoreCodexSettings() error {
	configPath, catalogPath, restorePath, err := codexPaths()
	if err != nil {
		return err
	}
	return restoreCodexApp(configPath, catalogPath, restorePath)
}

func removeNativeCodexSettings() error {
	configPath, catalogPath, restorePath, err := codexPaths()
	if err != nil {
		return err
	}
	if fileExists(restorePath) {
		return restoreCodexApp(configPath, catalogPath, restorePath)
	}
	original, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(original) > 0 {
		if _, err := writeTimestampedBackup(configPath, original); err != nil {
			return err
		}
		cleaned := removeACCFromCodexConfig(string(original))
		if err := validateCodexConfigText(cleaned); err != nil {
			return err
		}
		if err := atomicWriteFile(configPath, []byte(cleaned), 0600); err != nil {
			return err
		}
	}
	if err := os.Remove(catalogPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
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

func processMatchesOwnership(ownership codexProcessOwnership) bool {
	if err := syscall.Kill(ownership.PID, 0); err != nil {
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

func stopOwnedCodexProcess(path string) (bool, error) {
	ownership, err := readCodexProcessOwnership(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
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
