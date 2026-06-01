# Tool Specifications

These tools let your AI assistant search the web, read pages, find academic papers, track multi-step research, and more — always returning real, verifiable sources. Below are the detailed schemas and behavioral contracts for each tool.

> **Note:** Output schemas describe the JSON shape returned by each tool. See the corresponding `internal/tools/*.go` file for the implementation. Input schemas are auto-generated from struct `jsonschema` tags.

## Tool Registration Pattern

Each tool follows the pattern in `internal/tools/registry.go`: a typed input struct with `jsonschema` tags (the SDK auto-generates JSON Schema from these) and a `register*` function that calls `mcp.AddTool`. See `internal/tools/search.go` for a representative example.

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

### Output Schema

```go
type SearchOutput struct {
    URLs        []string       `json:"urls"`
    Query       string         `json:"query"`
    ResultCount int            `json:"resultCount"`
    Results     []SearchResult `json:"results"`
}

type SearchResult struct {
    Title       string `json:"title"`
    URL         string `json:"url"`
    Snippet     string `json:"snippet"`
    DisplayLink string `json:"displayLink"`
}
```

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
    ContentLength   int       `json:"contentLength"`
    Truncated       bool      `json:"truncated"`
    EstimatedTokens int       `json:"estimatedTokens"`
    SizeCategory    string    `json:"sizeCategory"`   // small, medium, large, very_large
    Citation        *Citation `json:"citation"`       // always present
    Raw             bool      `json:"raw,omitempty"`  // true only in raw mode; omitted otherwise
    Metadata        *Metadata `json:"metadata,omitempty"` // present only when a title was extracted (full/preview only)
}

type Metadata struct {
    Title  string `json:"title"`
    Author string `json:"author"`
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

3. WEB PAGE EXTRACTION (4-tier, ordered by speed)
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

### Output Schema

```go
type SearchAndScrapeOutput struct {
    Query           string          `json:"query"`
    Status          string          `json:"status"`           // "complete", "partial", or "failed"
    Sources         []SourceResult  `json:"sources"`
    CombinedContent string          `json:"combinedContent"`
    ScrapeFailures  []FailureInfo   `json:"scrapeFailures,omitempty"`
    Note            string          `json:"note,omitempty"`   // guidance when status="failed"
    Summary         PipelineSummary `json:"summary"`
    SizeMetadata    SizeMetadata    `json:"sizeMetadata"`
    Recommendations []Recommendation `json:"recommendations,omitempty"` // advisory; see below
    Components      []Component      `json:"components,omitempty"`      // AI-formatted; see below
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
    URL         string        `json:"url"`
    Title       string        `json:"title,omitempty"`
    Content     string        `json:"content"`
    ContentType string        `json:"contentType"`
    Scores      *QualityScore `json:"scores,omitempty"`
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
8. Optionally append `recommendations` (advisory, content-based; `SOURCE_RECOMMENDATIONS`, default on) and `components` (AI-formatted renderables; `GENERATIVE_UI_ENABLED`, default off) — both derived purely from the quality scores already computed, with no extra scoring pass and no model call

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
}

type NewsArticle struct {
    Title       string `json:"title"`
    URL         string `json:"url"`
    Source      string `json:"source"`
    PublishedAt string `json:"publishedAt,omitempty"`
    Snippet     string `json:"snippet"`
}
```

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
| `provider` | string | no | — | Force provider: openalex, crossref (academic APIs), or google, brave, serper, searxng, searchapi (web fallback) |
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
| `pdfUrl` | string | no | Direct link to PDF |

Additional output fields: `query`, `totalResults`, `resultCount`, `source` (which provider answered: openalex, crossref, router, web_search).

### Behavior
- 4-strategy fallback: explicit provider → router → academic providers → site-restricted web search
- When academic providers (OpenAlex, CrossRef) are configured, returns rich metadata (DOI, authors, citations, OA status)
- Metadata richness varies by provider: OpenAlex returns abstracts, citation counts, and authors consistently; CrossRef is a DOI registry and may omit abstracts/citation counts. Automatic selection prefers OpenAlex; CrossRef answers when explicitly forced or as a fallback. Field absence reflects the provider, not an error.
- Without academic env vars, falls back to site-restricted web search (identical to previous behavior)
- Academic providers require only an email address (no API key registration)
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

Additional output fields: `query`, `searchType`, `resultCount`, `source` (which provider answered), `searchUrl`.

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

All tools declare annotations for client consumption (enforced by `TestAllToolsHaveAnnotations`):

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
