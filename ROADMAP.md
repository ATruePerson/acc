# ACC roadmap

ACC stays one small Go gateway with strict client and provider boundaries. New work should improve reliability and proof, not turn the project into a general orchestration framework.

## Stable

- Anthropic Messages, OpenAI Responses, and OpenAI-compatible Chat Completions
- Claude alias routing with explicit fallbacks, direct Codex provider/model routing, exact effort mapping, and model discovery
- Loopback-only gateway, provider-key isolation, dashboards, setup, doctor, and benchmarks
- Claude Code launcher and bundled guarded MCP tools

## Stabilization gate for native Codex

Native Codex remains experimental until all of these are true:

- `go test -race ./...`, `go vet ./...`, and release builds pass
- `acc certify --full` passes text, streaming, ordinary tools, `apply_patch`, namespace/MCP bridging, and multi-turn on at least one exact Codex provider/model entry
- Setup, restore, and remove preserve unrelated user configuration
- No withdrawn model remains in the shipped default route set
- The compatibility matrix records the exact ACC and provider/model versions used for live verification

## Current hardening

- Versioned config schema with explicit backup-first migrations
- Live per-model certification reports with latency and failure details
- Optional certification-aware warnings or strict capability rejection
- Encrypted, expiring, opt-in durable `previous_response_id` state
- A maintained public roadmap and compatibility record

## Next

- CI fixture server for deterministic provider faults, stalls, malformed SSE, and error propagation
- Signed release artifacts and checksums
- Automatic stale-model detection without silently rewriting user routes
- Exportable diagnostics bundle with secret redaction
- Windows and Linux lifecycle support after macOS behavior is stable

## Not planned

- Silently switching a selected Codex provider or model
- Silently lowering reasoning effort
- Silently removing tools to make a request pass
- Advertising capabilities that were not deterministically tested or live-certified
- Exposing ACC outside loopback without a separate authenticated transport design
