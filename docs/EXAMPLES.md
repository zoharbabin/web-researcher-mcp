# Usage Examples

Real-world examples showing how to call each tool with JSON arguments and what to expect in the response.

---

## Quick Web Search

Search the web and get structured results with URLs, titles, and snippets.

```json
{
  "tool": "web_search",
  "arguments": {
    "query": "MCP Model Context Protocol specification",
    "num_results": 5
  }
}
```

**Response** contains: `urls` (array of result URLs), `query` (echoed back), `resultCount`, and `results` (array with `title`, `url`, `snippet`, `displayLink` for each result). Results are cached — repeated identical queries return instantly.

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

The `programming` lens restricts results to sites like stackoverflow.com, github.com, go.dev, developer.mozilla.org, and other curated programming resources. Available lenses include: `programming`, `news`, `tech`, `legal`, `medical`, `finance`, `science`, `government`.

---

## Deep Research with search_and_scrape

Combines search and content extraction in a single call. Searches the web, then scrapes the top results to extract full-text content.

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

**Response** contains: `query`, `combinedContent` (merged extracted text), `sources` (array with `url`, `title`, `content`, `contentType`, `scores` — included when `include_sources=true`), `summary` (`urlsSearched`, `urlsScraped`, `processingTimeMs`), and `sizeMetadata` (`totalLength`, `estimatedTokens`, `sizeCategory`). Content is sanitized, deduplicated, and truncated at natural sentence boundaries.

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

**Response** contains: `urls`, `query`, `resultCount`, and `results` with academic-specific fields. The search targets scholarly databases including arXiv, PubMed, IEEE, Nature, and Springer. Pair with `scrape_page` to extract full paper content from accessible URLs.

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

**Response** contains: `urls`, `query`, `resultCount`, and `results` with patent-specific metadata. Supports strict office filtering (US, EP, WO, JP, CN, KR) and CPC classification codes for precise patent landscape mapping.

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

**Response** contains: `urls`, `query`, `resultCount`, and `results` with news-specific fields (publication date, source). Use `time_range` values: `day`, `week`, `month`, `year` to control freshness.

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

**Response** contains: `urls`, `query`, `resultCount`, and `results` with image-specific fields (`thumbnailUrl`, `width`, `height`, `contextUrl`). Filter options: `size` (small/medium/large), `type` (photo/lineart/clipart/animated), `color` (various), `format` (jpg/png/gif/webp).

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

**Response** contains: `url`, `title`, `content` (full extracted text in markdown format), `contentLength`, `method` (which tier succeeded: markdown/stealth/html/browser), and `qualityScore`. The 4-tier pipeline tries lightweight methods first and only falls back to the headless browser for JavaScript-heavy sites.

---

## Multi-Step Investigation (sequential_search)

Track multi-step research with persistent sessions for iterative investigation.

### Step 1: Start a new session

```json
{
  "tool": "sequential_search",
  "arguments": {
    "searchStep": "Initial research on MCP server implementations in Go",
    "stepNumber": 1,
    "nextStepNeeded": true,
    "totalStepsEstimate": 3
  }
}
```

**Response** returns a `sessionId` that you use for subsequent steps.

### Step 2: Continue the session

```json
{
  "tool": "sequential_search",
  "arguments": {
    "sessionId": "abc123-from-step-1",
    "searchStep": "Compared caching strategies across implementations",
    "stepNumber": 2,
    "nextStepNeeded": true,
    "knowledgeGap": "Need to understand how other servers handle multi-tenancy"
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
    "nextStepNeeded": false
  }
}
```

**Response** contains the full session state: `sessionId`, `currentStep`, `isComplete`, `steps` (array of all research steps with timestamps), `gaps` (knowledge gaps identified), `startedAt`, and `completedAt`. Use `branchFromStep` + `branchId` to explore alternative research directions without losing the main thread.

---

## Combining Tools for Deep Research

A typical research workflow combines multiple tools:

1. **web_search** with a lens to find relevant sources
2. **scrape_page** on the most promising URLs to get full content
3. **academic_search** or **news_search** for domain-specific depth
4. **sequential_search** to track progress across multiple steps

The AI assistant orchestrates these tools automatically based on the research question — you don't need to call them manually.
