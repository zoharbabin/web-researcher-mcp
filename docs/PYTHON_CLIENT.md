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

All typed methods strip `None` keyword arguments before sending — pass only what you need.

### `web_search`

```python
result: SearchResponse = await client.web_search(
    query,
    num_results=5,        # 1–100
    time_range=None,      # "day" | "week" | "month" | "year"
    safe=None,            # "active" | "off"
    language=None,        # ISO 639-1 code
    site=None,            # restrict to domain
    exact_terms=None,
    exclude_terms=None,
    country=None,         # ISO 3166-1 alpha-2
    lens=None,            # lens key from lenses catalog
    provider=None,        # "google" | "brave" | "duckduckgo" | ...
    session_id=None,
    claim=None,           # claim text for claimSignal scoring
)
```

### `scrape_page`

```python
result: ScrapeResult = await client.scrape_page(
    url,
    raw=False,            # True -> skip markdown conversion
    max_length=None,      # character cap
    session_id=None,
)
```

### `search_and_scrape`

```python
result: SearchAndScrapeResult = await client.search_and_scrape(
    query,
    num_results=3,
    provider=None,
    lens=None,
    claim=None,
    session_id=None,
)
```

### `image_search`

```python
result: ImageSearchResponse = await client.image_search(query, num_results=5, provider=None)
```

### `news_search`

```python
result: NewsSearchResponse = await client.news_search(
    query,
    num_results=5,
    time_range=None,      # "day" | "week" | "month" | "year"
    provider=None,
)
```

### `academic_search`

```python
result: AcademicSearchResponse = await client.academic_search(
    query,
    num_results=5,
    year_from=None,
    year_to=None,
    provider=None,
    open_access_only=False,
)
```

### `verify_citation`

```python
result: VerifyResult = await client.verify_citation(
    citation,             # DOI, URL, or free-text reference
    claim=None,           # optional claim to check against the cited work
)
```

### `audit_bibliography`

```python
result: AuditBibliographyResult = await client.audit_bibliography(
    bibliography=None,    # BibTeX / RIS / APA string
    format="auto",        # "bibtex" | "ris" | "apa" | "csl-json" | "auto"
    entries=None,         # list of {"doi": ..., "url": ..., ...} dicts
    session_id=None,
)
```

### `archive_source`

```python
result: ArchiveResult = await client.archive_source(url)
```

### `verify_recommendation`

```python
result: dict = await client.verify_recommendation([
    {"title": "Best tool", "url": "https://example.com", "author": "Acme", "authorBio": "..."},
])
```

### `sequential_search`

```python
result: dict = await client.sequential_search(
    search_step,           # query or research action for this step
    step_number=1,
    next_step_needed=False,
    total_steps_estimate=None,
    session_id=None,       # omit to start a new session
    is_revision=None,
    revises_step=None,
    branch_from_step=None,
    branch_id=None,
    knowledge_gap=None,
)
```

### `patent_search`

```python
result: dict = await client.patent_search(
    query,
    num_results=5,
    search_type=None,      # "keyword" | "classification" | "assignee" | "inventor"
    patent_office=None,    # "USPTO" | "EPO" | "WIPO"
    assignee=None,
    inventor=None,
    cpc_code=None,
    year_from=None,
    year_to=None,
    provider=None,
)
```

### `econ_search`

```python
result: dict = await client.econ_search(
    query,
    series_id=None,        # FRED series ID
    country=None,          # ISO 3166-1 alpha-2
    date_from=None,        # "YYYY-MM-DD"
    date_to=None,
    frequency=None,        # "annual" | "quarterly" | "monthly"
    num_results=5,
    provider=None,
)
```

### `legal_search`

```python
result: dict = await client.legal_search(
    query,
    jurisdiction=None,
    date_from=None,
    date_to=None,
    num_results=5,
    provider=None,
)
```

### `clinical_search`

```python
result: dict = await client.clinical_search(
    query,
    condition=None,
    intervention=None,
    sponsor=None,
    status=None,
    num_results=5,
    provider=None,
)
```

### `filing_search`

```python
result: dict = await client.filing_search(
    query,
    form_type=None,
    ticker=None,
    date_from=None,
    date_to=None,
    num_results=5,
    provider=None,
)
```

### Session / bibliography helpers

```python
session: dict = await client.get_research_session(session_id)
export:  dict = await client.research_export(session_id, format="markdown")
biblio:  dict = await client.format_bibliography(session_id=None, format="apa", urls=None)
graph:   dict = await client.citation_graph(doi=None, title=None, depth=1, influential_only=False)
```

### Generic passthrough

```python
raw: dict   = await client.call("web_search", query="test", num_results=3)
tools: list = await client.list_tools()
```

`call()` returns the raw parsed JSON dict. It strips `None` values but does not return a typed model.

## Return types

All typed methods return dataclass instances defined in `web_researcher_mcp.models`.

| Method | Return type |
|--------|-------------|
| `web_search` | `SearchResponse` |
| `scrape_page` | `ScrapeResult` |
| `search_and_scrape` | `SearchAndScrapeResult` |
| `image_search` | `ImageSearchResponse` |
| `news_search` | `NewsSearchResponse` |
| `academic_search` | `AcademicSearchResponse` |
| `verify_citation` | `VerifyResult` |
| `audit_bibliography` | `AuditBibliographyResult` |
| `archive_source` | `ArchiveResult` |
| all others | `dict[str, Any]` |

### Key model shapes

**`SearchResponse`**

```python
@dataclass
class SearchResponse:
    query: str
    results: list[SearchResult]     # each has title, url, snippet, displayLink, claimSignal
    resultCount: int
    urls: list[str]
    hints: dict[str, Any]
```

**`ScrapeResult`**

```python
@dataclass
class ScrapeResult:
    url: str
    content: str                    # Markdown or raw HTML
    contentType: str
    contentLength: int
    truncated: bool
    estimatedTokens: int
    sizeCategory: str               # "tiny" | "small" | "medium" | "large" | "huge"
    raw: bool
    extractionQuality: str          # "high" | "medium" | "low"
    extractedBy: str                # "markdown" | "stealth" | "html" | "browser"
    citation: Optional[Citation]
    metadata: dict[str, Any]
    sourceType: str
    authorityTier: str
    domainCategory: str
    detectedDoi: str
    retractionStatus: Optional[dict[str, Any]]
```

**`VerifyResult`**

```python
@dataclass
class VerifyResult:
    input: str
    inputType: str                  # "doi" | "url" | "text"
    exists: Optional[bool]
    matchedRecord: Optional[dict]   # raw Crossref/OpenAlex record
    matchConfidence: str            # "exact" | "fuzzy" | "none"
    detectedDoi: str
    titleMatch: str                 # "exact" | "partial" | "none"
    retractionStatus: Optional[RetractionStatus]
    httpStatus: Optional[int]
    archivedUrl: str
    provenance: list[str]
    claim: str
    claimSupport: str               # "supported" | "partially_supported" | "not_supported" | "unchecked"
    claimEvidence: list[str]
    claimSourceUrl: str
    contrastSignal: Optional[bool]
    conflictOfInterest: Optional[dict]
```

**`AuditBibliographyResult`**

```python
@dataclass
class AuditBibliographyResult:
    source: str                     # "entries" | "bibliography" | "session"
    entryCount: int
    summary: AuditSummary           # total/retracted/deadLink/notFound/unchecked/mischaracterized/ok
    entries: list[BibEntryAudit]
    skipped: Optional[int]
    checkedAt: str
```

**`ArchiveResult`**

```python
@dataclass
class ArchiveResult:
    requestedUrl: str
    snapshotUrl: str
    archivedAt: str
    captured: bool
    status: str                     # "captured" | "pending" | "failed" | "already_exists"
    httpStatus: int
    reason: str
    pollUrl: str
    source: str                     # "wayback"
    provenance: list[str]
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
make test-python                              # pytest tests/python/ -v
python3 -m pytest tests/python/ -v           # direct
python3 -m unittest tests.python.test_client # stdlib runner
```

Tests use a stdlib `HTTPServer` mock — no API keys, no binary, no network required.
