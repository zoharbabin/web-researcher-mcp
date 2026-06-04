<!-- mcp-name: io.github.zoharbabin/web-researcher-mcp -->
<p align="center">
  <img src="assets/logo-final.svg" width="120" alt="web-researcher-mcp logo">
</p>
<h1 align="center">web-researcher-mcp</h1>
<p align="center">
  <strong>Your AI research assistant that cites real sources and stays honest.</strong>
</p>
<p align="center">
  Search the entire web or narrow it down to just the sites you trust;<br/>
  medical journals, court databases, news outlets, academic papers.<br/>
  Analyze the full source, not just snippets. Links that work, citations you can trust,<br/>
  no made up closed garden pre-synthesized results.
</p>

<p align="center">
  <a href="https://github.com/zoharbabin/web-researcher-mcp/actions/workflows/ci.yml"><img src="https://github.com/zoharbabin/web-researcher-mcp/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://goreportcard.com/report/github.com/zoharbabin/web-researcher-mcp"><img src="https://goreportcard.com/badge/github.com/zoharbabin/web-researcher-mcp" alt="Go Report Card"></a>
  <a href="https://pkg.go.dev/github.com/zoharbabin/web-researcher-mcp"><img src="https://pkg.go.dev/badge/github.com/zoharbabin/web-researcher-mcp.svg" alt="Go Reference"></a>
  <a href="https://opensource.org/licenses/MIT"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
  <a href="https://github.com/zoharbabin/web-researcher-mcp/releases"><img src="https://img.shields.io/github/v/release/zoharbabin/web-researcher-mcp" alt="Release"></a>
  <a href="https://hub.docker.com/r/zoharbabin/web-researcher-mcp"><img src="https://img.shields.io/docker/pulls/zoharbabin/web-researcher-mcp?cacheSeconds=3600" alt="Docker"></a>
  <a href="https://glama.ai/mcp/servers/zoharbabin/web-researcher-mcp"><img src="https://glama.ai/mcp/servers/zoharbabin/web-researcher-mcp/badges/score.svg" alt="web-researcher-mcp MCP server"></a>
  <a href="https://github.com/zoharbabin/web-researcher-mcp/stargazers"><img src="https://img.shields.io/github/stars/zoharbabin/web-researcher-mcp?style=social" alt="GitHub Stars"></a>
</p>

### Get started in 30 seconds

**macOS (Homebrew):**
```bash
brew install zoharbabin/tap/web-researcher-mcp
```

**macOS / Linux (no package manager):**
```bash
curl -fsSL https://raw.githubusercontent.com/zoharbabin/web-researcher-mcp/main/install.sh | sh
```

**Windows (PowerShell):**
```powershell
powershell -ExecutionPolicy Bypass -c "irm https://raw.githubusercontent.com/zoharbabin/web-researcher-mcp/main/install.ps1 | iex"
```

That's it — no dev tools needed. Downloads the binary, verifies its checksum, puts it on your PATH, and registers it with Claude Code automatically.

Your AI can now search the web, read full articles, find academic papers, look up patents, and run multi-step research — only from sources you pick.

---

## Why does this exist?

Perplexity [gets its citations wrong over a third of the time](https://www.cjr.org/tow_center/we-compared-eight-ai-search-engines-theyre-all-bad-at-citing-news.php). It links to papers that don't exist, invents DOIs, and presents SEO spam with the same confidence as peer-reviewed research. ChatGPT's web search isn't much better — it can't tell a blog post from a court filing.

If your work gets cited, published, submitted to a court, or shown to a client — you can't afford "probably real" sources.

**This tool fixes the root cause:** instead of searching the entire web and hoping, you tell your AI *exactly which sources to search*. We call these "search lenses" — curated lists of trusted sites for each field.

| What you get | What that means for you |
|---|---|
| **Search lenses** — choose your sources by field | Your AI only sees the sites you trust (PubMed, SEC.gov, arXiv — not random blogs) |
| **Research tools for every source type** | Papers, patents, news, web pages, images, full-text reading, and multi-step deep research |
| **Always has a backup** | Multiple search engines working together — if one has issues, the others pick up automatically |
| **Reads full articles** | Doesn't just give you snippets — extracts and reads entire pages, PDFs, Word docs, even YouTube transcripts |
| **Real citations, formatted** | Every source comes with a proper APA/MLA citation and a link that actually works |
| **Your queries stay private** | Runs on your machine — nobody sees what you're researching. Not us, not anyone. |
| **Paper trail** | Every search is logged so you can reproduce your research process months later |

Works with Claude, Claude Desktop, Cursor, and any AI assistant that supports tool use.

### Who uses this

- **Academic researchers** — "I need a literature review with real DOIs, not made-up citations"
- **Business analysts** — "My deliverable needs sources a client can actually click and verify"
- **Lawyers** — "If I cite a case that doesn't exist, I get fined $50,000"
- **Journalists** — "I need to cross-check government records and court filings, not Perplexity summaries"
- **Medical researchers** — "Clinical decisions based on a health blog could hurt someone"
- **Graduate students** — "I spent 3 hours tracking down a citation my AI invented"
- **Enterprise teams** — "Our competitive research can't go through a third party's servers"

---

https://github.com/user-attachments/assets/17fa3484-e4c5-4099-982d-785f544b3a94

---

## How It Compares

|  | web-researcher-mcp | Perplexity | Scite.ai | Elicit |
|---|---|---|---|---|
| You pick which sources are searched | **Yes** (built-in + custom lenses) | No | No | No |
| Makes up citations | **Never** — every link is real | ~37% incorrect | Rare (journals only) | Rare |
| Works across all fields | **Yes** — legal, medical, news, patents, everything | Yes | Journals only | Papers only |
| Keeps your research private | **Yes** — runs on your machine | No (they see everything) | No | No |
| Works inside your existing AI (Claude, Cursor, etc.) | **Yes** | No (separate app) | Partially | No (separate app) |
| Can read full articles, not just snippets | **Yes** — pages, PDFs, Word docs, YouTube | No | No | Limited |
| Cost | **Free forever** (open source) | $20/mo | $20/mo | $10-49/mo |

### When to use what

- **Perplexity** — Quick casual lookups where you don't need to cite your sources
- **Scite.ai / Elicit** — Browsing a specific database of academic papers
- **web-researcher-mcp** — Anything where your reputation is attached to the research: client work, court filings, publications, grant proposals, medical decisions, journalism
- **Claude built-in search** — Quick one-off lookups mid-conversation

---

## What your AI can do with this

| Tool | What it does |
|------|-------------|
| `web_search` | Search the web — optionally restricted to only the sources you trust via lenses |
| `scrape_page` | Read any URL in full — web pages, PDFs, Word docs, slideshows, YouTube transcripts; supports `mode: raw` for verbatim, unsanitized source (e.g. inspecting JSON or HTML) |
| `search_and_scrape` | Search and then read the best results — with quality scoring to surface the most reliable sources |
| `image_search` | Find images by size, type, color, or format |
| `news_search` | Search recent news with date controls and source filtering |
| `academic_search` | Find real papers with real DOIs — authors, citation counts, open-access links |
| `patent_search` | Search patent offices (US, Europe, international) with classification codes |
| `sequential_search` | Multi-step deep research — your AI remembers what it already found and builds on it |
| `get_research_session` | Recover a research session after context loss — picks up right where you left off |

These are the always-on core tools. Operators can also enable opt-in, consent-gated tools (per-user analytics, long-term memory, shared workspaces) that appear only when their feature is turned on — see [`docs/TOOLS.md`](docs/TOOLS.md) for the authoritative, CI-verified tool list and full schemas.

### Ready-made research templates

The server also ships guided **prompt templates** your AI assistant can pull in with one click — they walk it through a proven, multi-step process so you don't have to spell out every instruction:

| Template | What it guides your AI to do |
|----------|------------------------------|
| `comprehensive-research` | Run a structured, multi-step deep dive on a topic |
| `fact-check` | Verify a claim against multiple independent sources |
| `competitive-analysis` | Size up a company and its market (news, patents, web) |
| `literature-review` | Systematically review academic literature on a topic |

In most AI apps these show up wherever you pick a prompt or "/" command. The server exposes live **status resources** too (`stats://tools`, `stats://sessions`, `stats://rate-limits`, `stats://providers`) so you — or your AI — can check usage, limits, and which providers are active. See [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md#mcp-resources--prompts) for the full list.

---

## Quick Start

### Option 1: Homebrew (macOS / Linux — recommended)

```bash
brew install zoharbabin/tap/web-researcher-mcp
claude mcp add --scope user web-researcher -- web-researcher-mcp
```

Homebrew handles trust, updates, and PATH for you — no signing warnings.

### Option 2: One-command install (any OS — no dev tools needed)

**macOS / Linux:**
```bash
curl -fsSL https://raw.githubusercontent.com/zoharbabin/web-researcher-mcp/main/install.sh | sh
```

**Windows (PowerShell):**
```powershell
powershell -ExecutionPolicy Bypass -c "irm https://raw.githubusercontent.com/zoharbabin/web-researcher-mcp/main/install.ps1 | iex"
```

Downloads the binary, verifies its SHA-256 checksum against the signed release, puts it on your PATH, and registers it with Claude Code if installed. Customize the install location:

```bash
INSTALL_DIR=/opt/tools curl -fsSL https://raw.githubusercontent.com/zoharbabin/web-researcher-mcp/main/install.sh | sh
```

<details>
<summary><strong>Other install methods</strong></summary>

**Go install** (if you have Go):
```bash
go install github.com/zoharbabin/web-researcher-mcp/cmd/web-researcher-mcp@latest
claude mcp add --scope user web-researcher -- web-researcher-mcp
```

**Docker:**
```bash
# STDIO mode needs -i so the container's stdin stays attached for MCP JSON-RPC
docker run -i --rm \
           -e GOOGLE_CUSTOM_SEARCH_API_KEY=YOUR_KEY \
           -e GOOGLE_CUSTOM_SEARCH_ID=YOUR_CX \
           docker.io/zoharbabin/web-researcher-mcp:latest
```

**Build from source:**
```bash
git clone https://github.com/zoharbabin/web-researcher-mcp.git
cd web-researcher-mcp
go build -o web-researcher-mcp ./cmd/web-researcher-mcp
```

</details>

### Connect to Your AI Assistant

The install script registers with Claude Code automatically. For other apps, add to your AI's config file:

```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "web-researcher-mcp",
      "env": {
        "GOOGLE_CUSTOM_SEARCH_API_KEY": "YOUR_GOOGLE_API_KEY",
        "GOOGLE_CUSTOM_SEARCH_ID": "YOUR_SEARCH_ENGINE_ID"
      }
    }
  }
}
```

Or with Brave Search (no Google keys needed):

```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "web-researcher-mcp",
      "env": {
        "SEARCH_ROUTING": "brave",
        "BRAVE_API_KEY": "YOUR_BRAVE_API_KEY"
      }
    }
  }
}
```

Done. Your AI assistant now has access to all research tools.

---

## Configuration

**No API key required.** DuckDuckGo is the zero-config fallback — install and go. For higher quality, more results, and image/news search, add one of the optional providers below.

### Option A: Google

| Variable | What it is | Where to get it |
|----------|-------------|-----------|
| `GOOGLE_CUSTOM_SEARCH_API_KEY` | Your Google API key | [Get one here](https://developers.google.com/custom-search/v1/introduction) (free, 100 searches/day) |
| `GOOGLE_CUSTOM_SEARCH_ID` | Your search engine ID | [Create one here](https://programmablesearchengine.google.com/) |

### Option B: Brave Search (simpler signup)

| Variable | What it is | Where to get it |
|----------|-------------|-----------|
| `BRAVE_API_KEY` | Your Brave API key | [Get one here](https://brave.com/search/api/) (free tier available) |

Set `SEARCH_PROVIDER=brave` and you're done. No Google keys needed.

> **Tip:** You can set up multiple providers for automatic backup — see [Search Providers](#search-providers) below.

<details>
<summary><strong>All Search Provider Options</strong></summary>

| Variable | Description | Default |
|----------|-------------|---------|
| `SEARCH_PROVIDER` | Which engine to use: `google`, `brave`, `serper`, `searxng`, `searchapi`, `duckduckgo`, or `tavily` | `google` (falls back to `duckduckgo` if no key is set) |
| `BRAVE_API_KEY` | Brave Search API key | |
| `SERPER_API_KEY` | Serper.dev API key (uses Google results) | |
| `SEARCHAPI_API_KEY` | SearchAPI.io key | |
| `SEARXNG_URL` | Your own SearXNG instance (fully private, no third-party API needed) | |
| `SEARCH_ROUTING` | Use multiple providers with automatic backup (see [docs](docs/DEPLOYMENT.md#multi-provider-routing)) | |

</details>

### Academic Search (Optional — no signup needed)

| Variable | What to put | Why |
|----------|-------------|-----|
| `OPENALEX_EMAIL` | Your email address | Unlocks faster access to OpenAlex's full catalog of scholarly works — no registration, just an email |
| `CROSSREF_EMAIL` | Your email address | Same — faster access to DOI metadata for citations |

> With these set, `academic_search` returns real papers with DOIs, authors, citation counts, and open-access PDF links. Without them, it still works but uses web search as a fallback.

### Patent Search (Optional)

| Variable | What it is | Where to get it |
|----------|-------------|-----------|
| `EPO_OPS_CONSUMER_KEY` | European Patent Office key | [developers.epo.org](https://developers.epo.org) (free) |
| `EPO_OPS_CONSUMER_SECRET` | EPO secret | Same as above |
| `USPTO_API_KEY` | US patent office key | [developer.uspto.gov](https://developer.uspto.gov) (free) |
| `LENS_API_TOKEN` | The Lens (patents + scholarly) | [lens.org](https://www.lens.org) |

> With these, `patent_search` returns structured patent data with classification codes, dates, and inventors. Without them, it falls back to web search.

<details>
<summary><strong>Advanced: HTTP mode, OAuth, and all other settings</strong></summary>

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | Run as a web server (for team/shared setups) | Off (runs locally) |
| `OAUTH_ISSUER_URL` | Authentication server URL (for team access control) | |
| `OAUTH_AUDIENCE` | Expected audience claim | |

See [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md#environment-variables) for the complete list of all settings (cache, rate limiting, scraping, observability, etc.).

</details>

---

## Under the Hood

<details>
<summary><strong>Architecture (for developers and contributors)</strong></summary>

```
web-researcher-mcp/
├── cmd/web-researcher-mcp/     # Entry point (wiring only)
├── internal/
│   ├── config/                 # Env-based strongly-typed configuration
│   ├── server/                 # MCP server lifecycle + signal handling
│   ├── tools/                  # Tool handlers (one file per tool)
│   ├── search/                 # Pluggable search providers + router + lens routing
│   ├── scraper/                # 4-tier scraping pipeline (markdown → stealth → HTML → browser)
│   ├── documents/              # PDF, DOCX, PPTX parsing
│   ├── cache/                  # Hybrid cache (memory + AES-encrypted disk)
│   ├── auth/                   # OAuth 2.1 middleware + JWKS
│   ├── audit/                  # Structured audit logging
│   ├── session/                # Per-tenant session persistence (memory index + encrypted disk)
│   ├── content/                # Sanitize, dedup, truncate, quality score
│   ├── metrics/                # Prometheus metrics + per-tool stats
│   ├── ratelimit/              # Three-tier rate limiting
│   ├── circuit/                # Circuit breaker for external APIs
│   ├── persist/                # TTL key/value store (memory or encrypted disk) for token revocation + rate quotas
│   └── resources/              # MCP Resources + Prompts
├── lenses/                     # Search lens JSON files
└── docs/                       # Extended documentation
```

<details>
<summary><strong>High-Level Architecture Diagram</strong></summary>

The full layered diagram (MCP transports → tool dispatch → service layer → infrastructure) and the per-package map live in **[ARCHITECTURE.md](ARCHITECTURE.md)** — kept in one place to avoid drift.

</details>

<details>
<summary><strong>Design Principles (for developers)</strong></summary>

1. **Zero global state** -- all dependencies injected via constructors
2. **Interface-driven** -- every external dependency behind an interface for testing and swapping
3. **Bounded concurrency** -- explicit semaphores for external API calls
4. **Defense in depth** -- SSRF protection, rate limiting, content sanitization at every layer
5. **Fail loud** -- errors returned, never swallowed; validation at boundaries

</details>

</details>

---

## Search Providers

You choose which search engine powers your research. All of them work with lenses.

| Provider | Whole-Web | Images | News | Notes |
|----------|:---------:|:------:|:----:|-------|
| **DuckDuckGo** | Yes | — | — | Zero-config default (no API key needed); rate-limited for heavy use |
| **Google PSE** | Yes | Yes | Yes | Best quality; free tier: 100 queries/day |
| **Brave Search** | Yes | Yes | Yes | Recommended for high-volume whole-web |
| **Serper.dev** | Yes | Yes | Yes | Google-identical results |
| **SearXNG** | Yes | Yes | Yes | Self-hosted, privacy-first, air-gapped deployments |
| **SearchAPI.io** | Yes | Yes | Yes | Unified API with multiple engine backends |

### Multiple Providers (recommended)

Set up multiple search engines so if one has issues, your research doesn't stop:

```bash
export SEARCH_ROUTING=brave,google,serper
```

If Brave is down, it automatically tries Google. If Google is rate-limited, it falls through to Serper. Your research just works.

See [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md#multi-provider-routing) for advanced routing options (per-topic routing, patent-specific providers, etc.).

### Single Provider

If you only have one search API key, that works too — just set it up and go.

<details>
<summary><strong>Provider Setup Examples</strong></summary>

**Multi-provider routing (recommended):**
```bash
export SEARCH_ROUTING=brave,google,serper
export BRAVE_API_KEY=BSAxxxxxxxxxx
export GOOGLE_CUSTOM_SEARCH_API_KEY=AIza...
export GOOGLE_CUSTOM_SEARCH_ID=017...
export SERPER_API_KEY=...
```

**Single provider — Brave Search:**
```bash
export SEARCH_PROVIDER=brave
export BRAVE_API_KEY=BSAxxxxxxxxxx
```

**Single provider — SearXNG (self-hosted, privacy-first):**
```bash
export SEARCH_PROVIDER=searxng
export SEARXNG_URL=http://localhost:8080
```

**Single provider — Google PSE only (simplest setup):**
```bash
export GOOGLE_CUSTOM_SEARCH_API_KEY=AIza...
export GOOGLE_CUSTOM_SEARCH_ID=017...
# SEARCH_PROVIDER defaults to "google"
```

</details>

---

## Search Lenses

Search lenses let you control which websites your AI is allowed to search. Instead of searching the entire web (and getting blogs, spam, and AI-generated junk), a lens restricts results to only the sources you trust for that topic.

### Built-in Lenses

| Lens | Focus |
|------|-------|
| `docs` | Official documentation and API references only |
| `academic` | Preprint servers, repositories, open-access journals |
| `clinical` | Clinical trials, drug safety, evidence-based medicine |
| `security` | CVEs, advisories, vulnerability research |
| `journalism` | Public records, corporate filings, FOIA |
| `programming` | Code docs, tutorials, Q&A |
| `news` | Current events, journalism |
| `tech` | Technology industry |
| `legal` | Law, cases, statutes |
| `medical` | Health, medicine |
| `finance` | Markets, filings |
| `science` | Research, papers |
| `government` | Policy, regulations |

You can also [create your own lenses](#search-lenses) for any field — just list the domains you trust.

### How it works

When you (or your AI) use a lens, results come only from the sites in that lens. For example, using the `medical` lens means your AI searches PubMed, WHO, NIH, and other clinical sources — never health blogs or supplement ads.

Your AI uses lenses automatically when you ask it to. For example: *"Search for recent findings on SGLT2 inhibitors using the clinical lens."*

<details>
<summary><strong>Creating Your Own Lens</strong></summary>

Add a JSON file to the `lenses/` directory with the sites you trust:

```json
{
  "name": "my-industry",
  "description": "Only searches sources I trust for my field",
  "domains": [
    "trusted-source.com",
    "industry-journal.org",
    "official-database.gov"
  ],
  "cx": "",
  "routing": ""
}
```

That's it. Now your AI will only search those sites when you use this lens. You can add up to ~10 domains per lens.

**Advanced options** (optional — most users can ignore these):
- **cx** — If you have a Google Programmable Search Engine with up to 5,000 domains, put the engine ID here
- **routing** — Force this lens to use a specific search provider (e.g., `"google"`)

</details>

---

## Privacy & Security

Your research queries go directly from your machine to the search provider you chose. They never pass through our servers (we don't have servers). The tool runs entirely on your computer.

<details>
<summary><strong>Technical security details (for enterprise / compliance teams)</strong></summary>

- **SSRF protection** — blocks internal network access, cloud metadata endpoints, DNS rebinding attacks
- **OAuth 2.1** (HTTP mode) — JWKS token validation, per-tenant isolation, audience/issuer validation
- **Rate limiting** (HTTP mode) — per-tenant + global limits to protect upstream APIs
- **Content sanitization** — HTML cleaned via whitelist policy, deduplication, quality scoring

For the full threat model, see [docs/SECURITY.md](docs/SECURITY.md).

</details>

---

## Setup for Each AI App

### Claude Code

Add to your MCP config (`~/.claude.json`):

```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "/path/to/web-researcher-mcp",
      "env": {
        "GOOGLE_CUSTOM_SEARCH_API_KEY": "AIza...",
        "GOOGLE_CUSTOM_SEARCH_ID": "017...",
        "SEARCH_PROVIDER": "brave",
        "BRAVE_API_KEY": "BSA..."
      }
    }
  }
}
```

### Claude Desktop

Add to `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "/path/to/web-researcher-mcp",
      "env": {
        "GOOGLE_CUSTOM_SEARCH_API_KEY": "AIza...",
        "GOOGLE_CUSTOM_SEARCH_ID": "017..."
      }
    }
  }
}
```

### Cursor

Add to `.cursor/mcp.json` in your project root:

```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "/path/to/web-researcher-mcp",
      "env": {
        "GOOGLE_CUSTOM_SEARCH_API_KEY": "AIza...",
        "GOOGLE_CUSTOM_SEARCH_ID": "017..."
      }
    }
  }
}
```

### HTTP Mode (Teams / Shared Server)

For teams that want one shared instance everyone connects to:

```bash
PORT=3000 \
OAUTH_ISSUER_URL=https://auth.example.com \
OAUTH_AUDIENCE=https://api.example.com \
./web-researcher-mcp
```

Then connect any AI app to `http://localhost:3000/mcp/`.

<details>
<summary><strong>Docker Compose Example</strong></summary>

```yaml
services:
  web-researcher:
    image: zoharbabin/web-researcher-mcp
    ports:
      - "3000:3000"
    environment:
      PORT: "3000"
      GOOGLE_CUSTOM_SEARCH_API_KEY: ${GOOGLE_CUSTOM_SEARCH_API_KEY}
      GOOGLE_CUSTOM_SEARCH_ID: ${GOOGLE_CUSTOM_SEARCH_ID}
      SEARCH_PROVIDER: brave
      BRAVE_API_KEY: ${BRAVE_API_KEY}
```

</details>

> **Note:** Tool behavior is identical across all connection modes (STDIO and HTTP). The only differences are auth (HTTP requires OAuth) and rate limiting (HTTP enforces per-tenant limits; STDIO has only upstream API quotas). See [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) for details.

---

## Performance

Searches come back in under a second. Previously-seen results are cached so repeats are instant. Full article extraction works on 95%+ of the web — including sites that try to block bots. Heavy JavaScript sites get a real browser behind the scenes (automatic, no setup needed).

---

## Development

```bash
go build -o web-researcher-mcp ./cmd/web-researcher-mcp   # Build
go test -race ./...                                        # Test (with race detector)
make verify                                                # Full gate: fmt, vet, lint, gosec, govulncheck, tests, E2E, build
```

The lint, gosec, and govulncheck tools are pinned as `go.mod` tool directives, so `make verify` runs them at the exact versions CI uses (no global installs needed). Branch protection requires the Lint, Test, Security, and E2E checks to pass.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full development workflow, code style guide, and PR process.

---

## Troubleshooting

<details>
<summary><strong>Server starts but tools fail with "API key" errors</strong></summary>

The server starts even with missing credentials (to allow MCP handshake). Set your API keys in the `env` block of your MCP client config, not in your shell profile.

</details>

<details>
<summary><strong>Some pages come back empty</strong></summary>

For JavaScript-heavy sites, the tool uses a real browser (Chromium). With the binary install it auto-downloads on first use (~200MB). If you already have Chrome installed, set `CHROME_PATH` to point to it. The Docker image ships with Chromium bundled (`CHROME_PATH` preset), so JavaScript rendering works out of the box — no download.

</details>

<details>
<summary><strong>Cache serving stale results after upgrade</strong></summary>

The disk cache lives at your OS cache directory (e.g., `~/Library/Caches/web-researcher-mcp/` on macOS, `~/.cache/web-researcher-mcp/` on Linux). Delete that directory to clear it, or set `CACHE_DIR` to a custom path.

</details>

<details>
<summary><strong>Hitting search limits (429 errors)</strong></summary>

Google's free tier allows 100 searches/day. If you're hitting that:
- Switch to Brave Search (`SEARCH_PROVIDER=brave`) — more generous free tier
- Set up multiple providers (`SEARCH_ROUTING=brave,google`) — if one is rate-limited, it uses the other
- Or upgrade Google to paid ($5 per 1,000 searches)

</details>

<details>
<summary><strong>macOS: "Failed to reconnect" / error -32000 after a manual update</strong></summary>

This happens only if you replaced the binary by copying new bytes *over* the existing file in place (`cp new /path/to/web-researcher-mcp`). On Apple Silicon, macOS caches the binary's ad-hoc code signature against the file, and overwriting it in place can make the next launch get killed before it starts. The official installers (Homebrew, the one-command `install.sh`, and the Claude Code plugin) avoid this by installing to a fresh file. To fix a manual install, replace it cleanly and re-sign:

```bash
rm -f /path/to/web-researcher-mcp
cp /path/to/new-build /path/to/web-researcher-mcp
codesign --force -s - /path/to/web-researcher-mcp   # ad-hoc re-sign
```

Then reconnect your client. (Re-running `install.sh` does this correctly for you.)

</details>

---

## Contributing

Contributions are welcome. Please see [CONTRIBUTING.md](CONTRIBUTING.md) for code style guidelines, development workflow, and how to submit pull requests.

---

## Documentation

| Document | Description |
|----------|-------------|
| [ARCHITECTURE.md](ARCHITECTURE.md) | Design decisions, technology stack, dependencies |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Development setup, code style, PR workflow |
| [docs/TOOLS.md](docs/TOOLS.md) | Tool specifications and parameter schemas |
| [docs/EXAMPLES.md](docs/EXAMPLES.md) | Usage examples with JSON tool calls |
| [docs/API_SETUP.md](docs/API_SETUP.md) | Search provider API key setup for all providers |
| [docs/SECURITY.md](docs/SECURITY.md) | Threat model, SSRF, auth, compliance (SOC2/GDPR/FedRAMP) |
| [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) | Build, Docker, Kubernetes, client configs, scaling |
| [docs/LESSONS_LEARNED.md](docs/LESSONS_LEARNED.md) | Node.js to Go migration story and lessons |
| [docs/SESSION_PERSISTENCE.md](docs/SESSION_PERSISTENCE.md) | How sessions survive context loss — design, data flow, citations |
| [docs/MIGRATION.md](docs/MIGRATION.md) | Migrating from the deprecated google-researcher-mcp |

---

## License

[MIT](LICENSE)

---

## Star History

[![Star History Chart](https://api.star-history.com/chart?repos=zoharbabin/web-researcher-mcp&type=date&legend=top-left)](https://www.star-history.com/?repos=zoharbabin%2Fweb-researcher-mcp&type=date&legend=top-left)

---

<p align="center">
  Built with <a href="https://go.dev">Go</a> and the <a href="https://modelcontextprotocol.io/">Model Context Protocol</a>
  <br/><br/>
  If you're tired of AI making things up, give this a try — and a ⭐ if it helps.
</p>
