# Tool Specifications

These tools let your AI assistant search the web, read pages, find academic papers, track multi-step research, and more — always returning real, verifiable sources. Below are the detailed schemas and behavioral contracts for each tool.

> **Note:** Output schemas describe the JSON shape returned by each tool. See the corresponding `internal/tools/*.go` file for the implementation. Input schemas are auto-generated from struct `jsonschema` tags.

## Tool Registration Pattern

Each tool follows the pattern in `internal/tools/registry.go`: a typed input struct with `jsonschema` tags (the SDK auto-generates JSON Schema from these) and a `register*` function that calls `mcp.AddTool`. See `internal/tools/search.go` for a representative example.

## Cache-Key Contract

**Every result-affecting parameter MUST be included in a tool's cache key.** This single rule prevents an entire class of cache-collision bugs — two requests that would produce different results must never share a key (e.g. different providers, or a smaller `max_length` that would serve a later larger request a truncated body).

- Canonical implementations: `searchCacheKey(...)` (`internal/tools/search.go`) for the search family — keys on the tool name plus every query parameter **including `provider`** — and `scrapeCacheKey(url, mode, maxLength)` (`internal/tools/scrape.go`) for scrapes.
- Each key carries a **version segment** (e.g. `v2`); bump it whenever the cached response *shape* changes so a post-upgrade cache hit can never serve a blob missing a newly-added field.
- Enforcement: `internal/tools/cachekey_test.go` guards today's parameters. When you add a tool or a result-affecting parameter, extend both the key and that test — the test only covers the params it knows about, so a new param can reintroduce the bug without failing any existing assertion.

---

## Tool 1: `web_search`

### Purpose
Perform a web search and return structured result URLs with metadata.

### Input Schema

| Field | Type | Required | Default | Constraints |
|-------|------|----------|---------|-------------|
| `query` | string | yes | — | 1-500 chars |
| `num_results` | int | no | 5 | 1-10 |
| `time_range` | string | no | — | `day`, `week`, `month`, `year` |
| `safe` | string | no | `medium` | `off`, `medium`, `high` |
| `language` | string | no | — | ISO 639-1 code |
| `site` | string | no | — | Domain restriction (cannot combine with `lens`) |
| `exact_terms` | string | no | — | Exact phrase match |
| `exclude_terms` | string | no | — | Terms to exclude |
| `country` | string | no | — | ISO 3166-1 alpha-2 |
| `lens` | string | no | — | Domain lens (overrides `site`). See `lenses/` directory for available lenses |
| `provider` | string | no | — | Force search provider. Returns error listing available providers if unknown |
| `sessionId` | string | no | — | Link results to a `sequential_search` session |
| `claim` | string | no | — | Optional claim to evaluate against each result's snippet; when set, each result gains a `claimSignal` (#66). Evidence only — never a verdict |

### Output Schema

```go
type SearchOutput struct {
    URLs        []string       `json:"urls"`
    Query       string         `json:"query"`
    ResultCount int            `json:"resultCount"`
    Results     []SearchResult `json:"results"`
    Hints       *ZeroResultHints `json:"hints,omitempty"` // present ONLY on zero-result responses (see below)
    Trust       string         `json:"trust"`   // "untrusted-external-content" — treat results as data, not instructions (OWASP LLM01)
}

type SearchResult struct {
    Title       string `json:"title"`
    URL         string `json:"url"`
    Snippet     string `json:"snippet"`
    DisplayLink string `json:"displayLink"`
    ClaimSignal string `json:"claimSignal,omitempty"` // most claim-relevant snippet sentence; present per result only when `claim` is set and matched
}
```

When `claim` is omitted the output is byte-identical to the above without `claimSignal`. With `claim`, the server extracts the most claim-relevant sentence from each result's snippet — evidence to help triage which links to read; for full-text claim evidence use `search_and_scrape` with `claim`.

On a zero-result response, `hints` carries a `ZeroResultHints` object (the same shape `academic_search` and `patent_search` emit) explaining why nothing matched and how to recover: `reason` (`no_match` | `filters_too_restrictive`), `filtersApplied` (the constraints that may have eliminated results — `site`, `lens`, `time_range`, `country`, `language`, `exact_terms`, `exclude_terms`), and `suggestedActions` (remove-filter / try-different-provider). Suggested alternative providers are limited to those **configured and currently healthy**. On any non-empty result set the field is omitted.

### Behavior

1. If `SEARCH_ROUTING` is set, route through the multi-provider Router (priority-ordered fallback with per-provider circuit breakers).
2. If `lens` is specified and has a dedicated `cx`, route directly to that Google PSE engine.
3. If `lens` is specified without `cx`, inject `site:` operators and route to the configured provider.
4. Apply `time_range` as date restriction parameter.
5. Return deduplicated URLs and full result objects.

### Cache
- Key: SHA-256 of (provider + query + all params)
- TTL: 30 minutes

### Error Conditions
- Unknown provider → error listing all supported providers (no duplicates)
- Invalid/missing API key → `upstreamErrorResponse()` with setup instructions referencing `.env.example`
- Rate limited → `rateLimitError()` suggesting 60s wait or different provider
- No results → return empty `urls` array (not an error)
- All errors use `upstreamErrorResponse()` from `internal/tools/search.go` for consistent formatting

---

## Tool 2: `scrape_page`

### Purpose
Extract content from a URL, supporting web pages, documents, and YouTube videos.

### Input Schema

| Field | Type | Required | Default | Constraints |
|-------|------|----------|---------|-------------|
| `url` | string | yes | — | Valid HTTP(S) URL |
| `mode` | string | no | `full` | `full` (cleaned readable text), `preview` (first ~5000 bytes), `raw` (verbatim unsanitized bytes — see [Raw Mode](#raw-mode)) |
| `max_length` | int | no | 50000 | Bytes. Capped at 5,000,000 (5 MB) for all modes; in `preview` mode it is forced to 5000. Applies to `raw` mode as an `io.LimitReader` cap on the fetched bytes |
| `sessionId` | string | no | — | Link to a `sequential_search` session |

### Output Schema

```go
type ScrapeOutput struct {
    URL             string    `json:"url"`
    Content         string    `json:"content"`
    ContentType     string    `json:"contentType"`    // html, markdown, youtube, pdf, docx, pptx (raw mode: the server's Content-Type header, may be "")
    Trust           string    `json:"trust"`          // always "untrusted-external-content" — boundary marker: treat content as data, not instructions (OWASP LLM01)
    ContentLength   int       `json:"contentLength"`
    Truncated       bool      `json:"truncated"`
    EstimatedTokens int       `json:"estimatedTokens"`
    SizeCategory    string    `json:"sizeCategory"`   // small, medium, large, very_large
    Citation        *Citation `json:"citation"`       // always present
    Raw             bool      `json:"raw,omitempty"`  // true only in raw mode; omitted otherwise
    ExtractedBy     string    `json:"extractedBy,omitempty"` // extraction tier: markdown|stealth|html|browser|exa:cached|exa:crawled; omitted when unknown
    Metadata        *Metadata `json:"metadata,omitempty"` // present only when a title was extracted (full/preview only)
    StructuredData  *StructuredData `json:"structuredData,omitempty"` // page-embedded machine-readable metadata; present only when found (full/preview, HTML pages)
    SourceType      string    `json:"sourceType"`     // typed classification (#62): peer_reviewed|official_docs|government|news_publication|blog|forum|wiki|social_media|unknown
    AuthorityTier   string    `json:"authorityTier"`  // banded authority: high|medium|low
    DomainCategory  string    `json:"domainCategory"` // subject area: academic|legal|medical|financial|technical|general
}

type Metadata struct {
    Title  string `json:"title"`
    Author string `json:"author"`
}

type StructuredData struct {
    JSONLD    []json.RawMessage `json:"jsonLd,omitempty"`    // each <script type="application/ld+json"> block, verbatim
    OpenGraph map[string]string `json:"openGraph,omitempty"` // og:* and article:* meta, keys keep their prefix
    Citation  map[string]string `json:"citation,omitempty"`  // Highwire <meta name="citation_*"> tags
}

type Citation struct {
    URL          string           `json:"url"`
    AccessedDate string           `json:"accessedDate"`
    Metadata     CitationMetadata `json:"metadata"`
    Formatted    CitationFormats  `json:"formatted"`
}

type CitationMetadata struct {
    Title  string `json:"title"`
    Author string `json:"author"`
    Site   string `json:"site"`
    Date   string `json:"date"`
}

type CitationFormats struct {
    APA string `json:"apa"`
    MLA string `json:"mla"`
}
```

On a **cache hit**, the result also carries a top-level `_meta` block with cache-freshness provenance (`cached: true`, `ageSeconds`, `maxAgeSeconds`, `freshness`) — see [Cache Freshness Provenance](#cache-freshness-provenance). Freshly fetched scrapes have no `_meta`.

In `raw` mode the output additionally carries `"raw": true`, and `contentType` is the server's real `Content-Type` header (it may be empty). No `metadata` block is emitted.

**Tables in content (#48).** HTML `<table>` elements are rendered as GitHub-flavored markdown pipe tables inside `content` (header row + `---` separator + data rows), preserving row/column structure instead of flattening cells into disconnected fragments. Pipe characters in cells are escaped and multi-line cells are collapsed to a single row. Layout, malformed, single-column, and nested tables degrade gracefully to plain text — never an error, never a panic.

**Structured data (#46).** When the page embeds machine-readable metadata, the response carries a `structuredData` object alongside `content`: `jsonLd` (each `<script type="application/ld+json">` block, kept verbatim — invalid JSON is skipped, never failing the scrape), `openGraph` (`og:*`/`article:*` meta, keys keep their prefix), and `citation` (Highwire `citation_*` meta — DOI, authors, journal). The whole object is omitted when no such markup is present, and each sub-field is omitted when empty. It is produced by the HTML-extraction tiers only (absent for `raw` mode, PDFs, YouTube, and markdown-tier results), is independently size-bounded so a pathological page cannot blow the response budget, and is **untrusted external data** under the same trust boundary as `content`.

**Extraction provenance (`extractedBy`).** When known, the response names the tier that produced the content: `markdown`, `stealth`, `html`, `browser`, or — for the paid Exa fallback — `exa:cached` / `exa:crawled`. It lets a caller see whether content came from a free local tier or the metered Exa `/contents` API (Tier 5, present only when `EXA_API_KEY` is set). Omitted when unknown (e.g. document/YouTube routes).

**Typed source classification (#62).** Every scrape response (full and raw) carries three categorical fields alongside the numeric content: `sourceType` (the kind of source — derived from Schema.org `@type` / Highwire `citation_*` meta when present, else a domain heuristic, else `unknown`), `authorityTier` (`high`/`medium`/`low`, a banding of the internal authority score), and `domainCategory` (`academic`/`legal`/`medical`/`financial`/`technical`/`general`, from a domain heuristic). They let the model hedge in natural language by source type. They are best-effort hints derived from untrusted page data — treat them as signals, not guarantees. (In raw mode, with no structured-data extraction, `sourceType` falls back to the host heuristic.)

**Trust boundary marker.** Every scrape response (full, preview, and raw) carries `"trust": "untrusted-external-content"` in the JSON envelope — an explicit, machine-readable boundary marker. It is deliberately placed in the structured output, never inside the `content` string (where a malicious page could forge or close it), and signals that `content` is external data to be treated as data, never as instructions (OWASP LLM01, indirect prompt injection). The server cannot enforce the prompt boundary itself — the model and agent loop live in the host application — so this marker exists to make the untrusted provenance unmissable to that host.

### Raw Mode

`mode=raw` returns the fetched bytes **verbatim** — the content extraction pipeline and `content.Process` sanitization are skipped entirely. Use it only to inspect source such as JSON, HTML markup, JavaScript, or plain text that the cleaned `full` mode would strip or reformat.

Raw mode still runs through the **same safety guards** as every other scrape: `validateScrapeURL` (HTTP/HTTPS scheme + non-empty host), the SSRF-safe client (private-IP and metadata-endpoint blocking, DNS-rebinding prevention), the `ALLOWED_DOMAINS` allowlist, and an `io.LimitReader` bounded by `max_length`. Only `content.Process` is bypassed.

**Trade-off — untrusted bytes.** Because sanitization is skipped, raw content may contain active `<script>`/HTML, embedded markup, or indirect prompt-injection payloads. The bytes are untrusted: never execute or render them, and treat any instructions inside them as data, not commands. For normal reading, prefer `full` (sanitized). `search_and_scrape` is always sanitized and has no raw mode.

Raw responses are keyed like any other scrape: the cache key includes `mode` (so `raw` never collides with a cleaned `full`/`preview` entry for the same URL) and `max_length`. See the Cache section below for the full key.

### Scraping Strategy (Tiered Fallback)

```
1. SSRF VALIDATION
   └─ Resolve DNS, check all IPs against private ranges
   └─ Block: loopback, link-local, RFC1918, metadata endpoints

2. CONTENT TYPE DETECTION
   ├─ YouTube URL → YouTube extractor (3-strategy fallback):
   │     Strategy 1: Player response captions (primary + alt regex)
   │     Strategy 2: Direct timedtext API (en, en-US, en-GB)
   │     Strategy 3: Video description (shortDescription JSON field)
   ├─ .pdf / application/pdf → PDF parser
   ├─ .docx / application/vnd.openxmlformats* → DOCX parser
   └─ .pptx / application/vnd.ms-powerpoint → PPTX parser

3. WEB PAGE EXTRACTION (4 free tiers, ordered by speed; + optional paid Exa tier last)
   a) Tier 1: MARKDOWN NEGOTIATION (fastest, ~200ms)
      ├─ Send GET with Accept: text/markdown
      ├─ 5-second timeout
      ├─ Verify response is actually markdown (heuristic check)
      └─ If content-type mismatch or too short → next tier

   b) Tier 2: STEALTH HTTP CLIENT (fast, ~300ms)
      ├─ Browser-like TLS fingerprint (TLS 1.2+, HTTP/2)
      ├─ Full Chrome 131 headers (User-Agent, Sec-Ch-Ua, Sec-Fetch-*)
      ├─ Parse with goquery (article > [role=main] > main > body)
      ├─ Remove: script, style, nav, footer, aside, ads, popups
      ├─ SSRF protection via safe dialer when AllowPrivateIPs=false
      └─ If below 100-char threshold → next tier

   c) Tier 3: HTML EXTRACTION via goquery (standard, ~500ms)
      ├─ Fetch page with standard Accept header
      ├─ Parse with goquery
      ├─ Extract: article > main > body (priority order)
      ├─ Remove: script, style, nav, footer, aside, ads
      ├─ Minimum content: 100 bytes, 10% meaningful text ratio
      └─ If below threshold → next tier

   d) Tier 4: HEADLESS BROWSER via go-rod + stealth (slow, ~5s)
      ├─ Browser pool with lazy init + singleton pattern
      ├─ go-rod/stealth plugin (navigator spoofing, WebGL masking)
      ├─ Used for: Known SPA domains, JS-rendered content, bot challenges
      ├─ Wait for: page stability (2s for SPA domains, 500ms otherwise) OR 30s timeout
      ├─ Extract: rendered DOM via JavaScript evaluation
      └─ Graceful cleanup via Pipeline.Close()

   e) Tier 5: EXA /contents (PAID, opt-in, last resort) — only when EXA_API_KEY is set
      ├─ Neural extractor: POST https://api.exa.ai/contents (x-api-key auth)
      ├─ Runs ONLY after every free tier above failed to extract >100 bytes,
      │  so the common path never incurs Exa cost
      ├─ Recovers bot-blocked / JS-heavy pages the local tiers cannot
      └─ Records provenance into extractedBy: "exa:cached" (served from Exa's
         cache) or "exa:crawled" (freshly fetched by Exa)

4. CONTENT PROCESSING
   ├─ Sanitize: strip hidden text, zero-width chars, dangerous patterns
   ├─ Truncate: at paragraph/sentence boundary if > max_length
   ├─ Estimate tokens: length / 4
   └─ Extract citation: from <meta> tags, URL, response headers
```

### Known SPA Domains (require headless browser)
- patents.google.com, scholar.google.com, news.google.com
- trends.google.com, twitter.com, x.com
- linkedin.com, facebook.com, instagram.com
- medium.com, dev.to

### Cache
- Key: SHA-256 of (`url` + `mode` + `max_length`) — `max_length` is part of the key so a larger request never serves a shorter cached body
- TTL: 1 hour

### Error Taxonomy (`internal/scraper/errors.go`)

All scrape errors are typed as `ScrapeError{Kind, Message, Cause, URL, Tier}`. The `scrapeErrorResponse()` function in `internal/tools/scrape.go` maps each kind to an actionable LLM-facing message:

| ErrorKind | Retryable | Trigger | LLM Message (verbatim shape from `scrape.go`) |
|-----------|-----------|---------|-----------------------------------------------|
| `ErrValidation` | no (permanent) | Unsupported scheme, empty host, SSRF / private-IP / blocked-hostname denial, domain allowlist denial | "URL rejected for {url}: {detail}. Provide a valid public http(s) URL." |
| `ErrNetwork` | yes | DNS failure, timeout, connection refused, TLS | "Network error on {url}: {detail}. Check connectivity." |
| `ErrBlocked` | yes | HTTP 403, remote bot detection | "Blocked: {url} uses bot detection. Try alternative source or report at {issueURL}" |
| `ErrBrowser` | no | Chrome not found, launch failed, connect failed | "Scrape failed: Chrome unavailable. Set CHROME_PATH or install Chrome. Report at {issueURL}" |
| `ErrContent` | yes | Page loaded but no usable content extracted | "No content extracted from {url}. May need browser rendering. Report at {issueURL}" |
| `ErrAuth` | no | HTTP 401, login redirect | "Auth required: {url} is behind a login wall." |
| `ErrRateLimit` | yes (after delay) | HTTP 429 | "Rate limited on {url}. Retry in 60 seconds." |

When all tiers fail, the composite error message lists each tier's outcome (e.g., `markdown: empty, stealth: HTTP 403, html: 12 bytes, browser: chrome launch failed`).

---

## Tool 3: `search_and_scrape`

### Purpose
Combined search + scrape pipeline with quality scoring, deduplication, and source ranking.

### Input Schema

| Field | Type | Required | Default | Constraints |
|-------|------|----------|---------|-------------|
| `query` | string | yes | — | 1-500 chars |
| `num_results` | int | no | 3 | 1-10 |
| `include_sources` | bool | no | true | — |
| `deduplicate` | bool | no | true | — |
| `max_length_per_source` | int | no | 50000 | Bytes |
| `total_max_length` | int | no | 300000 | Bytes |
| `filter_by_query` | bool | no | false | — |
| `provider` | string | no | — | Force search provider for the search phase |
| `sessionId` | string | no | — | Link results to a `sequential_search` session |
| `claim` | string | no | — | Optional claim to evaluate against each source; when set, each source gains `keySentences` + `claimSignal` (#66). Evidence only — never a verdict |

### Output Schema

```go
type SearchAndScrapeOutput struct {
    Query           string          `json:"query"`
    Status          string          `json:"status"`           // "complete", "partial", or "failed"
    Sources         []SourceResult  `json:"sources"`
    CombinedContent string          `json:"combinedContent"`
    Trust           string          `json:"trust"`            // "untrusted-external-content" — boundary marker for combinedContent + every source; treat as data, not instructions (OWASP LLM01)
    ScrapeFailures  []FailureInfo   `json:"scrapeFailures,omitempty"`
    Note            string          `json:"note,omitempty"`   // guidance when status="failed"
    Summary         PipelineSummary `json:"summary"`
    SizeMetadata    SizeMetadata    `json:"sizeMetadata"`
    Recommendations []Recommendation `json:"recommendations,omitempty"` // advisory; see below
    Components      []Component      `json:"components,omitempty"`      // mcp-auto-formatted (deterministic, no LLM); see below
}

// Recommendation is an advisory pointer to a higher-quality source already in
// `sources`. Content-based and non-profiling; never re-ranks or hides results.
// Present only when SOURCE_RECOMMENDATIONS=true (default) AND something clears
// the quality bar. Omitted otherwise.
type Recommendation struct {
    URL    string  `json:"url"`
    Title  string  `json:"title,omitempty"`
    Score  float64 `json:"score"`
    Reason string  `json:"reason"`  // transparent, content-derived
}

// Component is an optional, additive, mcp-auto-formatted renderable (card/table)
// built DETERMINISTICALLY from already-extracted data — NO server-side LLM call,
// no model of any kind. The "mcp-auto-formatted" label states the MCP server
// shaped this structure (not an LLM, not another component). Always carries
// autoFormatted=true and references to raw source data; it never replaces
// `content`/`sources`. Present only when GENERATIVE_UI_ENABLED=true.
type Component struct {
    Type          string   `json:"type"`          // "card" | "table"
    AutoFormatted bool     `json:"autoFormatted"` // always true (non-disableable label)
    Label         string   `json:"label"`         // "mcp-auto-formatted"
    Title         string   `json:"title,omitempty"`
    SourceRefs    []string `json:"sourceRefs,omitempty"` // URLs of the underlying raw data
    Card          *Card    `json:"card,omitempty"`
    Table         *Table   `json:"table,omitempty"`
}

type SourceResult struct {
    URL            string        `json:"url"`
    Title          string        `json:"title,omitempty"`
    Content        string        `json:"content"`
    ContentType    string        `json:"contentType"`
    Trust          string        `json:"trust"`        // "untrusted-external-content" (see top-level Trust)
    Scores         *QualityScore `json:"scores,omitempty"`
    SourceType     string        `json:"sourceType,omitempty"`     // typed classification (#62): peer_reviewed|official_docs|government|news_publication|blog|forum|wiki|social_media|unknown
    AuthorityTier  string        `json:"authorityTier,omitempty"`  // high|medium|low
    DomainCategory string        `json:"domainCategory,omitempty"` // academic|legal|medical|financial|technical|general
    ClaimSignal    string        `json:"claimSignal,omitempty"`    // strongest claim-relevant sentence; present only when `claim` is set and matched (#66)
    KeySentences   []string      `json:"keySentences,omitempty"`   // top claim-relevant sentences in document order; present only with `claim`
}

type FailureInfo struct {
    URL             string `json:"url"`
    Kind            string `json:"kind,omitempty"`            // error category (blocked, auth_required, etc.)
    Reason          string `json:"reason"`
    Retryable       bool   `json:"retryable"`
    SuggestedAction string `json:"suggestedAction,omitempty"` // recovery hint
}

type QualityScore struct {
    Overall        float64 `json:"overall"`
    Relevance      float64 `json:"relevance"`
    Freshness      float64 `json:"freshness"`
    Authority      float64 `json:"authority"`
    ContentQuality float64 `json:"contentQuality"`
}

type PipelineSummary struct {
    URLsSearched     int `json:"urlsSearched"`
    URLsScraped      int `json:"urlsScraped"`
    URLsFailed       int `json:"urlsFailed"`
    ProcessingTimeMs int `json:"processingTimeMs"`
}
```

### Behavior

1. Execute search (via configured provider)
2. Scrape all result URLs in parallel (bounded concurrency: 5)
3. If `deduplicate`: paragraph-level djb2 hashing, drop blocks whose hash exactly matches one already seen (exact-match dedup, not fuzzy similarity)
4. Score and rank sources by quality (weighted: relevance 35%, freshness 20%, authority 25%, content 20%)
5. If `filter_by_query`: extract keywords, remove sources below relevance threshold
6. Combine content, truncate to `total_max_length`
7. Return structured result with scores and metadata
8. Optionally append `recommendations` (advisory, content-based; `SOURCE_RECOMMENDATIONS`, default on) and `components` (`mcp-auto-formatted` renderables, deterministic — no LLM; `GENERATIVE_UI_ENABLED`, default off) — both derived purely from the quality scores already computed, with no extra scoring pass and no model call

### Recommendations & components (additive)

- **`recommendations`** surface the highest-quality related sources from the *current* result set using the transparent quality signals (authority, relevance, freshness, content). They are **advisory only** — `sources` ordering is never changed and the caller can ignore them. Strictly content-based: no user-behavior inputs, no profiling. Toggle with `SOURCE_RECOMMENDATIONS` (default `true`). Behavior-based/personalized ranking is explicitly out of scope.
- **`components`** are optional renderable structures (source cards, a quality-comparison table) assembled **deterministically** from data already extracted — there is no server-side LLM call and no model of any kind. Every component is labelled `autoFormatted: true` / `"mcp-auto-formatted"` (stating the MCP server shaped it, not an LLM) and references the raw source URLs, so nothing is hidden or unverifiable. Off by default (`GENERATIVE_UI_ENABLED=false`); when off, output is byte-for-byte unchanged. The raw `content`/`sources` are always present regardless.

### Cache
- NOT cached as a whole (composed of cached sub-operations)
- Individual search and scrape results are cached per their own TTLs

---

## Tool 4: `image_search`

### Input Schema

| Field | Type | Required | Default | Constraints |
|-------|------|----------|---------|-------------|
| `query` | string | yes | — | 1-500 chars |
| `num_results` | int | no | 5 | 1-10 |
| `size` | string | no | — | huge, icon, large, medium, small, xlarge, xxlarge |
| `type` | string | no | — | clipart, face, lineart, stock, photo, animated |
| `color_type` | string | no | — | color, gray, mono, trans |
| `dominant_color` | string | no | — | black, blue, brown, gray, green, orange, pink, purple, red, teal, white, yellow |
| `file_type` | string | no | — | jpg, gif, png, bmp, svg, webp |
| `safe` | string | no | `medium` | off, medium, high |
| `provider` | string | no | — | Force search provider |

### Output Schema

```go
type ImageSearchOutput struct {
    Images      []ImageResult `json:"images"`
    Query       string        `json:"query"`
    ResultCount int           `json:"resultCount"`
    Trust       string        `json:"trust"`   // "untrusted-external-content"
}

type ImageResult struct {
    Title         string `json:"title"`
    Link          string `json:"link"`
    ThumbnailLink string `json:"thumbnailLink,omitempty"`
    DisplayLink   string `json:"displayLink"`
    ContextLink   string `json:"contextLink,omitempty"`
    Width         int    `json:"width,omitempty"`
    Height        int    `json:"height,omitempty"`
    FileSize      string `json:"fileSize,omitempty"`
}
```

### Provider notes
- Filters (`type`, `color_type`, `dominant_color`, `file_type`) are passed to the provider's image API. The `size` bucket is a hint the provider applies loosely — returned dimensions may not strictly match the requested bucket. Use the `width`/`height` fields to filter precisely when exact sizing matters.

### Cache
- Key: SHA-256 of (query + all filter params)
- TTL: 30 minutes

---

## Tool 5: `news_search`

### Input Schema

| Field | Type | Required | Default | Constraints |
|-------|------|----------|---------|-------------|
| `query` | string | yes | — | 1-500 chars |
| `num_results` | int | no | 5 | 1-10 |
| `freshness` | string | no | `week` | hour, day, week, month, year |
| `sort_by` | string | no | `relevance` | relevance, date |
| `news_source` | string | no | — | Domain filter |
| `provider` | string | no | — | Force search provider |
| `sessionId` | string | no | — | Link results to a `sequential_search` session |

### Output Schema

```go
type NewsSearchOutput struct {
    Articles    []NewsArticle `json:"articles"`
    Query       string        `json:"query"`
    ResultCount int           `json:"resultCount"`
    Hints       *ZeroResultHints `json:"hints,omitempty"` // present ONLY on zero-result responses (see below)
    Trust       string        `json:"trust"`   // "untrusted-external-content"
}

type NewsArticle struct {
    Title       string `json:"title"`
    URL         string `json:"url"`
    Source      string `json:"source"`
    PublishedAt string `json:"publishedAt,omitempty"`
    Snippet     string `json:"snippet"`
}
```

On a zero-result response, `hints` carries the same `ZeroResultHints` object as `web_search`/`academic_search`/`patent_search`. The active `freshness` window (default `week`) and any `news_source` are surfaced in `filtersApplied`, since an over-narrow recency window is the most common reason news returns nothing; suggested alternative providers are limited to those configured and healthy. Omitted on any non-empty result set.

### Behavior

1. Route to configured search provider's news endpoint.
2. Apply `freshness` as date restriction.
3. If `news_source` specified, add as domain filter.
4. Sort by `sort_by`: `relevance` (default) uses the provider's native ranking; `date` requests newest-first ordering.
5. Return deduplicated articles.

### Provider notes
- `publishedAt` is populated when the provider exposes a publish timestamp (Google CSE via page metadata; Brave/Serper/SearchAPI/SearXNG natively). It is omitted (not fabricated) when the provider does not supply one, so treat it as best-effort.
- `sort_by=date` maps to each provider's date-sort control; exact ordering and `freshness=hour` granularity depend on the provider's index and may be approximate. News providers may also surface high-ranking forum/aggregator pages — `news_source` narrows to a trusted outlet when that matters.

### Cache
- TTL: 15 minutes (news is time-sensitive)

---

## Tool 6: `academic_search`

### Input Schema

| Field | Type | Required | Default | Constraints |
|-------|------|----------|---------|-------------|
| `query` | string | yes | — | 1-500 chars |
| `num_results` | int | no | 5 | 1-10 |
| `year_from` | int | no | — | 1900-2030 |
| `year_to` | int | no | — | 1900-2030 |
| `source` | string | no | `all` | all, arxiv, pubmed, ieee, nature, springer |
| `pdf_only` | bool | no | false | — |
| `sort_by` | string | no | `relevance` | relevance, date |
| `open_access` | bool | no | false | Only return open-access papers |
| `provider` | string | no | — | Force provider: openalex, crossref, semanticscholar, exa (academic APIs), or google, brave, serper, searxng, searchapi, duckduckgo, tavily (web fallback) |
| `sessionId` | string | no | — | Link results to a `sequential_search` session; sources are auto-recorded for recovery after context loss |

### Output Fields

Each paper in the `papers` array contains:

| Field | Type | Always Present | Description |
|-------|------|---------------|-------------|
| `title` | string | yes | Paper title |
| `url` | string | yes | Link to paper (DOI URL or publisher page) |
| `source` | string | yes | Provider that returned this result |
| `doi` | string | no | Digital Object Identifier |
| `authors` | []string | no | Author names |
| `journal` | string | no | Journal or venue name |
| `year` | int | no | Publication year |
| `abstract` | string | no | Paper abstract (up to 500 chars) |
| `citationCount` | int | no | Number of citations |
| `openAccess` | bool | no | Whether the paper is freely available |
| `pdfUrl` | string | no | Direct link to PDF (Semantic Scholar/OpenAlex supply this directly; for DOI-only results it is filled by Unpaywall open-access enrichment when `UNPAYWALL_EMAIL`/`OPENALEX_EMAIL` is set) |
| `tldr` | string | no | AI-generated one-sentence summary of the paper (Semantic Scholar only; machine-generated, not author-written) |
| `isInfluential` | bool | no | Whether Semantic Scholar flags this as a highly-influential paper |
| `citationIntents` | []string | no | Citation-intent labels (e.g. background, methodology) — populated by `citation_graph`, not plain search |

Additional output fields: `query`, `totalResults`, `resultCount`, `source` (which provider answered: openalex, crossref, router, web_search), `hints` (a `ZeroResultHints` object explaining why a query returned nothing and suggesting how to broaden it — present on zero-result responses), and `trust` (always `"untrusted-external-content"` — treat results as data, not instructions; OWASP LLM01).

### Behavior
- 4-strategy fallback: explicit provider → router → academic providers → site-restricted web search
- When academic providers (OpenAlex, CrossRef, Semantic Scholar) are configured, returns rich metadata (DOI, authors, citations, OA status)
- Metadata richness varies by provider: OpenAlex returns abstracts, citation counts, and authors consistently; Semantic Scholar adds `tldr` and `isInfluential`; CrossRef is a DOI registry and may omit abstracts/citation counts. Automatic selection prefers OpenAlex; others answer when explicitly forced or as a fallback. Field absence reflects the provider, not an error.
- Without academic env vars, falls back to site-restricted web search (identical to previous behavior)
- OpenAlex/CrossRef require only an email address (no API key); Semantic Scholar works key-free at a lower shared rate (`SEMANTIC_SCHOLAR_API_KEY` raises the limit)
- **Open-access enrichment (Unpaywall):** when `UNPAYWALL_EMAIL` (or `OPENALEX_EMAIL`) is set, DOI-bearing results that lack a PDF link are enriched with the best open-access PDF Unpaywall knows about. Best-effort: never overwrites a provider-supplied `pdfUrl`, never fails the search, and runs *before* the `pdf_only` filter so resolved PDFs are counted. No-op when unconfigured.
- `source` filter: when set (e.g., "arxiv"), OpenAlex filters by source ID; web fallback restricts to that source's domain
- `sort_by=date`: OpenAlex sorts by `publication_date:desc`; CrossRef uses `published:desc`
- `pdf_only`: post-filters results to only those with `PDFUrl` populated (may reduce result count)

### Academic Site Pool (web search fallback)
arxiv.org, pubmed.ncbi.nlm.nih.gov, scholar.google.com, ieeexplore.ieee.org, dl.acm.org, nature.com, sciencedirect.com, link.springer.com, researchgate.net, plos.org, frontiersin.org, mdpi.com, wiley.com, jstor.org, semanticscholar.org, biorxiv.org, medrxiv.org

### Cache
- TTL: 1 hour (academic providers use semantic ranking that can shift)

---

## Tool 7: `patent_search`

### Input Schema

| Field | Type | Required | Default | Constraints |
|-------|------|----------|---------|-------------|
| `query` | string | no | — | Patent terms or number. Not required when `assignee` or `inventor` provided |
| `num_results` | int | no | 5 | 1-10 |
| `search_type` | string | no | `prior_art` | prior_art, specific, landscape |
| `patent_office` | string | no | `all` | all, US, EP, WO, JP, CN, KR |
| `assignee` | string | no | — | Company name (auto-strips Inc/LLC/Ltd suffixes) |
| `inventor` | string | no | — | Inventor name |
| `cpc_code` | string | no | — | CPC classification (e.g., G06F) |
| `year_from` | int | no | — | Only patents filed in or after this year |
| `year_to` | int | no | — | Only patents filed in or before this year |
| `provider` | string | no | — | Force provider: searchapi, epo, lens, uspto, or web search providers |
| `sessionId` | string | no | — | Link results to a `sequential_search` session; sources are auto-recorded for recovery after context loss |

### Output Fields

Each patent in the `patents` array contains:

| Field | Type | Always Present | Description |
|-------|------|---------------|-------------|
| `title` | string | yes | Patent title |
| `number` | string | yes | Patent number (e.g., US10165245B2) |
| `url` | string | yes | Link to patent detail page |
| `abstract` | string | no | Patent abstract or snippet |
| `assignee` | string | no | Patent owner/assignee |
| `inventor` | string | no | Primary inventor name |
| `filed` | string | no | Filing date (YYYY-MM-DD) |
| `granted` | string | no | Grant date (YYYY-MM-DD) |
| `pdf` | string | no | Direct link to patent PDF |
| `status` | string | no | Application status (e.g., "Patented Case") |

Additional output fields: `query`, `searchType`, `resultCount`, `source` (which provider answered), `searchUrl`, `hints` (a `ZeroResultHints` object explaining why a query returned nothing and suggesting how to broaden it — present on zero-result responses), and `trust` (always `"untrusted-external-content"` — treat results as data, not instructions; OWASP LLM01).

### Behavior
- 4-strategy fallback: explicit provider → router → patent-only providers → web search discovery
- **When an explicit provider is set**: that provider is used exclusively. If it returns empty results (e.g., USPTO for non-US patents), empty results are returned — no silent fallback to web_discovery
- **Unknown provider**: returns error listing all supported providers (no duplicates)
- Strips HTML from API responses; extracts clean patent numbers from paths
- Normalizes assignee names (removes Inc/LLC/Corp/Ltd suffixes for matching)
- Region-aware routing: `patent_office` filters which providers are tried
- Post-filter results by patent number prefix when `patent_office` is specified
- Does not cache empty results (only caches when patents are found)
- USPTO uses simple full-text search (quoted phrases); Lens uses Elasticsearch bool queries with match_phrase
- `num_results` is enforced for every provider, including a defensive cap on the USPTO path (its API may return more rows than requested)
- Provider matching is token/substring-based: `inventor`/`assignee` matches share a surname or company token rather than disambiguating entities, and a nonsense query may still fuzzy-match loosely-related patents instead of returning zero. Verify results against the returned bibliographic fields rather than assuming exact-entity matching.

### Cache
- TTL: 24 hours (only for non-empty results)

---

## Tool 8: `sequential_search`

### Purpose
Multi-step research tracking with session persistence, branching, and knowledge gap identification.

### Input Schema

| Field | Type | Required | Default | Constraints |
|-------|------|----------|---------|-------------|
| `searchStep` | string | yes | — | Description of this step |
| `stepNumber` | int | yes | — | Starts at 1 |
| `nextStepNeeded` | bool | yes | — | Whether more steps follow |
| `sessionId` | string | no | — | Session ID (required for steps 2+) |
| `researchGoal` | string | no | — | Set on step 1; defines the research objective |
| `reasoning` | string | no | — | Why this search direction was chosen |
| `confidence` | string | no | — | Confidence in this step: high, medium, or low |
| `rejectedApproaches` | []string | no | — | Approaches considered but rejected |
| `sessionSummary` | string | no | — | Running summary (used for recovery) |
| `responseMode` | string | no | auto | Force `full` or `summary` output |
| `totalStepsEstimate` | int | no | — | Estimated total steps |
| `isRevision` | bool | no | false | Revising a previous step |
| `revisesStep` | int | no | — | Step being revised |
| `branchFromStep` | int | no | — | Branching point |
| `branchId` | string | no | — | Branch identifier |
| `knowledgeGap` | string | no | — | Gap identified |
| `depth` | string | no | `quick` | Iteration-assist level: `quick`, `standard`, or `thorough` (see Iterative Depth below) |

### Session Management
- Sessions created on first call (stepNumber=1)
- A `stepNumber > 1` call with no `sessionId` is rejected with guidance (pass the sessionId, recover with `get_research_session`, or restart at step 1) — it does **not** silently start a new session, so a lost sessionId never orphans the in-flight research trail
- Session ID: UUID v4, returned in output
- TTL: 4 hours of inactivity (configurable via `SESSION_TTL`), resets on every access
- Max concurrent sessions: 50 per tenant (oldest evicted when exceeded)
- Max steps per session: 200 (configurable via `SESSION_MAX_STEPS`)
- Persistence: encrypted disk (AES-256-GCM), survives server restarts
- Cleanup: goroutine every 15 minutes removes expired sessions from memory + disk
- Per-tenant isolation: sessions keyed by `{tenantID}:{sessionID}`
- Recovery: use `get_research_session` tool after context loss
- Response modes: "full" for ≤8 steps, "summary" for >8 (override with `responseMode` input)

### Output Schema

```go
type SequentialSearchOutput struct {
    SessionID          string           `json:"sessionId"`
    ResponseMode       string           `json:"responseMode"`        // "full" or "summary"
    ResearchGoal       string           `json:"researchGoal"`
    CurrentStep        int              `json:"currentStep"`         // echoes the input stepNumber
    TotalStepsEstimate int              `json:"totalStepsEstimate"`
    IsComplete         bool             `json:"isComplete"`          // !nextStepNeeded
    StartedAt          string           `json:"startedAt"`
    CompletedAt        string           `json:"completedAt,omitempty"` // set only when complete
    Warning            string           `json:"warning,omitempty"`     // e.g. max-steps reached
    Trust              string           `json:"trust"`                 // "untrusted-external-content" — echoed source metadata is external data

    // "full" mode (default for <=8 steps):
    Steps              []StepIndexEntry `json:"steps,omitempty"`     // one-liner index, full mode only

    // "summary" mode (default for >8 steps):
    Summary            string           `json:"summary,omitempty"`   // summary mode only
    StepIndex          []StepIndexEntry `json:"stepIndex,omitempty"` // summary mode only

    // Both modes:
    LastSteps          []ResearchStep   `json:"lastSteps,omitempty"` // most recent full steps
    Gaps               []KnowledgeGap   `json:"gaps,omitempty"`
    Sources            []ResearchSource `json:"sources,omitempty"`
}

type StepIndexEntry struct {
    StepNumber int    `json:"stepNumber"`
    OneLiner   string `json:"oneLiner"`
    BranchID   string `json:"branchId"`
    Confidence string `json:"confidence"`
}
```

> The key set depends on `responseMode`: **full** mode emits `steps`; **summary** mode emits `summary` + `stepIndex` instead. Both emit `lastSteps`, `gaps`, and `sources`. This tool does **not** emit a `_meta` block (no caching).

### Iterative Depth (`depth`)

An optional iteration-assist level (#67). The server stays **infrastructure, not synthesis** — it never writes an answer, only richer metadata and (for `thorough`) raw results.

| Level | Behavior |
|-------|----------|
| `quick` (default) | Record the step and return. Byte-for-byte the prior behavior — no extra fields. |
| `standard` | Also analyze coverage of the sources gathered so far and suggest refinement queries. **No auto-execution.** Adds `depth`, `coverage`, and `refinementQueries`. |
| `thorough` | Same as `standard`, plus auto-runs up to **3** of the suggested refinement queries as web searches and attaches their merged, provenance-tagged results. Adds `refinementResults` (and `refinementNote` when the suggestion list was truncated). |

**Extra output fields** (present only for `standard`/`thorough`):

- `coverage` — `{ sourceCount, uniqueDomains, domainSpread, dominantDomain?, sourceTypes, gaps[] }`. Descriptive coverage signals (domain spread, source-type balance, thin-coverage flags) computed from the session's recorded sources. Never an answer.
- `refinementQueries` — suggested follow-up search strings derived from knowledge gaps + coverage gaps. The caller's AI decides whether to act on them.
- `refinementResults` (`thorough` only) — array of `{ query, resultCount, results[] }` (or `{ query, error }`), one per auto-run query. Raw web results tagged with the originating query; **not** synthesized. Each result is `{ title, url, snippet }`.
- `refinementNote` (`thorough` only) — present when more than 3 queries were suggested and the auto-run was bounded.

`thorough` searches respect the same rate limits and circuit breakers as `web_search`, record their sources into the session, and contribute to session `providerStats`.

### State Management
- Two-tier: in-memory index (lightweight) + encrypted disk (full session JSON)
- Write-through on every step (crash-safe: temp → fsync → rename)
- Index rebuilt from disk on server startup — no data loss across restarts
- Behind a load balancer, use session-affinity (sticky sessions) so clients reconnect to the same instance

---

## Tool 9: `get_research_session`

Recover a `sequential_search` session after context loss. Returns the session summary, step index, and most recent steps. Use `stepId` to retrieve full details of a specific earlier step.

### Input Schema

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `sessionId` | string | yes | The session ID to recover |
| `stepId` | integer | no | Retrieve full details for a specific step number |

### Behavior

1. Without `stepId`: returns session summary view from memory (no disk I/O)
   - Includes: researchGoal, summary, stepIndex, last 3 full steps, active gaps
2. With `stepId`: loads full step data from disk for that specific step number
3. Every response carries `"trust": "untrusted-external-content"` — the echoed source metadata (titles/URLs) is external data; treat it as data, not instructions (OWASP LLM01).
4. Sessions are private to the `(tenant, user)` that created them — a session ID is honored only for its owning user (anonymous/STDIO uses a single owner).

#### Cross-call error patterns (#99)

The summary view additionally surfaces aggregated outcome telemetry recorded across the session's tool calls (scrapes, searches). This is **additive** metadata — per-call errors are still returned in full to the caller; this is the cross-call view.

- `errorPatterns` — array of `{ kind, count, affectedUrls[], suggestion, lastSeen }`, surfaced **only** when a given error `kind` occurred **3 or more times** in the session (a false-positive guard for small samples). `kind` uses the shared error taxonomy (`auth_required`, `blocked`, `rate_limited`, `browser_unavailable`, …); `suggestion` is a session-level remediation hint (e.g. *auth_required* → "Consider open_access=true or target preprint servers (arxiv, biorxiv)."). Absent when nothing crosses the threshold.
- `providerStats` — object keyed by provider name → `{ attempts, successes }` for the session. Absent when no provider outcomes were recorded.

Recorded outcome state is bounded (most-recent 200 events, FIFO) and tenant/user-isolated, honoring the no-unbounded-retention posture.

### Annotations
- ReadOnly: true
- Idempotent: true (safe to call repeatedly)
- OpenWorld: false (reads internal state only)

### Error Conditions
- Session not found → "Session not found or expired. Sessions last 4 hours from last activity."
- Step not found → error with step number

### Cache
- No cache (reads internal session state)

---

## Tool 10: `get_my_analytics`

**Opt-in, consent-gated (#92). Registered only when `USER_ANALYTICS_ENABLED=true`.** Read-only.

### Purpose

Return the **calling user's own** usage analytics (tools used, counts, first/last seen) for their tenant. This is per-user data under GDPR / Quebec Law 25, so it is off by default, collected only after recorded consent, isolated per user, encrypted at rest, and covered by the data-subject access/erasure endpoints (`/admin/data`).

### Input Schema

No inputs. The subject is always the authenticated caller — a user can never request another user's analytics.

### Output Schema

```go
type GetMyAnalyticsOutput struct {
    Status    string         `json:"status"`           // "ok" | "empty" | "no_consent" | "unavailable"
    Reason    string         `json:"reason,omitempty"`
    Analytics *UserAnalytics `json:"analytics,omitempty"`
}

type UserAnalytics struct {
    TenantID    string           `json:"tenantId"`
    UserID      string           `json:"userId"`
    TotalCalls  int64            `json:"totalCalls"`
    ToolCounts  map[string]int64 `json:"toolCounts"`
    FirstSeen   string           `json:"firstSeen,omitempty"`
    LastSeen    string           `json:"lastSeen,omitempty"`
    RecentTools []string         `json:"recentTools,omitempty"`
}
```

### Behavior

1. Requires an authenticated user (`status: "unavailable"` for anonymous).
2. Requires recorded consent for the `analytics` purpose (`status: "no_consent"` otherwise — nothing is collected without it).
3. Returns the caller's own summary, or `status: "empty"` if none recorded yet.

### Cache

- No cache (reads per-user state directly).

---

## Tool 11: `memory_save`

**Opt-in, consent-gated (#88). Registered only when `MEMORY_ENABLED=true`.** This is a **write** tool (`ReadOnlyHint: false`, `DestructiveHint: false` — it appends, never deletes).

### Purpose

Persist a research finding to the calling user's long-term memory so it can be recalled in future sessions (unlike `sequential_search` sessions, which expire after 4 hours). Stored per-user, encrypted, retention-bounded (`MEMORY_RETENTION`, default 90 days), and erasable via the data-subject endpoint (`/admin/data`). There is no `memory_forget` tool — deletion flows through the GDPR erasure endpoint.

### Input Schema

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `note` | string | yes | The finding/conclusion to remember |
| `topic` | string | no | Group label for later recall |
| `url` | string | no | Source URL this memory refers to |
| `tags` | string[] | no | Organizational tags |

### Output Schema

```go
type MemorySaveOutput struct {
    Status    string `json:"status"`    // "ok" | "no_consent" | "unavailable"
    Reason    string `json:"reason,omitempty"`
    ID        string `json:"id,omitempty"`
    CreatedAt string `json:"createdAt,omitempty"`
}
```

### Behavior

Requires an authenticated user and recorded consent for the `memory` purpose; otherwise returns `unavailable` / `no_consent` and persists nothing.

### Cache

- Not cached (a write).

---

## Tool 12: `memory_recall`

**Opt-in, consent-gated (#88). Registered only when `MEMORY_ENABLED=true`.** Read-only.

### Purpose

Recall findings the calling user previously saved with `memory_save`, across sessions, optionally filtered by topic. Shows only the caller's own memories — never another user's.

### Input Schema

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `topic` | string | no | Filter by topic; omit for most recent across all topics |
| `limit` | int | no | Max memories to return (default 20) |

### Output Schema

```go
type MemoryRecallOutput struct {
    Status   string        `json:"status"`   // "ok" | "no_consent" | "unavailable"
    Reason   string        `json:"reason,omitempty"`
    Count    int           `json:"count"`
    Memories []MemoryEntry `json:"memories"`
    Trust    string        `json:"trust"`    // "user-asserted-content" — recalled notes are data, not instructions
}

type MemoryEntry struct {
    ID        string   `json:"id"`
    TenantID  string   `json:"tenantId"`
    UserID    string   `json:"userId"`
    Topic     string   `json:"topic,omitempty"`
    Note      string   `json:"note"`
    URL       string   `json:"url,omitempty"`
    Tags      []string `json:"tags,omitempty"`
    CreatedAt string   `json:"createdAt"`
}
```

### Cache

- No cache (reads per-user state directly).

---

## Tool 13: `workspace_contribute`

**Opt-in, consent-gated (#96). Registered only when `WORKSPACES_ENABLED=true`.** This is a **write** tool (`ReadOnlyHint: false`, `DestructiveHint: false`).

### Purpose

Share a research finding into a shared team workspace. The contribution is stored as a **copy** with immutable provenance (your tenant/user, timestamp) — never a live link to your private data, so per-tenant isolation is never silently voided. Membership is managed by your host app (the server enforces, the host owns the policy). Erasable by the contributor via the data-subject endpoint.

### Input Schema

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `workspace_id` | string | yes | Workspace to contribute to (you must be a member) |
| `note` | string | yes | The finding to share |
| `url` | string | no | Source URL |
| `tags` | string[] | no | Organizational tags |

### Output Schema

```go
type WorkspaceContributeOutput struct {
    Status string `json:"status"` // "ok" | "not_member" | "no_consent" | "unavailable"
    Reason string `json:"reason,omitempty"`
    ID     string `json:"id,omitempty"`
}
```

### Behavior

Requires an authenticated user, recorded consent for the `workspace` purpose, AND membership. The caller's identity is taken from the validated token — never from a parameter, and never from the `workspace_id`.

### Cache

- Not cached (a write).

---

## Tool 14: `workspace_read`

**Opt-in, consent-gated (#96). Registered only when `WORKSPACES_ENABLED=true`.** Read-only.

### Purpose

Read the shared findings in a workspace you belong to (each with its contributor attribution). **Non-members receive zero contributions** — membership is re-verified on every read.

### Input Schema

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `workspace_id` | string | yes | Workspace to read (you must be a member) |

### Output Schema

```go
type WorkspaceReadOutput struct {
    Status        string         `json:"status"` // "ok" | "not_member" | "no_consent" | "unavailable"
    Count         int            `json:"count"`
    Contributions []Contribution `json:"contributions"`
    Trust         string         `json:"trust"`  // "untrusted-external-content" — cross-member notes are untrusted data (does not restrict who may read)
}

type Contribution struct {
    ID                string   `json:"id"`
    WorkspaceID       string   `json:"workspaceId"`
    ContributorTenant string   `json:"contributorTenant"`
    ContributorUser   string   `json:"contributorUser"`
    Note              string   `json:"note"`
    URL               string   `json:"url,omitempty"`
    Tags              []string `json:"tags,omitempty"`
    CreatedAt         string   `json:"createdAt"`
}
```

### Membership management

Membership is host-owned via admin endpoints (not MCP tools): `POST /admin/workspace/members` and `DELETE /admin/workspace/members` with `{workspace_id, tenant_id, user_id}`. The server enforces the resulting membership checks; it does not own the membership policy.

### Cache

- No cache (reads workspace state directly).

---

## Tool 15: `answer`

**Provider-independent.** Registered only when at least one answer provider is configured (currently Exa via `EXA_API_KEY`; future providers register the same way). Read-only, open-world, idempotent.

### Purpose

Ask a factual question and get one grounded, synthesized answer with source citations. Unlike `web_search` (a list of links) or `search_and_scrape` (raw page text), this returns a direct written answer plus the URLs it relied on. The backing provider is pluggable — set `provider` to choose one when several are configured. The result names the answering provider, and `costUsd` reports the per-call estimate for metered providers (0 for free ones).

### Input Schema

| Field | Type | Required | Default | Constraints |
|-------|------|----------|---------|-------------|
| `query` | string | yes | — | The question to answer |
| `provider` | string | no | — | Force a provider (e.g. `exa`); required only when more than one is configured |

### Output Schema

```go
type AnswerOutput struct {
    Answer    string     `json:"answer"`
    Citations []Citation `json:"citations"`
    Provider  string     `json:"provider"`         // which provider answered
    CostUsd   float64    `json:"costUsd,omitempty"` // per-call estimate for metered providers (not an invoice)
    Trust     string     `json:"trust"`            // "untrusted-external-content"
}

type Citation struct {
    Title         string `json:"title,omitempty"`
    URL           string `json:"url"`
    PublishedDate string `json:"publishedDate,omitempty"`
}
```

### Behavior

1. Resolve the `search.AnswerSearcher` for the requested `provider` (or the sole configured one).
2. Call the provider; map its grounded answer + citations + cost into the output.
3. `costUsd` and the resolved provider are surfaced into audit metadata (`cost_usd`, `provider`).
4. The answer is external content — `trust` is always `"untrusted-external-content"`.

### Cache
- TTL: 1 hour (keyed by query + provider)

---

## Tool 16: `structured_search`

**Provider-independent.** Registered only when at least one structured-search provider is configured (currently Exa via `EXA_API_KEY`). Read-only, open-world, idempotent.

### Purpose

Search the web and extract structured data from each result. Supply a JSON `schema` to pull specific fields back as JSON per result, and/or a `category` to focus the search. The backing provider is pluggable (`provider` field). Valid `category` values and any `schema` limits are provider-specific and validated by the chosen provider — an unsupported value returns an error listing the valid options. The result names the provider, and `costUsd` reports the per-call estimate for metered providers.

### Input Schema

| Field | Type | Required | Default | Constraints |
|-------|------|----------|---------|-------------|
| `query` | string | yes | — | What to search for (entity name for entity lookups) |
| `category` | string | no | — | Provider-specific result category (validated by the provider) |
| `num_results` | int | no | 5 | 1-10 |
| `schema` | object | no | — | JSON Schema for per-result field extraction; provider-specific limits apply |
| `provider` | string | no | — | Force a provider (e.g. `exa`); required only when more than one is configured |

### Output Schema

```go
type StructuredOutput struct {
    Query       string           `json:"query"`
    Category    string           `json:"category"`
    ResultCount int              `json:"resultCount"`
    Results     []StructuredItem `json:"results"`
    Provider    string           `json:"provider"`         // which provider answered
    CostUsd     float64          `json:"costUsd,omitempty"` // per-call estimate for metered providers
    Trust       string           `json:"trust"`            // "untrusted-external-content"
}

type StructuredItem struct {
    Title         string          `json:"title,omitempty"`
    URL           string          `json:"url"`
    PublishedDate string          `json:"publishedDate,omitempty"`
    Author        string          `json:"author,omitempty"`
    Summary       json.RawMessage `json:"summary,omitempty"`    // schema-conforming JSON, or plain text summary
    Highlights    []string        `json:"highlights,omitempty"`
    Entities      json.RawMessage `json:"entities,omitempty"`   // provider-specific structured entities, when available
}
```

### Behavior

1. Resolve the `search.StructuredSearcher` for the requested `provider` (or the sole configured one).
2. The provider validates its own constraints (category vocabulary, schema limits) **before** any paid call; a violation returns a validation tool-error, never a wasted upstream request.
3. When `schema` is set, each result's `summary` is JSON conforming to it; otherwise it is a plain text summary. Providers may populate per-result `entities` for entity categories (e.g. Exa's `company`).
4. `costUsd` and the resolved provider are surfaced into audit metadata. Results are external content — `trust` is always `"untrusted-external-content"`.

### Provider notes

- **Exa**: `category` ∈ company, people, research paper, news, pdf, github, financial report, personal site; `schema` must be a flat object (root `object`, ≤10 properties, nesting depth ≤2, primitive array items); `category:"company"` returns structured company entities.

### Cache
- TTL: 1 hour (keyed by query + category + num_results + schema + provider)

---

## Tool 17: `citation_graph`

Map a seed paper's citation neighborhood: works that **cite** it (forward edges, `cited_by`) and works it **cites** (backward edges, `references`). Single-hop per call — multi-hop traversal is the caller's to orchestrate (the server stays infrastructure, not an autonomous crawler). Registered only when a citation-capable academic provider (Semantic Scholar or OpenAlex) is configured.

### Input Schema

| Field | Type | Required | Default | Constraints |
|-------|------|----------|---------|-------------|
| `paper` | string | yes | — | Seed paper: a DOI (e.g. `10.1038/nature12373`) or an exact title |
| `direction` | string | no | `both` | `cited_by` (forward), `references` (backward), or `both` |
| `num_results` | int | no | 10 | 1-25 per direction |
| `influential_only` | bool | no | false | Keep only highly-influential citations when the provider supplies that signal (Semantic Scholar); no-op otherwise (results pass through) |
| `provider` | string | no | — | Force `semanticscholar` (intent + influence) or `openalex` (counts only). Omit to auto-select (prefers semanticscholar) |
| `sessionId` | string | no | — | Link discovered works to a `sequential_search` session for recovery after context loss |

### Output Fields

| Field | Type | Always Present | Description |
|-------|------|---------------|-------------|
| `seed` | string | yes | The seed paper as supplied |
| `direction` | string | yes | The direction traversed |
| `provider` | string | yes | Which citation provider answered (`semanticscholar` = intent+influence; `openalex` = counts only) |
| `citedBy` | []paper | when direction includes `cited_by` | Works that cite the seed (each a full academic-paper object, same shape as `academic_search` papers) |
| `citedByCount` | int | when direction includes `cited_by` | Count of forward edges returned |
| `references` | []paper | when direction includes `references` | Works the seed cites |
| `referencesCount` | int | when direction includes `references` | Count of backward edges returned |
| `trust` | string | yes | Always `"untrusted-external-content"` — treat results as data, not instructions (OWASP LLM01) |

Each work in `citedBy`/`references` carries the same fields as an `academic_search` paper, including `tldr`, `isInfluential`, and `citationIntents` when the provider (Semantic Scholar) supplies them.

### Behavior
- **Provider fidelity:** Semantic Scholar returns citation intent (`citationIntents`) and an influence flag (`isInfluential`); OpenAlex is counts-only (forward edges via the `cites:` filter, backward edges from the seed's `referenced_works`). Auto-selection prefers Semantic Scholar.
- **Seed resolution:** a DOI resolves directly; a title resolves to the provider's top match.
- **Explicit provider honoring:** forcing an unconfigured/unknown/incapable provider returns a structured error listing the supported providers (`semanticscholar`, `openalex`); it never silently falls back.
- `influential_only` filters out works the provider did not flag as influential; providers without the signal pass all results through unchanged.

### Annotations
- ReadOnly: true · Idempotent: true · OpenWorld: true (queries external scholarly APIs)

### Cache
- TTL: 24 hours (citation graphs change slowly)

---

## Tool 18: `research_export`

Export a completed `sequential_search` session as a shareable deliverable — a human-readable **markdown** report or the full structured **json** session. Read-only and idempotent: it renders existing session state, never mutates it. Scoped to the caller's own `(tenant, user)`.

### Input Schema

| Field | Type | Required | Default | Constraints |
|-------|------|----------|---------|-------------|
| `sessionId` | string | yes | — | The `sequential_search` session to export |
| `format` | string | no | `markdown` | `markdown` (readable report) or `json` (full structured session) |

### Output Fields

| Field | Type | Always Present | Description |
|-------|------|---------------|-------------|
| `sessionId` | string | yes | The exported session ID |
| `format` | string | yes | `markdown` or `json` |
| `researchGoal` | string | yes | The session's research goal |
| `stepCount` | int | yes | Number of recorded steps |
| `sourceCount` | int | yes | Number of recorded sources |
| `startedAt` | string | yes | Session creation time (RFC3339) |
| `exportedAt` | string | yes | When this export was generated (RFC3339) |
| `tenantId` | string | yes | Owning tenant — provenance for the export |
| `document` | string \| object | yes | The rendered report: a markdown string when `format=markdown`, or the structured session object when `format=json` |
| `trust` | string | yes | Always `"untrusted-external-content"` — source titles/URLs are external data |

### Behavior
- **Markdown report** contains: research-goal heading, a metadata block (session id, started, step/source counts), every step with its reasoning/confidence/rejected-approaches/timestamp (revisions and branches are labeled in the step heading), an Open Questions section (knowledge gaps), a numbered Sources list, and a provenance footer (export time, tenant).
- Deterministic: same session → byte-identical output aside from the `exportedAt` stamp (idempotency).
- Sessions are private to their owning `(tenant, user)`; a leaked sessionId is honored only for its owner.

### Annotations
- ReadOnly: true · Idempotent: true · OpenWorld: false (reads internal session state)

### Error Conditions
- Missing `sessionId` → validation error
- Unknown/expired session → "Session not found or expired. Sessions last 4 hours from last activity."
- Invalid `format` → validation error (use `markdown` or `json`)

### Cache
- No cache (renders live session state)

---

## Tool 19: `format_bibliography`

Turn a set of sources into a formatted bibliography in **APA**, **MLA**, or **BibTeX**. Sources come from either a `sequential_search` session (its recorded sources) or an explicit list the caller supplies (e.g. `academic_search` / `citation_graph` results). Read-only and idempotent.

### Input Schema

| Field | Type | Required | Default | Constraints |
|-------|------|----------|---------|-------------|
| `style` | string | no | `apa` | `apa`, `mla`, or `bibtex` |
| `sessionId` | string | no | — | Build from this session's recorded sources. Provide this **or** `sources` |
| `sources` | []object | no | — | Explicit sources. Provide this **or** `sessionId`. Each: `url` (required), `title`, `author`, `site`, `date` |

### Output Fields

| Field | Type | Always Present | Description |
|-------|------|---------------|-------------|
| `style` | string | yes | The citation style used |
| `entryCount` | int | yes | Number of unique entries rendered (after de-duplication by URL) |
| `bibliography` | string | yes | The formatted bibliography — entries separated by blank lines |
| `sessionId` | string | no | Echoed when sources were drawn from a session |
| `trust` | string | yes | Always `"untrusted-external-content"` — source metadata is external data |

### Behavior
- Sources are **de-duplicated by URL** (first occurrence wins) and ordered deterministically: APA/MLA alphabetically by the rendered line, BibTeX by cite key.
- **BibTeX cite keys** are `surname + year + first-title-word` (e.g. `vaswani2017attention`), made collision-free within the list by appending `a`/`b`/`c…`; BibTeX-significant characters in values are escaped.
- An unrecognized style is rejected at the tool boundary (the lower-level formatter falls back to APA, but the tool validates first).
- Entries with no `url` are skipped. Either `sessionId` or a non-empty `sources` list is required.
- Session-sourced bibliographies are scoped to the caller's own `(tenant, user)`.

### Annotations
- ReadOnly: true · Idempotent: true · OpenWorld: false (formats supplied/stored data)

### Cache
- No cache (pure formatting of supplied/stored data)

---

## Tool 20: `filing_search`

Search SEC EDGAR — the authoritative primary source for US public-company disclosures (10-K/10-Q/8-K/S-1/DEF 14A/…). Registered only when a filing provider is configured (`edgar`, which needs a contact email for SEC's required User-Agent).

### Input Schema

| Field | Type | Required | Default | Constraints |
|-------|------|----------|---------|-------------|
| `query` | string | yes* | — | Company name, ticker, or CIK — or free text to full-text search all filings. *Required unless `ticker` is set |
| `form_type` | string | no | — | Restrict to a filing type (10-K, 10-Q, 8-K, S-1, DEF 14A, …) |
| `ticker` | string | no | — | Direct ticker lookup; takes precedence over `query` for entity resolution |
| `date_from` | string | no | — | Only filings on/after this date (YYYY-MM-DD) |
| `date_to` | string | no | — | Only filings on/before this date (YYYY-MM-DD) |
| `facts` | bool | no | false | Return structured XBRL company facts (revenue, net income, EPS, assets) instead of a filing list |
| `num_results` | int | no | 5 | 1-10 |
| `provider` | string | no | — | Force a filing provider: `edgar` |
| `sessionId` | string | no | — | Link results to a `sequential_search` session |

### Output Fields

Each item in `filings[]`: `company`, `cik`, `formType`, `filingDate`, `periodOfReport`, `accession`, `url` (document link; pair with `scrape_page`), `description`, `source`. In `facts=true` mode each item is one XBRL fact: `concept`, `unit`, `value` (**exactly as filed — no rounding**). Plus `query`, `resultCount`, `provider`, `hints` (zero-result), and `trust` (`untrusted-external-content`).

### Behavior
- Entity resolution: a ticker/CIK/known-company `query` resolves to a CIK and lists its recent filings from the submissions API; otherwise a full-text search runs across all filers (EFTS).
- `facts=true` returns a curated set of headline XBRL concepts (revenue, net income, assets, EPS, …), most-recent value each, passed through verbatim.
- **Required `User-Agent`**: SEC blocks requests without a descriptive UA + contact email; the provider only registers when `EDGAR_CONTACT_EMAIL` (or `OPENALEX_EMAIL`) is set. No request is ever made without it.
- Ticker→CIK map is fetched once and cached for the process lifetime.

### Annotations
- ReadOnly: true · Idempotent: true · OpenWorld: true (queries the live SEC API)

### Cache
- TTL: 24 hours (only for non-empty results)

---

## Tool 21: `legal_search`

Search US court opinions (federal + state) via CourtListener for case-law research and precedent tracing. Registered only when a case provider is configured (`courtlistener`, which works keyless at a lower rate).

### Input Schema

| Field | Type | Required | Default | Constraints |
|-------|------|----------|---------|-------------|
| `query` | string | yes | — | Legal topic, case name (e.g. `Miranda v. Arizona`), or statutory reference |
| `jurisdiction` | string | no | — | Court id: `scotus`, `ca9`, `ny`, … |
| `date_from` | string | no | — | Only opinions decided on/after this date (YYYY-MM-DD) |
| `date_to` | string | no | — | Only opinions decided on/before this date (YYYY-MM-DD) |
| `num_results` | int | no | 10 | 1-20 |
| `provider` | string | no | — | Force a case-law provider: `courtlistener` |
| `sessionId` | string | no | — | Link results to a `sequential_search` session |

### Output Fields

Each item in `cases[]`: `caseName`, `citation` (Bluebook), `court`, `courtId`, `dateFiled`, `docketNumber`, `citationCount`, `url` (opinion page; `scrape_page` for full text), `source`. Plus `query`, `resultCount`, `provider`, `hints`, and `trust` (`untrusted-external-content`).

### Behavior
- Searches the CourtListener v4 opinions index; `jurisdiction` maps to the `court` filter, dates to `filed_after`/`filed_before`.
- **Auth**: works keyless at ~100 req/day; `COURTLISTENER_API_TOKEN` raises the limit (~5000/day). The token is sent as an `Authorization` header and never logged.

### Annotations
- ReadOnly: true · Idempotent: true · OpenWorld: true (queries the live CourtListener API)

### Cache
- TTL: 24 hours (only for non-empty results)

---

## Tool 22: `econ_search`

Look up macroeconomic data from FRED (Federal Reserve Economic Data) — 800K+ time series (GDP, CPI, unemployment, rates). Registered only when `FRED_API_KEY` is set.

### Input Schema

| Field | Type | Required | Default | Constraints |
|-------|------|----------|---------|-------------|
| `query` | string | yes* | — | Keyword to search series by. *Provide this OR `series_id` |
| `series_id` | string | yes* | — | A FRED series ID (e.g. `GDP`, `CPIAUCSL`, `UNRATE`) to fetch observations. *Provide this OR `query` |
| `date_from` | string | no | — | Only observations on/after this date (YYYY-MM-DD) |
| `date_to` | string | no | — | Only observations on/before this date (YYYY-MM-DD) |
| `frequency` | string | no | — | Resample: d, w, m, q, a |
| `units` | string | no | — | FRED units transform (e.g. `pch`, `pc1`); omit for raw levels |
| `num_results` | int | no | 5 (search) / 10 (observations) | — |
| `provider` | string | no | — | Force an economic-data provider: `fred` |

### Output Fields

`mode` is `series` (keyword search) or `observations` (series_id lookup). In series mode each `results[]` item: `seriesId`, `title`, `units`, `frequency`, `lastUpdated`, `notes`. In observations mode: `seriesId`, `date`, `value` (**exactly as returned — no rounding**; missing observations carry no `value`). Plus `query`, `seriesId` (echoed in observations mode), `resultCount`, `provider`, `hints`, and `trust` (`untrusted-external-content`).

### Behavior
- `series_id` set → returns that series' observations (most-recent first), honoring date/frequency/units filters; otherwise keyword-searches series.
- No macro web-lens fallback exists, so an error/empty returns a structured zero-result with hints (no fallback).
- **Auth**: `FRED_API_KEY` (free at fred.stlouisfed.org) is required; the provider is skipped when unset. The key is sent as a query param and never logged.

### Annotations
- ReadOnly: true · Idempotent: true · OpenWorld: true (queries the live FRED API)

### Cache
- TTL: 6 hours (only for non-empty results)

---

## Cross-Cutting Concerns

### Timeouts (all configurable via env)
| Operation | Default | Max |
|-----------|---------|-----|
| Search API call | 10s | 30s |
| Markdown negotiation | 5s | 10s |
| HTML scrape (goquery) | 15s | 30s |
| Browser scrape (go-rod) | 30s | 60s |
| YouTube transcript | 30s | 60s |
| Document download | 30s | 60s |
| Total tool execution | 60s | 120s |

### Content Size Limits
| Content | Limit |
|---------|-------|
| `scrape_page` content — default `max_length` | 50 KB |
| `scrape_page` content — hard cap (`maxScrapeLength`, all modes incl. `raw`) | 5 MB |
| `search_and_scrape` per-source content — default | 50 KB |
| `search_and_scrape` combined content — default `total_max_length` | 300 KB |
| Document download | 10 MB |
| YouTube transcript | 100 KB |

### Token Estimation
- Formula: `len(content) / 4` (conservative, ~4 chars per token)
- Size categories: small (<5K chars), medium (<20K), large (<50K), very_large (>=50K)

### Cache Freshness Provenance

Cacheable tools attach a top-level MCP `_meta` block so a client can tell whether a result was served from cache and how stale it is. The fields (set in `cachedResultWithMeta` / `freshResult`, `internal/tools/errors.go`) are:

| Field | Type | Meaning |
|-------|------|---------|
| `cached` | bool | `true` if served from cache, `false` if freshly fetched |
| `ageSeconds` | int | Age of the cached entry in seconds (`0` for fresh) |
| `maxAgeSeconds` | int | The entry's TTL in seconds |
| `freshness` | string | Human-readable freshness label (e.g. `fresh`) |

Which tools emit `_meta`:

| Tool | Fresh result | Cache hit |
|------|--------------|-----------|
| `web_search` | yes (`cached: false`) | yes (`cached: true`) |
| `image_search`, `news_search`, `academic_search`, `patent_search`, `scrape_page` | no | yes (`cached: true`) |
| `search_and_scrape`, `sequential_search`, `get_research_session` | no (not cached as a unit) | n/a |

### Audit & Tenant Scope

Every tool call is logged through `deps.Auditor.Log()` as an `audit.AuditEvent` (`internal/audit/logger.go`) carrying `tenant_id`, `user_id`, `request_id`, `tool_name`, `duration_ms`, `success`, and an optional `error_code` (field names are the JSON tags on `AuditEvent`). Tenant and user identity are read from the request context (`auth.TenantIDFromContext` / `auth.UserIDFromContext`).

Privacy: the raw query text is attached to `metadata.query` **only** when `AUDIT_INCLUDE_REQUEST_BODY` is enabled (`Auditor.IncludeRequestBody()`); otherwise just `metadata.query_length` is recorded. All metadata string values and error strings pass through `audit.MaskSecrets` so credentials never persist. Cache keys and session keys are tenant-scoped, so one tenant cannot read another's cached or session data.

### Unified Error Handling

All tools use a **dual-format error response**: a natural-language first line + a JSON block with machine-readable metadata:

```
Rate limited (google). Wait 60 seconds and retry, or try a different provider.

{"error":{"kind":"rate_limited","retryable":true,"retryAfterSeconds":60,"suggestedAction":"retry_after_delay","provider":"google"}}
```

Error kinds: `rate_limited`, `auth_required`, `blocked`, `network`, `content_empty`, `browser_unavailable`, `config`, `upstream_unavailable`. Each maps to a `suggestedAction` the LLM can branch on programmatically.

Full details: see `docs/ERROR_HANDLING.md` — covers the three-layer architecture, all error kinds and actions, and contributor patterns.

### Tool Annotations (MCP Protocol)

All tools declare annotations for client consumption. CI enforces tool↔doc consistency via `TestAllToolsHaveAnnotations`, `TestToolsDocMatchesRegistry`, `TestOutputSchemaMatchesResponse`, and `TestToolDescriptionQuality` (`internal/tools/metadata_test.go`) — including on docs-only PRs via the standalone `docs-drift` CI job:

| Tool | ReadOnly | Idempotent | OpenWorld | Destructive |
|------|----------|------------|-----------|-------------|
| web_search | true | true | true | false |
| scrape_page | true | true | true | false |
| search_and_scrape | true | true | true | false |
| image_search | true | true | true | false |
| news_search | true | true | true | false |
| academic_search | true | true | true | false |
| patent_search | true | true | true | false |
| sequential_search | true | **false** | false | false |
| get_research_session | true | true | false | false |

`sequential_search` is non-idempotent because it writes session state to disk on every call.

### Provider Resolution

When a `provider` field is set on any search tool:
1. If provider is in the `SearchProviders`/`PatentProviders`/`AcademicProviders` map → use it
2. If provider is known but not configured → error with env var hint
3. If provider is completely unknown → error listing all supported providers (via `allSupportedProviders()`)

Source of truth for supported providers: `search.SupportedProviders`, `search.SupportedPatentProviders`, `search.SupportedAcademicProviders` in `internal/search/provider.go`.

### Known Provider Behaviors (not bugs)

These are upstream behaviors we cannot control — they reflect how the underlying APIs work:

| Provider | Behavior | Impact |
|----------|----------|--------|
| SearchAPI | May return fewer results than `num_results` requested | Query has limited coverage in their index; not an error |
| Google (news) | `freshness=hour` may return articles 5-10 hours old | Google's "last hour" filter is approximate, not strict |
| Google (images) | `size=large` may return images as small as 600x600 | Google's size thresholds differ from typical expectations |
| USPTO | Full-text search only (no field-qualified queries) | API rejects field syntax; results rely on relevance ranking |
| OpenAlex | `pdf_only` may return 0 results for common topics | Not all papers have PDF URLs indexed in their metadata |
| DuckDuckGo | Rate-limited aggressively from cloud/datacenter IPs | Works well from local/STDIO; may return 0 results from servers |
| DuckDuckGo | Images and News return empty results | HTML endpoint doesn't support these categories; Router falls through |

These are not errors in web-researcher-mcp. The tool faithfully passes parameters to the upstream API and returns whatever the API provides.
