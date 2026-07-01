package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// providerInfo describes a known upstream provider for the setup wizard,
// doctor health check, and default config generation.
type providerInfo struct {
	Key       string // config provider name, e.g. "nvidia"
	Label     string // human name, e.g. "NVIDIA NIM"
	EnvVar    string // dotenv variable, e.g. "NVIDIA_NIM_API_KEY"
	BaseURL   string
	SignupURL string
}

func knownProviders() []providerInfo {
	return []providerInfo{
		{"nvidia", "NVIDIA NIM (free tier, fast)", "NVIDIA_NIM_API_KEY", "https://integrate.api.nvidia.com/v1", "https://build.nvidia.com"},
		{"opencode", "OpenCode Zen (free models)", "OPENCODE_API_KEY", "https://opencode.ai/zen/v1", "https://opencode.ai"},
		{"openrouter", "OpenRouter (many models)", "OPENROUTER_API_KEY", "https://openrouter.ai/api/v1", "https://openrouter.ai/keys"},
		{"gemini", "Google Gemini", "GEMINI_API_KEY", "https://generativelanguage.googleapis.com/v1beta/openai", "https://aistudio.google.com/apikey"},
		{"zai", "Z.AI (GLM models)", "ZAI_API_KEY", "https://api.z.ai/api/paas/v4", "https://z.ai"},
	}
}

// dispatch handles `acc <subcommand>`. Returns true if a subcommand ran, so
// main() can skip starting the server. Unknown first args fall through to the
// normal flag-based server path.
func dispatch(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[1] {
	case "setup", "init":
		cmdSetup()
	case "doctor", "check":
		cmdDoctor()
	case "models", "list":
		cmdModels()
	case "bench":
		cmdBench()
	case "claude", "run":
		cmdClaude(args[2:])
	case "help", "--help", "-h":
		printHelp()
	default:
		return false
	}
	return true
}

func printHelp() {
	fmt.Print(`acc — point Claude Code at cheaper models

Usage:
  acc                 Start the proxy (use -tui for the dashboard)
  acc setup           Interactive first-time setup (keys + config)
  acc doctor          Test that your provider keys work
  acc models          List the model names you can use
  acc bench           Benchmark every persona + fallback, judged for quality
  acc claude [args]   Start the proxy and launch Claude Code through it
  acc help            Show this help

First time? Run:  acc setup
`)
}

// ---------- paths ----------

func accDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".config/acc"
	}
	return filepath.Join(home, ".config", "acc")
}

func defaultEnvPath() string    { return filepath.Join(accDir(), ".env") }
func defaultConfigPath() string { return filepath.Join(accDir(), "config.json") }

// ---------- setup wizard ----------

func cmdSetup() {
	in := bufio.NewReader(os.Stdin)
	fmt.Print(`
  acc setup
  ─────────
  This sets up acc so Claude Code can use cheaper models.
  You'll paste API keys for any providers you have. Skip the rest.

`)

	keys := map[string]string{}
	for _, p := range knownProviders() {
		fmt.Printf("  %s\n    Get a key: %s\n    Paste key (or press Enter to skip): ", p.Label, p.SignupURL)
		line, _ := in.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			keys[p.EnvVar] = line
		}
		fmt.Println()
	}

	if len(keys) == 0 {
		fmt.Println("  No keys entered — nothing to save. Run `acc setup` again when you have one.")
		return
	}

	dir := accDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		fmt.Printf("  Could not create %s: %v\n", dir, err)
		return
	}

	envPath := defaultEnvPath()
	if err := os.WriteFile(envPath, []byte(renderEnv(keys)), 0600); err != nil {
		fmt.Printf("  Could not write %s: %v\n", envPath, err)
		return
	}
	fmt.Printf("  Saved %d key(s) to %s\n", len(keys), envPath)

	cfgPath := defaultConfigPath()
	if _, err := os.Stat(cfgPath); err == nil {
		fmt.Printf("  Config already exists at %s — keeping it.\n", cfgPath)
	} else {
		if err := os.WriteFile(cfgPath, []byte(defaultConfigJSON), 0644); err != nil {
			fmt.Printf("  Could not write %s: %v\n", cfgPath, err)
			return
		}
		fmt.Printf("  Wrote default config to %s\n", cfgPath)
	}

	fmt.Print("\n  Testing your keys...\n\n")
	loadDotenv(envPath)
	for _, p := range knownProviders() {
		if _, ok := keys[p.EnvVar]; !ok {
			continue
		}
		printPing(p, keys[p.EnvVar])
	}

	fmt.Print(`
  Done. Start using it with:

      acc claude

  That launches Claude Code through acc. Happy hacking.
`)
}

// renderEnv produces dotenv file contents for the given key/value pairs,
// sorted for stable output.
func renderEnv(keys map[string]string) string {
	var names []string
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# acc provider API keys — keep this file private\n")
	for _, n := range names {
		fmt.Fprintf(&b, "%s=%s\n", n, keys[n])
	}
	return b.String()
}

// ---------- doctor ----------

func cmdDoctor() {
	envPath := defaultEnvPath()
	loadDotenv(envPath)

	fmt.Printf("\n  acc doctor — checking provider keys (%s)\n\n", envPath)
	any := false
	for _, p := range knownProviders() {
		key := os.Getenv(p.EnvVar)
		if key == "" {
			fmt.Printf("  --  %s\n        no key set (%s)\n", p.Label, p.EnvVar)
			continue
		}
		any = true
		printPing(p, key)
	}
	if !any {
		fmt.Print("\n  No keys configured yet. Run `acc setup`.\n")
	}
	fmt.Println()
}

// printPing tests one provider and prints a friendly status line.
func printPing(p providerInfo, key string) {
	switch pingProvider(p.BaseURL, key) {
	case pingOK:
		fmt.Printf("  OK  %s — key works\n", p.Label)
	case pingBadKey:
		fmt.Printf("  XX  %s — key rejected (check the key)\n", p.Label)
	default:
		fmt.Printf("  ??  %s — could not reach provider\n", p.Label)
	}
}

type pingResult int

const (
	pingUnreachable pingResult = iota
	pingBadKey
	pingOK
)

// pingProvider does a cheap GET /models to verify a key without spending
// tokens. 200 means good, 401/403 means the key is bad, anything else (or a
// network error) means unreachable.
func pingProvider(baseURL, key string) pingResult {
	req, err := http.NewRequest("GET", strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return pingUnreachable
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return pingUnreachable
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return pingBadKey
	case resp.StatusCode < 500:
		return pingOK
	default:
		return pingUnreachable
	}
}

// ---------- models ----------

func cmdModels() {
	cfgPath := defaultConfigPath()
	var cfg *Config
	if c, err := loadConfig(cfgPath); err == nil {
		cfg = c
	}

	fmt.Print("\n  Model names you can give Claude Code (set as the model):\n\n")
	for _, d := range modelCatalog() {
		fmt.Printf("  anthropic/%-26s → %s (%s)\n", d.Canonical, d.Route.Model, d.Route.Provider)
	}
	if cfg != nil && len(cfg.Aliases) > 0 {
		fmt.Print("\n  Your custom aliases (from config.json):\n\n")
		var names []string
		for k := range cfg.Aliases {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			r := cfg.Aliases[k]
			fmt.Printf("  anthropic/%-26s → %s (%s)\n", normalizeModelID(k), r.Model, r.Provider)
		}
	}
	fmt.Print("\n  Or use the family names (opus / sonnet / haiku) — those follow config.json routes.\n\n")
}

// ---------- claude launcher ----------

func cmdClaude(extra []string) {
	cfg, err := loadConfig(defaultConfigPath())
	if err != nil {
		fmt.Printf("  No config found. Run `acc setup` first. (%v)\n", err)
		return
	}
	loadDotenv(defaultEnvPath())

	base := fmt.Sprintf("http://localhost:%d", cfg.Port)
	if !proxyAlive(base) {
		fmt.Printf("  Starting acc on port %d...\n", cfg.Port)
		if err := startProxyDetached(); err != nil {
			fmt.Printf("  Could not start acc: %v\n", err)
			return
		}
		if !waitForProxy(base, 10*time.Second) {
			fmt.Println("  acc did not come up in time. Try `acc` in another terminal.")
			return
		}
	}

	claude, err := exec.LookPath("claude")
	if err != nil {
		fmt.Printf("  Claude Code not found on PATH. acc is running at %s —\n  set ANTHROPIC_BASE_URL=%s in your client.\n", base, base)
		return
	}

	fmt.Printf("  Launching Claude Code through acc (%s)...\n\n", base)
	cmd := exec.Command(claude, extra...)
	cmd.Env = append(os.Environ(), "ANTHROPIC_BASE_URL="+base)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Run()
}

func proxyAlive(base string) bool {
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(base + "/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func waitForProxy(base string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if proxyAlive(base) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// startProxyDetached launches this same binary as a background proxy.
func startProxyDetached() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self)
	cmd.Stdout, cmd.Stderr = nil, nil
	return cmd.Start()
}

// defaultConfigJSON is the config written by `acc setup`. Providers reference
// ${ENV_VAR} placeholders resolved at load time from the .env file.
const defaultConfigJSON = `{
  "port": 9999,
  "system_prepend": "Always respond in English unless the user explicitly writes in another language.",
  "providers": {
    "nvidia":     { "base_url": "https://integrate.api.nvidia.com/v1", "api_key": "${NVIDIA_NIM_API_KEY}" },
    "gemini":     { "base_url": "https://generativelanguage.googleapis.com/v1beta/openai", "api_key": "${GEMINI_API_KEY}" },
    "openrouter": { "base_url": "https://openrouter.ai/api/v1", "api_key": "${OPENROUTER_API_KEY}" },
    "zai":        { "base_url": "https://api.z.ai/api/paas/v4", "api_key": "${ZAI_API_KEY}" },
    "opencode":   { "base_url": "https://opencode.ai/zen/v1", "api_key": "${OPENCODE_API_KEY}" }
  },
  "routes": {
    "opus":   { "provider": "nvidia",   "model": "z-ai/glm-5.1" },
    "sonnet": { "provider": "opencode", "model": "big-pickle" },
    "haiku":  { "provider": "nvidia",   "model": "stepfun-ai/step-3.7-flash" }
  },
  "effort": {
    "low":       { "budget": 2000,  "reasoning": "low" },
    "medium":    { "budget": 6000,  "reasoning": "low" },
    "high":      { "budget": 16000, "reasoning": "medium" },
    "xhigh":     { "budget": 24000, "reasoning": "high" },
    "max":       { "budget": 32000, "reasoning": "high" },
    "ultracode": { "budget": 48000, "reasoning": "high" }
  }
}
`
