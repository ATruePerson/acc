package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponsesRequestPreservesUnknownFields(t *testing.T) {
	var req ResponsesRequest
	if err := json.Unmarshal([]byte(`{"model":"nvidia/z-ai~sglm-5.2","input":"hello","future_control":{"mode":"keep"}}`), &req); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"future_control":{"mode":"keep"}`) {
		t.Fatalf("unknown request field was dropped: %s", b)
	}
}

func TestResponseToolOutputNormalizesNativeTextPartsForChatProviders(t *testing.T) {
	output := json.RawMessage(`[{"type":"input_text","text":"tool failed"},{"type":"output_text","text":"details"}]`)
	content := responseToolOutputContent(output)
	if got := decodeStringContent(content); got != "tool failed\ndetails" {
		t.Fatalf("normalized tool output = %q (%s)", got, content)
	}
	if strings.Contains(string(content), "input_text") {
		t.Fatalf("native Responses part leaked into Chat content: %s", content)
	}
}

func TestPreviousResponseIDExpandsLocalConversation(t *testing.T) {
	s := testServer(codexTestConfig())
	priorContent := json.RawMessage(`[{"type":"output_text","text":"prior answer","annotations":[]}]`)
	s.rememberResponse(&ResponsesResponse{
		ID: "resp_local",
		Output: []ResponsesItem{{
			Type: "message", Role: "assistant", Status: "completed", Content: priorContent,
		}},
	})
	req := &ResponsesRequest{
		Model:              "nvidia/z-ai~sglm-5.2",
		PreviousResponseID: "resp_local",
		Input:              json.RawMessage(`[{"type":"message","role":"user","content":"continue"}]`),
	}
	if err := s.applyPreviousResponse(req); err != nil {
		t.Fatal(err)
	}
	or, _, err := translateFromResponsesWithTools(req, Route{Provider: "nvidia", Model: "z-ai/glm-5.2"}, codexTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(or.Messages) != 2 || or.Messages[0].Role != "assistant" || or.Messages[1].Role != "user" {
		t.Fatalf("continuation messages = %+v, want prior assistant and current user", or.Messages)
	}
	if got := decodeStringContent(or.Messages[0].Content); got != "prior answer" {
		t.Fatalf("prior assistant content = %q", got)
	}
}

func TestResponsesReasoningSurvivesTranslation(t *testing.T) {
	upstream := &OpenAIResponse{Choices: []OpenAIChoice{{Message: &OpenAIMessage{
		Role:             "assistant",
		ReasoningContent: jsonString("short plan"),
		Content:          jsonString("answer"),
	}}}}
	resp := translateToResponses(upstream, "nvidia/z-ai~sglm-5.2")
	if len(resp.Output) != 2 || resp.Output[0].Type != "reasoning" || resp.Output[1].Type != "message" {
		t.Fatalf("translated output = %+v, want reasoning then message", resp.Output)
	}
	if len(resp.Output[0].Summary) != 1 || resp.Output[0].Summary[0].Text != "short plan" {
		t.Fatalf("reasoning summary = %+v", resp.Output[0].Summary)
	}
}

func TestResponsesStreamWithoutDoneIsIncomplete(t *testing.T) {
	stream := `data: {"choices":[{"delta":{"content":"partial"}}]}` + "\n\n"
	w := httptest.NewRecorder()
	var completed *ResponsesResponse
	streamTranslateResponsesWithCompletion(w, strings.NewReader(stream), "nvidia/z-ai~sglm-5.2", func(response *ResponsesResponse) {
		completed = response
	})
	body := w.Body.String()
	if !strings.Contains(body, "event: response.incomplete") || strings.Contains(body, "event: response.completed") {
		t.Fatalf("stream terminal event = %s", body)
	}
	if completed == nil || completed.Status != "incomplete" {
		t.Fatalf("completion state = %+v, want incomplete", completed)
	}
}
