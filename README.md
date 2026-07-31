# acc

[![CI](https://github.com/ATruePerson/acc/actions/workflows/ci.yml/badge.svg)](https://github.com/ATruePerson/acc/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/ATruePerson/acc.svg)](https://pkg.go.dev/github.com/ATruePerson/acc)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

A local gateway for Claude Code and experimental Codex Desktop integration. It
accepts Anthropic Messages and OpenAI Responses requests, translates them to OpenAI-compatible chat
completions, then routes them to the provider and model you choose.

Use it to keep your normal client while routing to NVIDIA NIM, Gemini,
OpenRouter, OpenCode, or another OpenAI-compatible provider.

## Quick start

```bash
# Install the latest release. No Go toolchain needed.
curl -fsSL https://raw.githubusercontent.com/ATruePerson/acc/main/scripts/install.sh | sh

# Save provider keys and create the split config under ~/.config/acc/.
acc setup

# Start Claude Code through ACC.
acc claude

# Or try the experimental Codex Desktop integration.
acc codex
```

`acc setup` stores keys in `~/.config/acc/.env` and creates a config file if
one does not exist. `acc claude` starts the gateway when needed and launches
Claude Code with the right connection. `acc codex` creates or preserves a
durable raw and sanitized subscription baseline, switches it to ACC, and
reopens the existing ChatGPT desktop app.

## Commands

| Command | What it does |
| --- | --- |
| `acc setup` | Save provider keys and create the first config file. |
| `acc doctor` | Check whether configured provider keys work. |
| `acc models` | List built-in model aliases and your config aliases. |
| `acc bench` | Benchmark configured personas and fallbacks. |
| `acc claude [args]` | Start ACC and launch Claude Code through it. |
| `acc codex setup` | Back up Codex and point it directly at ACC. |
| `acc codex start` | Start an owned ACC process and verify Responses readiness. |
| `acc codex status` | Show safe config, catalog, process, and provider status. |
| `acc codex doctor` | Run non-destructive direct-integration checks. |
| `acc codex restore` | Restore the durable sanitized subscription baseline. |
| `acc codex remove` | Remove active ACC/OpenCodex routing through the same sanitized restore path. |
| `acc auth login/status/logout` | Manage native provider login without printing secrets. |
| `acc mcp install` | Install ACC's safe bundled MCP config. |
| `acc mcp doctor` | Check bundled MCP tools and config. |
| `acc` | Run the gateway directly. |
| `acc -tui` | Run the gateway with the terminal dashboard. |
| `acc -ui` | Run the gateway and open the web dashboard. |

### Bundled local tools

ACC includes two safe-by-default local MCP servers for Claude Code:

- `acc-websearch`: keyless multi-source search plus guarded readable-page fetch.
- `acc-mac-control`: Calendar, Reminders, notifications, and path-based Apple
  Notes tools. Notes can be addressed as `Stillness/Sleep`; IDs are optional
  when a folder path and exact title identify the note. `notes_recent` returns
  the newest 1, 3, or 7 notes from a specific folder.

Obsidian is deliberately separate from ACC core. Its standalone Codex plugin,
server, skills, and build instructions live in [`plugins/obsidian`](plugins/obsidian/README.md).

`acc claude` creates `~/.config/acc/mcp.json` when missing and passes it through
Claude Code's `--mcp-config` option in strict mode. That keeps older global MCP
servers from shadowing ACC's tools without rewriting Claude's global config.
Install or refresh it directly with:

```bash
acc mcp install
acc mcp doctor
```

The separate Claude-3p desktop app reads its own config. Merge ACC into that
config, remove only the three legacy custom servers, and preserve unrelated
servers and preferences with:

```bash
acc mcp install --claude-3p
```

Obsidian remains a separate plugin, but ACC can register its standalone,
vault-locked MCP server in Claude-3p when requested:

```bash
acc mcp install --claude-3p --include-obsidian
```

This adds Claude's `obsidian` MCP entry without moving or copying the plugin.
The plugin's skills and server stay together under `plugins/obsidian`.

The unrestricted `acc-osascript` server is bundled but disabled by default.
Enable it only when you need arbitrary AppleScript or JXA:

```bash
acc mcp install --include-raw-osascript
# Claude-3p, including raw osascript:
acc mcp install --claude-3p --include-raw-osascript
```

`web_fetch` accepts only public HTTP(S) destinations and rejects private/local
network targets, error pages, oversized responses, unsupported binary content,
redirect loops, and timeouts. Mac apps opened by `acc-mac-control` are closed
after the call; apps already running before the call are left alone.

### Codex Desktop (experimental)

This integration is a work in progress and can still break on app or protocol
changes. Keep `acc codex restore` as the escape hatch back to the normal
subscription connection.

`acc codex setup` generates a deterministic catalog of real, provider-prefixed
IDs such as `nvidia/z-ai/glm-5.2`, `opencode/big-pickle`, or an authenticated
provider's discovered IDs. Codex connects directly to ACC's `/v1/responses`
endpoint. The Codex catalog never advertises Claude aliases (`fable`, `opus`,
`sonnet`, `haiku`); those remain available only to Claude Code.

Choose one directly when scripting:

```bash
acc codex setup --model nvidia/z-ai/glm-5.2
acc codex start
```

The catalog contains only the efforts declared for each model. Unsupported
choices are rejected before ACC contacts a provider. Model and effort arrive on
every request, so separate Codex tasks stay independent. `acc codex restore`
removes active ACC/OpenCodex routing and writes the durable sanitized
subscription baseline while retaining the raw snapshot for recovery.

Codex real-model IDs route to that exact provider and model. Each request goes
directly to the selected provider with no automatic fallback or rerouting.

Codex's free-form custom tools are bridged through Chat Completions without
changing their native Responses call or streaming shape. Provider-hosted tools
such as web search are not ACC capabilities and return a clear backend-specific
error if Codex sends one. Codex 0.144.2 enables web search by default, so
`acc codex` disables it only for the active ACC connection; `acc codex restore`
returns the previous subscription-safe setting.

### Native provider login

Kimi uses device authorization. xAI/Grok browser OAuth is experimental because
an endorsed third-party flow could not be confirmed; `XAI_API_KEY` remains the
stable alternative. Anthropic uses `ANTHROPIC_API_KEY` as the stable inference
path. `--import-claude-code` makes an explicit, read-only ACC copy but is not
advertised for inference, and unsupported subscription impersonation is not
implemented.

```bash
acc auth login kimi
acc auth login xai
acc auth login anthropic
acc auth status
```

OAuth credentials are stored in macOS Keychain. A private file store is used
only when explicitly enabled with `ACC_AUTH_STORE=file` and an absolute
`ACC_AUTH_FILE_DIR`. OpenCodex is no longer installed, started, configured, or
required by ACC. Existing user-installed OpenCodex files are left untouched.
`acc codex setup` safely migrates an active port-10100 connection while
preserving unrelated Codex providers; it does not read or move OpenCodex auth.

## Configuration

ACC reads the split config under `~/.config/acc/` by default:

```text
~/.config/acc/
  providers.json           # port, providers, global system_prepend
  claude/config.json       # Claude Code alias_routes
  claude/system_prompts/   # Fable, Opus, Sonnet, Haiku
  codex/config.json        # Codex models catalog
  system_prompts/persona.md
  .env
```

Claude aliases (`fable`, `opus`, `sonnet`, `haiku`) each point to one direct
provider/model route with no automatic fallbacks. Provider errors return
directly to the client — no silent model or provider switching. ACC
hot-reloads all three config files on every request; no restart needed for
config-only changes.

Provider keys belong in `~/.config/acc/.env`, never in JSON config files or Git.
You can name any provider in `providers.json` as long as it exposes an
OpenAI-compatible `/chat/completions` endpoint.

Use a direct provider path to bypass family routing for one request:

```
<anything>/<provider>/<model...>
```

For example, `anthropic/nvidia/z-ai/glm-5.2` uses the `nvidia` provider from
your config and sends it `z-ai/glm-5.2`.

Claude aliases use each route's locked `reasoning_effort` (and optional
`extra_body.reasoning_budget`) — not a global thinking-budget table. Codex
models use the efforts declared under `models.<id>.reasoning`.

## What it handles

- Anthropic Messages, OpenAI Responses, and OpenAI Chat Completions endpoints.
- Streaming responses, parallel tool calls, tool results, images, and file parts.
- Codex model discovery and multi-turn tool calls.
- Per-provider rate limiting and retrying.
- Live terminal and web dashboards with request logs.
- Config validation before the gateway starts.

## Model traits

Known provider and model behaviors to be aware of:

- **nemotron-ultra (550B) is slow.** A 32000-token reasoning budget can hang 6+
  minutes on a trivial prompt. At 8000 it answers in ~99s. Only a model swap
  fixes the speed.
- **big-pickle** (opencode) is a codename for `deepseek-v4-flash` — a reasoning
  model that returns EMPTY content if `max_tokens` is too low (it spends the
  entire budget on `reasoning_content`). Always set `max_tokens` high enough to
  leave room for visible output.
- **NVIDIA reasoning models** (nemotron, deepseek, glm) are text-only. Do not
  send images to these routes.
- **Gemini 3.x multi-turn tools work** — the proxy injects
  `skip_thought_signature_validator` automatically. The old rule restricting
  Gemini to single-turn requests is obsolete.
- **MiniMax M3** rejects `reasoning_budget` — it reasons natively without one.
  Its tool support is partial (`DEGRADED function cannot be invoked`), so it is
  excluded from requests carrying tools.

## Routing architecture

ACC supports two routing paths:

**Claude aliases** (`anthropic/claude-fable`, `anthropic/claude-opus`,
`anthropic/claude-sonnet`, `anthropic/claude-haiku`). Each alias maps to one
direct provider and model. Provider errors are returned as-is — no automatic
fallback or model switching. These four are the only Claude aliases;
`claude-writer` is obsolete.

**Codex model IDs** (e.g. `nvidia/z-ai~sglm-5.2`, `opencode/big-pickle`). Codex
uses exact provider-prefixed stable IDs from `codex/config.json` `models`. Each
ID routes to exactly one provider/model with no alias lookup or fallback chain.
Codex never uses Claude aliases.

**Direct provider path.** Any request to
`anthropic/<provider>/<model>` (e.g. `anthropic/nvidia/z-ai/glm-5.2`) bypasses
alias and family routing and uses the named provider directly.

## Identity

ACC identifies itself as `I'm Kabir's Second Brain.` for Codex and non-alias
routes. The editable source is [`system_prompts/persona.md`](system_prompts/persona.md);
`persona.go` only loads it and substitutes `{{backend}}`.

Claude Code aliases (`fable`, `opus`, `sonnet`, `haiku`) may each set
`system_prepend` in [`claude/config.json`](claude/config.json) to a file under
[`claude/system_prompts/`](claude/system_prompts/) (for example
`@system_prompts/Fable`). When an alias has that file, ACC injects it and
**skips** the Second Brain persona for that request. You choose which alias
follows which prompt in `claude/config.json`.

## Working context

Durable handoff notes that are easy to forget between tasks. `AGENTS.md` remains
the main agent guide.

### Live paths

- Source: this repository
- Commands: `~/.local/bin/acc` and `~/.local/bin/acc-proxy`
- Runtime config root: `~/.config/acc/` (`providers.json`, `claude/`, `codex/`)
- Secrets: `~/.config/acc/.env`
- Alias prompts: `~/.config/acc/claude/system_prompts/`
- Shared persona: `~/.config/acc/system_prompts/persona.md`
- Codex config: `~/.codex/config.toml`
- Request metrics: `~/acc/test_runs.jsonl`

### Codex Desktop contract (experimental)

The integration remains incomplete and may break when Codex changes its model
catalog, request shape, or desktop behavior. Preserve the reversible
`acc codex restore` path and do not describe this surface as production-ready.

- `acc codex` must open the existing `/Applications/ChatGPT.app` directly with
  macOS `open`. Never invoke the bundled
  `/Applications/ChatGPT.app/Contents/Resources/codex app` command: when a
  separate app bundle is absent, that command downloads another 587 MB installer
  and creates `/Applications/Codex.app`.
- When a command needs to start the background gateway, it prefers the sibling
  `acc-proxy` binary so `acc-stop` / `acc-restart` can manage the process.
- ACC backs up the existing Codex config, writes an ownership-marked direct ACC
  Responses provider, and preserves unrelated projects, MCPs, and preferences.
- Codex 0.144.2 enables hosted web search by default even when a model catalog
  does not advertise it. ACC writes `web_search = "disabled"` only while its
  provider is active; restore puts the prior setting back.
- ACC writes a Codex-compatible `model_catalog_json` from `codex/config.json`
  `models`. The installed Codex CLI must parse this file before a release is
  verified.
- Codex stores the chosen stable model ID and reasoning effort in the task. ACC
  resolves both on every request. Never read a global "active model" to rewrite
  another task's request.

### Codex model registry

The Codex menu uses deterministic `provider/upstream-model` IDs from enabled ACC
capabilities plus authenticated native-provider discovery. Claude aliases stay
in the Claude Code registry and never appear in Codex. A direct Codex ID routes
exactly to its named provider/model with no automatic fallback.

Unsupported efforts return 400 before a provider call. Response headers report
requested model/effort plus actual provider and backend model.

### Plugins, Sites, and scheduled tasks

- Claude Code loads ACC's local MCP bundle from `~/.config/acc/mcp.json` via
  `--mcp-config --strict-mcp-config`. Obsidian stays a separate plugin under
  `plugins/obsidian`.
- Claude-3p uses
  `~/Library/Application Support/Claude-3p/claude_desktop_config.json`;
  `acc mcp install --claude-3p` merges ACC servers there.
- Sites 0.1.27 is a Codex plugin: design picker is MCP; save/deploy is a Codex
  connector. Never claim a Sites deployment was tested unless one was created.
- Scheduled local jobs store model ID and effort but have no custom
  `model_provider` field yet — do not advertise scheduled ACC support until
  Codex persists the custom provider.

### Native Codex lifecycle and auth

Supported path: `Codex -> ACC loopback -> exact provider/model`.
`acc codex setup/start/stop/status/doctor/restore/remove` do not execute or
configure OpenCodex. Process ownership is recorded only when `start` launches
ACC. Config changes are atomic, timestamp-backed up, ownership-marked, and
exactly restorable.

Native Kimi and xAI credentials live in macOS Keychain. Anthropic API keys
remain the stable path. Existing official Claude/Grok credentials are read only
after an explicit import command.

### Verification

```bash
make test
curl -sS http://localhost:9999/health
tail -n 10 /Users/kabir/acc/test_runs.jsonl
```

For a live model check, send a tiny `/v1/responses` request and verify the
`X-ACC-*` response headers plus the final JSONL row. Use a temporary Codex home
for catalog parser tests.

Third-party notices for referenced OpenCodex patterns live in
[`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md).

## Security

ACC has no authentication and listens on loopback only (`127.0.0.1`). Do not
change it to a LAN or internet-facing address. If another local process can
reach that port, it can use your provider keys through ACC.

- Keep `~/.config/acc/.env` private. `chmod 600 ~/.config/acc/.env` is a good
  default on a shared machine.
- ACC does not provide TLS. Do not expose it to the internet.
- Prompts, tool data, and images leave your machine for the provider selected
  by the route.
- The web endpoints allow cross-origin requests. That is useful for local tools,
  but makes network exposure riskier.

## From source

```bash
go install github.com/ATruePerson/acc@latest
# Or in this repository:
go run . -config providers.json
```

If you start the gateway yourself, point Anthropic clients at it:

```bash
export ANTHROPIC_BASE_URL=http://localhost:9999
```

The `-env` flag loads a dotenv file, defaulting to `~/.config/acc/.env`.
Existing environment variables win over values from that file.

## API

| Endpoint | Purpose |
| --- | --- |
| `GET /health` | Health check. Returns `acc-proxy ok`. |
| `GET /v1/models` | Model discovery for Anthropic and OpenAI clients. |
| `POST /v1/messages` | Anthropic Messages API. |
| `POST /v1/responses` | OpenAI Responses API, used by Codex. |
| `POST /v1/chat/completions` | OpenAI-compatible chat endpoint. |
| `GET /app` | Web dashboard. |

## Tests

```bash
make test
```

### Development

ACC is maintained by Kabir and developed with assistance from OpenAI Codex.

## License

Apache License 2.0. See [LICENSE](LICENSE).
