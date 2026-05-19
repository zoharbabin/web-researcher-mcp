# Tool Specifications

Each tool is registered via `mcp.AddTool` with a typed input struct. This document specifies the contract for each tool — the schemas, behavior, caching, and error conditions that the implementation must satisfy.

> **Note:** Output schemas shown below describe the JSON shape returned by each tool. They are documentation of the response contract — see the corresponding `internal/tools/*.go` file for the actual implementation. Input schemas are auto-generated from the struct `jsonschema` tags.

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
| `lens` | string | no | — | Search lens name (NEW) |

### Output Schema

```go
type SearchOutput struct {
    URLs        []string `json:"urls"`
    Query       string   `json:"query"`
    ResultCount int      `json:"resultCount"`
}
```

### Behavior

1. If `lens` is specified and has a dedicated `cx`, route directly to that Google PSE engine.
2. If `lens` is specified without `cx`, inject `site:` operators and route to the configured provider.
3. Apply `time_range` as date restriction parameter.
4. Return deduplicated URLs.

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

### Academic Site Pool (site-restricted via configured provider)
arxiv.org, pubmed.ncbi.nlm.nih.gov, scholar.google.com, ieeexplore.ieee.org, dl.acm.org, nature.com, sciencedirect.com, link.springer.com, researchgate.net, plos.org, frontiersin.org, mdpi.com, wiley.com, jstor.org, semanticscholar.org, biorxiv.org, medrxiv.org

### Output Schema

```go
type AcademicSearchOutput struct {
    Papers       []AcademicPaper `json:"papers"`
    Query        string          `json:"query"`
    TotalResults int             `json:"totalResults"`
    ResultCount  int             `json:"resultCount"`
    Source       string          `json:"source"`
}

type AcademicPaper struct {
    Title    string            `json:"title"`
    Authors  []string          `json:"authors"`
    Year     int               `json:"year,omitempty"`
    Venue    string            `json:"venue,omitempty"`
    Abstract string            `json:"abstract,omitempty"`
    URL      string            `json:"url"`
    PDFURL   string            `json:"pdfUrl,omitempty"`
    DOI      string            `json:"doi,omitempty"`
    ArxivID  string            `json:"arxivId,omitempty"`
    Source   string            `json:"source"`
    Citations AcademicCitations `json:"citations"`
}
```

### Cache
- TTL: 24 hours (papers don't change)

---

## Tool 7: `patent_search`

### Input Schema

| Field | Type | Required | Default | Constraints |
|-------|------|----------|---------|-------------|
| `query` | string | yes | — | 1-500 chars |
| `num_results` | int | no | 5 | 1-10 |
| `search_type` | string | no | `prior_art` | prior_art, specific, landscape |
| `patent_office` | string | no | `all` | all, US, EP, WO, JP, CN, KR |
| `assignee` | string | no | — | Company name |
| `inventor` | string | no | — | Inventor name |
| `cpc_code` | string | no | — | CPC classification (e.g., G06F) |
| `year_from` | int | no | — | 1900-2030 |
| `year_to` | int | no | — | 1900-2030 |

### Behavior
- Generate company name variations (5-8 permutations: no spaces, with suffixes, base names)
- Map patent office to prefix codes
- Always uses `site:patents.google.com` restriction via configured provider
- Post-filter results by patent number prefix (US, EP, WO, JP, CN, KR) — non-matching patents are dropped when `patent_office` is specified

### Cache
- TTL: 24 hours

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
| `source` | object | no | — | Source found in this step |
| `knowledgeGap` | string | no | — | Gap identified |

### Session Management
- Sessions created on first call (stepNumber=1)
- Session ID: UUID v4, returned in output
- TTL: 30 minutes of inactivity (configurable)
- Max concurrent sessions: 50 per server instance
- Cleanup: goroutine every 5 minutes
- Per-tenant isolation: sessions keyed by `{tenantID}:{sessionID}`

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
- In single-instance: `sync.Map` per tenant
- In multi-instance: Redis hash per session (key: `session:{tenantID}:{sessionID}`)
- No cache (state tracking, not content)

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
