# Codex integration

The supported temporary path is:

```text
Codex Desktop -> OpenCodex 127.0.0.1:10100 -> ACC 127.0.0.1:<port> -> provider
```

OpenCodex is an adapter and lifecycle dependency, not an ACC-owned fork. ACC embeds the small helper at `scripts/opencodex/acc-opencodex` and writes a matching copy to `~/.config/acc/acc-opencodex` when needed.

## Commands

```text
acc codex setup
acc codex start [--model MODEL]
acc codex stop
acc codex status
acc codex doctor
acc codex restore
acc codex remove
```

`setup` starts ACC if necessary, discovers the live Codex catalog from `/v1/models`, and adds only OpenCodex's `acc` provider. `start` starts OpenCodex and points Codex Desktop at its loopback Responses endpoint. `stop` stops OpenCodex and restores the saved Codex config/catalog. `restore` restores only the saved Codex files. `remove` also removes only the generated OpenCodex `acc` provider and leaves unrelated providers intact.

The older `acc codex [path]` launcher remains available for direct ACC testing. `acc codex --restore` is retained as a compatibility alias for `acc codex restore`.

## Backups and rollback

Before ACC changes Codex Desktop, it saves `~/.codex/config.toml` and `~/.codex/acc-models.json` to `~/.config/acc/codex-restore.json`. OpenCodex makes timestamped backups beside its config. Restore is atomic and does not delete provider credentials. If the bridge is unhealthy, run:

```text
acc codex restore
acc codex remove
```

## Current boundary

The direct ACC Responses gateway supports normalized text, reasoning summaries, function/custom/namespace tools, usage, fallback selection, and local `previous_response_id` continuity. OpenCodex remains in front for the desktop lifecycle and auth/session behavior. Live tool, vision, fallback, and Codex Desktop event checks still require a configured provider and are reported as `SKIPPED` by `acc codex doctor` when no safe smoke test is available.
