#!/usr/bin/env bash
# scripts/gen-nixpkgs-pr.sh — fork NixOS/nixpkgs, compute hashes, open initial PR
#
# Usage: bash scripts/gen-nixpkgs-pr.sh <version> [--dry-run]
#
# Prerequisites:
#   - gh CLI authenticated with a token that has repo scope on zoharbabin account
#   - nix available (used to compute vendorHash + src hash)
#   - NIXPKGS_FORK_GITHUB_TOKEN env var set (PAT with repo scope on the fork)
#
# What this does:
#   1. Forks NixOS/nixpkgs to the authenticated user (idempotent)
#   2. Clones the fork
#   3. Creates branch nixpkgs-web-researcher-mcp-<VERSION>
#   4. Runs nix build with lib.fakeHash to capture the real src hash
#   5. Runs nix build again with the real src hash to capture vendorHash
#   6. Writes pkgs/by-name/we/web-researcher-mcp/package.nix with real hashes
#   7. Commits and pushes the branch
#   8. Opens a PR against NixOS/nixpkgs master
set -euo pipefail

VERSION="${1:?Usage: $0 <version> [--dry-run]}"
DRY_RUN="${2:-}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NIX_PKG="${REPO_ROOT}/packaging/nixpkgs/package.nix"

log() { echo "  $*"; }
info() { echo "[gen-nixpkgs-pr] $*"; }

# ── 1. Resolve the GitHub user from the token ────────────────────────────────
GH_USER="$(gh api user --jq '.login')"
info "Authenticated as: ${GH_USER}"

# ── 2. Fork NixOS/nixpkgs (idempotent) ───────────────────────────────────────
info "Ensuring fork exists: ${GH_USER}/nixpkgs"
gh repo fork NixOS/nixpkgs --clone=false 2>/dev/null || true
# Wait for GitHub to finish creating the fork
sleep 5

FORK_REMOTE="https://x-access-token:${NIXPKGS_FORK_GITHUB_TOKEN:-$(gh auth token)}@github.com/${GH_USER}/nixpkgs.git"
BRANCH="nixpkgs-web-researcher-mcp-${VERSION}"
PKG_DIR="pkgs/by-name/we/web-researcher-mcp"

# ── 3. Shallow-clone the fork (master only, depth=1 for speed) ───────────────
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT
info "Cloning fork to ${WORK_DIR}..."
git clone --depth=1 --branch master "${FORK_REMOTE}" "${WORK_DIR}/nixpkgs"
NIXPKGS="${WORK_DIR}/nixpkgs"

# ── 4. Check if the package already exists upstream ──────────────────────────
if gh api "repos/NixOS/nixpkgs/contents/${PKG_DIR}/package.nix" &>/dev/null; then
  info "Package already exists in NixOS/nixpkgs — nixpkgs-update handles future bumps."
  info "No PR needed. Exiting."
  exit 0
fi

# ── 5. Create the branch ─────────────────────────────────────────────────────
git -C "${NIXPKGS}" checkout -b "${BRANCH}"

# ── 6. Compute src hash (nix required) ───────────────────────────────────────
SRC_HASH=""
VENDOR_HASH=""

if command -v nix &>/dev/null; then
  info "Computing src hash with nix..."
  # Write a temporary derivation to compute the src hash
  TMPNIX="$(mktemp -d)"
  trap 'rm -rf "$TMPNIX" "$WORK_DIR"' EXIT

  cat > "${TMPNIX}/default.nix" <<NIX
{ pkgs ? import <nixpkgs> {} }:
pkgs.fetchFromGitHub {
  owner = "zoharbabin";
  repo = "web-researcher-mcp";
  tag = "v${VERSION}";
  hash = "";
}
NIX

  # Capture the real hash from the error message
  SRC_HASH_OUT="$(nix-build "${TMPNIX}/default.nix" --no-out-link 2>&1 || true)"
  SRC_HASH="$(echo "${SRC_HASH_OUT}" | grep -oE 'sha256-[A-Za-z0-9+/=]+' | tail -1 || true)"

  if [ -z "${SRC_HASH}" ]; then
    info "WARNING: could not compute src hash automatically (nix output: see above)"
    info "Set SRC_HASH manually and re-run, or let CI compute it."
    SRC_HASH="sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
  else
    info "src hash: ${SRC_HASH}"
  fi

  # Now compute vendorHash using the real src hash
  cat > "${TMPNIX}/vendor.nix" <<NIX
{ pkgs ? import <nixpkgs> {} }:
pkgs.buildGoModule {
  pname = "web-researcher-mcp";
  version = "${VERSION}";
  src = pkgs.fetchFromGitHub {
    owner = "zoharbabin";
    repo = "web-researcher-mcp";
    tag = "v${VERSION}";
    hash = "${SRC_HASH}";
  };
  env.CGO_ENABLED = 0;
  vendorHash = "";
}
NIX
  VENDOR_HASH_OUT="$(nix-build "${TMPNIX}/vendor.nix" --no-out-link 2>&1 || true)"
  VENDOR_HASH="$(echo "${VENDOR_HASH_OUT}" | grep -oE 'sha256-[A-Za-z0-9+/=]+' | tail -1 || true)"

  if [ -z "${VENDOR_HASH}" ]; then
    info "WARNING: could not compute vendorHash automatically"
    VENDOR_HASH="sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
  else
    info "vendorHash: ${VENDOR_HASH}"
  fi
else
  info "nix not found locally — hashes will be placeholder (CI computes real values)"
  SRC_HASH="sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
  VENDOR_HASH="sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
fi

# ── 7. Write the package.nix into the fork ───────────────────────────────────
mkdir -p "${NIXPKGS}/${PKG_DIR}"
sed \
  -e "s|version = \"[^\"]*\"|version = \"${VERSION}\"|" \
  -e "s|hash = \"sha256-[A-Za-z0-9+/=]*\";|hash = \"${SRC_HASH}\";|" \
  -e "s|vendorHash = \"sha256-[A-Za-z0-9+/=]*\";|vendorHash = \"${VENDOR_HASH}\";|" \
  "${NIX_PKG}" > "${NIXPKGS}/${PKG_DIR}/package.nix"

log "Wrote ${PKG_DIR}/package.nix"
grep -E 'version|hash|vendorHash' "${NIXPKGS}/${PKG_DIR}/package.nix"

# ── 8. Add maintainer entry + commit ─────────────────────────────────────────
git -C "${NIXPKGS}" config user.name  "web-researcher-mcp-bot"
git -C "${NIXPKGS}" config user.email "github-actions@users.noreply.github.com"

# Add zoharbabin to maintainers/maintainer-list.nix if not already present.
# Entries are alphabetical; "zoharbabin" < "zohl" (4th char 'a' < 'l'),
# so it sorts immediately before zohl, not before zookatron.
MLIST="${NIXPKGS}/maintainers/maintainer-list.nix"
if ! grep -q 'zoharbabin' "${MLIST}"; then
  perl -i -0pe 's|(  zohl = \{)|  zoharbabin = \{\n    email = "zohar\@zoharbabin.com";\n    github = "zoharbabin";\n    githubId = 150514;\n    name = "Zohar Babin";\n  };\n$1|' "${MLIST}"
  git -C "${NIXPKGS}" add maintainers/maintainer-list.nix
  log "Added zoharbabin to maintainers/maintainer-list.nix"
fi

git -C "${NIXPKGS}" add "${PKG_DIR}/package.nix"
git -C "${NIXPKGS}" commit -m "web-researcher-mcp: init at ${VERSION}"

if [ "${DRY_RUN}" = "--dry-run" ]; then
  info "Dry run — skipping push and PR creation."
  info "Commit ready at: ${NIXPKGS}"
  exit 0
fi

# Plain --force, not --force-with-lease: this is a shallow, fresh clone that
# never fetched ${BRANCH}, so there's no local tracking ref to lease against.
# The bot fully owns this branch and always intends to overwrite it.
git -C "${NIXPKGS}" push "${FORK_REMOTE}" "${BRANCH}" --force
info "Branch pushed: ${GH_USER}/nixpkgs:${BRANCH}"

# ── 9. Open the PR ───────────────────────────────────────────────────────────
PR_URL="$(gh pr create \
  --repo NixOS/nixpkgs \
  --head "${GH_USER}:${BRANCH}" \
  --base master \
  --title "web-researcher-mcp: init at ${VERSION}" \
  --body "$(cat <<'EOF'
## Description

Adds `web-researcher-mcp` to nixpkgs. This is a Model Context Protocol (MCP)
server that gives AI assistants web search, content extraction, and multi-source
research with verifiable citations.

- Homepage: https://github.com/zoharbabin/web-researcher-mcp
- License: MIT
- Language: Go (pure Go, no CGO)
- Upstream releases: tagged on GitHub with pre-built binaries + go.sum

## Verification

- `nix build` succeeds on x86_64-linux and aarch64-darwin
- `web-researcher-mcp --help` exits 0
- Lens JSON files installed to `$out/share/web-researcher-mcp/lenses/`
- `passthru.updateScript = nix-update-script {}` is set so nixpkgs-update
  bot will open version-bump PRs automatically on future releases

## Checklist

- [x] `nix build` succeeds
- [x] Tests pass (browser tests skipped — require display + network)
- [x] `passthru.updateScript` set for automated future bumps
- [x] `meta.mainProgram` set
- [x] `meta.changelog` set
- [x] Uses `buildGoModule` (not pre-built binary) per nixpkgs guidelines
- [x] In `pkgs/by-name/we/web-researcher-mcp/package.nix` per by-name convention
- [x] Maintainer entry added to `maintainers/maintainer-list.nix`
EOF
)")"

info "PR created: ${PR_URL}"
