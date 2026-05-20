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
│  │ Router  │ │ Scraper Implementations          │               │
│  │(fallbk) │ │ ┌──────────┐ ┌───────┐ ┌──────┐│               │
│  │ Brave   │ │ │ Markdown │ │goquery│ │chrom-││               │
│  │ Google  │ │ │ Negotiat.│ │(HTML) │ │  dp  ││               │
│  │ Serper  │ │ └──────────┘ └───────┘ └──────┘│               │
│  │ SearXNG │                                    │               │
│  │SearchAPI│                                    │               │
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
│   ├── session/                  # Per-tenant session management
│   ├── content/                  # Sanitize, dedup, truncate, quality
│   ├── metrics/                  # Prometheus metrics
│   ├── ratelimit/                # Three-tier rate limiting
│   ├── circuit/                  # Circuit breaker
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

Five providers implement this interface: Google PSE, Brave, Serper, SearXNG, and SearchAPI.io. The `Router` also implements `Provider`, enabling transparent multi-provider fallback — tools don't need to know whether they're calling a single provider or a routing layer.

When `SEARCH_ROUTING` is configured, the Router wraps all available providers with per-provider circuit breakers and priority-ordered fallback. Search lenses inject `site:` operators and route through the configured provider. Lenses with a dedicated `cx` field route directly to that Google PSE engine.

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
| In-Memory Cache | Custom `sync.RWMutex` + map | Simple LRU with TTL, size-bounded |
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
Per-tenant rate limit:        30 req/min     (RATE_LIMIT_PER_TENANT)
Scraping semaphore:           5 slots        (MAX_SCRAPE_CONCURRENCY)
Browser pool (go-rod):        3 slots        (subset of scraping slots)
```

Browser scrapes hold both a scraping slot and a browser slot simultaneously.

## Error Handling

Tool handlers return errors as MCP tool results with `IsError: true`:

| Error Type | MCP Response |
|------------|-------------|
| `context.DeadlineExceeded` | Tool error: timeout message |
| `ErrCircuitOpen` | Tool error: "service temporarily unavailable" |
| `ErrSSRFBlocked` | Tool error: "URL blocked by SSRF protection" |
| Invalid input | Tool error: descriptive validation message |
| Protocol-level errors | Handled by go-sdk (invalid JSON-RPC, unknown method) |

## Binary Output

Single static binary with no runtime dependencies except optional Chromium for JS rendering (auto-downloaded by go-rod on first headless scrape).

```bash
go build -o web-researcher-mcp ./cmd/web-researcher-mcp   # Build
./web-researcher-mcp                                       # Run (STDIO)
PORT=3000 ./web-researcher-mcp                             # Run (HTTP)
```
