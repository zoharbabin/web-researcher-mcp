# Installing web-researcher-mcp

This guide is designed for AI coding agents (Cline, Claude Code, Cursor) to autonomously install and configure web-researcher-mcp.

## What this is

An MCP server that gives AI assistants web search, content extraction, academic paper lookup, patent search, SEC filings, US case law, economic data, and multi-step research capabilities — with real, verifiable citations.

## Prerequisites

One of:
- macOS (arm64 or amd64), Linux (amd64 or arm64), or Windows (amd64)
- OR Docker installed
- OR Go (version per `go.mod`) installed

## Option A: uvx (no compile, any OS)

Install [`uv`](https://docs.astral.sh/uv/) first if you don't have it:

```bash
# macOS/Linux
curl -LsSf https://astral.sh/uv/install.sh | sh
# Windows
winget install astral-sh.uv
```

Then add to your MCP client configuration:

**Claude Code** (run in terminal):
```bash
claude mcp add --scope user web-researcher -- uvx web-researcher-mcp
```

**Cursor / VS Code / Cline / other clients** (Settings > MCP Servers):
```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "uvx",
      "args": ["web-researcher-mcp"],
      "env": {
        "GOOGLE_CUSTOM_SEARCH_API_KEY": "YOUR_API_KEY",
        "GOOGLE_CUSTOM_SEARCH_ID": "YOUR_SEARCH_ENGINE_ID"
      }
    }
  }
}
```

`uvx` fetches the right prebuilt binary for your platform — no Go, no compile, no manual PATH setup. The `env` block is optional; omit it to run zero-config with DuckDuckGo.

## Option B: One-command install (macOS/Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/zoharbabin/web-researcher-mcp/main/install.sh | sh
```

Windows:
```powershell
powershell -ExecutionPolicy Bypass -c "irm https://raw.githubusercontent.com/zoharbabin/web-researcher-mcp/main/install.ps1 | iex"
```

Downloads the binary, verifies its SHA-256 checksum, puts it on your PATH, and registers it with Claude Code automatically when the `claude` CLI is present.

Then add to other MCP clients as in Option A (replacing `"command": "uvx", "args": ["web-researcher-mcp"]` with `"command": "web-researcher-mcp"`).

## Option C: Download binary manually

1. Download the latest release for your platform:
   ```text
   https://github.com/zoharbabin/web-researcher-mcp/releases/latest
   ```
   Files are named: `web-researcher-mcp_<version>_<os>_<arch>.tar.gz`

2. Extract and place the binary in your PATH (e.g., `~/.local/bin/` or `/usr/local/bin/`)

3. Configure as in Option A (use `"command": "web-researcher-mcp"` instead of uvx).

## Option D: Docker

```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "docker",
      "args": ["run", "-i", "--rm",
        "-e", "GOOGLE_CUSTOM_SEARCH_API_KEY=YOUR_API_KEY",
        "-e", "GOOGLE_CUSTOM_SEARCH_ID=YOUR_SEARCH_ENGINE_ID",
        "ghcr.io/zoharbabin/web-researcher-mcp:latest"
      ]
    }
  }
}
```

The Docker image bundles Chromium (with `CHROME_PATH` preset), so JavaScript-heavy pages render without an extra download.

## Option E: Go Install

```bash
go install github.com/zoharbabin/web-researcher-mcp/cmd/web-researcher-mcp@latest
```

Then configure as in Option C.

## Environment Variables

**None required.** With no configuration, the server uses DuckDuckGo as a zero-config fallback search provider (no API key needed). For optional provider keys and advanced configuration, see `.env.example` and `docs/DEPLOYMENT.md`.

## Available Tools

The differentiator is the **trust suite**: `verify_citation` checks whether one citation actually exists, matches a real record, has been retracted (live Crossref / Retraction Watch), and still resolves — evidence, never a verdict. `audit_bibliography` runs those same checks over a whole reference list (CSL-JSON / RIS / BibTeX / a session) and flags retracted, dead, fabricated (`not_found`), or mischaracterized entries. `archive_source` captures a source to the Internet Archive (Wayback / Save Page Now) so a link you cite can't quietly rot. `verify_recommendation` checks a recommended source for self-promotion, conflicts of interest, domain reputation, and dead links before you trust it.

Alongside those, the always-on core tools include web search, full-page/document scraping (`mode: raw` for verbatim source), combined search-and-scrape with quality ranking, image and news search, academic search (real DOIs), patent search (US/EP/WO/JP/CN/KR), US case-law search (`legal_search`, CourtListener), economic data (`econ_search`, FRED + World Bank + OECD + Eurostat), clinical-trial search (`clinical_search`, ClinicalTrials.gov), community-curated GitHub list search (`awesome_list_search`, ecosyste.ms), research-session export and bibliography formatting (APA/MLA/BibTeX/RIS/CSL-JSON), multi-step `sequential_search` with recoverable sessions, and `brand_research` for structured brand identity data (colors, logos, typography, social handles) from any domain or company name.

A few tools register only when their provider or config is present: `citation_graph` requires a citation-capable academic provider (OpenAlex or Semantic Scholar); `filing_search` (SEC EDGAR) requires `EDGAR_CONTACT_EMAIL` (falls back to `OPENALEX_EMAIL`); `answer` and `structured_search` require `EXA_API_KEY`; `local_search` (place/business search) registers only when `BRAVE_API_KEY` is set. Optional enrichment keys `COURTLISTENER_API_TOKEN` and `FRED_API_KEY` raise limits or add data to always-available tools.

Operators can additionally enable opt-in, consent-gated tools (per-user analytics, long-term memory, shared workspaces) that register only when their feature is turned on.

See [`docs/TOOLS.md`](docs/TOOLS.md) for the authoritative, CI-verified list with full parameter and output schemas (`internal/tools/registry.go` is the source of truth).

## Verification

After configuration, test by asking your AI assistant:
```text
Search for recent news about AI regulation
```

The assistant should invoke `news_search` and return results with real, clickable source URLs.

## Troubleshooting

- **"command not found"** — Ensure the binary is in your PATH or use the full path in the config
- **"invalid API key"** — Verify your Google API key is enabled for Custom Search API
- **No results** — Check that your Search Engine ID (cx) is configured to search the entire web
- **Timeout errors** — Each scrape tier has its own bounded timeout (a few seconds up to ~30s for browser rendering); slow or JavaScript-heavy sites may hit these and fall through to the next tier
- **Scraping private/internal URLs** — By default the server blocks private IP ranges (SSRF protection). Set `ALLOW_PRIVATE_IPS=true` to permit internal network URLs
