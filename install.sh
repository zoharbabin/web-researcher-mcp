#!/bin/sh
set -e

# web-researcher-mcp installer
# Usage: curl -fsSL https://raw.githubusercontent.com/zoharbabin/web-researcher-mcp/main/install.sh | sh
#    or: wget -qO- https://raw.githubusercontent.com/zoharbabin/web-researcher-mcp/main/install.sh | sh
# Options (via env vars):
#   INSTALL_DIR=/custom/path  — where to put the binary (default: ~/.local/bin)
#   SKIP_MCP_REGISTER=1      — skip registering with Claude Code
#   VERSION=1.9.0             — install a specific version instead of latest
#   SKIP_CHECKSUM=1           — skip checksum verification (not recommended)

REPO="zoharbabin/web-researcher-mcp"
BINARY="web-researcher-mcp"

detect_os() {
  case "$(uname -s)" in
    Darwin*) echo "darwin" ;;
    Linux*)  echo "linux" ;;
    *)       echo "unsupported" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)  echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *)             echo "unsupported" ;;
  esac
}

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- "$1"
  else
    echo "Error: neither curl nor wget found. Install one and retry."
    exit 1
  fi
}

download() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$2" "$1"
  fi
}

verify_checksum() {
  ARCHIVE_PATH="$1"
  ARCHIVE_NAME="$2"
  CHECKSUMS_URL="$3"

  if [ "${SKIP_CHECKSUM:-0}" = "1" ]; then
    echo "  (checksum verification skipped)"
    return 0
  fi

  echo "  Verifying checksum..."
  CHECKSUMS=$(fetch "$CHECKSUMS_URL")
  if [ -z "$CHECKSUMS" ]; then
    echo "  Warning: could not download checksums.txt — skipping verification."
    return 0
  fi

  EXPECTED=$(echo "$CHECKSUMS" | grep "  $ARCHIVE_NAME\$" | cut -d' ' -f1)
  if [ -z "$EXPECTED" ]; then
    echo "  Warning: no checksum found for $ARCHIVE_NAME — skipping verification."
    return 0
  fi

  if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL=$(sha256sum "$ARCHIVE_PATH" | cut -d' ' -f1)
  elif command -v shasum >/dev/null 2>&1; then
    ACTUAL=$(shasum -a 256 "$ARCHIVE_PATH" | cut -d' ' -f1)
  else
    echo "  Warning: no sha256sum or shasum found — skipping verification."
    return 0
  fi

  if [ "$ACTUAL" != "$EXPECTED" ]; then
    echo "Error: checksum mismatch!"
    echo "  Expected: $EXPECTED"
    echo "  Got:      $ACTUAL"
    echo ""
    echo "The download may be corrupted or tampered with."
    echo "Try again or download manually from https://github.com/$REPO/releases"
    exit 1
  fi
  echo "  Checksum verified."
}

main() {
  OS=$(detect_os)
  ARCH=$(detect_arch)

  if [ "$OS" = "unsupported" ] || [ "$ARCH" = "unsupported" ]; then
    echo "Error: unsupported platform ($(uname -s)/$(uname -m))"
    echo "See https://github.com/$REPO/releases for available binaries."
    exit 1
  fi

  INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
  mkdir -p "$INSTALL_DIR"

  # Get version
  if [ -n "$VERSION" ]; then
    TAG="v$VERSION"
  else
    TAG=$(fetch "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | head -1 | cut -d'"' -f4)
    if [ -z "$TAG" ]; then
      echo "Error: could not determine latest release (GitHub API rate limit?)."
      echo "Try setting VERSION explicitly: VERSION=1.9.0 sh install.sh"
      exit 1
    fi
  fi
  VERSION="${TAG#v}"

  ARCHIVE="${BINARY}_${VERSION}_${OS}_${ARCH}.tar.gz"
  URL="https://github.com/$REPO/releases/download/$TAG/$ARCHIVE"
  CHECKSUMS_URL="https://github.com/$REPO/releases/download/$TAG/checksums.txt"

  echo "Installing $BINARY $TAG ($OS/$ARCH)..."
  echo "  from: $URL"
  echo "  to:   $INSTALL_DIR/$BINARY"

  TMP=$(mktemp -d)
  trap 'rm -rf "$TMP"' EXIT

  download "$URL" "$TMP/$ARCHIVE"
  verify_checksum "$TMP/$ARCHIVE" "$ARCHIVE" "$CHECKSUMS_URL"
  tar -xzf "$TMP/$ARCHIVE" -C "$TMP"
  # Use `install` (allocates a NEW inode), never an in-place `cp` over the
  # existing file. On macOS/Apple Silicon the kernel caches an unsigned binary's
  # ad-hoc CDHash against its inode; rewriting that inode's bytes makes the next
  # launch SIGKILL before any code runs (clients see a -32000 connect failure).
  # A fresh inode avoids it. Keep this as `install`/`mv`, do not "simplify" to cp.
  install -m 755 "$TMP/$BINARY" "$INSTALL_DIR/$BINARY"

  # macOS: remove quarantine attribute so Gatekeeper doesn't block it
  if [ "$OS" = "darwin" ] && command -v xattr >/dev/null 2>&1; then
    xattr -d com.apple.quarantine "$INSTALL_DIR/$BINARY" 2>/dev/null || true
  fi

  echo ""
  echo "Installed $BINARY to $INSTALL_DIR/$BINARY"

  # Check if install dir is on PATH
  case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *)
      echo ""
      SHELL_NAME=$(basename "${SHELL:-/bin/sh}")
      case "$SHELL_NAME" in
        zsh)  PROFILE="$HOME/.zshrc" ;;
        bash)
          if [ -f "$HOME/.bashrc" ]; then PROFILE="$HOME/.bashrc"
          else PROFILE="$HOME/.profile"; fi ;;
        *)    PROFILE="$HOME/.profile" ;;
      esac
      EXPORT_LINE="export PATH=\"$INSTALL_DIR:\$PATH\""

      # Auto-add to shell profile if running interactively; otherwise just print instructions
      if [ -t 0 ] 2>/dev/null; then
        if [ -f "$PROFILE" ] && grep -qF "$INSTALL_DIR" "$PROFILE" 2>/dev/null; then
          : # already there
        else
          printf "Add $INSTALL_DIR to your PATH in $PROFILE? [Y/n] "
          read -r REPLY < /dev/tty 2>/dev/null || REPLY="n"
          case "$REPLY" in
            [Nn]*) ;;
            *)
              echo "$EXPORT_LINE" >> "$PROFILE"
              echo "Done. Run 'source $PROFILE' or open a new terminal."
              ;;
          esac
        fi
      else
        echo "Note: $INSTALL_DIR is not on your PATH."
        echo "Add it by running:"
        echo ""
        echo "  echo '$EXPORT_LINE' >> $PROFILE && source $PROFILE"
      fi
      export PATH="$INSTALL_DIR:$PATH"
      ;;
  esac

  # Register with Claude Code if available
  if [ "${SKIP_MCP_REGISTER:-0}" != "1" ] && command -v claude >/dev/null 2>&1; then
    echo ""
    echo "Registering with Claude Code..."
    claude mcp add --scope user web-researcher -- "$INSTALL_DIR/$BINARY"
    echo "Done — Claude Code can now use web-researcher-mcp."
  else
    echo ""
    echo "To connect to Claude Code, run:"
    echo ""
    echo "  claude mcp add --scope user web-researcher -- $INSTALL_DIR/$BINARY"
  fi

  echo ""
  echo "Next: set up a search provider API key."
  echo "See https://github.com/$REPO#configuration"
}

main
