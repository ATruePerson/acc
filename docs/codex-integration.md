# Codex integration

The supported path is direct and loopback-only:

```text
Codex Desktop -> ACC 127.0.0.1:<port> -> exact provider/model
```

OpenCodex is not a runtime, setup, auth, catalog, or lifecycle dependency.
ACC does not uninstall or modify a user's existing OpenCodex installation.

## Commands

```text
acc codex setup [--model PROVIDER/MODEL]
acc codex start [--model PROVIDER/MODEL]
acc codex stop
acc codex status
acc codex doctor
acc codex restore
acc codex remove
```

`setup` backs up Codex and writes the direct Responses provider without
starting a service. `start` avoids duplicates and records ownership only if it
launches ACC. `stop` terminates only that recorded, command-verified process.
`status` never displays secrets. `doctor` is read-only. `restore` restores the
exact saved config after timestamp-backing up the current file. `remove`
restores the saved baseline when available, otherwise removes ownership-marked
ACC entries only.

## Catalog and routing

Catalog IDs are real `provider/upstream-model` values. They are deterministic,
unique, and exclude the Claude aliases `opus`, `sonnet`, and `haiku`. Native
Kimi, xAI, and Anthropic model discovery refreshes after login and setup;
non-secret results are cached atomically. A static seed is used only when live
discovery is unavailable. Direct Codex IDs do not inherit Claude alias fallback
chains.

## Authentication

```text
acc auth list
acc auth login kimi
acc auth login xai
acc auth login grok
acc auth login anthropic
acc auth status [PROVIDER]
acc auth logout PROVIDER
```

Kimi uses device authorization. xAI/Grok OAuth uses OIDC discovery, PKCE,
state, nonce, and an IPv4 loopback callback, but remains experimental because
endorsed third-party OAuth support could not be confirmed. Anthropic API keys
are stable. Claude Code import is explicit and read-only; the copied
subscription credential is not advertised for inference. Unsupported account
impersonation is intentionally absent.

OAuth credentials are isolated by provider in macOS Keychain. Refresh is lazy,
single-flight, rotation-safe, cancellation-aware, and replayed once after a
401. The optional file store requires explicit environment opt-in and writes
private atomic files outside the repository.

## Backups

ACC preserves unrelated Codex projects, MCP servers, trust settings, sandbox
settings, approvals, preferences, and history. The exact restore state is in
`~/.config/acc/codex-restore.json`; timestamped config backups sit beside
`~/.codex/config.toml`. Generated credentials and catalogs never contain one
another.

## Migration and troubleshooting

`setup` detects legacy OpenCodex markers and port `10100`, replaces only the
active ACC connection, and preserves unrelated provider tables. It never reads,
moves, deletes, or refreshes OpenCodex credentials. Run `acc codex doctor` if
Codex cannot connect, then `acc codex restore` to return to the exact baseline.
If a provider model disappears, log in again to refresh discovery or choose a
different real catalog ID; a catalog entry does not guarantee current quota.

For OAuth trouble, run `acc auth status PROVIDER`, then log out and log in again.
For an API-key rotation, revoke the old key at the provider, update the private
`~/.config/acc/.env`, and restart ACC. Never paste a key into `config.json`,
`config.toml`, a model catalog, logs, an issue, or a commit.
