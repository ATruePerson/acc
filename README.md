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

# Save provider keys and create ~/.config/acc/config.json.
acc setup

# Start Claude Code through ACC.
acc claude

# Or try the experimental Codex Desktop integration.
acc codex
```

`acc setup` stores keys in `~/.config/acc/.env` and creates a config file if
one does not exist. `acc claude` starts the gateway when needed and launches
Claude Code with the right connection. `acc codex` backs up your Codex provider
settings, switches it to ACC, and reopens the existing ChatGPT desktop app.

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
| `acc codex restore` | Restore the exact previous Codex settings. |
| `acc codex remove` | Remove only ACC-owned Codex settings. |
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
changes. Keep `acc codex --restore` as the escape hatch back to the normal
subscription connection.

`acc codex setup` generates a deterministic catalog of real, provider-prefixed
IDs such as `nvidia/z-ai/glm-5.2`, `opencode/big-pickle`, or an authenticated
provider's discovered IDs. Codex connects directly to ACC's `/v1/responses`
endpoint. The Codex catalog never advertises the Claude aliases `opus`,
`sonnet`, or `haiku`; those remain available only to Claude Code.

Choose one directly when scripting:

```bash
acc codex setup --model nvidia/z-ai/glm-5.2
acc codex start
```

The catalog contains only the efforts declared for each model. Unsupported
choices are rejected before ACC contacts a provider. Model and effort arrive on
every request, so separate Codex tasks stay independent. `acc codex --restore`
puts your previous Codex connection back.

Codex real-model IDs route to that exact provider and model. They never inherit
Claude alias fallback chains. Explicit real-model fallback configuration still
works, and ACC never quietly lowers effort or removes tools to force a fallback.

Codex's free-form custom tools are bridged through Chat Completions without
changing their native Responses call or streaming shape. Provider-hosted tools
such as web search are not ACC capabilities and return a clear backend-specific
error if Codex sends one. Codex 0.144.2 enables web search by default, so
`acc codex` disables it only for the active ACC connection; `--restore` returns
your previous setting.

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

ACC reads `~/.config/acc/config.json` by default. Routes are yours to control:
`opus`, `sonnet`, and `haiku` each point to a provider, model, optional fallback,
and request settings. Restart ACC after changing the file.

Provider keys belong in `~/.config/acc/.env`, never in `config.json` or Git.
You can name any provider in `providers` as long as it exposes an
OpenAI-compatible `/chat/completions` endpoint.

Use a direct provider path to bypass family routing for one request:

```
<anything>/<provider>/<model...>
```

For example, `anthropic/nvidia/z-ai/glm-5.2` uses the `nvidia` provider from
your config and sends it `z-ai/glm-5.2`.

Thinking budgets map to the closest `reasoning_effort` value in your config:

```json
"effort": {
  "low":       { "budget": 2000,  "reasoning": "low" },
  "medium":    { "budget": 6000,  "reasoning": "low" },
  "high":      { "budget": 16000, "reasoning": "medium" },
  "xhigh":     { "budget": 24000, "reasoning": "high" },
  "max":       { "budget": 32000, "reasoning": "high" },
  "ultracode": { "budget": 48000, "reasoning": "high" }
}
```

## What it handles

- Anthropic Messages, OpenAI Responses, and OpenAI Chat Completions endpoints.
- Streaming responses, parallel tool calls, tool results, images, and file parts.
- Codex model discovery and multi-turn tool calls.
- Per-provider rate limiting, retrying, and configured fallbacks.
- Live terminal and web dashboards with request logs.
- Config validation before the gateway starts.

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
go run . -config config.json
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
