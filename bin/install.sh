#!/usr/bin/env bash
set -euo pipefail

# Downloads the correct web-researcher-mcp binary for the current platform.
# Called by the Claude Code plugin hook on session start.

REPO="zoharbabin/web-researcher-mcp"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
BINARY="web-researcher-mcp"

# Resolve latest version
VERSION="${VERSION:-}"
if [ -z "$VERSION" ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | sed -E 's/.*"v([^"]+)".*/\1/')
fi

# Skip if already installed at this version
if command -v "$BINARY" &>/dev/null; then
  CURRENT=$("$BINARY" --version 2>/dev/null || echo "")
  if [ "$CURRENT" = "$VERSION" ] || [ "$CURRENT" = "v$VERSION" ]; then
    exit 0
  fi
fi

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  darwin) ;;
  linux)  ;;
  mingw*|msys*|cygwin*) OS="windows" ;;
  *) echo "Unsupported OS: $OS" >&2; exit 1 ;;
esac

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

EXT=""
if [ "$OS" = "windows" ]; then
  EXT=".exe"
  BINARY="${BINARY}${EXT}"
fi

ARCHIVE="web-researcher-mcp_${VERSION}_${OS}_${ARCH}"
if [ "$OS" = "windows" ]; then
  ARCHIVE="${ARCHIVE}.zip"
else
  ARCHIVE="${ARCHIVE}.tar.gz"
fi

DOWNLOAD_URL="https://github.com/$REPO/releases/download/v${VERSION}/${ARCHIVE}"

mkdir -p "$INSTALL_DIR"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

echo "Installing web-researcher-mcp v${VERSION} (${OS}/${ARCH})..."
curl -fsSL "$DOWNLOAD_URL" -o "$TMPDIR/$ARCHIVE"

mkdir -p "$TMPDIR/extracted"
if [ "$OS" = "windows" ]; then
  unzip -qo "$TMPDIR/$ARCHIVE" -d "$TMPDIR/extracted"
else
  tar -xzf "$TMPDIR/$ARCHIVE" -C "$TMPDIR/extracted"
fi

cp "$TMPDIR/extracted/$BINARY" "$INSTALL_DIR/$BINARY"
chmod +x "$INSTALL_DIR/$BINARY"

# Ensure install dir is on PATH
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo "NOTE: Add $INSTALL_DIR to your PATH if not already present." ;;
esac

echo "Installed web-researcher-mcp v${VERSION} to $INSTALL_DIR/$BINARY"
