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
- ACC backs up the existing Codex config once, writes the ACC provider and 5.6 Sol
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
| 5.6 Sol | `gpt-5.6-sol` | OpenRouter `tencent/hy3:free` | Nemotron Ultra; Gemini 3.5 Flash for images | max |
| 5.6 Terra | `gpt-5.6-terra` | OpenCode `big-pickle` | HY3, Gemini 3.5 Flash, then Nemotron Super | max |
| 5.6 Luna | `gpt-5.6-luna` | Google `gemini-3.5-flash` | Nemotron Super; MiniMax for image-only fallback | max |

Only those three models appear in the Codex picker, in Sol/Terra/Luna order.
Old task IDs (`opus`, `sonnet`, `haiku`, and `openai/codex-5.6-*`) remain
accepted but are not advertised. Claude family aliases remain separate:
Opus maps to Sol, Sonnet to Terra, and Haiku to Luna. All tool requests retain
their tools on fallback. Terra is text-only; Sol and Luna advertise images
because each has an explicit image route.

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

## Optional OpenCodex layer

OpenCodex is an optional compatibility proxy in front of ACC. It owns the
Codex wire compatibility and model catalog injection; ACC remains the provider
router for Claude routes, free-model selection, fallbacks, rate limits, and
credentials.

```text
Codex -> OpenCodex 127.0.0.1:10100 -> ACC 127.0.0.1:9999 -> configured provider
```

The current stable package is installed with:

```bash
npm install -g @bitkyc08/opencodex
```

From this repository, use the wrapper rather than `ocx init`:

```bash
scripts/opencodex/acc-opencodex setup
scripts/opencodex/acc-opencodex start
scripts/opencodex/acc-opencodex status
scripts/opencodex/acc-opencodex doctor
scripts/opencodex/acc-opencodex restore
scripts/opencodex/acc-opencodex remove
```

`setup` queries the running ACC `/v1/models` endpoint and uses the exact
returned IDs. It creates an OpenCodex provider named `acc` with the documented
`openai-chat` adapter and ACC's `/v1` base URL. Because OpenCodex blocks private
destinations by default, the generated provider explicitly allows this known
loopback destination. It writes no provider key.
Before writing local state it makes timestamped backups. The generated config
sets `hostname` to `127.0.0.1`, disables OpenCodex history remapping, and does
not install an autostart shim.

The wrapper rejects an ACC config whose upstream points at OpenCodex. ACC must
continue to point at NVIDIA, OpenRouter, OpenCode, Gemini, or another real
provider. Both services are local and unauthenticated, so never expose either
port to the LAN or internet.

`doctor` reports deterministic checks as `PASS`, `FAIL`, or `SKIPPED`. A PASS
for health or model discovery does not claim that streaming, tools, fallback,
vision, or a real Codex run succeeded. Those checks require a harmless live
smoke test and are reported separately in
[`docs/opencodex-integration-test-report.md`](docs/opencodex-integration-test-report.md).
