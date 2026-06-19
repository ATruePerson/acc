package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"
)

func main() {
	cfgPath := flag.String("config", "", "path to config.json")
	envPath := flag.String("env", os.Getenv("HOME")+"/.config/acc/.env", "dotenv file with provider keys")
	tuiFlag := flag.Bool("tui", false, "launch interactive TUI dashboard")
	uiFlag := flag.Bool("ui", false, "launch web UI dashboard in Safari")
	flag.Parse()

	loadDotenv(*envPath)

	path := *cfgPath
	if path == "" {
		if _, err := os.Stat("config.json"); err == nil {
			path = "config.json"
		} else {
			path = os.Getenv("HOME") + "/.config/acc/config.json"
		}
	}

	cfg, err := loadConfig(path)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := validateConfig(cfg); err != nil {
		log.Fatalf("config: %v", err)
	}

	s := &server{cfg: cfg, http: &http.Client{Timeout: 5 * time.Minute}}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", s.handleMessages)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("acc-proxy ok"))
	})

	mux.HandleFunc("/dashboard", s.handleDashboard)
	mux.HandleFunc("/dashboard/api/logs", s.handleDashboardLogs)
	mux.HandleFunc("/dashboard/api/clear", s.handleDashboardClear)
	mux.HandleFunc("/dashboard/api/restart", s.handleDashboardRestart)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/dashboard", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})

	addr := fmt.Sprintf(":%d", cfg.Port)

	srv := &http.Server{Addr: addr, Handler: corsMiddleware(mux)}

	if *tuiFlag {
		killPortOwner(cfg.Port)
		go func() {
			if err := srv.ListenAndServe(); err != http.ErrServerClosed {
				log.Fatal(err)
			}
		}()

		stopChan := make(chan bool, 1)
		RunTUI(cfg.Port, stopChan)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	} else {
		if *uiFlag {
			killPortOwner(cfg.Port)
			log.Printf("acc Web UI: launching dashboard in Safari...")
			exec.Command("open", fmt.Sprintf("http://localhost:%d/dashboard", cfg.Port)).Start()
		}

		log.Printf("acc on %s — point ANTHROPIC_BASE_URL at http://localhost%s", addr, addr)
		go func() {
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
			<-sig
			log.Print("caught signal, shutting down...")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			srv.Shutdown(ctx)
		}()

		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}
}

type server struct {
	cfg  *Config
	http *http.Client
}

func (s *server) handleMessages(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		httpErr(w, 400, "read body: "+err.Error())
		return
	}

	var ar AnthropicRequest
	if err := json.Unmarshal(raw, &ar); err != nil {
		httpErr(w, 400, "parse request: "+err.Error())
		return
	}

	route, err := s.routeFor(ar.Model)
	if err != nil {
		httpErr(w, 400, err.Error())
		AddTUILog(LogEntry{
			Timestamp: time.Now(),
			Model:     ar.Model,
			Route:     "error",
			Status:    400,
		})
		return
	}
	prov, ok := s.cfg.Providers[route.Provider]
	if !ok {
		httpErr(w, 500, "unknown provider: "+route.Provider)
		AddTUILog(LogEntry{
			Timestamp: time.Now(),
			Model:     ar.Model,
			Route:     route.Model,
			Status:    500,
		})
		return
	}

	or, err := translateRequest(&ar, route, s.cfg)
	if err != nil {
		httpErr(w, 400, "translate: "+err.Error())
		AddTUILog(LogEntry{
			Timestamp: time.Now(),
			Model:     ar.Model,
			Route:     route.Model,
			Status:    400,
		})
		return
	}

	body, _ := json.Marshal(or)
	upstream, err := http.NewRequestWithContext(r.Context(), "POST", prov.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		httpErr(w, 500, err.Error())
		AddTUILog(LogEntry{
			Timestamp: time.Now(),
			Model:     ar.Model,
			Route:     route.Model,
			Status:    500,
		})
		return
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("Authorization", "Bearer "+prov.APIKey)

	resp, err := s.http.Do(upstream)
	if err != nil {
		httpErr(w, 502, "upstream: "+err.Error())
		AddTUILog(LogEntry{
			Timestamp: time.Now(),
			Model:     ar.Model,
			Route:     route.Model,
			Status:    502,
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		log.Printf("upstream %d for model=%s->%s/%s: %s", resp.StatusCode, ar.Model, route.Provider, route.Model, truncate(string(b), 500))
		httpErr(w, resp.StatusCode, fmt.Sprintf("upstream %s/%s: %s", route.Provider, route.Model, truncate(string(b), 300)))
		AddTUILog(LogEntry{
			Timestamp: time.Now(),
			Model:     ar.Model,
			Route:     route.Model,
			Status:    resp.StatusCode,
		})
		return
	}

	if ar.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		outTokens := streamTranslate(w, resp.Body, ar.Model)
		AddTUILog(LogEntry{
			Timestamp: time.Now(),
			Model:     ar.Model,
			Route:     route.Model,
			Status:    resp.StatusCode,
			TokensIn:  0,
			TokensOut: outTokens,
		})
		return
	}

	var oresp OpenAIResponse
	b, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(b, &oresp); err != nil {
		httpErr(w, 502, "parse upstream: "+err.Error())
		AddTUILog(LogEntry{
			Timestamp: time.Now(),
			Model:     ar.Model,
			Route:     route.Model,
			Status:    502,
		})
		return
	}
	out := translateResponse(&oresp, ar.Model)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)

	tokensIn := 0
	tokensOut := 0
	if oresp.Usage != nil {
		tokensIn = oresp.Usage.PromptTokens
		tokensOut = oresp.Usage.CompletionTokens
	}
	AddTUILog(LogEntry{
		Timestamp: time.Now(),
		Model:     ar.Model,
		Route:     route.Model,
		Status:    resp.StatusCode,
		TokensIn:  tokensIn,
		TokensOut: tokensOut,
	})
}

func (s *server) handleModels(w http.ResponseWriter, r *http.Request) {
	ids := []string{
		"anthropic/opencode/big-pickle",
		"anthropic/claude_step_3.7_flash",
		"anthropic/claude_K_2",
		"anthropic/claude_M_2.6",
	}
	var data []map[string]any
	for _, id := range ids {
		data = append(data, map[string]any{
			"type": "model", "id": id, "display_name": id,
			"created_at": "2025-01-01T00:00:00Z",
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"data": data, "has_more": false})
}

func (s *server) routeFor(model string) (Route, error) {
	cleanModel := strings.TrimPrefix(model, "anthropic/")
	normalizedModel := strings.ToLower(strings.ReplaceAll(cleanModel, "_", "-"))

	switch normalizedModel {
	case "claude-tencent-hy3-preview":
		return Route{Provider: "openrouter", Model: "tencent/hy3-preview"}, nil
	case "claude-big-pickle", "opencode/big-pickle":
		return Route{Provider: "opencode", Model: "big-pickle", ReasoningEffort: "high"}, nil
	case "claude-mimo-v2.5-free", "opencode/mimo-v2.5-free", "claude-m-2.6":
		return Route{Provider: "opencode", Model: "mimo-v2.5-free", ReasoningEffort: "high"}, nil
	case "claude-step-3.7-flash", "stepfun-ai/step-3.7-flash", "stepfun-ai-step-3.7-flash", "stepfun-ai-step-3-7-flash", "stepfun_ai_step_3.7_flash":
		return Route{Provider: "nvidia", Model: "stepfun-ai/step-3.7-flash", ReasoningEffort: "max"}, nil
	case "claude-kimi-k2", "claude-kim-2", "claude-k-2":
		return Route{Provider: "nvidia", Model: "moonshotai/kimi-k2.6", ReasoningEffort: "high"}, nil
	case "claude-nemotron-ultra":
		return Route{Provider: "nvidia", Model: "nvidia/nemotron-3-ultra-550b-a55b"}, nil
	}

	if parts := strings.SplitN(model, "/", 3); len(parts) == 3 {
		if _, ok := s.cfg.Providers[parts[1]]; ok {
			return Route{Provider: parts[1], Model: parts[2]}, nil
		}
	}

	m := strings.ToLower(normalizedModel)
	for _, fam := range []string{"opus", "sonnet", "haiku"} {
		if strings.Contains(m, fam) {
			if r, ok := s.cfg.Routes[fam]; ok {
				return r, nil
			}
		}
	}

	return Route{}, fmt.Errorf("unrecognized model ID %q — did you mean anthropic/claude-kimi-k2 or a direct provider path like anthropic/nvidia/moonshotai/kimi-k2.6?", model)
}

// ---------- Config ----------

func loadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b = expandEnv(b)
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.Port == 0 {
		c.Port = 8787
	}
	return &c, nil
}

func validateConfig(cfg *Config) error {
	for slot, route := range cfg.Routes {
		if _, ok := cfg.Providers[route.Provider]; !ok {
			return fmt.Errorf("route %q: provider %q not defined", slot, route.Provider)
		}
	}
	if cfg.Vision != nil {
		if _, ok := cfg.Providers[cfg.Vision.Provider]; !ok {
			return fmt.Errorf("vision route: provider %q not defined", cfg.Vision.Provider)
		}
	}
	for name, e := range cfg.Effort {
		if e.Budget <= 0 {
			return fmt.Errorf("effort %q: budget must be > 0", name)
		}
	}
	return nil
}

// ---------- Networking ----------

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key, anthropic-version")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- Dotenv ----------

func loadDotenv(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if k != "" && os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

var envRe = regexp.MustCompile(`\$\{([A-Z0-9_]+)\}`)

func expandEnv(b []byte) []byte {
	return envRe.ReplaceAllFunc(b, func(m []byte) []byte {
		name := envRe.FindSubmatch(m)[1]
		return []byte(os.Getenv(string(name)))
	})
}

// ---------- HTTP helpers ----------

func httpErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{
		"type":  "error",
		"error": map[string]any{"type": "proxy_error", "message": msg},
	})
}

func randID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func killPortOwner(port int) {
	cmd := exec.Command("lsof", "-t", "-i", fmt.Sprintf("tcp:%d", port))
	out, err := cmd.Output()
	if err != nil {
		return
	}
	pidStr := strings.TrimSpace(string(out))
	if pidStr == "" {
		return
	}
	for _, line := range strings.Split(pidStr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var pid int
		if _, err := fmt.Sscanf(line, "%d", &pid); err == nil {
			if pid != os.Getpid() {
				if proc, err := os.FindProcess(pid); err == nil {
					proc.Signal(syscall.SIGTERM)
					time.Sleep(200 * time.Millisecond)
				}
			}
		}
	}
}

