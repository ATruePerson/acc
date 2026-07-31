package claude

import "net/http"

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

// StreamTranslate proxies an upstream SSE stream into Anthropic SSE events.
func StreamTranslate(w http.ResponseWriter, body interface{ Read([]byte) (int, error) }, model string) (int, int, int) {
	return streamTranslate(w, body, model)
}

// TranslateResponse converts a non-streaming OpenAI response into Anthropic format.
func TranslateResponse(or *OpenAIResponse, model string) map[string]any {
	return translateResponse(or, model)
}

// RunBench executes the full persona benchmark matrix.
func RunBench() { cmdBench() }
