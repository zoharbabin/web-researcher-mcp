# Release Signing

How the Windows `.exe` in each release is Authenticode-signed, and how to operate, rotate, and disable it.

## What & where

Windows binaries are signed with **Azure Trusted Signing** (a.k.a. Azure Artifact Signing) from the release job in `.github/workflows/release.yml`. Signing happens **in place on the Linux runner** via [`jsign`](https://ebourg.github.io/jsign/) — which calls the Azure signing REST endpoint directly, so there is no separate Windows job and no Wine. The job then re-packages the Windows zip and rewrites its `checksums.txt` entry so the published artifact and checksum cover the **signed** binary.

macOS/Linux binaries are not Authenticode-signed (not applicable); release integrity for all platforms is additionally covered by the cosign signatures + SBOM produced by GoReleaser.

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
| Endpoint (region: East US) | `https://eus.codesigning.azure.net/` |
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

## Verifying a signed release

On a Windows machine (or with `osslsigncode verify` on Linux/macOS), confirm the published `.exe` shows publisher **Zohar Babin** and a valid timestamp. The release `checksums.txt` entry for `*_windows_amd64.zip` must match the re-uploaded signed zip.

## Local secret convention (maintainer)

Secret **names** are registered in `~/.zshenv` (`_SECRETS`); **values** live in the macOS Keychain (`_keychain_set <NAME>`). The `AZURE_CLIENT_ID` / `AZURE_TENANT_ID` / `AZURE_CLIENT_SECRET` entries follow that pattern.
