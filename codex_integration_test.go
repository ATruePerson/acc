package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

func codexTestConfig() *Config {
	return &Config{
		Providers: map[string]Provider{
			"nvidia":     {BaseURL: "https://nvidia.test", APIKey: "nvidia-key"},
			"opencode":   {BaseURL: "https://opencode.test", APIKey: "opencode-key"},
			"openrouter": {BaseURL: "https://openrouter.test", APIKey: "openrouter-key"},
		},
		Routes: map[string]Route{
			"sol":   {Provider: "nvidia", Model: "z-ai/glm-5.2", Toolcalling: boolPtr(true)},
			"terra": {Provider: "opencode", Model: "big-pickle", MaxContext: 131072, MaxTokens: 48000, Toolcalling: boolPtr(true)},
			"luna":  {Provider: "nvidia", Model: "stepfun-ai/step-3.7-flash", Toolcalling: boolPtr(true)},
		},
		AliasRoutes: map[string]Route{
			"opus":   {Provider: "nvidia", Model: "z-ai/glm-5.2", Toolcalling: boolPtr(true)},
			"sonnet": {Provider: "opencode", Model: "big-pickle", Toolcalling: boolPtr(true)},
			"haiku":  {Provider: "nvidia", Model: "stepfun-ai/step-3.7-flash", Toolcalling: boolPtr(true)},
		},
		Models: map[string]ModelCapability{
			codexOpusID: {
				DisplayName: "5.6 Sol", CatalogPriority: 1, Route: "sol", Enabled: true,
				Reasoning: map[string]ReasoningTarget{
					"minimal": {}, "low": {Effort: "low"}, "medium": {Effort: "medium"},
					"high": {Effort: "high"}, "xhigh": {Effort: "xhigh"},
				},
				ToolCallSupport: true, StreamingSupport: true, ImageInputSupport: true,
				MaxContext: 131072, MaxOutput: 131072,
			},
			codexSonnetID: {
				DisplayName: "5.6 Terra", CatalogPriority: 2, Route: "terra", Enabled: true,
				Reasoning: map[string]ReasoningTarget{
					"minimal": {}, "low": {Effort: "low"}, "medium": {Effort: "medium"},
					"high": {Effort: "high"}, "xhigh": {Effort: "xhigh"}, "max": {Effort: "max"},
				},
				ToolCallSupport: true, StreamingSupport: true, FileInputSupport: true,
				MaxContext: 262144, MaxOutput: 65536,
			},
			codexHaikuID: {
				DisplayName: "5.6 Luna", CatalogPriority: 3, Route: "luna", Enabled: true,
				Reasoning:        map[string]ReasoningTarget{"minimal": {}, "low": {Effort: "low"}},
				StreamingSupport: true, ImageInputSupport: true,
				MaxContext: 131072, MaxOutput: 26000,
			},
			"disabled-model": {
				DisplayName: "Disabled", Provider: "nvidia", Model: "disabled", Enabled: false,
			},
		},
	}
}

func TestACCPersonaSeparatesCodexAndClaudeRuntimeAdapters(t *testing.T) {
	codexPrompt := accPersonaForRuntime("nvidia", "z-ai/glm-5.2", personaRuntimeCodex)
	claudePrompt := accPersonaForRuntime("nvidia", "z-ai/glm-5.2", personaRuntimeClaudeCode)
	for _, prompt := range []string{codexPrompt, claudePrompt} {
		for _, want := range []string{
			"Core behavior",
			"Kabir's personal instructions",
			"Your identity is Kabir's Second Brain.",
			`Normal identity answer: “I’m Kabir’s Second Brain.”`,
			`This task is currently being powered by nvidia/z-ai/glm-5.2.`,
			"Only disclose the backend when Kabir explicitly asks",
			"Tool results are authoritative",
			"Inspect files before modifying them",
			"Never claim a tool succeeded",
			"Do not repeat a destructive call",
			"Follow repository instructions such as AGENTS.md",
		} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("persona missing %q:\n%s", want, prompt)
			}
		}
	}
	if !strings.Contains(codexPrompt, "Codex runtime/tool adapter") || strings.Contains(codexPrompt, "Claude Code runtime/tool adapter") {
		t.Fatalf("Codex prompt leaked the Claude adapter:\n%s", codexPrompt)
	}
	if !strings.Contains(claudePrompt, "Claude Code runtime/tool adapter") || strings.Contains(claudePrompt, "Codex runtime/tool adapter") {
		t.Fatalf("Claude prompt leaked the Codex adapter:\n%s", claudePrompt)
	}
	for _, prompt := range []string{codexPrompt, claudePrompt} {
		for _, forbidden := range []string{"You are Claude", "You are Codex", "You are ChatGPT"} {
			if strings.Contains(prompt, forbidden) {
				t.Fatalf("persona contains forbidden identity %q", forbidden)
			}
		}
	}
	namespaced := accPersonaForRuntime("nvidia", "nvidia/nemotron-3-super-120b-a12b", personaRuntimeCodex)
	if strings.Contains(namespaced, "nvidia/nvidia/nemotron") || !strings.Contains(namespaced, "nvidia/nemotron-3-super-120b-a12b") {
		t.Fatalf("persona has an inaccurate namespaced backend label:\n%s", namespaced)
	}
}

func TestCodexCatalogComesFromEnabledCapabilityRegistry(t *testing.T) {
	var catalog struct {
		Models []struct {
			Slug             string `json:"slug"`
			DisplayName      string `json:"display_name"`
			BaseInstructions string `json:"base_instructions"`
			EffectiveContext int    `json:"effective_context_window_percent"`
			Levels           []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
			Context int `json:"context_window"`
		} `json:"models"`
	}
	if err := json.Unmarshal(codexModelCatalogJSON(codexTestConfig()), &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 3 {
		t.Fatalf("catalog has %d models, want 3 enabled models", len(catalog.Models))
	}
	if catalog.Models[0].Slug != "nvidia/z-ai/glm-5.2" || catalog.Models[1].Slug != "opencode/big-pickle" || catalog.Models[2].Slug != "nvidia/stepfun-ai/step-3.7-flash" {
		t.Fatalf("catalog slugs are not deterministic provider-prefixed IDs: %+v", catalog.Models)
	}
	if !strings.Contains(catalog.Models[1].BaseInstructions, "Kabir's Second Brain") || strings.Contains(catalog.Models[1].BaseInstructions, "You are Codex") {
		t.Fatalf("catalog has the wrong identity: %q", catalog.Models[1].BaseInstructions)
	}
	if !strings.Contains(catalog.Models[1].BaseInstructions, "Codex runtime/tool adapter") || strings.Contains(catalog.Models[1].BaseInstructions, "Claude Code runtime/tool adapter") {
		t.Fatalf("catalog has the wrong runtime adapter: %q", catalog.Models[1].BaseInstructions)
	}
	if catalog.Models[1].Context != 262144 {
		t.Fatalf("context window = %d, want 262144", catalog.Models[1].Context)
	}
	if catalog.Models[1].EffectiveContext != 75 {
		t.Fatalf("effective context = %d%%, want 75%% with output reserved", catalog.Models[1].EffectiveContext)
	}
	var glm, pickle []string
	for _, model := range catalog.Models {
		for _, level := range model.Levels {
			if model.Slug == "nvidia/z-ai/glm-5.2" {
				glm = append(glm, level.Effort)
			}
			if model.Slug == "opencode/big-pickle" {
				pickle = append(pickle, level.Effort)
			}
		}
	}
	if strings.Contains(strings.Join(glm, ","), "max") {
		t.Fatalf("GLM exposes unsupported Max: %v", glm)
	}
	if !strings.Contains(strings.Join(pickle, ","), "max") {
		t.Fatalf("big-pickle should expose provider-supported Max: %v", pickle)
	}
}

func TestOversizedTerraRequestSkipsSmallContextRoute(t *testing.T) {
	cfg := codexTestConfig()
	terra := cfg.Models[codexSonnetID]
	terra.MaxContext = 262144
	terra.FallbackModels = []string{"acc-openrouter-hy3"}
	cfg.Models[codexSonnetID] = terra
	terraRoute := cfg.Routes["terra"]
	terraRoute.MaxContext = 131072
	cfg.Routes["terra"] = terraRoute
	cfg.Models["acc-openrouter-hy3"] = ModelCapability{
		DisplayName: "HY3", Provider: "openrouter", Model: "tencent/hy3:free", Enabled: true,
		Reasoning:       map[string]ReasoningTarget{"max": {Effort: "high"}},
		ToolCallSupport: true, StreamingSupport: true, MaxContext: 262144, MaxOutput: 65536,
	}

	s := testServer(cfg)
	calledProvider := ""
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		calledProvider = req.URL.Host
		return chatSuccess("ok"), nil
	}}}

	body, err := json.Marshal(map[string]any{
		"model":     codexSonnetID,
		"reasoning": map[string]any{"effort": "max"},
		"input":     strings.Repeat("x", 150000),
		"tools": []map[string]any{{
			"type": "function", "name": "computer", "parameters": map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.handleResponses(w, httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if calledProvider != "openrouter.test" {
		t.Fatalf("oversized Terra request reached %q, want context-capable openrouter fallback", calledProvider)
	}
	if w.Header().Get("X-ACC-Capability-Reroute") != "true" {
		t.Fatalf("context reroute was not reported: %+v", w.Header())
	}
}

func TestResponsesTokenEstimateUsesDenseInputSafetyMargin(t *testing.T) {
	if got := estimateResponsesInputTokens(make([]byte, 200000)); got != 66667 {
		t.Fatalf("200 KB estimate = %d, want 66667", got)
	}
	if got := estimateResponsesInputTokens(make([]byte, 200001)); got != 66667 {
		t.Fatalf("odd-byte estimate = %d, want 66667", got)
	}
}

func TestTerraContextSelectionUsesRequestedOutputBudget(t *testing.T) {
	req := &ResponsesRequest{Model: codexSonnetID, MaxOutputTokens: 1000}
	routes := []resolvedModel{{
		ID: codexSonnetID,
		Capability: ModelCapability{
			DisplayName: "5.6 Terra", MaxContext: 131072, MaxOutput: 65536,
			StreamingSupport: true,
		},
		Route: Route{Provider: "opencode", Model: "big-pickle", MaxContext: 131072, MaxTokens: 48000},
	}}
	selected, err := selectResponseModelChainForInput(req, routes, 130000)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].Route.Model != "big-pickle" {
		t.Fatalf("selected routes = %+v, want the primary route", selected)
	}
}

func TestTerraLargeContextSelectsMillionTokenFallback(t *testing.T) {
	req := &ResponsesRequest{Model: codexSonnetID, MaxOutputTokens: 8000}
	routes := []resolvedModel{
		{
			ID:         codexSonnetID,
			Capability: ModelCapability{DisplayName: "5.6 Terra", MaxContext: 262144, MaxOutput: 65536, StreamingSupport: true},
			Route:      Route{Provider: "opencode", Model: "big-pickle", MaxContext: 131072, MaxTokens: 48000},
		},
		{
			ID: "acc-openrouter-hy3", Fallback: true,
			Capability: ModelCapability{DisplayName: "HY3", MaxContext: 262144, MaxOutput: 65536, StreamingSupport: true},
			Route:      Route{Provider: "openrouter", Model: "tencent/hy3:free", MaxContext: 262144, MaxTokens: 65536},
		},
		{
			ID: "acc-gemini-3.5-flash", Fallback: true,
			Capability: ModelCapability{DisplayName: "Gemini 3.5 Flash", MaxContext: 1048576, MaxOutput: 65536, StreamingSupport: true},
			Route:      Route{Provider: "gemini", Model: "gemini-3.5-flash", MaxContext: 1048576, MaxTokens: 65536},
		},
	}
	selected, err := selectResponseModelChainForInput(req, routes, 300000)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].Route.Model != "gemini-3.5-flash" {
		t.Fatalf("selected routes = %+v, want Gemini long-context fallback", selected)
	}
}

func TestResponsesRespectSmallerRequestedOutputLimit(t *testing.T) {
	cfg := codexTestConfig()
	s := testServer(cfg)
	gotMaxTokens := 0
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotMaxTokens = int(body["max_tokens"].(float64))
		return chatSuccess("ok"), nil
	}}}

	body := []byte(`{"model":"gpt-5.6-terra","input":"hello","max_output_tokens":1000,"reasoning":{"effort":"max"}}`)
	w := httptest.NewRecorder()
	s.handleResponses(w, httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if gotMaxTokens != 1000 {
		t.Fatalf("upstream max_tokens = %d, want 1000", gotMaxTokens)
	}
}

func TestHiddenBenchmarkModelIsRoutableButNotInCatalog(t *testing.T) {
	cfg := codexTestConfig()
	hidden := false
	cfg.Models["bench-hidden"] = ModelCapability{
		DisplayName: "Hidden benchmark candidate", Provider: "nvidia", Model: "candidate", Enabled: true,
		CatalogVisible: &hidden, StreamingSupport: true, ToolCallSupport: true,
		Reasoning: map[string]ReasoningTarget{"high": {Effort: "high"}},
	}

	for _, model := range codexNamedModels(cfg) {
		if model.ID == "bench-hidden" {
			t.Fatal("hidden benchmark candidate leaked into the Codex catalog")
		}
	}
	routes, err := testServer(cfg).responseModelChain("bench-hidden")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].Route.Model != "candidate" {
		t.Fatalf("hidden benchmark route = %+v", routes)
	}
}

func TestCapabilityChainKeepsToolFallbackAndSeparateImageRoute(t *testing.T) {
	cfg := codexTestConfig()
	hidden := false
	primary := cfg.Models[codexOpusID]
	primary.ImageInputSupport = false
	primary.FallbackModel = ""
	primary.FallbackModels = []string{"tool-fallback"}
	primary.ImageModel = "image-fallback"
	cfg.Models[codexOpusID] = primary
	cfg.Models["tool-fallback"] = ModelCapability{
		DisplayName: "Tool fallback", CatalogVisible: &hidden, Provider: "nvidia", Model: "tool-model", Enabled: true,
		ToolCallSupport: true, StreamingSupport: true, Reasoning: map[string]ReasoningTarget{"max": {Effort: "high"}},
	}
	cfg.Models["image-fallback"] = ModelCapability{
		DisplayName: "Image fallback", CatalogVisible: &hidden, Provider: "nvidia", Model: "image-model", Enabled: true,
		ToolCallSupport: false, StreamingSupport: true, ImageInputSupport: true, Reasoning: map[string]ReasoningTarget{"max": {}},
	}

	chain, err := testServer(cfg).responseModelChain(codexOpusID)
	if err != nil {
		t.Fatal(err)
	}
	toolChain, err := selectResponseModelChain(&ResponsesRequest{
		Model: codexOpusID, Input: json.RawMessage(`"hello"`),
		Tools: []ResponsesTool{{Type: "function", Name: "exec", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}, chain)
	if err != nil {
		t.Fatal(err)
	}
	if len(toolChain) != 2 || toolChain[1].ID != "tool-fallback" {
		t.Fatalf("tool chain dropped or included an incompatible route: %+v", toolChain)
	}
	imageChain, err := selectResponseModelChain(&ResponsesRequest{
		Model: codexOpusID,
		Input: json.RawMessage(`[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}]`),
	}, chain)
	if err != nil {
		t.Fatal(err)
	}
	if len(imageChain) != 1 || imageChain[0].ID != "image-fallback" || !imageChain[0].CapabilityReroute {
		t.Fatalf("image route was not selected separately: %+v", imageChain)
	}
}

func TestResponsesUseExactModelAndEffortOnEveryRequest(t *testing.T) {
	cfg := codexTestConfig()
	s := testServer(cfg)
	var got []map[string]any
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		got = append(got, body)
		return chatSuccess("ok"), nil
	}}}

	requests := []string{
		`{"model":"opus","instructions":"project rules","input":"first","reasoning":{"effort":"xhigh"}}`,
		`{"model":"sonnet","instructions":"project rules","input":"second","reasoning":{"effort":"max"}}`,
		`{"model":"opus","instructions":"project rules","input":"third","reasoning":{"effort":"minimal"}}`,
	}
	for _, body := range requests {
		w := httptest.NewRecorder()
		s.handleResponses(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
		if w.Code != http.StatusOK {
			t.Fatalf("request failed: status=%d body=%s", w.Code, w.Body.String())
		}
	}

	if len(got) != 3 {
		t.Fatalf("upstream requests = %d, want 3", len(got))
	}
	if got[0]["model"] != "z-ai/glm-5.2" || got[0]["reasoning_effort"] != "xhigh" {
		t.Fatalf("first task request = %+v", got[0])
	}
	if got[1]["model"] != "big-pickle" || got[1]["reasoning_effort"] != "max" {
		t.Fatalf("switched task request = %+v", got[1])
	}
	if got[2]["model"] != "z-ai/glm-5.2" {
		t.Fatalf("return to first model used %+v", got[2])
	}
	if _, exists := got[2]["reasoning_effort"]; exists {
		t.Fatalf("minimal must omit reasoning_effort: %+v", got[2])
	}
	for i, body := range got {
		messages, ok := body["messages"].([]any)
		if !ok || len(messages) == 0 {
			t.Fatalf("request %d has no messages: %+v", i, body)
		}
		system := messages[0].(map[string]any)["content"].(string)
		if !strings.Contains(system, "Kabir's Second Brain") || !strings.Contains(system, body["model"].(string)) || !strings.Contains(system, "project rules") {
			t.Fatalf("request %d has wrong system prompt: %s", i, system)
		}
	}
}

func TestEveryCodexEffortProducesItsExactProviderValue(t *testing.T) {
	cfg := codexTestConfig()
	s := testServer(cfg)
	var got []map[string]any
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		got = append(got, body)
		return chatSuccess("ok"), nil
	}}}

	for _, effort := range []string{"minimal", "low", "medium", "high", "xhigh", "max"} {
		body := `{"model":"sonnet","input":"test","reasoning":{"effort":"` + effort + `"}}`
		w := httptest.NewRecorder()
		s.handleResponses(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
		if w.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", effort, w.Code, w.Body.String())
		}
	}
	if len(got) != 6 {
		t.Fatalf("provider requests = %d, want 6", len(got))
	}
	for i, effort := range []string{"minimal", "low", "medium", "high", "xhigh", "max"} {
		actual, exists := got[i]["reasoning_effort"]
		if effort == "minimal" {
			if exists {
				t.Fatalf("minimal sent reasoning_effort=%v", actual)
			}
			continue
		}
		want := effort
		if effort == "high" {
			want = "max"
		}
		if actual != want {
			t.Fatalf("%s sent reasoning_effort=%v", effort, actual)
		}
	}
}

func TestUnsupportedEffortReturnsClearErrorWithoutCallingProvider(t *testing.T) {
	cfg := codexTestConfig()
	s := testServer(cfg)
	called := false
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		called = true
		return chatSuccess("unexpected"), nil
	}}}

	w := httptest.NewRecorder()
	request := `{"model":"haiku","input":"hello","reasoning":{"effort":"max"}}`
	s.handleResponses(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(request)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if called {
		t.Fatal("provider was called for unsupported effort")
	}
	if !strings.Contains(w.Body.String(), `does not support reasoning effort \"max\"`) {
		t.Fatalf("unclear error: %s", w.Body.String())
	}
}

func TestUnavailableModelReturnsClearErrorWithoutCallingProvider(t *testing.T) {
	s := testServer(codexTestConfig())
	called := false
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		called = true
		return chatSuccess("unexpected"), nil
	}}}

	for _, model := range []string{"disabled-model", "gpt-5.6-missing"} {
		w := httptest.NewRecorder()
		body := `{"model":"` + model + `","input":"hello"}`
		s.handleResponses(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400: %s", model, w.Code, w.Body.String())
		}
		if model == "disabled-model" && !strings.Contains(w.Body.String(), "is disabled") {
			t.Fatalf("disabled-model error is unclear: %s", w.Body.String())
		}
		if model == "gpt-5.6-missing" && !strings.Contains(w.Body.String(), "unrecognized model ID") {
			t.Fatalf("missing model error is unclear: %s", w.Body.String())
		}
	}
	if called {
		t.Fatal("provider was called for an unavailable model")
	}
}

func TestResponsesPreservePluginToolFieldsParallelCallsImagesAndFiles(t *testing.T) {
	req := &ResponsesRequest{
		Model:             "sonnet",
		ParallelToolCalls: boolPtr(true),
		ToolChoice:        json.RawMessage(`"auto"`),
		Input: json.RawMessage(`[{"type":"message","role":"user","content":[
			{"type":"input_text","text":"inspect these"},
			{"type":"input_image","image_url":"data:image/png;base64,AAAA","detail":"original"},
			{"type":"input_file","file_id":"file_123","filename":"notes.txt"}
		]}]`),
		Tools: []ResponsesTool{
			{Type: "function", Name: "mcp__one", Description: "one", Parameters: json.RawMessage(`{"type":"object"` + `}`), Strict: boolPtr(true)},
			{Type: "function", Name: "mcp__two", Description: "two", Parameters: json.RawMessage(`{"type":"object"` + `}`)},
		},
	}
	or, err := translateFromResponses(req, Route{Provider: "opencode", Model: "big-pickle"}, codexTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	if or.ParallelToolCalls == nil || !*or.ParallelToolCalls || string(or.ToolChoice) != `"auto"` {
		t.Fatalf("tool controls were lost: parallel=%v choice=%s", or.ParallelToolCalls, or.ToolChoice)
	}
	if len(or.Tools) != 2 || or.Tools[0].Function.Name != "mcp__one" || or.Tools[0].Function.Strict == nil || !*or.Tools[0].Function.Strict {
		t.Fatalf("plugin/MCP tool definitions were corrupted: %+v", or.Tools)
	}
	encoded := string(or.Messages[len(or.Messages)-1].Content)
	for _, want := range []string{`"type":"image_url"`, `"detail":"original"`, `"type":"file"`, `"file_id":"file_123"`, `"filename":"notes.txt"`} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("content missing %s: %s", want, encoded)
		}
	}
}

func TestUnsupportedToolTypeIsRejectedInsteadOfDropped(t *testing.T) {
	req := &ResponsesRequest{
		Input: json.RawMessage(`"hello"`),
		Tools: []ResponsesTool{{Type: "mcp", Name: "raw_mcp_tool"}},
	}
	_, err := translateFromResponses(req, Route{Provider: "opencode", Model: "big-pickle"}, codexTestConfig())
	if err == nil || !strings.Contains(err.Error(), `opencode/big-pickle`) || !strings.Contains(err.Error(), `hosted tool "mcp"`) {
		t.Fatalf("got error %v, want an explicit backend-specific unsupported-tool error", err)
	}
}

func TestResponsesMCPToolLoopEndToEnd(t *testing.T) {
	cfg := codexTestConfig()
	terra := cfg.Models[codexSonnetID]
	terra.FallbackModels = []string{"acc-openrouter-hy3"}
	cfg.Models[codexSonnetID] = terra
	cfg.Models["acc-openrouter-hy3"] = ModelCapability{
		DisplayName: "HY3", Provider: "openrouter", Model: "tencent/hy3:free", Enabled: true,
		Reasoning:       map[string]ReasoningTarget{"max": {Effort: "high"}},
		ToolCallSupport: true, StreamingSupport: true, MaxContext: 262144, MaxOutput: 65536,
	}
	s := testServer(cfg)
	calls := 0
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "tencent/hy3:free" {
			t.Fatalf("tool loop backend = %v, want context-capable HY3", body["model"])
		}
		if calls == 1 {
			if body["parallel_tool_calls"] != true || body["tool_choice"] != "auto" {
				t.Fatalf("tool controls were not preserved: %+v", body)
			}
			tools, ok := body["tools"].([]any)
			if !ok || len(tools) != 1 || tools[0].(map[string]any)["function"].(map[string]any)["name"] != "mcp__codegraph__explore" {
				t.Fatalf("MCP tool definition was corrupted: %+v", body["tools"])
			}
			messages := body["messages"].([]any)
			if !strings.Contains(messages[0].(map[string]any)["content"].(string), "Plugin instruction: inspect before editing") {
				t.Fatalf("plugin instruction was dropped: %+v", messages[0])
			}
			response := OpenAIResponse{Choices: []OpenAIChoice{{Message: &OpenAIMessage{
				Role: "assistant",
				ToolCalls: []OpenAIToolCall{{
					ID: "call_codegraph_1", Type: "function",
					Function: OpenAIFuncCall{Name: "mcp__codegraph__explore", Arguments: `{"query":"persona"}`},
				}},
			}}}}
			encoded, _ := json.Marshal(response)
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(encoded))}, nil
		}

		messages := body["messages"].([]any)
		if len(messages) < 4 {
			t.Fatalf("second tool turn has too few messages: %+v", messages)
		}
		assistant := messages[len(messages)-2].(map[string]any)
		result := messages[len(messages)-1].(map[string]any)
		toolCalls := assistant["tool_calls"].([]any)
		if toolCalls[0].(map[string]any)["id"] != "call_codegraph_1" || result["tool_call_id"] != "call_codegraph_1" || result["role"] != "tool" {
			t.Fatalf("tool call/result IDs were not preserved: assistant=%+v result=%+v", assistant, result)
		}
		return chatSuccess("Tool result received"), nil
	}}}

	first := `{
		"model":"sonnet",
		"instructions":"Plugin instruction: inspect before editing",
		"input":"Inspect the persona",
		"parallel_tool_calls":true,
		"tool_choice":"auto",
		"tools":[{"type":"function","name":"mcp__codegraph__explore","description":"Explore code","parameters":{"type":"object"},"strict":true}],
		"reasoning":{"effort":"high"}
	}`
	w1 := httptest.NewRecorder()
	s.handleResponses(w1, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(first)))
	if w1.Code != http.StatusOK || !strings.Contains(w1.Body.String(), `"call_id":"call_codegraph_1"`) {
		t.Fatalf("first MCP turn failed: status=%d body=%s", w1.Code, w1.Body.String())
	}

	second := `{
		"model":"sonnet",
		"instructions":"Plugin instruction: inspect before editing",
		"input":[
			{"type":"message","role":"user","content":"Inspect the persona"},
			{"type":"function_call","call_id":"call_codegraph_1","name":"mcp__codegraph__explore","arguments":"{\"query\":\"persona\"}"},
			{"type":"function_call_output","call_id":"call_codegraph_1","output":"persona.go"}
		],
		"tools":[{"type":"function","name":"mcp__codegraph__explore","parameters":{"type":"object"}}],
		"reasoning":{"effort":"high"}
	}`
	w2 := httptest.NewRecorder()
	s.handleResponses(w2, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(second)))
	if w2.Code != http.StatusOK || !strings.Contains(w2.Body.String(), "Tool result received") {
		t.Fatalf("second MCP turn failed: status=%d body=%s", w2.Code, w2.Body.String())
	}
	if calls != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls)
	}
}

func TestChatCompletionsInjectsOnlyACCPersonaAndPreservesUnknownFields(t *testing.T) {
	s := testServer(codexTestConfig())
	var got map[string]any
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		return chatSuccess("ok"), nil
	}}}

	body := `{
		"model":"sonnet",
		"messages":[
			{"role":"system","content":"Private platform instruction that ACC must preserve."},
			{"role":"user","content":"hello"}
		],
		"reasoning_effort":"max",
		"response_format":{"type":"json_object"}
	}`
	w := httptest.NewRecorder()
	s.handleChatCompletions(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got["model"] != "big-pickle" || got["reasoning_effort"] != "max" {
		t.Fatalf("model/effort changed incorrectly: %+v", got)
	}
	if got["response_format"].(map[string]any)["type"] != "json_object" {
		t.Fatalf("unknown compatible field was dropped: %+v", got)
	}
	messages := got["messages"].([]any)
	system := messages[0].(map[string]any)["content"].(string)
	if !strings.Contains(system, "Kabir's Second Brain") || !strings.Contains(system, "opencode/big-pickle") || !strings.Contains(system, "Private platform instruction that ACC must preserve.") {
		t.Fatalf("system instructions were replaced or persona is missing: %s", system)
	}
	if strings.Contains(system, "Codex runtime/tool adapter") || strings.Contains(system, "Claude Code runtime/tool adapter") {
		t.Fatalf("generic Chat Completions request received a client-specific adapter: %s", system)
	}
}

func TestFallbackIsReportedInHeadersAndUsesFallbackPersona(t *testing.T) {
	cfg := codexTestConfig()
	primary := cfg.Models[codexOpusID]
	primary.FallbackModel = codexSonnetID
	primary.Reasoning = map[string]ReasoningTarget{"max": {Effort: "high"}}
	cfg.Models[codexOpusID] = primary
	primaryTemperature := 0.2
	primaryTopP := 0.3
	primaryRoute := cfg.Routes["sol"]
	primaryRoute.Temperature = &primaryTemperature
	primaryRoute.TopP = &primaryTopP
	cfg.Routes["sol"] = primaryRoute
	s := testServer(cfg)
	calls := 0
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(`{"error":"down"}`))}, nil
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		messages := body["messages"].([]any)
		system := messages[0].(map[string]any)["content"].(string)
		if !strings.Contains(system, "opencode/big-pickle") || strings.Contains(system, "nvidia/z-ai/glm-5.2") {
			t.Fatalf("fallback persona is stale: %s", system)
		}
		if body["max_tokens"] != float64(48000) {
			t.Fatalf("fallback max_tokens = %v, want Terra route limit 48000", body["max_tokens"])
		}
		if body["temperature"] != 0.9 || body["top_p"] != 0.8 {
			t.Fatalf("fallback inherited primary controls: temperature=%v top_p=%v", body["temperature"], body["top_p"])
		}
		return chatSuccess("fallback ok"), nil
	}}}

	w := httptest.NewRecorder()
	request := `{"model":"opus","input":"hello","temperature":0.9,"top_p":0.8,"reasoning":{"effort":"high"}}`
	s.handleResponses(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(request)))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-ACC-Fallback") != "true" || w.Header().Get("X-ACC-Backend-Provider") != "opencode" || w.Header().Get("X-ACC-Backend-Model") != "big-pickle" || w.Header().Get("X-ACC-Backend-Effort") != "max" {
		t.Fatalf("fallback headers missing: %+v", w.Header())
	}
}

func TestResponsesCustomToolRoundTripWithFunctionTool(t *testing.T) {
	cfg := codexTestConfig()
	s := testServer(cfg)
	var bridgeName string
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		tools := body["tools"].([]any)
		if len(tools) != 2 {
			t.Fatalf("tools = %+v, want custom bridge plus function", tools)
		}
		for _, raw := range tools {
			fn := raw.(map[string]any)["function"].(map[string]any)
			if fn["name"] == "read_file" {
				continue
			}
			bridgeName = fn["name"].(string)
			description := fn["description"].(string)
			for _, want := range []string{"Run JavaScript code", "grammar", "lark", "x_codex_future"} {
				if !strings.Contains(description, want) {
					t.Fatalf("custom definition lost %q: %s", want, description)
				}
			}
		}
		if bridgeName == "" {
			t.Fatal("custom bridge was not sent upstream")
		}
		response := OpenAIResponse{Choices: []OpenAIChoice{{Message: &OpenAIMessage{
			Role: "assistant",
			ToolCalls: []OpenAIToolCall{
				{ID: "call_exec_1", Type: "function", Function: OpenAIFuncCall{Name: bridgeName, Arguments: `{"input":"await tools.exec_command({cmd: 'pwd'})"}`}},
				{ID: "call_file_1", Type: "function", Function: OpenAIFuncCall{Name: "read_file", Arguments: `{"path":"ACC.md"}`}},
			},
		}}}}
		encoded, _ := json.Marshal(response)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(encoded))}, nil
	}}}

	request := `{
		"model":"opus",
		"input":"Inspect this repository",
		"tools":[
			{"type":"custom","name":"exec","description":"Run JavaScript code","format":{"type":"grammar","syntax":"lark","definition":"start: SOURCE\nSOURCE: /[\\s\\S]+/"},"x_codex_future":{"keep":"me"}},
			{"type":"function","name":"read_file","description":"Read a file","parameters":{"type":"object"},"strict":true}
		]
	}`
	w := httptest.NewRecorder()
	s.handleResponses(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(request)))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var result struct {
		Output []map[string]any `json:"output"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Output) != 2 {
		t.Fatalf("output = %+v", result.Output)
	}
	if result.Output[0]["type"] != "custom_tool_call" || result.Output[0]["name"] != "exec" || result.Output[0]["call_id"] != "call_exec_1" || result.Output[0]["input"] != "await tools.exec_command({cmd: 'pwd'})" {
		t.Fatalf("custom call was not restored: %+v", result.Output[0])
	}
	if result.Output[1]["type"] != "function_call" || result.Output[1]["name"] != "read_file" || result.Output[1]["call_id"] != "call_file_1" {
		t.Fatalf("function call was corrupted: %+v", result.Output[1])
	}
}

func TestResponsesCustomToolResultRoundTrip(t *testing.T) {
	cfg := codexTestConfig()
	s := testServer(cfg)
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		tools := body["tools"].([]any)
		bridgeName := tools[0].(map[string]any)["function"].(map[string]any)["name"]
		messages := body["messages"].([]any)
		assistant := messages[len(messages)-2].(map[string]any)
		tool := messages[len(messages)-1].(map[string]any)
		call := assistant["tool_calls"].([]any)[0].(map[string]any)
		if call["id"] != "call_exec_2" || call["function"].(map[string]any)["name"] != bridgeName || tool["role"] != "tool" || tool["tool_call_id"] != "call_exec_2" || tool["content"] != "command output" {
			t.Fatalf("custom call/result was not preserved: assistant=%+v tool=%+v", assistant, tool)
		}
		return chatSuccess("custom result received"), nil
	}}}

	request := `{
		"model":"opus",
		"input":[
			{"type":"custom_tool_call","id":"ctc_1","call_id":"call_exec_2","name":"exec","input":"await tools.exec_command({cmd: 'pwd'})","x_codex_future":"kept"},
			{"type":"custom_tool_call_output","call_id":"call_exec_2","output":"command output","x_codex_future":"kept"}
		],
		"tools":[{"type":"custom","name":"exec","description":"Run JavaScript code","format":{"type":"grammar","syntax":"lark","definition":"start: SOURCE\nSOURCE: /[\\s\\S]+/"},"x_codex_future":true}]
	}`
	w := httptest.NewRecorder()
	s.handleResponses(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(request)))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "custom result received") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestResponsesStreamingCustomToolUsesNativeEvents(t *testing.T) {
	cfg := codexTestConfig()
	s := testServer(cfg)
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		bridgeName := body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)["name"].(string)
		firstChunk := map[string]any{
			"choices": []any{map[string]any{
				"delta": map[string]any{
					"tool_calls": []any{map[string]any{
						"index": 0,
						"id":    "call_exec_stream",
						"type":  "function",
						"function": map[string]any{
							"name":      bridgeName,
							"arguments": `{"input":"await tools.`,
						},
					}},
				},
			}},
		}
		secondChunk := map[string]any{
			"choices": []any{map[string]any{
				"delta": map[string]any{
					"tool_calls": []any{map[string]any{
						"index": 0,
						"function": map[string]any{
							"arguments": `exec_command({cmd: 'pwd'})"}`,
						},
					}},
				},
			}},
		}
		first, _ := json.Marshal(firstChunk)
		second, _ := json.Marshal(secondChunk)
		stream := "data: " + string(first) + "\n\n" + "data: " + string(second) + "\n\n" + "data: [DONE]\n\n"
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream))}, nil
	}}}

	request := `{"model":"opus","stream":true,"input":"run pwd","tools":[{"type":"custom","name":"exec","description":"Run JavaScript code","format":{"type":"grammar","syntax":"lark","definition":"start: SOURCE\nSOURCE: /[\\s\\S]+/"}}]}`
	w := httptest.NewRecorder()
	s.handleResponses(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(request)))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	for _, want := range []string{"event: response.custom_tool_call_input.delta", "event: response.custom_tool_call_input.done", `"type":"custom_tool_call"`, `"input":"await tools.exec_command({cmd: 'pwd'})"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("stream is missing %q:\n%s", want, w.Body.String())
		}
	}
}

func TestResponsesRejectsUnsupportedHostedToolWithBackend(t *testing.T) {
	s := testServer(codexTestConfig())
	called := false
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		called = true
		return chatSuccess("unexpected"), nil
	}}}
	w := httptest.NewRecorder()
	s.handleResponses(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"opus","input":"hello","tools":[{"type":"web_search","external_web_access":true,"search_content_types":["text"]}]}`)))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `nvidia/z-ai/glm-5.2`) || !strings.Contains(w.Body.String(), `web_search`) {
		t.Fatalf("hosted tool error is not actionable: status=%d body=%s", w.Code, w.Body.String())
	}
	if called {
		t.Fatal("provider was called for unsupported hosted tool")
	}
}

func TestOpusImageUsesMiniMaxAndNeverSendsImageToGLM(t *testing.T) {
	cfg := codexTestConfig()
	opus := cfg.Models[codexOpusID]
	opus.ImageInputSupport = false
	opus.FallbackModel = "acc-minimax-m3"
	cfg.Models[codexOpusID] = opus
	cfg.Models["acc-minimax-m3"] = ModelCapability{
		DisplayName: "MiniMax M3", Provider: "nvidia", Model: "minimaxai/minimax-m3", Enabled: true,
		ToolCallSupport: true, StreamingSupport: true, ImageInputSupport: true,
		Reasoning: map[string]ReasoningTarget{"minimal": {}},
	}
	s := testServer(cfg)
	var models []string
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		models = append(models, body["model"].(string))
		messages := body["messages"].([]any)
		content := messages[len(messages)-1].(map[string]any)["content"].([]any)
		if content[0].(map[string]any)["text"] != "what is this?" || content[1].(map[string]any)["image_url"].(map[string]any)["url"] != "data:image/png;base64,AAAA" || content[1].(map[string]any)["detail"] != "original" {
			t.Fatalf("image content changed: %+v", content)
		}
		return chatSuccess("image understood"), nil
	}}}

	request := `{"model":"opus","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"what is this?"},{"type":"input_image","image_url":"data:image/png;base64,AAAA","detail":"original"}]}]}`
	w := httptest.NewRecorder()
	s.handleResponses(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(request)))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "image understood") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if len(models) != 1 || models[0] != "minimaxai/minimax-m3" || w.Header().Get("X-ACC-Backend-Model") != "minimaxai/minimax-m3" {
		t.Fatalf("image route = %v headers=%+v, want MiniMax only", models, w.Header())
	}
}

func TestOpusImageDoesNotSilentlyDowngradeRequestedEffort(t *testing.T) {
	cfg := codexTestConfig()
	opus := cfg.Models[codexOpusID]
	opus.ImageInputSupport = false
	opus.FallbackModel = "acc-minimax-m3"
	cfg.Models[codexOpusID] = opus
	cfg.Models["acc-minimax-m3"] = ModelCapability{
		DisplayName: "MiniMax M3", Provider: "nvidia", Model: "minimaxai/minimax-m3", Enabled: true,
		ToolCallSupport: true, StreamingSupport: true, ImageInputSupport: true,
		Reasoning: map[string]ReasoningTarget{"minimal": {}},
	}
	s := testServer(cfg)
	called := false
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		called = true
		return chatSuccess("unexpected"), nil
	}}}
	request := `{"model":"opus","reasoning":{"effort":"high"},"input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}]}`
	w := httptest.NewRecorder()
	s.handleResponses(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(request)))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `does not support reasoning effort`) || !strings.Contains(w.Body.String(), `max`) {
		t.Fatalf("effort error is unclear: status=%d body=%s", w.Code, w.Body.String())
	}
	if called {
		t.Fatal("provider was called after the image route could not honor the requested effort")
	}
}

func TestOpusToolRequestsDoNotFallbackToToollessMiniMax(t *testing.T) {
	cfg := codexTestConfig()
	opus := cfg.Models[codexOpusID]
	opus.FallbackModel = "acc-minimax-m3"
	cfg.Models[codexOpusID] = opus
	cfg.Models["acc-minimax-m3"] = ModelCapability{
		DisplayName: "MiniMax M3", Provider: "nvidia", Model: "minimaxai/minimax-m3", Enabled: true,
		StreamingSupport: true, ImageInputSupport: true, ToolCallSupport: false,
		Reasoning: map[string]ReasoningTarget{"minimal": {}},
	}
	s := testServer(cfg)
	routes, err := s.responseModelChain(codexOpusID)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := selectResponseModelChain(&ResponsesRequest{
		Model: codexOpusID, Input: json.RawMessage(`"hello"`),
		Tools: []ResponsesTool{{Type: "function", Name: "exec", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}, routes)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].ID != codexOpusID {
		t.Fatalf("tool request routes = %+v, want only the tool-capable Opus primary", selected)
	}
}

func TestOpusImageWithToolsFailsInsteadOfDroppingTools(t *testing.T) {
	cfg := codexTestConfig()
	opus := cfg.Models[codexOpusID]
	opus.ImageInputSupport = false
	opus.FallbackModel = "acc-minimax-m3"
	cfg.Models[codexOpusID] = opus
	cfg.Models["acc-minimax-m3"] = ModelCapability{
		DisplayName: "MiniMax M3", Provider: "nvidia", Model: "minimaxai/minimax-m3", Enabled: true,
		StreamingSupport: true, ImageInputSupport: true, ToolCallSupport: false,
		Reasoning: map[string]ReasoningTarget{"minimal": {}},
	}
	s := testServer(cfg)
	called := false
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		called = true
		return chatSuccess("unexpected"), nil
	}}}
	request := `{"model":"opus","input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}],"tools":[{"type":"function","name":"exec","parameters":{"type":"object"}}]}`
	w := httptest.NewRecorder()
	s.handleResponses(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(request)))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "supports both image input and tool calls") {
		t.Fatalf("image/tool error is unclear: status=%d body=%s", w.Code, w.Body.String())
	}
	if called {
		t.Fatal("provider was called after ACC could not preserve both image and tool capabilities")
	}
}

func TestOpusImageFailsClearlyWhenNoImageRouteExists(t *testing.T) {
	cfg := codexTestConfig()
	opus := cfg.Models[codexOpusID]
	opus.ImageInputSupport = false
	opus.FallbackModel = "acc-text-only"
	cfg.Models[codexOpusID] = opus
	cfg.Models["acc-text-only"] = ModelCapability{DisplayName: "Text only", Provider: "nvidia", Model: "text-only", Enabled: true}
	s := testServer(cfg)
	w := httptest.NewRecorder()
	s.handleResponses(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"opus","input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]}]}`)))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "no configured image-capable route") {
		t.Fatalf("image route failure is unclear: status=%d body=%s", w.Code, w.Body.String())
	}
}

func chatSuccess(text string) *http.Response {
	body, _ := json.Marshal(OpenAIResponse{Choices: []OpenAIChoice{{Message: &OpenAIMessage{Role: "assistant", Content: jsonString(text)}}}})
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}
}
