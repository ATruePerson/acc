# Codex compatibility matrix

`Verified` means mocked or deterministic local tests pass. `Live required`
means a real provider or Codex client is still needed.

| Capability | Status | Boundary |
| --- | --- | --- |
| Direct `setup/start/stop/status/doctor/restore/remove` | Verified | No OpenCodex execution or configuration |
| Loopback ACC listener and OAuth callback | Verified | Explicit `127.0.0.1` binds |
| Real provider-prefixed catalog | Verified | Unique IDs; no Claude aliases; auth refresh |
| Secure provider isolation and rotation | Verified | Keychain abstraction, memory/file stores, single-flight |
| Kimi device authorization | Verified | Polling, slow-down, denial, expiry, refresh |
| xAI OIDC + PKCE | Verified, experimental | Discovery, endpoint validation, state, callback, retry |
| Anthropic API-key transport | Verified | Native Messages conversion, stream/tools/images/usage |
| Claude/Grok credential import | Verified, explicit only | Read-only detection; never automatic |
| Responses unary and SSE | Verified | Text, reasoning, tools, usage, terminal states |
| Custom, namespace, apply_patch, and MCP tools | Verified | Existing normalized Responses suite |
| `previous_response_id` | Verified, process-local | Restart invalidates stored IDs |
| Fallback and exact effort mapping | Verified | Explicit Codex chains only |
| OAuth provider smoke requests | Live required | Requires user-approved login or existing secure credentials |
| Codex Desktop end-to-end run | Live required | Requires temporary safe config and provider request |
| Durable response history across restart | Not implemented | Store remains process-local |
