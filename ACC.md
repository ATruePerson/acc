# ACC Working Context

This is the durable handoff for ACC-specific work that is easy to forget between
tasks. `AGENTS.md` remains the main repository guide.

## Live paths

- Source: `/Users/kabir/acc`
- Commands: `/Users/kabir/.local/bin/acc` and `/Users/kabir/.local/bin/acc-proxy`
- Runtime config: `/Users/kabir/.config/acc/config.json`
- Secrets: `/Users/kabir/.config/acc/.env`
- Codex config: `/Users/kabir/.codex/config.toml`
- Request metrics: `/Users/kabir/acc/test_runs.jsonl`

## Codex Desktop contract

- `acc codex` must open the existing `/Applications/ChatGPT.app` directly with
  macOS `open`. Never invoke the bundled
  `/Applications/ChatGPT.app/Contents/Resources/codex app` command: when a
  separate app bundle is absent, that command downloads another 587 MB installer
  and creates `/Applications/Codex.app`.
- When a command needs to start the background gateway, it prefers the sibling
  `acc-proxy` binary. This keeps `acc-stop` and `acc-restart` able to find and
  manage the process; do not regress to launching the `acc` command itself.
- ACC backs up the existing Codex config once, writes the ACC provider and Sol
  as the new-task default, closes the running app, then reopens ChatGPT at the
  requested workspace. It never prompts in the terminal; Codex owns per-task
  model selection.
- Codex 0.144.2 enables hosted web search by default even when a model catalog
  does not advertise it. ACC therefore writes `web_search = "disabled"` only
  while its provider is active; `acc codex --restore` restores the exact prior
  setting.
- `acc codex --restore` restores the original subscription config and removes the
  generated catalog and backup state.
- ACC writes a Codex-compatible `model_catalog_json` generated from
  `config.json.models`. The installed Codex CLI must parse this file before a
  release is considered verified.
- Codex stores the chosen stable model ID and reasoning effort in the task. ACC
  resolves both values on every request. Never read a global "active model" to
  rewrite another task's request.

## Codex model registry

`config.json.models` is the source of truth for the Codex menu. Every enabled
entry has a stable request ID, display name, concrete route, capabilities,
exact effort mappings, limits, and an optional explicit fallback model.

| Codex name | Stable ID | Current primary | Explicit fallback | Efforts exposed |
| --- | --- | --- | --- | --- |
| Sol | `gpt-5.6-sol` | NVIDIA `z-ai/glm-5.2` | `acc-minimax-m3` | minimal, low, medium, high |
| Terra | `gpt-5.6-terra` | OpenCode `big-pickle` | `acc-nemotron-super` | minimal, low, medium, high, xhigh, max |
| Luna | `gpt-5.6-luna` | NVIDIA `stepfun-ai/step-3.7-flash` | none | minimal |

Sol is one logical Codex entry. Text starts on GLM-5.2 and uses MiniMax M3 only
as its explicitly configured fallback. Image or mixed text-image input skips
text-only GLM-5.2 and starts directly on MiniMax M3. This is a capability
reroute, not an invisible effort downgrade: MiniMax currently exposes only
Minimal, so an image request at a higher effort returns a clear error.
MiniMax M3 also rejects function calls, so ACC excludes it from tool-request
fallback chains rather than dropping Codex tools.

The direct fallback models are also selectable. Unsupported efforts return 400
before a provider call. A fallback is used only through `fallback_model`, and
the response headers report requested model/effort plus actual provider,
backend model, effort, and fallback state.

## ACC identity

[`persona.go`](persona.go) is the only ACC-owned identity source. Normal
identity is `I'm Kabir's Second Brain.` The current provider/model is included
in the model-visible prompt so it can be disclosed only when explicitly asked.
ACC does not inject the retired route-specific provider imitation prompts.
Codex, project, tool, safety, developer, and user instructions remain separate.

## Plugins, Sites, and scheduled tasks

- Claude Code can load ACC's local MCP bundle from
  `~/.config/acc/mcp.json`. `acc claude` supplies that file through
  `--mcp-config`; it does not rewrite `~/.claude.json`. The safe default bundle
  contains `acc-websearch` and `acc-mac-control`. Unrestricted
  `acc-osascript` is opt-in through
  `acc mcp install --include-raw-osascript`.
- `acc-mac-control` addresses Notes by nested `folderPath`, supports folder
  `Instructions` notes, and returns recent note IDs with counts 1, 3, or 7.
  Write tools preserve existing note HTML where possible and replace/delete
  require explicit confirmation. Apps opened by a call are closed afterward;
  pre-existing apps are left running.
- Plugins and MCP servers are controlled by Codex. ACC preserves function tool
  definitions, strict schemas, tool choice, parallel calls, call IDs, results,
  multi-turn loops, images, files, and Responses streaming events. Codex's
  free-form `custom` tools are translated through a one-string function wrapper
  for Chat Completions upstreams, then restored as native `custom_tool_call`
  items and streaming events. Unsupported provider-hosted tools, including web
  search, fail before a provider is contacted with the exact backend and tool.
- Sites 0.1.27 is a Codex plugin. Its design picker is an MCP tool and its save/
  deploy operations are a Codex connector. Tool calls can pass through ACC;
  connector authentication and deployment do not pass through the model
  provider. Never claim a Sites deployment was tested unless one was created.
- Scheduled local jobs store model ID and effort, but the current automation
  schema has no custom `model_provider` field. A task using `gpt-5.6-terra`
  therefore does not prove it used ACC. Do not advertise scheduled ACC support
  until Codex persists the custom provider too.

## July 14, 2026 failure

The first desktop attempt used the bundled `codex app` installer and created a
duplicate `/Applications/Codex.app`; it was verified byte-for-byte against the
real app and removed with Kabir's approval. The app showed a blank custom model
menu. Two Sol requests reached ACC, failed over from GLM 5.2 to MiniMax M3, and
ended with HTTP 504 because the fallback emitted no response before the
first-token timeout. A later live check showed GLM could also stall before
returning HTTP headers, which bypassed the first-token guard; ACC now applies the
same timeout while waiting for headers so fallback can begin.

The blank picker and timeout were separate problems. The old catalog used
slash-prefixed IDs and a static three-model table. Routing also read the globally
configured Codex model and could overwrite a task's request. The capability
registry and per-request stable IDs replace that path.

## Provider details that bite

- NVIDIA reasoning models are text-only unless a live capability check proves
  otherwise. MiniMax M3 is the current NVIDIA vision exception.
- MiniMax M3 rejects `reasoning_budget`; let it reason natively.
- `big-pickle` is a reasoning model and can spend a small output limit entirely
  on reasoning. Do not mistake empty text at tiny limits for a transport failure.
- NVIDIA GLM-5.2 produced no response for 120 seconds during an `xhigh` probe on
  2026-07-14. Do not expose Sol Extra High until a live probe succeeds.
- Terra `xhigh` and `max` both returned 200 through OpenCode on 2026-07-14.

## Verification

```bash
make test
curl -sS http://localhost:9999/health
tail -n 10 /Users/kabir/acc/test_runs.jsonl
```

For a live model check, send a tiny `/v1/responses` request and verify the
`X-ACC-*` response headers plus the final JSONL row. Use a temporary Codex home
for catalog parser tests. Do not change global Codex settings just to test.
