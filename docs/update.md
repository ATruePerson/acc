# Updating ACC

Run:

```bash
acc update
```

The command selects the latest release asset for the current macOS or Linux architecture, downloads the release archive and its published SHA-256 file, verifies the checksum, extracts only the expected ACC binary, validates that the new binary can run, and atomically replaces the installed executable.

By default, ACC updates the executable currently running when it is a normal `acc` binary. Otherwise it installs to `~/.local/bin/acc`, matching `scripts/install.sh`. Set `ACC_BINDIR` to choose another writable installation directory:

```bash
ACC_BINDIR="$HOME/bin" acc update
```

The updater changes only the ACC executable. It does not edit `~/.config/acc/`
config files (`providers.json`, `claude/`, `codex/`), provider keys, Codex
settings, subscription baselines, login files, or authentication state.

ACC release binaries are currently provided for:

- macOS arm64
- macOS amd64
- Linux arm64
- Linux amd64
