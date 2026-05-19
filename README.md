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

- **Multiple specialized research tools** in a single server (see [Tools](#tools) below)
- **Pluggable search backends** (Google, Brave, Serper, SearXNG)
- **4-tier content extraction** -- markdown negotiation, stealth HTTP, HTML parsing, headless browser (go-rod + stealth)
- **Search lenses** for domain-focused research (programming, news, legal, medical, and more)
- **Single static binary** with optional Chromium for JS rendering (auto-downloaded on first use)
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

### Option 1: Install with Go (Recommended)

```bash
go install github.com/zoharbabin/web-researcher-mcp/cmd/web-researcher-mcp@latest
```

The binary is now in your `$GOPATH/bin`. Add to Claude Code:

```bash
claude mcp add --scope user --transport stdio web-researcher -- web-researcher-mcp
```

### Option 2: Download Binary

Download the latest release for your platform from [Releases](https://github.com/zoharbabin/web-researcher-mcp/releases). Archives are named `web-researcher-mcp_<version>_<os>_<arch>.tar.gz`.

```bash
# Example: macOS Apple Silicon (replace VERSION with the latest from Releases)
curl -L https://github.com/zoharbabin/web-researcher-mcp/releases/download/v${VERSION}/web-researcher-mcp_${VERSION}_darwin_arm64.tar.gz | tar xz
chmod +x web-researcher-mcp
```

### Option 3: Docker

```bash
docker run -e GOOGLE_CUSTOM_SEARCH_API_KEY=YOUR_KEY \
           -e GOOGLE_CUSTOM_SEARCH_ID=YOUR_CX \
           docker.io/zoharbabin/web-researcher-mcp:latest
```

Also available from GHCR: `ghcr.io/zoharbabin/web-researcher-mcp:latest`

### Option 4: Build from Source

```bash
git clone https://github.com/zoharbabin/web-researcher-mcp.git
cd web-researcher-mcp
go build -o web-researcher-mcp ./cmd/web-researcher-mcp
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

Done. Your AI assistant now has access to all research tools.

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

See [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md#environment-variables) for the complete reference of all environment variables (cache, rate limiting, scraping, observability, etc.).

</details>

---

## Architecture

```
web-researcher-mcp/
├── cmd/web-researcher-mcp/     # Entry point (wiring only)
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

The server supports four search backends. All providers support lenses via `site:` operator injection.

| Provider | Whole-Web | Images | News | Notes |
|----------|:---------:|:------:|:----:|-------|
| **Google PSE** | Yes | Yes | Yes | Default; free tier: 100 queries/day |
| **Brave Search** | Yes | Yes | Yes | Recommended for high-volume whole-web |
| **Serper.dev** | Yes | Yes | Yes | Google-identical results |
| **SearXNG** | Yes | Yes | Yes | Self-hosted, privacy-first, air-gapped deployments |

### Routing Logic

```
Request arrives
  |-- lens with dedicated CX? --> That specific Google PSE engine
  |-- lens (no CX)?           --> Configured provider + site: operators
  |-- site: param set?        --> Configured provider + site: operator
  `-- unrestricted?           --> Configured SEARCH_PROVIDER
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

Search lenses are curated domain lists that focus search results on high-quality sources for specific topics. They inject `site:` operators into queries and route through your configured search provider.

### Built-in Lenses

| Lens | Focus |
|------|-------|
| `programming` | Code docs, tutorials, Q&A |
| `news` | Current events, journalism |
| `tech` | Technology industry |
| `legal` | Law, cases, statutes |
| `medical` | Health, medicine |
| `finance` | Markets, filings |
| `science` | Research, papers |
| `government` | Policy, regulations |

Each lens is a JSON file in `lenses/` containing the curated domain list. See [Creating Custom Lenses](#search-lenses) below for the format.

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
- **domains** -- URL patterns for the lens (up to ~10 injected per query via `site:` operators)
- **cx** -- Optional dedicated Google PSE engine ID. If set, bypasses site injection and routes directly to that PSE engine (supports up to 5,000 domains)

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

Search results are cached in-memory for sub-millisecond hits. The scraping pipeline tries the fastest tier first and falls back progressively — most pages resolve in under a second via stealth HTTP, with the headless browser reserved for JS-heavy sites. See [ARCHITECTURE.md](ARCHITECTURE.md#performance-characteristics) for detailed latency breakdowns.

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
<summary><strong>scrape_page returns empty content for JavaScript-heavy sites</strong></summary>

The browser tier (go-rod) requires Chromium. On first use it auto-downloads ~200MB. Set `CHROME_PATH` to use an existing Chrome installation, or use the Docker image which includes headless Chrome.

</details>

<details>
<summary><strong>Cache serving stale results after upgrade</strong></summary>

The disk cache auto-invalidates on version change. If you're running from source without `-ldflags`, the version is always "dev" — delete the `./cache` directory manually or set `CACHE_DIR` to a versioned path.

</details>

<details>
<summary><strong>Rate limited by Google (HTTP 429)</strong></summary>

Google PSE free tier allows 100 queries/day. Either upgrade to paid ($5/1K queries), or switch to Brave Search (`SEARCH_PROVIDER=brave`) for higher-volume queries.

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
| [docs/SECURITY.md](docs/SECURITY.md) | Threat model, SSRF, auth, compliance (SOC2/GDPR/FedRAMP) |
| [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) | Build, Docker, Kubernetes, client configs, scaling |

---

## License

[MIT](LICENSE)

---

<p align="center">
  Built with <a href="https://go.dev">Go</a> and the <a href="https://modelcontextprotocol.io/">Model Context Protocol</a>
  <br/><br/>
  If this project helps your workflow, consider giving it a star.
</p>
