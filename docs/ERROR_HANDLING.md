# Error Handling

How web-researcher-mcp detects, classifies, and reports errors — and why errors are designed to guide LLM clients toward resolution.

---

## Why This Matters

When an AI assistant calls a tool and gets an error, it has three options:
1. Retry blindly (wastes API quota, annoys users)
2. Give up and say "something went wrong" (unhelpful)
3. Understand what failed, explain it clearly, and suggest a next step

This project optimizes for option 3. Every error response tells the LLM client *what category of failure occurred*, *what specifically went wrong*, and *what action to take* — including when to suggest the user file a bug report.

### The GitHub Issue Guidance Pattern

Some errors indicate a limitation in the MCP server itself — sites that should be scrapeable but aren't, providers that return unexpected formats, or content types not yet supported. For these cases, error messages include a direct link to the project's GitHub issues page, prompting the LLM to suggest the user report the problem.

This creates a feedback loop: users encounter real-world edge cases → LLM guides them to report it → maintainers get actionable bug reports with the exact URL and failure mode → the server improves.

The issue link appears **only** for errors where the MCP server could plausibly improve:
- `ErrBlocked` — a commonly-needed site the scraper can't access
- `ErrContent` — a page loaded but yielded no usable text
- `ErrBrowser` — Chrome not available for a JS-heavy site
- General upstream failures that persist across retries

It does **not** appear for:
- `ErrAuth` — login-walled pages (user's problem, not ours)
- `ErrRateLimit` — transient, resolves with time
- `ErrNetwork` — user's connectivity or the remote server is down

---

## Error Layers

Errors are handled at three layers, each with a different audience:

```
┌────────────────────────────────────────────────────┐
│  Layer 1: Scraper errors (internal/scraper/)       │
│  Audience: Server operators (via slog)             │
│  Type: ScrapeError{Kind, Message, Cause, URL, Tier}│
└──────────────────────┬─────────────────────────────┘
                       │
┌──────────────────────▼─────────────────────────────┐
│  Layer 2: Tool-level error mapping (internal/tools/)│
│  Audience: LLM clients (via MCP result)            │
│  Functions: scrapeErrorResponse(), upstreamError-  │
│             Response(), toolError()                 │
└──────────────────────┬─────────────────────────────┘
                       │
┌──────────────────────▼─────────────────────────────┐
│  Layer 3: MCP protocol (IsError: true)             │
│  Audience: MCP client framework                    │
│  Contract: text content with actionable message    │
└────────────────────────────────────────────────────┘
```

---

## Layer 1: Scraper Error Taxonomy

**File:** `internal/scraper/errors.go`

All scrape pipeline errors are typed as `ScrapeError`:

```go
type ScrapeError struct {
    Kind    ErrorKind  // Category (see table below)
    Message string     // Human-readable description
    Cause   error      // Underlying error (for Unwrap)
    URL     string     // The URL that was being scraped
    Tier    string     // Which pipeline tier produced this ("markdown", "stealth", "html", "browser")
}
```

### Error Kinds

| Kind | Constant | Triggers | Tier Examples |
|------|----------|----------|---------------|
| Network | `ErrNetwork` | DNS failure, timeout, connection refused, TLS error | Any tier's HTTP client |
| Blocked | `ErrBlocked` | SSRF protection, domain allowlist, HTTP 403, bot detection | stealth (403), pipeline (allowlist) |
| Browser | `ErrBrowser` | Chrome not found, launch failed, connect failed | browser tier only |
| Content | `ErrContent` | Page loaded but <100 bytes of useful text extracted | All tiers (composite failure) |
| Auth | `ErrAuth` | HTTP 401, login redirect detected | stealth, html |
| Rate Limit | `ErrRateLimit` | HTTP 429 | Any tier's HTTP client |

### Helper Constructors

Each tier uses these to create appropriately-typed errors:

| Function | Creates | Used By |
|----------|---------|---------|
| `networkError(url, tier, cause)` | `ErrNetwork` | All tiers on HTTP failures |
| `blockedError(url, tier, cause, detail)` | `ErrBlocked` | stealth/html on 403, pipeline on allowlist |
| `browserError(url, cause, detail)` | `ErrBrowser` | browser tier on init/launch failure |
| `contentError(url, detail)` | `ErrContent` | Pipeline when all tiers extract nothing |
| `authError(url, tier, statusCode)` | `ErrAuth` | stealth/html on 401 |
| `rateLimitError(url, tier)` | `ErrRateLimit` | Any tier on 429 |

### Classification Functions

| Function | Purpose |
|----------|---------|
| `classifyHTTPStatus(code, url, tier)` | Maps HTTP status codes to the correct ErrorKind |
| `classifyRawError(err, url)` | Wraps any untyped error into a ScrapeError by inspecting the message text |

### Composite Error (All Tiers Failed)

When all pipeline tiers fail, `scrapeWithTieredFallback()` in `internal/scraper/pipeline.go` composes a diagnostic message showing what each tier observed:

```
no content extracted from https://x.com/user/status/123 (markdown: empty, stealth: HTTP 403, html: 12 bytes, browser: chrome launch failed)
```

The composite error's `Kind` is escalated:
- If any tier returned `ErrBlocked`/`ErrAuth`/`ErrRateLimit`/`ErrBrowser` → use that kind
- If all tiers returned `ErrNetwork` → use `ErrNetwork`
- Otherwise → use `ErrContent`

---

## Layer 2: Tool-Level Error Response Functions

**File:** `internal/tools/search.go` (shared), `internal/tools/scrape.go` (scrape-specific)

### Scrape Errors → LLM Messages

**Function:** `scrapeErrorResponse(err error, url string) *mcp.CallToolResult`

Maps `ScrapeError.Kind` to an actionable message for the LLM client:

| Kind | LLM Receives | Includes Issue Link |
|------|-------------|---------------------|
| `ErrBrowser` | "Chrome is not available. Set CHROME_PATH..." | Yes |
| `ErrBlocked` | "access was blocked — site may use bot detection..." | Yes |
| `ErrContent` | "page loaded but no readable content extracted..." | Yes |
| `ErrAuth` | "authentication required — behind a login wall" | No |
| `ErrRateLimit` | "rate limited — try again in 60 seconds" | No |
| `ErrNetwork` | "network error — check connectivity or try again" | No |

### Search/Provider Errors → LLM Messages

**Function:** `upstreamErrorResponse(toolName string, err error) *mcp.CallToolResult`

Detects the error category and formats an appropriate response:

| Detection | LLM Receives |
|-----------|-------------|
| `isRateLimitError(err)` → contains "rate limited", "429", "quota" | "service temporarily busy... wait 60 seconds or try different provider" |
| `isAuthError(err)` → contains "401", "API key not valid", "unauthorized" | "check that the required API key is set... see .env.example" |
| General failure | "failed: {error}... try different provider or report at {issueURL}" |

### Provider Not Found

**Function:** `resolveProvider()`, `resolvePatentSearcher()`, `resolveAcademicSearcher()`

When a user requests an unknown provider:
```
Unknown search provider "foo". Supported providers: google, brave, serper, searxng, searchapi, epo, lens, uspto, openalex, crossref.
```

The provider list is generated dynamically via `allSupportedProviders()` which deduplicates across all three provider lists. No hardcoded list to drift.

When a user requests a known provider that's not configured:
```
Patent provider "epo" is not configured. Set EPO_OPS_CONSUMER_KEY and EPO_OPS_CONSUMER_SECRET. See .env.example for details.
```

### Validation Errors

**Function:** `toolError(msg string) *mcp.CallToolResult`

For input validation failures (missing required params, values out of range):
```
query is required
query must be 500 characters or less
query, assignee, or inventor is required
```

---

## Layer 3: MCP Protocol Contract

All error responses set `IsError: true` on the MCP `CallToolResult`. The text content is always:
- A single natural-language message (not JSON, not a stack trace)
- Written for an LLM to read and relay to the user
- Self-contained: includes what failed, why, and what to do next

Tools never panic. Tools never return Go errors from the handler function (the third return value is always `nil`). All failures are communicated via the MCP result.

---

## Design Principles

### 1. Errors are actionable, not diagnostic

Bad: `"error: HTTP 403"`
Good: `"Scrape failed for https://x.com/post/123: access was blocked (HTTP 403). The site may use bot detection that this scraper cannot bypass. If this is a commonly-needed site, consider reporting at https://github.com/zoharbabin/web-researcher-mcp/issues"`

### 2. Errors are categorized, not strings

The `ErrorKind` enum means tool handlers can switch on category rather than parsing error messages. This keeps the mapping stable even as providers change their error formats.

### 3. Errors flow up, never sideways

```
tier produces ScrapeError → pipeline collects per-tier outcomes → tool handler maps to LLM message
```

Each layer enriches without losing information. The pipeline adds multi-tier diagnostics; the tool handler adds user-facing guidance. Nothing is swallowed.

### 4. The issue link is surgical

The GitHub issue link appears in exactly three places (`scrapeErrorResponse` cases for `ErrBrowser`, `ErrBlocked`, `ErrContent`) and one place in `upstreamErrorResponse` (general upstream failures). These are the only categories where a bug report could lead to an improvement.

### 5. Errors are tested

- `TestAllToolsHaveAnnotations` — CI verifies every tool has proper MCP annotations
- `internal/tools/scrape_errors_test.go` — integration tests for each error kind → LLM message mapping
- `internal/scraper/scraper_test.go` — unit tests for error classification, composite errors, tier propagation

---

## For Contributors: Adding Error Handling to New Code

### In a new scraper tier:
```go
// Wrap HTTP errors with the appropriate kind:
resp, err := client.Do(req)
if err != nil {
    return nil, networkError(url, "my-tier", err)
}
if resp.StatusCode >= 400 {
    return nil, classifyHTTPStatus(resp.StatusCode, url, "my-tier")
}
```

### In a new search provider:
```go
// Use the conventional error message patterns so isRateLimitError/isAuthError detect them:
if resp.StatusCode == 429 {
    return nil, fmt.Errorf("myprovider: rate limited")
}
if resp.StatusCode == 401 {
    return nil, fmt.Errorf("myprovider: authentication failed (check MY_API_KEY)")
}
```

### In a new tool handler:
```go
// Use the shared response functions — never format errors manually:
results, err := provider.Web(ctx, params)
if err != nil {
    return upstreamErrorResponse("my_tool", err), nil, nil
}
```

---

## File Reference

| File | Owns |
|------|------|
| `internal/scraper/errors.go` | `ScrapeError` type, `ErrorKind` enum, helper constructors, classifiers |
| `internal/scraper/pipeline.go` | Composite error assembly (per-tier diagnostics) |
| `internal/tools/scrape.go` | `scrapeErrorResponse()` — maps ScrapeError to LLM message |
| `internal/tools/search.go` | `upstreamErrorResponse()`, `toolError()`, `rateLimitError()`, `isRateLimitError()`, `isAuthError()`, `resolveProvider()`, `resolvePatentSearcher()`, `resolveAcademicSearcher()`, `allSupportedProviders()` |
| `internal/tools/scrape_errors_test.go` | Integration tests for error → response mapping |
| `internal/scraper/scraper_test.go` | Unit tests for error classification |
