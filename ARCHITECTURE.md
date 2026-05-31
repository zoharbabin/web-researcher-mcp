# Architecture

## Context

This is the architecture reference for web-researcher-mcp — the tool that gives AI assistants reliable, cited web research capabilities. It communicates with AI apps via the Model Context Protocol (MCP). The system is designed for:

- **Reliability** — clean process lifecycle, no orphan processes, immediate EOF detection
- **Modularity** — one package per concern, interface-driven, testable in isolation
- **Security** — SSRF protection, content sanitization, session isolation, audit logging
- **Scalability** — bounded concurrency, backpressure, stateless HTTP transport for multi-instance
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
│  │  STDIO Transport │              │  HTTP Transport         │  │
│  │  (zero-config)   │              │  (Streamable, OAuth 2.1)│  │
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
│  │ Router  │ │ Scraper Implementations          │               │
│  │(fallbk) │ │ ┌──────────┐ ┌───────┐ ┌──────┐│               │
│  │ wraps   │ │ │ Markdown │ │goquery│ │go-rod││               │
│  │  every  │ │ │ Negotiat.│ │(HTML) │ │(CDP) ││               │
│  │provider │ │ └──────────┘ └───────┘ └──────┘│               │
│  │  in     │ │                                  │               │
│  │Supported│ │                                  │               │
│  │Providers│ │                                  │               │
│  └─────────┘ │                                  │               │
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
│  │(memory+ │ │Protect │ │ Limiter │ │Collect.│ │   Logger  │  │
│  │   disk) │ │(dialer)│ │(x/time) │ │(prom.) │ │  (slog)   │  │
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
├── cmd/web-researcher-mcp/       # Entry point (wiring only)
├── internal/
│   ├── config/                   # Strongly-typed config from env
│   ├── server/                   # MCP server lifecycle (STDIO + HTTP)
│   ├── tools/                    # Tool handlers (one file per tool)
│   ├── search/                   # Pluggable providers + router + lens routing
│   ├── scraper/                  # 4-tier pipeline + SSRF protection
│   ├── documents/                # PDF, DOCX, PPTX parsing
│   ├── cache/                    # Hybrid cache (memory + disk)
│   ├── auth/                     # OAuth 2.1 middleware (JWT/JWKS)
│   ├── audit/                    # Structured audit logging
│   ├── session/                  # Per-tenant session persistence (memory index + encrypted disk)
│   ├── content/                  # Sanitize, dedup, truncate, quality
│   ├── metrics/                  # Prometheus metrics
│   ├── ratelimit/                # Three-tier rate limiting
│   ├── circuit/                  # Circuit breaker
│   ├── persist/                  # TTL key/value store (memory or AES-256-GCM disk) backing token revocation + rate quotas
│   └── resources/                # MCP Resources + Prompts
├── lenses/                       # Search lens JSON files
├── tests/                        # E2E, integration tests + benchmarks
├── scripts/                      # CI/CD helper scripts
└── docs/                         # Extended documentation
```

Run `find . -name '*.go' | head -50` or `tree internal/` for the full file listing.

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
type Provider interface {
    Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error)
    Images(ctx context.Context, params ImageSearchParams) ([]ImageResult, error)
    News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error)
    Name() string
}
```

Several providers implement this interface — Google PSE, Brave, Serper, SearXNG, SearchAPI.io, and DuckDuckGo (the zero-config, no-key fallback). The canonical list is `search.SupportedProviders` in `internal/search/provider.go`. The `Router` also implements `Provider`, enabling transparent multi-provider fallback — tools don't need to know whether they're calling a single provider or a routing layer.

When `SEARCH_ROUTING` is configured, the Router wraps all available providers with per-provider circuit breakers and priority-ordered fallback. Search lenses inject `site:` operators and route through the configured provider. Lenses with a dedicated `cx` field route directly to that Google PSE engine.

#### Domain-Specific Providers (Patents)

In addition to the general `Provider` interface, the system supports domain-specific providers via a `PatentProvider` interface (see `internal/search/domain.go`):

```go
type PatentProvider interface {
    PatentSearcher // Patents(ctx, params) ([]PatentResult, error)
    Name() string
    Metadata() ProviderMeta
}
```

Each patent provider carries metadata declaring its regional coverage and capabilities (`ProviderMeta`). The patent tool filters providers by region before calling them — e.g., if `patent_office=EP`, providers covering only US are skipped. Four patent providers are available: SearchAPI (via its native patent engine), EPO OPS (worldwide, OAuth2), The Lens (worldwide, token-based), and USPTO (US-only). Each gets an independent circuit breaker.

### 3. Tiered Scraping Pipeline

```go
type Pipeline struct {
    client    *http.Client
    semaphore chan struct{}
    config    PipelineConfig
}

func (p *Pipeline) Scrape(ctx context.Context, url string, maxLength int) (*ScrapeResult, error)
```

The pipeline routes specialized content (YouTube, PDF/DOCX/PPTX) via early-return detection, then falls back through tiers in order: markdown → stealth → HTML → browser (go-rod). Each tier is a private method with the same signature; the pipeline tries each in sequence and promotes the first result that meets a quality threshold.

`Pipeline.ScrapeRaw()` is a separate, non-tiered path used by `scrape_page`'s `mode: raw`: it performs a single SSRF-checked fetch and returns the response body verbatim — no sanitization, no quality scoring, no tier fallback. Raw output is untrusted (it may contain injection payloads) and is cached under a distinct key so it never collides with the cleaned `full`/`preview` results.

### 4. Dependency Injection

All services are constructed explicitly in `main.go` and passed down via the `tools.Dependencies` struct. Tool handlers receive deps via closure capture at registration time — see `internal/tools/registry.go` for the canonical pattern.

### 5. Context Propagation

Every request carries a `context.Context` with deadline. Session and tenant IDs flow through the session manager for isolation. Structured logging via `slog` attaches relevant fields at each layer.

### 6. Concurrency Model

- **Per-tool timeout**: Context with deadline on every tool call
- **Bounded parallelism**: Semaphore channel for concurrent scrapes (max 5)
- **Per-client backpressure**: Rate limiter per session, reject with 429
- **Graceful shutdown**: Context cancellation propagates, in-flight requests drain

## Technology Stack

| Concern | Library | Why |
|---------|---------|-----|
| MCP Protocol | `github.com/modelcontextprotocol/go-sdk` | Official MCP SDK, full spec compliance |
| HTML Parsing | `github.com/PuerkitoBio/goquery` | jQuery-style CSS selectors |
| Headless Browser | `github.com/go-rod/rod` + `go-rod/stealth` | DevTools Protocol, auto-download Chromium, anti-detection |
| In-Memory Cache | Custom `sync.RWMutex` + map | Expiry-ordered eviction with TTL, size-bounded |
| Disk Cache | File-based with AES-256-GCM | Custom implementation, no external dependency |
| JWT/JWKS | Custom RS256 implementation | Minimal, no external JWT library |
| Rate Limiting | `golang.org/x/time/rate` | Token bucket, stdlib-adjacent |
| HTML Sanitizer | `github.com/microcosm-cc/bluemonday` | Whitelist-based, used by Gitea/Hugo |
| Metrics | `github.com/prometheus/client_golang` | Standard Prometheus |
| UUID | `github.com/google/uuid` | Session ID generation |
| Logging | `log/slog` (stdlib) | Standard, extensible |

For exact versions, see `go.mod`. All dependencies use MIT, Apache 2.0, or BSD licenses.

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

## Concurrency Limits

Default values (all configurable via environment variables — see `docs/DEPLOYMENT.md`):

```
Global request throughput:    1000 req/s     (RATE_LIMIT_GLOBAL)
Per-tenant rate limit:        120 req/min    (RATE_LIMIT_PER_TENANT)  [HTTP mode only]
Daily quota per tenant:       5000 req/day   (DAILY_QUOTA_PER_TENANT) [HTTP mode only]
Scraping semaphore:           5 slots        (MAX_SCRAPE_CONCURRENCY)
Browser pool (go-rod):        serialized     (mutex-protected shared instance)
```

Rate limiting applies only in HTTP mode. STDIO mode (the default for Claude Code, Cursor, and Claude Desktop) has no internal rate limiting — only upstream API quotas apply.

Browser scrapes hold a scraping semaphore slot and then acquire the browser pool mutex (serializing browser access to a single shared instance).

## Error Handling

Three-layer architecture: typed scraper errors (`ScrapeError{Kind}` in `internal/scraper/errors.go`) → structured tool-level responses (`structuredError()`, `upstreamErrorResponse()` in `internal/tools/errors.go`) → MCP protocol (`IsError: true` with dual-format text: natural language + JSON metadata).

Every error response includes machine-readable JSON: `{"error":{"kind":"...","retryable":...,"suggestedAction":"..."}}`. This lets LLM clients branch programmatically on error type. Error kinds: `rate_limited`, `auth_required`, `blocked`, `network`, `content_empty`, `browser_unavailable`, `config`, `upstream_unavailable`.

Full specification: see `docs/ERROR_HANDLING.md`.

## Binary Output

Single static binary with no runtime dependencies except optional Chromium for JS rendering (auto-downloaded by go-rod on first headless scrape). The published Docker image bundles Chromium with `CHROME_PATH` preset, so JavaScript rendering works out of the box with no download.

```bash
go build -o web-researcher-mcp ./cmd/web-researcher-mcp   # Build
./web-researcher-mcp                                       # Run (STDIO)
PORT=3000 ./web-researcher-mcp                             # Run (HTTP)
```
