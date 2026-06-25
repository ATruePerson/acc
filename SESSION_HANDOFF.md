# acc proxy session handoff (2026-06-25)

## What this is
acc = local Anthropic->OpenAI proxy on :9999, drives Claude Desktop (3p) inference
via free providers (nvidia NIM, gemini, opencode). Config: `~/.config/acc/config.json`,
env: `~/.config/acc/.env`, source: `~/acc`, binary: `~/.local/bin/acc-proxy`.

## Current model map (LIVE on :9999)
| slot / alias | model | provider | notes |
|---|---|---|---|
| opus / anthropic/claude-opus | mistral-large-3-675b | nvidia | 2s, fast. fires web_search in probe |
| sonnet / anthropic/claude-sonnet | gemini-2.5-flash | gemini | off-nvidia, clean tools, 2.3s |
| haiku / anthropic/claude-haiku | gemini-2.5-flash-lite | gemini | fast |
| fable / anthropic/claude-fable | minimax-m3 | nvidia | smart, 2s |
| mythos / anthropic/claude-mythos | big-pickle | opencode | RATE-LIMITED, returns empty |

Killed: glm-5.1 on opus (was 42s), nemotron-ultra (60s), kimi (garbles tools).

## Code patch made this session (UNCOMMITTED in ~/acc)
- `types.go`: added `SystemPrepend` field to `Route` struct (per-model system prompt).
- `translate.go`: route.SystemPrepend overrides global cfg.SystemPrepend.
- `translate_test.go`: added TestRouteSystemPrependOverridesGlobal. `go test ./...` GREEN.
- ALSO present (True's prior WIP, now built into live binary): main.go 429/503 retry+backoff,
  translate.go sanitizeReasoningEffort.
- Built to `~/.local/bin/acc-proxy`; old binary backed up `acc-proxy.bak-prepatch`.
- NOT git-committed. To commit: `cd ~/acc && git add -A && git commit`.

## Per-model Claude system prompts (LIVE)
Each anthropic/claude-* alias carries its real Claude system prompt via `system_prepend`.
Verified identities: Opus 4.8, Sonnet 4.6, Haiku 4.5, Fable 5 all answer correctly.
mythos uses the fable prompt (no mythos prompt exists yet). input_tokens ~3-4.5k confirms inject.
Source prompt files: scratchpad opus_sys.txt / sonnet_sys.txt / haiku_sys.txt / fable_sys.txt.

## Key facts / gotchas
- acc has NO hot-reload. Config or binary change => restart:
  `kill $(lsof -nP -iTCP:9999 -sTCP:LISTEN -t); nohup ~/.local/bin/acc-proxy --config ~/.config/acc/config.json --env ~/.config/acc/.env >/tmp/acc-proxy.log 2>&1 &`
- nvidia NIM = 40 RPM SHARED across whole key (opus+fable both nvidia). sonnet on gemini relieves it.
- opencode models (claude-sonnet-4-6, gpt-5.4-mini etc) = PAID, need card. Only big-pickle free-ish + rate-limited.
- deepseek-v4-pro works (8.8s, slow); v4-flash = 429.
- Config backups: config.json.bak3/.bak4/.bak5.

## Open items
1. mythos/big-pickle rate-limited -> empty. Point at a working model or drop.
2. SpaceX-type hallucination: per-model prompts tell models to defer to web_search on current
   facts (helps), but grounding still depends on model firing search. mistral did in isolation;
   failed in a chat that already had bad context.
3. OpenRouter sonnet option (not wired): NVIDIA Nemotron 3 Super (free) = best pick if adding a
   3rd bucket (1M ctx, 1169ms, 66 t/s, tools). Needs openrouter provider + OPENROUTER_API_KEY re-added.
4. SECURITY (carried, still open): GitHub tokens in plaintext in Claude-3p settings.local.json +
   github MCP header. Rotate + move to keychain.
5. Decide whether to git-commit the acc patch.
