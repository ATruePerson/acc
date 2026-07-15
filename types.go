package main

import "encoding/json"

// ---------- Config ----------

type Config struct {
	Port      int                 `json:"port"`
	Providers map[string]Provider `json:"providers"`
	Routes    map[string]Route    `json:"routes"`
	// Models is the user-visible capability registry. The map key is the stable
	// model ID Codex sends on every request.
	Models map[string]ModelCapability `json:"models,omitempty"`
	Effort map[string]EffortMap       `json:"effort"`
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
	TopP            *float64 `json:"top_p,omitempty"`
	MaxTokens       int      `json:"max_tokens,omitempty"`
	Stream          *bool    `json:"stream,omitempty"`
	// SystemPrepend is accepted only so old config files still load. ACC clears
	// it during config loading; route-specific identity prompts are retired.
	SystemPrepend string `json:"system_prepend,omitempty"`
	// ExtraBody is a map of arbitrary JSON fields to merge into the outgoing
	// OpenAI-compatible request body.
	ExtraBody map[string]any `json:"extra_body,omitempty"`
	// Reasoning maps a Codex effort name to the exact provider request value.
	// An empty target intentionally omits reasoning_effort (Minimal).
	Reasoning map[string]ReasoningTarget `json:"reasoning,omitempty"`
	// Fallbacks is an ordered list of routes to try when this route returns 429
	// (rate limited). The proxy tries each in sequence and stops after the first
	// success or after the last fallback fails.
	Toolcalling *bool   `json:"toolcalling,omitempty"`
	Fallbacks   []Route `json:"fallbacks,omitempty"`
}

type ModelCapability struct {
	DisplayName string `json:"display_name"`
	// Route references Config.Routes. Provider+Model can instead define a direct
	// selectable model without duplicating a named family route.
	Route    string `json:"route,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`

	Reasoning map[string]ReasoningTarget `json:"reasoning,omitempty"`

	ToolCallSupport   bool   `json:"tool_call_support"`
	StreamingSupport  bool   `json:"streaming_support"`
	ImageInputSupport bool   `json:"image_input_support"`
	FileInputSupport  bool   `json:"file_input_support"`
	MaxContext        int    `json:"max_context"`
	MaxOutput         int    `json:"max_output"`
	Enabled           bool   `json:"enabled"`
	FallbackModel     string `json:"fallback_model,omitempty"`
}

type ReasoningTarget struct {
	Effort    string         `json:"effort,omitempty"`
	ExtraBody map[string]any `json:"extra_body,omitempty"`
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
	TopP        *float64           `json:"top_p,omitempty"`
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
	Model             string          `json:"model"`
	MaxTokens         int             `json:"max_tokens,omitempty"`
	Messages          []OpenAIMessage `json:"messages"`
	Stream            bool            `json:"stream"`
	Tools             []OpenAITool    `json:"tools,omitempty"`
	ReasoningEffort   string          `json:"reasoning_effort,omitempty"`
	Temperature       *float64        `json:"temperature,omitempty"`
	TopP              *float64        `json:"top_p,omitempty"`
	StreamOptions     *StreamOptions  `json:"stream_options,omitempty"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls,omitempty"`
	ToolChoice        json.RawMessage `json:"tool_choice,omitempty"`
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
	Detail   string          `json:"detail,omitempty"`
	File     *OpenAIFile     `json:"file,omitempty"`
}

type OpenAIImageURL struct {
	URL string `json:"url"`
}

type OpenAIFile struct {
	FileID   string `json:"file_id,omitempty"`
	FileData string `json:"file_data,omitempty"`
	Filename string `json:"filename,omitempty"`
}

type OpenAITool struct {
	Type     string         `json:"type"`
	Function OpenAIFunction `json:"function"`
}

type OpenAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      *bool           `json:"strict,omitempty"`
}

type OpenAIToolCall struct {
	Index        int                 `json:"index,omitempty"`
	ID           string              `json:"id,omitempty"`
	Type         string              `json:"type,omitempty"`
	Function     OpenAIFuncCall      `json:"function"`
	ExtraContent *OpenAIExtraContent `json:"extra_content,omitempty"`
}

type OpenAIExtraContent struct {
	Google *OpenAIGoogleExtra `json:"google,omitempty"`
}

type OpenAIGoogleExtra struct {
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

type OpenAIFuncCall struct {
	Name             string `json:"name,omitempty"`
	Arguments        string `json:"arguments,omitempty"`
	ThoughtSignature string `json:"thought_signature,omitempty"`
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
	PromptTokens            int `json:"prompt_tokens"`
	CompletionTokens        int `json:"completion_tokens"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details,omitempty"`
}

// reasoningTokens returns the reasoning_tokens count if the upstream reported
// it (OpenAI-style nested detail), else 0.
func (u *OpenAIUsage) reasoningTokens() int {
	if u != nil && u.CompletionTokensDetails != nil {
		return u.CompletionTokensDetails.ReasoningTokens
	}
	return 0
}

// ---------- Responses API (front) ----------

type ResponsesRequest struct {
	Model             string              `json:"model"`
	Instructions      string              `json:"instructions,omitempty"`
	Input             json.RawMessage     `json:"input"` // string OR []ResponsesItem
	Stream            bool                `json:"stream"`
	Tools             []ResponsesTool     `json:"tools,omitempty"`
	Reasoning         *ResponsesReasoning `json:"reasoning,omitempty"`
	Temperature       *float64            `json:"temperature,omitempty"`
	TopP              *float64            `json:"top_p,omitempty"`
	MaxTokens         int                 `json:"max_tokens,omitempty"`
	MaxOutputTokens   int                 `json:"max_output_tokens,omitempty"`
	ParallelToolCalls *bool               `json:"parallel_tool_calls,omitempty"`
	ToolChoice        json.RawMessage     `json:"tool_choice,omitempty"`
}

type ResponsesReasoning struct {
	Effort string `json:"effort,omitempty"`
}

type ResponsesTool struct {
	Type        string            `json:"type"`
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Parameters  json.RawMessage   `json:"parameters,omitempty"`
	Strict      *bool             `json:"strict,omitempty"`
	Function    ResponsesFunction `json:"function,omitempty"`
	// Format is used by Responses custom tools to constrain their raw string
	// input. It is intentionally raw so new format shapes survive ACC.
	Format json.RawMessage `json:"format,omitempty"`
	// Extra and Raw retain forward-compatible fields that Chat Completions
	// cannot represent directly. The custom-tool bridge keeps them alongside
	// the original definition instead of discarding them.
	Extra map[string]json.RawMessage `json:"-"`
	Raw   json.RawMessage            `json:"-"`
}

type ResponsesFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ResponsesItem struct {
	ID        string                     `json:"id,omitempty"`
	Type      string                     `json:"type"` // message, function/custom call, function/custom call output
	Status    string                     `json:"status,omitempty"`
	Role      string                     `json:"role,omitempty"`      // for message
	Content   json.RawMessage            `json:"content,omitempty"`   // string OR []part
	Name      string                     `json:"name,omitempty"`      // for function/custom call
	Arguments string                     `json:"arguments,omitempty"` // for function_call
	Input     string                     `json:"input,omitempty"`     // for custom_tool_call
	CallID    string                     `json:"call_id,omitempty"`   // for calls and outputs
	Output    json.RawMessage            `json:"output,omitempty"`    // string OR structured tool output
	Extra     map[string]json.RawMessage `json:"-"`
	Raw       json.RawMessage            `json:"-"`
}

type ResponsesResponse struct {
	ID        string          `json:"id"`
	Object    string          `json:"object"`
	CreatedAt int64           `json:"created_at"`
	Status    string          `json:"status"`
	Model     string          `json:"model"`
	Output    []ResponsesItem `json:"output"`
	Usage     *ResponsesUsage `json:"usage,omitempty"`
}

type ResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

func (t *ResponsesTool) UnmarshalJSON(data []byte) error {
	type plain ResponsesTool
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"type", "name", "description", "parameters", "strict", "function", "format"} {
		delete(fields, key)
	}
	decoded.Raw = append(decoded.Raw[:0], data...)
	if len(fields) > 0 {
		decoded.Extra = fields
	}
	*t = ResponsesTool(decoded)
	return nil
}

func (i *ResponsesItem) UnmarshalJSON(data []byte) error {
	type plain ResponsesItem
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"id", "type", "status", "role", "content", "name", "arguments", "input", "call_id", "output"} {
		delete(fields, key)
	}
	decoded.Raw = append(decoded.Raw[:0], data...)
	if len(fields) > 0 {
		decoded.Extra = fields
	}
	*i = ResponsesItem(decoded)
	return nil
}
