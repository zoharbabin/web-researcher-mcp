#!/usr/bin/env bash
set -euo pipefail

# Reads VERSION file and patches all version-bearing files in the repo.
# Usage: bash scripts/sync-version.sh

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VERSION="$(tr -d '[:space:]' < "$REPO_ROOT/VERSION")"

if [ -z "$VERSION" ]; then
  echo "ERROR: VERSION file is empty" >&2
  exit 1
fi

echo "Syncing version: $VERSION"

# server.json
jq --arg v "$VERSION" \
   --arg ghcr "ghcr.io/zoharbabin/web-researcher-mcp:$VERSION" \
   --arg dh "docker.io/zoharbabin/web-researcher-mcp:$VERSION" \
   '.version = $v | .packages[0].identifier = $ghcr | .packages[1].identifier = $dh' \
   "$REPO_ROOT/server.json" > "$REPO_ROOT/server.json.tmp" \
   && mv "$REPO_ROOT/server.json.tmp" "$REPO_ROOT/server.json"
echo "  ✓ server.json"

# .claude-plugin/plugin.json
jq --arg v "$VERSION" '.version = $v' \
   "$REPO_ROOT/.claude-plugin/plugin.json" > "$REPO_ROOT/.claude-plugin/plugin.json.tmp" \
   && mv "$REPO_ROOT/.claude-plugin/plugin.json.tmp" "$REPO_ROOT/.claude-plugin/plugin.json"
echo "  ✓ .claude-plugin/plugin.json"

echo "Done. All files now at v$VERSION"
