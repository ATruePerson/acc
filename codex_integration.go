package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

//go:embed scripts/opencodex/acc-opencodex
var embeddedOpenCodexHelper []byte

const openCodexHelperName = "acc-opencodex"

func openCodexHelperPath() string {
	return filepath.Join(accDir(), openCodexHelperName)
}

func ensureOpenCodexHelper() (string, error) {
	path := openCodexHelperPath()
	if existing, err := os.ReadFile(path); err == nil && string(existing) == string(embeddedOpenCodexHelper) {
		return path, nil
	}
	if err := os.MkdirAll(accDir(), 0700); err != nil {
		return "", err
	}
	if err := atomicWriteFile(path, embeddedOpenCodexHelper, 0700); err != nil {
		return "", err
	}
	return path, nil
}

func runOpenCodexCommand(args ...string) error {
	if _, err := exec.LookPath("ocx"); err != nil {
		return fmt.Errorf("OpenCodex is not installed; install @bitkyc08/opencodex first")
	}
	helper, err := ensureOpenCodexHelper()
	if err != nil {
		return fmt.Errorf("prepare OpenCodex helper: %w", err)
	}
	cmd := exec.Command("bash", append([]string{helper}, args...)...)
	cmd.Env = append(os.Environ(), "ACC_CONFIG="+defaultConfigPath(), "ACC_REPO_DIR="+repoRoot())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func repoRoot() string {
	if cwd, err := os.Getwd(); err == nil {
		if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
			return cwd
		}
	}
	return filepath.Dir(openCodexHelperPath())
}

func ensureACCForCodex() (*Config, error) {
	cfg, err := loadConfig(defaultConfigPath())
	if err != nil {
		return nil, fmt.Errorf("load ACC config: %w", err)
	}
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("validate ACC config: %w", err)
	}
	loadDotenv(defaultEnvPath())
	base := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	if !proxyAlive(base) {
		if err := startProxyDetached(); err != nil {
			return nil, fmt.Errorf("start ACC: %w", err)
		}
		if !waitForProxy(base, 10*time.Second) {
			return nil, fmt.Errorf("ACC did not become healthy at %s", base)
		}
	}
	return cfg, nil
}

func codexFrontGatewayURL() string {
	return "http://127.0.0.1:10100/v1"
}

func codexPaths() (configPath, catalogPath, restorePath string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", "", err
	}
	return filepath.Join(home, ".codex", "config.toml"), filepath.Join(home, ".codex", "acc-models.json"), filepath.Join(accDir(), "codex-restore.json"), nil
}

func codexIntegrationStatus(cfg *Config) map[string]any {
	status := map[string]any{
		"acc_port":          cfg.Port,
		"opencodex_port":    10100,
		"acc_running":       proxyAlive(fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)),
		"opencodex_running": proxyAlive("http://127.0.0.1:10100/healthz"),
		"loopback_only":     true,
		"selected_model":    defaultCodexModel,
	}
	s := &server{}
	s.cfg.Store(cfg)
	if route, err := s.routeFor(defaultCodexModel); err == nil {
		status["selected_provider"] = route.Provider
		status["selected_route"] = route.Model
	}
	return status
}

func printCodexStatus() {
	cfg, err := loadConfig(defaultConfigPath())
	if err != nil {
		fmt.Printf("ACC config: FAIL (%v)\n", err)
		return
	}
	status := codexIntegrationStatus(cfg)
	b, _ := json.MarshalIndent(status, "", "  ")
	fmt.Println(string(b))
	if out, err := captureOpenCodexStatus(); err == nil && len(out) > 0 {
		fmt.Println("OpenCodex:")
		fmt.Println(string(out))
	}
}

func captureOpenCodexStatus() ([]byte, error) {
	if _, err := exec.LookPath("ocx"); err != nil {
		return nil, err
	}
	helper, err := ensureOpenCodexHelper()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("bash", helper, "status")
	cmd.Env = append(os.Environ(), "ACC_CONFIG="+defaultConfigPath(), "ACC_REPO_DIR="+repoRoot())
	return cmd.Output()
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

	if command == "status" {
		printCodexStatus()
		return
	}
	if command == "doctor" {
		if err := runOpenCodexCommand("doctor"); err != nil {
			fmt.Printf("  Codex doctor failed: %v\n", err)
		}
		return
	}
	if command == "restore" {
		if _, err := exec.LookPath("ocx"); err == nil {
			if err := runOpenCodexCommand("restore"); err != nil {
				fmt.Printf("  OpenCodex restore failed: %v\n", err)
				return
			}
		}
		if err := restoreCodexSettings(); err != nil {
			fmt.Printf("  Could not restore Codex settings: %v\n", err)
			return
		}
		fmt.Println("  Restored Codex settings.")
		return
	}
	if command == "remove" {
		cmdCodexLifecycle([]string{"restore"})
		if _, err := exec.LookPath("ocx"); err == nil {
			if err := runOpenCodexCommand("remove"); err != nil {
				fmt.Printf("  OpenCodex remove failed: %v\n", err)
				return
			}
		}
		removeOpenCodexHelper()
		fmt.Println("  Removed the ACC OpenCodex integration. Other providers were preserved.")
		return
	}
	if command == "stop" {
		if _, err := exec.LookPath("ocx"); err == nil {
			if err := runOpenCodexCommand("stop"); err != nil {
				fmt.Printf("  OpenCodex stop failed: %v\n", err)
				return
			}
		}
		if err := restoreCodexSettings(); err != nil {
			fmt.Printf("  OpenCodex stopped, but Codex settings were not restored: %v\n", err)
			return
		}
		fmt.Println("  OpenCodex stopped and Codex settings were restored.")
		return
	}

	cfg, err := ensureACCForCodex()
	if err != nil {
		fmt.Printf("  Could not prepare ACC: %v\n", err)
		return
	}
	if model == "" {
		model = defaultCodexModel
		if !isCodexModel(cfg, model) {
			ids := enabledModelIDs(cfg)
			if len(ids) == 0 {
				fmt.Println("  No enabled ACC models are configured.")
				return
			}
			model = ids[0]
		}
	}
	if !isCodexModel(cfg, model) {
		fmt.Printf("  Unknown or disabled ACC model %q. Run `acc models` to list enabled model IDs.\n", model)
		return
	}
	configPath, catalogPath, restorePath, err := codexPaths()
	if err != nil {
		fmt.Printf("  Could not find Codex settings: %v\n", err)
		return
	}
	if err := saveCodexRestoreState(configPath, catalogPath, restorePath); err != nil {
		fmt.Printf("  Could not back up Codex settings: %v\n", err)
		return
	}
	if err := runOpenCodexCommand(command); err != nil {
		fmt.Printf("  OpenCodex %s failed: %v\n", command, err)
		return
	}
	if command == "start" {
		if err := configureCodexApp(configPath, catalogPath, restorePath, codexFrontGatewayURL(), model, cfg); err != nil {
			fmt.Printf("  Could not switch Codex to OpenCodex: %v\n", err)
			return
		}
		fmt.Printf("  Codex is using ACC through OpenCodex (%s).\n", model)
		return
	}
	fmt.Printf("  OpenCodex is configured for ACC (%s). Run `acc codex start` to activate it.\n", model)
}

func restoreCodexSettings() error {
	configPath, catalogPath, restorePath, err := codexPaths()
	if err != nil {
		return err
	}
	return restoreCodexApp(configPath, catalogPath, restorePath)
}

func removeOpenCodexHelper() {
	_ = os.Remove(openCodexHelperPath())
}
