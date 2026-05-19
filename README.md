<h1 align="center">web-researcher-mcp</h1>
<p align="center">
  A production-grade MCP server that gives AI assistants the power to search the web, extract content, and conduct multi-source research.
</p>

<p align="center">
  <a href="https://github.com/zoharbabin/web-researcher-mcp/actions/workflows/ci.yml"><img src="https://github.com/zoharbabin/web-researcher-mcp/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://goreportcard.com/report/github.com/zoharbabin/web-researcher-mcp"><img src="https://goreportcard.com/badge/github.com/zoharbabin/web-researcher-mcp" alt="Go Report Card"></a>
  <a href="https://pkg.go.dev/github.com/zoharbabin/web-researcher-mcp"><img src="https://pkg.go.dev/badge/github.com/zoharbabin/web-researcher-mcp.svg" alt="Go Reference"></a>
  <a href="https://opensource.org/licenses/MIT"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
  <a href="https://github.com/zoharbabin/web-researcher-mcp/releases"><img src="https://img.shields.io/github/v/release/zoharbabin/web-researcher-mcp" alt="Release"></a>
  <a href="https://hub.docker.com/r/zoharbabin/web-researcher-mcp"><img src="https://img.shields.io/docker/pulls/zoharbabin/web-researcher-mcp?cacheSeconds=3600" alt="Docker"></a>
</p>

---

## Why Web Researcher MCP?

AI assistants are only as good as the information they can access. **web-researcher-mcp** bridges the gap between LLMs and the live internet through the [Model Context Protocol](https://modelcontextprotocol.io/) standard:

- **8 specialized research tools** in a single server
- **4 pluggable search backends** (Google, Brave, Serper, SearXNG)
- **4-tier content extraction** -- markdown negotiation, stealth HTTP, HTML parsing, headless browser (go-rod + stealth)
- **Search lenses** for domain-focused research (programming, news, legal, medical, and more)
- **Single static binary** (~20MB) with zero runtime dependencies
- **Enterprise-ready** with OAuth 2.1, multi-tenancy, rate limiting, and audit logging

Works with Claude Code, Claude Desktop, Cursor, and any MCP-compatible client.

---

## Tools

| Tool | Description |
|------|-------------|
| `web_search` | General web search with optional search lenses for domain-focused results |
| `scrape_page` | Extract content from any URL -- web pages, PDFs, DOCX, PPTX, YouTube transcripts (3-strategy fallback) |
| `search_and_scrape` | Combined search + extraction pipeline with quality scoring and deduplication |
| `image_search` | Search for images with size, type, color, and file format filters |
| `news_search` | Search news sources with freshness controls and source filtering |
| `academic_search` | Search academic papers via Scholar, arXiv, and PubMed |
| `patent_search` | Search patent databases with CPC classification, strict office filtering (US/EP/WO/JP/CN/KR) |
| `sequential_search` | Multi-step research tracking with session state for iterative investigation |

---

## Quick Start

### One-Line Install (Claude Code)

```bash
claude mcp add web-researcher -- go run github.com/zoharbabin/web-researcher-mcp/cmd/web-researcher-mcp@latest
```

Then set your API keys in `~/.claude/settings.json` under the server's `env` block (see [Connect to Your AI Assistant](#connect-to-your-ai-assistant) below).

### Option 1: Download Binary

Download the latest release for your platform from [Releases](https://github.com/zoharbabin/web-researcher-mcp/releases):

```bash
# macOS (Apple Silicon)
curl -L https://github.com/zoharbabin/web-researcher-mcp/releases/latest/download/web-researcher-mcp_1.0.0_darwin_arm64.tar.gz | tar xz
chmod +x web-researcher-mcp

# macOS (Intel)
curl -L https://github.com/zoharbabin/web-researcher-mcp/releases/latest/download/web-researcher-mcp_1.0.0_darwin_amd64.tar.gz | tar xz
chmod +x web-researcher-mcp

# Linux (x86_64)
curl -L https://github.com/zoharbabin/web-researcher-mcp/releases/latest/download/web-researcher-mcp_1.0.0_linux_amd64.tar.gz | tar xz
chmod +x web-researcher-mcp
```

### Option 2: Docker

```bash
docker run -e GOOGLE_CUSTOM_SEARCH_API_KEY=YOUR_KEY \
           -e GOOGLE_CUSTOM_SEARCH_ID=YOUR_CX \
           docker.io/zoharbabin/web-researcher-mcp:latest
```

Also available from GHCR: `ghcr.io/zoharbabin/web-researcher-mcp:latest`

### Option 3: Build from Source

```bash
git clone https://github.com/zoharbabin/web-researcher-mcp.git
cd web-researcher-mcp
go build -o web-researcher-mcp ./cmd/web-researcher-mcp
```

Or install directly:

```bash
go install github.com/zoharbabin/web-researcher-mcp/cmd/web-researcher-mcp@latest
```

### Connect to Your AI Assistant

Add this to your MCP client configuration (example for Claude Code `~/.claude/settings.json`):

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

Done. Your AI assistant now has access to all 8 research tools.

---

## Configuration

### Required

| Variable | Description | How to Get |
|----------|-------------|-----------|
| `GOOGLE_CUSTOM_SEARCH_API_KEY` | Google API key | [Google Cloud Console](https://developers.google.com/custom-search/v1/introduction) |
| `GOOGLE_CUSTOM_SEARCH_ID` | Programmable Search Engine ID | [PSE Console](https://programmablesearchengine.google.com/) |

### Search Provider

| Variable | Description | Default |
|----------|-------------|---------|
| `SEARCH_PROVIDER` | Backend: `google`, `brave`, `serper`, or `searxng` | `google` |
| `BRAVE_API_KEY` | Brave Search API key | |
| `SERPER_API_KEY` | Serper.dev API key | |
| `SEARXNG_URL` | SearXNG instance URL | |

### HTTP Transport (Optional)

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | Enable HTTP/SSE mode | STDIO only |
| `OAUTH_ISSUER_URL` | JWT issuer URL for token validation | |
| `OAUTH_AUDIENCE` | Expected JWT audience claim | |

<details>
<summary><strong>All Environment Variables</strong></summary>

| Variable | Description | Default |
|----------|-------------|---------|
| `GOOGLE_CUSTOM_SEARCH_API_KEY` | Google API key | (required) |
| `GOOGLE_CUSTOM_SEARCH_ID` | Search engine ID | (required) |
| `SEARCH_PROVIDER` | Search backend | `google` |
| `BRAVE_API_KEY` | Brave Search API key | |
| `SERPER_API_KEY` | Serper.dev API key | |
| `SEARXNG_URL` | SearXNG instance URL | |
| `PORT` | HTTP port (enables HTTP/SSE mode) | STDIO |
| `OAUTH_ISSUER_URL` | JWT issuer for auth | |
| `OAUTH_AUDIENCE` | Expected JWT audience | |
| `REDIS_URL` | Redis URL for shared cache/sessions | in-memory |
| `CACHE_DIR` | Disk cache directory | `./cache` |
| `CACHE_MAX_MEMORY_MB` | Max memory cache size in MB | `64` |
| `CACHE_ENCRYPTION_KEY` | AES-256-GCM key for disk cache (64 hex chars) | |
| `RATE_LIMIT_PER_TENANT` | Requests per minute per tenant | `30` |
| `RATE_LIMIT_GLOBAL` | Total requests per second | `1000` |
| `DAILY_QUOTA_PER_TENANT` | Max API calls per tenant per day | `1000` |
| `LOG_LEVEL` | Logging level: debug, info, warn, error | `info` |
| `LOG_FORMAT` | Log format: json, text | `json` |
| `METRICS_ENABLED` | Enable Prometheus metrics | `true` |
| `MAX_SCRAPE_CONCURRENCY` | Concurrent scrape limit | `5` |
| `CHROME_PATH` | Custom Chrome/Chromium binary path | auto-detect |
| `ALLOW_PRIVATE_IPS` | Disable SSRF protection (dev only) | `false` |

</details>

---

## Architecture

```
web-researcher-mcp/
├── cmd/web-researcher-mcp/     # Entry point (wiring only, ~50 lines)
├── internal/
│   ├── config/                 # Env-based strongly-typed configuration
│   ├── server/                 # MCP server lifecycle + signal handling
│   ├── tools/                  # Tool handlers (one file per tool)
│   ├── search/                 # Pluggable search providers + lens routing
│   ├── scraper/                # 4-tier scraping pipeline (markdown → stealth → HTML → browser)
│   ├── documents/              # PDF, DOCX, PPTX parsing
│   ├── cache/                  # Hybrid cache (memory + disk + optional Redis)
│   ├── auth/                   # OAuth 2.1 middleware + JWKS
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
│  │  STDIO Transport │              │  HTTP/SSE Transport     │  │
│  │  (zero-config)   │              │  (OAuth 2.1 + CORS)     │  │
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
│  │ Brave    │ │  Scraper Tiers (4-tier pipeline)     │           │
│  │ Google   │ │  markdown > stealth > HTML > browser│           │
│  │ Serper   │ │  + YouTube (3-strategy) + documents │           │
│  │ SearXNG  │ └─────────────────────────────────────┘           │
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

### Design Principles

1. **Zero global state** -- all dependencies injected via constructors
2. **Interface-driven** -- every external dependency behind an interface for testing and swapping
3. **Bounded concurrency** -- explicit semaphores for external API calls
4. **Defense in depth** -- SSRF protection, rate limiting, content sanitization at every layer
5. **Fail loud** -- errors returned, never swallowed; validation at boundaries

---

## Search Providers

The server supports four search backends. Google PSE is always used for lens-restricted and site-restricted queries (free, works indefinitely). The configured provider handles unrestricted whole-web searches.

| Provider | Cost per 1K Queries | Whole-Web | Images | News | Notes |
|----------|-------------------|:---------:|:------:|:----:|-------|
| **Google PSE** | Free (100/day) to $5 | Until 2027 | Yes | Yes | Default; always used for lenses |
| **Brave Search** | $5 (free tier available) | Yes | Yes | Yes | Recommended for whole-web |
| **Serper.dev** | $0.30-$1 | Yes | Yes | Yes | Google-identical results |
| **SearXNG** | Free (self-hosted) | Yes | Yes | Yes | Privacy-first, air-gapped deployments |

### Routing Logic

```
Request arrives
  |-- lens specified?     --> Google PSE (site-restricted, free forever)
  |-- site: param set?    --> Google PSE (site-restricted)
  `-- unrestricted?       --> Configured SEARCH_PROVIDER
```

<details>
<summary><strong>Provider Setup Examples</strong></summary>

**Brave Search (recommended for whole-web):**
```bash
export SEARCH_PROVIDER=brave
export BRAVE_API_KEY=BSAxxxxxxxxxx
export GOOGLE_CUSTOM_SEARCH_API_KEY=AIza...  # still needed for lenses
export GOOGLE_CUSTOM_SEARCH_ID=017...
```

**SearXNG (self-hosted, privacy-first):**
```bash
export SEARCH_PROVIDER=searxng
export SEARXNG_URL=http://localhost:8080
export GOOGLE_CUSTOM_SEARCH_API_KEY=AIza...
export GOOGLE_CUSTOM_SEARCH_ID=017...
```

**Google PSE only (simplest setup):**
```bash
export GOOGLE_CUSTOM_SEARCH_API_KEY=AIza...
export GOOGLE_CUSTOM_SEARCH_ID=017...
# SEARCH_PROVIDER defaults to "google"
```

</details>

---

## Search Lenses

Search lenses are curated domain lists that focus search results on high-quality sources for specific topics. They route through Google PSE in site-restricted mode -- free and works indefinitely.

### Built-in Lenses

| Lens | Focus | Example Domains |
|------|-------|-----------------|
| `programming` | Code docs, tutorials, Q&A | stackoverflow.com, github.com, developer.mozilla.org |
| `news` | Current events, journalism | reuters.com, apnews.com, bbc.com, nytimes.com |
| `tech` | Technology industry | arstechnica.com, techcrunch.com, theverge.com |
| `legal` | Law, cases, statutes | law.cornell.edu, courtlistener.com, justia.com |
| `medical` | Health, medicine | nih.gov, mayoclinic.org, who.int, pubmed.ncbi.nlm.nih.gov |
| `finance` | Markets, filings | sec.gov, bloomberg.com, investopedia.com |
| `science` | Research, papers | nature.com, science.org, nasa.gov |
| `government` | Policy, regulations | *.gov, europa.eu, gov.uk, un.org |

### Usage Example

```json
{
  "tool": "web_search",
  "arguments": {
    "query": "golang context best practices",
    "lens": "programming"
  }
}
```

This searches only stackoverflow.com, github.com, go.dev, developer.mozilla.org, and other curated programming sites.

<details>
<summary><strong>Creating Custom Lenses</strong></summary>

Add a JSON file to the `lenses/` directory:

```json
{
  "name": "my-custom-lens",
  "description": "Description of what this lens covers",
  "domains": [
    "example.com",
    "docs.example.org",
    "*.trusted-source.io"
  ],
  "cx": ""
}
```

Fields:
- **domains** -- Up to 5,000 URL patterns per lens (Google PSE limit)
- **cx** -- Optional dedicated PSE engine ID. If empty, `site:` operators are injected at query time (limited to ~10 domains per query)

</details>

---

## Security

<details>
<summary><strong>SSRF Protection</strong></summary>

The server implements a custom `DialContext` that validates all resolved IPs before connecting:

- Blocks all private/reserved IP ranges (127.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, fc00::/7)
- Blocks cloud metadata endpoints (169.254.169.254)
- Validates against DNS rebinding by connecting only to the first resolved IP
- Re-validates redirect targets at each hop

</details>

<details>
<summary><strong>Authentication and Authorization</strong></summary>

In HTTP mode, the server supports OAuth 2.1 with:

- JWKS-based token validation with automatic key rotation
- Per-tenant session isolation
- Audience and issuer validation
- Configurable claim extraction for multi-tenancy

</details>

<details>
<summary><strong>Rate Limiting</strong></summary>

Three-tier rate limiting protects both the server and upstream APIs:

1. **Per-client** -- token bucket per authenticated session
2. **Per-provider** -- prevents exceeding upstream API quotas
3. **Global** -- server-wide backpressure valve

</details>

<details>
<summary><strong>Content Safety</strong></summary>

- HTML sanitization via whitelist-based policy (bluemonday)
- Paragraph-level deduplication across scraped results
- Smart truncation at natural content breakpoints
- Quality scoring to filter low-value results before returning to the LLM

</details>

For the full threat model and security architecture, see [docs/SECURITY.md](docs/SECURITY.md).

---

## MCP Client Integration

### Claude Code

Add to `~/.claude/settings.json`:

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

### HTTP/SSE Mode (Multi-Client, Teams)

For shared deployments serving multiple clients or web applications:

```bash
PORT=3000 \
OAUTH_ISSUER_URL=https://auth.example.com \
OAUTH_AUDIENCE=https://api.example.com \
./web-researcher-mcp
```

Connect any MCP client to `http://localhost:3000/sse`.

<details>
<summary><strong>Docker Compose Example</strong></summary>

```yaml
version: "3.8"
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
      REDIS_URL: redis://redis:6379
    depends_on:
      - redis

  redis:
    image: redis:7-alpine
    volumes:
      - redis-data:/data

volumes:
  redis-data:
```

</details>

---

## Performance

| Operation | Expected Latency | Notes |
|-----------|-----------------|-------|
| Search (cache hit) | < 1ms | Direct return from in-memory cache |
| Search (API call) | 200-500ms | Circuit-breaker protected |
| Scrape (markdown) | 100-300ms | Tier 1: content negotiation |
| Scrape (stealth HTTP) | 300-800ms | Tier 2: browser-like TLS + headers |
| Scrape (HTML) | 500-2000ms | Tier 3: goquery-based extraction |
| Scrape (browser) | 2-10s | Tier 4: go-rod headless + stealth plugin |
| YouTube transcript | 1-5s | 3-strategy: captions → timedtext API → description |
| search_and_scrape | 2-15s | Parallel scrape with semaphore (max 5) |

---

## Development

```bash
# Build
go build -o web-researcher-mcp ./cmd/web-researcher-mcp

# Run all tests
go test ./...

# Tests with race detector
go test -race ./...

# E2E tests
go test -v ./tests/e2e/...

# Benchmarks
go test -bench=. ./tests/benchmark/

# Lint
golangci-lint run

# Security audit
govulncheck ./...

# Production build (static, stripped)
CGO_ENABLED=0 go build -ldflags="-s -w" -o web-researcher-mcp ./cmd/web-researcher-mcp
```

---

## Contributing

Contributions are welcome. Please see [CONTRIBUTING.md](CONTRIBUTING.md) for code style guidelines, development workflow, and how to submit pull requests.

---

## Documentation

| Document | Description |
|----------|-------------|
| [ARCHITECTURE.md](ARCHITECTURE.md) | Full architecture, design decisions, technology stack |
| [docs/TOOLS.md](docs/TOOLS.md) | Detailed tool specifications and parameter schemas |
| [docs/SECURITY.md](docs/SECURITY.md) | Threat model, SSRF, authentication, content safety |
| [docs/SEARCH_PROVIDERS.md](docs/SEARCH_PROVIDERS.md) | Provider system, lenses, routing, migration plan |
| [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) | Build, Docker, Kubernetes, scaling |
| [docs/TESTING.md](docs/TESTING.md) | Test strategy and patterns |
| [docs/COMPLIANCE.md](docs/COMPLIANCE.md) | SOC2, GDPR, FedRAMP compliance |
| [docs/GO_MODULE.md](docs/GO_MODULE.md) | Every dependency with rationale |

---

## License

[MIT](LICENSE)

---

<p align="center">
  Built with <a href="https://go.dev">Go</a> and the <a href="https://modelcontextprotocol.io/">Model Context Protocol</a>
  <br/><br/>
  If this project helps your workflow, consider giving it a star.
</p>
