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
│   ├── cache/                    # Hybrid cache (memory L1 + optional Redis L2 + disk L3)
│   ├── auth/                     # OAuth 2.1 middleware (JWT/JWKS)
│   ├── audit/                    # Structured audit logging (PodID for cross-pod correlation)
│   ├── session/                  # Per-tenant session persistence — Manager interface (memory+disk or Redis)
│   ├── content/                  # Sanitize, dedup, truncate, quality, typed source classification, claim evidence, recommendations + auto-formatted components
│   ├── metrics/                  # Prometheus metrics + per-tenant aggregate analytics
│   ├── ratelimit/                # Three-tier rate limiting + optional atomic cross-pod daily quota
│   ├── circuit/                  # Circuit breaker
│   ├── persist/                  # TTL key/value store (memory or AES-256-GCM disk) backing token revocation + rate quotas
│   ├── redisbackend/             # Sole go-redis importer: Redis impls of cache/persist/session (opt-in, HTTP-only, encrypted)
│   ├── consent/                  # Consent record-verify-honor for regulated features (Checker + Noop)
│   ├── datasubject/              # GDPR access/erasure registry — (tenantID,userID) Exporter/Eraser fan-out
│   ├── useranalytics/            # Opt-in consent-gated per-user analytics (Recorder + Noop)
│   ├── memory/                   # Opt-in consent-gated long-term cross-session memory (Store + Noop)
│   ├── workspace/                # Opt-in shared workspaces — server-enforced data-plane + isolation, host-owned membership
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

Several providers implement this interface — Google PSE, Brave, Serper, SearXNG, SearchAPI.io, Tavily, Exa, and DuckDuckGo (the zero-config, no-key fallback). The canonical list is `search.SupportedProviders` in `internal/search/provider.go`. The `Router` also implements `Provider`, enabling transparent multi-provider fallback — tools don't need to know whether they're calling a single provider or a routing layer.

When `SEARCH_ROUTING` is configured, the Router wraps all available providers with per-provider circuit breakers and priority-ordered fallback. Search lenses inject `site:` operators and route through the configured provider. Lenses with a dedicated `cx` field route directly to that Google PSE engine.

#### Capability Interfaces (Patents, Academic, Synthesis)

Beyond the general `Provider`, the system layers **opt-in capability interfaces** so a provider implements only what it supports. Each capability follows the same shape — a `…Searcher` (the method) plus a `…Provider` (Searcher + `Name()` + `Metadata()`) — with a parallel `Supported…Providers` list, `New…ProviderByName` factory, and `Available…Providers` constructor (all in `internal/search/`):

- **`PatentProvider`** (`Patents`) — `internal/search/domain.go`. Carries `ProviderMeta` for regional filtering (e.g. `patent_office=EP` skips US-only providers): SearchAPI, EPO OPS, The Lens, USPTO.
- **`AcademicProvider`** (`Scholarly`) — `internal/search/domain.go`. OpenAlex, CrossRef, Semantic Scholar, and Exa (via its research-paper category).
- **`CitationSearcher`** (`Citations` / `References`) — `internal/search/domain.go`. Forward (cited-by) and backward (references) citation edges behind the `citation_graph` tool. Implemented by Semantic Scholar (rich — citation intent + influence) and OpenAlex (counts-only); the tool auto-selects Semantic Scholar first.
- **`FilingSearcher` / `CaseSearcher` / `EconSearcher`** — `internal/search/structured_domains.go`. The structured-research domains behind `filing_search` (SEC EDGAR), `legal_search` (CourtListener), and `econ_search` (FRED). Each follows the same `…Searcher` + `…Provider` + `Supported…Providers` + `New…ByName` + `Available…Providers` shape as the patent/academic capabilities, resolved from the `Dependencies` maps in the tool layer.
- **`AnswerSearcher` / `StructuredSearcher`** — `internal/search/synthesis.go`. The provider-independent capabilities behind the `answer` and `structured_search` tools (grounded Q&A and per-result structured extraction). Currently Exa; a new provider (e.g. Perplexity) is added with one factory case + one list entry and the tools pick it up with no tool-layer change.

Separate from the capability interfaces, **`OAResolver`** (`internal/search/unpaywall.go`, implemented by Unpaywall) is an *enrichment* layer — not a search provider. After `academic_search` returns DOI-bearing results, `EnrichOpenAccess` fills the open-access PDF link on any result the provider left bare. Best-effort and nil-safe (a no-op when unconfigured); it never overwrites a provider-supplied PDF and never fails a search.

A provider can satisfy several at once — `ExaProvider` implements `Provider`, `AcademicProvider`, `AnswerProvider`, and `StructuredProvider` simultaneously, and Semantic Scholar/OpenAlex implement both `AcademicProvider` and `CitationSearcher`. The `Router` routes the `Provider`, `PatentSearcher`, and `AcademicSearcher` capabilities with per-provider breaker fallback; the synthesis, citation, and structured-domain (filing/case/econ) capabilities are resolved directly from the `Dependencies` maps in the tool layer. Each configured provider gets an independent circuit breaker.

### 3. Tiered Scraping Pipeline

```go
type Pipeline struct {
    client    *http.Client
    semaphore chan struct{}
    config    PipelineConfig
}

func (p *Pipeline) Scrape(ctx context.Context, url string, maxLength int) (*ScrapeResult, error)
```

The pipeline routes specialized content (YouTube, PDF/DOCX/PPTX) via early-return detection, then falls back through tiers in order: markdown → stealth → HTML → browser (go-rod). Each tier is a private method with the same signature; the pipeline tries each in sequence and promotes the first result that meets a quality threshold. When `EXA_API_KEY` is set, a fifth, **paid** tier (Exa `/contents`) is appended as the last resort — it runs only after every free tier fails, so the common path never incurs cost. The winning tier is surfaced to the caller as `extractedBy` (e.g. `stealth`, `exa:cached`).

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
