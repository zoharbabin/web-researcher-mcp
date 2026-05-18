# Implementation Plan

## Phase Overview

| Phase | Scope | Est. Effort | Outcome |
|-------|-------|-------------|---------|
| **0** | Project scaffold + CI | 1 day | Repo structure, go.mod, CI pipeline, Makefile |
| **1** | Core: STDIO + web_search | 3 days | Minimal working MCP server with one tool |
| **2** | Scraping pipeline | 4 days | scrape_page with tiered strategy |
| **3** | Remaining tools | 3 days | All 8 tools functional |
| **4** | Cache + persistence | 2 days | Ristretto + disk + hybrid strategy |
| **5** | HTTP transport + OAuth | 2 days | Multi-client with auth |
| **6** | Search providers + lenses | 2 days | Brave, Serper, SearXNG adapters |
| **7** | Hardening | 3 days | Rate limiting, circuit breaker, metrics, audit |
| **8** | E2E tests + docs | 2 days | Full test suite, deployment docs verified |
| **Total** | | **~22 days** | Production-ready v1.0 |

---

## Phase 0: Scaffold

**Goal:** Empty project that builds, lints, and passes CI.

### Tasks
- [ ] `go mod init github.com/zoharbabin/web-researcher-mcp`
- [ ] Add `go-sdk` dependency: `go get github.com/modelcontextprotocol/go-sdk@v1.6.0`
- [ ] Create `cmd/web-researcher-mcp/main.go` (minimal: starts MCP server, handles stdin EOF)
- [ ] Create `internal/config/config.go` (load env vars)
- [ ] Create `Makefile` with targets: build, test, lint, vet, vuln
- [ ] Create `.github/workflows/ci.yml` (Go 1.23, ubuntu + macos)
- [ ] Create `.goreleaser.yml` for releases
- [ ] Create `Dockerfile` (multi-stage, distroless)
- [ ] Create `.env.example`
- [ ] Copy `lenses/*.json` from architecture docs

### Verification
```bash
go build ./cmd/web-researcher-mcp
echo '{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}' | ./web-researcher-mcp
# Should respond with capabilities JSON
```

---

## Phase 1: Core — STDIO + web_search

**Goal:** A working MCP server that Claude Code can connect to and execute Google searches.

### Tasks
- [ ] `internal/server/server.go` — MCP server setup, tool registration
- [ ] `internal/server/lifecycle.go` — Signal handling, graceful shutdown
- [ ] `internal/tools/registry.go` — Tool registration helper
- [ ] `internal/tools/search.go` — web_search tool handler
- [ ] `internal/search/provider.go` — SearchProvider interface
- [ ] `internal/search/google.go` — Google PSE adapter
- [ ] `internal/scraper/ssrf.go` — SSRF-safe HTTP client
- [ ] Unit tests for all of the above
- [ ] Integration test: tool call via in-memory transport

### Key Decisions
- Use `mcp.AddTool` with generics for type-safe input/output
- Google PSE adapter uses `net/http` with SSRF-safe transport
- Circuit breaker wraps API calls from day one
- Cache interface defined but implementation deferred to Phase 4

### Verification
```bash
# Configure Claude Code to use the binary
# Execute: web_search("golang mcp server")
# Should return URLs
```

---

## Phase 2: Scraping Pipeline

**Goal:** Full `scrape_page` tool with tiered fallback strategy.

### Tasks
- [ ] `internal/scraper/pipeline.go` — Orchestrator (try scrapers in order)
- [ ] `internal/scraper/markdown.go` — Accept: text/markdown negotiation
- [ ] `internal/scraper/html.go` — goquery-based extraction
- [ ] `internal/scraper/browser.go` — chromedp headless extraction
- [ ] `internal/scraper/youtube.go` — YouTube transcript extraction
- [ ] `internal/documents/parser.go` — Unified document parser interface
- [ ] `internal/documents/pdf.go` — PDF text extraction
- [ ] `internal/documents/docx.go` — DOCX extraction
- [ ] `internal/documents/pptx.go` — PPTX extraction
- [ ] `internal/content/sanitize.go` — HTML/content sanitization
- [ ] `internal/content/truncate.go` — Smart truncation at breakpoints
- [ ] `internal/content/citation.go` — Citation extraction
- [ ] `internal/tools/scrape.go` — scrape_page tool handler
- [ ] Test fixtures in `testdata/` (sample HTML, PDF, DOCX)
- [ ] Integration tests with httptest mock servers

### Key Decisions
- goquery for HTML (not colly — we don't need crawling, just parsing)
- chromedp with pool (reuse browser instances, max 3 concurrent)
- YouTube: native Go implementation (~200 lines) + yt-dlp fallback
- Documents: `sajari/docconv` for DOCX/PPTX, `ledongthuc/pdf` for PDF
- Content sanitization via `bluemonday` whitelist policy

### Verification
```bash
# scrape_page("https://go.dev/doc/effective_go")
# Should return clean markdown/text content
# scrape_page("https://arxiv.org/pdf/2301.00001")
# Should return PDF text
```

---

## Phase 3: Remaining Tools

**Goal:** All 8 tools functional.

### Tasks
- [ ] `internal/tools/searchandscrape.go` — Combined pipeline
- [ ] `internal/tools/imagesearch.go` — Image search
- [ ] `internal/tools/newssearch.go` — News search
- [ ] `internal/tools/academic.go` — Academic search (site-restricted)
- [ ] `internal/tools/patent.go` — Patent search (site-restricted)
- [ ] `internal/tools/sequential.go` — Sequential research tracker
- [ ] `internal/session/manager.go` — Session lifecycle management
- [ ] `internal/session/state.go` — Research session state
- [ ] `internal/content/dedup.go` — Paragraph-level deduplication
- [ ] `internal/content/quality.go` — Quality scoring
- [ ] Unit tests for all tools
- [ ] Patent: company name variations, CPC codes
- [ ] Academic: site pool, year filtering, source selection

### Key Decisions
- search_and_scrape uses `errgroup` for parallel scraping
- Sequential search state in `sync.Map` (single instance), Redis (multi)
- Quality scoring weights: relevance 35%, freshness 20%, authority 25%, content 20%
- Deduplication: djb2 hash, 85% similarity threshold

### Verification
```bash
# All tools callable via Claude Code
# sequential_search maintains session across multiple calls
# search_and_scrape returns ranked, deduplicated sources
```

---

## Phase 4: Cache + Persistence

**Goal:** Fast in-memory cache with disk persistence and optional Redis.

### Tasks
- [ ] `internal/cache/cache.go` — Cache interface
- [ ] `internal/cache/memory.go` — Ristretto wrapper
- [ ] `internal/cache/disk.go` — bbolt persistence
- [ ] `internal/cache/redis.go` — Redis adapter (behind build tag or runtime check)
- [ ] `internal/cache/hybrid.go` — L1 memory + L2 disk/redis
- [ ] Wire cache into all tools (inject via tool constructor)
- [ ] Cache key generation (SHA-256 of params)
- [ ] TTL per tool type (search=30m, scrape=1h, academic=24h)
- [ ] Optional AES-256-GCM encryption for disk cache
- [ ] Benchmark tests for cache operations

### Key Decisions
- Ristretto v2 for memory (TinyLFU admission, memory-bounded)
- bbolt for disk (simple, single-file, no separate process)
- Redis via `go-redis/v9` (optional, for horizontal scaling)
- Encryption: Go stdlib `crypto/aes` + `crypto/cipher`

### Verification
```bash
# Second search for same query returns instantly (cache hit)
# Restart server → cache reloads from disk
# With REDIS_URL → cache shared across instances
```

---

## Phase 5: HTTP Transport + OAuth

**Goal:** Multi-client HTTP server with OAuth 2.1 authentication.

### Tasks
- [ ] HTTP server setup (net/http ServeMux + MCP SDK handler)
- [ ] `internal/auth/middleware.go` — OAuth 2.1 Bearer token validation
- [ ] `internal/auth/jwks.go` — JWKS auto-fetching with cache
- [ ] `internal/auth/claims.go` — Tenant/user extraction from JWT
- [ ] CORS middleware
- [ ] Health check endpoints (`/health/live`, `/health/ready`)
- [ ] Prometheus metrics endpoint (`/metrics`)
- [ ] Security headers middleware
- [ ] Per-tenant context propagation
- [ ] Integration tests with test JWT tokens

### Key Decisions
- `lestrrat-go/jwx` v3 for JWKS + JWT (auto-refresh, production-proven)
- `net/http` ServeMux (Go 1.22+ patterns — no external router needed)
- OAuth metadata at `/.well-known/oauth-authorization-server`
- Both STDIO and HTTP run simultaneously when PORT is set

### Verification
```bash
PORT=3000 OAUTH_ISSUER_URL=... ./web-researcher-mcp
# curl -H "Authorization: Bearer ..." http://localhost:3000/mcp
# Should respond to MCP requests
# Without token → 401
```

---

## Phase 6: Search Providers + Lenses

**Goal:** Pluggable search backends ready for Google PSE sunset.

### Tasks
- [ ] `internal/search/brave.go` — Brave Search API adapter
- [ ] `internal/search/serper.go` — Serper.dev adapter
- [ ] `internal/search/searxng.go` — SearXNG adapter
- [ ] `internal/search/lenses.go` — Lens loading + query injection
- [ ] `internal/search/factory.go` — Provider factory from config
- [ ] Fallback chain (primary → fallback provider)
- [ ] Per-provider circuit breakers
- [ ] Lens auto-discovery from `lenses/` directory
- [ ] Integration tests for each provider (mock HTTP)

### Key Decisions
- Default provider: Google PSE (backward compatible)
- Brave is the recommended alternative (configure via docs)
- Lenses use `site:` injection for <10 domains, dedicated `cx` for more
- Custom lenses loadable from `CUSTOM_LENSES_PATH`

### Verification
```bash
# SEARCH_PROVIDER=brave: searches use Brave API
# lens="programming": restricts to curated domains via Google PSE
# Primary fails → falls back to secondary
```

---

## Phase 7: Hardening

**Goal:** Production-ready with all security and observability features.

### Tasks
- [ ] `internal/ratelimit/limiter.go` — Three-tier rate limiting
- [ ] `internal/circuit/breaker.go` — Timer-free circuit breaker
- [ ] `internal/metrics/collector.go` — Per-tool metrics + reservoir sampling
- [ ] `internal/metrics/prometheus.go` — Prometheus export
- [ ] `internal/resources/stats.go` — MCP Resources (stats://*, search://recent)
- [ ] `internal/resources/prompts.go` — MCP Prompts (comprehensive-research, fact-check, etc.)
- [ ] Audit logging (structured, per-request)
- [ ] Content size optimization (token estimation, size categories)
- [ ] Daily quota tracking per tenant
- [ ] Admin endpoints (cache flush, session kill)
- [ ] Env validation at startup (warn, don't exit)

### Key Decisions
- Rate limiter: `golang.org/x/time/rate` per tenant + global
- Circuit breaker: timer-free state machine (check elapsed on execute)
- Metrics: Prometheus client_golang (standard)
- Audit: slog JSON to stderr (ship to SIEM externally)

### Verification
```bash
# Exceed rate limit → 429 with Retry-After
# API failures → circuit opens → fast-fail
# /metrics endpoint returns Prometheus format
# stats://tools resource returns per-tool metrics
```

---

## Phase 8: E2E Tests + Documentation

**Goal:** Comprehensive test suite and verified documentation.

### Tasks
- [ ] E2E: STDIO transport (spawn process, MCP handshake, tool call)
- [ ] E2E: HTTP transport (start server, OAuth, tool call)
- [ ] E2E: Process lifecycle (stdin close → clean exit)
- [ ] E2E: Concurrent sessions (parallel tool calls, isolation)
- [ ] E2E: Orphan prevention (kill parent → child exits)
- [ ] Benchmark suite (cache, scraper, search)
- [ ] `go test -race ./...` passes clean
- [ ] Coverage report (>80% target)
- [ ] Verify all docs match implementation
- [ ] Test with Claude Code (real usage)
- [ ] Test with Cursor and Claude Desktop
- [ ] GoReleaser: build + publish test release

### Verification
```bash
go test -race -coverprofile=c.out ./...
go tool cover -func c.out | tail -1  # Should show >80%
goreleaser release --snapshot
# Binary works in Claude Code, Cursor, Claude Desktop
```

---

## Post-v1.0 Roadmap

| Item | Priority | Notes |
|------|----------|-------|
| WebSocket transport | Medium | For real-time streaming use cases |
| Plugin system | Low | Custom tools via shared libraries |
| Distributed tracing | Medium | OpenTelemetry integration |
| SearXNG Docker Compose | Low | One-command self-hosted search |
| Browser pool management | Medium | Shared Chrome instances across sessions |
| Content negotiation v2 | Low | llms.txt, robots.txt-for-AI compliance |
| Streaming tool responses | Medium | Progressive scraping results |
| Multi-language lenses | Low | Localized domain lists per language |

---

## Risk Mitigation

| Risk | Mitigation |
|------|------------|
| Go MCP SDK breaking changes | Pin version, test on upgrade |
| chromedp Chrome dependency | Optional (HTML scraper works without it) |
| YouTube API changes | yt-dlp fallback, native extractor is robust to HTML changes |
| Google PSE sunset earlier than announced | Brave adapter ready from Phase 6 |
| Redis unavailability | Graceful degradation to local cache |
| High memory under load | Ristretto is memory-bounded, chromedp pool is capped |
