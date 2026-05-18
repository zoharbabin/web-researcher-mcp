# Supplementary Specifications

Technical details that complete the implementation docs. Reference this alongside the main architecture and tool docs.

---

## Configuration Struct

```go
// internal/config/config.go
package config

import (
    "log/slog"
    "time"
)

type Config struct {
    // Search
    GoogleAPIKey       string // GOOGLE_CUSTOM_SEARCH_API_KEY
    GoogleCX           string // GOOGLE_CUSTOM_SEARCH_ID
    SearchProvider     string // SEARCH_PROVIDER (default: "google")
    FallbackProvider   string // SEARCH_FALLBACK_PROVIDER
    BraveAPIKey        string // BRAVE_API_KEY
    SerperAPIKey       string // SERPER_API_KEY
    SearXNGURL         string // SEARXNG_URL
    CustomLensesPath   string // CUSTOM_LENSES_PATH

    // HTTP Transport
    Port            int    // PORT (0 = STDIO only)
    OAuthIssuerURL  string // OAUTH_ISSUER_URL
    OAuthAudience   string // OAUTH_AUDIENCE
    AllowedOrigins  []string // ALLOWED_ORIGINS (comma-separated)
    TLSCertFile     string // TLS_CERT_FILE
    TLSKeyFile      string // TLS_KEY_FILE

    // Cache
    CacheDir           string // CACHE_DIR (default: "./cache")
    CacheMaxMemoryMB   int    // CACHE_MAX_MEMORY_MB (default: 64)
    CacheEncryptionKey string // CACHE_ENCRYPTION_KEY (64 hex chars)
    RedisURL           string // REDIS_URL
    CacheIsolation     string // CACHE_ISOLATION (default: "shared", or "tenant")
    CacheDisabledTenants []string // CACHE_DISABLED_TENANTS (comma-separated)

    // Rate Limiting
    RateLimit RateLimitConfig

    // Scraping
    AllowPrivateIPs     bool     // ALLOW_PRIVATE_IPS (default: false)
    AllowedDomains      []string // ALLOWED_DOMAINS (comma-separated)
    ChromePath          string   // CHROME_PATH (auto-detect if empty)
    MaxScrapeConcurrency int     // MAX_SCRAPE_CONCURRENCY (default: 5)
    MaxBrowserConcurrency int    // (hardcoded: 3, not configurable)

    // Session
    SessionTTL time.Duration // (default: 30 * time.Minute)

    // Observability
    LogLevel       slog.Level // LOG_LEVEL (default: slog.LevelInfo)
    LogFormat      string     // LOG_FORMAT (default: "json")
    MetricsEnabled bool       // METRICS_ENABLED (default: true)

    // Admin
    CacheAdminKey string // CACHE_ADMIN_KEY
}

type RateLimitConfig struct {
    PerTenant    int // RATE_LIMIT_PER_TENANT (default: 30 req/min)
    Global       int // RATE_LIMIT_GLOBAL (default: 1000 req/s)
    DailyQuota   int // DAILY_QUOTA_PER_TENANT (default: 1000)
}

// Load reads all env vars, validates format, returns Config.
// Returns error if required vars are missing or malformed.
// The server SHOULD NOT exit on error — log it and allow MCP handshake.
func Load() (*Config, error) { ... }
```

### Validation Rules

| Variable | Required | Pattern | On Failure |
|----------|----------|---------|------------|
| `GOOGLE_CUSTOM_SEARCH_API_KEY` | Yes | `AIza` prefix, 39 chars | Warn, tools fail at call time |
| `GOOGLE_CUSTOM_SEARCH_ID` | Yes | Non-empty | Warn, tools fail at call time |
| `BRAVE_API_KEY` | If provider=brave | `BSA` prefix | Warn |
| `CACHE_ENCRYPTION_KEY` | No | 64 hex chars exactly | Warn, use plaintext |
| `PORT` | No | 1-65535 | Warn, STDIO only |
| `OAUTH_ISSUER_URL` | If PORT set | Valid URL | Warn, HTTP starts without auth |
| `REDIS_URL` | No | `redis://` or `rediss://` prefix | Warn, local cache only |

### Redis Unavailability Behavior

If `REDIS_URL` is configured but Redis is unreachable:
1. **At startup:** Log warning. Server starts normally. Health `/ready` returns 503.
2. **At runtime:** Hybrid cache degrades to local memory + disk (L1 only). No error to clients.
3. **On reconnect:** Hybrid cache auto-promotes back to L1+L2. Health returns 200.
4. **Sessions:** Fall back to local `sync.Map`. Sessions are NOT shared across instances until Redis recovers.

---

## Error Types

```go
// internal/errors/errors.go
package errors

import "fmt"

// Sentinel errors — check with errors.Is()
var (
    ErrSSRFBlocked    = fmt.Errorf("ssrf: request blocked (private IP or blocked hostname)")
    ErrRateLimited    = fmt.Errorf("rate limited")
    ErrCircuitOpen    = fmt.Errorf("circuit breaker open")
    ErrSessionExpired = fmt.Errorf("session expired")
    ErrNotFound       = fmt.Errorf("not found")
    ErrTimeout        = fmt.Errorf("operation timed out")
)

// ToolError is returned to the MCP client as tool result with isError=true
type ToolError struct {
    Tool    string `json:"tool"`
    Code    string `json:"code"`
    Message string `json:"message"`
    Err     error  `json:"-"`
}

func (e *ToolError) Error() string { return fmt.Sprintf("%s: %s", e.Tool, e.Message) }
func (e *ToolError) Unwrap() error { return e.Err }

// Error codes (returned in MCP tool error responses)
const (
    CodeInvalidInput   = "invalid_input"    // Bad parameters
    CodeRateLimited    = "rate_limited"     // Per-tenant or global limit hit
    CodeTimeout        = "timeout"          // Operation timed out
    CodeUpstreamError  = "upstream_error"   // Search provider or target site failed
    CodeSSRFBlocked    = "ssrf_blocked"     // URL targets private IP
    CodeNotConfigured  = "not_configured"   // Required API key missing
    CodeSessionExpired = "session_expired"  // Sequential search session gone
    CodeContentEmpty   = "content_empty"    // Scrape returned no useful content
    CodeQuotaExceeded  = "quota_exceeded"   // Daily quota exhausted
)
```

### Error → MCP Response Mapping

| Error Type | MCP Response |
|------------|-------------|
| `ToolError` | `CallToolResult{IsError: true, Content: [{Type: "text", Text: error.Message}]}` |
| `context.DeadlineExceeded` | Wrap as `ToolError{Code: "timeout"}` |
| `ErrRateLimited` | HTTP 429 + `Retry-After` header (HTTP mode); or `ToolError{Code: "rate_limited"}` (STDIO) |
| `ErrCircuitOpen` | `ToolError{Code: "upstream_error", Message: "service temporarily unavailable"}` |
| Protocol-level errors | SDK handles these (invalid JSON-RPC, unknown method) |

---

## YouTube Transcript Extraction

```go
// internal/scraper/youtube.go

// Strategy:
// 1. Detect YouTube URL (youtube.com/watch, youtu.be, youtube.com/embed)
// 2. Extract video ID from URL
// 3. Fetch watch page HTML
// 4. Parse ytInitialPlayerResponse JSON from <script> tag
// 5. Extract captionTracks from playerCaptionsTracklistRenderer
// 6. Fetch the first available caption track (prefer "en", fall back to any)
// 7. Parse XML transcript (TimedText format) → plain text
// 8. If steps 3-7 fail: try yt-dlp subprocess fallback

// Native extraction (~200 lines):
// - GET https://www.youtube.com/watch?v={id} with browser-like User-Agent
// - Regex: `ytInitialPlayerResponse\s*=\s*({.+?})\s*;`
// - JSON path: .captions.playerCaptionsTracklistRenderer.captionTracks[0].baseUrl
// - GET the baseUrl → returns XML: <transcript><text start="0" dur="5.2">Hello</text>...</transcript>
// - Join all <text> elements with timestamps as: "[0:00] Hello\n[0:05] World\n..."

// yt-dlp fallback:
// - exec.CommandContext(ctx, "yt-dlp", "--write-auto-sub", "--sub-lang", "en",
//   "--skip-download", "--sub-format", "json3", "-o", tempFile, url)
// - Parse json3 subtitle format
// - Only used if native fails AND yt-dlp binary exists in PATH
// - 30-second timeout on subprocess

// Edge cases:
// - Age-restricted: return ToolError{Code: "content_empty", Message: "video requires authentication"}
// - Live streams: return ToolError{Code: "content_empty", Message: "live streams not supported"}
// - No captions available: return ToolError{Code: "content_empty", Message: "no transcript available"}
// - Private/deleted: HTTP 404 from watch page → ToolError{Code: "not_found"}
```

---

## MCP Resources

```go
// internal/resources/stats.go

// Resource: stats://tools
// Returns per-tool execution metrics
type ToolStats struct {
    Tools map[string]ToolMetrics `json:"tools"`
}
type ToolMetrics struct {
    TotalCalls   int64   `json:"totalCalls"`
    SuccessCalls int64   `json:"successCalls"`
    ErrorCalls   int64   `json:"errorCalls"`
    CacheHits    int64   `json:"cacheHits"`
    AvgLatencyMs float64 `json:"avgLatencyMs"`
    P95LatencyMs float64 `json:"p95LatencyMs"`
    LastCalled   string  `json:"lastCalled,omitempty"` // RFC3339
}

// Resource: stats://tools/{name}
// Same as above but single tool (URI template)

// Resource: stats://cache
type CacheStats struct {
    MemoryHits     int64   `json:"memoryHits"`
    MemoryMisses   int64   `json:"memoryMisses"`
    DiskHits       int64   `json:"diskHits"`
    DiskMisses     int64   `json:"diskMisses"`
    RedisHits      int64   `json:"redisHits,omitempty"`
    RedisMisses    int64   `json:"redisMisses,omitempty"`
    HitRate        float64 `json:"hitRate"`
    MemoryUsedMB   float64 `json:"memoryUsedMB"`
    DiskUsedMB     float64 `json:"diskUsedMB"`
    EntryCount     int64   `json:"entryCount"`
}

// Resource: stats://events
type EventStats struct {
    TotalEvents      int64  `json:"totalEvents"`
    EventsPerMinute  float64 `json:"eventsPerMinute"`
    OldestEvent      string `json:"oldestEvent"` // RFC3339
    NewestEvent      string `json:"newestEvent"` // RFC3339
}

// Resource: search://recent (per-tenant, isolated)
// Returns the last N search queries for the current tenant
type RecentSearches struct {
    Searches []RecentSearch `json:"searches"`
}
type RecentSearch struct {
    Query     string `json:"query"`
    Tool      string `json:"tool"`
    Timestamp string `json:"timestamp"`
    ResultCount int  `json:"resultCount"`
}
// Max 50 entries, FIFO. Keyed by tenantID. Not persisted across restarts (memory only).
```

## MCP Prompts

```go
// internal/resources/prompts.go

// Prompt: comprehensive-research
// Description: "Guide an AI assistant through a multi-step research process"
// Arguments: topic (string, required), depth (string: "quick"|"standard"|"deep", default: "standard")
// Returns a structured prompt that instructs the assistant to:
//   1. Search broadly, 2. Identify key sources, 3. Scrape top results,
//   4. Cross-reference findings, 5. Identify gaps, 6. Summarize with citations

// Prompt: fact-check
// Description: "Verify a claim using multiple independent sources"
// Arguments: claim (string, required), context (string, optional)
// Returns a prompt that instructs the assistant to:
//   1. Search for the claim, 2. Search for counter-evidence,
//   3. Evaluate source authority, 4. Report confidence level

// Prompt: competitive-analysis
// Description: "Research competitors in a given market"
// Arguments: company (string, required), market (string, optional)
// Returns a prompt guiding: company search, news search, patent search, synthesis

// Prompt: literature-review
// Description: "Systematic review of academic literature on a topic"
// Arguments: topic (string, required), year_from (int, optional), year_to (int, optional)
// Returns a prompt using academic_search + scrape_page systematically
```

---

## .goreleaser.yml

```yaml
version: 2
project_name: web-researcher-mcp

builds:
  - id: server
    main: ./cmd/web-researcher-mcp
    binary: web-researcher-mcp
    env:
      - CGO_ENABLED=0
    goos:
      - linux
      - darwin
      - windows
    goarch:
      - amd64
      - arm64
    ldflags:
      - -s -w -X main.version={{.Version}}

archives:
  - id: default
    format: tar.gz
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    format_overrides:
      - goos: windows
        format: zip

checksum:
  name_template: "checksums.txt"

changelog:
  sort: asc
  filters:
    exclude:
      - "^docs:"
      - "^test:"
      - "^ci:"

dockers:
  - image_templates:
      - "ghcr.io/zoharbabin/web-researcher-mcp:{{ .Version }}"
      - "ghcr.io/zoharbabin/web-researcher-mcp:latest"
    dockerfile: Dockerfile
    build_flag_templates:
      - "--label=org.opencontainers.image.version={{.Version}}"
```

---

## CI Workflow

```yaml
# .github/workflows/ci.yml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  test:
    runs-on: ${{ matrix.os }}
    strategy:
      matrix:
        go-version: ['1.23']
        os: [ubuntu-latest, macos-latest]
    steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: ${{ matrix.go-version }}
    - run: go test -race -coverprofile=coverage.out ./...
    - uses: codecov/codecov-action@v4
      with:
        file: coverage.out
      if: matrix.os == 'ubuntu-latest'

  lint:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: '1.23'
    - uses: golangci/golangci-lint-action@v6
      with:
        version: latest

  vuln:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: '1.23'
    - run: go install golang.org/x/vuln/cmd/govulncheck@latest
    - run: govulncheck ./...

  e2e:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: '1.23'
    - run: go build -o web-researcher-mcp ./cmd/web-researcher-mcp
    - run: go test -v -timeout 120s ./tests/e2e/...
      env:
        GOOGLE_CUSTOM_SEARCH_API_KEY: ${{ secrets.GOOGLE_API_KEY }}
        GOOGLE_CUSTOM_SEARCH_ID: ${{ secrets.GOOGLE_CX }}

  docker:
    runs-on: ubuntu-latest
    needs: [test, lint]
    steps:
    - uses: actions/checkout@v4
    - run: docker build -t web-researcher-mcp:test .

  release:
    runs-on: ubuntu-latest
    needs: [test, lint, vuln, e2e]
    if: startsWith(github.ref, 'refs/tags/v')
    steps:
    - uses: actions/checkout@v4
      with:
        fetch-depth: 0
    - uses: actions/setup-go@v5
      with:
        go-version: '1.23'
    - uses: goreleaser/goreleaser-action@v6
      with:
        version: latest
        args: release --clean
      env:
        GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

---

## Search Lens Behavior (>10 domains)

When a lens has more domains than can fit in a single `site:` query (~10 max):

**Option A (preferred): Pre-configured `cx` engine**
- The lens JSON includes a `"cx": "017..."` field pointing to a Google PSE engine pre-configured with all domains in the Google Console.
- The query is sent without `site:` operators; the engine's configuration handles domain restriction.
- This is the **recommended approach** for lenses like `programming` (15+ domains).

**Option B (fallback): Query splitting**
- If `cx` is empty, the system sends multiple parallel queries, each with up to 10 `site:` operators.
- Results are merged and deduplicated.
- This uses more API quota (2+ queries per search call) but requires no manual PSE configuration.

**Implementation note:** For built-in lenses, we ship a pre-configured `cx` per lens. For user-created custom lenses, Option B is the automatic fallback.

---

## Concurrency Model (Clarification)

```
┌─────────────────────────────────────────────┐
│         Concurrency Limits                   │
│                                              │
│  Global request throughput:    1000 req/s    │
│  Per-tenant rate limit:        30 req/min   │
│  Per-session concurrent tools:  5           │
│                                              │
│  Scraping semaphore (all types): 5 slots    │
│  Browser pool (chromedp only):   3 slots    │
│                                              │
│  Browser slots are INSIDE scraping slots:   │
│  [scrape-1] [scrape-2] [scrape-3]          │
│  [scrape-4/browser-1] [scrape-5/browser-2] │
│  [waiting: browser-3]                       │
│                                              │
│  A browser scrape holds BOTH a scraping     │
│  slot AND a browser slot simultaneously.    │
└─────────────────────────────────────────────┘
```

---

## Admin Endpoints (HTTP Mode)

All admin endpoints require the `X-Admin-Key` header matching `CACHE_ADMIN_KEY` env var. They are separate from OAuth — admin auth is a simple shared secret for operational use.

| Method | Path | Purpose |
|--------|------|---------|
| DELETE | `/admin/cache` | Flush all cache (memory + disk) |
| DELETE | `/admin/sessions` | Kill all active sessions |
| DELETE | `/admin/tenant/{id}` | Purge all data for a tenant |
| GET | `/admin/audit` | Query audit logs (query params: `tenant_id`, `from`, `to`) |

These are NOT listed in the MCP tool surface — they are HTTP-only operational endpoints.

### GDPR Endpoints (HTTP Mode)

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/users/{id}/data` | Export all stored data for a user (GDPR Art. 15) |
| DELETE | `/users/{id}/data` | Purge all user data (GDPR Art. 17) |

Protected by OAuth (user can only access own data) or Admin key (for any user).
