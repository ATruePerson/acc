# Gemini Project Context: acc-proxy

`acc-proxy` is a high-performance Go-based gateway that intercepts Anthropic SDK requests (such as Claude Code) and translates them into OpenAI-compatible requests, routing them to alternative, cheaper, or specialized upstream backends (e.g. NVIDIA NIM, Gemini, OpenRouter, OpenCode, ZAI).

---

## Architecture & Project Files

The codebase is modular and structured as follows:

| File | Core Responsibility | Key Functions / Types |
| :--- | :--- | :--- |
| [`main.go`](file:///Users/kabir/acc/main.go) | HTTP Server, routers, model listings, and command lifecycle. | `handleMessages`, `handleModels`, `routeFor` |
| [`translate.go`](file:///Users/kabir/acc/translate.go) | Protocol translation (messages, tools, images). | `translateRequest`, `translateMessage`, `translateResponse`, `bucketForBudget` |
| [`stream.go`](file:///Users/kabir/acc/stream.go) | Real-time SSE translator for streaming requests. | `streamTranslate` (extracts usage from final chunks) |
| [`tui.go`](file:///Users/kabir/acc/tui.go) | Live terminal dashboard rendering and persistent logger. | `AddTUILog` (writes to `test_runs.jsonl`), `drawDashboard` |
| [`types.go`](file:///Users/kabir/acc/types.go) | Shared configuration, requests, and response schemas. | `Config`, `AnthropicRequest`, `OpenAIRequest`, `OpenAIUsage` |
| [`dashboard.go`](file:///Users/kabir/acc/dashboard.go) | Web dashboard HTML and auxiliary JSON API endpoints. | `handleDashboard`, `handleDashboardLogs` |

---

## Active Environment & Path Map

When deployed on the host system, the proxy interacts with several global and local files:

*   **Active Binary**: `/Users/kabir/.local/bin/acc-proxy`
*   **Active Config**: `/Users/kabir/.config/acc/config.json`
*   **API Keys & Env**: `/Users/kabir/.config/acc/.env`
*   **Log Output**: `/Users/kabir/.config/acc/proxy.log`
*   **Persistent Runs Log**: `/Users/kabir/acc/test_runs.jsonl`

### Management Commands
*   **Start**: `/Users/kabir/.local/bin/acc-start` (launches background daemon)
*   **Stop**: `/Users/kabir/.local/bin/acc-stop` (kills active proxy processes)
*   **Restart**: `/Users/kabir/.local/bin/acc-restart` (stops, sleeps, and restarts)

---

## Key Protocols & Features

### 1. Token Tracking & Metrics
We have enhanced the streaming SSE translator to extract `PromptTokens` and `CompletionTokens` in real-time from the final SSE chunk payloads (when `include_usage: true` is passed upstream). 
All requests (both streaming and unary) write a persistent metric log line to `/Users/kabir/acc/test_runs.jsonl` in the following format:
```json
{"timestamp":"2026-06-21T13:57:40+05:30","model":"anthropic/claude_K_2","route":"moonshotai/kimi-k2.6","status":200,"tokens_in":36,"tokens_out":765,"budget":16000,"effort":"high"}
```

### 2. Effort & Reasoning Mapping
Anthropic requests containing a `thinking` block are dynamically mapped to OpenAI's `reasoning_effort` using closest-budget matching:
```go
func bucketForBudget(budget int, cfg *Config) string {
	best := "low"
	bestBudget := -1
	for _, e := range cfg.Effort {
		if e.Budget <= budget && e.Budget > bestBudget {
			bestBudget = e.Budget
			best = e.Reasoning
		}
	}
	return best
}
```

> [!TIP]
> Standard OpenAI reasoning effort supports `"low"`, `"medium"`, and `"high"`. If a custom mapping (like `"max"` or `"ultracode"`) is passed to an upstream model that doesn't support it, ensure the provider maps or ignores it safely.

### 3. Tool Message Sequence Ordering
In Anthropic's protocol, a message can contain both `tool_result` blocks and `text` blocks. In OpenAI's API, any message with `role: "tool"` must immediately follow the assistant message containing the matching `tool_calls`.

If an Anthropic message containing both `tool_result` and `text` blocks is translated by prepending the user text message before the tool messages, OpenAI/DeepSeek backends will reject the request with a 400 error (e.g., "An assistant message with 'tool_calls' must be followed by tool messages responding to each 'tool_call_id'").

To ensure compatibility:
* All translated tool messages (with `role: "tool"`) are placed first in the translated slice.
* The user text/image message is appended last in the slice.

---

## Development Cheat Sheet

### Running Tests
```bash
make test       # Runs the entire test suite with race detector
make cover      # Runs tests and shows code coverage
```

### Watching Active Logs Live
```bash
tail -f /Users/kabir/acc/test_runs.jsonl
```
