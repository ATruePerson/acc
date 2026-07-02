# AGENTS.md — acc-proxy

`acc-proxy` is a high-performance Go gateway that intercepts Anthropic SDK requests (like Codex) and translates them into OpenAI-compatible requests, routing to cheaper or specialized upstreams (NVIDIA NIM, Gemini, OpenRouter, OpenCode).

> ZAI (`api.z.ai`) was removed 2026-06-28 — it is paid (error 1113 insufficient balance). `z-ai/glm-5.1` on the NVIDIA provider is a different, free thing.

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

### 4. Thinking-budget knobs differ per provider (probed live 2026-06-28)

A route's `extra_body` is **flat-merged to the top level** of the outgoing request (`main.go` ~L260: `merged[k] = v`). The right shape depends on the upstream:

| Provider | Config shape | Result after flat-merge |
| :--- | :--- | :--- |
| NVIDIA reasoning (nemotron-ultra/super, deepseek-pro/flash, glm-5.1) | `"extra_body": { "chat_template_kwargs": {"enable_thinking": true}, "reasoning_budget": N }` | top-level keys — accepted. NVIDIA **400s on an `extra_body` wrapper**, so they must end up top-level. |
| Gemini (3.1-pro, flash-lite) | `"extra_body": { "extra_body": { "google": { "thinking_config": { "thinking_budget": N } } } }` | DOUBLE-wrapped — proxy emits top-level `extra_body:{google:...}`, the only shape Gemini accepts. Top-level `google` or `reasoning_budget` both 400. |
| **minimax-m3** | **none** | 400s `Unsupported parameter: reasoning_budget` — reasons natively, never add a budget. |

### 5. Model traits that bite

- **nemotron-ultra (550B) is slow.** `reasoning_budget` 32000 hangs 6+ min on a trivial prompt; at 8000 it answers in ~99s. Still the slowest tier — 550B is heavy regardless. Only a model swap fixes speed.
- **Gemini 3.x multi-turn tools work now** — the proxy injects `skip_thought_signature_validator` (`translate.go`/`stream.go`/`responses_handler.go`). The old "never use 3.x for tools" rule is obsolete. Verified live: 2-turn tool round-trip on gemini-3.1-pro.
- **Vision:** set `"vision": true` only on Gemini + minimax (vision-capable). NVIDIA reasoning models (nemotron/deepseek/glm) are text-only — forcing `vision:true` makes them answer images blind; leave it off so image requests auto-reroute via `vision_route`.
- **big-pickle** (opencode) is a codename for `deepseek-v4-flash` — a reasoning model that returns EMPTY content if `max_tokens` is too low (spends it all in `reasoning_content`).

### 6. Routing: `routes` vs `aliases`

- `aliases` (`anthropic/Codex-*`) — exact-match, the explicit persona models.
- `routes` (`opus`/`sonnet`/`haiku`) — substring fallback; this is what real Codex model strings (`Codex-opus-4-x`) hit. Keep them mirrored to the matching alias (model, params, budget, vision, fallbacks, `system_prepend`).
- Config is hot-reloaded per request (no restart needed for config-only edits); Go source changes need a rebuild.

## Dev cheat sheet

```bash
make test    # full suite with race detector
make cover   # tests + coverage
tail -f /Users/kabir/acc/test_runs.jsonl   # watch live logs
```
