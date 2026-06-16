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
    Tier    string     // Which pipeline tier produced this ("markdown", "stealth", "html", "browser", and the optional paid "exa:cached"/"exa:crawled")
}
```

### Error Kinds

| Kind | Constant | Triggers | Tier Examples |
|------|----------|----------|---------------|
| Network | `ErrNetwork` | DNS failure, timeout, connection refused, TLS error | Any tier's HTTP client |
| Validation | `ErrValidation` | Unsupported scheme, empty host, SSRF / private-IP / blocked-hostname denial, domain allowlist | pipeline (validation chokepoint) |
| Blocked | `ErrBlocked` | HTTP 403, remote bot detection (a real site refusing us) | stealth/html (403) |
| Browser | `ErrBrowser` | Chrome not found, launch failed, connect failed | browser tier only |
| Content | `ErrContent` | Page loaded but <100 bytes of useful text extracted | All tiers (composite failure) |
| Auth | `ErrAuth` | HTTP 401, login redirect detected | stealth, html |
| Rate Limit | `ErrRateLimit` | HTTP 429 | Any tier's HTTP client |
| Not Found | `ErrNotFound` | HTTP 404/410 — dead link, resource gone | stealth/html/browser |

`ErrValidation` is distinct from `ErrBlocked` on purpose: a validation/security rejection is a **permanent** client error (the URL itself is invalid or disallowed), so it is **never retryable** and must not be reported as transient bot-detection. `ErrBlocked` is reserved for a real remote site actively refusing the request (HTTP 403 / bot walls), which is retryable from a different source.

### Helper Constructors

Each tier uses these to create appropriately-typed errors:

| Function | Creates | Used By |
|----------|---------|---------|
| `networkError(url, tier, cause)` | `ErrNetwork` | All tiers on HTTP failures |
| `validationError(url, tier, cause, detail)` | `ErrValidation` | Pipeline chokepoint on bad scheme/host, SSRF denial, allowlist |
| `blockedError(url, tier, cause, detail)` | `ErrBlocked` | stealth/html on remote HTTP 403 |
| `browserError(url, cause, detail)` | `ErrBrowser` | browser tier on init/launch failure |
| `contentError(url, detail)` | `ErrContent` | Pipeline when all tiers extract nothing |
| `authError(url, tier, statusCode)` | `ErrAuth` | stealth/html on 401 |
| `rateLimitError(url, tier)` | `ErrRateLimit` | Any tier on 429 |
| `notFoundError(url, tier, statusCode)` | `ErrNotFound` | stealth/html/browser on 404/410 |

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

The composite error's `Kind` is selected by priority — the single highest-priority kind across all tiers wins:
- `ErrValidation` (priority 6) wins unconditionally — a security/validation denial is permanent and must never be downgraded
- Otherwise the highest-priority kind from the remaining tiers: `ErrNotFound` (5) > `ErrAuth` (4) > `ErrRateLimit` (3) > `ErrBlocked` (2) > `ErrBrowser` (1) > `ErrContent` (0)
- If all tiers returned `ErrNetwork` → use `ErrNetwork`

A 404 co-occurring with a bot-block surfaces as `not_found` (priority 5 > 2), not `blocked`.

---

## Layer 2: Structured Error Responses

**Files:** `internal/tools/errors.go` (types + helpers), `internal/tools/search.go`, `internal/tools/scrape.go`

All error responses use a **dual-format** pattern: a natural-language first line (for LLMs and legacy clients) followed by a JSON block with machine-readable metadata (for programmatic parsing).

### Response Format

```
Rate limited (google). Wait 60 seconds and retry, or try a different provider.

{"error":{"kind":"rate_limited","retryable":true,"retryAfterSeconds":60,"suggestedAction":"retry_after_delay","provider":"google"}}
```

### Structured Error Fields (`ToolError` in `internal/tools/errors.go`)

| Field | Type | Description |
|-------|------|-------------|
| `kind` | string | Error category (see enum below) |
| `retryable` | bool | Whether retrying the same call might succeed |
| `retryAfterSeconds` | int (optional) | Seconds to wait before retrying |
| `suggestedAction` | string | Recovery strategy for the LLM |
| `provider` | string (optional) | Which provider failed |
| `alternatives` | []string (optional) | Other available providers |
| `detail` | string (optional) | Technical detail for debugging (secret-masked, see below) |
| `recoveryHint` | object (optional) | Session-recovery guidance, set on `session_not_found`: `{lastKnownStep int, canResume bool}` — lets a client resume or restart deterministically when a follow-up step reaches a pod that doesn't hold the (in-memory) session |

> **Secret masking:** Before any error string reaches an LLM-facing result (or a downstream audit log), it is passed through `audit.MaskSecrets()`. Scrape errors can echo a target URL containing embedded credentials, and upstream provider errors occasionally reflect back an API key (e.g. `?key=AIza...`). `scrapeErrorToToolError()` masks `te.Detail`, `failureFromScrapeError()` masks the failure `reason`, and `upstreamErrorResponse()` masks the upstream detail. As a result, the `detail`/`reason` fields and the human-readable message never expose API keys, tokens, or credentials.

### Error Kind Enum (`ErrorKind`)

| Kind | When | Retryable | Suggested Action |
|------|------|-----------|-----------------|
| `rate_limited` | HTTP 429, quota exceeded | true | `retry_after_delay` |
| `auth_required` | Provider HTTP 401 / invalid API key → `check_api_key`; scrape login wall (`ErrAuth`) → `inform_user` | false | `check_api_key` (provider) or `inform_user` (scrape) |
| `blocked` | HTTP 403, remote bot detection | false | `inform_user` |
| `validation` | Invalid input params, unsupported scheme, SSRF / private-IP / blocked-host / allowlist denial, or a provider-side rejection (`search.InvalidParamsError` — bad `category` / out-of-spec `schema` in `structured_search`) | false | `inform_user` |
| `network` | DNS failure, timeout, connection refused | true | `retry_after_delay` |
| `content_empty` | Page loaded but no text extracted | true | `report_bug` |
| `not_found` | HTTP 404/410 — page does not exist (dead link) | false | `inform_user` |
| `browser_unavailable` | Chrome not found/failed | false | `report_bug` |
| `config` | Unknown/unconfigured provider | false | `try_different_provider` or `check_api_key` |
| `upstream_unavailable` | General provider failure | true | `try_different_provider` |
| `session_not_found` | `sequential_search` follow-up step reached a pod that doesn't hold the (in-memory) session, or the session expired | false | `inform_user` (carries a `recoveryHint` with the last known step) |

### Suggested Action Vocabulary

| Action | LLM Should |
|--------|------------|
| `retry_after_delay` | Wait N seconds, call same tool again |
| `try_different_provider` | Re-call with a different `provider` param |
| `check_api_key` | Tell user to verify API key configuration |
| `broaden_query` | Remove filters or use broader terms |
| `inform_user` | Tell user this content is permanently inaccessible |
| `report_bug` | Suggest user file a GitHub issue |

### Key Functions

| Function | File | Purpose |
|----------|------|---------|
| `structuredError(msg, ToolError)` | errors.go | Builds dual-format error response |
| `scrapeErrorResponse(err, url)` | scrape.go | Maps ScrapeError → structured response |
| `upstreamErrorResponse(toolName, err)` | search.go | Maps provider errors → structured response |
| `resolveProvider()` | search.go | Returns structured error for unknown providers |
| `resolvePatentSearcher()` | search.go | Same for patent providers |
| `resolveAcademicSearcher()` | academic.go | Same for academic providers |
| `toolError(msg)` | search.go | Plain-text validation errors (no JSON block) |

### Validation Errors

**Function:** `toolError(msg string)` — used only for input validation (no structured JSON needed since there's nothing to retry):
```
query is required
query must be 500 characters or less
query, assignee, or inventor is required
```

---

## Layer 3: MCP Protocol Contract

All error responses set `IsError: true` on the MCP `CallToolResult`. The text content contains:
- Line 1: natural-language message (what failed + what to do next)
- Blank line separator
- JSON block: `{"error":{...}}` with machine-readable metadata

`StructuredContent` is always nil on error responses (per MCP spec — SDKs exempt `isError: true` from outputSchema validation).

Tools never panic. Tools never return Go errors from the handler function (the third return value is always `nil`). All failures are communicated via the MCP result.

---

## Layer 4: Session-level Error Aggregation

Layers 1–3 handle a **single** call. Across a multi-step `sequential_search` session, repeated failures of the *same kind* (auth walls, bot blocks, rate limits) are a pattern the LLM should act on — but no single call sees the whole picture. Layer 4 is the cross-call view.

**How it works:**

- Tools that carry a `sessionId` (scrape, academic search, and the `thorough`-depth refinement searches) record a bounded `OutcomeEvent` per call via `trackOutcome` / `trackScrapeOutcome` (`internal/tools/sourcetracker.go`): `{ provider, success, errorKind, url, timestamp }`. Scrape errors map their `ScrapeError.Kind` to the shared `ErrorKind` taxonomy via `mapScrapeErrorKind`, so the cross-call kinds line up with the per-call ones.
- The session layer (`internal/session/outcomes.go`) stores the most-recent **200** events per session (FIFO) — bounded, tenant/user-isolated, honoring the no-unbounded-retention posture.
- `get_research_session` surfaces the aggregation (`internal/session` `AggregateOutcomes`):
  - `errorPatterns` — only when a kind occurs **≥ 3 times** (`ErrorPatternMinCount`, a false-positive guard). Each carries a session-level `suggestion` from the kind→remediation map.
  - `providerStats` — per-provider `{ attempts, successes }`.

**Session-level remediation map** (distinct from the per-call `suggestedAction`):

| Kind | Session-level suggestion |
|------|--------------------------|
| `auth_required` | Consider `open_access=true` or target preprint servers (arxiv, biorxiv). |
| `blocked` | Try alternative sources or use `web_search` for cached versions. |
| `rate_limited` | Switch to a different provider or space requests further apart. |
| `browser_unavailable` | Set `CHROME_PATH` for JavaScript-heavy sites. |
| `network` | Transient network errors — retry, or try a different source. |
| `content_empty` | The page yielded no usable text — try a different source or the original PDF. |
| `upstream_unavailable` | The provider is unavailable — switch providers or retry later. |

Aggregation is **additive** — it never suppresses or alters the per-call errors that callers already receive.

---

## For LLM Agents: Parsing and Recovery

When consuming error responses, LLM agents can use the structured JSON for autonomous recovery:

### Recovery Decision Tree

```
1. Parse JSON block from the error response (after the blank line)
2. Check retryable:
   - true  → check retryAfterSeconds (if present, wait; then retry)
   - false → follow suggestedAction directly
3. Check suggestedAction:
   - "retry_after_delay"      → wait retryAfterSeconds, retry same call
   - "try_different_provider" → re-call with provider set to one from alternatives[]
   - "check_api_key"          → inform user their API key needs configuration
   - "broaden_query"          → remove filters or use broader terms
   - "inform_user"            → tell user this content is inaccessible
   - "report_bug"             → suggest user file a GitHub issue
```

### Zero-Result Responses (Not Errors)

When `resultCount` is 0, patent_search and academic_search include a `hints` object:

```json
{"resultCount": 0, "hints": {"reason": "coverage_miss", "suggestedActions": [{"action": "switch_provider", "value": "lens"}]}}
```

### Partial Success (search_and_scrape)

The `status` field tells you immediately: `"complete"`, `"partial"`, or `"failed"`. On `"partial"`, check `scrapeFailures[]` for per-URL recovery options.

---

## Design Principles

### 1. Errors are actionable, not diagnostic

Bad: `"error: HTTP 403"`
Good:
```
Blocked: x.com uses bot detection. Try an alternative source — its content can't be read directly.

{"error":{"kind":"blocked","retryable":false,"suggestedAction":"inform_user","detail":"access blocked: HTTP 403"}}
```

### 2. Errors are categorized, not strings

The `ErrorKind` enum means tool handlers can switch on category rather than parsing error messages. This keeps the mapping stable even as providers change their error formats.

### 3. Errors flow up, never sideways

```
tier produces ScrapeError → pipeline collects per-tier outcomes → tool handler maps to LLM message
```

Each layer enriches without losing information. The pipeline adds multi-tier diagnostics; the tool handler adds user-facing guidance. Nothing is swallowed.

### 4. The issue link is surgical

The GitHub issue link appears in exactly two places (`scrapeErrorResponse` cases for `ErrBrowser`, `ErrContent`) and one place in `upstreamErrorResponse` (general upstream failures). These are the only categories where a bug report could lead to an improvement.

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
    // isAuthError matches any of: "401", "API key not valid", "unauthorized", "INVALID_ARGUMENT"
    return nil, fmt.Errorf("myprovider: 401 unauthorized")
}
```

### In a new tool handler:
```go
// Use the shared response functions — never format errors manually:
results, err := provider.Web(ctx, params)
if err != nil {
    return upstreamErrorResponse("my_tool", err), nil, nil
}

// For validation errors (no structured JSON needed):
if input.Query == "" {
    return toolError("query is required"), nil, nil
}
```

---

## File Reference

| File | Owns |
|------|------|
| `internal/tools/errors.go` | `ToolError` struct, `ErrorKind`/`SuggestedAction` enums, `structuredError()`, `FailureInfo`, `ZeroResultHints`, cache freshness helpers |
| `internal/scraper/errors.go` | `ScrapeError` type, scraper `ErrorKind` enum, helper constructors, classifiers |
| `internal/scraper/pipeline.go` | Composite error assembly (per-tier diagnostics) |
| `internal/tools/scrape.go` | `scrapeErrorResponse()`, negative cache helpers |
| `internal/session/outcomes.go` | Session-level outcome log + `AggregateOutcomes()`, kind→remediation map, `ErrorPatternMinCount` |
| `internal/tools/sourcetracker.go` | `trackOutcome()` / `trackScrapeOutcome()` — record per-call outcomes onto a session |
| `internal/tools/search.go` | `upstreamErrorResponse()`, `toolError()`, `rateLimitError()`, resolver functions, `allSupportedProviders()` |
| `internal/tools/scrape_errors_test.go` | Integration tests for error → response mapping |
| `internal/scraper/scraper_test.go` | Unit tests for error classification |
