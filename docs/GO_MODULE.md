# Go Module Dependencies

## go.mod (planned)

```go
module github.com/zoharbabin/web-researcher-mcp

go 1.23

require (
    // MCP Protocol
    github.com/modelcontextprotocol/go-sdk v1.6.0

    // HTML Parsing & Scraping
    github.com/PuerkitoBio/goquery v1.9.2
    github.com/chromedp/chromedp v0.11.2

    // Caching
    github.com/dgraph-io/ristretto/v2 v2.1.0
    go.etcd.io/bbolt v1.4.0

    // Redis (optional)
    github.com/redis/go-redis/v9 v9.7.0

    // Authentication
    github.com/lestrrat-go/jwx/v3 v3.0.0

    // Document Parsing
    github.com/ledongthuc/pdf v0.0.5
    github.com/sajari/docconv v1.3.8

    // Content Security
    github.com/microcosm-cc/bluemonday v1.0.27

    // Metrics
    github.com/prometheus/client_golang v1.20.5

    // Rate Limiting
    golang.org/x/time v0.9.0

    // Testing
    github.com/stretchr/testify v1.9.0
)
```

## Dependency Rationale

### Core: `modelcontextprotocol/go-sdk` v1.6.0
- **Why:** Official MCP SDK maintained by Google. Full spec compliance (2025-11-25).
- **What it provides:** Server, StdioTransport, StreamableHTTPHandler, tool/resource/prompt registration with generics.
- **Alternative considered:** `mark3labs/mcp-go` (8.3K stars, community) — more features today but official SDK is safer long-term bet.

### HTML: `PuerkitoBio/goquery` v1.9.x
- **Why:** jQuery-style API, 14K stars, battle-tested. Faster than launching a browser.
- **What it provides:** CSS selector-based DOM traversal and text extraction.
- **Alternative considered:** `x/net/html` (stdlib) — too low-level for practical use.

### Browser: `go-rod/rod` v0.116.2 + `go-rod/stealth` v0.4.9
- **Why:** High-level Chrome DevTools Protocol driver, 6.9K stars. Auto-downloads Chromium, built-in stealth plugin for anti-bot detection bypass. Simpler API than chromedp.
- **What it provides:** Headless browser automation with browser pool, stealth pages (navigator spoofing, WebGL masking), JavaScript evaluation for content extraction.
- **Alternative considered:** `chromedp/chromedp` (11K stars) — more established but lower-level API, no built-in anti-detection. `enetx/surf` — requires Go 1.25+, not compatible with our Go 1.23 target.

### Cache: `dgraph-io/ristretto/v2`
- **Why:** TinyLFU admission + sampled LFU eviction. Memory-bounded via cost parameter. Used in Dgraph and Badger (production databases).
- **What it provides:** Concurrent cache with automatic admission control — only caches items that are worth caching.
- **Alternative considered:** `maypok86/otter` (faster benchmarks, newer) — less battle-tested. `allegro/bigcache` — no admission policy, wastes memory on one-off items.

### Disk: `go.etcd.io/bbolt`
- **Why:** Single-file B+tree database. Embedded, no separate process. Used in etcd.
- **What it provides:** Persistent key-value storage for cache entries.
- **Alternative considered:** `dgraph-io/badger` (LSM-tree, higher throughput) — overkill for a cache backend. bbolt is simpler and the file is easier to manage.

### Redis: `redis/go-redis/v9`
- **Why:** Official Redis Go client. Full feature set, pipelining, pub/sub, cluster support.
- **What it provides:** Shared cache and session state for horizontal scaling.
- **When used:** Only when `REDIS_URL` is set. Otherwise, local cache only.

### Auth: `lestrrat-go/jwx/v3`
- **Why:** Full JOSE suite (JWT, JWK, JWE, JWS). Built-in JWKS auto-caching and refresh.
- **What it provides:** Zero-config JWKS management, JWT validation, key set resolution.
- **Alternative considered:** `golang-jwt/jwt/v5` — simpler but doesn't handle JWKS caching. Would need custom code for auto-refresh.

### PDF: `ledongthuc/pdf`
- **Why:** MIT licensed, free. Basic text extraction from PDF.
- **Limitation:** Limited charset/encoding support for complex PDFs.
- **Fallback:** Shell out to `pdftotext` (from Xpdf/Poppler) for complex documents.
- **Alternative considered:** `unidoc/unipdf` — excellent but commercial license.

### DOCX/PPTX: `sajari/docconv`
- **Why:** Multi-format text extraction in one package. DOCX, PPTX, PDF, HTML all handled.
- **What it provides:** `docconv.Convert(reader, mimeType)` → plain text.
- **Alternative considered:** `unidoc/unioffice` — commercial. Manual ZIP parsing — too much code.

### Sanitization: `microcosm-cc/bluemonday`
- **Why:** Whitelist-based HTML sanitizer. Used by Gitea, Hugo, and many Go web apps.
- **What it provides:** Policy-based sanitization — only allow safe elements/attributes.
- **Usage:** Strip everything dangerous from scraped content before returning to LLM.

### Metrics: `prometheus/client_golang`
- **Why:** De facto standard for Go metrics. Direct integration with Prometheus/Grafana.
- **What it provides:** Counter, gauge, histogram, summary metric types. HTTP handler for scraping.

### Rate Limiting: `golang.org/x/time/rate`
- **Why:** Standard library extension. Token bucket algorithm. Used by the Go MCP SDK itself.
- **What it provides:** `rate.Limiter` with configurable rate and burst.
- **Usage:** Per-tenant limiter instances stored in `sync.Map`.

### Testing: `stretchr/testify`
- **Why:** Most popular Go testing library. Clean assertion API.
- **What it provides:** `assert`, `require`, `mock` packages.
- **Note:** Table-driven tests don't require testify — it just makes assertions cleaner.

---

## Dependency Footprint

| Metric | Value |
|---|---|
| Direct dependencies | 11 |
| Total (including transitive) | ~50 |
| Binary size | ~20MB (self-contained) |
| Install time | 0s (single binary, no runtime) |

---

## Security: Dependency Audit

```bash
# Check for known vulnerabilities
govulncheck ./...

# Verify module checksums
go mod verify

# Generate SBOM
cyclonedx-gomod mod -json -output sbom.json
```

All dependencies chosen have:
- Active maintenance (commits within last 3 months)
- No known unpatched CVEs
- Permissive licenses (MIT, Apache 2.0, BSD)
- Significant adoption (>1000 stars or official/stdlib)
