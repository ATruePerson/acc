package main

import (
	"encoding/json"
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
	or, err := translateRequest(ar, Route{Model: "glm-4.6", Vision: true}, testCfg())
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

func TestRouteSystemPrependOverridesGlobal(t *testing.T) {
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
	if !strings.Contains(sys, "I am Claude Fable 5.") {
		t.Fatalf("route prepend missing from system: %s", sys)
	}
	if strings.Contains(sys, "GLOBAL") {
		t.Fatalf("global prepend should be overridden, got: %s", sys)
	}
	if !strings.Contains(sys, "base") {
		t.Fatalf("original system text dropped: %s", sys)
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
	msgs, err := translateMessage(AnthropicMessage{Role: "user", Content: json.RawMessage(content)}, false)
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
	msgs, err := translateMessage(AnthropicMessage{Role: "user", Content: json.RawMessage(content)}, false)
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

func TestImageFailsForTextOnlyRoute(t *testing.T) {
	content := `[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]`
	_, err := translateMessage(AnthropicMessage{Role: "user", Content: json.RawMessage(content)}, false)
	if err == nil {
		t.Fatal("expected error when an image is sent to a text-only route, got nil")
	}
	if !strings.Contains(err.Error(), "text-only") {
		t.Fatalf("expected text-only error, got %v", err)
	}
}

func TestImageKeptForVisionRoute(t *testing.T) {
	content := `[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]`
	msgs, err := translateMessage(AnthropicMessage{Role: "user", Content: json.RawMessage(content)}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(msgs[0].Content), "image_url") {
		t.Fatalf("vision route should keep image_url, got %s", msgs[0].Content)
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
		{"anthropic/claude-kim-2", "moonshotai/kimi-k2.6", "nvidia", "high"},
		{"anthropic/claude_K_2", "moonshotai/kimi-k2.6", "nvidia", "high"},
		{"anthropic/claude-kimi", "moonshotai/kimi-k2.6", "nvidia", "high"},
		{"anthropic/claude-kim", "moonshotai/kimi-k2.6", "nvidia", "high"},
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

func TestSanitizeReasoningEffort(t *testing.T) {
	testCases := []struct {
		provider string
		effort   string
		expected string
	}{
		{"opencode", "ultracode", "max"},
		{"opencode", "max", "max"},
		{"opencode", "high", "high"},
		{"nvidia", "ultracode", "high"},
		{"nvidia", "max", "high"},
		{"nvidia", "medium", "medium"},
		{"gemini", "xhigh", "high"},
		{"random", "ultracode", "ultracode"}, // unknown provider gets returned as is
	}

	for _, tc := range testCases {
		got := sanitizeReasoningEffort(tc.provider, tc.effort)
		if got != tc.expected {
			t.Errorf("sanitizeReasoningEffort(%q, %q) = %q, want %q", tc.provider, tc.effort, got, tc.expected)
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
		if m.Role == "assistant" {
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
		} else if m.Role == "tool" {
			if m.ToolCallID != "call_abc" {
				t.Errorf("expected stripped ToolCallID 'call_abc', got %q", m.ToolCallID)
			}
		}
	}
	if !foundThought {
		t.Fatal("tool call with thought not found")
	}
}

