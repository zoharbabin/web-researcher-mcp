#!/usr/bin/env bash
# Updates packaging/aur/PKGBUILD, packaging/aur/.SRCINFO, and
# packaging/nix/flake.nix to a new release version.
# Usage: scripts/update-packaging.sh <version>
# Example: scripts/update-packaging.sh 1.37.0
set -euo pipefail

VERSION="${1:?Usage: $0 <version>}"
REPO="zoharbabin/web-researcher-mcp"
RELEASE_BASE="https://github.com/${REPO}/releases/download/v${VERSION}"

TMPDIR_SUMS="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_SUMS"' EXIT

echo "Fetching checksums for v${VERSION}..."
curl -fsSL "${RELEASE_BASE}/checksums.txt" -o "${TMPDIR_SUMS}/checksums.txt"

get_sha256() {
  # Exact filename match — avoids picking up .sbom.json lines for the same archive
  grep "  $1$" "${TMPDIR_SUMS}/checksums.txt" | awk '{print $1}'
}

hex_to_nix_sri() {
  printf '%s' "$1" | xxd -r -p | base64 | tr -d '\n' | sed 's/^/sha256-/'
}

SHA_LINUX_AMD64="$(get_sha256 "web-researcher-mcp_${VERSION}_linux_amd64.tar.gz")"
SHA_LINUX_ARM64="$(get_sha256 "web-researcher-mcp_${VERSION}_linux_arm64.tar.gz")"
SHA_DARWIN_AMD64="$(get_sha256 "web-researcher-mcp_${VERSION}_darwin_amd64.tar.gz")"
SHA_DARWIN_ARM64="$(get_sha256 "web-researcher-mcp_${VERSION}_darwin_arm64.tar.gz")"

NIX_LINUX_AMD64="$(hex_to_nix_sri "$SHA_LINUX_AMD64")"
NIX_LINUX_ARM64="$(hex_to_nix_sri "$SHA_LINUX_ARM64")"
NIX_DARWIN_AMD64="$(hex_to_nix_sri "$SHA_DARWIN_AMD64")"
NIX_DARWIN_ARM64="$(hex_to_nix_sri "$SHA_DARWIN_ARM64")"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# portable in-place sed: macOS needs `sed -i ''`, Linux accepts `sed -i`
SED_INPLACE() { perl -i -pe "$@"; }

# ── AUR PKGBUILD ──────────────────────────────────────────────────────────────
PKGBUILD="${REPO_ROOT}/packaging/aur/PKGBUILD"
SED_INPLACE "s/^pkgver=.*/pkgver=${VERSION}/" "$PKGBUILD"
SED_INPLACE "s/^pkgrel=.*/pkgrel=1/" "$PKGBUILD"
echo "Updated ${PKGBUILD}"

# ── AUR .SRCINFO ──────────────────────────────────────────────────────────────
SRCINFO="${REPO_ROOT}/packaging/aur/.SRCINFO"
OLD_VERSION="$(grep 'pkgver = ' "$SRCINFO" | awk '{print $3}' | head -1)"
# Update version strings, URLs, and SHA256 sums
perl -i -pe "s/\Q${OLD_VERSION}\E/${VERSION}/g" "$SRCINFO"
perl -i -pe "s|(?<=sha256sums_x86_64 = )[0-9a-f]{64}|${SHA_LINUX_AMD64}|" "$SRCINFO"
perl -i -pe "s|(?<=sha256sums_aarch64 = )[0-9a-f]{64}|${SHA_LINUX_ARM64}|" "$SRCINFO"
echo "Updated ${SRCINFO}"

# ── Nix flake ─────────────────────────────────────────────────────────────────
FLAKE="${REPO_ROOT}/packaging/nix/flake.nix"
# Update version string
OLD_VERSION="$(grep 'version = "' "$FLAKE" | head -1 | grep -oE '[0-9]+\.[0-9]+\.[0-9]+')"
SED_INPLACE "s/version = \"${OLD_VERSION}\"/version = \"${VERSION}\"/" "$FLAKE"

# Replace per-platform SRI hashes. The flake structure is:
#   "x86_64-linux" = {
#     url = "...";
#     hash = "sha256-BASE64=";
#   };
# Python handles the multi-line block match safely.
python3 - <<PYEOF
import re

flake_path = "${FLAKE}"
hashes = [
    ("x86_64-linux",   "${NIX_LINUX_AMD64}"),
    ("aarch64-linux",  "${NIX_LINUX_ARM64}"),
    ("x86_64-darwin",  "${NIX_DARWIN_AMD64}"),
    ("aarch64-darwin", "${NIX_DARWIN_ARM64}"),
]

with open(flake_path) as f:
    content = f.read()

for platform, new_hash in hashes:
    # Match the platform block: "platform" = { ... hash = "sha256-..."; ... };
    # Capture everything up to the hash value, replace just the hash value.
    pattern = r'("' + re.escape(platform) + r'"[^{]*\{[^}]*?hash\s*=\s*")sha256-[A-Za-z0-9+/=]+'
    replacement = r'\g<1>' + new_hash
    content = re.sub(pattern, replacement, content, flags=re.DOTALL)

with open(flake_path, 'w') as f:
    f.write(content)

print("Nix hashes updated.")
PYEOF
echo "Updated ${FLAKE}"

echo ""
echo "Done. Packaging updated to v${VERSION}."
echo "Verify: grep -n 'version\|sha256\|hash' packaging/aur/PKGBUILD packaging/aur/.SRCINFO packaging/nix/flake.nix"
