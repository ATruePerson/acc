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
			"nvidia":   {BaseURL: "https://nvidia.test", APIKey: "nvidia-key"},
			"opencode": {BaseURL: "https://opencode.test", APIKey: "opencode-key"},
		},
		Routes: map[string]Route{
			"opus":   {Provider: "nvidia", Model: "z-ai/glm-5.2", Toolcalling: boolPtr(true)},
			"sonnet": {Provider: "opencode", Model: "big-pickle", Toolcalling: boolPtr(true)},
			"haiku":  {Provider: "nvidia", Model: "stepfun-ai/step-3.7-flash", Toolcalling: boolPtr(true)},
		},
		Models: map[string]ModelCapability{
			"gpt-5.6-sol": {
				DisplayName: "GPT-5.6 Sol", Route: "opus", Enabled: true,
				Reasoning: map[string]ReasoningTarget{
					"minimal": {}, "low": {Effort: "low"}, "medium": {Effort: "medium"},
					"high": {Effort: "high"}, "xhigh": {Effort: "xhigh"},
				},
				ToolCallSupport: true, StreamingSupport: true, ImageInputSupport: true,
				MaxContext: 131072, MaxOutput: 131072,
			},
			"gpt-5.6-terra": {
				DisplayName: "GPT-5.6 Terra", Route: "sonnet", Enabled: true,
				Reasoning: map[string]ReasoningTarget{
					"minimal": {}, "low": {Effort: "low"}, "medium": {Effort: "medium"},
					"high": {Effort: "high"}, "xhigh": {Effort: "xhigh"}, "max": {Effort: "max"},
				},
				ToolCallSupport: true, StreamingSupport: true, FileInputSupport: true,
				MaxContext: 131072, MaxOutput: 48000,
			},
			"gpt-5.6-luna": {
				DisplayName: "GPT-5.6 Luna", Route: "haiku", Enabled: true,
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

func TestACCPersonaHasOneIdentityAndTruthfulBackendDisclosure(t *testing.T) {
	prompt := accPersona("nvidia", "z-ai/glm-5.2")
	for _, want := range []string{
		"Your identity is Kabir's Second Brain.",
		`Normal identity answer: “I’m Kabir’s Second Brain.”`,
		`This task is currently being powered by nvidia/z-ai/glm-5.2.`,
		"Only disclose the backend when Kabir explicitly asks",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("persona missing %q:\n%s", want, prompt)
		}
	}
	for _, forbidden := range []string{"You are Claude", "You are Codex", "You are ChatGPT"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("persona contains forbidden identity %q", forbidden)
		}
	}
}

func TestCodexCatalogComesFromEnabledCapabilityRegistry(t *testing.T) {
	var catalog struct {
		Models []struct {
			Slug             string `json:"slug"`
			DisplayName      string `json:"display_name"`
			BaseInstructions string `json:"base_instructions"`
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
	if catalog.Models[0].Slug != "gpt-5.6-luna" || catalog.Models[1].Slug != "gpt-5.6-sol" || catalog.Models[2].Slug != "gpt-5.6-terra" {
		t.Fatalf("catalog slugs are not stable and sorted: %+v", catalog.Models)
	}
	if !strings.Contains(catalog.Models[1].BaseInstructions, "Kabir's Second Brain") || strings.Contains(catalog.Models[1].BaseInstructions, "You are Codex") {
		t.Fatalf("catalog has the wrong identity: %q", catalog.Models[1].BaseInstructions)
	}
	if catalog.Models[1].Context != 131072 {
		t.Fatalf("context window = %d, want 131072", catalog.Models[1].Context)
	}
	var sol, terra []string
	for _, model := range catalog.Models {
		for _, level := range model.Levels {
			if model.Slug == "gpt-5.6-sol" {
				sol = append(sol, level.Effort)
			}
			if model.Slug == "gpt-5.6-terra" {
				terra = append(terra, level.Effort)
			}
		}
	}
	if strings.Contains(strings.Join(sol, ","), "max") {
		t.Fatalf("Sol exposes unsupported Max: %v", sol)
	}
	if !strings.Contains(strings.Join(terra, ","), "max") {
		t.Fatalf("Terra should expose provider-supported Max: %v", terra)
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
		`{"model":"gpt-5.6-sol","instructions":"project rules","input":"first","reasoning":{"effort":"xhigh"}}`,
		`{"model":"gpt-5.6-terra","instructions":"project rules","input":"second","reasoning":{"effort":"max"}}`,
		`{"model":"gpt-5.6-sol","instructions":"project rules","input":"third","reasoning":{"effort":"minimal"}}`,
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
		body := `{"model":"gpt-5.6-terra","input":"test","reasoning":{"effort":"` + effort + `"}}`
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
		if actual != effort {
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
	request := `{"model":"gpt-5.6-luna","input":"hello","reasoning":{"effort":"max"}}`
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
		Model:             "gpt-5.6-terra",
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
	if err == nil || !strings.Contains(err.Error(), `unsupported tool type "mcp"`) {
		t.Fatalf("got error %v, want explicit unsupported tool error", err)
	}
}

func TestResponsesMCPToolLoopEndToEnd(t *testing.T) {
	cfg := codexTestConfig()
	s := testServer(cfg)
	calls := 0
	s.http = &http.Client{Transport: &mockTripper{fn: func(req *http.Request) (*http.Response, error) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "big-pickle" {
			t.Fatalf("tool loop backend = %v, want big-pickle", body["model"])
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
		"model":"gpt-5.6-terra",
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
		"model":"gpt-5.6-terra",
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
		"model":"gpt-5.6-terra",
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
}

func TestFallbackIsReportedInHeadersAndUsesFallbackPersona(t *testing.T) {
	cfg := codexTestConfig()
	cfg.Models["gpt-5.6-sol"] = ModelCapability{
		DisplayName: "GPT-5.6 Sol", Route: "opus", Enabled: true,
		FallbackModel: "gpt-5.6-terra",
		Reasoning:     map[string]ReasoningTarget{"high": {Effort: "high"}},
	}
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
		return chatSuccess("fallback ok"), nil
	}}}

	w := httptest.NewRecorder()
	request := `{"model":"gpt-5.6-sol","input":"hello","reasoning":{"effort":"high"}}`
	s.handleResponses(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(request)))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-ACC-Fallback") != "true" || w.Header().Get("X-ACC-Backend-Provider") != "opencode" || w.Header().Get("X-ACC-Backend-Model") != "big-pickle" || w.Header().Get("X-ACC-Backend-Effort") != "high" {
		t.Fatalf("fallback headers missing: %+v", w.Header())
	}
}

func chatSuccess(text string) *http.Response {
	body, _ := json.Marshal(OpenAIResponse{Choices: []OpenAIChoice{{Message: &OpenAIMessage{Role: "assistant", Content: jsonString(text)}}}})
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}
}
