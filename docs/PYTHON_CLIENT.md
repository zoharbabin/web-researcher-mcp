# Python Client Library

A typed, async-first Python SDK bundled inside the `web-researcher-mcp` PyPI wheel. No separate install needed — the client ships with the binary.

```bash
pip install web-researcher-mcp
# or
uv add web-researcher-mcp
```

## Quickstart

```python
import asyncio
from web_researcher_mcp import WebResearcherClient

async def main():
    async with WebResearcherClient() as client:
        result = await client.web_search("CRISPR off-target effects 2024")
        for r in result.results:
            print(r.title, r.url)

asyncio.run(main())
```

`WebResearcherClient()` with no arguments auto-starts the bundled Go binary on a free loopback port and stops it on context exit. Pass `port=<n>` to connect to an already-running instance instead.

## Sync usage (no async/await)

```python
from web_researcher_mcp import WebResearcherClient

with WebResearcherClient.sync() as client:
    result = client.web_search("climate tipping points")
    print(result.resultCount, "results")
```

`WebResearcherClient.sync()` returns a `SyncWebResearcherClient` that runs the event loop in a background daemon thread.

## Connecting to a running server

```python
async with WebResearcherClient(port=8080) as client:
    result = await client.web_search("quantum computing")
```

## Constructor parameters

Both `WebResearcherClient` and `SyncWebResearcherClient` accept identical keyword arguments:

| Parameter | Default | Description |
|-----------|---------|-------------|
| `port` | `None` | Port of an existing HTTP server. `None` = auto-start the bundled binary. |
| `timeout` | `120.0` | Per-request HTTP timeout (seconds). |
| `server_env` | `None` | Extra env vars forwarded to the subprocess when auto-starting. |
| `startup_timeout` | `30.0` | Seconds to wait for the binary's `/health/live` endpoint. |

## Tool methods

Every tool has a typed method on the client. The method's keyword arguments mirror the tool's JSON parameter names verbatim — see [`docs/TOOLS.md`](TOOLS.md) for the authoritative, CI-gated parameter schemas — and each returns a typed `<Name>Response` dataclass from `web_researcher_mcp.models`. `None` keyword arguments are stripped before sending, so pass only what you need.

The examples below show the common tools; the full set is generated from the live Go schemas (`make gen-python-client`), so the in-editor signature is always the source of truth.

### `web_search`

```python
result: WebSearchResponse = await client.web_search(
    query,
    num_results=5,        # 1–100
    time_range=None,      # "day" | "week" | "month" | "year"
    safe=None,            # "active" | "off"
    language=None,        # ISO 639-1 code
    site=None,            # restrict to domain
    exact_terms=None,
    exclude_terms=None,
    country=None,         # ISO 3166-1 alpha-2
    lens=None,            # lens key from the lenses://catalog resource
    provider=None,        # "google" | "brave" | "duckduckgo" | ...
    sessionId=None,
    claim=None,           # claim text for claimSignal scoring
)
```

### `scrape_page`

```python
result: ScrapePageResponse = await client.scrape_page(
    url,
    mode=None,            # "full" | "preview" | "raw" (default full markdown)
    max_length=None,      # character cap
    sessionId=None,
)
```

### `search_and_scrape`

```python
result: SearchAndScrapeResponse = await client.search_and_scrape(
    query,
    num_results=None,
    provider=None,
    claim=None,
    deduplicate=None,
    filter_by_query=False,
    include_sources=None,
    max_length_per_source=None,
    total_max_length=None,
    sessionId=None,
)
```

### `image_search`

```python
result: ImageSearchResponse = await client.image_search(
    query,
    num_results=5,
    provider=None,
    color_type=None,   # e.g. "color" | "gray" | "mono" | "trans"
    dominant_color=None,
    file_type=None,    # e.g. "jpg" | "png" | "gif"
    safe=None,         # "active" | "off"
    size=None,         # e.g. "large" | "medium" | "icon"
    type=None,         # e.g. "photo" | "clipart" | "face"
)
```

See [`docs/TOOLS.md`](TOOLS.md) for the full parameter schema.

### `news_search`

```python
result: NewsSearchResponse = await client.news_search(
    query,
    num_results=5,
    time_range=None,      # "day" | "week" | "month" | "year"
    news_source=None,     # restrict to a specific news outlet
    sort_by=None,
    provider=None,
    sessionId=None,
)
```

See [`docs/TOOLS.md`](TOOLS.md) for the full parameter schema.

### `academic_search`

```python
result: AcademicSearchResponse = await client.academic_search(
    query,
    num_results=5,
    year_from=None,
    year_to=None,
    provider=None,
    open_access=False,    # restrict to open-access papers
    pdf_only=False,
    sort_by=None,
    source=None,
)
```

### `verify_citation`

```python
result: VerifyCitationResponse = await client.verify_citation(
    citation,             # DOI, URL, or free-text reference
    claim=None,           # optional claim to check against the cited work
)
```

### `audit_bibliography`

```python
result: AuditBibliographyResponse = await client.audit_bibliography(
    bibliography=None,    # CSL-JSON / RIS / BibTeX string
    format=None,          # "bibtex" | "ris" | "csl-json" (auto-detected when omitted)
    entries=None,         # explicit list of {"doi": ..., "url": ..., ...} dicts
    sessionId=None,       # audit a sequential_search session instead
)
```

### `archive_source`

```python
result: ArchiveSourceResponse = await client.archive_source(url)
```

### `verify_recommendation`

```python
result: VerifyRecommendationResponse = await client.verify_recommendation([
    {"title": "Best tool", "url": "https://example.com", "author": "Acme", "authorBio": "..."},
])
```

### `sequential_search`

The three positional arguments are required; the following shows common optional keyword arguments (note the camelCase names — they match the JSON field names exactly). See [`docs/TOOLS.md`](TOOLS.md) for the complete list.

```python
result: SequentialSearchResponse = await client.sequential_search(
    search_step,           # query or research action for this step
    step_number,           # 1-based step index
    next_step_needed,      # bool — False ends the session
    sessionId=None,        # omit to start a new session
    totalStepsEstimate=None,
    isRevision=False,
    revisesStep=None,
    branchFromStep=None,
    branchId=None,
    knowledgeGap=None,
    researchGoal=None,
)
```

### Structured-domain searches

`patent_search`, `econ_search`, `legal_search`, `clinical_search`, and `filing_search` follow the same shape — all keyword arguments, all optional, returning their typed `<Name>Response`. The exact parameters are documented per-tool in [`docs/TOOLS.md`](TOOLS.md):

```python
patents:  PatentSearchResponse   = await client.patent_search(query="mRNA vaccine", patent_office="USPTO")
econ:     EconSearchResponse     = await client.econ_search(series_id="GDP", date_from="2020-01-01")
cases:    LegalSearchResponse    = await client.legal_search(query="fair use", jurisdiction="ca9")
trials:   ClinicalSearchResponse = await client.clinical_search(condition="melanoma", status="recruiting")
filings:  FilingSearchResponse   = await client.filing_search(ticker="AAPL", form_type="10-K")
```

### Session / bibliography helpers

```python
session: GetResearchSessionResponse = await client.get_research_session(sessionId, stepId=None)
export:  ResearchExportResponse     = await client.research_export(sessionId, format="markdown")
biblio:  FormatBibliographyResponse = await client.format_bibliography(sessionId=None, sources=None, style=None)
graph:   CitationGraphResponse      = await client.citation_graph(paper, direction=None, influential_only=False,
                                                                   num_results=None, provider=None, sessionId=None)
```

`citation_graph`'s `paper` is a DOI or title; `format_bibliography` takes either a `sessionId` or an explicit `sources` list (pass `style="apa"` to control the output format).

### Generic passthrough

```python
raw: dict   = await client.call("web_search", query="test", num_results=3)
tools: list = await client.list_tools()
```

`call()` returns the raw parsed JSON dict. It strips `None` values but does not return a typed model.

## Return types

Every typed method returns a dataclass named `<ToolName>Response`, defined in `web_researcher_mcp.models` (e.g. `web_search` → `WebSearchResponse`, `scrape_page` → `ScrapePageResponse`). A few short aliases are exported for ergonomics: `SearchResponse` (= `WebSearchResponse`), `ScrapeResult` (= `ScrapePageResponse`), `VerifyResult` (= `VerifyCitationResponse`), `ArchiveResult` (= `ArchiveSourceResponse`), `AuditBibliographyResult` (= `AuditBibliographyResponse`), `SearchAndScrapeResult` (= `SearchAndScrapeResponse`).

Each dataclass exposes a `from_dict` constructor and tolerates missing keys (every field defaults to `None`/empty), so partial server responses never raise. Inspect the generated `models.py` for the complete field set of any response.

### Key model shapes

**`WebSearchResponse`** (alias `SearchResponse`)

```python
@dataclass
class WebSearchResponse:
    query: Optional[str]
    results: list[WebSearchResult]  # each: title, url, snippet, displayLink, claimSignal
    resultCount: Optional[int]
    urls: list[str]
    hints: dict[str, Any]
    trust: Optional[str]            # trust marker for externally-sourced content
```

**`ScrapePageResponse`** (alias `ScrapeResult`)

```python
@dataclass
class ScrapePageResponse:
    url: Optional[str]
    content: Optional[str]          # Markdown or raw HTML
    contentType: Optional[str]
    contentLength: Optional[int]
    truncated: Optional[bool]
    estimatedTokens: Optional[int]
    sizeCategory: Optional[str]     # "tiny" | "small" | "medium" | "large" | "huge"
    raw: Optional[bool]
    extractionQuality: Optional[str]  # "high" | "medium" | "low"
    extractedBy: Optional[str]        # "markdown" | "stealth" | "html" | "browser"
    citation: Optional[Citation]
    metadata: Optional[ScrapePageMetadata]
    structuredData: Optional[StructuredData]
    sourceType: Optional[str]
    authorityTier: Optional[str]
    domainCategory: Optional[str]
    detectedDoi: Optional[str]
    retractionStatus: Optional[Any]
    trust: Optional[str]
```

**`VerifyCitationResponse`** (alias `VerifyResult`)

```python
@dataclass
class VerifyCitationResponse:
    input: Optional[str]
    inputType: Optional[str]        # "doi" | "url" | "text"
    exists: Optional[bool]
    matchedRecord: Optional[Any]    # raw Crossref/OpenAlex record
    matchConfidence: Optional[str]  # "exact" | "fuzzy" | "none"
    detectedDoi: Optional[str]
    titleMatch: Optional[str]       # "exact" | "partial" | "none"
    retractionStatus: Optional[RetractionStatus]
    httpStatus: Optional[int]
    archivedUrl: Optional[str]
    provenance: list[str]
    claim: Optional[str]
    claimSupport: Optional[str]     # "supported" | "partially_supported" | "not_supported" | "unchecked"
    claimEvidence: list[str]
    claimSourceUrl: Optional[str]
    contrastSignal: Optional[bool]
    conflictOfInterest: Optional[ConflictOfInterest]
    trust: Optional[str]
```

**`AuditBibliographyResponse`** (alias `AuditBibliographyResult`)

```python
@dataclass
class AuditBibliographyResponse:
    source: Optional[str]           # "entries" | "bibliography" | "session"
    entryCount: Optional[int]
    summary: Optional[AuditBibliographySummary]  # total/retracted/deadLink/notFound/unchecked/mischaracterized/ok
    entries: list[AuditBibliographyEntry]
    skipped: Optional[int]
    skippedNote: Optional[str]
    checkedAt: Optional[str]
    trust: Optional[str]
```

**`ArchiveSourceResponse`** (alias `ArchiveResult`)

```python
@dataclass
class ArchiveSourceResponse:
    requestedUrl: Optional[str]
    snapshotUrl: Optional[str]
    archivedAt: Optional[str]
    captured: Optional[bool]
    status: Optional[str]           # "captured" | "pending" | "failed" | "already_exists"
    httpStatus: Optional[int]
    reason: Optional[str]
    pollUrl: Optional[str]
    source: Optional[str]           # "wayback"
    provenance: list[str]
    trust: Optional[str]
```

## Error handling

```python
from web_researcher_mcp import MCPError

try:
    result = await client.verify_citation("fake-doi-xyz")
except MCPError as exc:
    print(exc.message, exc.code)
```

`MCPError` is raised when the server returns `isError: true` in the tool response or a JSON-RPC `error` object. It is a plain `Exception` subclass (not `RuntimeError`).

## LangChain integration

```python
from web_researcher_mcp import WebResearcherClient

async with WebResearcherClient() as client:
    tools = client.as_langchain_tools()   # list[StructuredTool | Tool]
    # pass tools to any LangChain agent
```

The sync client has the same method:

```python
with WebResearcherClient.sync() as client:
    tools = client.as_langchain_tools()
```

Each tool's name and description come directly from the server's `tools/list` response. Requires `langchain-core` (`pip install langchain-core`).

## Subprocess auto-start details

When `port=None` (the default), `WebResearcherClient` constructs a `_ServerProcess` that:

1. Picks a free loopback port via `socket.bind(("127.0.0.1", 0))`.
2. Launches the bundled binary as a subprocess with `PORT=<n>` in the environment.
3. Polls `GET /health/live` every 100 ms until HTTP 200 (up to `startup_timeout` seconds).
4. On `stop()` / context exit, sends SIGTERM and waits up to 5 s before SIGKILL.

Override `server_env` to forward API keys to the subprocess:

```python
async with WebResearcherClient(server_env={"GOOGLE_CUSTOM_SEARCH_API_KEY": "..."}) as client:
    result = await client.web_search("quantum error correction")
```

## Testing

```bash
make test-python                              # pytest tests/python/ --ignore=tests/python/test_live_e2e.py -v
python3 -m pytest tests/python/ -v           # direct
python3 -m unittest tests.python.test_client # stdlib runner
```

Tests use a stdlib `HTTPServer` mock — no API keys, no binary, no network required.
