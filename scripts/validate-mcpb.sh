#!/usr/bin/env bash
# Builds one representative .mcpb bundle and validates it with the official
# mcpb CLI against the ASSEMBLED bundle contents (manifest schema + icon
# presence), not just the raw mcpb/manifest.json template — the template
# alone can't pass the icon check since build-mcpb.sh is what copies icon.png
# in. Catches manifest schema drift and missing-asset regressions before release.
set -euo pipefail

WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

bash scripts/build-mcpb.sh 0.0.0-validate >/dev/null

bundle="dist/mcpb/web-researcher-mcp_0.0.0-validate_linux_amd64.mcpb"
unzip -q "$bundle" -d "$WORK_DIR"

npx --yes @anthropic-ai/mcpb validate "${WORK_DIR}/manifest.json"

rm -rf dist/mcpb
