# acc

Anthropic API → OpenAI-compatible proxy. Routes Claude SDK calls to
third-party providers (NVIDIA NIM, Gemini, OpenRouter, OpenCode, ZAI) by
translating message format, tool use, streaming, and images between protocols.

## Quick Start

```bash
# 1. Copy and configure
cp .env.example .env
# fill in API keys

# 2. Run
go run . -config config.json

# 3. Point Claude Code at it
export ANTHROPIC_BASE_URL=http://localhost:9999
```

The `-env` flag loads a dotenv file (default `~/.config/acc/.env`).
Variables are set only if not already in the environment.

## Configuration

### `config.json`

Routes map Claude model families to upstream providers:

| Slot    | Default route                     |
| ------- | --------------------------------- |
| opus    | GLM-5.1 via NVIDIA NIM            |
| sonnet  | big-pickle via OpenCode           |
| haiku   | Step 3.7 Flash via NVIDIA NIM     |
| vision  | Gemini 2.5 Flash (image requests) |

Override per-request by using the direct path form as the model name:

```
<anything>/<provider>/<model...>
```

Example: `anthropic/nvidia/z-ai/glm-5.1` routes to NVIDIA using GLM-5.1 directly.

### Effort mapping

Thinking budget tokens → `reasoning_effort` bucket:

```json
"effort": {
  "low":       { "budget": 2000,  "reasoning": "low" },
  "medium":    { "budget": 6000,  "reasoning": "low" },
  "high":      { "budget": 16000, "reasoning": "medium" },
  "ultracode": { "budget": 48000, "reasoning": "high" }
}
```

### Providers

| Provider    | Base URL                                    |
|-------------|---------------------------------------------|
| NVIDIA NIM  | `https://integrate.api.nvidia.com/v1`       |
| Gemini      | `https://generativelanguage.googleapis.com/v1beta/openai` |
| OpenRouter  | `https://openrouter.ai/api/v1`              |
| ZAI         | `https://api.z.ai/api/paas/v4`              |
| OpenCode    | `https://opencode.ai/zen/v1`                |

API keys come from environment variables — never hardcode secrets in config.json.

## Features

- **Protocol translation** — Anthropic `/v1/messages` ↔ OpenAI `/v1/chat/completions`
- **Streaming** — real-time SSE with per-token flushing
- **Tool use** — function calling in both directions
- **Images** — translates image blocks to OpenAI format
- **Effort mapping** — thinking budget → reasoning_effort
- **Graceful shutdown** — drains active requests on SIGINT/SIGTERM
- **Context cancellation** — cancels upstream if client disconnects
- **CORS** — cross-origin headers for desktop/UI tools
- **Config validation** — catches misspelled providers at startup, not first request

## Security

**Run on localhost only.** acc has no authentication. It binds to all
interfaces on the configured port, so exposing it to a network lets anyone
send requests through your upstream API keys.

Other things to keep in mind:

- **Protect your key file.** The default dotenv path is `~/.config/acc/.env`;
  restrict file permissions (`chmod 600`) so other users on the machine
  cannot read your provider keys.
- **No TLS.** Traffic between your client and acc is plaintext. That is
  fine for `localhost`, but do not terminate TLS here and expose the port.
- **Data leaves your machine.** Every prompt, tool call, and image is
  forwarded to whichever upstream provider your routing config selects.
- **Upstream errors are echoed.** Failed provider responses are logged and
  partially returned to the client, which can leak provider error details.
- **CORS is open.** `Access-Control-Allow-Origin: *` is intentional for
  local desktop tools, but it widens who can call the API if the port is
  reachable.

## Tests

```bash
go test -v ./...
```

## API

### `GET /health`

```
acc ok
```

### `GET /v1/models`

Lists advertised Claude model IDs so model discovery works.

### `POST /v1/messages`

Standard [Anthropic Messages API](https://docs.anthropic.com/en/api/messages)
format. Translated to OpenAI chat completions upstream and back.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
