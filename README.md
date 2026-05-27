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

```bash
go install github.com/zoharbabin/web-researcher-mcp/cmd/web-researcher-mcp@latest
claude mcp add web-researcher -- web-researcher-mcp
```

That's it. Your AI can now search the web, read full articles, find academic papers, look up patents, and run multi-step research — only from sources you pick.

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
| `scrape_page` | Read any URL in full — web pages, PDFs, Word docs, slideshows, YouTube transcripts |
| `search_and_scrape` | Search and then read the best results — with quality scoring to surface the most reliable sources |
| `image_search` | Find images by size, type, color, or format |
| `news_search` | Search recent news with date controls and source filtering |
| `academic_search` | Find real papers with real DOIs — authors, citation counts, open-access links |
| `patent_search` | Search patent offices (US, Europe, international) with classification codes |
| `sequential_search` | Multi-step deep research — your AI remembers what it already found and builds on it |

---

## Quick Start

### Option 1: Download (simplest)

Download the ready-to-use binary for your system from [Releases](https://github.com/zoharbabin/web-researcher-mcp/releases). No programming tools needed.

### Option 2: One command (if you have Go installed)

```bash
go install github.com/zoharbabin/web-researcher-mcp/cmd/web-researcher-mcp@latest
```

### Option 3: Docker

```bash
docker run -e GOOGLE_CUSTOM_SEARCH_API_KEY=YOUR_KEY \
           -e GOOGLE_CUSTOM_SEARCH_ID=YOUR_CX \
           docker.io/zoharbabin/web-researcher-mcp:latest
```

<details>
<summary><strong>Option 4: Build from source</strong></summary>

```bash
git clone https://github.com/zoharbabin/web-researcher-mcp.git
cd web-researcher-mcp
go build -o web-researcher-mcp ./cmd/web-researcher-mcp
```

</details>

### Connect to Your AI Assistant

Tell your AI where to find the tool. Here's how for each app:

**Claude Code** (terminal — fastest setup):
```bash
claude mcp add --scope user --transport stdio web-researcher -- web-researcher-mcp
```

**Or add manually** to your AI's config file:

```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "/path/to/web-researcher-mcp",
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
      "command": "/path/to/web-researcher-mcp",
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

You need at least one search provider's API key. Pick whichever is easiest for you:

### Option A: Google (default)

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
| `SEARCH_PROVIDER` | Which engine to use: `google`, `brave`, `serper`, `searxng`, or `searchapi` | `google` |
| `BRAVE_API_KEY` | Brave Search API key | |
| `SERPER_API_KEY` | Serper.dev API key (uses Google results) | |
| `SEARCHAPI_API_KEY` | SearchAPI.io key | |
| `SEARXNG_URL` | Your own SearXNG instance (fully private, no third-party API needed) | |
| `SEARCH_ROUTING` | Use multiple providers with automatic backup (see [docs](docs/DEPLOYMENT.md#multi-provider-routing)) | |

</details>

### Academic Search (Optional — no signup needed)

| Variable | What to put | Why |
|----------|-------------|-----|
| `OPENALEX_EMAIL` | Your email address | Unlocks faster access to 250M+ scholarly works — no registration, just an email |
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
│   ├── session/                # Per-tenant session management
│   ├── content/                # Sanitize, dedup, truncate, quality score
│   ├── metrics/                # Prometheus metrics + per-tool stats
│   ├── ratelimit/              # Three-tier rate limiting
│   ├── circuit/                # Circuit breaker for external APIs
│   └── resources/              # MCP Resources + Prompts
├── lenses/                     # Search lens JSON files
└── docs/                       # Extended documentation
```

<details>
<summary><strong>High-Level Architecture Diagram</strong></summary>

```
┌─────────────────────────────────────────────────────────────────┐
│                         MCP Protocol Layer                        │
│  ┌──────────────────┐              ┌─────────────────────────┐  │
│  │  STDIO Transport │              │  HTTP Transport         │  │
│  │  (zero-config)   │              │  (Streamable, OAuth 2.1)│  │
│  └────────┬─────────┘              └──────────┬──────────────┘  │
│           └────────────────┬───────────────────┘                 │
│                    ┌───────▼───────┐                             │
│                    │  MCP Server   │                             │
│                    │  (go-sdk)     │                             │
│                    └───────┬───────┘                             │
└────────────────────────────┼─────────────────────────────────────┘
                             │
┌────────────────────────────┼─────────────────────────────────────┐
│                    Tool Dispatch Layer                             │
│  ┌─────────┐ ┌────────┐ ┌┴───────┐ ┌────────┐ ┌─────────────┐  │
│  │ Search  │ │ Scrape │ │Combined│ │Academic│ │ Sequential  │  │
│  │ Tools   │ │ Tool   │ │  Tool  │ │& Patent│ │  Research   │  │
│  └────┬────┘ └───┬────┘ └───┬────┘ └───┬────┘ └──────┬──────┘  │
└───────┼──────────┼───────────┼──────────┼─────────────┼──────────┘
        │          │           │          │             │
┌───────┼──────────┼───────────┼──────────┼─────────────┼──────────┐
│       │     Service Layer    │          │             │           │
│  ┌────▼────┐ ┌───▼────┐ ┌───▼───┐ ┌───▼────┐ ┌─────▼─────┐   │
│  │ Search  │ │Scraper │ │Quality│ │Citation│ │  Session   │   │
│  │Provider │ │Pipeline│ │Scorer │ │Extract │ │  Manager   │   │
│  └────┬────┘ └───┬────┘ └───────┘ └────────┘ └────────────┘   │
│       │          │                                               │
│  ┌────▼─────┐ ┌─▼──────────────────────────────────┐           │
│  │ Router   │ │  Scraper Tiers (4-tier pipeline)     │           │
│  │(fallback)│ │  markdown > stealth > HTML > browser│           │
│  │  Brave   │ │  + YouTube (3-strategy) + documents │           │
│  │  Google  │ └─────────────────────────────────────┘           │
│  │  Serper  │                                                    │
│  │  SearXNG │                                                    │
│  │SearchAPI │                                                    │
│  └──────────┘                                                    │
└──────────────────────────────────────────────────────────────────┘
        │          │
┌───────┼──────────┼──────────────────────────────────────────────┐
│       │   Infrastructure Layer                                    │
│  ┌────▼────┐ ┌───▼────┐ ┌─────────┐ ┌────────┐ ┌───────────┐  │
│  │  Cache  │ │  SSRF  │ │  Rate   │ │Metrics │ │   Audit   │  │
│  │(hybrid) │ │Protect │ │ Limiter │ │(Prom.) │ │   Logger  │  │
│  └─────────┘ └────────┘ └─────────┘ └────────┘ └───────────┘  │
│  ┌──────────────────┐  ┌──────────────────────────────────────┐ │
│  │  Circuit Breaker  │  │  Content Pipeline (sanitize, dedup,  │ │
│  │                   │  │  truncate, quality score)             │ │
│  └───────────────────┘  └──────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────┘
```

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
| **Google PSE** | Yes | Yes | Yes | Default; free tier: 100 queries/day |
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

---

## Performance

Searches come back in under a second. Previously-seen results are cached so repeats are instant. Full article extraction works on 95%+ of the web — including sites that try to block bots. Heavy JavaScript sites get a real browser behind the scenes (automatic, no setup needed).

---

## Development

```bash
go build -o web-researcher-mcp ./cmd/web-researcher-mcp   # Build
go test -race ./...                                        # Test (with race detector)
golangci-lint run                                          # Lint
govulncheck ./...                                          # Security audit
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full development workflow, code style guide, and PR process.

---

## Troubleshooting

<details>
<summary><strong>Server starts but tools fail with "API key" errors</strong></summary>

The server starts even with missing credentials (to allow MCP handshake). Set your API keys in the `env` block of your MCP client config, not in your shell profile.

</details>

<details>
<summary><strong>Some pages come back empty</strong></summary>

For JavaScript-heavy sites, the tool uses a real browser (Chromium). It auto-downloads on first use (~200MB). If you already have Chrome installed, set `CHROME_PATH` to point to it, or use the Docker image which has everything included.

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
