# Normalized protocol boundary

ACC keeps one internal boundary between the Codex Responses API and provider-specific Chat Completions APIs.

```text
Codex Responses request
        |
        v
ResponsesRequest -> normalized items/tools/reasoning/state
        |
        v
OpenAIRequest -> provider adapter -> upstream
        |
        v
OpenAIResponse/SSE -> normalized output/events
        |
        v
Codex Responses response/events
```

## Invariants

- The public model is the Codex catalog slug. ACC resolves it through `models`, then applies the configured capability and fallback chain.
- Requested reasoning effort is validated against that model's advertised levels. It is never silently renamed or lowered.
- Tool calls keep their call ID, namespace, custom-tool input, and provider thought signature where the upstream supplies one.
- Unknown request, response, item, and tool fields are retained in `Extra` and re-emitted when the normalized value is serialized. This gives newer Codex fields a forward-compatible path while ACC learns their semantics.
- `previous_response_id` is resolved against an in-process, loopback-local response store. ACC does not upload conversation state to implement continuity.
- A streamed response is `completed` only when the upstream sends `[DONE]`. Scanner errors or an early upstream close produce `response.incomplete`.

## Normalized types

`ResponsesRequest`, `ResponsesItem`, `ResponsesResponse`, `ResponsesUsage`, and `ResponsesSummary` are the current shared representation. Provider adapters consume `OpenAIRequest` and produce `OpenAIResponse`; the Responses handler owns the conversion and event sequencing.

The implementation is deliberately incremental. It does not attempt to duplicate every provider protocol. New provider-specific behavior belongs in the translation or route adapter boundary, not in the CLI or Codex lifecycle code.

## State and security

The response store is process-local and guarded by a mutex. It is not a durable transcript database, and restarting ACC invalidates old response IDs. The gateway remains bound to `127.0.0.1`; the OpenCodex bridge is also configured for loopback and private-network access only.
