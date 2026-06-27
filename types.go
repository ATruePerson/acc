package main

import "encoding/json"

// ---------- Config ----------

type Config struct {
	Port      int                  `json:"port"`
	Providers map[string]Provider  `json:"providers"`
	Routes    map[string]Route     `json:"routes"`
	Effort    map[string]EffortMap `json:"effort"`
	// Aliases maps a friendly model ID to a concrete route. These overlay the
	// built-in catalog (see modelCatalog), so adding or overriding a route is a
	// config edit + restart, not a recompile.
	Aliases map[string]Route `json:"aliases,omitempty"`
	// Pricing maps an upstream model name to its USD price per 1M tokens, used
	// to estimate per-request cost in the metrics log. Omit or zero for free
	// providers.
	Pricing map[string]ModelPrice `json:"pricing,omitempty"`
	// SystemPrepend is prepended to every system prompt — use it to force
	// behavior the upstream model otherwise ignores (e.g. respond in English).
	SystemPrepend string `json:"system_prepend"`
	// VisionRoute, when set, is the model that image-bearing requests are
	// rerouted to when the chosen model is text-only. Defaults to
	// gemini-2.5-flash when omitted.
	VisionRoute *Route `json:"vision_route,omitempty"`
}

type ModelPrice struct {
	InputPer1M  float64 `json:"input_per_1m"`
	OutputPer1M float64 `json:"output_per_1m"`
}

type Provider struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

type Route struct {
	Provider        string   `json:"provider"`
	Model           string   `json:"model"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxTokens       int      `json:"max_tokens,omitempty"`
	// SystemPrepend, when set, overrides the global Config.SystemPrepend for
	// this route/alias only — lets each model carry its own identity/behavior
	// prompt (e.g. the real Claude system prompt per tier).
	SystemPrepend string `json:"system_prepend,omitempty"`
	// Vision marks a route whose model accepts image_url content blocks. When
	// false (the default), image blocks are dropped and replaced with a text
	// placeholder so a text-only upstream (e.g. DeepSeek) doesn't 400 on a
	// pasted screenshot.
	Vision bool `json:"vision,omitempty"`
	// Fallbacks is an ordered list of routes to try when this route returns 429
	// (rate limited). The proxy tries each in sequence and stops after the first
	// success or after the last fallback fails.
	Fallbacks []Route `json:"fallbacks,omitempty"`
}

type EffortMap struct {
	Budget    int    `json:"budget"`
	Reasoning string `json:"reasoning"`
}

// ---------- Anthropic request (front) ----------

type AnthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      json.RawMessage    `json:"system,omitempty"` // string OR []block
	Messages    []AnthropicMessage `json:"messages"`
	Stream      bool               `json:"stream"`
	Tools       []AnthropicTool    `json:"tools,omitempty"`
	Thinking    *Thinking          `json:"thinking,omitempty"`
	Temperature *float64           `json:"temperature,omitempty"`
}

type Thinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type AnthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string OR []block
}

type AnthropicBlock struct {
	Type string `json:"type"`
	// text
	Text string `json:"text,omitempty"`
	// image
	Source *ImageSource `json:"source,omitempty"`
	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type AnthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ---------- OpenAI request (back) ----------

type OpenAIRequest struct {
	Model           string          `json:"model"`
	MaxTokens       int             `json:"max_tokens,omitempty"`
	Messages        []OpenAIMessage `json:"messages"`
	Stream          bool            `json:"stream"`
	Tools           []OpenAITool    `json:"tools,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	StreamOptions   *StreamOptions  `json:"stream_options,omitempty"`
}

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type OpenAIMessage struct {
	Role       string           `json:"role"`
	Content    json.RawMessage  `json:"content,omitempty"` // string OR []part
	ToolCalls  []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type OpenAIContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *OpenAIImageURL `json:"image_url,omitempty"`
}

type OpenAIImageURL struct {
	URL string `json:"url"`
}

type OpenAITool struct {
	Type     string         `json:"type"`
	Function OpenAIFunction `json:"function"`
}

type OpenAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type OpenAIToolCall struct {
	Index    int            `json:"index,omitempty"`
	ID       string         `json:"id,omitempty"`
	Type     string         `json:"type,omitempty"`
	Function OpenAIFuncCall `json:"function"`
}

type OpenAIFuncCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ---------- OpenAI response (back) ----------

type OpenAIResponse struct {
	Choices []OpenAIChoice `json:"choices"`
	Usage   *OpenAIUsage   `json:"usage,omitempty"`
}

type OpenAIChoice struct {
	Delta        *OpenAIMessage `json:"delta,omitempty"`
	Message      *OpenAIMessage `json:"message,omitempty"`
	FinishReason string         `json:"finish_reason,omitempty"`
}

type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// ---------- Responses API (front) ----------

type ResponsesRequest struct {
	Model       string              `json:"model"`
	Input       json.RawMessage     `json:"input"` // string OR []ResponsesItem
	Stream      bool                `json:"stream"`
	Tools       []ResponsesTool     `json:"tools,omitempty"`
	Reasoning   *ResponsesReasoning `json:"reasoning,omitempty"`
	Temperature *float64            `json:"temperature,omitempty"`
	MaxTokens   int                 `json:"max_tokens,omitempty"`
}

type ResponsesReasoning struct {
	Effort string `json:"effort,omitempty"`
}

type ResponsesTool struct {
	Type     string            `json:"type"`
	Function ResponsesFunction `json:"function"`
}

type ResponsesFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ResponsesItem struct {
	ID        string          `json:"id,omitempty"`
	Type      string          `json:"type"` // "message", "function_call", "function_call_output"
	Role      string          `json:"role,omitempty"` // for message
	Content   json.RawMessage `json:"content,omitempty"` // string OR []part
	Name      string          `json:"name,omitempty"` // for function_call
	Arguments string          `json:"arguments,omitempty"` // for function_call
	CallID    string          `json:"call_id,omitempty"` // for function_call_output
	Output    string          `json:"output,omitempty"` // for function_call_output
}

type ResponsesResponse struct {
	ID        string          `json:"id"`
	CreatedAt int64           `json:"created_at"`
	Model     string          `json:"model"`
	Output    []ResponsesItem `json:"output"`
	Usage     *OpenAIUsage    `json:"usage,omitempty"`
}
