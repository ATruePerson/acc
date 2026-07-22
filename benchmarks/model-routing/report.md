# ACC model-routing report

Generated: 2026-07-16

Profile: `combined`

Reasoning policy: `maximum`
Status: **provisional** because the selected primary and fallback are free endpoints.

## 1-4. ACC repair

The regression had four independent causes: the source and runtime config still
exposed the retired Sol/Terra/Luna catalog, the installed `acc` and `acc-proxy`
binaries were different builds, a stale proxy process continued serving old
routes after installation, and the non-Codex `/v1/models` handler hard-coded
five legacy names instead of reading the configured public aliases. Codex also
retained stale root Sol/Luna model defaults without usable provider state.

The repair replaced the catalog with stable `opus`, `sonnet`, and `haiku` IDs,
made both model-list formats derive from configured public entries, made the
launcher generate and validate that catalog, cleaned the user's normal Codex
subscription baseline, rebuilt identical binaries, and verified plain,
streaming, standard-tool, custom-tool, continuation, effort, image capability,
and catalog/restore paths live. A pre-repair checkpoint exists as Git stash commit
`57f9ed240042d9a68abbdbd374ad353e88a6e9d1`.

Core repair files: `cli.go`, `cli_test.go`, `codex_app.go`,
`codex_integration_test.go`, `model_registry.go`, `types.go`, `main.go`,
`persona.go`, routing/translation tests, `config.json`, `ACC.md`, and `README.md`.
The full race-enabled Go suite passed before benchmarking. The final rerun is
recorded in section 26.

## 5-11. Models, runs, reliability, and tools

The curated comparison contains 201 real requests through ACC. Provider success
means the upstream completed a usable response. Correct means the result also
passed the case evaluator. Tool success covers standard functions, the raw
custom exec bridge, and the multi-step repository workflow. No successful tool
call had malformed arguments, so schema error rate is 0%. No malformed call
required a repair attempt, so repair success is `N/A (0 attempts)`, not 0%.

| Provider / exact model ID | Runs | Provider | Correct | Tools | Schema errors | Avg total | Avg TTFT |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| NVIDIA `z-ai/glm-5.2` | 3 | 0.0% | 0.0% | 0.0% | 0.0% | 15238 ms | N/A |
| NVIDIA `minimaxai/minimax-m3` | 12 | 66.7% | 58.3% | unsupported | 0.0% | 3875 ms | 1591 ms |
| NVIDIA `nvidia/nemotron-3-super-120b-a12b` | 24 | 75.0% | 75.0% | 60.0% | 0.0% | 4738 ms | 1217 ms |
| NVIDIA `nvidia/nemotron-3-ultra-550b-a55b` | 27 | 70.4% | 66.7% | 60.0% | 0.0% | 9063 ms | 2939 ms |
| OpenCode `big-pickle` | 27 | 81.5% | 81.5% | 66.7% | 0.0% | 4076 ms | 2314 ms |
| OpenCode `deepseek-v4-flash` | 3 | 0.0% | 0.0% | 0.0% | 0.0% | 1133 ms | N/A |
| OpenCode `deepseek-v4-flash-free` | 27 | 81.5% | 77.8% | 66.7% | 0.0% | 3868 ms | 1634 ms |
| OpenRouter `tencent/hy3:free` | 27 | **100.0%** | **100.0%** | **100.0%** | 0.0% | 7874 ms | 3589 ms |
| OpenRouter `poolside/laguna-m.1:free` | 24 | 0.0% | 0.0% | 0.0% | 0.0% | 1263 ms | N/A |
| OpenRouter `nvidia/nemotron-3-ultra-550b-a55b:free` | 27 | 88.9% | 81.5% | 86.7% | 0.0% | 12891 ms | 9541 ms |

Models that could not complete a valid test were NVIDIA GLM-5.2, which timed
out, OpenCode `deepseek-v4-flash`, which returned 401 credit errors, and
OpenRouter Laguna, whose repeated matrix returned free-quota 429s after an
initial discovery probe.

## 12-16. Coding, context, latency, and reasoning

| Model | Coding workflow | 20K | 50K | 80K | Reasoning actually sent |
| --- | ---: | ---: | ---: | ---: | --- |
| OpenRouter HY3 | 5/5 | pass | pass | pass | `reasoning_effort=high` |
| OpenRouter Nemotron Ultra | 4/5 | fail | fail | fail | `reasoning_effort=high` |
| NVIDIA Nemotron Ultra | 2/5 | not run | not run | not run | `reasoning_budget=32000`, `enable_thinking=true` |
| NVIDIA Nemotron Super | 0/5 | not run | not run | not run | `reasoning_budget=32000`, `enable_thinking=true` |
| OpenCode Big Pickle | 0/5 | pass | pass | pass | `reasoning_effort=max` |
| OpenCode DeepSeek V4 Flash Free | 0/5 | pass | pass | pass | `reasoning_effort=max` |

Both OpenCode models passed simple standard and custom tools, but all ten
multi-tool coding workflows failed upstream with HTTP 400. HY3 retained the
20K, 50K, and 80K constraints and returned the exact answer in all three runs.
Its corrected long-context total latencies were 8215, 43272, and 10324 ms.

The normalized user-facing effort is `max`. Provider mappings are exact:
NVIDIA reasoning models receive a 32000 budget plus thinking enabled; OpenRouter
HY3 and Nemotron Ultra receive `high`; OpenRouter Laguna receives nested
`reasoning.effort=high`; OpenCode receives `max`; MiniMax receives no unsupported
reasoning parameter and reasons natively.

## 17. Opus scoring

Weights: coding 25%, tools 25%, reasoning 20%, long context 15%, reliability 15%.

| Candidate | Score | Main reason |
| --- | ---: | --- |
| OpenRouter HY3 | **100.0** | Perfect in every measured category |
| OpenRouter Nemotron Ultra | 75.0 | Strong tools, but all long-context attempts failed |
| NVIDIA Nemotron Ultra | 48.9 | Coding and provider reliability were too weak |
| NVIDIA GLM-5.2 | 0.0 | No completed requests |
| OpenRouter Laguna | 0.0 | Repeated free-quota failures |

## 18. Sonnet scoring

Weights: tools 30%, reliability 25%, coding 20%, latency 15%, reasoning 10%.
Latency is normalized against the fastest eligible successful candidate.

| Candidate | Score | Main reason |
| --- | ---: | --- |
| OpenRouter HY3 | **92.4** | Slower, but perfect tools, coding, and reliability |
| OpenCode Big Pickle | 64.6 | Fast simple tools; multi-tool coding failed 5/5 |
| OpenCode DeepSeek V4 Flash Free | 62.0 | Same workflow failure plus one instruction miss |
| OpenCode DeepSeek V4 Flash | 0.0 | 401 credits required |

## 19. Haiku scoring

Weights: reliability 35%, latency 30%, tools 20%, coding 10%, reasoning 5%.
Latency is normalized against NVIDIA Nemotron Super, the fastest eligible
candidate with successful requests.

| Candidate | Score | Main reason |
| --- | ---: | --- |
| OpenRouter HY3 | **88.1** | Reliability and correctness outweighed slower TTFT |
| NVIDIA Nemotron Super | 73.3 | Fast, but quota failures and 0/5 coding workflows |
| OpenRouter Nemotron Ultra | 72.5 | Capable but much slower and less reliable |

## 20-23. Final routing

Opus keeps the strongest measured chain: OpenRouter `tencent/hy3:free` primary
and OpenRouter `nvidia/nemotron-3-ultra-550b-a55b:free` fallback. Sonnet uses
OpenCode `big-pickle` with NVIDIA `nvidia/nemotron-3-super-120b-a12b` fallback.
Haiku uses that NVIDIA Nemotron Super route first, with OpenCode
`deepseek-v4-flash-free` fallback.

Sonnet and Haiku deliberately trade some benchmark score for provider
diversity. If OpenRouter fails, switching away from Opus moves the request to a
different provider instead of another OpenRouter endpoint. Their primary and
fallback routes are all text-only, so neither chain changes image capability
mid-fallback. Provider maximum reasoning remains explicit: OpenRouter receives
`high`, OpenCode receives `max`, and NVIDIA receives `high` plus a 32000-token
reasoning budget with thinking enabled.

Images use hidden route NVIDIA `minimaxai/minimax-m3`, with native reasoning and
no tools. MiniMax accepted images but identified the red test image correctly
only 1/3 times; one answer was wrong and one run hit quota. Image-plus-tool input
returns a clear unsupported error. OpenCode models never receive images.

## 24-25. Files created and changed

Created: `benchmarks/model-routing/README.md`, case/model manifests, runner and
tests, safe in-memory fixture repository, raw result JSON files, `results.json`,
this report, and `recommended-routing.yaml`.

Changed for this work: `config.json`, `types.go`, `model_registry.go`, `main.go`,
`codex_app.go`, `cli.go`, `persona.go`, their related tests, `ACC.md`, and
`README.md`. The ACC persona is now explicitly split into core behavior,
Claude Code runtime/tool-adapter rules, and Kabir's personal instructions.

## 26-28. Final verification, risks, and status

Final verification passed: `go test -race ./...` completed in 13.487 seconds for
the root package and passed the benchmark runner package from cache. Catalog
parsing, launcher and clean restore snapshot, three visible aliases with default
`max`, live text and streaming, standard and custom tools, multi-turn
continuation, exact effort headers, image routing, and clear image-plus-tool
rejection also passed. ACC is left active on Opus with the gateway healthy.

Remaining risks: both selected text routes are free and can be rate-limited or
removed; HY3's 50K run was much slower than 20K/80K; the benchmark observed no
malformed tool arguments, so automatic argument-repair behavior has unit but not
live-model evidence; MiniMax image accuracy was poor; and fallback under a real
mid-stream provider failure remains harder to reproduce deterministically than
the unit coverage.

The routing is **provisional**, not permanently confirmed. Re-run the suite when
provider IDs, quotas, or behavior change.
