# OpenCodex integration test report (historical)

> Superseded by the native direct integration. These results describe the
> removed temporary bridge and are not current verification evidence.

Date: 2026-07-21
Branch: `feat/opencodex-codex-integration`

## Result

PARTIAL. The repository wrapper, live two-proxy path, model discovery,
streaming, ephemeral Codex CLI, and temporary file/shell tools work. MCP,
multi-turn tool results, controlled fallback, vision, and apply_patch remain
unverified, so the integration is not claimed fully working.

## Baseline

- `git status --short --branch`: existing uncommitted ACC/Obsidian work was
  present on `feat/model-bench`; it was preserved.
- `git remote -v`: `https://github.com/ATruePerson/acc.git`.
- `make test`: the default sandbox cache path is blocked. With the task-local
  cache `GOCACHE=/private/tmp/acc-opencodex-gocache`, the full race suite passes.
- Node: `v26.0.0`.
- npm: `11.12.1`.
- Bun: not installed. OpenCodex's npm package bundles its runtime.
- OpenCodex: installed as `@bitkyc08/opencodex@2.7.30`; both `ocx` and
  `opencodex` report that version.

## Repository and local checks

| Check | Result | Evidence |
| --- | --- | --- |
| Wrapper shell syntax | PASS | `bash -n scripts/opencodex/acc-opencodex` |
| Diff whitespace | PASS | `git diff --check` |
| Secret ignore rules | PASS | `.env`, `auth.json`, `.opencodex/`, and restore artifacts are ignored |
| Synthetic model discovery/config merge | PASS | Temporary loopback fake ACC returned `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna`; wrapper wrote the expected provider with no `apiKey` |
| Proxy-loop guard | PASS | ACC config does not point at OpenCodex |
| ACC live health | PASS | `127.0.0.1:9999/health` |
| OpenCodex live health | PASS | `127.0.0.1:10100/healthz` |
| Deterministic doctor | PASS | Health, catalog, loopback, backup, and ignore checks passed; capability-only checks remain `SKIPPED` |

## Live model matrix

| ACC model | Provider path | Basic response | Streaming | Codex CLI | File tool | Shell tool | Overall |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `gpt-5.6-sol` | OpenRouter HY3 via ACC | PASS | PASS, 11 SSE events | PASS, `ACC_SMOKE_OK` | PASS in temp dir | PASS in temp dir | PASS for tested surface |
| `gpt-5.6-terra` | OpenCode Big Pickle via ACC | PASS | PASS, 11 SSE events | SKIPPED | SKIPPED | SKIPPED | PARTIAL |
| `gpt-5.6-luna` | Gemini 3.5 Flash via ACC | PASS | PASS, 11 SSE events | SKIPPED | SKIPPED | SKIPPED | PARTIAL |

The Codex file/shell test created `smoke.txt` only in a temporary directory.
It did not touch the repository or persist session history.

## Deferred tests

The following remain `SKIPPED`: file reading, apply_patch specifically,
multi-turn tool results, MCP forwarding, controlled primary-route failure and
fallback, reasoning-effort permutations, vision, and Codex App behavior.

Run the safe sequence when repeating the integration:

```bash
acc-start
scripts/opencodex/acc-opencodex setup
scripts/opencodex/acc-opencodex start
scripts/opencodex/acc-opencodex doctor
```

Do not mark the integration WORKING from health or HTTP 200 alone. Complete
the Codex tool and streaming smoke tests and record their sanitized results
here first.
