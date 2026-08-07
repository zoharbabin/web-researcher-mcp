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
3. **Zero global state** — All deps flow through the `tools.Dependencies` struct (constructed in `main.go` and injected at registration time). Request-scoped state (tenant ID, trace) travels in `context.Context`.
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

The five boxes above are a schematic, not an exhaustive tool list — the actual tool count has grown well past what fits in five boxes. For the full, current tool inventory grouped by category, see `internal/tools/registry.go`'s `RegisterAll()` (the source of truth) or `docs/TOOLS.md`. As of this writing that includes, beyond the five shown: structured-domain research (`filing_search`, `legal_search`, `econ_search`, `clinical_search`, `awesome_list_search`, `monarch_search`), OSINT/company reconnaissance (`company_recon`), single-call paper retrieval (`paper_fulltext`), place/context search (`local_search`), citation tooling (`citation_graph`, `verify_citation`, `audit_bibliography`, `format_bibliography`, `research_export`), trust/anti-sloptimization (`verify_recommendation`, `archive_source`, `brand_research`), and the opt-in regulated tools (`memory_save`/`memory_recall`, `workspace_contribute`/`workspace_read`, `get_my_analytics`). All of these are always registered (keyless backing data sources), except `filing_search` (`EDGAR_CONTACT_EMAIL`).

## Module Layout

```
web-researcher-mcp/
├── cmd/web-researcher-mcp/       # Entry point (wiring only)
├── cmd/gen-python-client/        # Python client schema generator (make gen-python-client)
├── internal/
│   ├── config/                   # Strongly-typed config from env
│   ├── server/                   # MCP server lifecycle (STDIO + HTTP)
│   ├── tools/                    # Tool handlers (one file per tool)
│   ├── search/                   # Pluggable providers + router + lens routing
│   ├── scraper/                  # Tiered pipeline (markdown → stealth → Jina Reader → HTML → browser) + SPA fast-path + SSRF protection + optional paid Exa final tier
│   ├── documents/                # PDF, DOCX, PPTX parsing
│   ├── cache/                    # Hybrid cache (memory L1 + optional Redis L2 + disk L3)
│   ├── auth/                     # OAuth 2.1 middleware (JWT/JWKS)
│   ├── audit/                    # Structured audit logging (PodID for cross-pod correlation)
│   ├── session/                  # Per-tenant session persistence — Manager interface (memory+disk or Redis)
│   ├── content/                  # Sanitize, dedup, truncate, quality, typed source classification, claim evidence, citation extraction, bibliography read/write (APA/MLA/BibTeX/RIS/CSL-JSON round-trip), recommendations + auto-formatted components
│   ├── metrics/                  # Prometheus metrics + per-tenant aggregate analytics
│   ├── ratelimit/                # Four-tier rate limiting (per-IP, per-tenant, global, daily quota) + optional atomic cross-pod daily quota
│   ├── circuit/                  # Circuit breaker
│   ├── persist/                  # TTL key/value store (memory or AES-256-GCM disk) backing token revocation + rate quotas
│   ├── redisbackend/             # Sole go-redis importer: Redis impls of cache/persist/session (opt-in, HTTP-only, encrypted)
│   ├── consent/                  # Consent record-verify-honor for regulated features (Checker + Noop)
│   ├── datasubject/              # GDPR access/erasure registry — (tenantID,userID) Exporter/Eraser fan-out
│   ├── useranalytics/            # Opt-in consent-gated per-user analytics (Recorder + Noop)
│   ├── memory/                   # Opt-in consent-gated long-term cross-session memory (Store + Noop)
│   ├── workspace/                # Opt-in shared workspaces — server-enforced data-plane + isolation, host-owned membership
│   └── resources/                # MCP Resources + Prompts + completion/complete handler (lens/provider/enum arg autocompletion)
├── lenses/                       # Search lens JSON files
├── tests/                        # E2E, integration tests, benchmarks + Python SDK tests
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

Several providers implement this interface — Google PSE, Brave, Serper, SearXNG, SearchAPI.io, Tavily, Exa, DuckDuckGo (the zero-config, no-key fallback), and Hacker News (the no-key HN Algolia index, `internal/search/hackernews.go`). The canonical list is `search.SupportedProviders` in `internal/search/provider.go`. The `Router` also implements `Provider`, enabling transparent multi-provider fallback — tools don't need to know whether they're calling a single provider or a routing layer.

This HN *search provider* is a separate integration from the HN *scrape-time* fetcher in the Performance table below (`internal/scraper/hackernews.go`) — one indexes and ranks HN stories for `news_search`/`web_search`, the other fetches a specific item/thread/user directly from HN's own Firebase API when `scrape_page` is pointed at an `news.ycombinator.com` URL. They share no code and hit different upstream APIs.

When `SEARCH_ROUTING` is configured, the Router wraps all available providers with per-provider circuit breakers and priority-ordered fallback. Search lenses inject `site:` operators and route through the configured provider. Lenses with a dedicated `cx` field route directly to that Google PSE engine.

#### Capability Interfaces (Patents, Academic, Structured Domains)

Beyond the general `Provider`, the system layers **opt-in capability interfaces** so a provider implements only what it supports. Each capability follows the same shape — a `…Searcher` (the method) plus a `…Provider` (Searcher + `Name()` + `Metadata()`) — with a parallel `Supported…Providers` list, `New…ProviderByName` factory, and `Available…Providers` constructor (all in `internal/search/`):

- **`PatentProvider`** (`Patents`) — `internal/search/domain.go`. Carries `ProviderMeta` for regional filtering (e.g. `patent_office=EP` skips US-only providers): SearchAPI, EPO OPS, The Lens, USPTO.
- **`AcademicProvider`** (`Scholarly`) — `internal/search/domain.go`. OpenAlex, CrossRef, PubMed (with PMC efetch full-text when `AcademicSearchParams.FullText` is set and a result carries a PMCID), Semantic Scholar, CORE.ac.uk (300M+ open-access works, native full text, all results OA by definition), and Exa (via its research-paper category).
- **`CitationSearcher`** (`Citations` / `References`) — `internal/search/domain.go`. Forward (cited-by) and backward (references) citation edges behind the `citation_graph` tool. Implemented by Semantic Scholar (rich — citation intent + influence) and OpenAlex (counts-only); the tool auto-selects Semantic Scholar first.
- **`FilingSearcher` / `CaseSearcher` / `EconSearcher` / `TrialSearcher` / `MonarchSearcher`** — `internal/search/structured_domains.go`. The structured-research domains behind `filing_search` (SEC EDGAR), `legal_search` (CourtListener), `econ_search` (FRED + World Bank + OECD + Eurostat), `clinical_search` (ClinicalTrials.gov), and `monarch_search` (Monarch Initiative biomedical knowledge graph, `internal/search/monarch.go`). Each follows the same `…Searcher` + `…Provider` + `Supported…Providers` + `New…ByName` + `Available…Providers` shape as the patent/academic capabilities, resolved from the `Dependencies` maps in the tool layer. A new provider behind an existing interface (e.g. OECD/Eurostat under `EconProvider`) is one factory case + one list entry, no tool change.
- **`AwesomeListSearcher`** (`AwesomeLists`) — `internal/search/awesome_domain.go`. Community-curated "awesome list" discovery behind `awesome_list_search`, backed by ecosyste.ms (keyless). Follows the same `…Searcher` + `…Provider` + `Supported…Providers` + `New…ByName` + `Available…Providers` shape as the other structured-domain capabilities above.
- **`LocalSearcher`** (`Local`) — `internal/search/local.go`. Physical-place search (restaurants, shops, offices) behind `local_search`, backed by Brave's three-call local pipeline (`web/search?result_filter=locations` → `local/pois` → `local/descriptions`). Brave is the sole provider today.
- **`ContextSearcher`** (`Context`) — `internal/search/context.go`. A provenance-rich LLM grounding context assembled server-side (Brave's `/res/v1/llm/context` endpoint), used by `search_and_scrape` as a fast-path via a type assertion — no tool-layer change needed to add a second provider.
- **`PaperFetcher`** (`internal/search/domain.go`, optional capability on academic providers, implemented by Semantic Scholar) — fetches full paper metadata by DOI or paper ID via `FetchPaper`, behind the single-call `paper_fulltext` tool.

Separate from the capability interfaces, three **enrichment** interfaces operate post-search on DOI-bearing results — not search providers:

- **`OAResolver`** (`internal/search/unpaywall.go`, implemented by Unpaywall) — fills the open-access PDF link after `academic_search` via `EnrichOpenAccess`. Best-effort, nil-safe, never overwrites a provider-supplied PDF.
- **`RetractionResolver`** (`internal/search/retraction.go`, implemented by `CrossrefRetractionResolver`) — flags retracted or corrected works via `EnrichRetraction`. Used by `academic_search`, `citation_graph`, `scrape_page`, `audit_bibliography`, and `verify_citation`.
- **`DOIResolver`** (`internal/search/domain.go`, optional capability on academic providers) — performs an exact entity lookup for a DOI (e.g. OpenAlex `/works/doi:{doi}`) so `verify_citation` always retrieves the cited work directly rather than relying on a relevance-ranked search whose top hit could be a different paper.

A provider can satisfy several at once — `ExaProvider` implements both `Provider` and `AcademicProvider` simultaneously, and Semantic Scholar/OpenAlex implement both `AcademicProvider` and `CitationSearcher`. The `Router` routes the `Provider`, `PatentSearcher`, and `AcademicSearcher` capabilities with per-provider breaker fallback; the citation, awesome-list, local, context, and structured-domain (filing/case/econ/trial/monarch) capabilities are resolved directly from the `Dependencies` maps in the tool layer. Each configured provider gets an independent circuit breaker.

### 3. Tiered Scraping Pipeline

```go
type Pipeline struct {
    client           *http.Client
    semaphore        chan struct{} // fast tiers: markdown/stealth/jina/html/exa + native routes
    browserSemaphore chan struct{} // browser (go-rod) tier only — smaller, independent pool (#472)
    config           PipelineConfig
}

func (p *Pipeline) Scrape(ctx context.Context, url string, maxLength int) (*ScrapeResult, error)
```

The pipeline routes specialized content (YouTube, Hacker News threads, PDF/DOCX/PPTX) via early-return detection, then falls back through tiers in order: markdown → stealth → Jina Reader → HTML → browser (go-rod). Each tier is a private method with the same signature; the pipeline tries each in sequence and promotes the first result that meets a quality threshold. Each tier attempt acquires its own concurrency slot for just that attempt — via `acquireTier(ctx, tierName)` — rather than holding one slot for the whole fallback sequence, and the browser tier draws from its own smaller `browserSemaphore` pool instead of the shared fast-tier `semaphore`, so a slow browser attempt (up to 30s) can never block unrelated fast requests (#472). The Jina Reader tier (`internal/scraper/jina.go`, `r.jina.ai`) is a cloud-based bot-wall bypass that runs unconditionally and keyless; `JINA_API_KEY` raises its rate limit, and `JINA_READER_DISABLED` is a kill switch. When `EXA_API_KEY` is set, a **paid** final tier (Exa `/contents`) is appended as the last resort — it runs only after every preceding tier fails to extract more than 100 bytes, so the common path never incurs cost. The winning tier is surfaced to the caller as `extractedBy` (e.g. `stealth`, `exa:cached`).

**SPA fast-path:** When the URL matches a known SPA domain (`isSPADomain` in `internal/scraper/`), the pipeline skips directly to the browser tier rather than spending time on the markdown/stealth/HTML tiers that would return JS shells. This avoids wasted round-trips and the associated latency for single-page apps.

`Pipeline.ScrapeRaw()` is a separate, non-tiered path used by `scrape_page`'s `mode: raw`: it performs a single SSRF-checked fetch and returns the response body verbatim — no sanitization, no quality scoring, no tier fallback. Raw output is untrusted (it may contain injection payloads) and is cached under a distinct key so it never collides with the cleaned `full`/`preview` results.

### 4. Dependency Injection

All services are constructed explicitly in `main.go` and passed down via the `tools.Dependencies` struct. Tool handlers receive deps via closure capture at registration time — see `internal/tools/registry.go` for the canonical pattern.

### 5. Context Propagation

Every request carries a `context.Context` with deadline. Session and tenant IDs flow through the session manager for isolation. Structured logging via `slog` attaches relevant fields at each layer.

### 6. Concurrency Model

- **Per-tool timeout**: Context with deadline on every tool call
- **Bounded parallelism**: Two independent semaphore channels for concurrent scrapes — the fast tiers (markdown/stealth/jina/html/exa) share one pool (default 5, `MAX_SCRAPE_CONCURRENCY`), the browser (go-rod) tier has its own smaller pool (default 2, `MAX_SCRAPE_CONCURRENCY_BROWSER`), so a burst of slow browser scrapes can't starve fast ones (#472)
- **Request coalescing**: `internal/tools/coalesce.go` wraps cache-miss fetches in `golang.org/x/sync/singleflight`, keyed by tenant ID + the tool's own cache key, so concurrent identical requests from the *same* tenant share one upstream call instead of firing N redundant ones; two tenants issuing the same query never share a dedup key or a result (#474)
- **Per-client backpressure**: Rate limiter per tenant (+ per-IP pre-auth), reject with 429
- **Graceful shutdown**: Context cancellation propagates, in-flight requests drain

### 7. Operator Observability

Routing, provider health, and recent errors are **operator/debug data, never model content** — the Router exists to make providers interchangeable to the LLM, so its internals surface only through non-content channels. Three subsystems implement this, each keeping the provider *name* as the disclosure boundary (no upstream URLs, credentials, or breaker counts leak):

- **Per-call routing trace** — `search.RoutingTrace` (`internal/search/routing_trace.go`) is a request-scoped, context-carried, concurrency-safe collector the `Router` populates while iterating providers. The tool layer reads its `RoutingDecision` and attaches it to the result's MCP `_meta.routing` (via `routingMeta` / `withRoutingMeta` in `internal/tools/errors.go`, merged with — never clobbering — the cache-freshness `_meta`). The same summary is mirrored to `audit.AuditEvent.Metadata["routing"]`. Omitted entirely when there is nothing to observe (single-provider / non-routed call).
- **Recent-errors ring** — `metrics.ErrorRing` (`internal/metrics/errors.go`) is a bounded, memory-only, tenant-aware ring buffer fed at the central audit sink (`auditToolCallQuery`), so every tool error path records one redacted sample (cause passed through `audit.MaskSecrets`). No disk, no unbounded growth — consistent with the no-retention posture.
- **Live provider health** — `search.Router.Health()` (`internal/search/health.go`) returns a tri-state `HealthSnapshot` (`healthy` / `degraded` / `unhealthy`) plus each routed provider's circuit-breaker state.
- **Bounded per-tenant metrics** — `metrics.Collector`'s `tenantStats` map (`internal/metrics/collector.go`) is capped at `METRICS_MAX_TENANTS` (default 10,000); once at capacity, `getOrCreateTenant` evicts the least-recently-called tenant before inserting a new one, so per-tenant aggregate stats can't grow unbounded on a multi-tenant deployment with high tenant churn (#475).

These reach operators through read-only MCP Resources (`diagnostics://errors/recent`, `diagnostics://health` beside `stats://*`, registered in `internal/resources/resources.go` via the small `HealthProvider` interface) and, in HTTP mode, an admin-gated operator dashboard (`GET /dashboard` + `GET /dashboard/data`, `internal/server/dashboard.go`). The dashboard is a self-contained HTML page (no CDN, no build) under a per-request nonce CSP, aggregate-only. See `docs/TOOLS.md` (Routing Provenance) and `docs/DEPLOYMENT.md` (Operator Observability) for the field contracts.

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
| Scrape (browser) | 2-10s | go-rod headless, bounded to MaxBrowserConcurrency (independent pool, #472) |
| YouTube transcript | 1-5s | 3-strategy: captions → timedtext API → description |
| Hacker News item/list/user | 200-700ms | Native HN Firebase REST; story + top comments fetched in parallel |
| search_and_scrape | 2-15s | Parallel scrape (semaphore, default 5) |

## Concurrency Limits

Default values are all configurable via environment variables — see `docs/DEPLOYMENT.md` for the full list with defaults.

```
Browser pool (go-rod):        concurrent (mutex guards init/liveness-check/relaunch only; page concurrency bounded by browserSemaphore)
```

A dead CDP connection (Chromium crashed via OOM, a malicious page, or a Chromium bug) is detected on the next `getBrowserPool` call via a bounded liveness probe and transparently relaunched — a crash no longer leaves the browser tier hanging toward its 30s timeout until the whole process restarts (#464).

Rate limiting applies only in HTTP mode. STDIO mode (the default for Claude Code, Cursor, and Claude Desktop) has no internal rate limiting — only upstream API quotas apply.

Browser scrapes hold a `browserSemaphore` slot (separate, smaller pool than the fast tiers' `semaphore`, #472); the browser pool mutex is released before page creation, so multiple scrapes run concurrently up to that limit.

## Error Handling

Three-layer architecture: typed scraper errors (`ScrapeError{Kind}` in `internal/scraper/errors.go`) → structured tool-level responses (`structuredError()`, `upstreamErrorResponse()` in `internal/tools/errors.go`) → MCP protocol (`IsError: true` with dual-format text: natural language + JSON metadata).

Every error response includes machine-readable JSON: `{"error":{"kind":"...","retryable":...,"suggestedAction":"..."}}`. This lets LLM clients branch programmatically on error type. Error kinds: `rate_limited`, `auth_required`, `blocked`, `network`, `not_found`, `content_empty`, `browser_unavailable`, `validation`, `config`, `upstream_unavailable`, `session_not_found`.

Full specification: see `docs/ERROR_HANDLING.md`.

## Binary Output

Single static binary with no runtime dependencies except optional Chromium for JS rendering (auto-downloaded by go-rod on first headless scrape). The published Docker image bundles Chromium with `CHROME_PATH` preset, so JavaScript rendering works out of the box with no download.

```bash
go build -o web-researcher-mcp ./cmd/web-researcher-mcp   # Build
./web-researcher-mcp                                       # Run (STDIO)
PORT=3000 ./web-researcher-mcp                             # Run (HTTP)
```
