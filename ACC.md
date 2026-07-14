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
- ACC backs up the existing Codex config once, writes the ACC provider and chosen
  model, closes the running app, then reopens ChatGPT at the requested workspace.
- `acc codex --restore` restores the original subscription config and removes the
  generated catalog and backup state.
- Codex Desktop currently filters custom `model_catalog_json` entries from its
  model picker. The configured model still works, but the app may display only
  `Custom`. `acc codex` therefore provides the reliable Sol/Terra/Luna picker in
  the terminal. `--model` remains available for scripts.

## Codex family routing

Codex aliases are derived dynamically from `config.json` family routes. The
entire `Route` is copied, including provider, model, prompts, limits, vision,
tool support, and fallbacks.

| Codex name | ACC ID | Family copied | Current primary | Current fallback |
| --- | --- | --- | --- | --- |
| Sol | `openai/codex-5.6-sol` | `routes.opus` | NVIDIA `z-ai/glm-5.2` | NVIDIA `minimaxai/minimax-m3` |
| Terra | `openai/codex-5.6-terra` | `routes.sonnet` | OpenCode `big-pickle` | NVIDIA `nvidia/nemotron-3-super-120b-a12b` |
| Luna | `openai/codex-5.6-luna` | `routes.haiku` | NVIDIA `stepfun-ai/step-3.7-flash` | none |

Do not create separate static Codex route copies. Change the family route in the
runtime config and Codex will follow it on the next request.

## July 14, 2026 failure

The first desktop attempt used the bundled `codex app` installer and created a
duplicate `/Applications/Codex.app`; it was verified byte-for-byte against the
real app and removed with Kabir's approval. The app showed a blank custom model menu.
Two Sol requests reached ACC, failed over from GLM 5.2 to MiniMax M3, and ended
with HTTP 504 because the fallback emitted no response before the first-token
timeout. A later live check showed GLM could also stall before returning HTTP
headers, which bypassed the first-token guard; ACC now applies the same timeout
while waiting for headers so fallback can begin. The blank picker and the
timeout were separate problems.

## Verification

```bash
make test
curl -sS http://localhost:9999/health
tail -n 10 /Users/kabir/acc/test_runs.jsonl
```

For a live model check, send a tiny `/v1/responses` request and confirm the final
JSONL row has the expected ACC model ID, routed upstream model, and status 200.
