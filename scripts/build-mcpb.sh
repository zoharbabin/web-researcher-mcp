#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:-$(cat VERSION 2>/dev/null || git describe --tags --abbrev=0 2>/dev/null || echo "0.0.0")}"
VERSION="${VERSION#v}"
DIST_DIR="dist/mcpb"
MANIFEST_TEMPLATE="mcpb/manifest.json"

PLATFORMS=(
  "darwin:amd64"
  "darwin:arm64"
  "linux:amd64"
  "linux:arm64"
  "windows:amd64"
)

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

for platform in "${PLATFORMS[@]}"; do
  IFS=: read -r os arch <<< "$platform"

  binary_name="web-researcher-mcp"
  ext=""
  if [ "$os" = "windows" ]; then
    ext=".exe"
    binary_name="web-researcher-mcp${ext}"
  fi

  bundle_name="web-researcher-mcp_${VERSION}_${os}_${arch}.mcpb"
  work_dir=$(mktemp -d)

  mkdir -p "${work_dir}/server"

  # Copy the pre-built binary from GoReleaser dist
  src_binary="dist/web-researcher-mcp_${os}_${arch}${ext:+_v1}/${binary_name}"
  # Try alternate GoReleaser naming patterns
  if [ ! -f "$src_binary" ]; then
    src_binary="dist/web-researcher-mcp_${os}_${arch}/${binary_name}"
  fi
  if [ ! -f "$src_binary" ]; then
    # Build from source as fallback
    echo "Building ${os}/${arch}..."
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -ldflags "-s -w -X main.version=${VERSION}" \
      -o "${work_dir}/server/${binary_name}" ./cmd/web-researcher-mcp
  else
    cp "$src_binary" "${work_dir}/server/${binary_name}"
  fi

  # Generate platform-specific manifest
  if [ "$os" = "windows" ]; then
    entry_point="server/web-researcher-mcp.exe"
    command="\${__dirname}/server/web-researcher-mcp.exe"
  else
    entry_point="server/web-researcher-mcp"
    command="\${__dirname}/server/web-researcher-mcp"
  fi

  jq --arg version "$VERSION" \
     --arg entry "$entry_point" \
     --arg cmd "$command" \
     '.version = $version | .server.entry_point = $entry | .server.mcp_config.command = $cmd' \
     "$MANIFEST_TEMPLATE" > "${work_dir}/manifest.json"

  # Create the .mcpb ZIP bundle
  (cd "$work_dir" && zip -qr - .) > "${DIST_DIR}/${bundle_name}"

  rm -rf "$work_dir"
  echo "Created ${bundle_name}"
done

echo "All .mcpb bundles created in ${DIST_DIR}/"
ls -la "${DIST_DIR}/"
