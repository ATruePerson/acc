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
	"sort"
	"strings"
	"syscall"
	"time"
)

func main() {
	// Subcommands (setup, doctor, models, claude, help) run and exit before the
	// flag-based server path.
	if dispatch(os.Args) {
		return
	}

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
		RunTUI(cfg, stopChan)

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

// maxRequestBytes caps the request body the proxy will buffer, so a runaway
// or malicious client can't drive the process out of memory. Generous enough
// for base64 image blocks.
const maxRequestBytes = 32 << 20 // 32 MiB

func (s *server) handleMessages(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
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

	budget := 0
	if ar.Thinking != nil {
		budget = ar.Thinking.BudgetTokens
	}
	// logit records one request to the TUI + persistent metrics log. Centralized
	// so every exit path logs consistently instead of repeating the struct.
	logit := func(routeModel string, status, in, out int, effort string) {
		AddTUILog(LogEntry{
			Timestamp: time.Now(),
			Model:     ar.Model,
			Route:     routeModel,
			Status:    status,
			TokensIn:  in,
			TokensOut: out,
			Budget:    budget,
			Effort:    effort,
			CostUSD:   costFor(routeModel, in, out, s.cfg),
		})
	}

	route, err := s.routeFor(ar.Model)
	if err != nil {
		httpErr(w, 400, err.Error())
		logit("error", 400, 0, 0, "")
		return
	}
	prov, ok := s.cfg.Providers[route.Provider]
	if !ok {
		httpErr(w, 500, "unknown provider: "+route.Provider)
		logit(route.Model, 500, 0, 0, "")
		return
	}

	or, err := translateRequest(&ar, route, s.cfg)
	if err != nil {
		httpErr(w, 400, "translate: "+err.Error())
		logit(route.Model, 400, 0, 0, "")
		return
	}

	body, _ := json.Marshal(or)

	var resp *http.Response
	for attempt := 1; attempt <= 10; attempt++ {
		var err error
		upstream, err := http.NewRequestWithContext(r.Context(), "POST", prov.BaseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			httpErr(w, 500, err.Error())
			logit(route.Model, 500, 0, 0, or.ReasoningEffort)
			return
		}
		upstream.Header.Set("Content-Type", "application/json")
		upstream.Header.Set("Authorization", "Bearer "+prov.APIKey)

		resp, err = s.http.Do(upstream)
		if err != nil {
			httpErr(w, 502, "upstream: "+err.Error())
			logit(route.Model, 502, 0, 0, or.ReasoningEffort)
			return
		}

		if (resp.StatusCode == 429 || resp.StatusCode == 503) && attempt < 10 {
			// Exponential backoff with jitter
			baseInt := 1 << attempt
			base := float64(baseInt)
			// Add 0-50% jitter
			jitter := base * 0.5 * (float64(time.Now().UnixNano()%1000) / 1000.0)
			sleepSecs := base + jitter
			if sleepSecs > 30 {
				sleepSecs = 30
			}
			sleepDuration := time.Duration(sleepSecs * float64(time.Second))

			log.Printf("upstream %d for model=%s->%s/%s: retrying in %v (attempt %d/10)", resp.StatusCode, ar.Model, route.Provider, route.Model, sleepDuration.Round(100*time.Millisecond), attempt)
			resp.Body.Close()

			select {
			case <-r.Context().Done():
				log.Printf("client disconnected during retry backoff for model=%s", ar.Model)
				return
			case <-time.After(sleepDuration):
			}
			continue
		}
		break
	}

	defer func() {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
	}()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		log.Printf("upstream %d for model=%s->%s/%s: %s", resp.StatusCode, ar.Model, route.Provider, route.Model, truncate(string(b), 500))
		httpErr(w, resp.StatusCode, fmt.Sprintf("upstream %s/%s: %s", route.Provider, route.Model, truncate(string(b), 300)))
		logit(route.Model, resp.StatusCode, 0, 0, or.ReasoningEffort)
		return
	}

	if ar.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		inTokens, outTokens := streamTranslate(w, resp.Body, ar.Model)
		logit(route.Model, resp.StatusCode, inTokens, outTokens, or.ReasoningEffort)
		return
	}

	var oresp OpenAIResponse
	b, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(b, &oresp); err != nil {
		httpErr(w, 502, "parse upstream: "+err.Error())
		logit(route.Model, 502, 0, 0, or.ReasoningEffort)
		return
	}
	out := translateResponse(&oresp, ar.Model)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)

	tokensIn, tokensOut := 0, 0
	if oresp.Usage != nil {
		tokensIn = oresp.Usage.PromptTokens
		tokensOut = oresp.Usage.CompletionTokens
	}
	logit(route.Model, resp.StatusCode, tokensIn, tokensOut, or.ReasoningEffort)
}

func (s *server) handleModels(w http.ResponseWriter, r *http.Request) {
	// Advertise only the specific premium models matching user's Claude UI configuration,
	// plus any explicit config aliases.
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}

	for _, name := range []string{"claude-step", "claude-kim", "claude-gemini-pro", "claude-gemini-flash", "claude-pickle", "claude-ultra"} {
		add("anthropic/" + name)
	}

	if s.cfg != nil {
		for k := range s.cfg.Aliases {
			add("anthropic/" + normalizeModelID(k))
		}
	}
	sort.Strings(ids)

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

// normalizeModelID strips the "anthropic/" prefix and normalizes separators so
// "anthropic/claude_K_2" and "claude-k-2" resolve to the same alias key.
func normalizeModelID(model string) string {
	clean := strings.TrimPrefix(model, "anthropic/")
	return strings.ToLower(strings.ReplaceAll(clean, "_", "-"))
}

// modelDef is one catalog entry: a canonical ID, accepted aliases, and the
// route they resolve to.
type modelDef struct {
	Canonical string
	Aliases   []string
	Route     Route
}

// modelCatalog is the built-in routing table. Keys must already be normalized
// (lowercase, underscores as dashes). Config aliases overlay these at runtime.
func modelCatalog() []modelDef {
	return []modelDef{
		{"claude-tencent-hy3-preview", nil, Route{Provider: "openrouter", Model: "tencent/hy3-preview"}},
		{"claude-pickle", []string{"claude-big-pickle", "opencode/big-pickle", "claude-pick"}, Route{Provider: "opencode", Model: "big-pickle", ReasoningEffort: "high"}},
		{"claude-ultra", []string{"claude-nemotron-3-ultra-free", "opencode/nemotron-3-ultra-free", "claude-nemotron-3-ultra", "claude-ultra-free"}, Route{Provider: "opencode", Model: "nemotron-3-ultra-free", ReasoningEffort: "high"}},
		{"claude-step", []string{"claude-step-3.7-flash", "stepfun-ai/step-3.7-flash", "stepfun-ai-step-3.7-flash", "stepfun-ai-step-3-7-flash", "stepfun/step-3.7-flash", "stepfun-step-3.7-flash"}, Route{Provider: "nvidia", Model: "deepseek-ai/deepseek-v4-flash"}},
		{"claude-kimi", []string{"claude-kimi-k2", "claude-kim-2", "claude-k-2", "claude-kim"}, Route{Provider: "nvidia", Model: "moonshotai/kimi-k2.6", ReasoningEffort: "high"}},
		{"claude-nemotron-ultra", nil, Route{Provider: "nvidia", Model: "nvidia/nemotron-3-ultra-550b-a55b"}},
		{"claude-glm", []string{"claude-opus", "claude-gl"}, Route{Provider: "nvidia", Model: "z-ai/glm-5.1", ReasoningEffort: "high"}},
		{"claude-minimax", []string{"minimax-m3", "claude-m3", "minimaxai/minimax-m3", "claude-mini"}, Route{Provider: "nvidia", Model: "minimaxai/minimax-m3", ReasoningEffort: "high"}},
		{"claude-deepseek-v4", []string{"deepseek-v4-pro", "claude-v4", "deepseek-ai/deepseek-v4-pro", "claude-deep"}, Route{Provider: "nvidia", Model: "deepseek-ai/deepseek-v4-pro", ReasoningEffort: "high"}},
		{"claude-gemini-pro", []string{"gemini-pro", "gemini-2.5-pro"}, Route{Provider: "gemini", Model: "gemini-2.5-pro"}},
		{"claude-gemini-flash", []string{"gemini-flash", "gemini-2.5-flash"}, Route{Provider: "gemini", Model: "gemini-2.5-flash"}},
	}
}

// effectiveAliases merges the built-in catalog with config aliases. Config
// entries win, so users can override a built-in route without recompiling.
func (s *server) effectiveAliases() map[string]Route {
	m := map[string]Route{}
	for _, d := range modelCatalog() {
		m[d.Canonical] = d.Route
		for _, a := range d.Aliases {
			m[a] = d.Route
		}
	}
	if s.cfg != nil {
		for k, r := range s.cfg.Aliases {
			m[normalizeModelID(k)] = r
		}
	}
	return m
}

func (s *server) routeFor(model string) (Route, error) {
	normalizedModel := normalizeModelID(model)

	if r, ok := s.effectiveAliases()[normalizedModel]; ok {
		return r, nil
	}

	if parts := strings.SplitN(model, "/", 3); len(parts) == 3 {
		if _, ok := s.cfg.Providers[parts[1]]; ok {
			return Route{Provider: parts[1], Model: parts[2]}, nil
		}
	}

	for _, fam := range []string{"opus", "sonnet", "haiku"} {
		if strings.Contains(normalizedModel, fam) {
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
