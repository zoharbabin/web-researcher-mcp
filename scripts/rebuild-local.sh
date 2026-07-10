#!/usr/bin/env bash
#
# rebuild-local.sh — deterministic local rebuild + reinstall for IRL testing.
#
# Does exactly three things, in order, with no LLM in the loop:
#   1. Clears caches    — the Go build cache (forces a from-scratch compile) and
#                         the MCP response cache (so a live tool call hits the
#                         network fresh, never a stale cached scrape).
#   2. Rebuilds         — syncs the embedded lenses, then builds the binary.
#   3. Reinstalls       — atomically replaces the binary at EVERY location it
#                         might actually be invoked from, using the rm + cp +
#                         codesign sequence that avoids the macOS
#                         "cp-over-in-place SIGKILL (-32000)" gotcha.
#
# "Every location" means the union of: the path ~/.claude.json's MCP config
# names, and every distinct file named web-researcher-mcp resolvable on
# $PATH. A dev machine commonly ends up with more than one installed copy
# (homebrew, ~/.local/bin, a pyenv shim a previous rebuild overwrote with a
# raw binary, a duplicated PATH entry) — whichever one precedes the others in
# a given client's $PATH is the one that actually runs, so all of them have to
# stay in sync or a rebuild silently doesn't take effect for that client.
# Deliberately excluded: any copy bundled inside a pip/site-packages install
# (e.g. the Python SDK's `.../web_researcher_mcp/bin/web-researcher-mcp`) —
# that is a versioned release artifact owned by pip, launched by its own
# wrapper rather than a PATH lookup of `web-researcher-mcp`, and pip would
# just overwrite it again on the next install anyway.
#
# It deliberately NEVER touches personal-data directories under the cache root
# (sessions/, persist/ — the latter holds long-term memory, token revocation,
# and rate quotas). Only the response cache (*.cache + .version) is removed.
#
# Usage:
#   scripts/rebuild-local.sh                  # clean + build + install everywhere
#   INSTALL_PATH=/usr/local/bin/web-researcher-mcp scripts/rebuild-local.sh
#   CACHE_DIR=/custom/cache scripts/rebuild-local.sh
#   scripts/rebuild-local.sh --no-install     # clean + build only
#   scripts/rebuild-local.sh --keep-build-cache  # skip `go clean -cache`
#
# Env overrides:
#   INSTALL_PATH      install to this ONE path only, skipping auto-discovery
#                     of every other location (default: the path configured
#                     in ~/.claude.json's mcpServers.web-researcher.command,
#                     PLUS every distinct web-researcher-mcp found on $PATH;
#                     falls back to /opt/homebrew/bin on macOS or /usr/local/bin
#                     elsewhere if neither source finds anything)
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

# claude_json_install_path reads the exact binary path Claude Code's own MCP
# config points at (~/.claude.json's mcpServers.web-researcher.command). This
# is the authoritative "this is what Claude Code loads" source when present.
claude_json_install_path() {
  local cfg="$HOME/.claude.json"
  [ -f "$cfg" ] || return 1
  command -v jq >/dev/null 2>&1 || return 1
  jq -er '.mcpServers["web-researcher"].command // empty' "$cfg" 2>/dev/null
}

# discover_install_targets prints "<path>\t<source>" lines: the ~/.claude.json
# path (if configured) followed by every distinct executable file named
# $BINARY found across $PATH, deduplicated by resolved path (first source
# wins — so a PATH entry that happens to match the config path still shows
# "~/.claude.json"). Bash-3.2-safe: no arrays, no mapfile, plain word-splitting
# on IFS in a for loop.
discover_install_targets() {
  local from_config
  if from_config="$(claude_json_install_path)" && [ -n "$from_config" ]; then
    printf '%s\t%s\n' "$from_config" "~/.claude.json"
  fi

  local old_ifs="$IFS" dir
  IFS=':'
  for dir in $PATH; do
    IFS="$old_ifs"
    [ -n "$dir" ] || continue
    if [ -f "$dir/$BINARY" ] && [ -x "$dir/$BINARY" ]; then
      printf '%s\t%s\n' "$dir/$BINARY" "PATH"
    fi
    IFS=':'
  done
  IFS="$old_ifs"
}

# resolve_install_targets wraps discover_install_targets with the dedup pass
# and the platform-default fallback for a machine with nothing findable yet.
resolve_install_targets() {
  local found
  found="$(discover_install_targets | awk -F'\t' '!seen[$1]++')"
  if [ -n "$found" ]; then
    printf '%s\n' "$found"
    return
  fi
  if [ "$UNAME" = "Darwin" ] && [ -d /opt/homebrew/bin ]; then
    printf '%s\t%s\n' "/opt/homebrew/bin/$BINARY" "default"
  else
    printf '%s\t%s\n' "/usr/local/bin/$BINARY" "default"
  fi
}

CACHE_DIR="${CACHE_DIR:-$(default_cache_dir)}"
if [ -n "${INSTALL_PATH:-}" ]; then
  TARGETS="$(printf '%s\t%s\n' "$INSTALL_PATH" "INSTALL_PATH env")"
else
  TARGETS="$(resolve_install_targets)"
fi
TARGET_COUNT="$(printf '%s\n' "$TARGETS" | grep -c . || true)"

echo "==> web-researcher-mcp local rebuild"
echo "    repo:    $REPO_ROOT"
echo "    cache:   $CACHE_DIR"
echo "    install targets ($TARGET_COUNT):"
while IFS=$'\t' read -r t_path t_source; do
  [ -n "$t_path" ] || continue
  echo "      - $t_path (source: $t_source)"
done <<< "$TARGETS"
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

echo "==> [3/3] installing to $TARGET_COUNT location(s)"
INSTALL_FAILED=0
while IFS=$'\t' read -r t_path t_source; do
  [ -n "$t_path" ] || continue

  t_dir="$(dirname "$t_path")"
  if [ ! -d "$t_dir" ]; then
    echo "    ERROR: install dir does not exist: $t_dir (target: $t_path, source: $t_source)" >&2
    INSTALL_FAILED=1
    continue
  fi
  if [ ! -w "$t_dir" ]; then
    echo "    ERROR: no write permission to $t_dir (target: $t_path, source: $t_source)" >&2
    INSTALL_FAILED=1
    continue
  fi

  # rm + cp (NOT cp-over-in-place): overwriting a running/just-run Mach-O in
  # place invalidates its code signature and macOS SIGKILLs it (-32000).
  # Remove first, copy fresh, then re-sign ad-hoc so Gatekeeper is satisfied.
  rm -f "$t_path"
  cp "$BINARY" "$t_path"
  if [ "$UNAME" = "Darwin" ]; then
    codesign --force --sign - "$t_path"
    codesign --verify --verbose "$t_path" 2>&1 | sed 's/^/      /'
  fi
  echo "    installed: $(ls -lh "$t_path" | awk '{print $5}') -> $t_path"
done <<< "$TARGETS"

echo
if [ "$INSTALL_FAILED" -eq 1 ]; then
  echo "FAILED: one or more install targets could not be updated (see ERROR lines above)." >&2
  exit 1
fi

echo "Done. Restart your MCP client(s) to load the new binary."
