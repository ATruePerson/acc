# ACC protocol stack analysis

Date: 2026-07-21  
Decision status: architecture recommendation only, no production implementation  
Target branch: `analysis/protocol-stack-architecture`

## Executive decision

**KEEP GO.** Keep ACC as one Go process and replace its direct, format-to-format translators with a typed internal request model plus a normalized event stream. Treat OpenCodex as a temporary compatibility bridge and a source of protocol fixtures, not as a permanent runtime dependency. Port the missing Codex and Claude behaviors into focused Go client/provider adapters only after compatibility tests exist.

Go already handles ACC's strongest concerns well: a small distributable binary, loopback HTTP, cancellation, retries, fallbacks, rate limiting, configuration, health, usage, and process control. The observed failures come from incomplete protocol modeling and permissive stream parsing, not from Go itself. A Bun sidecar would improve iteration speed but would add a second process, port, schema boundary, release cadence, log stream, and failure mode before those costs are justified.

Use Python outside the runtime for fixture analysis, provider probes, fuzzing, benchmarks, and compatibility reports. Reconsider a narrow Bun adapter only if a measured Go implementation repeatedly cannot keep pace with Codex protocol changes.

## Scope and evidence

The ACC working tree inspected for behavior was based on commit [`f34138cd7df570222459f72a2bfdf8d837baddb4`](https://github.com/ATruePerson/acc/commit/f34138cd7df570222459f72a2bfdf8d837baddb4) and contained uncommitted work. This report therefore distinguishes the inspected working tree from a reproducible commit. The report branch itself is based on `origin/main` at `7f93825` and contains only this document.

The external repositories were shallow-cloned at the exact commits listed below. Claims were checked against source and tests at those revisions. GitHub activity values are approximate snapshots from 2026-07-21 and will drift.

ACC verification on the inspected working tree:

```text
GOCACHE=/private/tmp/acc-go-cache GOMODCACHE=/private/tmp/acc-go-modcache make test
ok github.com/ATruePerson/acc                         13.559s
ok github.com/ATruePerson/acc/benchmarks/model-routing/runner (cached)
```

No live ACC, Codex, or OpenCodex configuration was changed. No global package was installed. No production source file was changed.

## Current ACC architecture

ACC is a loopback-only Go gateway with three client-facing protocol doors:

```text
Claude Code
  -> POST /v1/messages
  -> handleMessages
  -> translateRequest / translateMessage
  -> OpenAI Chat Completions request
  -> configured provider
  -> translateResponse or streamTranslate
  -> Anthropic Messages response/events

Codex direct
  -> POST /v1/responses
  -> handleResponses
  -> model registry and capability-aware route chain
  -> translateFromResponsesWithTools
  -> OpenAI Chat Completions request
  -> configured provider
  -> translateToResponsesWithTools or streamTranslateResponses
  -> Responses JSON/events

Codex through the current compatibility layer
  -> OpenCodex 127.0.0.1:10100
  -> OpenCodex openai-chat adapter
  -> ACC 127.0.0.1:9999/v1
  -> configured provider
```

The Go server owns config reload, model selection, retries, provider fallback, rate limiting, request size limits, first-byte timeouts, usage logging, model discovery, CLI launch/setup, and the dashboard. `config.json.models` is the Codex catalog and capability source of truth. ACC binds explicitly to `127.0.0.1`, but every local endpoint is unauthenticated.

### What ACC already does correctly

- Preserves ordinary function definitions, strict schemas, tool choice, parallel-tool intent, call IDs, multiple calls, fragmented arguments, results, and multi-turn function calls through the Chat Completions bridge.
- Bridges Responses `custom` tools through a deterministic one-string function wrapper and restores `custom_tool_call`, including streaming. This covers Codex freeform `apply_patch` when Codex advertises it as a custom tool.
- Flattens Responses `namespace` tools into collision-resistant function names and restores the original namespace and child name.
- Preserves user image data URLs and file parts on the Responses input path; Anthropic base64 images become OpenAI image URLs.
- Selects routes by model capability, requested effort, image/tool needs, context estimate, output reservation, and explicit fallbacks.
- Rejects unsupported hosted tool types before contacting a provider instead of silently dropping them.
- Carries request cancellation into Go upstream requests through `http.NewRequestWithContext` and interrupts rate-limit waits and retry backoff.
- Applies a request-body cap, HTTP header timeout, first-byte timeout, loopback binding, provider-specific reasoning controls, and usage accounting.
- Generates a Codex model catalog with stable IDs, context/output metadata, supported efforts, image/tool flags, and `apply_patch_tool_type: freeform`.

### Current boundaries

ACC does not have a provider-independent domain model. It has two direct translation pipelines whose shared intermediate shape is effectively OpenAI Chat Completions. That format cannot losslessly represent the full Codex Responses or Anthropic Messages protocols. Consequently, fields are either dropped on parse, approximated as text/functions, or synthesized on the way back.

Routing and protocol translation are also interleaved. `handleMessages` and `handleResponses` each perform route retries, body merging, upstream execution, stream guards, error rewriting, and metrics. This duplication makes behavior drift likely.

## Why Codex tools currently struggle

The main problem is not basic function calling. The inspected tree has strong function/custom/namespace bridges. The problem is that Codex uses a stateful event protocol, while ACC reduces each turn to Chat Completions messages and rebuilds a smaller Responses stream afterward.

### Proven losses and unsafe approximations

| Area | Current ACC behavior | Impact |
| --- | --- | --- |
| `previous_response_id` | Not present in `ResponsesRequest`; unknown JSON is discarded | ACC cannot continue provider-managed response state or locally expand a chained turn. |
| Context compaction | No `/v1/responses/compact`, `compaction_trigger`, `compaction`, or compaction state | Codex remote compaction cannot round-trip directly through ACC. |
| Reasoning summaries | Request only retains `reasoning.effort`; catalog says summaries unsupported | Summary mode, summary events, and summary content are lost. |
| Raw reasoning | Chat `reasoning_content` is not modeled in ACC's upstream response types | Raw reasoning cannot become Responses reasoning events. |
| Encrypted reasoning | No `encrypted_content` request or output item | Signed/opaque reasoning continuity cannot survive tool turns. |
| Tool search | Hosted `tool_search` is rejected | Deferred tools cannot be discovered and loaded through direct ACC. |
| Hosted MCP | Hosted `mcp` is rejected; namespace/function bridges work only for client-executed tools | Server-hosted MCP semantics are absent, though normal Codex MCP function calls can work. |
| Hosted tools | Web search, file search, code interpreter, shell, image generation, computer tools are rejected | Correct and safe for unsupported upstreams, but not feature complete. |
| Structured output | `ResponsesRequest` has no `text.format`; Chat request has no Responses-native schema output | Codex JSON-schema turns lose their contract. The separate Chat endpoint can pass raw fields, but the direct Responses translator cannot. |
| Image tool results | Structured tool output is retained as raw JSON but collapsed into a Chat tool message | Images in tool results are not represented natively to the upstream model. |
| Image output | No normalized image-output item/event | Generated images cannot round-trip. |
| Cancellation after streaming starts | Upstream context is cancelled when the HTTP request ends, but stream write errors are ignored | Disconnect handling is implicit and not asserted end to end. |
| Heartbeats | No downstream heartbeat/comment timer | A live but silent reasoning stream can look dead to clients or intermediaries. |
| Stalled stream after first byte | Only the first byte is timed | A provider can emit one byte and then stall indefinitely until the five-minute client timeout. |
| Malformed SSE | Bad JSON chunks are silently skipped | Protocol corruption can be hidden. |
| Truncated SSE | Scanner errors are logged, then ACC emits normal completion | Codex can receive a false `response.completed` after an incomplete upstream response. |
| Incomplete/failed responses | Stream bridge always ends with `response.completed` | `response.failed` and `response.incomplete` semantics are absent. |
| Usage detail | Responses output exposes input/output/total only | Cached-input and reasoning-output usage are lost on the direct Responses path. |
| Forward compatibility | Tools/items preserve some unknown fields, but top-level requests and events do not | New Codex fields disappear unless manually added. |

### Tool-specific trace

1. Codex sends function, custom, namespace, or hosted tool definitions.
2. ACC converts supported definitions into Chat Completions functions. Custom and namespace tools receive bridge names.
3. A provider streams `tool_calls[index].function.arguments` fragments.
4. ACC accumulates per-index fragments and emits function/custom Responses events.
5. Codex executes the client-side tool and sends a call output in the next request.
6. ACC converts the earlier call to an assistant Chat tool call and the result to a `role: tool` message.

This succeeds for ordinary function calls, parallel calls, MCP tools already exposed as functions/namespaces, and freeform tools that obey the wrapper. It fails when the turn also depends on Responses-only state, reasoning envelopes, deferred tool search, hosted execution, compaction, or richer result content.

### Claude Code losses

The Anthropic front door is narrower than the current Messages API:

- Request types omit `tool_choice`, stop sequences, metadata, service tier, output configuration, prompt caching controls, server tools, documents/files, citations, and adaptive thinking.
- Thinking is reduced to a budget-to-effort mapping. Thinking text, signatures, and redacted thinking blocks are not round-tripped.
- Tool-result content is flattened to text, so image/document result blocks are lost.
- Unknown content blocks are silently ignored.
- Streaming emits text and tool-use blocks but no native thinking blocks, ping events, citations, or structured errors after stream start.
- The stream parser has the same malformed/truncated stream completion problem as the Responses bridge.

## OpenCodex comparison

OpenCodex's central architectural idea is sound: parse each client request into `OcxParsedRequest`, translate provider streams into a typed `AdapterEvent` union, then render client-specific Responses events. Its adapters implement `buildRequest`, `fetchResponse`, `parseStream`, and optional unary/run-turn paths. This is an interface and testing idea, not a Bun-only capability.

### Features ACC already handles correctly

- Loopback binding and request-size protection.
- Basic model discovery/catalog injection.
- Function definitions, function calls, multiple parallel calls, fragmented arguments, results, and multi-turn message reconstruction.
- Custom/freeform tools such as `apply_patch` through a function bridge.
- Namespaced MCP functions through flatten-and-restore mapping.
- User image input and capability-aware image routing.
- Provider routing, reasoning-effort mapping, retries, explicit fallback, usage logs, and first-byte timeout.
- Reversible Codex configuration with backups, although the public ACC CLI is incomplete.

### Features ACC partly handles

- **Reasoning:** ACC maps effort and counts some reasoning tokens; OpenCodex models thinking deltas, signatures, redacted thinking, raw reasoning, and an encrypted replay envelope.
- **Streaming:** ACC emits useful text/tool events; OpenCodex uses an incremental event decoder, heartbeat events, stall timers, cancellation linking, failed/incomplete outcomes, and stricter error paths.
- **Images:** ACC handles top-level input; OpenCodex preserves images in tool results and normalizes provider detail constraints.
- **Model metadata:** ACC exposes context/effort/tool/image metadata; OpenCodex maintains broader live/provider catalogs and state migrations.
- **Continuation:** ACC reconstructs history only when the full history is resent; OpenCodex locally expands `previous_response_id`, persists bounded state, and treats misses explicitly.
- **Security:** ACC is loopback and caps bodies; OpenCodex additionally redacts provider errors, applies more destination/provider controls, bounds persisted state, and has OAuth-specific safeguards. Both remain sensitive unauthenticated local services.
- **Retries:** ACC has route fallback and selected status retry; OpenCodex separates connect timeout, stream stall, transient retry, key rotation, OAuth refresh replay, and provider-specific recovery.

### Features ACC does not currently handle

- `previous_response_id` storage/expansion.
- Responses compact v1 and compaction-trigger v2 compatibility.
- Reasoning summary/raw/encrypted event round trips.
- Tool-search calls and deferred tool reinjection.
- Structured Responses output.
- Heartbeats and post-first-byte stall detection.
- First-class failed/incomplete stream outcomes.
- Full Anthropic thinking signatures/redacted blocks.
- OAuth provider registry, token refresh, account pools, and provider-specific authentication.
- Native Responses passthrough and WebSocket provider transports.

### Generic ideas versus OpenCodex-specific code

Portable protocol-design ideas:

- A normalized request model that is richer than Chat Completions.
- One adapter boundary for client parse/render and another for provider build/parse.
- A normalized event stream with explicit lifecycle, usage, heartbeat, incomplete, and error events.
- Strict fixture tests for fragmented calls, replay state, compaction, reasoning, and cancellation.
- Bounded continuation state with explicit TTL/size limits.
- Secret-redacted provider errors and logs.

Tightly coupled code that ACC should not copy wholesale:

- Bun `ReadableStream`, `AbortSignal`, Zod schemas, and package/runtime wiring.
- OpenCodex's provider registry, OAuth account formats, Codex history database migration, dashboard, configuration migrations, sidecars, and virtual models.
- OpenCodex-specific encrypted `ocx1:`/`ocxr1:` compatibility envelopes unless ACC adopts and documents an equivalent format.
- Its large set of provider quirks that ACC does not support or need.

OpenCodex is MIT licensed. Any future copied code, fixtures, or substantial ports must retain its copyright and MIT notice. Reimplementing the design from protocol behavior and tests avoids unnecessary coupling but does not erase attribution duties for copied material.

## Repositories studied

### Selection summary

| Repository | Commit inspected | Language/runtime | License | Activity snapshot | Why selected |
| --- | --- | --- | --- | --- | --- |
| [ATruePerson/acc](https://github.com/ATruePerson/acc) | `f34138c` plus dirty local changes | Go | Apache-2.0 | Local active project | Subject of the analysis. |
| [lidge-jun/opencodex](https://github.com/lidge-jun/opencodex) | `e61dfba` | TypeScript/Bun | MIT | ~2.1k stars; v2.7.30 released 2026-07-21 | Required, richest Codex compatibility implementation studied. |
| [musistudio/claude-code-router](https://github.com/musistudio/claude-code-router) | `e11bb8b` | TypeScript/Node 22, Electron optional | MIT | ~36k stars; v3.0.15 on 2026-07-20 | Mature Claude routing, plugin/provider boundaries, broad tests. |
| [BerriAI/litellm](https://github.com/BerriAI/litellm) | `212a921` | Python/ASGI proxy, optional Rust pieces | MIT outside `enterprise/`; separate enterprise terms | ~54k stars; v1.93.0 on 2026-07-19 | Production gateway breadth, routing, auth, observability, Responses/Messages coverage. |
| [openai/codex](https://github.com/openai/codex) | `5120032` | Rust/Tokio | Apache-2.0 | ~100k stars; rust-v0.144.6 on 2026-07-18 | Authoritative client protocol and model-catalog contract. |
| [icebear0828/codex-proxy](https://github.com/icebear0828/codex-proxy) | `64df7c9` | TypeScript/Node/Hono | Non-commercial source license | ~1.6k stars; v2.0.84 on 2026-07-18 | Direct Anthropic/Responses translation, continuation, heartbeat, structured-output tests. |
| [raine/claude-code-proxy](https://github.com/raine/claude-code-proxy) | `0b79fbc` | Rust/Tokio/Axum | MIT | ~392 stars; v0.1.22 on 2026-07-20 | Focused single-binary proxy with OAuth, WebSocket, redaction, and continuation tests. |

Star counts did not decide the set. Claude Code Mux was rejected because its HEAD was dated 2025-11-20. Free Claude Code was rejected because its HEAD was dated 2026-04-26 and its adoption/activity was materially weaker than the final focused candidate.

### Per-project findings

#### OpenCodex

- **Clients/providers:** Codex Responses and Claude Messages fronts; OpenAI Chat/Responses, Anthropic, Google, Cursor, Kiro, and other provider adapters.
- **Protocol behavior:** typed normalized context/events; freeform/custom tools; namespace restoration; tool search; reasoning envelopes; image tool results; continuation state; compact v1/v2; HTTP and selected native transports.
- **Configuration/security:** local JSON config with migrations, loopback defaults, private-destination gates, OAuth/key stores, bounded response-state persistence, redaction, connect and stall timeouts.
- **Tests:** extensive focused tests around parsers, event streams, compaction, OAuth, providers, retries, images, continuation, history, and security.
- **Best idea:** request and event normalization before provider-specific translation.
- **Biggest weakness:** very large scope and fast-moving Codex-specific state/config behavior; adopting it permanently would make ACC depend on a second product, runtime, release cycle, and local state system.

#### Claude Code Router

- **Clients/providers:** Claude Code first, with Codex/local-agent integration and many provider presets.
- **Protocol behavior:** transformer/plugin pipeline, route strategies, upstream retry policy, credential pools, model catalogs, MCP discovery/tool hubs, media sidecars.
- **Configuration/security:** rich profiles and provider manifests; header sanitization and host restrictions; larger desktop/management surface increases attack and maintenance area.
- **Tests:** about 95 test/spec files in the inspected tree, including architecture, integration, disconnect, routing, MCP, provider, and UI tests.
- **Best idea:** explicit plugin/transformer and provider-manifest boundaries let protocol quirks remain local.
- **Biggest weakness:** product breadth and Electron/Node packaging are far beyond ACC's gateway needs; copying the topology would overbuild ACC.

#### LiteLLM

- **Clients/providers:** broad OpenAI, Responses, Anthropic Messages, MCP, and provider coverage.
- **Protocol behavior:** mature routing/fallback, per-provider transformations, streaming iterators, Responses bridges, structured output, tool handling, model metadata, and pass-through endpoints.
- **Configuration/security:** virtual keys, budgets, model access, admin database, guardrails, audit/observability. These are valuable for a shared organizational gateway but heavy for a personal loopback binary.
- **Tests:** thousands of test files covering provider translations, proxy security, routing, Responses, Anthropic, MCP, and reliability.
- **Best idea:** keep provider capability and transformation logic isolated and test it against a shared contract.
- **Biggest weakness:** Python dependency and feature surface create a much heavier install, runtime, and maintenance burden than ACC needs. The mixed open-source/enterprise tree also requires license care.

#### OpenAI Codex

- **Clients/providers:** authoritative Codex CLI/App implementation with custom Responses providers.
- **Protocol behavior:** function, custom/freeform, tool-search, MCP, encrypted content, reasoning summaries, compact v1/v2, previous response IDs, model metadata, and explicit failed/incomplete processing.
- **Configuration/security:** provider configuration and an official strict Responses proxy illustrate loopback, header filtering, request limits, and credential isolation. Its security history also shows loopback alone is not a complete authorization boundary.
- **Tests:** extensive Rust suites for model catalogs, items, apply patch, MCP, compaction, history, errors, and provider behavior.
- **Best idea:** use Codex's protocol types/tests as the compatibility contract instead of inferring behavior from UI success.
- **Biggest weakness for reuse:** it is a client, not a multi-provider translation gateway. Its internal types change with Codex and should inform ACC fixtures, not become a runtime library dependency.

#### Codex Proxy

- **Clients/providers:** Responses, Anthropic, OpenAI, Gemini, Codex subscription/API-key upstreams.
- **Protocol behavior:** native Responses passthrough, Anthropic-to-Codex translation, previous-response/session affinity, reasoning replay, compact endpoint, structured output, heartbeats, partial/premature stream handling, parallel tools.
- **Configuration/security:** API-key middleware and account pools; dashboard/control surface; explicit loopback recommendation. The non-commercial license prevents treating it as a normal reusable open-source code source.
- **Tests:** strong unit, integration, contract, e2e, and optional real-upstream suites.
- **Best idea:** separate response processing, metadata collection, streaming lifetime, retry recovery, and implicit resume rather than embedding them in one handler.
- **Biggest weakness:** source is not licensed for ordinary commercial reuse, and much of the implementation is specialized for ChatGPT account routing rather than ACC's free-provider role.

#### Claude Code Proxy

- **Clients/providers:** Claude Messages front with Codex, Cursor, Grok, and Kimi subscription-backed providers.
- **Protocol behavior:** provider-specific request reducers, live stream translation, WebSocket support, previous-response continuation/retry, token counting, images, tool bridging.
- **Configuration/security:** loopback default, OAuth PKCE/device flows, macOS Keychain support, single-flight refresh, traffic/log redaction, bounded captures.
- **Tests:** mock HTTP/WebSocket cutover tests, cancellation/continuation, auth, redaction, tools, and server binding.
- **Best idea:** a single compiled binary can still support sophisticated protocol adapters, OAuth, WebSockets, and stateful continuation. This directly weakens the argument that ACC must adopt Bun for those features.
- **Biggest weakness:** it translates Claude Code into a smaller set of subscription-backed services and does not solve full Codex-as-client compatibility.

## Architecture comparison

| Architecture | Strength | Main cost | Verdict |
| --- | --- | --- | --- |
| A. Keep all runtime code in Go | One binary, low memory/startup, simple process/port lifecycle, strong concurrency and cancellation, existing tests and deployment retained | More explicit code for evolving JSON unions/SSE events; protocol iteration is slower without strict schemas and fixtures | **Choose, with a redesigned protocol layer.** |
| B. Rewrite in Bun/TypeScript | Fast schema iteration, excellent discriminated unions, natural fit with OpenCodex ideas and web streams | High parity risk, full migration, dependency/runtime packaging, cross-platform Bun risk, loss of mature Go routing behavior | Reject. No evidence that a rewrite fixes the real problem faster than focused Go work. |
| C. Rewrite in Python | Fast experiments, broad provider ecosystem, excellent evaluation/data tooling | Heavier packaging, weaker single-binary story, runtime/dependency management, more care for high-concurrency streams and type invariants | Reject for the proxy core; use for tools only. |
| D. Permanent Go + Bun sidecar | Lets each language specialize; quickest way to reuse OpenCodex behavior | Two services, ports, logs, installers, schemas, versions, failures, startup ordering, latency, and rollback paths | Reject as the target. Accept only as the existing temporary bridge. |
| E. Go core with normalized/generated protocol layer | Keeps one process while adopting OpenCodex's best structural ideas and Codex-derived fixtures | Requires disciplined schema/event design and a staged refactor | **Primary implementation shape.** |

### Language-specific assessment

**Go:** JSON and SSE are not barriers. The existing code already handles parallel fragmented tool calls, stream flushing, cancellation, retry, and provider controls. Go needs tagged union conventions, strict decoding, a real SSE decoder, and contract fixtures. WebSockets and OAuth are well-supported libraries and are demonstrated in the Rust single-binary comparison; they do not justify a JavaScript runtime by themselves.

**Bun/TypeScript:** The real advantage is developer speed around unstable schemas and event unions. Keep it available for fixture extraction or a prototype adapter. Do not make it a permanent service until measured compatibility work shows a sustained advantage larger than the operational cost.

**Python:** Best used where startup, memory, and distribution are secondary: provider probes, captured-stream normalization, fuzz generation, benchmark orchestration, compatibility matrices, regression reports, and statistical evaluation. It should communicate with ACC as an external test client, not sit in the production request path.

## Recommended architecture

### Runtime ownership

Keep one Go process responsible for:

- loopback HTTP server and local authentication boundary;
- client adapters for Anthropic Messages and Codex Responses;
- provider adapters for Chat Completions, native Responses, Anthropic, and future transports;
- normalized request and event models;
- SSE/WebSocket parsing and rendering;
- routing, rate limits, retries, fallback, cancellation, provider health, config, usage, dashboard, and process management;
- model catalog and provider authentication interfaces.

Nothing moves to a permanent Bun service now. TypeScript may be used in repository-only compatibility scripts if consuming Codex/OpenCodex fixtures is significantly easier there. Python owns evaluations and research tooling only.

OpenCodex remains the supported compatibility bridge during migration, hidden behind ACC commands. It is removed as a required dependency only after direct ACC passes the same compatibility suite. ACC should eventually speak directly to Codex and should continue supporting Claude Code directly.

### Installation, updates, and rollback

- Ship one signed/versioned `acc` binary containing the gateway and CLI.
- During transition, `acc codex setup` may install or locate a pinned OpenCodex package in an ACC-owned versioned directory, not as an unpinned global dependency.
- Record ACC/OpenCodex/config versions in an atomic restore manifest before changes.
- `acc codex start` coordinates the two processes only during the bridge phase.
- Direct mode becomes the default only after the compatibility gate passes; bridge mode remains one release behind as rollback.
- Updates download to a versioned location, run `doctor` and compatibility smoke checks, atomically switch the active version, and retain the previous binary/config manifest.
- `acc codex restore` always restores the exact pre-ACC Codex configuration. `remove` also deletes only ACC-owned integration files after confirmation.

## Proposed component map

Conceptual future layout, not an instruction to create every file immediately:

```text
cmd/acc/                         CLI dispatch and user-facing commands
internal/server/                 loopback server, auth, limits, lifecycle
internal/protocol/model.go       normalized request/context/content/tool model
internal/protocol/event.go       normalized event stream
internal/protocol/errors.go      typed incomplete/failure/cancel outcomes
internal/client/codex/           Responses parse, render, continuation, compact
internal/client/anthropic/       Messages parse and render
internal/provider/chat/          OpenAI Chat Completions adapter
internal/provider/responses/     native Responses adapter/passthrough
internal/provider/anthropic/     native Anthropic adapter
internal/stream/sse/             incremental SSE decoder/encoder, heartbeats
internal/stream/ws/              optional provider WebSocket transport
internal/catalog/                model metadata and capability validation
internal/auth/                   provider auth interface and implementations
internal/routing/                route selection, retry, fallback, health
internal/state/                  bounded continuation and compaction state
internal/metrics/                normalized usage and logs
compat/fixtures/                 sanitized protocol request/event fixtures
compat/matrix/                   generated capability expectations
tools/eval/                      Python probes, fuzzing, benchmarks, reports
```

### Normalized request

```go
type Request struct {
    Model             string
    Instructions      []Instruction
    Items             []Item
    Tools             []Tool
    ToolChoice        ToolChoice
    ParallelTools     *bool
    Output            OutputConstraint
    Reasoning         ReasoningRequest
    Continuation      Continuation
    Limits            GenerationLimits
    Metadata          map[string]json.RawMessage
}
```

The item union must represent system/developer/user/assistant text, refusal, images, files, reasoning summary/raw/encrypted data, function calls, freeform calls, tool-search calls, MCP namespace calls, tool outputs with structured/image/encrypted content, compaction markers/summaries, and forward-compatible unknown items. Unknown items must be either preserved or rejected explicitly, never silently dropped.

### Adapter responsibilities

**Client adapter:** validate and parse a client wire request, preserve forward-compatible data, declare required capabilities, and render normalized results/errors back to that client.

**Provider adapter:** declare capabilities, build an upstream request, parse unary/streaming responses, normalize provider errors/usage, and never make routing decisions.

**Stream parser:** incrementally decode SSE or WebSocket frames, preserve event names/IDs/retry fields, bound frame sizes, distinguish EOF from valid completion, and surface malformed/truncated input.

**Event bridge:** enforce lifecycle/order invariants and render client events. It owns output indexes, call IDs, argument fragments, completion status, and final usage.

**Model catalog:** source stable IDs, limits, efforts, modalities, tool support, summary support, apply-patch type, and provider route requirements. Validation happens before provider contact.

**Authentication provider:** resolve credentials without exposing them to logs, refresh with a single-flight guard, identify account/tenant headers, and return typed auth failures. ACC's initial API-key implementation stays simple.

**Retry policy:** classify errors by phase and replay safety. It receives a fully rebuildable normalized request and cannot silently change requested capabilities or effort.

### Normalized event stream

Minimum events:

```text
response_start(id, model)
text_start(item_id)
text_delta(text)
text_end
reasoning_start(item_id)
reasoning_delta(text)
reasoning_raw_delta(text)
reasoning_signature_delta(data)
reasoning_encrypted(data)
reasoning_end
tool_call_start(item_id, call_id, kind, namespace, name)
tool_call_arguments_delta(bytes)
tool_call_end
tool_output(call_id, content, is_error)
image_output(item_id, mime_type, data_or_url)
usage(input, cached_input, output, reasoning_output, total)
heartbeat
response_complete
response_incomplete(reason)
response_error(code, safe_message, retryable)
```

The bridge must reject illegal sequences, such as arguments before a call start, duplicate terminal events, a tool result with no matching call, or EOF with open items. It must never convert parser failure into normal completion.

### Compatibility mapping

- Codex Responses items map directly into normalized items. Custom/freeform, namespace/MCP, and tool-search remain distinct kinds rather than being permanently flattened. Flattening occurs only inside a provider adapter that requires functions.
- Anthropic thinking maps to normalized reasoning with signature/redacted fields. Tool use maps to normalized calls; tool results retain block content rather than flattening to text.
- Chat Completions adapters may encode unsupported kinds through documented reversible bridges. The bridge metadata lives in per-request adapter state, not global maps.
- Native Responses providers bypass lossy Chat translation but still pass through validation, routing, events, metrics, and redaction.

## Codex compatibility requirements

Direct ACC support is not ready until all of these are proven:

- text and reasoning streaming with correct event order and terminal status;
- one and multiple parallel function calls;
- arguments split across arbitrary byte boundaries;
- multi-turn function output continuity;
- custom/freeform `apply_patch` calls and outputs;
- namespace/MCP calls and outputs;
- tool-search call/output and deferred tool reinjection;
- images in user input and tool output;
- structured `text.format` output;
- reasoning summary, raw reasoning, and encrypted/opaque replay;
- `previous_response_id` hit, miss, restart, TTL, and size limits;
- compact v1 and compaction-trigger v2;
- cancellation before headers, mid-stream, during retry, and during stall;
- heartbeats and inactivity timeout after the first byte;
- `response.failed` and `response.incomplete` instead of false completion;
- malformed, oversized, truncated, and out-of-order upstream events;
- exact usage, cached usage, reasoning usage, effort, model, and context metadata;
- fallback without losing tools, effort, images, output schema, or state;
- native Responses passthrough where supported;
- catalog parsing by the installed Codex client.

## Claude Code compatibility requirements

- System block order and cache-control preservation.
- Text, image, document/file, citation, and tool-result block fidelity.
- Function definitions, tool choice, parallel calls, ordering, errors, and results.
- Extended/adaptive thinking, signatures, redacted thinking, and replay.
- Stop sequences, output configuration, structured output, metadata, and service tier.
- Server-tool policy: native passthrough, safe emulation, or explicit rejection.
- Correct Anthropic SSE lifecycle, pings, mid-stream errors, cancellation, and usage.
- `/v1/messages/count_tokens` or a documented compatible local estimator for client compaction.
- Model aliases and per-model context metadata without disabling Claude's safety compaction incorrectly.

## CLI and port design

Public commands:

```text
acc codex setup
acc codex start
acc codex stop
acc codex status
acc codex doctor
acc codex restore
acc codex remove

acc claude setup
acc claude start
acc claude stop
acc claude status
acc claude doctor
acc claude restore
```

During transition, these commands hide the OpenCodex implementation. Users should never need to invoke `scripts/opencodex/acc-opencodex`.

Port strategy:

```text
Bridge phase:
Codex -> OpenCodex 127.0.0.1:10100 -> ACC 127.0.0.1:9999 -> provider

Direct phase:
Codex/Claude Code -> ACC 127.0.0.1:9999 -> provider
```

Never let two processes contend for one port. `doctor` must resolve listeners to expected executables, verify loopback binding, detect proxy loops, and report the configured front door. ACC should add a local bearer token or origin/content-type protection for sensitive local mutation/control endpoints; loopback alone does not stop malicious local processes or browser-origin request abuse. Dashboard restart/clear endpoints require particular attention.

## Migration plan

### Stage 0: preserve the working bridge

- **Scope:** keep Codex -> OpenCodex:10100 -> ACC:9999; document it as partial and reversible.
- **Expected files:** existing OpenCodex wrapper/docs only.
- **Risks:** dependency drift, two-process confusion, untested tool surfaces.
- **Tests:** current health/model/basic-stream smoke plus pinned version check.
- **Done:** current integration remains usable and `restore` returns native Codex exactly.
- **Rollback:** stop OpenCodex and restore its saved Codex config.

### Stage 1: ACC-owned CLI lifecycle

- **Scope:** implement the public `acc codex` setup/start/stop/status/doctor/restore/remove commands around the bridge.
- **Expected files:** `cli.go`, `codex_app.go`, a focused bridge lifecycle package, tests, docs.
- **Risks:** config corruption, wrong process termination, ownership ambiguity.
- **Tests:** temporary homes, occupied ports, missing binaries, version mismatch, repeated setup/remove, exact restore bytes.
- **Done:** no public script paths; every mutation is backed up, atomic, idempotent, and diagnosable.
- **Rollback:** restore manifest plus previous ACC binary.

### Stage 2: compatibility suite first

- **Scope:** add sanitized request/upstream/event fixtures and a matrix covering all Codex and Claude requirements above.
- **Expected files:** `compat/fixtures`, `compat/matrix`, Go black-box tests, `tools/eval` Python scripts.
- **Risks:** fixtures accidentally contain prompts or tokens; tests mirror one implementation bug.
- **Tests:** secret scanner, schema validation, golden event ordering, mutation/fuzz tests.
- **Done:** the suite fails against known ACC gaps and passes against each intentionally supported current behavior.
- **Rollback:** test-only files can be removed without runtime effect.

### Stage 3: normalized model and event stream

- **Scope:** introduce internal protocol types and adapters behind existing handlers without changing public behavior.
- **Expected files:** `internal/protocol`, `internal/stream`, initial client/provider adapter packages.
- **Risks:** double translation, ordering regressions, hidden allocations.
- **Tests:** old suite plus golden normalized round trips, benchmarks, race tests.
- **Done:** Messages and Responses handlers share upstream execution and event lifecycle code; current behavior is parity-clean.
- **Rollback:** feature flag selects the old path for one release.

### Stage 4: close direct Codex gaps in Go

- **Scope:** add strict SSE, incomplete/error events, heartbeats/stall timeout, reasoning envelopes, continuation, structured output, tool search, and compaction in small slices.
- **Expected files:** Codex client adapter, state, SSE, catalog, provider adapters.
- **Risks:** state privacy, incompatible envelopes, retrying non-replayable turns.
- **Tests:** one failing fixture per slice before code, bounded-state and crash/restart tests, installed-Codex smoke tests.
- **Done:** the full direct compatibility matrix passes. A Bun spike is allowed only if a named slice misses an agreed delivery/quality target.
- **Rollback:** keep bridge mode selectable.

### Stage 5: direct Codex canary

- **Scope:** opt-in `acc codex setup --direct`; bridge remains default.
- **Expected files:** CLI mode selection and doctor/reporting.
- **Risks:** real Codex updates expose unmodeled events; history differences.
- **Tests:** clean temporary Codex homes, multi-turn tools, app/CLI, cancellation, compaction, fallback, vision.
- **Done:** multiple release cycles with no bridge-only compatibility failures.
- **Rollback:** one command restores bridge mode and previous config.

### Stage 6: retire required OpenCodex

- **Scope:** make direct ACC default; keep optional bridge troubleshooting for one deprecation window.
- **Expected files:** installation/update docs and removal logic.
- **Risks:** losing a fast compatibility escape hatch.
- **Tests:** upgrade from bridge installs, downgrade, restore, remove, offline installation.
- **Done:** all compatibility tests pass and release telemetry/manual reports show no required OpenCodex path.
- **Rollback:** reinstall the pinned bridge bundle and restore its manifest.

## Testing layer

Every fixture should contain four artifacts: client request, expected normalized request, upstream wire/events, and expected client wire/events. Required cases:

1. text unary and streaming;
2. one function call;
3. multiple parallel calls with interleaved indexes;
4. arguments fragmented at every byte boundary;
5. multi-turn results, including out-of-order/missing outputs;
6. custom/freeform `apply_patch`;
7. namespace and hosted/client MCP distinctions;
8. tool search and loaded-tool continuation;
9. reasoning summary/raw/signature/encrypted replay;
10. user images, tool-result images, and image output;
11. structured JSON schema output;
12. previous-response hit/miss/restart/expiry;
13. compact v1 and v2;
14. cancellation in each lifecycle phase;
15. heartbeat and post-first-byte stall;
16. fallback with invariant preservation;
17. malformed JSON, malformed SSE fields, oversized frame, abrupt EOF, duplicate terminal;
18. exact usage and metadata.

Use Go fuzz tests for parsers and lifecycle invariants. Use Python to generate fragmentation permutations, compare provider recordings, produce capability matrices, and summarize regressions. Never commit raw provider recordings until a sanitizer verifies authorization, cookies, account IDs, prompts, local paths, and opaque tokens are absent.

## Risks

- **Protocol drift:** Codex changes quickly. Pin fixtures to client versions and run a scheduled compatibility job against new Codex releases before updating ACC.
- **False compatibility:** HTTP 200 and visible text are not proof. Require event, state, tool, and terminal assertions.
- **State privacy:** continuation/compaction stores can contain prompts and images. Use mode `0600`, bounded size/TTL, explicit ownership, and deletion on remove.
- **Local security:** loopback services can still be abused by another local process or a browser request. Add authentication/origin protections and protect dashboard mutation endpoints.
- **Retry corruption:** never replay a request after partial client-visible output unless the adapter can prove replay safety and deduplicate IDs.
- **Schema divergence:** generate or test schemas from one canonical internal model; do not maintain Go/TS JSON shapes by hand across processes.
- **License contamination:** OpenCodex and Claude Code Proxy are MIT, Codex is Apache-2.0, and copied code requires notices. Codex Proxy's non-commercial license makes direct code reuse inappropriate without separate permission. LiteLLM enterprise code is separately licensed.
- **Maintenance scope:** OAuth pools, sidecars, enterprise key management, and dozens of providers can swallow the project. Add only behavior required by ACC's supported clients/providers.
- **Migration complexity:** keep old and new paths selectable only for a bounded release window; permanent dual implementations would double bugs.

## What not to do

- Do not rewrite ACC in TypeScript or Python merely because another repository has more features or stars.
- Do not copy OpenCodex wholesale or make its internal config/history database ACC's architecture.
- Do not introduce a permanent Bun sidecar before compatibility tests prove a narrow Go blocker.
- Do not use Chat Completions as ACC's permanent normalized protocol model.
- Do not silently drop unknown items/events, malformed chunks, tool definitions, reasoning, or output constraints.
- Do not emit `response.completed` after parser failure or abrupt EOF.
- Do not retry after client-visible partial output without a replay-safe design.
- Do not expose ACC or OpenCodex on the LAN by default.
- Do not let ACC and OpenCodex bind the same address and port.
- Do not mutate Codex history/config without atomic backup, exact restore, and ownership tracking.
- Do not treat README claims, health checks, or a single text response as proof of tool compatibility.
- Do not copy non-commercial Codex Proxy code into ACC.

## Final verdict

**KEEP GO**

Build Architecture E: one Go binary with a strict normalized request model, normalized event stream, client/provider adapter boundaries, Codex-derived compatibility fixtures, and Python evaluation tooling. Keep OpenCodex hidden behind `acc codex` as a temporary bridge while that work lands. Do not add a permanent Bun process unless a later measured experiment proves that one narrow protocol adapter cannot be maintained reliably in Go.

This choice preserves ACC's current strengths and attacks the proven cause of failure: information is lost because Chat Completions is being used as the internal protocol, not because Go is incapable of expressing Codex or Claude behavior.
