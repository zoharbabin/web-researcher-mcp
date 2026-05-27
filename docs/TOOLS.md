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
| `site` | string | no | — | Domain restriction |
| `exact_terms` | string | no | — | Exact phrase match |
| `exclude_terms` | string | no | — | Terms to exclude |
| `country` | string | no | — | ISO 3166-1 alpha-2 |
| `lens` | string | no | — | Search lens name |

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
- Invalid API key → return error with setup instructions
- Rate limited → circuit breaker opens, return 429
- No results → return empty `urls` array (not an error)

---

## Tool 2: `scrape_page`

### Purpose
Extract content from a URL, supporting web pages, documents, and YouTube videos.

### Input Schema

| Field | Type | Required | Default | Constraints |
|-------|------|----------|---------|-------------|
| `url` | string | yes | — | Valid HTTP(S) URL |
| `mode` | string | no | `full` | `full`, `preview` |
| `max_length` | int | no | 50000 | Bytes |

### Output Schema

```go
type ScrapeOutput struct {
    URL            string            `json:"url"`
    Content        string            `json:"content"`
    ContentType    string            `json:"contentType"`    // html, markdown, youtube, pdf, docx, pptx
    ContentLength  int               `json:"contentLength"`
    Truncated      bool              `json:"truncated"`
    EstimatedTokens int              `json:"estimatedTokens"`
    SizeCategory   string            `json:"sizeCategory"`  // small, medium, large, very_large
    OriginalLength *int              `json:"originalLength,omitempty"`
    Metadata       *DocumentMetadata `json:"metadata,omitempty"`
    Citation       *Citation         `json:"citation,omitempty"`
}

type DocumentMetadata struct {
    Title     string `json:"title,omitempty"`
    Author    string `json:"author,omitempty"`
    PageCount int    `json:"pageCount,omitempty"`
    CreatedAt string `json:"createdAt,omitempty"`
    FileSize  int64  `json:"fileSize,omitempty"`
}

type Citation struct {
    URL          string           `json:"url"`
    AccessedDate string           `json:"accessedDate"`
    Metadata     CitationMetadata `json:"metadata"`
    Formatted    CitationFormats  `json:"formatted"`
}

type CitationFormats struct {
    APA string `json:"apa"`
    MLA string `json:"mla"`
}
```

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
      ├─ Wait for: page stability (500ms) OR 30s timeout
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

### Cache
- Key: SHA-256 of (url + mode)
- TTL: 1 hour

### Error Conditions
- SSRF violation → return error, do not fetch
- Timeout → return partial content if available, else error
- 404/5xx → return error with HTTP status
- Empty content after extraction → return error

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

### Output Schema

```go
type SearchAndScrapeOutput struct {
    Query           string          `json:"query"`
    Sources         []SourceResult  `json:"sources"`
    CombinedContent string          `json:"combinedContent"`
    Summary         PipelineSummary `json:"summary"`
    SizeMetadata    SizeMetadata    `json:"sizeMetadata"`
}

type SourceResult struct {
    URL         string        `json:"url"`
    Title       string        `json:"title,omitempty"`
    Content     string        `json:"content"`
    ContentType string        `json:"contentType"`
    Scores      *QualityScore `json:"scores,omitempty"`
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
    ProcessingTimeMs int `json:"processingTimeMs"`
}
```

### Behavior

1. Execute search (via configured provider)
2. Scrape all result URLs in parallel (bounded concurrency: 5)
3. If `deduplicate`: paragraph-level hashing (djb2), remove >85% similar blocks
4. Score and rank sources by quality (weighted: relevance 35%, freshness 20%, authority 25%, content 20%)
5. If `filter_by_query`: extract keywords, remove sources below relevance threshold
6. Combine content, truncate to `total_max_length`
7. Return structured result with scores and metadata

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
4. Sort by `sort_by` parameter.
5. Return deduplicated articles.

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
- Without academic env vars, falls back to site-restricted web search (identical to previous behavior)
- Academic providers require only an email address (no API key registration)

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
- Strips HTML from API responses; extracts clean patent numbers from paths
- Normalizes assignee names (removes Inc/LLC/Corp/Ltd suffixes for matching)
- Region-aware routing: `patent_office` filters which providers are tried
- Post-filter results by patent number prefix when `patent_office` is specified
- Does not cache empty results (only caches when patents are found)

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
| `totalStepsEstimate` | int | no | — | Estimated total |
| `nextStepNeeded` | bool | yes | — | Whether more steps follow |
| `isRevision` | bool | no | false | Revising a previous step |
| `revisesStep` | int | no | — | Step being revised |
| `branchFromStep` | int | no | — | Branching point |
| `branchId` | string | no | — | Branch identifier |
| `knowledgeGap` | string | no | — | Gap identified |

### Session Management
- Sessions created on first call (stepNumber=1)
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
    SessionID          string          `json:"sessionId"`
    Question           string          `json:"question"`
    CurrentStep        int             `json:"currentStep"`
    TotalStepsEstimate int             `json:"totalStepsEstimate"`
    IsComplete         bool            `json:"isComplete"`
    Steps              []ResearchStep  `json:"steps"`
    Sources            []ResearchSource `json:"sources"`
    Gaps               []KnowledgeGap  `json:"gaps"`
    StartedAt          string          `json:"startedAt"`
    CompletedAt        string          `json:"completedAt,omitempty"`
}
```

### State Management
- In-memory `sync.Map` per tenant (requires session-affinity in multi-instance deployments)
- No cache (state tracking, not content)

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
| Content | Max Size |
|---------|----------|
| Single page content | 50 KB |
| Combined research content | 300 KB |
| Document download | 10 MB |
| YouTube transcript | 100 KB |

### Token Estimation
- Formula: `len(content) / 4` (conservative, ~4 chars per token)
- Size categories: small (<5K chars), medium (<20K), large (<50K), very_large (>=50K)
