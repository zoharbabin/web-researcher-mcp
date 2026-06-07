# Release Signing

How the Windows `.exe` in each release is Authenticode-signed, and how to operate, rotate, and disable it.

## What & where

Windows binaries are signed with **Azure Trusted Signing** (a.k.a. Azure Artifact Signing) from the release job in `.github/workflows/release.yml`. Signing happens **in place on the Linux runner** via [`jsign`](https://ebourg.github.io/jsign/) — which calls the Azure signing REST endpoint directly, so there is no separate Windows job and no Wine.

Signing runs **inside GoReleaser**, as a `builds[].hooks.post` hook (`scripts/sign-windows.sh`) that fires right after the `.exe` is compiled but **before** the archive/checksum/Scoop/WinGet/Cask pipes. Because every downstream hash derives from the signed bytes, the release zip, `checksums.txt`, and the Scoop/WinGet/Cask manifests **all carry the signed-binary hash in a single GoReleaser run** — no post-hoc re-zip or checksum rewrite, and no manifest hash drift. (Earlier releases signed *after* GoReleaser had already published the Scoop/WinGet manifests with the unsigned hash, which broke `scoop install` and tripped WinGet's `Error-Hash-Mismatch`; doing it in the build hook is the fix.) The wrapper self-gates: it no-ops for non-Windows targets and whenever `AZURE_SIGNING_ENABLED != true`, so snapshot/local/credential-less builds ship unsigned exactly as before.

**macOS** binaries are signed with a **Developer ID Application** certificate and **notarized** by Apple via GoReleaser's cross-platform `notarize.macos` block (bundled `quill`) — on the same Linux runner, no macOS runner or GoReleaser Pro needed. This clears Gatekeeper's "developer cannot be verified" warning for users who download the darwin tarballs through a browser (the `com.apple.quarantine` path). Homebrew, the `curl | sh` installer, and Docker do not set quarantine, so they were never blocked. The signing+notarize step **self-gates on `MACOS_SIGN_P12` being set**, so absent the cert it ships unsigned darwin binaries exactly as before (zero regression).

> Stapling note: a bare Mach-O binary cannot be stapled, and it doesn't need to be — Gatekeeper verifies the notarization **online** on first launch. (The only gap is a fully offline first launch, irrelevant for a normally-networked CLI.) Shipping a stapled ticket would require a `.dmg`/`.pkg` + GoReleaser Pro.

**Linux** binaries need no OS-level code signing (Linux has no Gatekeeper/Authenticode equivalent for standalone binaries). Integrity for every platform is additionally covered by the **cosign signatures + SBOM** GoReleaser produces, plus cosign-signed Docker images.

## Toggle (mechanical)

- The signing steps run **only when** the repository variable `AZURE_SIGNING_ENABLED == 'true'`.
- Unset or `false` → releases ship the unsigned binary exactly as before (zero regression). This is the safe default and the fallback if signing ever breaks.

```bash
gh variable set AZURE_SIGNING_ENABLED --body true     # enable
gh variable set AZURE_SIGNING_ENABLED --body false    # disable
```

## Identity & certificate

- Signing identity (individual validation, Azure): `CN=Zohar Babin, O=Zohar Babin, L=Weston, S=fl, C=US`.
- Certificates issued by Trusted Signing are **short-lived (~3 days) and auto-rotate on every sign**. The signature stays valid after the cert expires because it is **RFC-3161 timestamped** (`http://timestamp.acs.microsoft.com`). This is expected behavior, not a misconfiguration.
- SmartScreen note: a valid signature establishes a **verified publisher**, but Microsoft SmartScreen reputation still builds with download volume — early downloaders may see a warning that fades over time. No certificate class (OV/EV/Azure) clears SmartScreen instantly.

## Azure resources (fixed inputs)

| Input | Value |
|-------|-------|
| Endpoint (region: East US) | `https://eus.codesigning.azure.net` (no trailing slash — jsign appends the API path, so a trailing slash 404s) |
| Signing account | `web-researcher-signing` |
| Certificate profile | `web-researcher-public` |
| jsign alias (`<account>/<profile>`) | `web-researcher-signing/web-researcher-public` |

These are non-secret and live in the workflow. The signing account requires a **paid** Azure subscription (Artifact Signing is unavailable on free/trial subscriptions). Individual eligibility is currently USA/Canada only.

## CI authentication (GitHub Actions secrets)

The release job authenticates as a service principal (`web-researcher-signing-ci`) that holds the **Trusted Signing Certificate Profile Signer** role on the signing account. Three repo secrets are consumed by the Azure CLI login, which then mints the short-lived signing token jsign uses:

| Secret | Purpose |
|--------|---------|
| `AZURE_TENANT_ID` | Directory (tenant) ID — identifier, not sensitive |
| `AZURE_CLIENT_ID` | App (client) ID — identifier, not sensitive |
| `AZURE_CLIENT_SECRET` | Service-principal client secret — **sensitive** |

`AZURE_SUBSCRIPTION_ID` is also set (used by other Azure flows; not required by signing).

## Rotating the client secret

The client secret expires (set when created). Rotate before expiry, or any time it may have been exposed:

1. Azure portal → **Entra ID → App registrations → `web-researcher-signing-ci` → Certificates & secrets** → **+ New client secret** → copy the new **Value**.
2. Update the GitHub secret without putting the value in shell history:
   ```bash
   gh secret set AZURE_CLIENT_SECRET   # paste at the prompt
   ```
3. Update the local keychain copy:
   ```bash
   security add-generic-password -a "$USER" -s AZURE_CLIENT_SECRET -w -U   # prompts for value
   ```
4. Delete the old secret in the Azure portal.

## macOS notarization setup (one-time)

Requires the Apple Developer Program ($99/yr). Produces two credentials → five GitHub secrets; once set, the next release auto-signs + notarizes the darwin binaries.

1. **Developer ID Application certificate (`.p12`):** at developer.apple.com → Certificates, create a CSR (Keychain Access) → certificate type **Developer ID Application** (not "Installer", not "Apple Development") → download `.cer` → import to Keychain → export with private key as `.p12` with a password.
   - `base64 -i cert.p12 | pbcopy` → `gh secret set MACOS_SIGN_P12` (paste); password → `gh secret set MACOS_SIGN_PASSWORD`.
2. **App Store Connect API key (`.p8`) for notarization:** appstoreconnect.apple.com → Users and Access → Integrations/Keys → generate a key with **Developer** access. Note the **Issuer ID** (UUID) and **Key ID**; download the `.p8` (once only).
   - `base64 -i AuthKey_XXXX.p8 | pbcopy` → `gh secret set MACOS_NOTARY_KEY` (paste); `gh secret set MACOS_NOTARY_KEY_ID` (the key id); `gh secret set MACOS_NOTARY_ISSUER_ID` (the issuer UUID).

| Secret | Contents |
|--------|----------|
| `MACOS_SIGN_P12` | base64 of the Developer ID Application `.p12` |
| `MACOS_SIGN_PASSWORD` | the `.p12` export password |
| `MACOS_NOTARY_KEY` | base64 of the App Store Connect `.p8` |
| `MACOS_NOTARY_KEY_ID` | App Store Connect key ID |
| `MACOS_NOTARY_ISSUER_ID` | App Store Connect issuer UUID |

Config lives in `.goreleaser.yml` (`notarize.macos`); secrets are threaded into the GoReleaser step in `.github/workflows/release.yml`. Notarization adds a few minutes to the release (`wait: true`).

## Verifying a signed release

On a Windows machine (or with `osslsigncode verify` on Linux/macOS), confirm the published `.exe` shows publisher **Zohar Babin** and a valid timestamp. The release `checksums.txt` entry for `*_windows_amd64.zip` must match the re-uploaded signed zip.

## Package-manager distribution (built on signing)

Signing unlocks the OS package managers — some validate/prefer signed binaries; signing also accrues SmartScreen/Gatekeeper reputation. All four publishers are configured in `.goreleaser.yml` and **gated**, so a release with no token/key set is byte-for-byte unchanged (the manifest is built but the push is skipped — same philosophy as the signing toggles).

| Channel | `.goreleaser.yml` block | Target | Secret / gate | Notes |
|---------|-------------------------|--------|---------------|-------|
| Homebrew Cask | `homebrew_casks` | `zoharbabin/homebrew-tap` (`Casks/`) | `HOMEBREW_TAP_GITHUB_TOKEN` (shared with the formula) | Ships the **notarized** darwin binary; `brew install --cask zoharbabin/tap/web-researcher-mcp`. `skip_upload: auto` skips prereleases. |
| Scoop | `scoops` | `zoharbabin/scoop-bucket` (`bucket/`) | `SCOOP_BUCKET_GITHUB_TOKEN` (gates `skip_upload`) | `scoop bucket add zoharbabin …; scoop install web-researcher-mcp`. |
| WinGet | `winget` | fork `zoharbabin/winget-pkgs` (manual PR to `microsoft/winget-pkgs`) | `WINGET_PKGS_GITHUB_TOKEN` (gates `skip_upload`) | Pushes the manifest to a branch on the fork; the maintainer opens the upstream PR manually (a fork-scoped fine-grained PAT cannot open a PR against `microsoft/winget-pkgs`, and the first submission needs manual Microsoft review anyway). Validation is smooth because the `.exe` is Azure-Trusted-Signing-signed. |
| Chocolatey | `chocolateys` | chocolatey.org (`push.chocolatey.org`) | `CHOCOLATEY_API_KEY` (workflow installs `choco` + un-`--skip`s the pipe only when set) | `choco install web-researcher-mcp`. The Linux runner gets `choco` via `mono`. |

**Tokens are configured** — `CHOCOLATEY_API_KEY` (account API key), `WINGET_PKGS_GITHUB_TOKEN`, and `SCOOP_BUCKET_GITHUB_TOKEN` are set as GitHub Actions secrets, so the next `v*` tag publishes to all four channels. To rotate or recreate a GitHub PAT (Chocolatey uses an account API key, not a PAT):

- **WinGet** — fine-grained PAT with **Contents: read/write + Pull requests: read/write** on `zoharbabin/winget-pkgs`. `gh secret set WINGET_PKGS_GITHUB_TOKEN`.
- **Scoop** — fine-grained PAT with **Contents: read/write** on `zoharbabin/scoop-bucket`. `gh secret set SCOOP_BUCKET_GITHUB_TOKEN`.

PATs cannot be minted by `gh`/the API (browser-only by design): GitHub → Settings → Developer settings → Fine-grained tokens.

> **First-release note:** the publishers activate on a release where the channel's secret is set. WinGet pushes the manifest to a branch on the `zoharbabin/winget-pkgs` fork; the maintainer then opens the PR to `microsoft/winget-pkgs` manually (fork-scoped PATs cannot open the upstream PR, and the first submission gets a one-time manual Microsoft review regardless). Chocolatey's first package goes through moderation. User-facing install instructions (`winget install` / `scoop install` / `choco install` / `brew install --cask`) are added to the README only once each channel's first package is confirmed live.

## Local secret convention (maintainer)

Secret **names** are registered in `~/.zshenv` (`_SECRETS`); **values** live in the macOS Keychain (`_keychain_set <NAME>`). The `AZURE_CLIENT_ID` / `AZURE_TENANT_ID` / `AZURE_CLIENT_SECRET`, the `MACOS_*`, and the package-publishing trio (`CHOCOLATEY_API_KEY`, `WINGET_PKGS_GITHUB_TOKEN`, `SCOOP_BUCKET_GITHUB_TOKEN`) all follow that pattern.
