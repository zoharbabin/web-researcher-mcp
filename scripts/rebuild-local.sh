#!/usr/bin/env bash
#
# rebuild-local.sh — deterministic local rebuild + reinstall for IRL testing.
#
# Does exactly three things, in order, with no LLM in the loop:
#   1. Clears caches    — the Go build cache (forces a from-scratch compile) and
#                         the MCP response cache (so a live tool call hits the
#                         network fresh, never a stale cached scrape).
#   2. Rebuilds         — syncs the embedded lenses, then builds the binary.
#   3. Reinstalls       — atomically replaces the installed binary using the
#                         rm + cp + codesign sequence that avoids the macOS
#                         "cp-over-in-place SIGKILL (-32000)" gotcha.
#
# It deliberately NEVER touches personal-data directories under the cache root
# (sessions/, persist/ — the latter holds long-term memory, token revocation,
# and rate quotas). Only the response cache (*.cache + .version) is removed.
#
# Usage:
#   scripts/rebuild-local.sh                  # clean + build + install
#   INSTALL_PATH=/usr/local/bin/web-researcher-mcp scripts/rebuild-local.sh
#   CACHE_DIR=/custom/cache scripts/rebuild-local.sh
#   scripts/rebuild-local.sh --no-install     # clean + build only
#   scripts/rebuild-local.sh --keep-build-cache  # skip `go clean -cache`
#
# Env overrides:
#   INSTALL_PATH      target binary path (default: resolve `web-researcher-mcp`
#                     on PATH, else /opt/homebrew/bin on macOS, /usr/local/bin else)
#   CACHE_DIR         MCP cache root (default: matches the server's own default —
#                     ~/Library/Caches/web-researcher-mcp on macOS,
#                     ~/.cache/web-researcher-mcp on Linux)

set -euo pipefail

# --- locate the repo root (this script lives in scripts/) -------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

BINARY="web-researcher-mcp"

# --- parse flags ------------------------------------------------------------
DO_INSTALL=1
CLEAN_BUILD_CACHE=1
for arg in "$@"; do
  case "$arg" in
    --no-install)         DO_INSTALL=0 ;;
    --keep-build-cache)   CLEAN_BUILD_CACHE=0 ;;
    -h|--help)
      # Print the header comment block (every line up to the first blank line
      # after the shebang), stripping the leading "# ".
      awk 'NR>1 && /^#/ {sub(/^# ?/,""); print; next} NR>1 {exit}' "$0"
      exit 0 ;;
    *)
      echo "unknown flag: $arg (try --help)" >&2
      exit 2 ;;
  esac
done

# --- resolve platform-dependent defaults ------------------------------------
UNAME="$(uname -s)"

default_cache_dir() {
  if [ "$UNAME" = "Darwin" ]; then
    echo "$HOME/Library/Caches/$BINARY"
  else
    echo "${XDG_CACHE_HOME:-$HOME/.cache}/$BINARY"
  fi
}

default_install_path() {
  # Prefer wherever the binary is already installed, so we replace the one the
  # MCP client actually loads.
  if command -v "$BINARY" >/dev/null 2>&1; then
    command -v "$BINARY"
    return
  fi
  if [ "$UNAME" = "Darwin" ] && [ -d /opt/homebrew/bin ]; then
    echo "/opt/homebrew/bin/$BINARY"
  else
    echo "/usr/local/bin/$BINARY"
  fi
}

CACHE_DIR="${CACHE_DIR:-$(default_cache_dir)}"
INSTALL_PATH="${INSTALL_PATH:-$(default_install_path)}"

echo "==> web-researcher-mcp local rebuild"
echo "    repo:    $REPO_ROOT"
echo "    cache:   $CACHE_DIR"
echo "    install: $INSTALL_PATH"
echo

# --- 1. clear caches --------------------------------------------------------
if [ "$CLEAN_BUILD_CACHE" -eq 1 ]; then
  echo "==> [1/3] clearing Go build cache"
  go clean -cache
else
  echo "==> [1/3] keeping Go build cache (--keep-build-cache)"
fi

echo "==> [1/3] clearing MCP response cache (preserving sessions/ + persist/)"
if [ -d "$CACHE_DIR" ]; then
  # Only the response cache: *.cache files at the root + the .version marker.
  # sessions/ (research sessions) and persist/ (long-term memory, token
  # revocation, rate quotas) are PERSONAL DATA and deliberately untouched.
  removed=$(find "$CACHE_DIR" -maxdepth 1 -type f -name '*.cache' 2>/dev/null | wc -l | tr -d ' ')
  find "$CACHE_DIR" -maxdepth 1 -type f -name '*.cache' -delete 2>/dev/null || true
  rm -f "$CACHE_DIR/.version" 2>/dev/null || true
  echo "    removed $removed cached response file(s) + version marker"
else
  echo "    (no cache dir yet — nothing to clear)"
fi
echo

# --- 2. rebuild from scratch ------------------------------------------------
echo "==> [2/3] syncing embedded lenses"
cp lenses/*.json internal/search/lenses_embed/
echo "    synced $(ls internal/search/lenses_embed/*.json | wc -l | tr -d ' ') lenses"

echo "==> [2/3] building $BINARY"
VERSION="$(cat VERSION 2>/dev/null || echo dev)"
CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION}" -o "$BINARY" ./cmd/web-researcher-mcp
echo "    built $(ls -lh "$BINARY" | awk '{print $5}') -> $REPO_ROOT/$BINARY (version $VERSION)"
echo

# --- 3. reinstall (atomic, macOS-SIGKILL-safe) ------------------------------
if [ "$DO_INSTALL" -eq 0 ]; then
  echo "==> [3/3] skipping install (--no-install)"
  echo
  echo "Done. Built binary at: $REPO_ROOT/$BINARY"
  exit 0
fi

echo "==> [3/3] installing to $INSTALL_PATH"
INSTALL_DIR="$(dirname "$INSTALL_PATH")"
if [ ! -d "$INSTALL_DIR" ]; then
  echo "ERROR: install dir does not exist: $INSTALL_DIR" >&2
  echo "       set INSTALL_PATH to a valid location and re-run." >&2
  exit 1
fi
if [ ! -w "$INSTALL_DIR" ]; then
  echo "ERROR: no write permission to $INSTALL_DIR" >&2
  echo "       re-run with a writable INSTALL_PATH, or fix the directory perms." >&2
  exit 1
fi

# rm + cp (NOT cp-over-in-place): overwriting a running/just-run Mach-O in place
# invalidates its code signature and macOS SIGKILLs it (-32000). Remove first,
# copy fresh, then re-sign ad-hoc so Gatekeeper is satisfied.
rm -f "$INSTALL_PATH"
cp "$BINARY" "$INSTALL_PATH"
if [ "$UNAME" = "Darwin" ]; then
  codesign --force --sign - "$INSTALL_PATH"
  codesign --verify --verbose "$INSTALL_PATH" 2>&1 | sed 's/^/    /'
fi

echo "    installed: $(ls -lh "$INSTALL_PATH" | awk '{print $5}') -> $INSTALL_PATH"
echo
echo "Done. Restart your MCP client to load the new binary."
