# Search Lenses

A **lens** restricts a `web_search` to a curated set of trusted, authority-weighted sources for a field — the project's core differentiator. Each lens is a single JSON file in this directory; the server loads them all at startup, and operators can add their own via `CUSTOM_LENSES_PATH`.

## Schema

```json
{
  "name": "legal",
  "description": "One line: what this lens scopes search to.",
  "domains": ["law.cornell.edu", "courtlistener.com", "supremecourt.gov"],
  "cx": "",
  "goggle": "",
  "routing": ""
}
```

| Field | Required | Meaning |
|-------|----------|---------|
| `name` | yes | The lens id users pass as `lens: <name>`. Must be unique (a later load overrides an earlier one). |
| `description` | recommended | Shown in tool docs/UX; keep to one line. |
| `domains` | one of `domains`/`cx`/`goggle` | Hosts injected as `site:` operators (up to 10 used). A host, optionally path-scoped (`github.com/advisories`). No scheme, no spaces. |
| `cx` | one of `domains`/`cx`/`goggle` | A dedicated Google Programmable Search Engine id. When set, the lens skips `site:` operator injection — the PSE engine is expected to already scope results to the intended domains. The cx value itself must be configured as the global `GOOGLE_CUSTOM_SEARCH_ID`; this field is a documentation signal, not a per-request routing override. |
| `goggle` | one of `domains`/`cx`/`goggle` | A Brave Goggle URL (`https://` required). When set, the Goggle is passed to the Brave provider for server-side re-ranking. Use with `SEARCH_PROVIDER=brave` or a routing config that includes Brave. See [`programming-goggle.json`](programming-goggle.json) for an example. |
| `routing` | no | Optional provider routing hint. |

A lens **must** define at least one of `domains`, `cx`, or `goggle` — otherwise it never restricts a search. This is enforced by `search.ValidateLens` (`internal/search/lenses.go`); an invalid lens fails `make validate-lenses`. For custom lenses loaded via `CUSTOM_LENSES_PATH`, an invalid lens also fails startup in HTTP mode (`PORT` set); in STDIO mode it is a warning.

## Add a bundled lens

1. Add `lenses/<name>.json` following the schema above (template: [`academic.json`](academic.json)).
2. `make validate-lenses` to check it.
3. Run `make sync-lenses` to copy it into `internal/search/lenses_embed/` (the `go:embed` source). Without this step the new lens is absent from the compiled binary.
4. No other code change needed — the registry loads all JSON files at startup.

## Add your own lenses without forking

Point `CUSTOM_LENSES_PATH` at a directory of lens JSON files. They load **after** the bundled set, so a custom lens with an existing `name` **overrides** the bundled one (last write wins) — the mechanism for an org-specific allowlist or a tuned vertical pack. See `docs/DEPLOYMENT.md`.

## Vertical packs

Some lenses are **vertical packs**: curated authority lists for a high-stakes field, designed to pair with the matching structured tool so the model grounds on primary sources instead of hallucinating.

| Pack (lens) | Pair with | What it grounds on |
|-------------|-----------|--------------------|
| `legal` | `legal_search` (CourtListener) + `verify_citation` | Official court opinions, statutes (`congress.gov`, `law.cornell.edu`), regulations (`ecfr.gov`), and high-authority secondary sources. Use `legal_search` to retrieve real opinions and `verify_citation` to confirm a cited case exists — the anti-hallucination guardrail for legal work. `legal_search` also scopes by `jurisdiction` (e.g. `scotus`, `ca9`). |
| `finance` | `filing_search` (SEC EDGAR) + `econ_search` (FRED/World Bank/OECD/Eurostat) | Primary financial disclosures and economic series. |
| `medical` / `clinical` | (academic providers) + `verify_citation` retraction check | Peer-reviewed and clinical primary sources; `verify_citation` flags retracted studies. |
| `osint` | `company-recon` prompt + `scrape_page` | Certificate transparency (`crt.sh`), DNS history (`securitytrails.com`, `dnsdumpster.com`), internet-wide scans (`censys.io`, `urlscan.io`), archive mining (`web.archive.org`, `index.commoncrawl.org`), analytics correlation (`publicwww.com`), active asset discovery (`hackertarget.com`), code search (`github.com`), corporate registries (`opencorporates.com`). Pair with the `company-recon` MCP prompt for a full OSINT pipeline. |

These are **curated authority lists, not a guarantee of correctness** — they bias retrieval toward trustworthy sources; the model and the user still judge the content. Extend or replace them for your context via `CUSTOM_LENSES_PATH`.

## Validate

```bash
make validate-lenses
```
