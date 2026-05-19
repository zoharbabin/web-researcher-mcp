# Architecture

## Context

This is an MCP (Model Context Protocol) server that provides AI assistants with web search, content extraction, and multi-source research capabilities. It is designed for:

- **Reliability** — clean process lifecycle, no orphan processes, immediate EOF detection
- **Modularity** — one package per concern, interface-driven, testable in isolation
- **Security** — SSRF protection, content sanitization, session isolation, audit logging
- **Scalability** — horizontal scaling via Redis, bounded concurrency, backpressure
- **Extensibility** — pluggable search backends, custom lenses, new tools as simple additions

## Design Principles

1. **Explicit over implicit** — No magic. Dependencies injected, not imported globally.
2. **Fail loud, fail fast** — Return errors, don't swallow them. Validate at boundaries.
3. **Zero global state** — All state lives in structs passed via `context.Context` or constructor injection.
4. **Interface-driven** — Every external dependency (search API, cache, browser) is behind an interface for testing and swapping.
5. **Bounded concurrency** — Goroutines are cheap, but external APIs are not. Explicit semaphores everywhere.
6. **Defense in depth** — SSRF, rate limiting, content sanitization, session isolation at every layer.

## High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         MCP Protocol Layer                        │
│  ┌──────────────────┐              ┌─────────────────────────┐  │
│  │  STDIO Transport │              │  HTTP/SSE Transport     │  │
│  │  (zero-config)   │              │  (OAuth 2.1 + CORS)     │  │
│  └────────┬─────────┘              └──────────┬──────────────┘  │
│           │                                    │                  │
│           └────────────────┬───────────────────┘                 │
│                            │                                     │
│                    ┌───────▼───────┐                             │
│                    │  MCP Server   │                             │
│                    │  (go-sdk)     │                             │
│                    └───────┬───────┘                             │
└────────────────────────────┼─────────────────────────────────────┘
                             │
┌────────────────────────────┼─────────────────────────────────────┐
│                    Tool Dispatch Layer                             │
│                            │                                      │
│  ┌─────────┐ ┌────────┐ ┌┴───────┐ ┌────────┐ ┌─────────────┐ │
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
│  │Interface│ │(tiered)│ │       │ │        │ │            │   │
│  └────┬────┘ └───┬────┘ └───────┘ └────────┘ └────────────┘   │
│       │          │                                               │
│  ┌────▼────┐ ┌───▼─────────────────────────────┐               │
│  │Brave    │ │ Scraper Implementations          │               │
│  │Google   │ │ ┌──────────┐ ┌───────┐ ┌──────┐│               │
│  │Serper   │ │ │ Markdown │ │goquery│ │chrom-││               │
│  │SearXNG  │ │ │ Negotiat.│ │(HTML) │ │  dp  ││               │
│  └─────────┘ │ └──────────┘ └───────┘ └──────┘│               │
│              │ ┌──────────┐ ┌───────┐ ┌──────┐│               │
│              │ │   PDF    │ │ DOCX  │ │ PPTX ││               │
│              │ └──────────┘ └───────┘ └──────┘│               │
│              │ ┌──────────────────────────────┐│               │
│              │ │    YouTube Transcript        ││               │
│              │ └──────────────────────────────┘│               │
│              └──────────────────────────────────┘               │
└──────────────────────────────────────────────────────────────────┘
        │          │
┌───────┼──────────┼──────────────────────────────────────────────┐
│       │   Infrastructure Layer                                    │
│  ┌────▼────┐ ┌───▼────┐ ┌─────────┐ ┌────────┐ ┌───────────┐  │
│  │  Cache  │ │  SSRF  │ │  Rate   │ │Metrics │ │   Audit   │  │
│  │(ristret-│ │Protect │ │ Limiter │ │Collect.│ │   Logger  │  │
│  │to+disk) │ │(dialer)│ │(x/time) │ │(prom.) │ │  (slog)   │  │
│  └─────────┘ └────────┘ └─────────┘ └────────┘ └───────────┘  │
│  ┌─────────────────┐  ┌──────────────────────────────────────┐  │
│  │  Circuit Breaker │  │  Content Pipeline (sanitize, dedup,  │  │
│  │                  │  │  truncate, score)                    │  │
│  └──────────────────┘  └──────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
```

## Module Layout

```
web-researcher-mcp/
├── cmd/
│   └── web-researcher-mcp/
│       └── main.go                    # Entry point (wiring only)
├── internal/
│   ├── config/
│   │   ├── config.go                  # Strongly-typed config from env
│   │   └── config_test.go
│   ├── server/
│   │   ├── server.go                  # MCP server wiring + lifecycle
│   │   └── server_test.go
│   ├── tools/
│   │   ├── registry.go                # Tool registration
│   │   ├── search.go                  # web_search tool
│   │   ├── imagesearch.go             # image_search tool
│   │   ├── newssearch.go              # news_search tool
│   │   ├── scrape.go                  # scrape_page tool
│   │   ├── searchandscrape.go         # search_and_scrape tool
│   │   ├── academic.go                # academic_search tool
│   │   ├── patent.go                  # patent_search tool
│   │   ├── sequential.go              # sequential_search tool
│   │   └── tools_test.go
│   ├── search/
│   │   ├── provider.go                # SearchProvider interface
│   │   ├── google.go                  # Google PSE adapter
│   │   ├── brave.go                   # Brave Search adapter
│   │   ├── serper.go                  # Serper.dev adapter (opt-in)
│   │   ├── searxng.go                 # SearXNG adapter (self-hosted)
│   │   ├── lenses.go                  # Search lens logic
│   │   └── search_test.go
│   ├── scraper/
│   │   ├── pipeline.go                # Tiered scraping orchestrator
│   │   ├── markdown.go                # Tier 1: Accept: text/markdown negotiation
│   │   ├── stealth.go                 # Tier 2: Browser-like TLS + Chrome headers
│   │   ├── html.go                    # Tier 3: goquery-based extraction
│   │   ├── browser.go                 # Tier 4: go-rod headless + stealth plugin
│   │   ├── document.go                # Document type detection + routing
│   │   ├── youtube.go                 # YouTube transcript extraction
│   │   ├── ssrf.go                    # SSRF-safe HTTP client + dialer
│   │   └── scraper_test.go
│   ├── documents/
│   │   ├── parser.go                  # Unified document parser
│   │   ├── pdf.go                     # PDF text extraction
│   │   ├── docx.go                    # DOCX extraction
│   │   ├── pptx.go                    # PPTX extraction
│   │   └── documents_test.go
│   ├── cache/
│   │   ├── cache.go                   # Cache interface
│   │   ├── memory.go                  # In-memory LRU cache (sync.Map + TTL)
│   │   ├── disk.go                    # File-based disk persistence (AES-256-GCM)
│   │   ├── hybrid.go                  # L1 memory + L2 disk
│   │   └── cache_test.go
│   ├── auth/
│   │   ├── middleware.go              # OAuth 2.1 middleware (JWT/JWKS/revocation)
│   │   └── middleware_test.go
│   ├── audit/
│   │   ├── logger.go                  # Structured audit logging
│   │   └── audit_test.go
│   ├── session/
│   │   ├── manager.go                 # Session lifecycle + state
│   │   └── manager_test.go
│   ├── content/
│   │   ├── processor.go               # Content processing pipeline
│   │   ├── sanitize.go                # HTML/content sanitization
│   │   ├── dedup.go                   # Paragraph-level deduplication
│   │   ├── truncate.go                # Smart truncation at breakpoints
│   │   ├── quality.go                 # Quality scoring
│   │   ├── citation.go                # Citation extraction + formatting
│   │   └── content_test.go
│   ├── metrics/
│   │   ├── collector.go               # Per-tool metrics + Prometheus exporter
│   │   └── collector_test.go
│   ├── ratelimit/
│   │   ├── limiter.go                 # Per-user/tenant rate limiting
│   │   └── limiter_test.go
│   ├── circuit/
│   │   ├── breaker.go                 # Circuit breaker (timer-free)
│   │   └── breaker_test.go
│   └── resources/
│       ├── resources.go               # MCP Resources + Prompts
│       └── resources_test.go
├── lenses/
│   ├── programming.json               # Curated domain lists
│   ├── news.json
│   ├── tech.json
│   ├── legal.json
│   ├── medical.json
│   ├── finance.json
│   ├── science.json
│   └── government.json
├── docs/                               # Extended documentation
├── testdata/                           # Fixtures for tests
├── scripts/
│   ├── run-e2e.sh                     # Run E2E test suite
│   └── build-mcpb.sh                  # Builds .mcpb bundles (CI)
├── mcpb/
│   └── manifest.json                   # Claude Desktop bundle template
├── .mcp.json                           # Claude Code / Cursor config
├── .vscode/mcp.json                    # VS Code / GitHub Copilot config
├── server.json                         # Official MCP Registry manifest
├── smithery.yaml                       # Smithery.ai marketplace config
├── go.mod
├── go.sum
├── Makefile
├── Dockerfile
├── Dockerfile.release                  # Slim Alpine image for GoReleaser
├── .goreleaser.yml
├── CLAUDE.md
├── README.md
└── LICENSE
```

## Key Design Decisions

### 1. Process Lifecycle

The server uses Go's native I/O model:

```go
ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer cancel()

if err := server.Run(ctx, transport); err != nil {
    // Run() returns when:
    // - stdin returns io.EOF (parent process exited)
    // - SIGINT/SIGTERM received
    // - context cancelled
}
```

When the parent process dies, `os.Stdin.Read()` returns `io.EOF`. Writing to a broken stdout returns `EPIPE` and Go raises `SIGPIPE`. No polling, no watchdog, no worker threads. The process exits cleanly within milliseconds.

### 2. Pluggable Search Backend

```go
type SearchProvider interface {
    Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error)
    Images(ctx context.Context, params ImageSearchParams) ([]ImageResult, error)
    News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error)
    Name() string
}
```

Search lenses route to Google PSE (site-restricted, free forever). Unrestricted queries route to the configured provider (Brave, Serper, SearXNG). Providers are swappable at runtime via configuration.

### 3. Tiered Scraping Pipeline

```go
type Scraper interface {
    Scrape(ctx context.Context, url string, opts ScrapeOptions) (*ScrapeResult, error)
    CanHandle(url string, contentType string) bool
}

// Pipeline tries scrapers in order, falls back on failure
type Pipeline struct {
    scrapers []Scraper // markdown → html → browser → document
}
```

### 4. Dependency Injection

All services constructed explicitly in `main.go` and passed down:

```go
srv := server.New(cfg, server.Deps{
    Cache:    cacheStore,
    Search:   searchProvider,
    Scraper:  scraperPipeline,
    Sessions: sessionManager,
})
```

### 5. Context Propagation

Every request carries deadline, session ID, tenant ID, trace ID, and a pre-configured logger:

```go
type RequestContext struct {
    SessionID string
    TenantID  string
    TraceID   string
    Logger    *slog.Logger
}
```

### 6. Concurrency Model

- **Per-tool timeout**: Context with deadline on every tool call
- **Bounded parallelism**: Semaphore channel for concurrent scrapes (max 5)
- **Per-client backpressure**: Rate limiter per session, reject with 429
- **Graceful shutdown**: Context cancellation propagates, in-flight requests drain

## Technology Stack

| Concern | Library | Why |
|---------|---------|-----|
| MCP Protocol | `github.com/modelcontextprotocol/go-sdk` v1.6.0 | Official MCP SDK, full spec compliance |
| HTML Parsing | `github.com/PuerkitoBio/goquery` | jQuery-style CSS selectors |
| Headless Browser | `github.com/go-rod/rod` + `go-rod/stealth` | DevTools Protocol, auto-download Chromium, anti-detection |
| In-Memory Cache | Custom `sync.RWMutex` + map | Simple LRU with TTL, size-bounded |
| Disk Cache | File-based with AES-256-GCM | Custom implementation, no external dependency |
| JWT/JWKS | Custom RS256 implementation | Minimal, no external JWT library |
| Rate Limiting | `golang.org/x/time/rate` | Token bucket, stdlib-adjacent |
| HTML Sanitizer | `github.com/microcosm-cc/bluemonday` | Whitelist-based |
| Metrics | `github.com/prometheus/client_golang` | Standard Prometheus |
| UUID | `github.com/google/uuid` | Session ID generation |
| Logging | `log/slog` (stdlib) | Standard, extensible |

## Performance Characteristics

| Operation | Expected Latency | Concurrency Model |
|-----------|-----------------|-------------------|
| Search (cache hit) | < 1ms | Direct return |
| Search (API call) | 200-500ms | Circuit-breaker protected |
| Scrape (markdown) | 100-300ms | HTTP GET + parse |
| Scrape (HTML) | 500-2000ms | goquery parse |
| Scrape (stealth HTTP) | 300-800ms | Browser-like TLS + headers, no JS |
| Scrape (browser) | 2-10s | go-rod headless, bounded to MaxConcurrency |
| YouTube transcript | 1-5s | 3-strategy: captions → timedtext API → description |
| search_and_scrape | 2-15s | Parallel scrape (semaphore=5) |

## Binary Output

Single static binary, ~20MB. No runtime dependencies except optional Chrome for JS rendering.

```bash
# Build
go build -o web-researcher-mcp ./cmd/web-researcher-mcp

# Run (STDIO)
./web-researcher-mcp

# Run (HTTP)
PORT=3000 ./web-researcher-mcp
```
