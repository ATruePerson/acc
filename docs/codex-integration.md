# Codex integration

The supported path is direct and loopback-only:

```text
Codex Desktop -> ACC 127.0.0.1:<port> -> exact provider/model
```

OpenCodex is not a runtime, setup, auth, catalog, or lifecycle dependency. ACC does not start OpenCodex or modify its credentials.

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

`setup` prepares the direct ACC Responses provider without starting a process. `start` prepares the same configuration, starts one ACC-owned process, and verifies the loopback endpoint, `/v1/responses`, selected model, and generated catalog. `stop` terminates only the recorded ACC-owned process. `restore` and `remove` both stop that owned process and return Codex to a sanitized subscription configuration. `status` never displays secrets. `doctor` is read-only.

## Durable subscription baseline

Before ACC takes control, it creates `~/.config/acc/codex-restore.json` with:

1. A raw snapshot of the original `~/.codex/config.toml` and actively referenced catalog files, including whether each file existed.
2. A sanitized subscription baseline that preserves unrelated bytes and removes only active custom model routing, ACC-owned blocks, ACC/OpenCodex loopback providers, custom catalog references, and root custom models.

A valid baseline is never overwritten by repeated `start` calls. If the current config already points to ACC or OpenCodex, the custom routing is not accepted as the subscription baseline. ACC saves the raw state for recovery and stores the sanitized result as the restore target.

Every mutation receives a private timestamped backup first. Writes are atomic. A failed configuration, catalog, process, or endpoint verification rolls the command back to its pre-command files.

## Restore and recovery

`acc codex restore` restores the sanitized baseline, not a stale raw OpenCodex configuration. It removes active references to ACC, OpenCodex, ports `9999` and `10100`, `model_catalog_json`, and provider-prefixed root models. It deletes only catalogs proven to be ACC/OpenCodex-generated and located in known managed locations. Raw baseline data and timestamped backups remain available for recovery.

If no valid baseline exists, restore enters recovery mode. It first backs up the current files, constructs a sanitized subscription baseline from the current config, restores it atomically, and reports that recovery mode was used. Repeated restore calls are safe no-ops.

ACC never deletes `~/.codex`, login files, projects, history, MCP servers, trust settings, sandbox settings, approvals, skills, or unrelated preferences. It never edits provider keys or `~/.config/acc/.env`.

## Status and doctor

Status reports:

```text
Mode: Subscription | ACC | OpenCodex | Unknown
ACC process: Running | Stopped
Codex endpoint: ...
Active model provider: ...
Active catalog: ...
Subscription baseline: Valid | Missing | Recoverable
Restart ChatGPT required: Yes | No
```

Doctor parses root TOML routing and the selected provider table. A harmless word such as `opencodex` in a comment, MCP command, or inactive provider table does not fail the check. Active port `10100`, selected OpenCodex routing, or a selected OpenCodex catalog does fail.

Codex Desktop reads provider settings when its app process starts. After `start` or `restore` changes active configuration, fully quit and reopen ChatGPT Desktop when the command or status says restart is required.

## Catalog and routing

Catalog IDs are real `provider/upstream-model` values. The Codex model selector contains only enabled models explicitly selected in `config.json` whose `catalog_visible` value is not `false`. Provider login and model discovery may refresh non-secret cached metadata, but discovered models are never added to the selector automatically. Catalog entries are deterministic, unique, and exclude the Claude aliases `opus`, `sonnet`, and `haiku`. Direct Codex IDs do not inherit Claude alias fallback chains.

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

OAuth credentials are isolated by provider in macOS Keychain. ACC verifies that Codex authentication-like files are unchanged across lifecycle mutations and never prints their contents.

For provider trouble, run `acc auth status PROVIDER`, then log out and log in again. For an API-key rotation, revoke the old key at the provider, update the private `~/.config/acc/.env`, and restart ACC. Never paste a key into `config.json`, `config.toml`, a model catalog, logs, an issue, or a commit.
