# Setting Up Your Search Keys

This guide shows you how to get the API keys that power your searches. You only need one provider to get started — or set up several, and the server will automatically switch between them if one goes down.

## How to Configure Keys

Pass API keys as environment variables. How you set them depends on your MCP client:

**Claude Code** (CLI / VS Code / JetBrains):
```json
// In ~/.claude.json under "mcpServers":
{
  "web-researcher": {
    "command": "web-researcher-mcp",
    "env": {
      "BRAVE_API_KEY": "your-key",
      "EPO_OPS_CONSUMER_KEY": "your-key",
      "EPO_OPS_CONSUMER_SECRET": "your-secret"
    }
  }
}
```

**Claude Desktop**:
```json
// In ~/Library/Application Support/Claude/claude_desktop_config.json (macOS)
// or %APPDATA%\Claude\claude_desktop_config.json (Windows)
{
  "mcpServers": {
    "web-researcher": {
      "command": "/path/to/web-researcher-mcp",
      "env": {
        "BRAVE_API_KEY": "your-key"
      }
    }
  }
}
```

**Shell (direct / Docker)**:
```bash
export BRAVE_API_KEY=your-key
web-researcher-mcp
```

Keys set in the MCP client config are passed directly to the server process — no `.env` file needed.

---

## DuckDuckGo (Zero-Config Default)

**Free**: No API key, no registration, no query limits to configure.

DuckDuckGo is the built-in fallback and works out of the box. If you set no provider keys at all, web search still works through DuckDuckGo. There is nothing to configure — but you can select it explicitly:

```bash
export SEARCH_PROVIDER=duckduckgo
web-researcher-mcp
```

For better result quality and higher volume, add one of the keyed providers below and the server will prefer it.

---

## Hacker News (Zero-Config)

**Free**: No API key, no registration. Searches Hacker News stories through the public [HN Algolia](https://hn.algolia.com/) index.

This is a domain-specific provider — it returns Hacker News stories only, not general web results. Select it when you want HN discussion and submission results:

```bash
export SEARCH_PROVIDER=hackernews
web-researcher-mcp
```

### Good to know

- **`web_search` and `news_search` only.** `image_search` with Hacker News returns empty (no error). Keep an image-capable provider (Google, Brave, SearchAPI) in `SEARCH_ROUTING` if you need images.
- **Date filtering works.** `dateRange` is honored via the Algolia `numericFilters` (`created_at_i`); `num_results` accepts 1–100 (values outside that range reset to the default of 10).
- **Reading threads.** `scrape_page` on a `news.ycombinator.com` item, user, or list URL is read natively through the HN Firebase API (story + top comments) — independent of which `SEARCH_PROVIDER` is set.

---

## Google Custom Search (Programmable Search Engine)

**Free tier**: 100 queries/day (paid: $5 per 1,000 queries)

### Step 1: Get an API Key

1. Go to the [Google Cloud Console](https://console.cloud.google.com/)
2. Create a new project (or select an existing one)
3. Navigate to **APIs & Services > Library**
4. Search for "Custom Search API" and enable it
5. Go to **APIs & Services > Credentials**
6. Click **Create Credentials > API Key**
7. Copy the key

### Step 2: Create a Programmable Search Engine

1. Go to [Programmable Search Engine](https://programmablesearchengine.google.com/)
2. Click **Add** to create a new search engine
3. Under "What to search", select **Search the entire web**
4. Give it a name and click **Create**
5. Copy the **Search Engine ID** (cx)

### Step 3: Configure

```bash
export GOOGLE_CUSTOM_SEARCH_API_KEY=AIzaSy...your-key
export GOOGLE_CUSTOM_SEARCH_ID=your-search-engine-id
```

---

## Brave Search

**Free tier**: 2,000 queries/month (paid plans available)

### Step 1: Get an API Key

1. Go to [Brave Search API](https://brave.com/search/api/)
2. Click **Get Started** and create an account
3. Subscribe to the **Free** plan (or a paid plan for higher volume)
4. Navigate to your dashboard and copy your API key

### Step 2: Configure

```bash
export BRAVE_API_KEY=BSAxxxxxxxxxxxxxxxxxx
```

To use Brave as your primary (or only) provider:

```bash
export SEARCH_PROVIDER=brave
export BRAVE_API_KEY=BSAxxxxxxxxxxxxxxxxxx
```

---

## Serper.dev

**Free tier**: 2,500 queries (one-time credit, then paid)

### Step 1: Get an API Key

1. Go to [Serper.dev](https://serper.dev/)
2. Sign up for an account
3. Your API key is shown on the dashboard immediately after sign-up
4. Copy the key

### Step 2: Configure

```bash
export SERPER_API_KEY=your-serper-key
```

To use Serper as your primary provider:

```bash
export SEARCH_PROVIDER=serper
export SERPER_API_KEY=your-serper-key
```

---

## Tavily

**Free tier**: monthly credits for development; paid plans for higher volume

Tavily is a search API purpose-built for AI agents — it returns clean, extracted, LLM-ready content rather than raw result pages. It supports web and news search (no native image search; image queries fall through to another provider when routing is enabled).

### Step 1: Get an API Key

1. Go to [app.tavily.com](https://app.tavily.com/)
2. Sign up for an account
3. Copy the `tvly-...` API key from your dashboard

### Step 2: Configure

```bash
export SEARCH_PROVIDER=tavily
export TAVILY_API_KEY=tvly-your-key
```

The key is sent as an `Authorization: Bearer` header (never in the request body), and queries are capped at Tavily's 400-character limit automatically.

### Good to know

- **No image search.** `image_search` with Tavily returns empty (no error). Keep an image-capable provider (Google, Brave, SearchAPI) in `SEARCH_ROUTING` if you need images — the Router falls through automatically. Best used as a routing member rather than the sole provider: `SEARCH_ROUTING=tavily,brave,google`.
- **Web `time_range` is strict.** Tavily's web recency filter is aggressive — a `time_range=week` web search may return nothing for terms that have older results. For recent *news* use `news_search` (its `freshness` window works well); for recent *web* content, widen `time_range` or omit it.
- **Some filters don't apply.** Tavily honors `site`, `lens`, `num_results`, `time_range`/`freshness`, but ignores `country`, `language`, `safe`, and exact/exclude-term filters (it has no API field for them). Use Google if you need hard country/language/exact-phrase control.

---

## Exa

**Free tier**: 1,000 requests/month; paid per call beyond that

Exa is a neural/semantic search API. Beyond ordinary web and news search, an Exa key unlocks several capabilities no other provider offers here:

- **Grounded answers** — backs the provider-independent `answer` tool (one synthesized answer with citations).
- **Structured extraction** — backs the provider-independent `structured_search` tool (schema-defined fields and company/people entities, as JSON per result).
- **Academic search** — `academic_search` can route to Exa via the research-paper category.
- **A paid scrape fallback** — Exa's `/contents` API becomes the last-resort extraction tier for `scrape_page`, recovering hard pages the free tiers can't (only when the local tiers all fail).

### Step 1: Get an API Key

1. Go to [dashboard.exa.ai](https://dashboard.exa.ai/)
2. Sign up for an account
3. Copy your API key from the dashboard

### Step 2: Configure

```bash
export SEARCH_PROVIDER=exa
export EXA_API_KEY=your-exa-key
```

The key is sent as the `x-api-key` header (never in the request body or logs).

### Good to know

- **Paid per call.** Exa charges per request (free tier: 1,000/month). Each `answer` / `structured_search` response (when served by Exa) reports the estimated `costUsd` of that call, and the cost is recorded in the audit trail as `cost_usd`. The estimate is not an invoice.
- **No image search.** `image_search` with Exa returns empty (no error) — keep an image-capable provider (Google, Brave, SearchAPI) in `SEARCH_ROUTING` if you need images.
- **Search type is fixed to `auto`.** The expensive deep/deep-reasoning tiers are deliberately not exposed; `auto` is the balanced, predictable-cost default.
- **The scrape fallback is opt-in by cost.** The Exa `/contents` tier runs only when the free scrape tiers (markdown → stealth → HTML → browser, when Chrome is configured) all fail to extract content — the common path never spends an Exa credit on scraping.
- **Best used as a routing member** when you also want a free default: `SEARCH_ROUTING=brave,exa`.

---

## SearXNG (Self-Hosted)

**Free**: Open source, self-hosted — no API key needed, no query limits

SearXNG is a privacy-respecting metasearch engine that aggregates results from multiple sources. Ideal for air-gapped deployments or organizations that need full control over search infrastructure.

### Step 1: Run SearXNG

The fastest way is Docker:

```bash
docker run -d --name searxng \
  -p 8080:8080 \
  -e SEARXNG_SECRET=your-secret-key \
  searxng/searxng:latest
```

For production deployments, see the [SearXNG documentation](https://docs.searxng.org/) for configuration options (engine selection, rate limiting, result ranking).

### Step 2: Enable JSON API

SearXNG needs the JSON format enabled. Create or edit `settings.yml`:

```yaml
search:
  formats:
    - html
    - json
```

### Step 3: Configure

```bash
export SEARCH_PROVIDER=searxng
export SEARXNG_URL=http://localhost:8080
```

### Step 4: Authenticating to a protected SearXNG (optional)

If your instance is behind HTTP Basic auth or a reverse proxy that requires a token, supply the credential at deploy time. Both variables are optional — unset, the server talks to SearXNG exactly as before.

```bash
# HTTP Basic auth (the most common case):
export SEARXNG_BASIC_AUTH=user:password   # everything after the first ':' is the password, so colons in the password are fine

# Non-Basic schemes (bearer token, Cloudflare Access service token, API-gateway shared secret) —
# comma-separated "Name: Value" pairs:
export SEARXNG_HEADERS="X-Proxy-Token: abc123, CF-Access-Client-Id: client.id"
```

Notes:

- **Never logged.** The credential and header values never appear in logs, errors, or audit records — messages name only the variable or the header name.
- **Fail-closed & validated.** A malformed value — Basic auth without a `user:password` shape, a header missing its `:`, an invalid header name, or a newline/control character in a value — is rejected at startup and never sent on the wire. In HTTP mode (`PORT` set) the server refuses to start; in STDIO mode it logs the error and drops the bad value (matching the existing zero-config startup behavior). Either way the malformed credential is never used.
- **No commas or newlines inside a header value** — commas delimit the pairs, and newlines are rejected to prevent header injection.
- **Precedence.** A custom `Authorization` header in `SEARXNG_HEADERS` overrides `SEARXNG_BASIC_AUTH` (last writer wins), which lets a bearer-token proxy take priority.
- Auth applies whenever `SEARXNG_URL` is set — including when SearXNG is only a `SEARCH_ROUTING` or fallback target, not just when `SEARCH_PROVIDER=searxng`.
- Never commit real credentials; set these as deployment secrets.

---

## SearchAPI.io

**Free tier**: 100 searches/month (paid plans available)

### Step 1: Get an API Key

1. Go to [SearchAPI.io](https://www.searchapi.io/)
2. Sign up for an account
3. Navigate to your dashboard
4. Copy your API key

### Step 2: Configure

```bash
export SEARCHAPI_API_KEY=your-searchapi-key
```

To use SearchAPI.io as your primary provider:

```bash
export SEARCH_PROVIDER=searchapi
export SEARCHAPI_API_KEY=your-searchapi-key
```

---

## Multi-Provider Routing

For maximum reliability, configure multiple providers and let the server route automatically with fallback:

```bash
# All providers configured
export BRAVE_API_KEY=BSA...
export GOOGLE_CUSTOM_SEARCH_API_KEY=AIza...
export GOOGLE_CUSTOM_SEARCH_ID=017...
export SERPER_API_KEY=...

# Priority-ordered routing with automatic failover
export SEARCH_ROUTING=brave,google,serper
```

If Brave is down or rate-limited, requests automatically switch to Google, then Serper. If one provider starts failing repeatedly, the server stops trying it and routes to the next one.

For per-operation routing (different providers for different search types):

```bash
export SEARCH_ROUTING='{"web":"brave,google","news":"brave,serper","images":"google,brave","academic":"openalex,crossref","patents":"epo,lens,searchapi,uspto","default":"brave,google,searchapi"}'
```

See [docs/DEPLOYMENT.md](DEPLOYMENT.md) for advanced routing configuration.

---

## Choosing a Provider

Not sure which provider to pick? See **[docs/PROVIDERS.md](PROVIDERS.md)** for a full comparison: index classification (own index vs. Google-backed vs. aggregator), capability matrix per tool, free-tier limits, and a quick-pick guide.

**Short recommendation**: Start with Brave (2,000/month free, own independent index) and add Google as a fallback. Use `SEARCH_ROUTING=brave,google` for a good balance of coverage and reliability.

---

## Patent Search Providers (Optional)

These providers enable structured patent search via `patent_search`. All are optional — without them, patent search falls back to web discovery via your configured web search provider.

### EPO Open Patent Services (Worldwide)

Free access to 100M+ patent documents across all major offices.

**Step 1**: Register at [developers.epo.org](https://developers.epo.org) and create an app with "OPS - EPO OPS Core APIs" enabled.

**Step 2**: Configure

```bash
export EPO_OPS_CONSUMER_KEY=your-consumer-key
export EPO_OPS_CONSUMER_SECRET=your-consumer-secret
```

**Notes**: Free tier is rate-limited (throttled, not hard-capped). Authentication uses OAuth2 Client Credentials (handled automatically). Coverage is worldwide — all patent offices.

### USPTO (US Patents)

Access to US patent applications and grants.

**Step 1**: Request an API key at [data.uspto.gov](https://data.uspto.gov).

**Step 2**: Configure

```bash
export USPTO_API_KEY=your-api-key
```

**Notes**: Covers US patents only. Queries for non-US patent offices automatically skip this provider.

### The Lens (Worldwide + Scholarly Links)

Access to 100+ jurisdictions with links between patents and scholarly works.

**Step 1**: Register at [lens.org](https://www.lens.org) and request API access from your account settings.

**Step 2**: Configure

```bash
export LENS_API_TOKEN=your-api-token
```

**Notes**: Free tier allows limited monthly requests. Unique capability: links patents to citing academic papers.

### Patent Routing

When you have multiple patent providers configured, the server tries each one in order — if the first doesn't have results, it moves to the next:

```bash
export SEARCH_ROUTING='{"patents":"epo,lens,searchapi,uspto","default":"brave,google"}'
```

Without explicit routing, all configured patent providers are tried in order until one returns results. The `patent_office` parameter in search requests enables intelligent routing — e.g., a search restricted to `EP` skips USPTO automatically.

---

## Academic Search Providers (Optional)

These providers enable structured scholarly search via `academic_search`. All are optional — without them, academic search falls back to site-restricted web discovery via your configured web search provider.

### OpenAlex (Worldwide — 287M+ Works)

Open scholarly metadata covering all academic disciplines. Returns DOIs, authors with affiliations, citation counts, open-access status, PDF links, and funding data.

**Step 1**: No registration needed — just provide a contact email for the polite pool (100 RPS instead of 10 RPS).

**Step 2**: Configure

```bash
export OPENALEX_EMAIL=you@example.com
```

**Notes**: CC0 data, no API key required. The email is used for the "polite pool" (higher rate limits, priority support). Abstracts are returned in inverted index format and reconstructed automatically.

### CrossRef (Worldwide — 140M+ DOI Works)

Authoritative DOI metadata with 99.94% documented uptime. Returns structured journal metadata, publication dates, and citation counts for peer-reviewed works.

**Step 1**: No registration needed — just provide a contact email for the polite pool (50 RPS instead of 5 RPS).

**Step 2**: Configure

```bash
export CROSSREF_EMAIL=you@example.com
```

**Notes**: The email is used for the polite pool (higher rate limits). CrossRef is the official DOI registration agency — every DOI-registered work appears here with authoritative metadata.

### Semantic Scholar (Worldwide — 200M+ Papers)

Adds AI `tldr` summaries and citation intent/influence signals, and powers `citation_graph` with rich edges. Works **without** a key at a lower shared rate limit; a key raises throughput.

**Step 1**: (Optional) Request a key at [semanticscholar.org/product/api](https://www.semanticscholar.org/product/api).

**Step 2**: Configure (optional)

```bash
export SEMANTIC_SCHOLAR_API_KEY=your-key
```

**Notes**: Keyless use is rate-limited by a shared public pool and may return a `rate_limited` error under load — set a key to avoid this. Also selectable as a `citation_graph` provider.

### PubMed (Biomedical Literature — 35M+ Citations)

NIH's NCBI E-utilities index of biomedical and life-science literature. Works **keyless** at ~3 requests/second; a free API key raises it to ~10 req/s.

**Step 1**: (Optional) Sign in at [ncbi.nlm.nih.gov/account](https://www.ncbi.nlm.nih.gov/account) and go to **Settings → API Key Management** to generate a key.

**Step 2**: Configure (both are optional)

```bash
export PUBMED_API_KEY=your-ncbi-key     # raises rate limit (~10 req/s)
export PUBMED_EMAIL=you@example.com     # NCBI contact param; falls back to OPENALEX_EMAIL when unset
```

**Notes**: Keyless use works out of the box. A key is recommended for sustained or high-volume use. `PUBMED_EMAIL` falls back to `OPENALEX_EMAIL` — setting the OpenAlex email is sufficient to cover both. Also selectable as an `academic_search` provider via `provider: pubmed`.

### Unpaywall (Open-Access Enrichment)

Not a search provider — it fills free-PDF links on DOI-bearing `academic_search` results that lack one. Best-effort; never fails or slows a search beyond its own bounded request.

**Step 1**: No registration — just provide a contact email.

**Step 2**: Configure

```bash
export UNPAYWALL_EMAIL=you@example.com
```

**Notes**: Falls back to `OPENALEX_EMAIL` when unset; a complete no-op when neither is set.

### Academic Routing

When multiple academic providers are configured, the router tries them in priority order with automatic fallback:

```bash
export SEARCH_ROUTING='{"academic":"openalex,crossref","patents":"epo,lens","default":"brave,google"}'
```

Without explicit routing, all configured academic providers are tried in order until one returns results. If no academic providers are configured, `academic_search` automatically falls back to site-restricted web search.

## Structured-Domain Providers (Optional)

These enable dedicated structured-research tools. Each is independent; `filing_search` is the only one that requires configuration (a contact email). The rest are always available — see each section for optional keys that raise rate limits or add data sources.

### SEC EDGAR (US Public-Company Filings)

Backs `filing_search`. SEC requires a contact email in the request User-Agent — there is **no API key**.

**Step 1**: No registration. SEC asks only that automated requests identify a contact email.

**Step 2**: Configure

```bash
export EDGAR_CONTACT_EMAIL=you@example.com
```

**Notes**: Falls back to `OPENALEX_EMAIL` if `EDGAR_CONTACT_EMAIL` is unset; `filing_search` registers only when one of the two is set. Returns recent filings or, with `facts=true`, structured XBRL company facts passed through exactly as filed.

### CourtListener (US Case Law)

Backs `legal_search`. Works **keyless** — `legal_search` is always available. An optional token raises the rate limit.

**Step 1**: (Optional) Register at [courtlistener.com](https://www.courtlistener.com) and create an API token in your account settings.

**Step 2**: Configure (optional)

```bash
export COURTLISTENER_API_TOKEN=your-token
```

**Notes**: Without a token, roughly 100 requests/day; a token raises this to ~5000/day. Coverage is US federal and state court opinions.

### World Bank + OECD + Eurostat (keyless) + FRED (key)

`econ_search` is backed by four providers. **World Bank Open Data**, **OECD**, and **Eurostat** are all keyless and always available. **FRED** (Federal Reserve Economic Data) adds 800K+ US macro series and needs a free key.

- World Bank (`provider: worldbank`) — global development indicators for 200+ economies, scope by `country`
- OECD (`provider: oecd`) — OECD economy indicators via SDMX
- Eurostat (`provider: eurostat`) — European official statistics
- FRED (`provider: fred`) — US macro series (GDP, CPI, unemployment, rates)

So `econ_search` works out of the box (World Bank, OECD, Eurostat); add the FRED key to also reach US macro series.

**FRED — Step 1**: Request a free key at [fred.stlouisfed.org](https://fred.stlouisfed.org/docs/api/api_key.html) (sign in → My Account → API Keys).

**FRED — Step 2**: Configure

```bash
export FRED_API_KEY=your-fred-key
```

**Notes**: World Bank, OECD, and Eurostat require no configuration. FRED is enabled by `FRED_API_KEY`. Observation values pass through exactly as each source returns them — no rounding; the FRED key is sent as a query param and never logged.

### ClinicalTrials.gov (Clinical Trials)

Backs `clinical_search`. Works **keyless** — `clinical_search` is always available. No registration or API key.

**Notes**: Queries the ClinicalTrials.gov v2 API (NIH registry of 400K+ studies). Returns trial registrations as typed data (status, phase, sponsor, conditions, interventions, results availability); read the full record via `scrape_page` on the returned `url`. Discovery + primary-source retrieval only — not medical advice.

### Internet Archive — Save Page Now (Optional, for `archive_source`)

The `archive_source` tool triggers an Internet Archive Save Page Now (SPN) capture. It works **keyless** by default — no registration is required. An optional S3-style key pair raises the rate limit and improves capture reliability for high-volume use.

**Step 1**: (Optional) Sign in at [archive.org/account/s3.php](https://archive.org/account/s3.php) to generate an access/secret key pair.

**Step 2**: Configure (both are optional)

```bash
export IA_ACCESS_KEY=your-ia-access-key
export IA_SECRET_KEY=your-ia-secret-key
```

**Notes**: Both keys are required together — set neither or both. Values are never logged or included in error messages. Keyless SPN is sufficient for occasional archiving; keys are recommended for production deployments that archive frequently.
