#!/usr/bin/env bash
# Authenticode-sign the Windows .exe IN PLACE via Azure Trusted Signing (jsign),
# called from GoReleaser's builds[].hooks.post — i.e. AFTER the binary is compiled
# but BEFORE the archive/checksum/scoop/winget/cask pipes run. Because every
# downstream hash derives from the bytes at $BIN_PATH, signing here makes the
# release zip, checksums.txt, and the Scoop/WinGet/Cask manifests all carry the
# SIGNED hash in a single GoReleaser run — no post-hoc repackaging, no hash drift.
#
# Usage (from .goreleaser.yml):  scripts/sign-windows.sh "{{ .Path }}" "{{ .Os }}" "{{ .Arch }}"
#
# Gating (two no-op guards so non-Windows targets, snapshots, and credential-less
# runs all succeed unchanged — mirrors the macOS notarize self-gate):
#   - skip unless GOOS=windows
#   - skip unless AZURE_SIGNING_ENABLED=true
#
# Required env when active (exported by the GoReleaser CI step):
#   AZURE_TENANT_ID, AZURE_CLIENT_ID, AZURE_CLIENT_SECRET
#   AZURE_SIGN_ENDPOINT  (e.g. https://eus.codesigning.azure.net — NO trailing slash)
#   AZURE_SIGN_ALIAS     ("<account>/<certificate-profile>")
# Optional: JSIGN_VERSION (default 7.4), JSIGN_JAR (path to a preinstalled jar).
set -euo pipefail

BIN_PATH="${1:-}"
GOOS="${2:-}"
# GOARCH ("${3}") is accepted for symmetry/future use; we currently sign every
# windows build GoReleaser produces (windows/arm64 is excluded in the build matrix).

if [ -z "$BIN_PATH" ]; then
  echo "sign-windows: no binary path provided; nothing to sign." >&2
  exit 0
fi

# Gate 1 — only Windows binaries get Authenticode.
if [ "$GOOS" != "windows" ]; then
  exit 0
fi

# Gate 2 — signing globally disabled (snapshot/local/no-creds). Zero regression.
if [ "${AZURE_SIGNING_ENABLED:-false}" != "true" ]; then
  echo "sign-windows: AZURE_SIGNING_ENABLED != true — skipping Authenticode signing of $BIN_PATH"
  exit 0
fi

if [ ! -f "$BIN_PATH" ]; then
  echo "sign-windows: expected binary not found at $BIN_PATH" >&2
  exit 1
fi

: "${AZURE_TENANT_ID:?sign-windows: AZURE_TENANT_ID required}"
: "${AZURE_CLIENT_ID:?sign-windows: AZURE_CLIENT_ID required}"
: "${AZURE_CLIENT_SECRET:?sign-windows: AZURE_CLIENT_SECRET required}"
: "${AZURE_SIGN_ENDPOINT:?sign-windows: AZURE_SIGN_ENDPOINT required}"
: "${AZURE_SIGN_ALIAS:?sign-windows: AZURE_SIGN_ALIAS required}"

# Toolchain: a JRE for jsign + the Azure CLI for the access token. Both are
# present on GitHub's ubuntu-latest, but install defensively (idempotent).
if ! command -v java >/dev/null 2>&1; then
  sudo apt-get update -qq
  sudo apt-get install -y -qq default-jre-headless
fi
if ! command -v az >/dev/null 2>&1; then
  curl -sL https://aka.ms/InstallAzureCLIDeb | sudo bash >/dev/null
fi

JSIGN_VERSION="${JSIGN_VERSION:-7.4}"
JSIGN_JAR="${JSIGN_JAR:-/tmp/jsign-${JSIGN_VERSION}.jar}"
if [ ! -f "$JSIGN_JAR" ]; then
  curl -fsSL -o "$JSIGN_JAR" \
    "https://github.com/ebourg/jsign/releases/download/${JSIGN_VERSION}/jsign-${JSIGN_VERSION}.jar"
fi

# Service-principal login → short-lived token scoped to the code-signing resource.
az login --service-principal \
  -u "$AZURE_CLIENT_ID" -p "$AZURE_CLIENT_SECRET" --tenant "$AZURE_TENANT_ID" >/dev/null
ACCESS_TOKEN="$(az account get-access-token \
  --resource https://codesigning.azure.net \
  --query accessToken -o tsv)"

# Sign in place at $BIN_PATH (jsign overwrites the input). RFC-3161 timestamp via
# the Microsoft TSA so the signature outlives the short-lived (~3-day) cert.
java -jar "$JSIGN_JAR" \
  --storetype TRUSTEDSIGNING \
  --keystore "$AZURE_SIGN_ENDPOINT" \
  --storepass "$ACCESS_TOKEN" \
  --alias "$AZURE_SIGN_ALIAS" \
  --tsmode RFC3161 \
  --tsaurl http://timestamp.acs.microsoft.com \
  "$BIN_PATH"

echo "sign-windows: Authenticode-signed $BIN_PATH via Azure Trusted Signing"
