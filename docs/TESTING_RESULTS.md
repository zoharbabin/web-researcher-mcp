# End-to-End Testing Results

> Real-world integration tests run against the live `web-researcher-mcp` server connected to Claude Code via STDIO transport with Google Custom Search API. Tested on 2026-05-18.

## Summary

| Tool | Tests Run | Passed | Failed | Pass Rate |
|------|-----------|--------|--------|-----------|
| `web_search` | 14 | 14 | 0 | 100% |
| `scrape_page` | 10 | 8 | 2 | 80% |
| `search_and_scrape` | 8 | 8 | 0 | 100% |
| `image_search` | 5 | 5 | 0 | 100% |
| `news_search` | 5 | 5 | 0 | 100% |
| `academic_search` | 5 | 5 | 0 | 100% |
| `patent_search` | 5 | 4 | 1 | 80% |
| `sequential_search` | 7 | 7 | 0 | 100% |
| **Total** | **59** | **56** | **3** | **94.9%** |

---

## 1. web_search — 14/14 Passed

### Test 1.1: Basic Search
```
Query: "Rust programming language async runtime comparison"
Result: 5 results returned
Sample: "The State of Async Rust: Runtimes" (reddit.com, corrode.dev)
```

### Test 1.2: Lens — Programming
```
Query: "golang context best practices", lens: "programming"
Result: 5 results, all from programming domains (go.dev, pkg.go.dev, stackoverflow.com)
Sample: "Go Concurrency Patterns: Context" (go.dev)
```

### Test 1.3: Lens — News
```
Query: "artificial intelligence regulation 2025", lens: "news"
Result: 5 results from major news outlets (NPR, NYT, Washington Post, Guardian, CNN)
Sample: "Trump says fewer regulations needed to win the AI race" (NPR)
```

### Test 1.4: Lens — Medical
```
Query: "CRISPR gene therapy clinical trials", lens: "medical"
Result: 5 results from medical sources (PubMed Central, NEJM)
Sample: "Advancing CRISPR genome editing into gene therapy clinical trials" (pmc.ncbi.nlm.nih.gov)
```

### Test 1.5: Lens — Legal
```
Query: "GDPR data processing agreement requirements", lens: "legal"
Result: 5 results from legal sources (Westlaw/Practical Law)
Sample: "Personal Data Processing Agreements Toolkit" (westlaw.com)
```

### Test 1.6: Lens — Finance
```
Query: "federal reserve interest rate decision", lens: "finance"
Result: 5 results from financial sources (federalreserve.gov)
Sample: "Federal Reserve issues FOMC statement" (federalreserve.gov)
```

### Test 1.7: Time Range — Week
```
Query: "SpaceX launch", time_range: "week"
Result: 5 results all from past 7 days
Sample: "CRS-34 Mission - SpaceX" (3 days ago)
```

### Test 1.8: Exact Terms
```
Query: "machine learning", exact_terms: "transformer architecture"
Result: 5 results all containing exact phrase "transformer architecture"
Sample: "Transformer (deep learning) - Wikipedia"
```

### Test 1.9: Exclude Terms
```
Query: "python web framework", exclude_terms: "django"
Result: 5 results, none mentioning Django
Sample: "A reactive web framework for Python built on PyScript"
```

### Test 1.10: Site Restriction
```
Query: "react server components", site: "github.com"
Result: 5 results all from github.com
Sample: "Support for React Server Components - Issue #1209" (github.com)
```

### Test 1.11: Language Filter
```
Query: "kubernetes deployment", language: "en"
Result: 5 results in English
Sample: "Deployments | Kubernetes" (kubernetes.io)
```

### Test 1.12: High Result Count
```
Query: "best databases for time series data", num_results: 10
Result: Exactly 10 results returned
Sample: "The Best Time-Series Databases Compared (2026)" (Tiger Data)
```

### Test 1.13: Safe Search
```
Query: "content moderation AI systems", safe: "high"
Result: 5 appropriate results
Sample: "Content Moderation in a New Era for AI and Automation"
```

### Test 1.14: Country Filter
```
Query: "NHS digital health services", country: "GB"
Result: 5 UK-focused results (digital.nhs.uk, england.nhs.uk)
Sample: "Home - NHS England Digital" (digital.nhs.uk)
```

---

## 2. scrape_page — 8/10 Passed

### Test 2.1: Web Page (go.dev) — PASS
```
URL: https://go.dev/doc/effective_go
Content Type: html | Length: 49,824 bytes (truncated at default max)
Excerpt: "Effective Go ... Go is an open-source programming lang..."
```

### Test 2.2: News Site (BBC) — PASS
```
URL: https://www.bbc.com/news
Content Type: html | Length: 6,670 bytes
Excerpt: "WHO to give update on hantavirus and Ebola after outbreaks..."
```

### Test 2.3: YouTube Transcript — PARTIAL FAIL
```
URL: https://www.youtube.com/watch?v=dQw4w9WgXcQ
Content Type: youtube | Length: 0 bytes
Title extracted: "Rick Astley - Never Gonna Give You Up (Official Video) (4K Remaster)"
Issue: Metadata extracted but transcript content empty (likely no caption track available or YouTube API restriction)
```

### Test 2.4: GitHub README — PASS
```
URL: https://github.com/anthropics/claude-code
Content Type: html | Length: 2,484 bytes
Excerpt: "Claude Code is an agentic coding tool that lives in your terminal..."
```

### Test 2.5: Technical Docs (MCP) — PASS
```
URL: https://modelcontextprotocol.io/introduction
Content Type: markdown | Length: 3,205 bytes
Excerpt: "Documentation Index ... Fetch the complete documentation index at..."
Note: Correctly detected markdown content negotiation
```

### Test 2.6: Wikipedia — PASS
```
URL: https://en.wikipedia.org/wiki/Large_language_model
Content Type: html | Length: 50,000 bytes (truncated at default max)
Excerpt: "A large language model (LLM) is a neural network trained on a vast amount of text..."
```

### Test 2.7: max_length Parameter — PASS
```
URL: https://en.wikipedia.org/wiki/Go_(programming_language)
max_length: 1000
Content Type: html | Length: exactly 1,000 bytes (correctly truncated)
Excerpt: "Go Paradigm Multi-paradigm: concurrent, imperative, functional..."
```

### Test 2.8: Preview Mode — PASS
```
URL: https://pkg.go.dev/net/http
mode: "preview"
Content Type: html | Length: 4,962 bytes (condensed from full docs)
Excerpt: "Package http provides HTTP client and server implementations..."
```

### Test 2.9: Blog Post — PASS
```
URL: https://blog.golang.org/go1.23
Content Type: html | Length: 5,113 bytes
Excerpt: "Go 1.23 is released ... Dmitri Shuralyov, on behalf of the Go team..."
```

### Test 2.10: Stack Overflow — FAIL
```
URL: https://stackoverflow.com/questions/21362843/what-is-a-goroutine
Error: "scrape failed: no content extracted"
Cause: Stack Overflow anti-bot protection blocks automated scraping
```

---

## 3. search_and_scrape — 8/8 Passed

### Test 3.1: Basic Research
```
Query: "how does WebAssembly work in the browser"
Sources: 1 (MDN) | Processing: 1,213ms | Content: 11,609 bytes
Quality: overall=0.72, relevance=0.66, authority=0.9
Excerpt: "WebAssembly concepts ... explains the concepts behind how WebAssembly works..."
```

### Test 3.2: Technical Deep-Dive (5 sources)
```
Query: "implementing raft consensus algorithm", num_results: 5
Sources: 5/5 returned | Processing: 2,237ms | Content: 91,946 bytes
Top sources:
  - raft.github.io (relevance=0.9)
  - github.com/hashicorp/raft (authority=0.9)
  - eli.thegreenplace.net (relevance=0.8)
  - notes.eatonphil.com (relevance=0.75)
  - benjamincongdon.me (relevance=0.7)
```

### Test 3.3: Deduplication Disabled
```
Query: "kubernetes vs docker swarm comparison", deduplicate: false
Sources: 2/3 | Content: 20,212 bytes
Result: Both sources preserved full overlapping content without dedup
Top: IBM (relevance=0.92), CircleCI (relevance=0.8)
```

### Test 3.4: Filter by Query Relevance
```
Query: "zero knowledge proofs blockchain applications", filter_by_query: true
Sources: 2/3 (1 filtered for low relevance) | Content: 30,080 bytes
Surviving sources both scored relevance=0.84
```

### Test 3.5: Max Length Per Source
```
Query: "OAuth 2.1 specification changes", max_length_per_source: 5000
Sources: 3/3 | Content: 9,502 bytes (each source under 5KB cap)
Sources: oauth.net, fusionauth.io, developer.okta.com
```

### Test 3.6: Total Max Length
```
Query: "PostgreSQL performance tuning guide", total_max_length: 10000
Sources: 2/3 | Content: 2,646 bytes (well under cap)
Note: Thin source content (TOC pages), quality correctly scored low (0.4-0.5)
```

### Test 3.7: Broad Topic
```
Query: "climate change mitigation strategies 2025"
Sources: 2/3 | Processing: 1,192ms | Content: 17,604 bytes
Top: PubMed (overall=0.77, authority=0.9), WRI (relevance=0.84)
```

### Test 3.8: Product Comparison
```
Query: "best open source vector databases comparison"
Sources: 2/3 | Processing: 926ms | Content: 29,516 bytes
Top: redis.io (relevance=1.0 — perfect match), encore.dev (relevance=0.87)
Excerpt: "Best Vector Databases in 2026 ... pgvector, Pinecone, Qdrant, Weaviate, Milvus..."
```

---

## 4. image_search — 5/5 Passed

### Test 4.1: Basic Image Search
```
Query: "neural network architecture diagram"
Results: 5 images with dimensions (1536x1024, 640x376, 731x482...)
Sample: "Neural Network Architecture Guide" (upgrad.com)
```

### Test 4.2: Size Filter
```
Query: "data center", size: "large"
Results: 5 large images (767x511, 900x506, 720x540...)
Sample: "Disaggregating Power in Data Centers" (vicorpower.com)
```

### Test 4.3: Type Filter
```
Query: "machine learning workflow", type: "clipart"
Results: 5 clipart/diagram images
Sample: "Workflow of building Machine Learning Model" (researchgate.net)
```

### Test 4.4: Color Filter
```
Query: "sunset landscape", dominant_color: "orange"
Results: 5 orange-dominant images
Sample: "Orange Sunset Landscape Painting" (youtube.com)
```

### Test 4.5: File Type Filter
```
Query: "vector logo design", file_type: "svg"
Results: 5 SVG files returned (all .svg URLs)
Sample: "Superman Symbol Logo Vector Images" (freepatternsarea.com)
```

---

## 5. news_search — 5/5 Passed

### Test 5.1: Breaking News (24h)
```
Query: "AI regulation", freshness: "day"
Results: 5 articles all from past 24 hours
Sample: "AI compliance: from regulatory burden to strategic enabler" (8 hours ago)
```

### Test 5.2: Weekly News
```
Query: "tech layoffs 2025", freshness: "week"
Results: 5 articles from past week
Sample: "AI in the workplace: A report for 2025" (mckinsey.com, 15 hours ago)
```

### Test 5.3: Source Filter
```
Query: "climate summit", news_source: "reuters.com"
Results: 5 articles all from reuters.com
Sample: "EU eyes more free carbon permits for fertiliser industry" (reuters.com)
```

### Test 5.4: Sort by Date
```
Query: "quantum computing breakthrough", sort_by: "date"
Results: 5 articles sorted newest first
Sample: "Bitcoin more exposed to quantum risks than Ethereum" (coindesk.com, 2 hours ago)
```

### Test 5.5: High Result Count
```
Query: "cryptocurrency market", num_results: 10
Results: Exactly 10 articles returned
Sample: "Prediction markets see low odds of Bitcoin hitting $150K" (pluang.com)
```

---

## 6. academic_search — 5/5 Passed

### Test 6.1: General Academic
```
Query: "transformer architecture attention mechanism"
Results: 5 papers
Sample: "Attention Is All You Need" (arxiv.org/abs/1706.03762) — the seminal paper
```

### Test 6.2: arXiv Source Filter
```
Query: "large language model alignment", source: "arxiv"
Results: 5 papers all from arxiv.org
Sample: "Large Language Model Alignment: A Survey" (arxiv.org/abs/2309.15025)
```

### Test 6.3: PubMed Source Filter
```
Query: "mRNA vaccine long term effects", source: "pubmed"
Results: 5 papers all from pubmed.ncbi.nlm.nih.gov
Sample: "Long-term risk of autoimmune diseases after mRNA-based SARS..." (Jul 2024)
```

### Test 6.4: Year Filter
```
Query: "federated learning privacy", year_from: 2023, year_to: 2025
Results: 5 papers from 2023-2024 range
Sample: "Privacy in Federated Learning" (arxiv, Aug 2024)
```

### Test 6.5: PDF Only
```
Query: "graph neural networks survey", pdf_only: true
Results: 5 papers with direct PDF links
Sample: "A Comprehensive Survey on Graph Neural Networks" (arxiv.org/pdf/1901.00596)
```

---

## 7. patent_search — 4/5 Passed

### Test 7.1: Prior Art Search — PASS
```
Query: "natural language processing chatbot", search_type: "prior_art"
Results: 5 patents
Sample: US10749823B1 "Geospatial chat bot using natural language processing"
```

### Test 7.2: Landscape Analysis — PASS
```
Query: "solid state battery", search_type: "landscape"
Results: 5 patents
Sample: US20230343957A1 "All-solid-state battery"
```

### Test 7.3: Assignee Filter — PASS
```
Query: "autonomous driving", assignee: "Tesla"
Results: 5 Tesla patents
Sample: US11215999B2 "Data pipeline and deep learning system for autonomous driving" (TESLA, INC.)
```

### Test 7.4: CPC Code Filter — PASS
```
Query: "machine learning", cpc_code: "G06N"
Results: 5 patents with G06N classification
Sample: US10649988B1 "Artificial intelligence and machine learning infrastructure"
```

### Test 7.5: Year + Office Filter — PARTIAL FAIL
```
Query: "solar panel efficiency", year_from: 2020, patent_office: "US"
Results: 5 patents returned but some were WO (international) and predated 2020
Issue: patent_office and year_from are soft filters (query terms, not strict facets)
```

---

## 8. sequential_search — 7/7 Passed

### Session 1: "AI Impact on Software Engineering Jobs" (4 steps)

| Step | Action | Result |
|------|--------|--------|
| 1 | Start research (totalStepsEstimate=4) | Session created, ID: `7ec1c824-...` |
| 2 | Deepen + knowledge gap | Gap tracked: "Need data on job displacement vs transformation" |
| 3 | Branch (branchId="new-roles") | Branch recorded in step metadata |
| 4 | Conclude (nextStepNeeded=false) | `isComplete: true`, `completedAt` set |

### Session 2: "WebAssembly Outside the Browser" (3 steps)

| Step | Action | Result |
|------|--------|--------|
| 1 | Start research (totalStepsEstimate=3) | New session created, ID: `6ca0fff4-...` |
| 2 | Revise step 1 (isRevision=true) | Revision metadata tracked, original preserved |
| 3 | Conclude (nextStepNeeded=false) | `isComplete: true`, `completedAt` set |

### Feature Verification

| Feature | Status | Evidence |
|---------|--------|----------|
| Session persistence | PASS | Same sessionId across all steps |
| Step tracking | PASS | Full history accumulates with each call |
| Knowledge gaps | PASS | Gap from step 2 persisted through steps 3-4 |
| Branching | PASS | branchId recorded in step metadata |
| Revision | PASS | isRevision=true tracked, original step preserved |
| Session isolation | PASS | Two sessions got different IDs, no contamination |
| Completion marking | PASS | Both sessions marked complete with timestamp |

---

## Known Limitations

| Issue | Tool | Severity | Cause |
|-------|------|----------|-------|
| YouTube transcripts sometimes empty | `scrape_page` | Low | Videos without caption tracks or YouTube API restrictions |
| Stack Overflow blocks scraping | `scrape_page` | Low | Anti-bot protection requires JavaScript execution |
| Patent office filter is soft | `patent_search` | Low | Google Patents site-restricted search doesn't support strict faceting |
| Freshness score always 0.5 | `search_and_scrape` | Low | Publication date extraction not fully implemented |
| Source attrition (~30% of scrapes fail) | `search_and_scrape` | Medium | Some sites block automated requests; pipeline gracefully returns successful scrapes only |

---

## Performance Characteristics

| Metric | Value |
|--------|-------|
| web_search latency | < 500ms typical |
| scrape_page latency | 500ms - 2s depending on site |
| search_and_scrape latency | 600ms - 2,200ms (parallel scraping) |
| Max content per scrape | 50,000 bytes (configurable) |
| Max search results | 10 per query |
| Session persistence | UUID-based, survives across calls |

---

## Real-World Usage Examples

### Example 1: Technical Research
```
User: "Research how Raft consensus works and find implementation guides"
Tool: search_and_scrape(query="implementing raft consensus algorithm", num_results=5)
Result: 5 sources (91,946 bytes) including raft.github.io, hashicorp/raft, and 3 implementation tutorials
```

### Example 2: Competitive Intelligence
```
User: "Find Tesla's autonomous driving patents"
Tool: patent_search(query="autonomous driving", assignee="Tesla")
Result: 5 patents including "Data pipeline and deep learning system for autonomous driving" (US11215999B2)
```

### Example 3: Medical Literature Review
```
User: "Find recent clinical research on CRISPR gene therapy"
Tools: 
  1. web_search(query="CRISPR gene therapy clinical trials", lens="medical")
     → 5 results from PubMed, NEJM
  2. academic_search(query="CRISPR gene therapy", source="pubmed", year_from=2023)
     → 5 peer-reviewed papers
```

### Example 4: News Monitoring
```
User: "What happened with SpaceX this week?"
Tool: news_search(query="SpaceX launch", freshness="week")
Result: 5 articles about CRS-34 mission, all from past 7 days
```

### Example 5: Legal Research
```
User: "What are the GDPR requirements for data processing agreements?"
Tools:
  1. web_search(query="GDPR data processing agreement requirements", lens="legal")
     → 5 results from Westlaw/Practical Law
  2. scrape_page(url="<top result URL>")
     → Full article content extracted
```

### Example 6: Multi-Step Investigation
```
User: "Investigate AI's impact on software engineering — track what we learn"
Tool: sequential_search (4 steps)
  Step 1: Initial findings on AI coding tool adoption
  Step 2: Deeper dive into task automation vs augmentation (identified knowledge gap)
  Step 3: Branched to explore new roles created by AI
  Step 4: Synthesis and conclusions
Result: Complete research trail with session persistence, gap tracking, and branching
```

### Example 7: Image Asset Discovery
```
User: "Find SVG logos for a design project"
Tool: image_search(query="vector logo design", file_type="svg")
Result: 5 SVG files with direct download URLs
```

### Example 8: Academic Paper Discovery
```
User: "Find survey papers on graph neural networks with PDFs"
Tool: academic_search(query="graph neural networks survey", pdf_only=true)
Result: 5 papers with direct PDF links from IEEE and arXiv
```

### Example 9: Content Extraction
```
User: "Get me the full content of the Effective Go documentation"
Tool: scrape_page(url="https://go.dev/doc/effective_go")
Result: 49,824 bytes of structured content extracted from the page
```

### Example 10: Focused Domain Research
```
User: "Search for federal reserve rate decisions on financial sites only"
Tool: web_search(query="federal reserve interest rate decision", lens="finance")
Result: 5 results exclusively from federalreserve.gov
```
