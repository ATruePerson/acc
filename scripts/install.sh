#!/bin/sh
# acc installer — downloads the latest prebuilt binary for your OS/arch.
#
#   curl -fsSL https://raw.githubusercontent.com/ATruePerson/acc/main/install.sh | sh
#
# Installs to ~/.local/bin/acc (no sudo, no Go toolchain needed).
set -eu

REPO="ATruePerson/acc"
BINDIR="${ACC_BINDIR:-$HOME/.local/bin}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
  x86_64 | amd64) arch="amd64" ;;
  arm64 | aarch64) arch="arm64" ;;
  *) echo "Unsupported architecture: $arch" >&2; exit 1 ;;
esac
case "$os" in
  darwin | linux) ;;
  *) echo "Unsupported OS: $os (use 'go install' instead)" >&2; exit 1 ;;
esac

asset="acc-${os}-${arch}.tar.gz"
url="https://github.com/${REPO}/releases/latest/download/${asset}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $asset ..."
if ! curl -fsSL "$url" -o "$tmp/$asset"; then
  echo "Download failed. No prebuilt binary yet? Try: go install github.com/${REPO}@latest" >&2
  exit 1
fi

tar -xzf "$tmp/$asset" -C "$tmp"
mkdir -p "$BINDIR"
mv "$tmp/acc-${os}-${arch}" "$BINDIR/acc"
chmod +x "$BINDIR/acc"

echo "Installed acc to $BINDIR/acc"
case ":$PATH:" in
  *":$BINDIR:"*) ;;
  *) echo "NOTE: $BINDIR is not on your PATH. Add this to your shell profile:"
     echo "      export PATH=\"$BINDIR:\$PATH\"" ;;
esac
echo
echo "Next: run  acc setup"
