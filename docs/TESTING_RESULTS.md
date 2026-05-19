# End-to-End Testing Results

> Real-world integration tests run against the live `web-researcher-mcp` server connected to Claude Code via STDIO transport with Google Custom Search API. Tested on 2026-05-18 (v1.0.4 — stealth tier read-limit fix + version-based cache invalidation).

## Summary

| Tool | Tests Run | Passed | Failed | Pass Rate |
|------|-----------|--------|--------|-----------|
| `web_search` | 12 | 12 | 0 | 100% |
| `scrape_page` | 5 | 5 | 0 | 100% |
| `search_and_scrape` | 2 | 2 | 0 | 100% |
| `image_search` | 2 | 2 | 0 | 100% |
| `news_search` | 2 | 2 | 0 | 100% |
| `academic_search` | 2 | 2 | 0 | 100% |
| `patent_search` | 2 | 2 | 0 | 100% |
| `sequential_search` | 3 | 3 | 0 | 100% |
| **Total** | **30** | **30** | **0** | **100%** |

---

## 1. web_search — 12/12 Passed

### Test 1.1: Basic Search
```
Query: "Model Context Protocol specification 2025", num_results: 5
Result: 5 results returned
Sample: "Specification - What is the Model Context Protocol (MCP)?" (modelcontextprotocol.io)
        "MCP Spec Updates from June 2025" (auth0.com)
```

### Test 1.2: Lens — Programming
```
Query: "golang context best practices", lens: "programming", num_results: 3
Result: 3 results, all from programming domains
Sample: "Go Concurrency Patterns: Context" (go.dev)
        "Contexts and structs" (go.dev)
        "Mastering Go's Context Package" (dev.to)
```

### Test 1.3: Lens — News + Time Range
```
Query: "EU AI Act enforcement", lens: "news", time_range: "month", num_results: 3
Result: 3 results from major news outlets, all from past month
Sample: "EU countries, lawmakers clinch provisional deal" (reuters.com, May 7 2026)
        "EU countries, lawmakers fail to reach deal" (reuters.com, Apr 29 2026)
```

### Test 1.4: Lens — Science
```
Query: "renewable energy storage", lens: "science", language: "en", num_results: 3
Result: 3 results from scientific sources
Sample: "Trimodal thermal energy storage material" (nature.com, Dec 2024)
```

### Test 1.5: Lens — Finance
```
Query: "SEC filing requirements 2026", lens: "finance", num_results: 3
Result: 3 results all from sec.gov
Sample: "Submit Filings" (sec.gov), "Form 13F FAQ" (sec.gov)
```

### Test 1.6: Lens — Medical
```
Query: "HIPAA compliance telemedicine requirements", lens: "medical", num_results: 3
Result: 3 results all from NIH/PMC
Sample: "Regulation and Compliance in Telemedicine" (pmc.ncbi.nlm.nih.gov)
```

### Test 1.7: Lens — Legal
```
Query: "habeas corpus federal court precedent", lens: "legal", num_results: 3
Result: 3 results from legal sources
Sample: "habeas corpus | Wex" (law.cornell.edu), "Williams v. Taylor" (supreme.justia.com)
```

### Test 1.8: Lens — Government
```
Query: "NASA Mars sample return mission status", lens: "government", num_results: 3
Result: 3 results from government sources
Sample: "NASA: Assessments of Major Projects" (gao.gov), "NASA Transition Authorization Act" (congress.gov)
```

### Test 1.9: Lens — Tech
```
Query: "Claude AI vs GPT-4 comparison benchmark", lens: "tech", num_results: 3
Result: 3 results from tech sources
Sample: "OpenAI's GPT-5.5 is here" (venturebeat.com), "Kimi K2 Thinking" (venturebeat.com)
```

### Test 1.10: Site Restriction
```
Query: "machine learning", site: "arxiv.org", time_range: "year", num_results: 3
Result: 3 results all from arxiv.org
Sample: "From Tiny Machine Learning to Tiny Deep Learning" (arxiv.org/abs/2506.18927)
```

### Test 1.11: Exact Terms + Exclude Terms
```
Query: "Python web framework", exact_terms: "async support", exclude_terms: "Django", num_results: 3
Result: 3 results focused on async — no Django results
Sample: "Rewriting 4000 lines to migrate to Quart (async Flask)" (reddit.com)
```

### Test 1.12: Result Structure Validation
```
All results include: title, url, snippet, displayLink
URL format: valid HTTPS URLs
No duplicate results within response
Response includes: query echo, resultCount, results array, urls array
```

---

## 2. scrape_page — 5/5 Passed

### Test 2.1: Wikipedia — PASS
```
URL: https://en.wikipedia.org/wiki/Model_Context_Protocol
Content Type: html | Length: 15,410 bytes
Title: "Model Context Protocol - Wikipedia"
Excerpt: "The Model Context Protocol (MCP) is an open standard and open-source framework
         introduced by Anthropic in November 2024..."
Note: Full article extracted with references, structured sections
Citation: APA + MLA auto-generated
```

### Test 2.2: YouTube Video — PASS
```
URL: https://www.youtube.com/watch?v=dQw4w9WgXcQ
Content Type: youtube | Length: 2,335 bytes
Title: "Rick Astley - Never Gonna Give You Up (Official Video) (4K Remaster)"
Content: Video description + full lyrics extracted
Note: YouTube-specific extraction working correctly
```

### Test 2.3: Go Wikipedia (max_length truncation) — PASS
```
URL: https://en.wikipedia.org/wiki/Go_(programming_language)
max_length: 2000
Content Type: html | Length: exactly 2,000 bytes | truncated: true
Title: "Go (programming language) - Wikipedia"
Content: First 2000 bytes of article (paradigm, designers, release info)
Note: Truncation works precisely at byte boundary
```

### Test 2.4: Go Blog (preview mode) — PASS
```
URL: https://go.dev/blog/context (preview mode)
Content Type: html | Length: 5,000 bytes | truncated: true
Title: "Go Concurrency Patterns: Context - The Go Programming Language"
Content: Full article extracted and truncated to preview size
Excerpt: "In Go servers, each incoming request is handled in its own goroutine.
         Request handlers often start additional goroutines..."
Note: Stealth tier now reads full HTML before extraction (1MB read limit),
      then truncates extracted content to maxLength. Previously failed due to
      read limit of maxLength*2 (10KB) which captured only <head> section.
```

### Test 2.5: Go Blog (full mode) — PASS
```
URL: https://go.dev/blog/context (full mode)
Content Type: html | Length: 13,372 bytes | truncated: false
Title: "Go Concurrency Patterns: Context - The Go Programming Language"
Content: Full blog post with code examples, context patterns, userip package
Note: Stealth tier extracts complete article via <main> selector
```

---

## 3. search_and_scrape — 2/2 Passed

### Test 3.1: Basic Pipeline with Deduplication
```
Query: "WebAssembly WASI preview 2 specification", num_results: 3, deduplicate: true
Sources searched: 3 | Sources scraped: 2
Processing time: 30,568ms
Top sources:
  - github.com/WebAssembly/WASI (score: 0.67, authority: 0.9)
  - wasi.dev (score: 0.58, authority: 0.5)
Combined content: 3,763 bytes (deduplicated)
Note: Quality scoring, authority ranking, and deduplication all working
```

### Test 3.2: Quality Scoring Validation
```
All sources include scores:
  - overall (weighted composite)
  - relevance (query match)
  - freshness (recency)
  - authority (domain reputation)
  - contentQuality (content richness)
Sources correctly ranked by overall score
```

---

## 4. image_search — 2/2 Passed

### Test 4.1: Type + Size Filter
```
Query: "neural network architecture diagram", type: "lineart", size: "large", num_results: 3
Results: 3 images with full metadata
Sample: "Architecture of a deep neural network" (researchgate.net, 508x660)
All results include: title, link, thumbnailLink, displayLink, contextLink, width, height
```

### Test 4.2: Color + File Type Filter
```
Query: "sunset over ocean", dominant_color: "orange", file_type: "jpg", num_results: 3
Results: 3 images matching color filter
Sample: "Sunset over ocean" (stockcake.com, 728x408)
Sources: stockcake.com, etsy.com — good diversity
```

---

## 5. news_search — 2/2 Passed

### Test 5.1: Weekly News + Sort by Date
```
Query: "SpaceX Starship launch", freshness: "week", sort_by: "date", num_results: 5
Results: 5 articles all from past week
Sample: "SpaceX Starship Launch: When To See Tuesday's High-Stakes Flight" (forbes.com, 17h ago)
        "What time is SpaceX's Starship V3 launch" (yahoo.com, 6h ago)
        "If Starship Explodes Again, It Could Derail SpaceX's Entire IPO" (futurism.com, 4h ago)
All articles include: title, url, source, snippet with timestamps
```

### Test 5.2: Freshness Accuracy
```
All 5 results have recent timestamps (4h to 24h old)
All within the requested "week" freshness window
Source attribution matches actual domain
```

---

## 6. academic_search — 2/2 Passed

### Test 6.1: arXiv Source + Year Filter
```
Query: "transformer attention mechanism efficiency", source: "arxiv", year_from: 2023, num_results: 5
Results: 5 papers all from arxiv.org, all 2023+
Sample: "Efficient Attention Mechanisms for Large Language Models: A Survey" (arxiv.org/abs/2507.19595, Jul 2025)
        "Gated Linear Attention Transformers with Hardware-Efficient Training" (arxiv.org/abs/2312.06635, Dec 2023)
All papers include: title, url, abstract, source
```

### Test 6.2: PubMed Source + Year Range
```
Query: "CRISPR gene therapy clinical trials", source: "pubmed", year_from: 2024, year_to: 2026, num_results: 3
Results: 3 papers all from pubmed.ncbi.nlm.nih.gov
Sample: "Advancing CRISPR genome editing into gene therapy clinical trials" (pubmed, Mar 2025)
        "Gene therapy for sickle cell disease: recent advances" (pubmed, Nov 2024)
Year filter correctly restricts to 2024-2026 window
```

---

## 7. patent_search — 2/2 Passed

### Test 7.1: Prior Art Search + Office Filter
```
Query: "large language model inference optimization", patent_office: "US", year_from: 2023, num_results: 5
Results: 2 patents (fewer available matching strict criteria)
Sample: US20230259705A1 "computer implemented methods for automated analysis using LLM"
        US11431660B1 "System and method for collaborative conversational AI"
Note: Patent office filter reduces results (expected — strict filtering)
```

### Test 7.2: Landscape Search + Assignee Filter
```
Query: "autonomous vehicle lidar sensor fusion", search_type: "landscape", assignee: "Waymo", num_results: 3
Results: 3 patents all mentioning Waymo
Sample: US9383753B1 "Wide-view LIDAR with areas of special attention"
        US20220126863A1 "Autonomous vehicle system"
        US20190258251A1 "Systems and methods for safe and reliable autonomous vehicles"
Assignee filter correctly restricts to Waymo-related patents
```

---

## 8. sequential_search — 3/3 Passed

### Session: "MCP Protocol Adoption Research" (3 steps)

| Step | Action | Result |
|------|--------|--------|
| 1 | Start research with knowledge gap | Session created: `94168e35-aa08-43d4-a57c-9ba1c146db7d`, gap tracked |
| 2 | Continue with new gap | Both gaps accumulated, steps array grows |
| 3 | Conclude (nextStepNeeded=false) | `isComplete: true`, `completedAt` timestamp set |

### Feature Verification

| Feature | Status | Evidence |
|---------|--------|----------|
| Session creation | PASS | UUID generated on first call |
| Session persistence | PASS | Same sessionId across all 3 steps |
| Step tracking | PASS | Full history with timestamps accumulates |
| Knowledge gaps | PASS | 2 gaps tracked across session lifetime |
| Completion marking | PASS | `isComplete: true` with `completedAt` |
| Step timestamps | PASS | ISO 8601 timestamps on each step |
| Total steps estimate | PASS | Accepted on step 1, tracked in response |

---

## Known Issues

| Issue | Tool | Severity | Status |
|-------|------|----------|--------|
| Patent office filter may return fewer results | `patent_search` | Low | Google Patents doesn't support strict faceting; post-filter removes non-matching |
| YouTube transcripts minimal for short videos | `scrape_page` | Low | Short videos have no captions; description + metadata extracted instead |

---

## Performance Characteristics

| Metric | Observed Value |
|--------|-------|
| web_search latency | < 500ms |
| scrape_page (HTML tier) | 200ms - 2s |
| scrape_page (stealth tier) | 300ms - 3s |
| search_and_scrape (3 sources) | ~30s (parallel scraping + quality scoring) |
| sequential_search per step | < 50ms (session management only) |
| news_search | < 500ms |
| academic_search | < 500ms |
| patent_search | < 500ms |
| image_search | < 500ms |

---

## MCP Integration Verification

| Feature | Status | Evidence |
|---------|--------|----------|
| STDIO transport | PASS | Claude Code connects via stdin/stdout |
| Tool registration (8 tools) | PASS | All 8 tools appear in Claude Code's tool list |
| JSON Schema inference | PASS | All tool parameters correctly typed and described |
| Protocol version 2025-03-26 | PASS | Handshake succeeds |
| Typed input structs | PASS | Parameters validated before handler execution |
| Error responses (isError) | PASS | Invalid inputs return descriptive error text |
| Citation metadata | PASS | scrape_page returns APA + MLA formatted citations |
| Quality scoring | PASS | search_and_scrape returns per-source quality scores |

---

## Lens Coverage

All 8 built-in lenses tested and verified:

| Lens | Domains Observed | Working |
|------|-----------------|---------|
| `programming` | go.dev, dev.to, stackoverflow.com | Yes |
| `news` | reuters.com, bbc.com, apnews.com | Yes |
| `tech` | venturebeat.com, techcrunch.com, engadget.com | Yes |
| `legal` | law.cornell.edu, supreme.justia.com, supremecourt.gov | Yes |
| `medical` | pmc.ncbi.nlm.nih.gov, nih.gov | Yes |
| `finance` | sec.gov, bloomberg.com | Yes |
| `science` | nature.com, science.org | Yes |
| `government` | gao.gov, congress.gov, nasa.gov | Yes |

---

## Version History

| Date | Version | Tests | Pass Rate | Notes |
|------|---------|-------|-----------|-------|
| 2026-05-18 | v1.0.4 | 30 | 100% | Stealth tier read-limit fix + version-based cache invalidation. All tools pass via Claude Code MCP |
| 2026-05-18 | v1.0.3 (SDK migration) | 30 | 96.7% | Post go-sdk v1.6.0 migration, scrape_page SPA regression in preview mode |
| 2026-05-18 | v1.0.2 | 41 | 97.6% | Gzip fix verified |
| 2026-05-18 | v1.0.1 | 41 | 90.2% | Gzip decompression regression in scrape_page |
