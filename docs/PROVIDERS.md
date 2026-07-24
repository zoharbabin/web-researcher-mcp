# Provider Reference

This document covers every search and data provider supported by web-researcher-mcp: what backs each one, which tools it enables, free-tier limits, and when to choose it. For setup instructions (API keys, env vars, routing config) see [API_SETUP.md](API_SETUP.md).

---

## Contents

- [Web Search Providers](#web-search-providers)
  - [Index Classification](#index-classification)
  - [Capability Matrix](#capability-matrix)
  - [Free Tier and Pricing](#free-tier-and-pricing)
  - [Quick-Pick Guide](#quick-pick-guide)
  - [Provider Notes](#provider-notes)
- [Academic Search Providers](#academic-search-providers)
  - [Capability Matrix](#capability-matrix-1)
  - [Coverage](#coverage)
  - [Academic Routing](#academic-routing)
- [Patent Search Providers](#patent-search-providers)
  - [Jurisdiction Matrix](#jurisdiction-matrix)
  - [Patent Routing](#patent-routing)
- [Structured-Domain Providers](#structured-domain-providers)
- [Multi-Provider Routing](#multi-provider-routing)

---

## Web Search Providers

### Index Classification

Understanding what backs each provider helps you reason about result overlap and independence.

| Provider | Index Type | What backs the results |
|---|---|---|
| **[DuckDuckGo](https://duckduckgo.com/)** | Bing-sourced | Microsoft Bing (+ Bing-syndicated sources) |
| **[Google PSE](https://programmablesearchengine.google.com/)** | Own index | Google's web index |
| **[Serper](https://serper.dev/)** | Google-backed | Google's web index via API |
| **[SearchAPI.io](https://www.searchapi.io/)** | Google-backed | Google's web index via API |
| **[Brave](https://brave.com/search/api/)** | Own independent | Brave's own web crawler and index |
| **[Exa](https://exa.ai/)** | Own independent | Neural/embedding-based web index |
| **[Tavily](https://app.tavily.com/)** | Aggregator | Queries multiple existing engines at runtime; scrapes top results; applies AI re-ranking — no proprietary crawled index |
| **[SearXNG](https://docs.searxng.org/)** | Meta-search | Configurable — routes to whatever backends you point it at (Bing, Google, DuckDuckGo, and others) |
| **[HackerNews](https://hn.algolia.com/)** | Niche | HN Algolia index — Hacker News stories and submissions only |
| **[Reddit](https://www.reddit.com/)** | Niche | Reddit Atom RSS — Reddit posts and community discussions only |
| **[Bluesky](https://bsky.app/)** | Niche | AT Protocol AppView (`public.api.bsky.app`) — Bluesky posts only |
| **[GitHub](https://docs.github.com/en/rest/search/search)** | Niche | GitHub REST Search API — issues and pull requests only |

**Practical implication**: Google PSE, Serper, and SearchAPI.io draw from the same index — using more than one adds no coverage, only redundancy. Brave and Exa bring genuinely independent results. Tavily and SearXNG aggregate results from others rather than crawling themselves.

---

### Capability Matrix

Which tools each web search provider enables. `—` means the provider returns empty (no error) for that capability — image-capable providers in `SEARCH_ROUTING` will handle the fallback automatically.

| Provider | `web_search` | `image_search` | `news_search` | `answer` | `structured_search` | `local_search` | Scrape fallback tier |
|---|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| **[DuckDuckGo](https://duckduckgo.com/)** | ✓ | ✓ | ✓ | — | — | — | — |
| **[Google PSE](https://programmablesearchengine.google.com/)** | ✓ | ✓ | ✓ | — | — | — | — |
| **[Serper](https://serper.dev/)** | ✓ | ✓ | ✓ | — | — | — | — |
| **[SearchAPI.io](https://www.searchapi.io/)** | ✓ | ✓ | ✓ | — | — | — | — |
| **[Brave](https://brave.com/search/api/)** | ✓ | ✓ | ✓ | — | — | ✓ | — |
| **[Exa](https://exa.ai/)** | ✓ | — | ✓ | ✓ | ✓ | — | ✓ (paid, last-resort) |
| **[Tavily](https://app.tavily.com/)** | ✓ | — | ✓ | — | — | — | — |
| **[SearXNG](https://docs.searxng.org/)** | ✓ | ✓ | ✓ | — | — | — | — |
| **[HackerNews](https://hn.algolia.com/)** | ✓ | — | ✓ | — | — | — | — |
| **[Reddit](https://www.reddit.com/)** | ✓ | — | ✓ | — | — | — | — |
| **[Bluesky](https://bsky.app/)** | ✓ | — | — | — | — | — | — |
| **[GitHub](https://docs.github.com/en/rest/search/search)** | ✓ | — | ✓ | — | — | — | — |

**Notes:**
- `answer` and `structured_search` are provider-independent tools, but Exa is the only web provider that backs them with its native API. They remain unavailable if no Exa key is set.
- `local_search` is Brave-only — it requires `BRAVE_API_KEY`. No other web provider supports the three-call local pipeline (locations → POIs → descriptions).
- Brave also exposes a LLM context endpoint (`/res/v1/llm/context`) consumed by `search_and_scrape` as a fast-path for RAG/grounding workflows. When Brave is the active provider, `search_and_scrape` tries the server-assembled context first; if that fails, it falls back to the standard search-then-scrape pipeline. Requires `BRAVE_DATA_FOR_AI` plan access.
- Exa's scrape fallback tier (`/contents`) fires only when all four free tiers (markdown → stealth → HTML → browser) have failed. It charges an Exa credit per call.
- Tavily's time-range filter is aggressive on web search — for recent content, `news_search` works better; `web_search` may return nothing for narrow windows.

---

### Free Tier and Pricing

| Provider | Free Tier | Paid |
|---|---|---|
| **[DuckDuckGo](https://duckduckgo.com/)** | Unlimited | Free |
| **[HackerNews](https://hn.algolia.com/)** | Unlimited | Free |
| **[Reddit](https://www.reddit.com/)** | Unlimited | Free |
| **[Bluesky](https://bsky.app/)** | Unlimited (public AppView) | Free |
| **[GitHub](https://docs.github.com/en/rest/search/search)** | 10 req/min (unauth) / 30 req/min (token) | Free |
| **[SearXNG](https://docs.searxng.org/)** | Unlimited (self-hosted) | Free (self-hosted) |
| **[Google PSE](https://programmablesearchengine.google.com/)** | 100 queries/day | $5 / 1,000 queries |
| **[Brave](https://brave.com/search/api/)** | 2,000 queries/month | Paid plans |
| **[Serper](https://serper.dev/)** | 2,500 queries (one-time) | Paid plans |
| **[SearchAPI.io](https://www.searchapi.io/)** | 100 searches/month | Paid plans |
| **[Exa](https://exa.ai/)** | 1,000 requests/month | Per call beyond free tier |
| **[Tavily](https://app.tavily.com/)** | Monthly dev credits | Paid plans |

---

### Quick-Pick Guide

| If you need… | Use |
|---|---|
| Zero-config, no signup | [DuckDuckGo](https://duckduckgo.com/) (built-in fallback), [HackerNews](https://hn.algolia.com/) (HN-only), [Reddit](https://www.reddit.com/) (Reddit-only), or [Bluesky](https://bsky.app/) (Bluesky-only) |
| Broadest index coverage | [Google PSE](https://programmablesearchengine.google.com/) |
| High-volume + own index | [Brave](https://brave.com/search/api/) (2,000/month free, privacy-first) |
| Independent results alongside Google | [Brave](https://brave.com/search/api/) or [Exa](https://exa.ai/) (different indices, no overlap) |
| Semantic / conceptual search | [Exa](https://exa.ai/) |
| LLM-ready extracted content | [Tavily](https://app.tavily.com/) |
| `answer` or `structured_search` tools | [Exa](https://exa.ai/) (required) |
| Air-gapped or no vendor lock-in | [SearXNG](https://docs.searxng.org/) (self-hosted) |
| Tech/developer community signal | [HackerNews](https://hn.algolia.com/) |
| Reddit / community discussion signal | [Reddit](https://www.reddit.com/) |
| Bluesky community signal | [Bluesky](https://bsky.app/) |
| GitHub issue and PR search | [GitHub](https://docs.github.com/en/rest/search/search) |
| Maximum reliability | `SEARCH_ROUTING=brave,google,serper` (three independent providers) |

---

### Provider Notes

**[DuckDuckGo](https://duckduckgo.com/)** — The zero-config default. No API key, no registration, no rate limit to configure. Result depth is lower than keyed providers; image and news results are present but less comprehensive. Use as a fallback, not a primary.

**[Google PSE](https://programmablesearchengine.google.com/)** — The largest index. Best for broadest coverage, image search, and exact-phrase queries. Requires both an API key (via [Google Cloud Console](https://console.cloud.google.com/)) and a Programmable Search Engine ID. Free tier of 100/day is low for sustained use.

**[Serper](https://serper.dev/) and [SearchAPI.io](https://www.searchapi.io/)** — Google results without the PSE setup overhead. Serper is the simpler option; SearchAPI.io supports multiple engine backends beyond Google. Both draw from Google — no coverage difference between them or vs. Google PSE.

**[Brave](https://brave.com/search/api/)** — Own crawler, own index, privacy-first. Best all-purpose choice when you want index independence from Google/Bing and a generous free tier. Supports web, image, news, and Goggles-based custom result weighting. Also exposes local/map results via `local_search` (the only provider that does) and a LLM context endpoint used by `search_and_scrape` for faster grounding when you're on Brave's Data for AI plan.

**[Exa](https://exa.ai/)** — Neural/semantic index. Results are ranked by embedding similarity, not just keyword match — better for conceptual or research queries. The only provider that backs `answer` (grounded synthesis with citations) and `structured_search` (schema-defined entity extraction). Also provides a paid `/contents` scrape tier as a last-resort fallback for `scrape_page`. Most expensive per-call but uniquely capable.

**[Tavily](https://app.tavily.com/)** — Aggregates from multiple existing search engines at query time, then scrapes the top results and applies AI re-ranking. No proprietary index — similar in architecture to SearXNG, but hosted/commercial with an AI synthesis layer. Returns pre-extracted LLM-ready content. Closest comparison: SearXNG (open-source, self-hosted, no synthesis layer) or Exa (own index, deeper semantic capabilities). Best used as a routing member rather than the sole provider since it lacks image search.

**[SearXNG](https://docs.searxng.org/)** — Open-source, self-hosted, routes to configurable backends. Best for air-gapped environments, organizations requiring no external vendor dependency, or privacy-first deployments. Requires hosting and setup but carries no query limits or API costs.

**[HackerNews](https://hn.algolia.com/)** — Searches HN stories and submissions via the public HN Algolia API. No key or registration. Not general web — use only when you specifically want HN community signal, tech discussions, or submission history. `scrape_page` on any HN URL (item, user, list) reads natively through the HN Firebase API regardless of which `SEARCH_PROVIDER` is set.

**[Reddit](https://www.reddit.com/)** — Searches Reddit posts via the public Atom RSS endpoint. No key or registration required. Not general web — use only when you specifically want Reddit community discussion, popular threads, or subreddit signal. Supports `web_search` and `news_search`; returns a maximum of 25 results per request (RSS hard limit). The `time_range` filter maps to Reddit's `t=` parameter (hour/day/week/month/year; defaults to month). `scrape_page` on any reddit.com URL works independently of which `SEARCH_PROVIDER` is set.

**[Bluesky](https://bsky.app/)** — Searches Bluesky posts via the AT Protocol public AppView (`public.api.bsky.app`, falling back to `api.bsky.app` — same backend, no caching layer — if the cached host 403s the search endpoint specifically). No key or registration required. Not general web — use only when you specifically want Bluesky community signal. Supports `web_search` only (no images, no news); returns up to 100 results per request (defaults to 10). No `time_range` filtering. `scrape_page` on any bsky.app post or profile URL reads natively through the same AT Protocol API regardless of which `SEARCH_PROVIDER` is set, surfacing engagement (likes, reposts, replies) via `forumSignals`.

**[GitHub](https://docs.github.com/en/rest/search/search)** — Searches GitHub issues and pull requests via the public REST Search API. No token required (10 req/min unauthenticated); set GITHUB_TOKEN to raise the limit to 30 req/min. Not general web — use only when you specifically want GitHub issue/PR signal: bug reports, feature requests, open-source community traction, or developer discussion history. Results include issue number, state, kind (issue/PR), reaction count, comment count, author, and creation date. `scrape_page` on any GitHub URL works through the standard scrape pipeline regardless of which `SEARCH_PROVIDER` is set.

---

## Academic Search Providers

### Capability Matrix

| Provider | Search | DOI Resolution | Citation Graph | OA PDF enrichment | AI summaries | Key Required |
|---|:---:|:---:|:---:|:---:|:---:|---|
| **[OpenAlex](https://openalex.org/)** | ✓ | ✓ | ✓ | via Unpaywall | — | No (email for polite pool) |
| **[CrossRef](https://www.crossref.org/)** | ✓ | ✓ (authoritative) | — | — | — | No (email for polite pool) |
| **[Semantic Scholar](https://www.semanticscholar.org/)** | ✓ | — | ✓ (rich edges) | — | ✓ (tldr) | No (key raises limits) |
| **[PubMed](https://pubmed.ncbi.nlm.nih.gov/)** | ✓ | — | — | — | — | No (key raises limits) |
| **[Exa](https://exa.ai/)** | ✓ | — | — | — | — | Yes (`EXA_API_KEY`) |

**Notes:**
- CrossRef is the official DOI registration agency — the authoritative source for DOI metadata. Every DOI-registered work appears here.
- Semantic Scholar enriches results with AI-generated `tldr` summaries and citation intent/influence edges, which power `citation_graph`. OpenAlex also implements `citation_graph` support with citation-count edges as a fallback.
- Only OpenAlex implements the `DOIResolver` interface (exact-entity lookup via `/works/doi:{doi}`). CrossRef, Semantic Scholar, and PubMed do not.
- Exa routes academic queries using its `research-paper` category — useful when its neural index surfaces papers the bibliographic databases miss.
- [Unpaywall](https://unpaywall.org/) OA enrichment runs as a post-processing step on any DOI-bearing result — not a separate provider to select.

### Coverage

| Provider | Corpus | Focus |
|---|---|---|
| **[OpenAlex](https://openalex.org/)** | 287M+ works | All academic disciplines; CC0 data |
| **[CrossRef](https://www.crossref.org/)** | 140M+ DOI-registered works | Peer-reviewed literature; authoritative DOI metadata |
| **[Semantic Scholar](https://www.semanticscholar.org/)** | 200M+ papers | Broad; strong on CS, medicine, biology |
| **[PubMed](https://pubmed.ncbi.nlm.nih.gov/)** | 35M+ citations | Biomedical and life science only |
| **[Exa](https://exa.ai/)** | Neural web index | Research-paper category; surfaces papers outside bibliographic DBs |

### Academic Routing

Without explicit routing, all configured academic providers are tried in order. The recommended starting config:

```bash
export SEARCH_ROUTING='{"academic":"openalex,crossref,semanticscholar","default":"brave,google"}'
```

If no academic providers are configured, `academic_search` automatically falls back to site-restricted web search.

---

## Patent Search Providers

### Jurisdiction Matrix

| Provider | US | EP | WO (PCT) | Other Offices | Key Required |
|---|:---:|:---:|:---:|:---:|---|
| **[EPO OPS](https://developers.epo.org/)** | ✓ | ✓ | ✓ | ✓ (100M+ docs, all major offices) | Yes (free registration) |
| **[The Lens](https://www.lens.org/)** | ✓ | ✓ | ✓ | ✓ (100+ jurisdictions) | Yes (free, request access) |
| **[USPTO](https://data.uspto.gov/)** | ✓ | — | — | — | Yes (free) |
| **[SearchAPI.io](https://www.searchapi.io/)** | ✓ | ✓ | ✓ | ✓ (Google Patents via SerpAPI) | Yes (`SEARCHAPI_API_KEY`) |

**Notes:**
- EPO OPS and The Lens cover worldwide jurisdictions; USPTO covers US patents only.
- SearchAPI.io wraps Google Patents via SerpAPI — good for quick coverage when you already have a SearchAPI key.
- The Lens uniquely links patents to citing academic papers.
- Without any patent provider configured, `patent_search` falls back to site-restricted web discovery.
- The `patent_office` parameter enables intelligent routing — a search restricted to `EP` automatically skips USPTO.

### Patent Routing

```bash
export SEARCH_ROUTING='{"patents":"epo,lens,searchapi,uspto","default":"brave,google"}'
```

---

## Structured-Domain Providers

These providers back dedicated tools and are independent of the web search providers above.

| Tool | Provider | Coverage | Key Required |
|---|---|---|---|
| `filing_search` | **[SEC EDGAR](https://www.sec.gov/edgar/)** | US public-company filings (10-K, 10-Q, 8-K, XBRL company facts) | No (contact email required) |
| `legal_search` | **[CourtListener](https://www.courtlistener.com/)** | US federal and state court opinions | No (token raises limit to ~5,000/day) |
| `econ_search` | **[World Bank](https://data.worldbank.org/)** | Global development indicators, 200+ economies | No |
| `econ_search` | **[OECD](https://data.oecd.org/)** | OECD economy indicators via SDMX | No |
| `econ_search` | **[Eurostat](https://ec.europa.eu/eurostat/)** | European official statistics | No |
| `econ_search` | **[FRED](https://fred.stlouisfed.org/)** | 800K+ US macro series (GDP, CPI, unemployment, rates) | Yes (free) |
| `clinical_search` | **[ClinicalTrials.gov](https://clinicaltrials.gov/)** | 400K+ NIH-registered clinical trials | No |
| `awesome_list_search` | **[ecosyste.ms](https://ecosyste.ms/)** | Community-curated "awesome list" discovery by topic — stars, project counts, archived status | No (`ECOSYSTEMS_EMAIL` optional, raises rate-limit tier via the "polite pool") |
| `archive_source` | **[Internet Archive SPN](https://web.archive.org/save/)** | Save Page Now capture | No (keys raise reliability/limits) |
| `brand_research` | **[BrandFetch](https://brandfetch.com/)** | Brand colors, fonts, logos, tagline, tone of voice (homepage meta + brand-page probing run unconditionally without a key) | No (`BRANDFETCH_API_KEY` optional) |

**Notes:**
- World Bank, OECD, Eurostat, ClinicalTrials.gov, CourtListener, and ecosyste.ms are always available — no configuration required. Setting `ECOSYSTEMS_EMAIL` (falls back to `OPENALEX_EMAIL`) opts ecosyste.ms calls into the "polite pool," raising the per-caller rate-limit tier above the shared "anonymous" pool. `ECOSYSTEMS_API_KEY` is also sent but only takes effect on ecosyste.ms's paid plans.
- SEC EDGAR and FRED activate on their respective env vars (`EDGAR_CONTACT_EMAIL` / `FRED_API_KEY`). `EDGAR_CONTACT_EMAIL` falls back to `OPENALEX_EMAIL`.
- `brand_research` is always available — without `BRANDFETCH_API_KEY` it runs homepage meta/structured-data extraction and brand-page probing; the key adds a concurrent BrandFetch enrichment tier on top, never a replacement.
- `archive_source`, `memory_save`, and `workspace_contribute` are the write tools in the suite. `archive_source` triggers a live internet capture; `memory_save` and `workspace_contribute` are opt-in regulated features.

---

## Multi-Provider Routing

See [docs/DEPLOYMENT.md](DEPLOYMENT.md) for full routing configuration. The short version:

```bash
# Priority-ordered fallback — if Brave is down, routes to Google, then Serper
export SEARCH_ROUTING=brave,google,serper

# Per-operation routing
export SEARCH_ROUTING='{"web":"brave,google","news":"brave,serper","images":"google,brave","academic":"openalex,crossref","patents":"epo,lens,searchapi,uspto","default":"brave,google,searchapi"}'
```

Providers with repeated failures are automatically circuit-broken and skipped until they recover.
