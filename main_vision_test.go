package main

import (
	"encoding/json"
	"testing"
)

// testServer builds a server with a hot-swappable config preloaded, since
// server.cfg is an atomic.Pointer and can't be set via a struct literal.
func testServer(c *Config) *server {
	s := &server{}
	s.cfg.Store(c)
	return s
}

func TestRequestHasImage(t *testing.T) {
	imgBlock := `[{"type":"text","text":"what is this"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"abc"}}]`
	textBlock := `[{"type":"text","text":"hello"}]`

	cases := []struct {
		name string
		msgs []AnthropicMessage
		want bool
	}{
		{"image block", []AnthropicMessage{{Role: "user", Content: json.RawMessage(imgBlock)}}, true},
		{"text blocks only", []AnthropicMessage{{Role: "user", Content: json.RawMessage(textBlock)}}, false},
		{"plain string", []AnthropicMessage{{Role: "user", Content: json.RawMessage(`"just text"`)}}, false},
		{"image in later msg", []AnthropicMessage{
			{Role: "user", Content: json.RawMessage(textBlock)},
			{Role: "user", Content: json.RawMessage(imgBlock)},
		}, true},
	}
	for _, c := range cases {
		ar := &AnthropicRequest{Messages: c.msgs}
		if got := requestHasImage(ar); got != c.want {
			t.Errorf("%s: requestHasImage=%v want %v", c.name, got, c.want)
		}
	}
}

func TestVisionReroutePreservesIdentity(t *testing.T) {
	s := testServer(&Config{})
	orig := Route{Provider: "nvidia", Model: "z-ai/glm-5.1", ReasoningEffort: "high", SystemPrepend: "You are Opus 4.8"}
	got := s.visionReroute(orig)

	if got.Provider != "gemini" || got.Model != "models/gemini-3.5-flash" {
		t.Errorf("reroute backend = %s/%s, want gemini/models/gemini-3.5-flash", got.Provider, got.Model)
	}
	if !got.Vision {
		t.Error("rerouted route should be vision-capable")
	}
	if got.SystemPrepend != "You are Opus 4.8" || got.ReasoningEffort != "high" {
		t.Error("reroute must preserve identity prompt + effort")
	}
}

func TestVisionRerouteHonorsConfig(t *testing.T) {
	s := testServer(&Config{VisionRoute: &Route{Provider: "gemini", Model: "gemini-2.5-pro"}})
	got := s.visionReroute(Route{Provider: "nvidia", Model: "z-ai/glm-5.1"})
	if got.Model != "gemini-2.5-pro" {
		t.Errorf("config vision_route ignored: got %s", got.Model)
	}
}

func TestInferVision(t *testing.T) {
	cases := []struct {
		provider, model string
		want            bool
	}{
		{"gemini", "gemini-2.5-flash", true},
		{"gemini", "gemini-2.5-flash-lite", true},
		{"nvidia", "moonshotai/kimi-k2.6", true},
		{"nvidia", "minimaxai/minimax-m3", true},
		{"nvidia", "deepseek-ai/deepseek-v4-flash", false},
		{"nvidia", "z-ai/glm-5.1", true},
		{"opencode", "big-pickle", false},
	}
	for _, c := range cases {
		if got := inferVision(c.provider, c.model); got != c.want {
			t.Errorf("inferVision(%q,%q)=%v want %v", c.provider, c.model, got, c.want)
		}
	}
}
