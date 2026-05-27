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

**Response** contains: `urls` (array of result URLs), `query` (echoed back), `resultCount`, and `results` (array with `title`, `url`, `snippet`, `displayLink` for each result). Results are saved temporarily — if you run the same search again, it responds instantly without using another API call.

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

The "programming" lens focuses your search on trusted developer sources — Stack Overflow, GitHub, Go docs, MDN, and other curated sites. This means fewer noise results and more relevant answers. Available lenses: `docs`, `academic`, `clinical`, `security`, `journalism`, `programming`, `news`, `tech`, `legal`, `medical`, `finance`, `science`, `government`.

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

**Response** contains: `query`, `combinedContent` (merged extracted text), `sources` (array with `url`, `title`, `content`, `contentType`, `scores` — included when `include_sources=true`), `summary` (`urlsSearched`, `urlsScraped`, `processingTimeMs`), and `sizeMetadata` (`totalLength`, `estimatedTokens`, `sizeCategory`). Duplicate paragraphs are removed, and long content is trimmed at sentence breaks so nothing cuts off mid-thought.

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

**Response** contains: `papers` (array of `{title, url, source, abstract}`), `query`, `totalResults`, `resultCount`, and `source`. Results come from scholarly databases (arXiv, PubMed, IEEE, Nature, Springer). If you want the full text of a paper, follow up by reading the URL — the tool handles PDFs and paywalled previews automatically.

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

**Response** contains: `patents` (array of `{title, url, number, abstract, assignee, inventor, filed, granted, pdf, status}`), `query`, `searchType`, `resultCount`, `source` (which provider answered), and `searchUrl`. You can filter by patent office (US, European, international, Japan, China, Korea) and by technology category codes. The server automatically picks the best data source for your region.

---

## News Monitoring

Search recent news with freshness controls and source filtering.

```json
{
  "tool": "news_search",
  "arguments": {
    "query": "artificial intelligence regulation",
    "freshness": "week",
    "num_results": 5
  }
}
```

**Response** contains: `articles` (array of `{title, url, source, publishedAt, snippet}`), `query`, and `resultCount`. Use `freshness` values: `hour`, `day`, `week`, `month`, `year` to control how recent articles must be.

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

Extract content from any URL — web pages, PDFs, DOCX, PPTX, or YouTube transcripts.

```json
{
  "tool": "scrape_page",
  "arguments": {
    "url": "https://go.dev/blog/context"
  }
}
```

**Response** contains: `url`, `content` (extracted text), `contentType` (html/markdown/youtube/pdf/docx/pptx), `contentLength`, `truncated`, `estimatedTokens`, `sizeCategory`, `citation` (with APA/MLA formatted citations), and optionally `metadata` (`{title, author}`). The tool uses the fastest method available and only launches a full browser for sites that require JavaScript — so most pages load in under a second.

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

**Response** contains the full session state: `sessionId`, `currentStep`, `isComplete`, `steps` (array of all research steps with timestamps and reasoning), `gaps` (knowledge gaps identified), `startedAt`, and `completedAt`. Use `branchFromStep` + `branchId` to explore alternative research directions without losing the main thread.

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

**Response** contains: `researchGoal`, `summary`, `stepCount`, `stepIndex` (one-liner per step with confidence), `lastSteps` (last 3 full steps), and `gaps` (open questions).

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

## Combining Tools for Deep Research

A typical research workflow combines multiple tools:

1. **web_search** with a lens to find relevant sources
2. **scrape_page** on the most promising URLs to get full content
3. **academic_search** or **news_search** for domain-specific depth
4. **sequential_search** to track progress across multiple steps

The AI assistant orchestrates these tools automatically based on the research question — you don't need to call them manually.
