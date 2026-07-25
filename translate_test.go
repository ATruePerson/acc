package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func testCfg() *Config {
	return &Config{
		Effort: map[string]EffortMap{
			"low":  {Budget: 2000, Reasoning: "low"},
			"high": {Budget: 16000, Reasoning: "medium"},
			"max":  {Budget: 32000, Reasoning: "high"},
		},
	}
}

// The screenshot bug: an Anthropic image block must become an OpenAI
// image_url data: URL, not get dropped.
func TestImageBlockTranslates(t *testing.T) {
	content := `[
		{"type":"text","text":"what is this screenshot of?"},
		{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAABBBB"}}
	]`
	ar := &AnthropicRequest{
		Model:    "claude-opus-4-8",
		Messages: []AnthropicMessage{{Role: "user", Content: json.RawMessage(content)}},
	}
	or, err := translateRequest(ar, Route{Model: "glm-4.6"}, testCfg())
	if err != nil {
		t.Fatal(err)
	}
	last := or.Messages[len(or.Messages)-1]
	var parts []OpenAIContentPart
	if err := json.Unmarshal(last.Content, &parts); err != nil {
		t.Fatalf("content not multipart: %s", last.Content)
	}
	foundImage := false
	for _, p := range parts {
		if p.Type == "image_url" {
			foundImage = true
			if !strings.HasPrefix(p.ImageURL.URL, "data:image/png;base64,AAAABBBB") {
				t.Fatalf("bad data url: %s", p.ImageURL.URL)
			}
		}
	}
	if !foundImage {
		t.Fatal("image block was dropped — the bug")
	}
}

func TestEffortBucket(t *testing.T) {
	cfg := testCfg()
	if got := bucketForBudget(2000, cfg); got != "low" {
		t.Fatalf("2000 -> %s, want low", got)
	}
	if got := bucketForBudget(32000, cfg); got != "high" {
		t.Fatalf("32000 -> %s, want high", got)
	}
}

func TestSystemPromptBecomesFirstMessage(t *testing.T) {
	ar := &AnthropicRequest{
		Model:    "claude-3-5-sonnet",
		System:   json.RawMessage(`"you are helpful"`),
		Messages: []AnthropicMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	}
	or, _ := translateRequest(ar, Route{Model: "x"}, testCfg())
	if or.Messages[0].Role != "system" {
		t.Fatalf("first msg role %s, want system", or.Messages[0].Role)
	}
}

func TestLegacyRoutePersonaIsNotInjected(t *testing.T) {
	ar := &AnthropicRequest{
		Model:    "x",
		System:   json.RawMessage(`"base"`),
		Messages: []AnthropicMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	}
	cfg := testCfg()
	cfg.SystemPrepend = "GLOBAL"
	route := Route{Model: "x", SystemPrepend: "I am Claude Fable 5."}
	or, _ := translateRequest(ar, route, cfg)

	sys := string(or.Messages[0].Content)
	if strings.Contains(sys, "I am Claude Fable 5.") {
		t.Fatalf("legacy route persona was injected: %s", sys)
	}
	if !strings.Contains(sys, "GLOBAL") {
		t.Fatalf("global user instruction was dropped: %s", sys)
	}
	if !strings.Contains(sys, "base") {
		t.Fatalf("original system text dropped: %s", sys)
	}
	if !strings.Contains(sys, "Kabir's Second Brain") {
		t.Fatalf("ACC persona missing: %s", sys)
	}
}

func TestAnthropicTranslationUsesOnlyClaudeRuntimePersona(t *testing.T) {
	ar := &AnthropicRequest{
		Model:    "claude-sonnet",
		System:   json.RawMessage(`"Claude platform instruction"`),
		Messages: []AnthropicMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
	}
	or, err := translateRequest(ar, Route{Provider: "opencode", Model: "big-pickle"}, testCfg())
	if err != nil {
		t.Fatal(err)
	}
	if len(or.Messages) == 0 {
		t.Fatal("translated request has no system message")
	}
	system := decodeStringContent(or.Messages[0].Content)
	if !strings.Contains(system, "Claude Code runtime/tool adapter") || strings.Contains(system, "Codex runtime/tool adapter") {
		t.Fatalf("Anthropic request has the wrong runtime adapter:\n%s", system)
	}
	if !strings.Contains(system, "Claude platform instruction") {
		t.Fatalf("Claude platform instruction was dropped:\n%s", system)
	}
}

func TestRouteOverridesTemperatureAndMaxTokens(t *testing.T) {
	tempVal := 0.2
	tempOrig := 1.0
	ar := &AnthropicRequest{
		Model:       "x",
		MaxTokens:   4000,
		Temperature: &tempOrig,
		Messages:    []AnthropicMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	}
	route := Route{
		Model:       "x",
		Temperature: &tempVal,
		MaxTokens:   500,
	}
	or, _ := translateRequest(ar, route, testCfg())

	if or.MaxTokens != 500 {
		t.Errorf("got MaxTokens %d, want 500", or.MaxTokens)
	}
	if or.Temperature == nil || *or.Temperature != 0.2 {
		t.Errorf("got Temperature %v, want 0.2", or.Temperature)
	}
}

func TestToolResultBecomesToolMessage(t *testing.T) {
	content := `[{"type":"tool_result","tool_use_id":"call_1","content":"42"}]`
	msgs, err := translateMessage(AnthropicMessage{Role: "user", Content: json.RawMessage(content)})
	if err != nil {
		t.Fatal(err)
	}
	if msgs[0].Role != "tool" || msgs[0].ToolCallID != "call_1" {
		t.Fatalf("got %+v", msgs[0])
	}
}

func TestToolResultAndTextOrder(t *testing.T) {
	content := `[
		{"type":"tool_result","tool_use_id":"call_1","content":"42"},
		{"type":"text","text":"continue"}
	]`
	msgs, err := translateMessage(AnthropicMessage{Role: "user", Content: json.RawMessage(content)})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "tool" || msgs[0].ToolCallID != "call_1" {
		t.Errorf("first message should be the tool response, got %+v", msgs[0])
	}
	if msgs[1].Role != "user" || string(msgs[1].Content) != `"continue"` {
		t.Errorf("second message should be the user text, got %+v", msgs[1])
	}
}

func TestConfigAliasOverridesAndExtends(t *testing.T) {
	s := testServer(&Config{
		Providers: map[string]Provider{
			"nvidia":   {BaseURL: "x", APIKey: "k"},
			"opencode": {BaseURL: "y", APIKey: "k"},
		},
		Aliases: map[string]Route{
			// new alias not in the built-in catalog
			"claude-fast": {Provider: "nvidia", Model: "stepfun-ai/step-3.7-flash", ReasoningEffort: "low"},
			// override a built-in canonical
			"claude_GLM": {Provider: "opencode", Model: "big-pickle", ReasoningEffort: "medium"},
		},
	})

	fast, err := s.routeFor("anthropic/claude-fast")
	if err != nil || fast.Model != "stepfun-ai/step-3.7-flash" || fast.ReasoningEffort != "low" {
		t.Fatalf("claude-fast routed to %+v, err %v", fast, err)
	}

	glm, err := s.routeFor("claude-glm")
	if err != nil || glm.Provider != "opencode" || glm.Model != "big-pickle" {
		t.Fatalf("config override failed, got %+v err %v", glm, err)
	}
}

func TestFamilyAliasesUseConfiguredRoutesExactly(t *testing.T) {
	want := map[string]Route{
		"opus":   {Provider: "openrouter", Model: "tencent/hy3:free"},
		"sonnet": {Provider: "opencode", Model: "big-pickle"},
		"haiku":  {Provider: "nvidia", Model: "nvidia/nemotron-3-super-120b-a12b"},
	}
	s := testServer(&Config{
		AliasRoutes: want,
		Routes: map[string]Route{
			// These routes are owned by the separate Codex capability registry.
			// Legacy family aliases must not read or modify them.
			"opus":   {Provider: "protected", Model: "codex-opus"},
			"sonnet": {Provider: "protected", Model: "codex-sonnet"},
			"haiku":  {Provider: "protected", Model: "codex-haiku"},
		},
		Aliases: map[string]Route{
			// Family aliases must not drift from their named route if an old
			// duplicated aliases block remains in a user's config.
			"anthropic/claude-opus":   {Provider: "stale", Model: "old-opus"},
			"anthropic/claude-sonnet": {Provider: "stale", Model: "old-sonnet"},
			"anthropic/claude-haiku":  {Provider: "stale", Model: "old-haiku"},
		},
	})

	for family, route := range want {
		for _, id := range []string{family, "claude-" + family, "anthropic/claude-" + family, "claude-" + family + "-4-5", "anthropic/claude-" + family + "-4-5-20260701"} {
			got, err := s.routeFor(id)
			if err != nil {
				t.Fatalf("routeFor(%q): %v", id, err)
			}
			if got.Provider != route.Provider || got.Model != route.Model {
				t.Errorf("routeFor(%q) = %s/%s, want %s/%s", id, got.Provider, got.Model, route.Provider, route.Model)
			}
		}
	}

	if _, err := s.routeFor("claude-opus-copy"); err == nil {
		t.Fatal("substring lookalike unexpectedly resolved as the opus alias")
	}
}

func TestConfiguredAliasRoutesKeepProviderDiversityAndMaximumReasoning(t *testing.T) {
	raw, err := os.ReadFile("config.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Aliases) != 0 {
		t.Fatalf("family routes are duplicated in aliases: %v", cfg.Aliases)
	}

	opus := cfg.AliasRoutes["opus"]
	sonnet := cfg.AliasRoutes["sonnet"]
	haiku := cfg.AliasRoutes["haiku"]
	if opus.Provider != "nvidia" || opus.Model != "z-ai/glm-5.2" || opus.ReasoningEffort != "high" {
		t.Fatalf("opus route = %+v", opus)
	}
	if sonnet.Provider != "opencode" || sonnet.Model != "big-pickle" || sonnet.ReasoningEffort != "max" {
		t.Fatalf("sonnet route = %+v", sonnet)
	}
	if haiku.Provider != "nvidia" || haiku.Model != "stepfun-ai/step-3.7-flash" {
		t.Fatalf("haiku route = %+v", haiku)
	}

	if len(opus.Fallbacks) != 1 || opus.Fallbacks[0].Provider != "openrouter" || opus.Fallbacks[0].Model != "nvidia/nemotron-3-ultra-550b-a55b:free" {
		t.Fatalf("opus fallback = %+v", opus.Fallbacks)
	}
	if len(sonnet.Fallbacks) != 2 || sonnet.Fallbacks[0].Provider != "openrouter" || sonnet.Fallbacks[0].Model != "tencent/hy3:free" || sonnet.Fallbacks[1].Provider != "nvidia" || sonnet.Fallbacks[1].Model != "nvidia/nemotron-3-super-120b-a12b" {
		t.Fatalf("sonnet fallback = %+v", sonnet.Fallbacks)
	}
	if len(haiku.Fallbacks) != 0 {
		t.Fatalf("haiku fallback = %+v", haiku.Fallbacks)
	}
	for name, route := range map[string]Route{
		"opus": opus, "opus fallback": opus.Fallbacks[0],
		"sonnet": sonnet, "sonnet fallback 1": sonnet.Fallbacks[0], "sonnet fallback 2": sonnet.Fallbacks[1],
		"haiku": haiku,
	} {
		if !route.ReasoningLocked {
			t.Errorf("%s reasoning is not locked to provider maximum", name)
		}
	}

	for name, route := range map[string]Route{"opus": opus, "sonnet fallback": sonnet.Fallbacks[1]} {
		if got := route.ExtraBody["reasoning_budget"]; got != float64(32000) {
			t.Fatalf("%s reasoning budget = %v, want 32000", name, got)
		}
		thinking, ok := route.ExtraBody["chat_template_kwargs"].(map[string]any)
		if !ok || thinking["enable_thinking"] != true {
			t.Fatalf("%s does not enable NVIDIA thinking: %v", name, route.ExtraBody)
		}
	}

	var defaults Config
	if err := json.Unmarshal([]byte(defaultConfigJSON), &defaults); err != nil {
		t.Fatal(err)
	}
	for _, family := range []string{"opus", "sonnet", "haiku"} {
		configured, _ := json.Marshal(cfg.AliasRoutes[family])
		setupDefault, _ := json.Marshal(defaults.AliasRoutes[family])
		if string(configured) != string(setupDefault) {
			t.Errorf("default %s alias route differs from config.json\nconfig: %s\ndefault: %s", family, configured, setupDefault)
		}
	}
}

func TestReasoningLockedKeepsProviderMaximum(t *testing.T) {
	ar := &AnthropicRequest{Thinking: &Thinking{BudgetTokens: 32000}}
	route := Route{Provider: "opencode", Model: "big-pickle", ReasoningEffort: "max", ReasoningLocked: true}
	or, err := translateRequest(ar, route, testCfg())
	if err != nil {
		t.Fatal(err)
	}
	if or.ReasoningEffort != "max" {
		t.Fatalf("locked reasoning effort = %q, want max", or.ReasoningEffort)
	}

	route.ReasoningLocked = false
	or, err = translateRequest(ar, route, testCfg())
	if err != nil {
		t.Fatal(err)
	}
	if or.ReasoningEffort != "high" {
		t.Fatalf("unlocked reasoning effort = %q, want budget-mapped high", or.ReasoningEffort)
	}
}

func TestValidateConfigChecksAliasRouteFallbackProviders(t *testing.T) {
	cfg := &Config{
		Providers: map[string]Provider{"openrouter": {}},
		AliasRoutes: map[string]Route{
			"opus": {
				Provider: "openrouter",
				Model:    "tencent/hy3:free",
				Fallbacks: []Route{
					{Provider: "missing", Model: "fallback"},
				},
			},
		},
	}
	if err := validateConfig(cfg); err == nil || !strings.Contains(err.Error(), "provider \"missing\" not defined") {
		t.Fatalf("validateConfig error = %v", err)
	}
}

func TestCodexAliasesFollowConfiguredFamilies(t *testing.T) {
	cfg := codexTestConfig()
	s := testServer(cfg)
	tests := []struct {
		id, routeName string
	}{
		{"nvidia/z-ai/glm-5.2", "nvidia-glm"},
		{"opencode/big-pickle", "opencode-pickle"},
		{"nvidia/stepfun-ai/step-3.7-flash", "nvidia-step"},
	}
	for _, tc := range tests {
		route, err := s.routeFor(tc.id)
		if err != nil {
			t.Fatalf("routeFor(%q): %v", tc.id, err)
		}
		if want := cfg.Routes[tc.routeName]; route.Provider != want.Provider || route.Model != want.Model {
			t.Errorf("routeFor(%q) = %s/%s, want %s/%s", tc.id, route.Provider, route.Model, want.Provider, want.Model)
		}
		if len(route.Reasoning) == 0 || route.MaxTokens == 0 {
			t.Errorf("routeFor(%q) did not apply registry capabilities: %+v", tc.id, route)
		}
	}
}

func TestCostFor(t *testing.T) {
	cfg := &Config{Pricing: map[string]ModelPrice{
		"paid/model": {InputPer1M: 2.0, OutputPer1M: 6.0},
	}}
	// 1M in @ $2 + 1M out @ $6 = $8
	if got := costFor("paid/model", 1_000_000, 1_000_000, cfg); got != 8.0 {
		t.Fatalf("cost = %v, want 8.0", got)
	}
	if got := costFor("free/model", 1_000_000, 1_000_000, cfg); got != 0 {
		t.Fatalf("unpriced model cost = %v, want 0", got)
	}
}

func TestCatalogModelsAllRoute(t *testing.T) {
	s := testServer(&Config{Providers: map[string]Provider{
		"nvidia": {}, "opencode": {}, "openrouter": {},
	}})
	for _, d := range modelCatalog() {
		if _, err := s.routeFor("anthropic/" + d.Canonical); err != nil {
			t.Errorf("catalog canonical %q does not route: %v", d.Canonical, err)
		}
	}
}

func TestRouteFor(t *testing.T) {
	s := testServer(&Config{
		Providers: map[string]Provider{
			"nvidia":   {BaseURL: "https://integrate.api.nvidia.com/v1", APIKey: "fake"},
			"opencode": {BaseURL: "https://opencode.ai/zen/v1", APIKey: "fake"},
		},
	})

	testCases := []struct {
		inputModel     string
		expectedModel  string
		expectedProv   string
		expectedEffort string
	}{
		{"anthropic/claude_step_3.7_flash", "deepseek-ai/deepseek-v4-flash", "nvidia", ""},
		{"anthropic/stepfun-ai/step-3.7-flash", "deepseek-ai/deepseek-v4-flash", "nvidia", ""},
		{"anthropic/stepfun_ai_step_3.7_flash", "deepseek-ai/deepseek-v4-flash", "nvidia", ""},
		{"stepfun-ai/step-3.7-flash", "deepseek-ai/deepseek-v4-flash", "nvidia", ""},
		{"stepfun_ai_step_3.7_flash", "deepseek-ai/deepseek-v4-flash", "nvidia", ""},

		// Manual overrides tests
		{"anthropic/opencode/big-pickle", "big-pickle", "opencode", "high"},
		{"anthropic/claude-pickle", "big-pickle", "opencode", "high"},
		{"claude-nemotron-3-ultra-free", "nemotron-3-ultra-free", "opencode", "high"},
		{"anthropic/opencode/nemotron-3-ultra-free", "nemotron-3-ultra-free", "opencode", "high"},
		{"anthropic/claude-nemotron-3-ultra", "nemotron-3-ultra-free", "opencode", "high"},
		{"anthropic/claude-ultra", "nemotron-3-ultra-free", "opencode", "high"},
		{"anthropic/claude-ultra-free", "nemotron-3-ultra-free", "opencode", "high"},
		{"anthropic/claude-kim-2", "qwen/qwen-1m", "cloudflare", "high"},
		{"anthropic/claude_K_2", "qwen/qwen-1m", "cloudflare", "high"},
		{"anthropic/claude-kimi", "qwen/qwen-1m", "cloudflare", "high"},
		{"anthropic/claude-kim", "qwen/qwen-1m", "cloudflare", "high"},
		{"anthropic/claude-step", "deepseek-ai/deepseek-v4-flash", "nvidia", ""},
		{"anthropic/claude-glm", "z-ai/glm-5.1", "nvidia", "high"},
		{"anthropic/claude-gl", "z-ai/glm-5.1", "nvidia", "high"},
		{"anthropic/claude-minimax", "minimaxai/minimax-m3", "nvidia", "high"},
		{"anthropic/claude-deepseek-v4", "deepseek-ai/deepseek-v4-pro", "nvidia", "high"},
		{"anthropic/claude-mini", "minimaxai/minimax-m3", "nvidia", "high"},
		{"anthropic/claude-deep", "deepseek-ai/deepseek-v4-pro", "nvidia", "high"},
	}

	for _, tc := range testCases {
		route, err := s.routeFor(tc.inputModel)
		if err != nil {
			t.Fatalf("routeFor(%s) failed: %v", tc.inputModel, err)
		}
		if route.Model != tc.expectedModel {
			t.Errorf("routeFor(%s) returned model %q, want %s", tc.inputModel, route.Model, tc.expectedModel)
		}
		if route.Provider != tc.expectedProv {
			t.Errorf("routeFor(%s) returned provider %q, want %s", tc.inputModel, route.Provider, tc.expectedProv)
		}
		if route.ReasoningEffort != tc.expectedEffort {
			t.Errorf("routeFor(%s) returned reasoning_effort %q, want %s", tc.inputModel, route.ReasoningEffort, tc.expectedEffort)
		}
	}
}

func TestExactProviderReasoningEffort(t *testing.T) {
	testCases := []struct {
		provider string
		effort   string
		expected string
		wantErr  bool
	}{
		{"opencode", "ultracode", "", true},
		{"opencode", "max", "max", false},
		{"opencode", "high", "high", false},
		{"nvidia", "ultracode", "", true},
		{"nvidia", "max", "", true},
		{"nvidia", "medium", "medium", false},
		{"gemini", "xhigh", "", true},
		{"random", "ultracode", "ultracode", false},
	}

	for _, tc := range testCases {
		got, err := exactProviderReasoningEffort(tc.provider, tc.effort)
		if (err != nil) != tc.wantErr {
			t.Errorf("exactProviderReasoningEffort(%q, %q) error = %v, wantErr %v", tc.provider, tc.effort, err, tc.wantErr)
			continue
		}
		if got != tc.expected {
			t.Errorf("exactProviderReasoningEffort(%q, %q) = %q, want %q", tc.provider, tc.effort, got, tc.expected)
		}
	}
}

func TestGeminiThoughtSignature(t *testing.T) {
	// Setup an assistant message containing a tool call
	content := `[
		{"type":"tool_use","id":"call_123","name":"run_test","input":{}}
	]`
	ar := &AnthropicRequest{
		Model: "gemini-model",
		Messages: []AnthropicMessage{
			{Role: "assistant", Content: json.RawMessage(content)},
		},
	}

	// If provider is gemini, thought_signature must be injected as "skip_thought_signature_validator"
	or, err := translateRequest(ar, Route{Provider: "gemini", Model: "gemini-model"}, testCfg())
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, m := range or.Messages {
		if m.Role == "assistant" {
			for _, tc := range m.ToolCalls {
				if tc.Function.Name == "run_test" {
					found = true
					if tc.ExtraContent == nil || tc.ExtraContent.Google == nil || tc.ExtraContent.Google.ThoughtSignature != "skip_thought_signature_validator" {
						t.Errorf("expected thought_signature skip_thought_signature_validator in ExtraContent")
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("tool call not found in translated messages")
	}

	// Test that an incoming tool call with __thought__ is successfully split into real ID and thought signature
	contentWithThought := `[
		{"type":"tool_use","id":"call_abc__thought__SIG_999","name":"run_test","input":{}}
	]`
	arWithThought := &AnthropicRequest{
		Model: "gemini-model",
		Messages: []AnthropicMessage{
			{Role: "assistant", Content: json.RawMessage(contentWithThought)},
			{Role: "user", Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"call_abc__thought__SIG_999","content":"success"}]`)},
		},
	}
	or3, err := translateRequest(arWithThought, Route{Provider: "gemini", Model: "gemini-model"}, testCfg())
	if err != nil {
		t.Fatal(err)
	}

	foundThought := false
	for _, m := range or3.Messages {
		switch m.Role {
		case "assistant":
			for _, tc := range m.ToolCalls {
				if tc.Function.Name == "run_test" {
					foundThought = true
					if tc.ID != "call_abc" {
						t.Errorf("expected split ID 'call_abc', got %q", tc.ID)
					}
					if tc.ExtraContent == nil || tc.ExtraContent.Google == nil || tc.ExtraContent.Google.ThoughtSignature != "SIG_999" {
						t.Errorf("expected split ThoughtSignature 'SIG_999' in ExtraContent")
					}
				}
			}
		case "tool":
			if m.ToolCallID != "call_abc" {
				t.Errorf("expected stripped ToolCallID 'call_abc', got %q", m.ToolCallID)
			}
		}
	}
	if !foundThought {
		t.Fatal("tool call with thought not found")
	}
}
