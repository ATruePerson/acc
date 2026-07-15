# acc

[![CI](https://github.com/ATruePerson/acc/actions/workflows/ci.yml/badge.svg)](https://github.com/ATruePerson/acc/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/ATruePerson/acc.svg)](https://pkg.go.dev/github.com/ATruePerson/acc)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

A local gateway for Claude Code and Codex Desktop. It accepts Anthropic Messages
and OpenAI Responses requests, translates them to OpenAI-compatible chat
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

# Or switch Codex Desktop to ACC.
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
| `acc codex [path]` | Switch Codex Desktop to ACC and open a workspace. |
| `acc codex --restore` | Restore the previous Codex provider settings. |
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

### Codex Desktop

`acc codex` refreshes Codex's catalog from enabled entries in
`config.json.models`, backs up the old connection, and reopens Codex without a
terminal picker. Opus is the default only for new tasks; Codex keeps the model
stored on existing tasks. The default config includes these five stable IDs:

| Model | Stable ID | Backend |
| --- | --- | --- |
| Opus | `opus` | `routes.opus` |
| Sonnet | `sonnet` | `routes.sonnet` |
| Haiku | `haiku` | `routes.haiku` |
| MiniMax M3 | `acc-minimax-m3` | NVIDIA direct |
| Nemotron Super | `acc-nemotron-super` | NVIDIA direct |

Choose one directly when scripting:

```bash
acc codex --model haiku /path/to/project
```

The catalog contains only the efforts declared for each model. Unsupported
choices are rejected before ACC contacts a provider. Model and effort arrive on
every request, so separate Codex tasks stay independent. `acc codex --restore`
puts your previous Codex connection back.

Opus is one logical model: text uses NVIDIA GLM-5.2 first, with an explicit
MiniMax M3 fallback. Image or mixed text-image requests go directly to MiniMax
M3 because GLM-5.2 is text-only. ACC never quietly lowers the selected effort.

Codex's free-form custom tools are bridged through Chat Completions without
changing their native Responses call or streaming shape. Provider-hosted tools
such as web search are not ACC capabilities and return a clear backend-specific
error if Codex sends one. Codex 0.144.2 enables web search by default, so
`acc codex` disables it only for the active ACC connection; `--restore` returns
your previous setting.

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

ACC has no authentication and currently listens on every network interface on
its configured port. Keep it on a trusted network. If someone else can reach
that port, they can use your provider keys through ACC.

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

## License

Apache License 2.0. See [LICENSE](LICENSE).
