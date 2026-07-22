# Codex compatibility matrix

Status reflects the implementation and automated tests in this checkout. `Verified` means covered by local tests. `Live required` means the behavior depends on a running Codex client/provider and must be checked with `acc codex doctor` or a controlled smoke test.

| Capability | Status | Evidence / boundary |
| --- | --- | --- |
| Native `acc codex setup/start/stop/status/doctor/restore/remove` commands | Verified | CLI dispatch and lifecycle wrapper |
| Loopback-only ACC listener | Verified | Go server binds `127.0.0.1`; doctor checks listeners |
| OpenCodex loopback adapter | Verified | Helper writes `hostname=127.0.0.1`, `allowPrivateNetwork=true` |
| Live model catalog with Codex slugs | Verified | `/v1/models` uses the Codex user-agent and accepts `models` or legacy `data` |
| Text Responses unary translation | Verified | `responses_handler_test.go`, `codex_integration_test.go` |
| Text Responses SSE event translation | Verified | `TestStreamTranslateResponsesUsesCodexEvents` |
| Reasoning summary preservation | Verified | `TestResponsesReasoningSurvivesTranslation` and stream reasoning events |
| Function tools | Verified | namespace/custom/stream tests |
| Custom and namespaced tools | Verified | `responses_namespace_test.go` |
| `previous_response_id` continuity | Verified, process-local | `TestPreviousResponseIDExpandsLocalConversation`; restart invalidates IDs |
| Unknown field preservation | Verified | `TestResponsesRequestPreservesUnknownFields` |
| Truncated stream terminal state | Verified | `TestResponsesStreamWithoutDoneIsIncomplete` |
| Provider fallback and capability reroute | Verified | existing routing/fallback test suite |
| Exact provider reasoning knobs | Verified | existing route translation tests and config traits |
| Vision | Live required | route and provider dependent; doctor reports skipped |
| Codex Desktop end-to-end tool round-trip | Live required | needs OpenCodex, Codex Desktop, and a safe live provider request |
| Durable response history across ACC restart | Not implemented | current store is intentionally process-local |
| Native direct ACC replacement for OpenCodex lifecycle/auth | Not implemented | OpenCodex remains the temporary front adapter |
