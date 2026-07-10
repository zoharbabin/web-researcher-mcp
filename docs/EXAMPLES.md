# Examples: What You Can Research

Real-world examples showing what each research capability does and the kind of results you get back.

---

## Quick Web Search

Search the web and get back a clean list of results — each with a title, link, and summary snippet.

```json
{
  "tool": "web_search",
  "arguments": {
    "query": "MCP Model Context Protocol specification",
    "num_results": 5
  }
}
```

**Response** contains: `urls` (array of result URLs), `query` (echoed back), `resultCount`, and `results` (array with `title`, `url`, `snippet`, `displayLink` for each result). Every response also carries a `_meta` block (`cached`, `ageSeconds`, `maxAgeSeconds`, `freshness`) telling you whether it came from cache. Results are saved temporarily — if you run the same search again, it responds instantly without using another API call.

Pass an optional `claim` to get a triage signal: each result then also carries a `claimSignal` — the most claim-relevant sentence from that result's snippet — so you can tell at a glance which links are worth reading. This is snippet-level evidence only; for full-text claim evidence use `search_and_scrape` with `claim`. The server surfaces evidence, never a verdict.

---

## Domain-Focused Search with Lenses

Use a search lens to restrict results to curated high-quality sources for a specific domain.

```json
{
  "tool": "web_search",
  "arguments": {
    "query": "context cancellation patterns",
    "lens": "programming",
    "num_results": 5
  }
}
```

The "programming" lens focuses your search on trusted developer sources — Stack Overflow, GitHub, Go docs, MDN, and other curated sites. This means fewer noise results and more relevant answers. For the full, current list of available lenses, read the `lenses://catalog` MCP resource (or the JSON files in the `lenses/` directory, which are the canonical source).

---

## Deep Research with search_and_scrape

Searches the web, then reads the top results for you — pulling out the full text so you get the actual content, not just a list of links.

```json
{
  "tool": "search_and_scrape",
  "arguments": {
    "query": "kubernetes pod security standards best practices",
    "num_results": 3,
    "include_sources": true,
    "deduplicate": true
  }
}
```

**Response** contains: `status` (`"complete"`, `"partial"`, or `"failed"`), `query`, `combinedContent` (merged extracted text), `sources` (array with `url`, `title`, `content`, `contentType`, `scores`, plus typed source classification `sourceType`/`authorityTier`/`domainCategory` for each source — included when `include_sources=true`), `summary` (`urlsSearched`, `urlsScraped`, `urlsFailed`, `processingTimeMs`), and `sizeMetadata` (`totalLength`, `estimatedTokens`, `sizeCategory`). When scrapes fail, `scrapeFailures` lists each with `url`, `kind`, `reason`, `retryable`, and `suggestedAction`. Duplicate paragraphs are removed, and long content is trimmed at sentence breaks so nothing cuts off mid-thought.

Pass an optional `claim` to evaluate each source against it: every source then also carries `keySentences` (the most claim-relevant sentences from its full text) and `claimSignal` (the single strongest). The server surfaces this evidence only — it never decides whether a source supports or contradicts the claim; your AI makes that call.

---

## Academic Literature Review

Search peer-reviewed papers, preprints, and academic databases.

```json
{
  "tool": "academic_search",
  "arguments": {
    "query": "transformer attention mechanisms efficiency",
    "num_results": 5
  }
}
```

**Response** contains: `papers` (array of `{title, url, source, doi, authors, journal, year, abstract, citationCount, openAccess, pdfUrl}` — plus `tldr`, `isInfluential`, and `citationIntents` when the provider supplies them), `query`, `totalResults`, `resultCount`, and `source` (which provider answered). When no results are found, a `hints` object explains why and suggests actions (e.g., remove restrictive filters, try a different source). Results come from scholarly databases (OpenAlex, CrossRef, PubMed, Semantic Scholar, or Exa) or site-restricted web search as fallback. To trace a paper's citation neighborhood — the works it cites and the works that cite it — pair this with `citation_graph`.

---

## Patent Landscape Analysis

Search patent databases with classification codes and office filtering.

```json
{
  "tool": "patent_search",
  "arguments": {
    "query": "natural language processing voice assistant",
    "num_results": 5,
    "patent_office": "US",
    "cpc_code": "G10L15"
  }
}
```

**Response** contains: `patents` (array of `{title, url, number, abstract, assignee, inventor, filed, granted, pdf, status}`), `query`, `searchType`, `resultCount`, `source` (which provider answered), and `searchUrl`. When no results are found, a `hints` object explains why (e.g., provider doesn't cover the requested region) and suggests alternatives. You can filter by patent office (`all` (default), `US`, `EP` (European), `WO` (international/PCT), `JP`, `CN`, `KR`) and by technology category codes. The server picks the best data source for your region, or you can force a specific provider.

---

## SEC Filing Search

Look up US public-company disclosures straight from SEC EDGAR — 10-K, 10-Q, 8-K, S-1, DEF 14A, and more. Search by company name, ticker, or CIK, or pass free text to full-text search across all filers.

```json
{
  "tool": "filing_search",
  "arguments": {
    "ticker": "AAPL",
    "form_type": "10-K",
    "num_results": 5
  }
}
```

**Response** contains: `query`, `resultCount`, `provider`, `trust`, and `filings` (array with `company`, `url`, `source`, and where present `cik`, `formType`, `filingDate`, `periodOfReport`, `accession`, `description`). Pair a filing `url` with `scrape_page` to read it.

To pull structured XBRL company facts (revenue, net income, EPS, assets) instead of a filing list, set `facts=true` — values pass through exactly as filed, no rounding:

```json
{
  "tool": "filing_search",
  "arguments": { "ticker": "AAPL", "facts": true }
}
```

With `facts=true`, each result carries `concept`, `unit`, and `value`. Filter any search with `form_type`, `date_from`, and `date_to`. EDGAR needs no API key — only a contact email in its User-Agent (set `EDGAR_CONTACT_EMAIL`, or it falls back to `OPENALEX_EMAIL`). Results stay fresh for 24 hours.

---

## US Case-Law Search

Search US federal and state court opinions for precedent. Query by legal topic, case name, or statutory reference; narrow by jurisdiction or decision date. Works with no API key.

```json
{
  "tool": "legal_search",
  "arguments": {
    "query": "Miranda v. Arizona",
    "jurisdiction": "scotus",
    "num_results": 10
  }
}
```

**Response** contains: `query`, `resultCount`, `provider`, `trust`, and `cases` (array with `caseName`, `url`, `source`, and where present `citation` (Bluebook), `court`, `courtId`, `dateFiled`, `docketNumber`, `citationCount`). Open the full opinion via `scrape_page` on a case `url`. Filter with `jurisdiction` (e.g. `scotus`, `ca9`, `ny`), `date_from`, and `date_to`. Set `COURTLISTENER_API_TOKEN` to raise the rate limit (it works keyless otherwise). Results stay fresh for 24 hours.

---

## Economic Data Search

Look up economic data from four providers. **World Bank Open Data** (keyless, always available) covers global development indicators for 200+ economies. **FRED** (Federal Reserve Economic Data, needs a free key) adds 800K+ US macro series — GDP, CPI, unemployment, rates. **OECD** (keyless) covers OECD member-country statistics. **Eurostat** (keyless) covers EU economic and social data. Search by keyword to discover series IDs, or pass a `series_id` to retrieve observations.

```json
{
  "tool": "econ_search",
  "arguments": {
    "query": "unemployment rate",
    "num_results": 5
  }
}
```

In **search mode** (`mode: "series"`), `results` is an array of `{seriesId, title, units, frequency, lastUpdated, notes}`. To retrieve observations for a known series, pass `series_id`:

```json
{
  "tool": "econ_search",
  "arguments": {
    "series_id": "UNRATE",
    "date_from": "2020-01-01",
    "units": "pch"
  }
}
```

For global data, force the World Bank provider and scope by `country` (an ISO code, or `WLD` for the world aggregate — the default):

```json
{
  "tool": "econ_search",
  "arguments": {
    "provider": "worldbank",
    "series_id": "NY.GDP.MKTP.CD",
    "country": "US",
    "date_from": "2018",
    "date_to": "2022"
  }
}
```

In **observations mode** (`mode: "observations"`), `results` is an array of `{seriesId, date, value}` (multi-country providers also include a `country` field at the top level). Numeric values pass through exactly as the source returns them — no rounding, and a real `0` is preserved (missing observations carry no `value`). FRED supports `frequency` (`d`/`w`/`m`/`q`/`a`) and `units` (e.g. `pch`, `pc1`). World Bank, OECD, and Eurostat scope by `country` and filter by year. FRED requires `FRED_API_KEY` (free at fred.stlouisfed.org); World Bank, OECD, and Eurostat need no key. Results stay fresh for 6 hours.

---

## Clinical Trial Search

Search **ClinicalTrials.gov** (keyless) for clinical-trial registrations — discovery and primary-source retrieval for evidence-based medicine, not medical advice. Combine free text, `condition`, `intervention`, `sponsor`, and a recruitment `status` filter.

```json
{
  "tool": "clinical_search",
  "arguments": {
    "condition": "covid-19",
    "intervention": "vaccine",
    "status": "COMPLETED",
    "num_results": 5
  }
}
```

Each `trials` item carries `{nctId, title, status, phases, conditions, interventions, sponsor, startDate, hasResults, url, source}`. `hasResults` tells you whether study results are posted to the registry — a completed trial with no posted results is worth scrutinizing. Read the full registration by passing the `url` to `scrape_page`, and check a linked publication with `verify_citation`. Results stay fresh for 6 hours.

---

## Awesome List Discovery (awesome_list_search)

Find community-curated "awesome list" collections on a topic — good for scouting an unfamiliar ecosystem or checking whether a niche already has a maintained resource list. Backed by **[ecosyste.ms](https://ecosyste.ms/)** (keyless).

```json
{
  "tool": "awesome_list_search",
  "arguments": {
    "topic": "osint",
    "num_results": 5
  }
}
```

**Response** contains: `query`, `resultCount`, `provider`, `trust`, and `lists` (array with `name`, `fullName`, `url`, `description`, `stars`, `projectsCount`, `topics`, `lastSyncedAt`, `archived`, `source`). Archived lists are excluded automatically. Filter with `min_stars` or `min_projects` to cut noise from small/abandoned lists, and `sort_by` (`stars` (default), `projects`, or `updated`) to change ranking. Pass `query` instead of `topic` for a free-text search when you don't know the exact topic tag.

---

## News Monitoring

Search recent news with freshness controls and source filtering.

```json
{
  "tool": "news_search",
  "arguments": {
    "query": "artificial intelligence regulation",
    "time_range": "week",
    "num_results": 5
  }
}
```

**Response** contains: `articles` (array of `{title, url, source, publishedAt, snippet}`), `query`, and `resultCount`. Use `time_range` values: `hour`, `day`, `week`, `month`, `year` to control how recent articles must be.

---

## Image Asset Discovery

Search for images with format, size, and color filters.

```json
{
  "tool": "image_search",
  "arguments": {
    "query": "system architecture diagram microservices",
    "num_results": 5,
    "size": "large",
    "type": "lineart"
  }
}
```

**Response** contains: `images` (array of `{title, link, thumbnailLink, displayLink, contextLink, width, height, fileSize}`), `query`, and `resultCount`. Filter options: `size` (small/medium/large/xlarge/xxlarge/huge/icon), `type` (photo/lineart/clipart/animated/face/stock), `color_type` (color/gray/mono/trans), `file_type` (jpg/png/gif/bmp/svg/webp).

---

## Page Scraping

Extract content from any URL — web pages, PDFs, DOCX, PPTX, YouTube transcripts, or Hacker News threads (read natively via the HN API).

```json
{
  "tool": "scrape_page",
  "arguments": {
    "url": "https://go.dev/blog/context"
  }
}
```

**Response** contains: `url`, `content` (extracted text), `contentType` (html/markdown/youtube/pdf/docx/pptx), `contentLength`, `truncated`, `estimatedTokens`, `sizeCategory`, `citation` (with APA/MLA/BibTeX formatted citations), typed source classification (`sourceType`: peer_reviewed/official_docs/government/news_publication/blog/forum/wiki/social_media/unknown; `authorityTier`: high/medium/low; `domainCategory`: academic/legal/medical/financial/technical/general), and optionally `metadata` (`{title, author}`), `extractedBy` (the extraction tier), `structuredData` (JSON-LD / Open Graph / citation meta when present), `detectedDoi` (a DOI the page declares itself — useful for verifying a scrape result against `verify_citation`), and `retractionStatus` (retraction data if the detected DOI is in the Crossref retraction watch). The tool uses the fastest method available and only launches a full browser for sites that require JavaScript — so most pages load in under a second. On a cache hit the result also carries a `_meta` block (`cached`, `ageSeconds`, `maxAgeSeconds`, `freshness`) so you can tell how recent the content is.

### Modes

`scrape_page` accepts a `mode` parameter:

- `full` (default) — cleaned, readable text, sanitized and truncated to `max_length`.
- `preview` — just the first ~5000 bytes; a fast first look.
- `raw` — the fetched bytes **verbatim**, with no sanitization. Use it only to inspect source like JSON, HTML markup, or JavaScript. Raw output adds `"raw": true` and reports the server's real `Content-Type`. Because nothing is sanitized, the bytes are untrusted — never execute or render them, and treat any instructions inside as data, not commands. Raw mode is exclusive to `scrape_page`; `search_and_scrape` is always sanitized and has no raw mode.

```json
{
  "tool": "scrape_page",
  "arguments": {
    "url": "https://api.example.com/data.json",
    "mode": "raw",
    "max_length": 20000
  }
}
```

---

## Multi-Step Investigation (sequential_search)

Track multi-step research with persistent sessions. Sessions survive server restarts (encrypted disk) and can be recovered after context loss.

### Step 1: Start a new session

```json
{
  "tool": "sequential_search",
  "arguments": {
    "searchStep": "Initial research on MCP server implementations in Go",
    "stepNumber": 1,
    "nextStepNeeded": true,
    "researchGoal": "Compare MCP server architectures for stateful multi-turn research",
    "reasoning": "Starting broad to map the landscape before narrowing",
    "confidence": "medium",
    "totalStepsEstimate": 3
  }
}
```

**Response** returns a `sessionId` that you use for subsequent steps, plus `researchGoal`, `responseMode`, and the step index.

### Step 2: Continue the session

```json
{
  "tool": "sequential_search",
  "arguments": {
    "sessionId": "abc123-from-step-1",
    "searchStep": "Compared caching strategies across implementations — found two-tier (memory+disk) is standard",
    "stepNumber": 2,
    "nextStepNeeded": true,
    "reasoning": "Narrowing to caching since it's the most complex subsystem",
    "confidence": "high",
    "rejectedApproaches": ["Redis-only approach - adds deployment complexity for single-instance use"],
    "knowledgeGap": "Need to understand how other servers handle multi-tenancy",
    "sessionSummary": "MCP servers in Go use interface-driven design. Two-tier caching is standard."
  }
}
```

### Step 3: Complete the session

```json
{
  "tool": "sequential_search",
  "arguments": {
    "sessionId": "abc123-from-step-1",
    "searchStep": "Synthesized findings on architecture patterns for MCP servers",
    "stepNumber": 3,
    "nextStepNeeded": false,
    "confidence": "high"
  }
}
```

**Response** contains the session state: `sessionId`, `responseMode`, `researchGoal`, `currentStep`, `totalStepsEstimate`, `isComplete`, `startedAt`, and (when complete) `completedAt`. The step detail depends on `responseMode`: in `full` mode (the default for 8 or fewer steps) you get a `steps` index; in `summary` mode (default beyond 8 steps) you get `summary` plus a `stepIndex`. Both modes also return `lastSteps` (the most recent full steps), `gaps` (knowledge gaps identified), and `sources`. Use `branchFromStep` + `branchId` to explore alternative research directions without losing the main thread.

Sessions persist for 4 hours from last activity and survive server restarts.

---

## Recovering a Session (get_research_session)

After context loss (e.g., LLM context window compaction), recover your session state:

```json
{
  "tool": "get_research_session",
  "arguments": {
    "sessionId": "abc123-from-earlier"
  }
}
```

**Response** contains: `sessionId`, `responseMode` (`summary`), `researchGoal`, `summary`, `stepCount`, `startedAt`, `stepIndex` (one-liner per step with confidence), `lastSteps` (last full steps), `gaps` (open questions), and `sources`. Passing `stepId` instead returns `responseMode: "step"` with the single full `step`.

To retrieve full details of a specific earlier step:

```json
{
  "tool": "get_research_session",
  "arguments": {
    "sessionId": "abc123-from-earlier",
    "stepId": 2
  }
}
```

---

## Auditing a Bibliography (audit_bibliography)

Before filing a brief or submitting a paper, audit the whole reference list in one pass — paste the bibliography your reference manager exports (CSL-JSON, RIS, or BibTeX) and get per-entry + corpus-level flags for **retracted**, **dead-link**, and **unverifiable** citations.

```json
{
  "tool": "audit_bibliography",
  "arguments": {
    "bibliography": "TY  - JOUR\nTI  - Ileal-lymphoid-nodular hyperplasia...\nDO  - 10.1016/S0140-6736(97)11096-0\nER  - ",
    "format": "auto"
  }
}
```

You can also pass an explicit `entries` list or a `sequential_search` `sessionId` instead of a document. The response carries a `summary` (`{total, retracted, deadLink, notFound, unchecked, mischaracterized, ok}`) plus per-entry `entries[]` with `exists`, `retractionStatus`, `linkLive`/`httpStatus`, an `archivedUrl` (Wayback) for dead links, `flags`, and a `reason` explaining any flagged entry. The flags distinguish a **possible fabrication** (`not_found` — a DOI Crossref doesn't have) from a source that **couldn't be checked** (`unchecked` — e.g. a book or paywalled report; absence of evidence, not proof it's fake). It is **evidence, not a verdict** — you decide what to fix. The audit is capped at 200 entries per call (overflow is reported in `skipped`). Use `verify_citation` for a single citation and `format_bibliography` to produce the list.

To also check that a source **actually says what it's cited for** (mischaracterization), add a `claim` to an explicit entry:

```json
{
  "tool": "audit_bibliography",
  "arguments": {
    "entries": [
      {
        "url": "https://www.nejm.org/doi/full/10.1056/NEJMoa2007764",
        "title": "Remdesivir for COVID-19",
        "claim": "remdesivir shortened recovery time in hospitalized patients"
      }
    ]
  }
}
```

The source page is fetched (live, or its Wayback snapshot if the link is dead) and checked for whether it addresses the claim. `claimSupport` reports **coverage, not a stance**: `addressed` (claim-relevant sentences found — returned in `claimEvidence` so you judge whether they support or contradict), `partially_addressed` (some overlap — evidence shown but not flagged; ambiguous, you judge), `not_addressed` (the source doesn't mention the claim → flagged `mischaracterized`), or `source_unavailable`. It never asserts "supports"/"refutes" — you read the evidence and decide.

## Verifying a Citation (verify_citation)

Before you cite a source, verify it exists, hasn't been retracted, and actually says what it's cited for.

```json
{
  "tool": "verify_citation",
  "arguments": {
    "citation": "10.1056/NEJMoa2007764",
    "claim": "remdesivir shortened recovery time in hospitalized patients"
  }
}
```

`citation` accepts a DOI, a URL, or a free-text reference string — the tool detects which. **Response** carries: `exists` (boolean), `matchedRecord` (the real bibliographic record from Crossref/academic sources, with a `matchConfidence`), `titleMatch` (does the title you supplied match the real record — catches swapped DOIs), `retractionStatus` (from Crossref Retraction Watch), and link-liveness (`linkLive`, `httpStatus`, `archivedUrl`). When you add a `claim`, you also get `claimCoverage` (`addressed` / `partially_addressed` / `not_addressed` → flagged `mischaracterized`), `claimEvidence` (claim-relevant sentences from the source), and a `contrastSignal` if the source contradicts the claim. This is evidence, not a verdict — you decide whether to cite. Use `audit_bibliography` to check an entire reference list at once.

---

## Checking a Recommendation List (verify_recommendation)

Got a listicle or AI-generated product/service recommendation? Check it for self-promotion, conflicts of interest, and independent corroboration.

```json
{
  "tool": "verify_recommendation",
  "arguments": {
    "recommendations": [
      { "title": "Stripe", "url": "https://stripe.com", "author": "John Smith", "authorBio": "VP of Sales at Stripe" },
      { "title": "Square", "url": "https://squareup.com" }
    ],
    "claim": "best payment processors for small businesses"
  }
}
```

**Response** carries per-item signals: `selfPromotionSignal` (is the author promoting their own product?), `conflictOfInterest` (does the author bio signal a financial relationship?), `domainReputation` (is this a known trustworthy source?), `linkLive` (is the URL still up?), and — when `claim` is set — `corroborationSearches` (what independent journalism and tech sources say about this recommendation). The `flags` field summarizes any concerns, including `no_independent_corroboration` when no outside source agrees. This is evidence, not a verdict — you decide whether the list is genuinely helpful or gaming you.

---

## Archiving a Source (archive_source)

Lock in a timestamped snapshot before you cite a page that might change or disappear.

```json
{
  "tool": "archive_source",
  "arguments": {
    "url": "https://www.bbc.com/news/technology-example"
  }
}
```

This is one of three **write tools** in the suite (the others, `memory_save` and `workspace_contribute`, are opt-in regulated features) — it asks the Internet Archive's Save Page Now to capture a fresh snapshot. **Response** carries: `status` (`archived` = fresh capture confirmed, `existing` = no new capture made but a recent snapshot exists, `pending` = capture in-flight), `snapshotUrl` (the permanent Wayback URL), `capturedAt` (timestamp), and `provenance`. Run `verify_citation` first to see whether a link is already dead or already archived before capturing.

---

## Tracing Citation Networks (citation_graph)

Map the academic neighborhood of a paper — the works it references and the works that cite it.

```json
{
  "tool": "citation_graph",
  "arguments": {
    "paper": "10.1038/nature12373",
    "direction": "both",
    "num_results": 10,
    "influential_only": false
  }
}
```

`paper` accepts a DOI or an exact title. `direction` is `cited_by` (forward — works that cite the seed), `references` (backward — works the seed cites), or `both` (default). **Response** carries: `seed` (the resolved seed record), `citedBy` and `references` arrays (each with `title`, `doi`, `authors`, `year`, `citationCount`, `isInfluential`, `citationIntents`), and `provider`. Requires at least one academic provider (Semantic Scholar or OpenAlex). Use alongside `academic_search` to discover papers and `verify_citation` to check individual ones.

---

## Brand Identity Research (brand_research)

Pull a company's colors, logo, typography, and tone of voice from its official brand portals and guidelines.

```json
{
  "tool": "brand_research",
  "arguments": {
    "url": "stripe.com"
  }
}
```

You can pass a `url` (domain or full URL) or a `company_name` — `url` takes precedence when both are set. **Response** carries a structured `identity` object with `name`, `domain`, `colors` (hex values with their roles), `fonts`, `logo` URLs, `socialHandles`, `toneOfVoice`, and `guidelinesUrl`. Empty fields mean the data wasn't found — not that it doesn't exist. When a brand portal is found, `brand_portal_resource` carries a `research://artifact/{id}` URI — pass it to `read_resource` to get the full rendered portal text for deeper analysis. When no portal is found, the `suggestion` field tells you what to do next. Results are cached for 24 hours; `cache_age` tells you how fresh they are.

---

## Local Place Search (local_search)

Find places near a location — restaurants, services, venues — with distance ranking and coordinate support. Requires `BRAVE_API_KEY`.

```json
{
  "tool": "local_search",
  "arguments": {
    "query": "best coffee shops near downtown Seattle",
    "near": "downtown Seattle",
    "num_results": 5
  }
}
```

For precise radius filtering, pass `latitude`, `longitude`, and `radius` (in meters) instead of `near`. **Response** carries `places` (array with `name`, `address`, `phone`, `rating`, `categories`, `url`, `distance`) and `resultCount`. Filter by `country` (ISO 3166-1 alpha-2) and set `units` to `metric` or `imperial`.

---

## Synthesized Answer (answer)

Get a single synthesized answer with citations — the provider searches the live web and distills it for you. Requires a provider that supports synthesis (e.g., Exa).

```json
{
  "tool": "answer",
  "arguments": {
    "query": "What is the current federal funds rate?"
  }
}
```

**Response** carries: `answer` (the synthesized text), `citations` (sources backing the answer), and `provider`. Use this for quick factual lookups; use `sequential_search` + `scrape_page` for deep investigation.

---

## Structured Entity Search (structured_search)

Extract structured data about entities — companies, people, papers — from search results. Requires a provider that supports structured extraction (e.g., Exa).

```json
{
  "tool": "structured_search",
  "arguments": {
    "query": "Stripe",
    "category": "company",
    "num_results": 3,
    "schema": {
      "type": "object",
      "properties": {
        "founded": { "type": "string" },
        "headquarters": { "type": "string" },
        "ceo": { "type": "string" }
      }
    }
  }
}
```

**Response** carries `results` (each with `title`, `url`, `highlights` — verbatim source snippets, and `summary` — JSON conforming to your schema if supplied). The `schema` field is optional; omit it for plain text summaries. Extraction is best-effort — treat `highlights` as the authoritative payload and `summary` as a convenience. Provider-specific limits on schema complexity apply.

---

## Exporting a Research Session (research_export)

Turn a `sequential_search` session into a shareable report.

```json
{
  "tool": "research_export",
  "arguments": {
    "sessionId": "abc123-from-earlier",
    "format": "markdown",
    "verify_links": true
  }
}
```

`format` is `markdown` (readable write-up with goal, steps, gaps, and sources) or `json` (full structured session for machine use). When `verify_links` is `true`, each source URL is checked for liveness and dead links get a Wayback snapshot attached — adds latency but ensures your report's sources stay verifiable. Pair with `format_bibliography` to produce a formatted citations list.

---

## Formatting a Bibliography (format_bibliography)

Format your sources as APA, MLA, BibTeX, RIS, or CSL-JSON — straight from a session or an explicit list.

```json
{
  "tool": "format_bibliography",
  "arguments": {
    "sessionId": "abc123-from-earlier",
    "style": "bibtex"
  }
}
```

Or pass an explicit `sources` list:

```json
{
  "tool": "format_bibliography",
  "arguments": {
    "style": "apa",
    "sources": [
      {
        "url": "https://www.nejm.org/doi/full/10.1056/NEJMoa2007764",
        "title": "Remdesivir for the Treatment of Covid-19",
        "author": "Beigel, J.H. et al.",
        "site": "New England Journal of Medicine",
        "date": "2020",
        "doi": "10.1056/NEJMoa2007764"
      }
    ]
  }
}
```

`style` is `apa` (default), `mla`, `bibtex`, `ris`, or `csl-json`. `apa`/`mla` are human-readable; `bibtex`/`ris`/`csl-json` are reference-manager interchange formats. Each source needs at least a `url`; add `doi` so reference managers keep the persistent ID.

---

## Checking Your Own Usage (get_my_analytics)

Return your own per-tool usage counts for this tenant — opt-in and consent-gated, and it only ever shows your own data.

```json
{
  "tool": "get_my_analytics",
  "arguments": {}
}
```

**Response** contains per-tool call counts plus first/last-seen timestamps, or a disabled/no-consent status if user-level analytics isn't turned on or you haven't consented to the `analytics` purpose.

---

## Remembering a Finding Across Sessions (memory_save, memory_recall)

Save a finding to your own long-term memory so future sessions can recall it — unlike `sequential_search` sessions, which expire after 4 hours. Opt-in and consent-gated.

```json
{
  "tool": "memory_save",
  "arguments": {
    "note": "The 2020 NEJM remdesivir trial found a modest reduction in recovery time, not mortality.",
    "topic": "covid-treatments",
    "url": "https://www.nejm.org/doi/full/10.1056/NEJMoa2007764"
  }
}
```

Recall it later, optionally filtered by topic:

```json
{
  "tool": "memory_recall",
  "arguments": {
    "topic": "covid-treatments",
    "limit": 20
  }
}
```

Both tools show only your own memories — never another user's — and persist nothing unless long-term memory is enabled and you've consented to the `memory` purpose.

---

## Sharing Findings in a Team Workspace (workspace_contribute, workspace_read)

Share a finding into a shared workspace your team belongs to. A copy is stored with your attribution — never a live link to your private data.

```json
{
  "tool": "workspace_contribute",
  "arguments": {
    "workspace_id": "research-team-alpha",
    "note": "Remdesivir shortens recovery time but doesn't move mortality — worth flagging before citing it as a survival benefit.",
    "url": "https://www.nejm.org/doi/full/10.1056/NEJMoa2007764"
  }
}
```

Read back everything the team has shared:

```json
{
  "tool": "workspace_read",
  "arguments": {
    "workspace_id": "research-team-alpha"
  }
}
```

Both tools require the `workspace` purpose consent and membership in the target workspace (membership is managed by your host app, not by these tools) — non-members get nothing back.

---

## Cross-Tool Trust Workflow

A full trust-verification pass for academic or legal research:

1. **academic_search** — discover relevant papers
2. **citation_graph** — trace influential references and citing works
3. **verify_citation** with `claim` — confirm each key citation exists, hasn't been retracted, and actually addresses the claim
4. **archive_source** — lock in a Wayback snapshot for any sources you intend to cite
5. **audit_bibliography** — batch-check your full reference list for retracted, dead, or possibly fabricated entries
6. **research_export** with `verify_links: true` — produce a final report with source liveness confirmed

A general discovery-to-report workflow:

1. **web_search** with a `lens` — find sources in a curated domain
2. **search_and_scrape** with `claim` — get full text with claim evidence surfaced
3. **news_search** or **academic_search** — add domain-specific depth
4. **sequential_search** — track progress across steps with recoverable session state
5. **format_bibliography** or **research_export** — produce the final output

The AI assistant orchestrates these tools based on the research question. You don't need to call them manually.
