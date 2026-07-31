package claude

import (
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// PersonaRuntimeCodex selects the Codex-specific persona adapter section.
const PersonaRuntimeCodex = personaRuntimeCodex

// PersonaRuntimeClaudeCode selects the Claude Code persona adapter section.
const PersonaRuntimeClaudeCode = personaRuntimeClaudeCode

// AccPersonaForRuntime assembles ACC's marked persona for a backend and runtime.
func AccPersonaForRuntime(provider, model string, runtime personaRuntime) string {
	return accPersonaForRuntime(provider, model, runtime)
}

// SetPersonaFilePath points ACC persona loading at an editable markdown file.
func SetPersonaFilePath(path string) { setPersonaFilePath(path) }

// ResolvePersonaFile returns the on-disk persona path when present.
func ResolvePersonaFile(baseDir string) string { return resolvePersonaFile(baseDir) }

// ResolveSystemPrepend loads a route system_prepend value from disk when needed.
func ResolveSystemPrepend(baseDir, value string) (string, error) {
	return resolveSystemPrepend(baseDir, value)
}

// LoadPrependFile reads a prepend file relative to baseDir.
func LoadPrependFile(baseDir, path string) (string, error) { return loadPrependFile(baseDir, path) }

// TranslateRequest converts an Anthropic request into OpenAI chat completions format.
func TranslateRequest(ar *AnthropicRequest, route Route, cfg *Config) (*OpenAIRequest, error) {
	return translateRequest(ar, route, cfg)
}

// RequestWithACCPersona injects ACC persona into an OpenAI request unless the route overrides it.
func RequestWithACCPersona(base *OpenAIRequest, route Route) (*OpenAIRequest, error) {
	return requestWithACCPersona(base, route, personaRuntimeClaudeCode)
}

// RequestWithACCPersonaRuntime injects ACC persona for an explicit runtime adapter.
func RequestWithACCPersonaRuntime(base *OpenAIRequest, route Route, runtime personaRuntime) (*OpenAIRequest, error) {
	return requestWithACCPersona(base, route, runtime)
}

// ExactProviderReasoningEffort maps a Codex effort name to a provider-specific value.
func ExactProviderReasoningEffort(provider, effort string) (string, error) {
	return exactProviderReasoningEffort(provider, effort)
}

// AwaitFirstByte blocks until the first byte is readable from src or d passes.
func AwaitFirstByte(src io.Reader, d time.Duration) (io.Reader, bool, error) {
	return awaitFirstByte(src, d)
}

// StreamTranslate proxies an upstream SSE stream into Anthropic SSE events.
func StreamTranslate(w http.ResponseWriter, body interface{ Read([]byte) (int, error) }, model string) (int, int, int) {
	return streamTranslate(w, body, model)
}

// TranslateResponse converts a non-streaming OpenAI response into Anthropic format.
func TranslateResponse(or *OpenAIResponse, model string) map[string]any {
	return translateResponse(or, model)
}

// JSONString marshals a string into json.RawMessage.
func JSONString(s string) json.RawMessage { return jsonString(s) }

// DecodeStringContent extracts plain text from a string-or-blocks content field.
func DecodeStringContent(raw json.RawMessage) string { return decodeStringContent(raw) }

// TranslateMessage converts one Anthropic message into OpenAI messages.
func TranslateMessage(m AnthropicMessage) ([]OpenAIMessage, error) {
	return translateMessage(m)
}

// BucketForBudget maps an Anthropic thinking budget to a reasoning effort name.
func BucketForBudget(budget int, cfg *Config) string { return bucketForBudget(budget, cfg) }

// ChatJSONWithACCPersona injects ACC persona into a raw chat-completions JSON body.
func ChatJSONWithACCPersona(raw []byte, route Route) ([]byte, error) {
	return chatJSONWithACCPersona(raw, route)
}

// CostFor estimates USD cost for a model invocation from configured pricing.
func CostFor(model string, in, out int, cfg *Config) float64 {
	return costFor(model, in, out, cfg)
}

// RunBench executes the full persona benchmark matrix.
func RunBench() { cmdBench() }
