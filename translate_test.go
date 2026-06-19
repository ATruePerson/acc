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

func TestRouteFor(t *testing.T) {
	s := &server{
		cfg: &Config{
			Providers: map[string]Provider{
				"nvidia":   {BaseURL: "https://integrate.api.nvidia.com/v1", APIKey: "fake"},
				"opencode": {BaseURL: "https://opencode.ai/zen/v1", APIKey: "fake"},
			},
		},
	}

	testCases := []struct {
		inputModel     string
		expectedModel  string
		expectedProv   string
		expectedEffort string
	}{
		{"anthropic/claude_step_3.7_flash", "stepfun-ai/step-3.7-flash", "nvidia", "max"},
		{"anthropic/stepfun-ai/step-3.7-flash", "stepfun-ai/step-3.7-flash", "nvidia", "max"},
		{"anthropic/stepfun_ai_step_3.7_flash", "stepfun-ai/step-3.7-flash", "nvidia", "max"},
		{"stepfun-ai/step-3.7-flash", "stepfun-ai/step-3.7-flash", "nvidia", "max"},
		{"stepfun_ai_step_3.7_flash", "stepfun-ai/step-3.7-flash", "nvidia", "max"},

		// Manual overrides tests
		{"anthropic/opencode/big-pickle", "big-pickle", "opencode", "high"},
		{"claude-mimo-v2.5-free", "mimo-v2.5-free", "opencode", "high"},
		{"anthropic/opencode/mimo-v2.5-free", "mimo-v2.5-free", "opencode", "high"},
		{"anthropic/claude_M_2.6", "mimo-v2.5-free", "opencode", "high"},
		{"anthropic/claude-kim-2", "moonshotai/kimi-k2.6", "nvidia", "high"},
		{"anthropic/claude_K_2", "moonshotai/kimi-k2.6", "nvidia", "high"},
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
