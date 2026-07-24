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

## Codex Desktop contract (experimental / work in progress)

The integration remains incomplete and may break when Codex changes its model
catalog, request shape, or desktop behavior. Preserve the reversible
`acc codex --restore` path and do not describe this surface as production-ready.

- `acc codex` must open the existing `/Applications/ChatGPT.app` directly with
  macOS `open`. Never invoke the bundled
  `/Applications/ChatGPT.app/Contents/Resources/codex app` command: when a
  separate app bundle is absent, that command downloads another 587 MB installer
  and creates `/Applications/Codex.app`.
- When a command needs to start the background gateway, it prefers the sibling
  `acc-proxy` binary. This keeps `acc-stop` and `acc-restart` able to find and
  manage the process; do not regress to launching the `acc` command itself.
- ACC backs up the existing Codex config, writes an ownership-marked direct ACC
  Responses provider, and preserves unrelated projects, MCPs, and preferences.
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

The Codex menu uses deterministic `provider/upstream-model` IDs generated from
enabled ACC capabilities plus authenticated native-provider discovery. The
Claude family aliases `opus`, `sonnet`, and `haiku` stay in the separate Claude
Code registry and never appear in Codex. A direct Codex ID routes exactly to its
named provider/model and gets no implicit Claude alias fallback.

Unsupported efforts return 400 before a provider call. A fallback is used only
through the capability registry's explicit fallback lists, and
the response headers report requested model/effort plus actual provider,
backend model, effort, and fallback state.

Terra advertises the 262K context available through HY3. Its Big Pickle primary
route remains capped at 131K. ACC estimates compacted Responses payloads at a
conservative three bytes per token, respects a smaller client output limit, and
skips routes that cannot safely hold the request. Requests beyond HY3's context
can continue through Gemini's million-token fallback instead of returning an
empty oversized-request error.

## ACC identity

[`persona.go`](persona.go) is the only ACC-owned identity source. Normal
identity is `I'm Kabir's Second Brain.` The current provider/model is included
in the model-visible prompt so it can be disclosed only when explicitly asked.
ACC does not inject the retired route-specific provider imitation prompts.
Codex, project, tool, safety, developer, and user instructions remain separate.
The shared identity core is paired with exactly one client adapter: Codex
Responses requests receive the Codex adapter, while Anthropic Messages requests
receive the Claude Code adapter. Neither client receives the other adapter.

## Plugins, Sites, and scheduled tasks

- Claude Code can load ACC's local MCP bundle from
  `~/.config/acc/mcp.json`. `acc claude` supplies that file through
  `--mcp-config --strict-mcp-config`; it does not rewrite `~/.claude.json`.
  Strict mode prevents legacy global servers with overlapping tools from
  shadowing the ACC bundle. The safe default bundle contains `acc-websearch`
  and `acc-mac-control`. Obsidian is a separate plugin bundle under
  `plugins/obsidian` and is not installed or served by ACC core. Unrestricted
  `acc-osascript` is opt-in through
  `acc mcp install --include-raw-osascript`.
- Claude-3p is a separate desktop client with its own config at
  `~/Library/Application Support/Claude-3p/claude_desktop_config.json`.
  `acc mcp install --claude-3p` merges the ACC servers there, removes only the
  legacy custom entries, preserves unrelated servers and preferences, and
  creates a backup. Add `--include-raw-osascript` when that unrestricted tool
  is explicitly wanted in Claude-3p. Add `--include-obsidian` to register the
  standalone vault-locked Obsidian MCP server; the plugin's skills and binary
  remain separate from ACC core.
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
  items and streaming events. Codex namespace groups are flattened to
  collision-safe function names for upstreams, then restored with their
  original namespace and child name on both unary and streaming responses.
  Unsupported provider-hosted tools, including web
  search, fail before a provider is contacted with the exact backend and tool.
- Sites 0.1.27 is a Codex plugin. Its design picker is an MCP tool and its save/
  deploy operations are a Codex connector. Tool calls can pass through ACC;
  connector authentication and deployment do not pass through the model
  provider. Never claim a Sites deployment was tested unless one was created.
- Scheduled local jobs store model ID and effort, but the current automation
  schema has no custom `model_provider` field. A task using `sonnet`
  therefore does not prove it used ACC. Do not advertise scheduled ACC support
  until Codex persists the custom provider too.

## July 14, 2026 failure

The first desktop attempt used the bundled `codex app` installer and created a
duplicate `/Applications/Codex.app`; it was verified byte-for-byte against the
real app and removed with Kabir's approval. The app showed a blank custom model
menu. Two Opus requests reached ACC, failed over from GLM 5.2 to MiniMax M3, and
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
  on reasoning. It completed simple tools but every repeated multi-tool coding
  workflow failed upstream. Terra therefore puts HY3 immediately behind it.
- NVIDIA GLM-5.2 repeatedly timed out during the July 16 benchmark. It remains
  unassigned.
- `tencent/hy3:free` completed all 27 curated runs at OpenRouter reasoning
  effort `high`, including 20K, 50K, and 80K contexts. The normalized Codex
  effort exposed for all three public models is `max`.
- Full evidence and rerun commands live in
  [`benchmarks/model-routing`](benchmarks/model-routing/README.md).

## Verification

```bash
make test
curl -sS http://localhost:9999/health
tail -n 10 /Users/kabir/acc/test_runs.jsonl
```

For a live model check, send a tiny `/v1/responses` request and verify the
`X-ACC-*` response headers plus the final JSONL row. Use a temporary Codex home
for catalog parser tests. Do not change global Codex settings just to test.

## Native Codex lifecycle and auth

The supported path is `Codex -> ACC loopback -> exact provider/model`.
`acc codex setup/start/stop/status/doctor/restore/remove` do not execute or
configure OpenCodex. Process ownership is recorded only when `start` launches
ACC, so `stop` refuses to kill an unrelated process. Config changes are atomic,
timestamp-backed up, ownership-marked, and exactly restorable.

Native Kimi and xAI credentials live in macOS Keychain and refresh lazily with
single-flight locking, rotation, expiry skew, and one replay after a 401.
Anthropic API keys remain the stable path. Existing official Claude/Grok
credentials are read only after an explicit import command; normal startup does
not inspect or migrate them.
