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
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
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

	s := &server{
		cfgPath: path,
		http:    newUpstreamHTTPClient(),
		limiter: newProviderRateLimiter(cfg),
	}
	s.cfg.Store(cfg)
	if fi, statErr := os.Stat(path); statErr == nil {
		s.cfgModNano.Store(fi.ModTime().UnixNano())
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", s.handleMessages)
	mux.HandleFunc("/v1/responses", s.handleResponses)
	mux.HandleFunc("/v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("acc-proxy ok"))
	})

	mux.HandleFunc("/app", s.handleApp)
	mux.HandleFunc("/dashboard", s.handleDashboard)
	mux.HandleFunc("/dashboard/api/logs", s.handleDashboardLogs)
	mux.HandleFunc("/dashboard/api/clear", s.handleDashboardClear)
	mux.HandleFunc("/dashboard/api/restart", s.handleDashboardRestart)
	mux.HandleFunc("/dashboard/api/info", s.handleDashboardInfo)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/app", http.StatusFound)
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
			log.Printf("acc Web UI: launching Assistant App in Safari...")
			exec.Command("open", fmt.Sprintf("http://localhost:%d/app", cfg.Port)).Start()
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

func newUpstreamHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// A dead model can stall before sending HTTP headers, which happens before
	// the streaming first-token guard gets a chance to run. Bound that phase to
	// the same window so the normal route fallback can take over.
	transport.ResponseHeaderTimeout = firstTokenTimeout
	return &http.Client{Timeout: 5 * time.Minute, Transport: transport}
}

type server struct {
	// cfg is hot-swappable: reloadIfChanged replaces the whole pointer when
	// config.json changes on disk, so model edits take effect without a restart.
	cfg        atomic.Pointer[Config]
	cfgPath    string
	cfgModNano atomic.Int64
	http       *http.Client
	limiter    *providerRateLimiter
}

// reloadIfChanged re-reads the config file when its modtime has advanced, so
// edits to config.json (e.g. swapping a model) take effect on the next request
// without restarting the proxy. A bad config is logged and ignored — the last
// good config stays live.
func (s *server) reloadIfChanged() {
	if s.cfgPath == "" {
		return
	}
	fi, err := os.Stat(s.cfgPath)
	if err != nil {
		return
	}
	mod := fi.ModTime().UnixNano()
	if mod <= s.cfgModNano.Load() {
		return
	}
	// Stamp the modtime first so a broken file isn't re-parsed every request.
	s.cfgModNano.Store(mod)
	cfg, err := loadConfig(s.cfgPath)
	if err != nil {
		log.Printf("config reload skipped (parse error, keeping old): %v", err)
		return
	}
	if err := validateConfig(cfg); err != nil {
		log.Printf("config reload skipped (invalid, keeping old): %v", err)
		return
	}
	s.cfg.Store(cfg)
	log.Printf("config reloaded from %s", s.cfgPath)
}

// maxRequestBytes caps the request body the proxy will buffer, so a runaway
// or malicious client can't drive the process out of memory. Generous enough
// for base64 image blocks.
const maxRequestBytes = 32 << 20 // 32 MiB

func (s *server) handleMessages(w http.ResponseWriter, r *http.Request) {
	s.reloadIfChanged()
	cfg := s.cfg.Load()
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
	logit := func(routeModel string, status, in, out, reasoning int, effort string) {
		AddTUILog(LogEntry{
			Timestamp:    time.Now(),
			Model:        ar.Model,
			Route:        routeModel,
			Status:       status,
			TokensIn:     in,
			TokensOut:    out,
			ReasoningOut: reasoning,
			Budget:       budget,
			Effort:       effort,
			CostUSD:      costFor(routeModel, in, out, cfg),
		})
	}

	route, err := s.routeFor(ar.Model)
	if err != nil {
		httpErr(w, 400, err.Error())
		logit("error", 400, 0, 0, 0, "")
		return
	}

	routes := append([]Route{route}, route.Fallbacks...)

	var (
		or           *OpenAIRequest
		resp         *http.Response
		activeRoute  Route
		streamReader io.Reader
	)

	for ri, currentRoute := range routes {
		activeRoute = currentRoute
		prov, ok := cfg.Providers[currentRoute.Provider]
		if !ok {
			if ri == len(routes)-1 {
				httpErr(w, 500, "unknown provider: "+currentRoute.Provider)
				logit(currentRoute.Model, 500, 0, 0, 0, "")
				return
			}
			log.Printf("unknown provider %q for route %d, trying fallback", currentRoute.Provider, ri)
			continue
		}

		or, err = translateRequest(&ar, currentRoute, cfg)
		if err != nil {
			if ri == len(routes)-1 {
				httpErr(w, 400, "translate: "+err.Error())
				logit(currentRoute.Model, 400, 0, 0, 0, "")
				return
			}
			log.Printf("translate failed for %s/%s, trying fallback: %v", currentRoute.Provider, currentRoute.Model, err)
			continue
		}

		body, _ := json.Marshal(or)
		if len(currentRoute.ExtraBody) > 0 {
			var merged map[string]any
			if err := json.Unmarshal(body, &merged); err == nil {
				for k, v := range currentRoute.ExtraBody {
					merged[k] = v
				}
				if newBody, err := json.Marshal(merged); err == nil {
					body = newBody
				}
			}
		}
		// When a fallback route exists, don't hammer a 503ing model for minutes —
		// bail after a couple quick tries so latency-sensitive callers (e.g. the
		// Agent safety classifier) fall through to a healthy route instead of
		// timing out. Only the last route gets the full retry budget.
		maxAttempts := 10
		if ri < len(routes)-1 {
			maxAttempts = 2
		}

		for attempt := 1; attempt <= maxAttempts; attempt++ {
			var err error
			upstream, err := http.NewRequestWithContext(r.Context(), "POST", prov.BaseURL+"/chat/completions", bytes.NewReader(body))
			if err != nil {
				httpErr(w, 500, err.Error())
				logit(currentRoute.Model, 500, 0, 0, 0, or.ReasoningEffort)
				return
			}
			upstream.Header.Set("Content-Type", "application/json")
			upstream.Header.Set("Authorization", "Bearer "+prov.APIKey)

			if err := s.limiter.Wait(r.Context(), currentRoute.Provider); err != nil {
				httpErr(w, 504, fmt.Sprintf("rate limiter interrupted for %s/%s: %v", currentRoute.Provider, currentRoute.Model, err))
				logit(currentRoute.Model, 504, 0, 0, 0, or.ReasoningEffort)
				return
			}

			resp, err = s.http.Do(upstream)
			if err != nil {
				httpErr(w, 502, "upstream: "+err.Error())
				logit(currentRoute.Model, 502, 0, 0, 0, or.ReasoningEffort)
				return
			}

			if resp.StatusCode == 503 && attempt < maxAttempts {
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

				log.Printf("upstream %d for model=%s->%s/%s: retrying in %v (attempt %d/10)", resp.StatusCode, ar.Model, currentRoute.Provider, currentRoute.Model, sleepDuration.Round(100*time.Millisecond), attempt)
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

		// On provider failures, try the next configured fallback. This includes a
		// provider's own generic 400, which is different from a bad client request.
		shouldFallback := resp.StatusCode == 429 || resp.StatusCode >= 500
		var degradedBody []byte
		if !shouldFallback && resp.StatusCode == 400 {
			degradedBody, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(degradedBody))
			if recoverableProvider400(degradedBody) {
				shouldFallback = true
			}
		}
		if shouldFallback && ri < len(routes)-1 {
			status := resp.StatusCode
			b := degradedBody
			if b == nil {
				b, _ = io.ReadAll(resp.Body)
			}
			resp.Body.Close()
			resp = nil
			log.Printf("upstream %d on %s/%s, falling back: %s", status, currentRoute.Provider, currentRoute.Model, truncate(string(b), 200))
			logit(currentRoute.Model, status, 0, 0, 0, or.ReasoningEffort)
			continue
		}

		// Time-to-first-token guard (streaming only): a route that returns 200
		// but emits no token within firstTokenTimeout is treated as stalled.
		// Fall back if a route remains, otherwise fail — never hang.
		if ar.Stream && resp.StatusCode < 400 {
			reader, timedOut := awaitFirstByte(resp.Body, firstTokenTimeout)
			if timedOut {
				resp.Body.Close()
				resp = nil
				log.Printf("no token from %s/%s within %s", currentRoute.Provider, currentRoute.Model, firstTokenTimeout)
				logit(currentRoute.Model, 504, 0, 0, 0, or.ReasoningEffort)
				if ri < len(routes)-1 {
					continue
				}
				httpErr(w, 504, fmt.Sprintf("⌛ %s and its fallback gave no response in time. Try again or switch models.", ar.Model))
				return
			}
			streamReader = reader
		}

		break // got a definitive response (success or final route exhausted)
	}

	defer func() {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
	}()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		log.Printf("upstream %d for model=%s->%s/%s: %s", resp.StatusCode, ar.Model, activeRoute.Provider, activeRoute.Model, truncate(string(b), 500))
		// Plain-English message for the two failure modes a free-tier user actually
		// hits, instead of leaking the raw upstream error blob.
		msg := fmt.Sprintf("upstream %s/%s: %s", activeRoute.Provider, activeRoute.Model, truncate(string(b), 300))
		switch {
		case resp.StatusCode == 429:
			msg = fmt.Sprintf("🪫 You're out of free usage on %s right now (rate-limited / quota hit). Wait a bit, or switch to another model.", activeRoute.Model)
		case resp.StatusCode >= 500:
			msg = fmt.Sprintf("⚠️ %s (provider %s) is down right now — server error %d. Try again in a moment or switch models.", activeRoute.Model, activeRoute.Provider, resp.StatusCode)
		}
		httpErr(w, resp.StatusCode, msg)
		logit(activeRoute.Model, resp.StatusCode, 0, 0, 0, or.ReasoningEffort)
		return
	}

	if ar.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		if streamReader == nil {
			streamReader = resp.Body
		}
		inTokens, outTokens, reasoningOut := streamTranslate(w, streamReader, ar.Model)
		logit(activeRoute.Model, resp.StatusCode, inTokens, outTokens, reasoningOut, or.ReasoningEffort)
		return
	}

	var oresp OpenAIResponse
	b, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(b, &oresp); err != nil {
		httpErr(w, 502, "parse upstream: "+err.Error())
		logit(activeRoute.Model, 502, 0, 0, 0, or.ReasoningEffort)
		return
	}
	out := translateResponse(&oresp, ar.Model)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)

	tokensIn, tokensOut, reasoningOut := 0, 0, 0
	if oresp.Usage != nil {
		tokensIn = oresp.Usage.PromptTokens
		tokensOut = oresp.Usage.CompletionTokens
		reasoningOut = oresp.Usage.reasoningTokens()
	}
	logit(activeRoute.Model, resp.StatusCode, tokensIn, tokensOut, reasoningOut, or.ReasoningEffort)
}

// handleChatCompletions implements an OpenAI-compatible /v1/chat/completions
// endpoint. It routes the model name through the same config as /v1/messages,
// then forwards the (already OpenAI-format) body directly to the upstream,
// avoiding a double translation loop.
func (s *server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	s.reloadIfChanged()
	cfg := s.cfg.Load()
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		httpErr(w, 400, "read body: "+err.Error())
		return
	}

	var meta struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		httpErr(w, 400, "parse request: "+err.Error())
		return
	}

	route, err := s.routeFor(meta.Model)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}

	logit := func(routeModel string, status, in, out, reasoning int, effort string) {
		AddTUILog(LogEntry{
			Timestamp: time.Now(), Model: meta.Model, Route: routeModel,
			Status: status, TokensIn: in, TokensOut: out, Budget: 0, Effort: effort,
		})
	}

	routes := append([]Route{route}, route.Fallbacks...)

	var (
		resp         *http.Response
		activeRoute  Route
		streamReader io.Reader
	)

	for ri, currentRoute := range routes {
		activeRoute = currentRoute
		prov, ok := cfg.Providers[currentRoute.Provider]
		if !ok {
			if ri == len(routes)-1 {
				httpErr(w, 500, "unknown provider: "+currentRoute.Provider)
				logit(currentRoute.Model, 500, 0, 0, 0, "")
				return
			}
			log.Printf("unknown provider %q for route %d, trying fallback", currentRoute.Provider, ri)
			continue
		}

		body, err := chatJSONWithACCPersona(raw, currentRoute)
		if err != nil {
			httpErr(w, 400, "prepare request: "+err.Error())
			logit(currentRoute.Model, 400, 0, 0, 0, "")
			return
		}
		// Rewrite model name to the actual upstream model. The client sends
		// "anthropic/claude-haiku" but the upstream expects "stepfun-ai/step-3.7-flash".
		var merged map[string]any
		if err := json.Unmarshal(body, &merged); err == nil {
			merged["model"] = currentRoute.Model
			for k, v := range currentRoute.ExtraBody {
				merged[k] = v
			}
			if newBody, err := json.Marshal(merged); err == nil {
				body = newBody
			}
		}

		maxAttempts := 10
		if ri < len(routes)-1 {
			maxAttempts = 2
		}

		for attempt := 1; attempt <= maxAttempts; attempt++ {
			upstream, err := http.NewRequestWithContext(r.Context(), "POST", prov.BaseURL+"/chat/completions", bytes.NewReader(body))
			if err != nil {
				httpErr(w, 500, err.Error())
				logit(currentRoute.Model, 500, 0, 0, 0, "")
				return
			}
			upstream.Header.Set("Content-Type", "application/json")
			upstream.Header.Set("Authorization", "Bearer "+prov.APIKey)

			if err := s.limiter.Wait(r.Context(), currentRoute.Provider); err != nil {
				httpErr(w, 504, fmt.Sprintf("rate limiter interrupted for %s/%s: %v", currentRoute.Provider, currentRoute.Model, err))
				logit(currentRoute.Model, 504, 0, 0, 0, "")
				return
			}

			resp, err = s.http.Do(upstream)
			if err != nil {
				httpErr(w, 502, "upstream: "+err.Error())
				logit(currentRoute.Model, 502, 0, 0, 0, "")
				return
			}

			if resp.StatusCode == 503 && attempt < maxAttempts {
				baseInt := 1 << attempt
				base := float64(baseInt)
				jitter := base * 0.5 * (float64(time.Now().UnixNano()%1000) / 1000.0)
				sleepSecs := base + jitter
				if sleepSecs > 30 {
					sleepSecs = 30
				}
				sleepDuration := time.Duration(sleepSecs * float64(time.Second))
				log.Printf("openai: upstream %d for model=%s->%s/%s: retrying in %v (attempt %d/%d)", resp.StatusCode, meta.Model, currentRoute.Provider, currentRoute.Model, sleepDuration.Round(100*time.Millisecond), attempt, maxAttempts)
				resp.Body.Close()
				select {
				case <-r.Context().Done():
					log.Printf("openai: client disconnected during retry backoff for model=%s", meta.Model)
					return
				case <-time.After(sleepDuration):
				}
				continue
			}
			break
		}

		shouldFallback := resp.StatusCode == 429 || resp.StatusCode >= 500
		var degradedBody []byte
		if !shouldFallback && resp.StatusCode == 400 {
			degradedBody, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(degradedBody))
			if recoverableProvider400(degradedBody) {
				shouldFallback = true
			}
		}
		if shouldFallback && ri < len(routes)-1 {
			status := resp.StatusCode
			b := degradedBody
			if b == nil {
				b, _ = io.ReadAll(resp.Body)
			}
			resp.Body.Close()
			resp = nil
			log.Printf("openai: upstream %d on %s/%s, falling back: %s", status, currentRoute.Provider, currentRoute.Model, truncate(string(b), 200))
			logit(currentRoute.Model, status, 0, 0, 0, "")
			continue
		}

		if meta.Stream && resp.StatusCode < 400 {
			sr, timedOut := awaitFirstByte(resp.Body, firstTokenTimeout)
			if timedOut {
				resp.Body.Close()
				resp = nil
				log.Printf("openai: no token from %s/%s within %s", currentRoute.Provider, currentRoute.Model, firstTokenTimeout)
				logit(currentRoute.Model, 504, 0, 0, 0, "")
				if ri < len(routes)-1 {
					continue
				}
				httpErr(w, 504, fmt.Sprintf("%s and its fallback gave no response in time. Try again or switch models.", meta.Model))
				return
			}
			streamReader = sr
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
		log.Printf("openai: upstream %d for model=%s->%s/%s: %s", resp.StatusCode, meta.Model, activeRoute.Provider, activeRoute.Model, truncate(string(b), 500))
		msg := fmt.Sprintf("upstream %s/%s: %s", activeRoute.Provider, activeRoute.Model, truncate(string(b), 300))
		switch {
		case resp.StatusCode == 429:
			msg = fmt.Sprintf("Rate-limited on %s. Wait a bit or switch models.", activeRoute.Model)
		case resp.StatusCode >= 500:
			msg = fmt.Sprintf("%s (provider %s) is down — server error %d. Try again or switch models.", activeRoute.Model, activeRoute.Provider, resp.StatusCode)
		}
		httpErr(w, resp.StatusCode, msg)
		logit(activeRoute.Model, resp.StatusCode, 0, 0, 0, "")
		return
	}

	if meta.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		if streamReader == nil {
			streamReader = resp.Body
		}
		io.Copy(w, streamReader)
		logit(activeRoute.Model, resp.StatusCode, 0, 0, 0, "")
		return
	}

	b, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(b)

	tokensIn, tokensOut := 0, 0
	var usage struct {
		Prompt     int `json:"prompt_tokens"`
		Completion int `json:"completion_tokens"`
	}
	if json.Unmarshal(b, &usage) == nil {
		tokensIn = usage.Prompt
		tokensOut = usage.Completion
	}
	logit(activeRoute.Model, resp.StatusCode, tokensIn, tokensOut, 0, "")
}

func (s *server) handleModels(w http.ResponseWriter, r *http.Request) {
	// Check client type: Anthropic SDK sends anthropic-version header, OpenAI clients
	// don't. Serve the right format so both can discover models.
	isAnthropic := r.Header.Get("anthropic-version") != ""
	isCodex := strings.Contains(strings.ToLower(r.UserAgent()), "codex")

	allow := []string{"claude-opus", "claude-sonnet", "claude-haiku", "claude-fable", "claude-mythos"}

	w.Header().Set("Content-Type", "application/json")
	if isCodex {
		json.NewEncoder(w).Encode(map[string]any{"models": codexModelCatalogEntries(s.cfg.Load())})
	} else if isAnthropic {
		var data []map[string]any
		for _, name := range allow {
			id := "anthropic/" + name
			data = append(data, map[string]any{
				"type": "model", "id": id, "display_name": id,
				"created_at": "2025-01-01T00:00:00Z",
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data, "has_more": false})
	} else {
		var data []map[string]any
		for _, name := range allow {
			id := "anthropic/" + name
			data = append(data, map[string]any{
				"id": id, "object": "model",
				"created": 1735689600, "owned_by": "acc-proxy",
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	}
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
		{"claude-kimi", []string{"claude-kimi-k2", "claude-kim-2", "claude-k-2", "claude-kim"}, Route{Provider: "cloudflare", Model: "qwen/qwen-1m", ReasoningEffort: "high"}},
		{"claude-nemotron-ultra", nil, Route{Provider: "nvidia", Model: "nvidia/nemotron-3-ultra-550b-a55b"}},
		{"claude-glm", []string{"claude-opus", "claude-gl"}, Route{Provider: "nvidia", Model: "z-ai/glm-5.1", ReasoningEffort: "high"}},
		{"claude-minimax", []string{"minimax-m3", "claude-m3", "minimaxai/minimax-m3", "claude-mini"}, Route{Provider: "nvidia", Model: "minimaxai/minimax-m3", ReasoningEffort: "high"}},
		{"claude-deepseek-v4", []string{"deepseek-v4-pro", "claude-v4", "deepseek-ai/deepseek-v4-pro", "claude-deep"}, Route{Provider: "nvidia", Model: "deepseek-ai/deepseek-v4-pro", ReasoningEffort: "high"}},
		{"claude-gemini-pro", []string{"gemini-pro", "gemini-3.1-pro-preview", "gemini-3-pro"}, Route{Provider: "gemini", Model: "models/gemini-3.1-pro-preview"}},
		{"claude-gemini-flash", []string{"gemini-flash", "gemini-3.5-flash", "gemini-3-flash"}, Route{Provider: "gemini", Model: "models/gemini-3.5-flash"}},
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
	if cfg := s.cfg.Load(); cfg != nil {
		for id, capability := range cfg.Models {
			if !capability.Enabled {
				continue
			}
			if route, err := resolveCapabilityRoute(cfg, id, capability); err == nil {
				m[normalizeModelID(id)] = route
			}
		}
		for k, r := range cfg.Aliases {
			m[normalizeModelID(k)] = r
		}
	}
	return m
}

func (s *server) routeFor(model string) (Route, error) {
	cfg := s.cfg.Load()
	normalizedModel := normalizeModelID(model)
	aliases := s.effectiveAliases()

	if r, ok := aliases[normalizedModel]; ok {
		// Enforce NVIDIA-only for fable/mythos aliases to avoid Gemini fallbacks
		if strings.Contains(normalizedModel, "fable") || strings.Contains(normalizedModel, "mythos") {
			if r.Provider != "nvidia" {
				log.Printf("forcing NVIDIA provider for alias %s (was %s/%s)", normalizedModel, r.Provider, r.Model)
				r.Provider = "nvidia"
				// If the model previously pointed at Gemini, prefer minimax-m3
				if strings.Contains(strings.ToLower(r.Model), "gemini") {
					r.Model = "minimaxai/minimax-m3"
				}
			}
		}
		return r, nil
	}

	if parts := strings.SplitN(model, "/", 3); len(parts) == 3 {
		if _, ok := cfg.Providers[parts[1]]; ok {
			return Route{Provider: parts[1], Model: parts[2]}, nil
		}
	}

	for _, fam := range []string{"opus", "sonnet", "haiku"} {
		if strings.Contains(normalizedModel, fam) {
			if r, ok := cfg.Routes[fam]; ok {
				return r, nil
			}
		}
	}

	return Route{}, fmt.Errorf("unrecognized model ID %q — did you mean anthropic/claude-kimi-k2 or a direct provider path like anthropic/nvidia/moonshotai/kimi-k2.6?", model)
}

// mergeRouteExtraBody flat-merges a route's provider-specific request settings
// into an already-encoded OpenAI request. NVIDIA expects these fields at the
// request root, while Gemini can intentionally use an extra_body wrapper.
func mergeRouteExtraBody(body []byte, extra map[string]any) []byte {
	if len(extra) == 0 {
		return body
	}
	var merged map[string]any
	if err := json.Unmarshal(body, &merged); err != nil {
		return body
	}
	for k, v := range extra {
		merged[k] = v
	}
	if newBody, err := json.Marshal(merged); err == nil {
		return newBody
	}
	return body
}

// recoverableProvider400 distinguishes a malformed client request from a
// provider admitting that its own backend rejected the request. The latter can
// safely try the next configured route.
func recoverableProvider400(body []byte) bool {
	lower := bytes.ToLower(body)
	return bytes.Contains(lower, []byte("degraded")) ||
		bytes.Contains(lower, []byte("cannot be invoked")) ||
		bytes.Contains(lower, []byte("error from provider")) && bytes.Contains(lower, []byte("upstream request failed"))
}

// ---------- Config ----------

func loadPrependFile(baseDir, path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[2:])
		}
	} else if !filepath.IsAbs(path) {
		// Try resolving relative to config file directory first, then Cwd
		absPath := filepath.Join(baseDir, path)
		if _, err := os.Stat(absPath); err == nil {
			path = absPath
		}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read system_prepend file %q: %w", path, err)
	}
	return string(content), nil
}

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

	baseDir := filepath.Dir(path)
	if strings.HasPrefix(c.SystemPrepend, "@") {
		resolved, err := loadPrependFile(baseDir, c.SystemPrepend[1:])
		if err != nil {
			return nil, err
		}
		c.SystemPrepend = resolved
	}

	for k, r := range c.Routes {
		// Route-specific persona files were an ACC-owned legacy mechanism. They
		// are intentionally retired so provider imitation prompts can never
		// override the central Kabir's Second Brain identity.
		r.SystemPrepend = ""
		for i := range r.Fallbacks {
			r.Fallbacks[i].SystemPrepend = ""
		}
		c.Routes[k] = r
	}

	for k, r := range c.Aliases {
		r.SystemPrepend = ""
		for i := range r.Fallbacks {
			r.Fallbacks[i].SystemPrepend = ""
		}
		c.Aliases[k] = r
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
	for id, capability := range cfg.Models {
		if !capability.Enabled {
			continue
		}
		if _, err := resolveCapabilityRoute(cfg, id, capability); err != nil {
			return err
		}
		for _, fallbackID := range configuredFallbackModels(capability) {
			fallback, ok := cfg.Models[fallbackID]
			if !ok || !fallback.Enabled {
				return fmt.Errorf("model %q: fallback model %q is unavailable", id, fallbackID)
			}
		}
		if capability.ImageModel != "" {
			imageModel, ok := cfg.Models[capability.ImageModel]
			if !ok || !imageModel.Enabled {
				return fmt.Errorf("model %q: image model %q is unavailable", id, capability.ImageModel)
			}
			if !imageModel.ImageInputSupport {
				return fmt.Errorf("model %q: image model %q does not support image input", id, capability.ImageModel)
			}
		}
		for effort := range capability.Reasoning {
			switch effort {
			case "minimal", "low", "medium", "high", "xhigh", "max":
			default:
				return fmt.Errorf("model %q: unsupported catalog reasoning effort %q", id, effort)
			}
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
