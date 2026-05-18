# Search Provider Architecture

## Background: Google PSE Sunset

Google will discontinue "Search the Entire Web" via Programmable Search Engine on **January 1, 2027**. This affects `web_search`, `image_search`, and `news_search` for unrestricted web queries.

**What still works indefinitely:** Site-restricted search (specifying domains). Our `academic_search` and `patent_search` already use this mode and are unaffected.

## Solution: Pluggable Providers + Search Lenses

### Provider Interface

```go
// search/provider.go

type WebSearchParams struct {
    Query        string
    NumResults   int
    TimeRange    string // day, week, month, year
    Safe         string // off, medium, high
    Language     string // ISO 639-1
    Country      string // ISO 3166-1 alpha-2
    Site         string // domain restriction
    ExactTerms   string
    ExcludeTerms string
}

type ImageSearchParams struct {
    Query         string
    NumResults    int
    Size          string
    Type          string
    ColorType     string
    DominantColor string
    FileType      string
    Safe          string
}

type NewsSearchParams struct {
    Query      string
    NumResults int
    Freshness  string // hour, day, week, month, year
    SortBy     string // relevance, date
    Source     string // domain filter
}

type SearchResult struct {
    Title       string `json:"title"`
    URL         string `json:"url"`
    Snippet     string `json:"snippet"`
    DisplayLink string `json:"displayLink"`
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

type NewsResult struct {
    Title       string `json:"title"`
    URL         string `json:"url"`
    Source      string `json:"source"`
    PublishedAt string `json:"publishedAt,omitempty"`
    Snippet     string `json:"snippet"`
}

type Provider interface {
    Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error)
    Images(ctx context.Context, params ImageSearchParams) ([]ImageResult, error)
    News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error)
    Name() string
}
```

### Available Providers

| Provider | Env Var | Whole-Web | Images | News | Cost/1K | Legal Risk |
|----------|---------|:-:|:-:|:-:|---------|:-:|
| **Google PSE** | `GOOGLE_CUSTOM_SEARCH_API_KEY` | Until 2027 | Yes | Yes | Free-$5 | None |
| **Brave Search** | `BRAVE_API_KEY` | Yes | Yes | Yes | $5 (free credit) | None |
| **Serper.dev** | `SERPER_API_KEY` | Yes | Yes | Yes | $0.30-$1 | HIGH |
| **SearXNG** | `SEARXNG_URL` | Yes | Yes | Yes | Free (self-hosted) | None |

### Routing Logic

```
Tool Call
    │
    ├─ lens specified? ──── YES ──→ Google PSE (site-restricted, free forever)
    │
    ├─ site: param set? ── YES ──→ Google PSE (site-restricted)
    │
    └─ unrestricted? ───── YES ──→ Configured provider (SEARCH_PROVIDER env)
                                    │
                                    ├─ "google" (default, sunset 2027 for whole-web)
                                    ├─ "brave" (recommended replacement)
                                    ├─ "serper" (opt-in, legal risk)
                                    └─ "searxng" (self-hosted)
```

### Configuration

```bash
# Required: Google PSE (for lenses, academic, patent — works forever)
GOOGLE_CUSTOM_SEARCH_API_KEY=AIzaSy...
GOOGLE_CUSTOM_SEARCH_ID=017...

# Default provider for unrestricted whole-web search
SEARCH_PROVIDER=brave          # brave | google | serper | searxng

# Provider-specific keys
BRAVE_API_KEY=BSA...           # Required if SEARCH_PROVIDER=brave
SERPER_API_KEY=...             # Required if SEARCH_PROVIDER=serper
SEARXNG_URL=http://localhost:8080  # Required if SEARCH_PROVIDER=searxng
```

---

## Search Lenses

Curated domain lists that route through Google PSE in site-restricted mode. Free forever, high-quality Google ranking within scope.

### How They Work

1. User specifies `lens: "programming"` in the `web_search` call
2. Server loads `lenses/programming.json`
3. Injects `site:` operators into the Google PSE query
4. For lenses with >10 domains: use a dedicated `cx` engine ID

### Built-in Lenses

| Lens | Domains | Use Case |
|------|---------|----------|
| `programming` | stackoverflow.com, github.com, developer.mozilla.org, docs.python.org, learn.microsoft.com, dev.to, go.dev, nodejs.org, rust-lang.org, cppreference.com | Code help, API docs |
| `news` | reuters.com, apnews.com, bbc.com, nytimes.com, theguardian.com, aljazeera.com, npr.org, cnn.com | Current events |
| `tech` | arstechnica.com, techcrunch.com, theverge.com, wired.com, theregister.com, zdnet.com | Technology news |
| `legal` | law.cornell.edu, courtlistener.com, findlaw.com, justia.com | Legal research |
| `medical` | nih.gov, mayoclinic.org, who.int, cdc.gov, pubmed.ncbi.nlm.nih.gov, medlineplus.gov | Health info |
| `finance` | sec.gov, yahoo.com/finance, bloomberg.com, investopedia.com, wsj.com, ft.com | Financial research |
| `science` | nature.com, science.org, nasa.gov, phys.org, scientificamerican.com | Science |
| `government` | *.gov, europa.eu, gov.uk, un.org, canada.ca | Policy, regulations |

### Lens File Format

```json
{
  "name": "programming",
  "description": "Programming documentation, tutorials, and Q&A sites",
  "domains": [
    "stackoverflow.com",
    "*.github.com",
    "developer.mozilla.org",
    "docs.python.org",
    "learn.microsoft.com",
    "dev.to",
    "go.dev",
    "nodejs.org",
    "rust-lang.org",
    "cppreference.com",
    "docs.oracle.com",
    "kotlinlang.org",
    "pkg.go.dev",
    "crates.io",
    "pypi.org"
  ],
  "cx": ""
}
```

- `domains`: Up to 5,000 URL patterns per lens (PSE limit)
- `cx`: Optional dedicated PSE engine ID for this lens (pre-configured with domains in Google PSE console)
- If `cx` is empty: inject `site:` operators into query at call time (limited to ~10 per query)

### Custom Lenses

Users can add lenses by:
1. Creating a JSON file in the `lenses/` directory
2. Setting `CUSTOM_LENSES_PATH` to an external directory
3. Via MCP admin endpoint (future)

---

## Provider Implementations

### Brave Search Adapter

```go
// search/brave.go

type BraveProvider struct {
    apiKey     string
    httpClient *http.Client
    baseURL    string
}

func (b *BraveProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
    // GET https://api.search.brave.com/res/v1/web/search?q=...
}

func (b *BraveProvider) Images(ctx context.Context, params ImageSearchParams) ([]ImageResult, error) {
    // GET https://api.search.brave.com/res/v1/images/search?q=...
}

func (b *BraveProvider) News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
    // GET https://api.search.brave.com/res/v1/news/search?q=...
}
```

**Brave API features:**
- $5/month free credit (1,000 searches)
- SOC2 certified, Zero Data Retention available
- Independent 40B+ page index (not Google-derived)
- Goggles for result re-ranking (future use)
- Country/language/freshness filters built-in

### Google PSE Adapter

```go
// search/google.go

type GoogleProvider struct {
    apiKey     string
    cx         string
    httpClient *http.Client
}

func (g *GoogleProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
    // GET https://www.googleapis.com/customsearch/v1?key=...&cx=...&q=...
}
```

**Google PSE notes:**
- Free: 100 queries/day per engine
- Paid: $5/1,000 queries after that
- Site-restricted mode: works forever (no sunset)
- Whole-web mode: ends Jan 1, 2027

### Serper Adapter (Opt-In)

```go
// search/serper.go — legal risk: Google sued SerpApi (Dec 2025)

type SerperProvider struct {
    apiKey     string
    httpClient *http.Client
}
```

**Serper notes:**
- Cheapest ($0.30-$1/1K queries)
- Returns Google-identical results (it scrapes Google)
- Legal risk: Google actively suing SerpApi (same category)
- User must explicitly opt-in via `SEARCH_PROVIDER=serper`

### SearXNG Adapter (Self-Hosted)

```go
// search/searxng.go

type SearXNGProvider struct {
    baseURL    string
    httpClient *http.Client
}
```

**SearXNG notes:**
- Free, open source, meta-search engine
- Aggregates results from Google, Bing, DuckDuckGo, etc.
- Requires self-hosting (Docker one-liner)
- No API key needed, no rate limits (you control the instance)
- Best for enterprise/air-gapped deployments

---

## Migration Path

| Phase | Timing | Action |
|-------|--------|--------|
| 1 | Day 1 | Ship with Google PSE as default + lenses. All existing behavior preserved. |
| 2 | v1.1 | Add Brave adapter. Switch default for unrestricted to Brave. |
| 3 | v1.2 | Add Serper + SearXNG adapters. |
| 4 | Before Jan 2027 | Log deprecation warning when Google PSE whole-web is used without lens. |
| 5 | After Jan 2027 | Remove Google whole-web code path. Lenses + site-restricted remain. |

---

## Fallback Chain (Resilience)

When the primary provider fails (rate limit, timeout, error):

```
Primary Provider (e.g., Brave)
    │ fails
    ▼
Fallback Provider (e.g., Google PSE)
    │ fails
    ▼
Circuit Breaker OPEN → Return error to client
```

Configuration:
```bash
SEARCH_PROVIDER=brave
SEARCH_FALLBACK_PROVIDER=google  # optional
```

Each provider has its own circuit breaker. If the primary opens, traffic routes to fallback until the primary resets.
