package main

import "encoding/json"

// ---------- Config ----------

type Config struct {
	Port      int                 `json:"port"`
	Providers map[string]Provider `json:"providers"`
	Routes    map[string]Route    `json:"routes"`
	// AliasRoutes owns the legacy Opus/Sonnet/Haiku routes. It is deliberately
	// separate from Routes because the Codex capability registry also references
	// Routes and must not be changed by legacy alias configuration.
	AliasRoutes map[string]Route `json:"alias_routes,omitempty"`
	// Models is the user-visible capability registry. The map key is the stable
	// model ID Codex sends on every request.
	Models map[string]ModelCapability `json:"models,omitempty"`
	Effort map[string]EffortMap       `json:"effort"`
	// Aliases keeps custom friendly IDs backward-compatible. The three family
	// aliases are owned by AliasRoutes instead of duplicating full route objects.
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
	Adapter string `json:"adapter,omitempty"`
}

type Route struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// ReasoningLocked keeps a route's exact provider-specific effort from being
	// replaced by a legacy Anthropic thinking-budget bucket.
	ReasoningLocked bool     `json:"reasoning_locked,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"top_p,omitempty"`
	MaxTokens       int      `json:"max_tokens,omitempty"`
	// MaxContext is the real context limit of this concrete route. It can be
	// smaller than the public model when a larger-context fallback is available.
	MaxContext int   `json:"max_context,omitempty"`
	Stream     *bool `json:"stream,omitempty"`
	// SystemPrepend is accepted only so old config files still load. ACC clears
	// it during config loading; route-specific identity prompts are retired.
	SystemPrepend string `json:"system_prepend,omitempty"`
	// ExtraBody is a map of arbitrary JSON fields to merge into the outgoing
	// OpenAI-compatible request body.
	ExtraBody map[string]any `json:"extra_body,omitempty"`
	// Reasoning maps a Codex effort name to the exact provider request value.
	// An empty target intentionally omits reasoning_effort (Minimal).
	Reasoning map[string]ReasoningTarget `json:"reasoning,omitempty"`
	// Toolcalling indicates whether the route supports tool calls.
	Toolcalling *bool `json:"toolcalling,omitempty"`
	// Fallbacks remains deserializable for old benchmark fixtures; live routing
	// does not traverse it.
	Fallbacks []Route `json:"fallbacks,omitempty"`
}

type ModelCapability struct {
	DisplayName string `json:"display_name"`
	Description string `json:"description,omitempty"`
	// CatalogPriority controls the model picker order. Lower values appear first.
	CatalogPriority int `json:"catalog_priority,omitempty"`
	// CatalogVisible defaults to true. Benchmark and fallback-only candidates
	// remain directly routable when false without cluttering the client menu.
	CatalogVisible *bool `json:"catalog_visible,omitempty"`
	// Route references Config.Routes. Provider+Model can instead define a direct
	// selectable model without duplicating a named family route.
	Route    string `json:"route,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`

	Reasoning map[string]ReasoningTarget `json:"reasoning,omitempty"`

	ToolCallSupport     bool     `json:"tool_call_support"`
	StreamingSupport    bool     `json:"streaming_support"`
	ImageInputSupport   bool     `json:"image_input_support"`
	FileInputSupport    bool     `json:"file_input_support"`
	MaxContext          int      `json:"max_context"`
	MaxOutput           int      `json:"max_output"`
	Enabled             bool     `json:"enabled"`
	FallbackModel       string   `json:"fallback_model,omitempty"`
	FallbackModels      []string `json:"fallback_models,omitempty"`
	ImageModel          string   `json:"image_model,omitempty"`
	ImageFallbackModels []string `json:"image_fallback_models,omitempty"`
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
	// thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
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
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content,omitempty"` // string OR []part
	ReasoningContent json.RawMessage `json:"reasoning_content,omitempty"`
	// Reasoning is emitted by some OpenAI-compatible providers instead of
	// reasoning_content. Keep it on the wire adapter so it can be replayed.
	Reasoning  json.RawMessage  `json:"reasoning,omitempty"`
	Refusal    string           `json:"refusal,omitempty"`
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
	Model              string                     `json:"model"`
	Instructions       string                     `json:"instructions,omitempty"`
	Input              json.RawMessage            `json:"input"` // string OR []ResponsesItem
	PreviousResponseID string                     `json:"previous_response_id,omitempty"`
	Store              *bool                      `json:"store,omitempty"`
	Truncation         string                     `json:"truncation,omitempty"`
	Metadata           map[string]string          `json:"metadata,omitempty"`
	User               string                     `json:"user,omitempty"`
	Stream             bool                       `json:"stream"`
	Tools              []ResponsesTool            `json:"tools,omitempty"`
	Reasoning          *ResponsesReasoning        `json:"reasoning,omitempty"`
	Temperature        *float64                   `json:"temperature,omitempty"`
	TopP               *float64                   `json:"top_p,omitempty"`
	MaxTokens          int                        `json:"max_tokens,omitempty"`
	MaxOutputTokens    int                        `json:"max_output_tokens,omitempty"`
	ParallelToolCalls  *bool                      `json:"parallel_tool_calls,omitempty"`
	ToolChoice         json.RawMessage            `json:"tool_choice,omitempty"`
	Extra              map[string]json.RawMessage `json:"-"`
	Raw                json.RawMessage            `json:"-"`
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
	// Tools contains the function tools grouped under a Codex namespace.
	Tools []ResponsesTool `json:"tools,omitempty"`
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
	Namespace string                     `json:"namespace,omitempty"` // for namespaced function calls
	Arguments string                     `json:"arguments,omitempty"` // for function_call
	Input     string                     `json:"input,omitempty"`     // for custom_tool_call
	CallID    string                     `json:"call_id,omitempty"`   // for calls and outputs
	Output    json.RawMessage            `json:"output,omitempty"`    // string OR structured tool output
	Summary   []ResponsesSummary         `json:"summary,omitempty"`   // reasoning summary parts
	Extra     map[string]json.RawMessage `json:"-"`
	Raw       json.RawMessage            `json:"-"`
}

type ResponsesSummary struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type ResponsesResponse struct {
	ID                 string                     `json:"id"`
	Object             string                     `json:"object"`
	CreatedAt          int64                      `json:"created_at"`
	Status             string                     `json:"status"`
	Model              string                     `json:"model"`
	PreviousResponseID string                     `json:"previous_response_id,omitempty"`
	Output             []ResponsesItem            `json:"output"`
	Usage              *ResponsesUsage            `json:"usage,omitempty"`
	Error              *ResponsesError            `json:"error,omitempty"`
	IncompleteDetails  map[string]any             `json:"incomplete_details,omitempty"`
	Extra              map[string]json.RawMessage `json:"-"`
	Raw                json.RawMessage            `json:"-"`
}

type ResponsesError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Type    string `json:"type,omitempty"`
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
	for _, key := range []string{"type", "name", "description", "parameters", "strict", "function", "tools", "format"} {
		delete(fields, key)
	}
	decoded.Raw = append(decoded.Raw[:0], data...)
	if len(fields) > 0 {
		decoded.Extra = fields
	}
	*t = ResponsesTool(decoded)
	return nil
}

func (r *ResponsesRequest) UnmarshalJSON(data []byte) error {
	type plain ResponsesRequest
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"model", "instructions", "input", "previous_response_id", "store", "truncation", "metadata", "user", "stream", "tools", "reasoning", "temperature", "top_p", "max_tokens", "max_output_tokens", "parallel_tool_calls", "tool_choice"} {
		delete(fields, key)
	}
	decoded.Raw = append(decoded.Raw[:0], data...)
	if len(fields) > 0 {
		decoded.Extra = fields
	}
	*r = ResponsesRequest(decoded)
	return nil
}

func (r ResponsesRequest) MarshalJSON() ([]byte, error) {
	type plain ResponsesRequest
	b, err := json.Marshal(plain(r))
	if err != nil {
		return nil, err
	}
	return mergeJSONFields(b, r.Extra)
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
	for _, key := range []string{"id", "type", "status", "role", "content", "name", "namespace", "arguments", "input", "call_id", "output", "summary"} {
		delete(fields, key)
	}
	decoded.Raw = append(decoded.Raw[:0], data...)
	if len(fields) > 0 {
		decoded.Extra = fields
	}
	*i = ResponsesItem(decoded)
	return nil
}

func (i ResponsesItem) MarshalJSON() ([]byte, error) {
	type plain ResponsesItem
	b, err := json.Marshal(plain(i))
	if err != nil {
		return nil, err
	}
	return mergeJSONFields(b, i.Extra)
}

func (r *ResponsesResponse) UnmarshalJSON(data []byte) error {
	type plain ResponsesResponse
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"id", "object", "created_at", "status", "model", "previous_response_id", "output", "usage", "error", "incomplete_details"} {
		delete(fields, key)
	}
	decoded.Raw = append(decoded.Raw[:0], data...)
	if len(fields) > 0 {
		decoded.Extra = fields
	}
	*r = ResponsesResponse(decoded)
	return nil
}

func (r ResponsesResponse) MarshalJSON() ([]byte, error) {
	type plain ResponsesResponse
	b, err := json.Marshal(plain(r))
	if err != nil {
		return nil, err
	}
	return mergeJSONFields(b, r.Extra)
}

func mergeJSONFields(base []byte, extra map[string]json.RawMessage) ([]byte, error) {
	if len(extra) == 0 {
		return base, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(base, &fields); err != nil {
		return nil, err
	}
	for key, value := range extra {
		if _, exists := fields[key]; !exists {
			fields[key] = value
		}
	}
	return json.Marshal(fields)
}
