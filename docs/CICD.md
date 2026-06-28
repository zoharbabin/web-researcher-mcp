# CI/CD Guide

One-stop reference for the four workflow files that govern every code change, release, and docs update in this repo. Read this when you're debugging a failed run, adding a new job, or preparing a release.

---

## 🗺️ At a glance

```
Every push / PR                         v* tag push
       │                                     │
       ▼                                     ▼
 ┌───────────────────┐             ┌──────────────────────┐
 │  ⚙️ ci.yml         │             │  🚀 release.yml       │
 │  (merge gate)     │             │  (publish pipeline)  │
 └─────────┬─────────┘             └──────────┬───────────┘
           │                                  │
    change detector                 🏗️ GoReleaser · Build & Publish
    ┌───────┴────────────────────────────────────────────────────┐
    │ always-run (every PR)     code-only (skipped on docs PRs)  │
    │ ─────────────────         ─────────────────────────────    │
    │ 🧹 lint                   🧪 test (race detector)          │
    │ 📄 docs-drift             🔬 e2e (STDIO + HTTP + OAuth)    │
    │ 🐍 python-drift           🐳 docker-smoke                  │
    │ 🧪 test-python            🏗️ build (5 platforms)           │
    │ 📦 validate-packaging     🛡️ security (vuln + gosec)       │
    └────────────────────────────────────────────────────────────┘
                                          │
                           ┌──────────────┼────────────────────┐
                           ▼              ▼                     ▼
                  🐳 docker-sign   📋 mcp-registry   🔨 smithery
                  🐍 pypi          📦 packaging      (all parallel)
```

There is also a fourth file, `codeql.yml`, that runs GitHub's CodeQL deep static analysis separately on push-to-main and a weekly schedule.

---

## 📄 Workflow files

| File | Trigger | Purpose |
|------|---------|---------|
| `.github/workflows/ci.yml` | push to `main`, any PR, `workflow_dispatch` | Gate: prevents broken code from merging |
| `.github/workflows/release.yml` | push of a `v*` tag | Publish: builds, signs, and ships a release |
| `.github/workflows/docs.yml` | push to `main` (docs paths only) | Deploy: builds the mkdocs site to GitHub Pages |
| `.github/workflows/codeql.yml` | push to `main`, weekly | Security: deep static analysis via GitHub CodeQL |

---

## ⚙️ ci.yml — The merge gate

### 📡 Triggers

- Every pull request targeting `main`
- Every push to `main`
- Manual: `Actions → ⚙️ CI → Run workflow` (add `run_python_live=true` to run live SDK tests)

### 🔍 Change detector (`changes` job)

The first job classifies every PR. It reads the list of changed files and outputs `code=true` or `code=false`.

**Harmless files** (skip heavy CI):
- `*.md`, `docs/**`, `decks/**`, `assets/**`
- `LICENSE`, `.gitignore`, `mkdocs.yml`, `overrides/**`
- `packaging/**` (version-bump PRs don't need a full Go test run)
- `.github/**_TEMPLATE*`, `.github/ISSUE_TEMPLATE/**`

Anything else → `code=true` → full CI runs.

> **Why?** Branch protection marks skipped *required* checks as passing. A docs-only PR goes green in seconds without getting stuck on checks that legitimately have nothing to test.

### 🔒 Always-run jobs (every PR, regardless of what changed)

These never skip. They catch cross-cutting drift that a change detector can't safely filter:

| Job | What it catches |
|-----|----------------|
| **🧹 lint** | `gofmt -s` + `golangci-lint` — formatting and static analysis |
| **📄 docs-drift** | `docs/TOOLS.md` ↔ registry drift; tool annotation coverage |
| **🐍 python-drift** | Python client (`models.py`/`client.py`) not regenerated after Go schema change |
| **🧪 test-python** | Python SDK unit + integration tests (mock HTTP, no binary needed) |
| **📦 validate-packaging** | `PKGBUILD` / `.SRCINFO` / `flake.nix` version + hash consistency |

> **Rule:** If you change a Go tool schema, run `make gen-python-client` before pushing. If you change `docs/TOOLS.md`, the matching tool definition must change in the same commit (or vice versa). These jobs enforce both.

### ⚡ Fast-fail ordering in the code path

```
🔍 changes ─► 🧹 lint ─► 🧪 test ─► 🔬 e2e
                      └──────────────► 🐳 docker-smoke
              └──────► 🛡️ security
🔍 changes ─► 🏗️ build   (parallel, independent of test)
```

- `lint` gates both `test` and `security` — a formatting error surfaces before expensive compute starts.
- `e2e` and `docker-smoke` wait for `test` — a unit test failure blocks the heavier suites.
- `build` runs in parallel with everything else — cross-compilation failures surface alongside test feedback without waiting.

### 🧪 Code-only jobs (skipped on docs/packaging PRs)

| Job | What it runs |
|-----|-------------|
| **🧪 test** | `go test -race` — unit + integration tests with race detector |
| **🔬 e2e** | Security + lifecycle E2E suite (STDIO, HTTP, OAuth) — network-free |
| **🐳 docker-smoke** | Builds the Docker image and drives MCP over HTTP end-to-end |
| **🏗️ build** | Cross-compile for Linux/Darwin/Windows × amd64/arm64 |
| **🛡️ security** | `govulncheck` + `gosec` — vulnerability and security scanning |

### 🐍 Manual-dispatch only

| Job | How to trigger |
|-----|---------------|
| **🐍 python-live-e2e** | `Actions → ⚙️ CI → Run workflow` with `run_python_live=true` |

Hits real external APIs. Not part of the required gate — too flaky on rate limits.

---

## 🚀 release.yml — The publish pipeline

### 📡 Trigger

Any tag matching `v*` pushed to the repo.

```bash
git tag v1.38.0
git push origin v1.38.0
```

### 🔑 Required secrets and variables

| Name | Kind | Purpose |
|------|------|---------|
| `GITHUB_TOKEN` | Auto | Release assets, Docker GHCR, PR creation |
| `DOCKERHUB_USERNAME` / `DOCKERHUB_TOKEN` | Secret | Docker Hub push |
| `HOMEBREW_TAP_GITHUB_TOKEN` | Secret | Homebrew tap PR |
| `SCOOP_BUCKET_GITHUB_TOKEN` | Secret | Scoop bucket PR |
| `WINGET_PKGS_GITHUB_TOKEN` | Secret | WinGet PR |
| `CHOCOLATEY_API_KEY` | Secret | Chocolatey push (optional — degrades gracefully) |
| `MACOS_SIGN_P12` / `MACOS_SIGN_PASSWORD` | Secret | macOS Developer ID signing |
| `MACOS_NOTARY_ISSUER_ID` / `MACOS_NOTARY_KEY_ID` / `MACOS_NOTARY_KEY` | Secret | Notarization |
| `AZURE_TENANT_ID` / `AZURE_CLIENT_ID` / `AZURE_CLIENT_SECRET` | Secret | Windows Authenticode (jsign) |
| `SMITHERY_API_KEY` | Secret | Smithery publish |
| `CODECOV_TOKEN` | Secret | Coverage upload (non-blocking) |
| `AZURE_SIGNING_ENABLED` | Var | `"true"` to enable Windows signing |
| `SMITHERY_ENABLED` | Var | `"true"` to enable Smithery job |
| `PYPI_PUBLISH_ENABLED` | Var | `"true"` to enable PyPI wheels |
| `PACKAGING_UPDATE_ENABLED` | Var | `"true"` to enable AUR/Nix auto-PR |

### 📦 Job dependency graph

```
[🏗️ GoReleaser · Build & Publish]
     │
     ├──► [🐳 Sign Docker Images]
     │
     ├──► [📋 Publish → MCP Registry]   (independent of docker-sign)
     │
     ├──► [🔨 Publish → Smithery]       (if SMITHERY_ENABLED)
     │
     ├──► [🐍 Publish → PyPI]           (if PYPI_PUBLISH_ENABLED)
     │
     └──► [📦 Update Packaging Manifests] (if PACKAGING_UPDATE_ENABLED)
```

All downstream jobs run in parallel after the core release job completes. A failure in any one job does not block the others.

### ⚡ Fast-fail ordering inside the core release job

Steps run in cheapest-first order so an early misconfiguration surfaces before slow Docker builds start:

```
1. Set up Go
2. Set up Python + verify Python client drift  ← fast fail
3. Verify macOS signing chain                  ← fast fail
4. Set up Chocolatey CLI (only if key set)     ← fast fail
5. Log in to Docker Hub + GHCR
6. Set up Docker Buildx + QEMU
7. Install cosign + Syft
8. Run GoReleaser  ← the slow step (cross-compile, sign, push)
9. Build + upload .mcpb bundles
10. Generate + attach SBOM
11. Sign checksums.txt with cosign (keyless OIDC)
12. Upload dist binaries artifact (for PyPI)
```

### 🔬 Core release steps in detail

| Step | What it does |
|------|-------------|
| Verify Python client drift | Fails fast if `make gen-python-client` was not run before tagging |
| Verify macOS signing chain | Checks p12 contains leaf + intermediate + Apple Root CA (prevents AMFI SIGKILL on launch) |
| Set up Chocolatey CLI | Installs `choco` under mono; `push` subcommand is non-fatal (Chocolatey moderation can 403) |
| Run GoReleaser | Cross-compiles 5 platforms; signs macOS (quill+notarize); signs Windows (jsign/Azure); pushes Docker multi-arch; updates Homebrew/Scoop/WinGet; publishes GitHub Release |
| Build .mcpb bundles | Assembles MCP bundle archives for Smithery + MCPB registries |
| Generate SBOM | Syft produces a full SPDX JSON; attached to the release |
| Sign checksums.txt | Cosign keyless OIDC signature; `.sig` + `.pem` attached to the release |
| Upload dist binaries | Artifact for the PyPI job to wrap into wheels without re-running GoReleaser |

### 🐳 [docker-sign] — Sign Docker images

Fetches the image digest from GHCR and signs it with cosign using GitHub's OIDC identity. Creates a publicly verifiable Sigstore signature.

### 📋 [publish-mcp-registry] — MCP Registry

Depends on `release` directly (not `docker-sign`) — the registry only needs the GitHub Release to exist, not the Docker signature. Re-syncs the version string and publishes via `mcp-publisher` + GitHub OIDC authentication.

### 🔨 [publish-smithery] — Smithery

Downloads the `.mcpb` bundle that was already built and uploaded by the `release` job (no re-build from source). Publishes to [Smithery](https://smithery.ai). Gated on `vars.SMITHERY_ENABLED == 'true'`.

### 🐍 [publish-pypi] — PyPI platform wheels

Downloads the cross-compiled binaries from the `release` job artifact, wraps them into platform wheels, smoke-tests the manylinux wheel (import check), then publishes via Trusted Publishing (OIDC — no token secret). Gated on `vars.PYPI_PUBLISH_ENABLED == 'true'`.

### 📦 [update-packaging] — AUR + Nix auto-PR

**Fully automated — no manual steps needed.**

```
1. Checkout main
2. Run scripts/update-packaging.sh <VERSION>
   └─ Fetches checksums.txt from the new release
   └─ Updates packaging/aur/PKGBUILD    (version)
   └─ Updates packaging/aur/.SRCINFO    (version + hex SHA256)
   └─ Updates packaging/nix/flake.nix   (version + SRI hashes, 4 platforms)
3. Push to branch: chore/packaging-vX.Y.Z
4. Open PR with gh pr create
5. Enable auto-merge (--squash --admin) so the PR merges itself
   once validate-packaging CI passes
```

Gated on `vars.PACKAGING_UPDATE_ENABLED == 'true'`. If the manifests are already up-to-date (e.g., a re-run), the job exits cleanly with no PR.

> **Why a PR instead of a direct push?** Branch protection requires PRs for all changes, including metadata bumps. The auto-merge flag means the PR merges itself within minutes — no human action needed.

---

## 📚 docs.yml — Docs deploy

### 📡 Trigger

Push to `main` touching any of:
- `README.md`, `ARCHITECTURE.md`, `CONTRIBUTING.md`
- `docs/**`, `decks/**`, `overrides/**`
- `assets/logo-final-transparent.svg`, `assets/favicon.ico`, `assets/demo.webm`, `assets/demo.mp4`
- `mkdocs.yml`, `.github/workflows/docs.yml`

### Steps

1. Checkout
2. Install `mkdocs-material` (pinned version)
3. Assemble `site_src/` from root + `docs/` files (with cross-link rewriting)
4. Generate `robots.txt`
5. Deploy to GitHub Pages via `mkdocs gh-deploy --force --remote-branch gh-pages`

> All `docs/*.md` links are rewritten from `](docs/FOO.md` → `](foo.md` so the published site URLs are clean. Root policy files (`SECURITY.md`, `CODE_OF_CONDUCT.md`) point to GitHub because they are not published to the site.

---

## 🔍 codeql.yml — Deep static analysis

### 📡 Triggers
- Push to `main` (excluding docs/assets)
- Any PR targeting `main` (same exclusion)
- Weekly schedule: every Monday at 06:00 UTC

Runs GitHub's CodeQL engine with `security-extended,security-and-quality` query suites. Results appear in the **Security → Code scanning** tab. Findings must be addressed or dismissed before they accumulate.

---

## 🛠️ Adding or changing a workflow

### Adding a job to ci.yml

1. If the job must run on every PR (e.g., a new drift gate): add it **without** a `needs: changes` dependency.
2. If the job is only relevant when Go code changes: gate it on `needs.changes.outputs.code == 'true'` and add `needs: [changes, lint]` (so formatting failures fast-fail first).
3. Add `packaging/**` to the `changes` allowlist if the new job should not run on packaging PRs.

### Adding a post-release distribution channel

1. Add a new job to `release.yml` with `needs: release`.
2. Gate it on a repo var (e.g., `vars.MY_CHANNEL_ENABLED == 'true'`) so an unconfigured fork is a clean no-op.
3. Use `|| echo "::warning::..."` for non-fatal publish failures so a channel hiccup doesn't block the overall release.
4. Document the required secret/var in the table above.

### Cutting a release

```bash
# 1. Merge your feature branch to main via PR
# 2. Bump version
echo "1.38.0" > VERSION
bash scripts/sync-version.sh
make sync-lenses
make gen-python-client
git add VERSION server.json .claude-plugin/plugin.json python/ internal/search/lenses_embed/
git commit -m "chore: bump VERSION to 1.38.0"
git push origin HEAD  # via PR

# 3. Tag + push
git tag v1.38.0
git push origin v1.38.0
# → release.yml starts automatically
# → update-packaging job opens and auto-merges chore/packaging-v1.38.0
```

---

## 🐛 Debugging a failed run

| Symptom | Where to look |
|---------|--------------|
| `gofmt` failure | Run `make fmt` locally, push again |
| `golangci-lint` failure | Run `go tool golangci-lint run` locally |
| `docs-drift` failure | `docs/TOOLS.md` section out of sync — update it or run `make gen-python-client` |
| `python-drift` failure | Run `make gen-python-client`, stage + commit the regenerated files |
| `validate-packaging` failure | Version mismatch across PKGBUILD / .SRCINFO / flake.nix — run `scripts/update-packaging.sh <version>` |
| GoReleaser failure | Check secrets are set; check `.goreleaser.yml` template syntax |
| Python drift fails during release | Tag pushed before `make gen-python-client` — rebuild on a new patch tag |
| macOS chain verify fails | Re-export the signing p12 with full chain (leaf + intermediate + Apple Root CA) |
| `update-packaging` PR not created | Check `vars.PACKAGING_UPDATE_ENABLED == 'true'`; check job logs |
| PyPI publish failure | Check `pypi` environment is configured with Trusted Publishing OIDC |
| Docker sign failure | Check GHCR image exists; check cosign OIDC token permissions |
| MCP Registry warning | Non-fatal; check `mcp-publisher` OIDC credentials, may already be published |
| Smithery warning | Non-fatal; check `SMITHERY_API_KEY` secret, may already be published |

---

## 🔗 See also

- [`CONTRIBUTING.md`](../CONTRIBUTING.md) — commit format, branch naming, PR process
- [`docs/DEPLOYMENT.md`](DEPLOYMENT.md) — Docker, Kubernetes, env vars
- [`docs/SECURITY_AND_COMPLIANCE.md`](SECURITY_AND_COMPLIANCE.md) — security rules that apply to CI changes
- [`.goreleaser.yml`](../.goreleaser.yml) — GoReleaser pipeline config (build matrix, signing, publishers)
- [`scripts/update-packaging.sh`](../scripts/update-packaging.sh) — packaging update script invoked by the CI job
