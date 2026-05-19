# Go Module Dependencies

## go.mod

```go
module github.com/zoharbabin/web-researcher-mcp

go 1.25.0

require (
    github.com/PuerkitoBio/goquery v1.10.3
    github.com/go-rod/rod v0.116.2
    github.com/go-rod/stealth v0.4.9
    github.com/google/uuid v1.6.0
    github.com/microcosm-cc/bluemonday v1.0.27
    github.com/modelcontextprotocol/go-sdk v1.6.0
    github.com/prometheus/client_golang v1.22.0
    golang.org/x/time v0.11.0
)
```

## Dependency Rationale

### Core: `modelcontextprotocol/go-sdk` v1.6.0
- **Why:** Official MCP SDK from the Model Context Protocol project. Full spec compliance (2025-03-26 protocol version).
- **What it provides:** Server, StdioTransport, StreamableHTTPHandler, typed tool registration with auto JSON schema inference, resource/prompt registration.
- **Alternative considered:** `mark3labs/mcp-go` (community) — migrated away from it to the official SDK for protocol compliance and long-term maintenance.

### HTML: `PuerkitoBio/goquery` v1.10.3
- **Why:** jQuery-style API, 14K stars, battle-tested. Faster than launching a browser.
- **What it provides:** CSS selector-based DOM traversal and text extraction.
- **Alternative considered:** `x/net/html` (stdlib) — too low-level for practical use.

### Browser: `go-rod/rod` v0.116.2 + `go-rod/stealth` v0.4.9
- **Why:** High-level Chrome DevTools Protocol driver. Auto-downloads Chromium, built-in stealth plugin for anti-bot detection bypass.
- **What it provides:** Headless browser automation with browser pool, stealth pages (navigator spoofing, WebGL masking), JavaScript evaluation for content extraction.
- **Alternative considered:** `chromedp/chromedp` — more established but lower-level API, no built-in anti-detection.

### Sanitization: `microcosm-cc/bluemonday` v1.0.27
- **Why:** Whitelist-based HTML sanitizer. Used by Gitea, Hugo, and many Go web apps.
- **What it provides:** Policy-based sanitization — only allow safe elements/attributes.
- **Usage:** Strip dangerous content from scraped HTML before returning to LLM.

### Metrics: `prometheus/client_golang` v1.22.0
- **Why:** De facto standard for Go metrics. Direct integration with Prometheus/Grafana.
- **What it provides:** Counter, gauge, histogram metric types. HTTP handler for scraping.

### Rate Limiting: `golang.org/x/time` v0.11.0
- **Why:** Standard library extension. Token bucket algorithm.
- **What it provides:** `rate.Limiter` with configurable rate and burst.
- **Usage:** Per-tenant limiter instances stored in `sync.Map`.

### UUID: `google/uuid` v1.6.0
- **Why:** Standard UUID generation for session IDs.

---

## Dependency Footprint

| Metric | Value |
|---|---|
| Direct dependencies | 8 |
| Total (including transitive) | ~30 |
| Binary size | ~22MB (self-contained) |
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
