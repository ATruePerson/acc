package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponsesNamespaceToolBridgesAndRestores(t *testing.T) {
	req := &ResponsesRequest{
		Tools: []ResponsesTool{{
			Type: "namespace", Name: "obsidian", Description: "Work with Kabir's vault",
			Tools: []ResponsesTool{{
				Type: "function", Name: "read_note", Description: "Read one note",
				Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
			}},
		}},
	}
	translation, tools, err := translateResponseTools(req, Route{Provider: "openrouter", Model: "tencent/hy3:free"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Function.Name == "read_note" || tools[0].Function.Name == "" {
		t.Fatalf("namespace tool was not flattened to a collision-safe function: %+v", tools)
	}

	upstream := &OpenAIResponse{Choices: []OpenAIChoice{{Message: &OpenAIMessage{
		Role: "assistant",
		ToolCalls: []OpenAIToolCall{{
			ID: "call_1", Type: "function",
			Function:     OpenAIFuncCall{Name: tools[0].Function.Name, Arguments: `{"path":"Dreams.md"}`},
			ExtraContent: &OpenAIExtraContent{Google: &OpenAIGoogleExtra{ThoughtSignature: "sig_1"}},
		}},
	}}}}
	got, err := translateToResponsesWithTools(upstream, "nvidia/z-ai/glm-5.2", translation)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Output) != 1 || got.Output[0].Type != "function_call" || got.Output[0].Namespace != "obsidian" || got.Output[0].Name != "read_note" || got.Output[0].CallID != "call_1__thought__sig_1" {
		t.Fatalf("namespace call was not restored: %+v", got.Output)
	}
}

func TestResponsesNamespaceContinuationUsesSameBridgeName(t *testing.T) {
	req := &ResponsesRequest{
		Input: json.RawMessage(`[
			{"type":"function_call","call_id":"call_2","namespace":"obsidian","name":"read_note","arguments":"{\"path\":\"Dreams.md\"}"},
			{"type":"function_call_output","call_id":"call_2","output":"note body"}
		]`),
		Tools: []ResponsesTool{{
			Type: "namespace", Name: "obsidian",
			Tools: []ResponsesTool{{Type: "function", Name: "read_note", Parameters: json.RawMessage(`{"type":"object"}`)}},
		}},
	}
	chat, translation, err := translateFromResponsesWithTools(req, Route{Provider: "openrouter", Model: "tencent/hy3:free"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	definition, ok := translation.namespaceForQualifiedName("obsidian", "read_note")
	if !ok {
		t.Fatal("namespace mapping was not retained")
	}
	if len(chat.Messages) != 2 || len(chat.Messages[0].ToolCalls) != 1 || chat.Messages[0].ToolCalls[0].Function.Name != definition.BridgeName {
		t.Fatalf("continuation did not reuse namespace bridge: %+v", chat.Messages)
	}
	if chat.Messages[1].Role != "tool" || chat.Messages[1].ToolCallID != "call_2" {
		t.Fatalf("namespace result was not paired with its call: %+v", chat.Messages)
	}
}

func TestResponsesNamespaceBridgeAvoidsChildNameCollisions(t *testing.T) {
	req := &ResponsesRequest{Tools: []ResponsesTool{
		{Type: "namespace", Name: "obsidian", Tools: []ResponsesTool{{Type: "function", Name: "search", Parameters: json.RawMessage(`{"type":"object"}`)}}},
		{Type: "namespace", Name: "gmail", Tools: []ResponsesTool{{Type: "function", Name: "search", Parameters: json.RawMessage(`{"type":"object"}`)}}},
	}}
	_, tools, err := translateResponseTools(req, Route{Provider: "openrouter", Model: "tencent/hy3:free"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].Function.Name == tools[1].Function.Name {
		t.Fatalf("same child name in two namespaces collided: %+v", tools)
	}
}

func TestResponsesNamespaceStreamingRestoresNamespace(t *testing.T) {
	req := &ResponsesRequest{Tools: []ResponsesTool{{
		Type: "namespace", Name: "obsidian",
		Tools: []ResponsesTool{{Type: "function", Name: "read_note", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}}}
	translation, tools, err := translateResponseTools(req, Route{Provider: "openrouter", Model: "tencent/hy3:free"})
	if err != nil {
		t.Fatal(err)
	}
	stream := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_stream","type":"function","function":{"name":"` + tools[0].Function.Name + `","arguments":"{\"path\":"}}]}}]}` + "\n\n" +
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Dreams.md\"}"}}]}}]}` + "\n\n" +
		"data: [DONE]\n\n"
	w := httptest.NewRecorder()
	streamTranslateResponses(w, strings.NewReader(stream), "nvidia/z-ai/glm-5.2", translation)
	for _, want := range []string{`"namespace":"obsidian"`, `"name":"read_note"`, `"arguments":"{\"path\":\"Dreams.md\"}"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("namespace stream is missing %s:\n%s", want, w.Body.String())
		}
	}
}
