# CLAUDE.md — acc-proxy

`acc-proxy` is a high-performance Go gateway that intercepts Anthropic SDK requests (like Claude Code) and translates them into OpenAI-compatible requests, routing to cheaper or specialized upstreams (NVIDIA NIM, Gemini, OpenRouter, OpenCode, ZAI).

## Architecture

| File | Responsibility | Key functions / types |
| :--- | :--- | :--- |
| `main.go` | HTTP server, routers, model listings, command lifecycle | `handleMessages`, `handleModels`, `routeFor` |
| `translate.go` | Protocol translation (messages, tools, images) | `translateRequest`, `translateMessage`, `translateResponse`, `bucketForBudget` |
| `stream.go` | Real-time SSE translator for streaming requests | `streamTranslate` (extracts usage from final chunks) |
| `tui.go` | Live terminal dashboard + persistent logger | `AddTUILog` (writes `test_runs.jsonl`), `drawDashboard` |
| `types.go` | Shared config, request, response schemas | `Config`, `AnthropicRequest`, `OpenAIRequest`, `OpenAIUsage` |
| `dashboard.go` | Web dashboard HTML + JSON API endpoints | `handleDashboard`, `handleDashboardLogs` |

## Active environment & paths

- **Binary**: `/Users/kabir/.local/bin/acc-proxy`
- **Config**: `/Users/kabir/.config/acc/config.json`
- **API keys / env**: `/Users/kabir/.config/acc/.env`
- **Proxy log**: `/Users/kabir/.config/acc/proxy.log`
- **Persistent runs log**: `/Users/kabir/acc/test_runs.jsonl`

### Management commands
- **Start**: `acc-start` (background daemon)
- **Stop**: `acc-stop` (kills proxy processes)
- **Restart**: `acc-restart` (stop, sleep, restart)

## Key protocols & features

### 1. Token tracking & metrics
The streaming SSE translator extracts `PromptTokens` and `CompletionTokens` in real-time from the final SSE chunk (when `include_usage: true` is passed upstream). All requests — streaming and unary — write a metric line to `test_runs.jsonl`:
```json
{"timestamp":"2026-06-21T13:57:40+05:30","model":"anthropic/claude_K_2","route":"moonshotai/kimi-k2.6","status":200,"tokens_in":36,"tokens_out":765,"budget":16000,"effort":"high"}
```

### 2. Effort & reasoning mapping
Anthropic requests with a `thinking` block map to OpenAI's `reasoning_effort` via closest-budget matching (`bucketForBudget`). OpenAI supports `low`/`medium`/`high`. Custom mappings (`max`, `ultracode`) must be mapped or safely ignored by upstreams that don't support them.

### 3. Tool message sequence ordering
Anthropic messages can hold both `tool_result` and `text` blocks. OpenAI requires any `role: "tool"` message to immediately follow the assistant message with matching `tool_calls`. Prepending user text before tool messages causes a 400 (`An assistant message with 'tool_calls' must be followed by tool messages...`).

Fix: translated `role: "tool"` messages go first in the slice; user text/image message is appended last.

## Dev cheat sheet

```bash
make test    # full suite with race detector
make cover   # tests + coverage
tail -f /Users/kabir/acc/test_runs.jsonl   # watch live logs
```
