"""
web_researcher_mcp.client
~~~~~~~~~~~~~~~~~~~~~~~~~
Async-first Python client for the web-researcher-mcp HTTP server.

AUTO-GENERATED — do not edit by hand.
Run: make gen-python-client

Usage (async, auto-start bundled binary)::

    async with WebResearcherClient() as c:
        result = await c.web_search("climate change 2025")
        print(result.query, result.results[0].title)

Usage (async, connect to running server)::

    async with WebResearcherClient(port=3000) as c:
        result = await c.web_search("climate change 2025")

Usage (sync)::

    with SyncWebResearcherClient() as c:
        result = c.web_search("climate change 2025")

Requires Python 3.10+.  Uses only the standard library.
"""
from __future__ import annotations

import asyncio
import json
import threading
import urllib.error
import urllib.request
from typing import Any, Optional

from web_researcher_mcp.models import (
    AcademicSearchResponse,
    AnswerResponse,
    ArchiveSourceResponse,
    AuditBibliographyResponse,
    CitationGraphResponse,
    ClinicalSearchResponse,
    EconSearchResponse,
    FilingSearchResponse,
    FormatBibliographyResponse,
    GetMyAnalyticsResponse,
    GetResearchSessionResponse,
    ImageSearchResponse,
    LegalSearchResponse,
    MCPError,
    MemoryRecallResponse,
    MemorySaveResponse,
    NewsSearchResponse,
    PatentSearchResponse,
    ResearchExportResponse,
    ScrapePageResponse,
    SearchAndScrapeResponse,
    SequentialSearchResponse,
    StructuredSearchResponse,
    VerifyCitationResponse,
    VerifyRecommendationResponse,
    WebSearchResponse,
    WorkspaceContributeResponse,
    WorkspaceReadResponse,
)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _strip_none(d: dict[str, Any]) -> dict[str, Any]:
    """Return a shallow copy of *d* with None-valued keys removed."""
    return {k: v for k, v in d.items() if v is not None}


def _parse_sse(data: bytes) -> dict[str, Any]:
    """Parse a text/event-stream body and return the last valid JSON data payload."""
    text = data.decode("utf-8", errors="replace")
    last_data: Optional[str] = None
    for line in text.splitlines():
        if line.startswith("data:"):
            payload = line[5:].strip()
            if not payload:
                continue
            try:
                json.loads(payload)
            except json.JSONDecodeError:
                continue
            last_data = payload
    if last_data is None:
        raise ValueError("No valid JSON data: line found in SSE response")
    return json.loads(last_data)


# ---------------------------------------------------------------------------
# Async client
# ---------------------------------------------------------------------------

class WebResearcherClient:
    """Async MCP client for web-researcher-mcp.

    Parameters
    ----------
    port:
        Port the web-researcher-mcp HTTP server is listening on.
        Pass ``None`` (the default) to auto-start the bundled binary on a
        free loopback port; the binary is stopped when the client closes.
    timeout:
        HTTP request timeout in seconds (default 120).
    server_env:
        Extra environment variables passed to the subprocess when *port* is
        ``None``.  Ignored when connecting to an already-running server.
    startup_timeout:
        Seconds to wait for the binary's ``/health/live`` endpoint when
        *port* is ``None`` (default 30).
    """

    def __init__(
        self,
        port: Optional[int] = None,
        timeout: float = 120.0,
        *,
        server_env: Optional[dict[str, str]] = None,
        startup_timeout: float = 30.0,
    ) -> None:
        self._timeout = timeout
        self._session_id: Optional[str] = None
        self._request_id: int = 0
        self._started: bool = False
        self._server = None

        if port is None:
            from web_researcher_mcp._server import _ServerProcess  # type: ignore[import]
            self._server = _ServerProcess(
                env=server_env,
                startup_timeout=startup_timeout,
            )
            self._port: int = 0  # updated in start()
        else:
            self._port = port

    # ------------------------------------------------------------------
    # Context-manager interface
    # ------------------------------------------------------------------

    async def __aenter__(self) -> "WebResearcherClient":
        await self.start()
        return self

    async def __aexit__(self, *_: Any) -> None:
        await self.stop()

    async def start(self) -> None:
        """Start the subprocess (if managed) and send the MCP initialize handshake.

        Idempotent: calling ``start()`` on an already-started client is a no-op.
        """
        if self._started:
            return
        if self._server is not None:
            self._port = await asyncio.to_thread(self._server.start)
        self._request_id = 0
        self._session_id = None
        await self._initialize()
        self._started = True

    async def stop(self) -> None:
        """Stop the subprocess if it was started by this client."""
        if self._server is not None:
            await asyncio.to_thread(self._server.stop)
        self._started = False

    # ------------------------------------------------------------------
    # Low-level transport
    # ------------------------------------------------------------------

    def _next_id(self) -> int:
        self._request_id += 1
        return self._request_id

    async def _http_post(self, body: dict[str, Any]) -> dict[str, Any]:
        url = f"http://127.0.0.1:{self._port}/mcp/"
        raw = json.dumps(body).encode("utf-8")
        headers: dict[str, str] = {
            "Content-Type": "application/json",
            "Accept": "application/json, text/event-stream",
        }
        if self._session_id:
            headers["Mcp-Session-Id"] = self._session_id

        req = urllib.request.Request(url, data=raw, headers=headers, method="POST")

        def _do_request() -> tuple[dict[str, Any], Optional[str]]:
            with urllib.request.urlopen(req, timeout=self._timeout) as resp:  # noqa: S310
                resp_data: bytes = resp.read()
                content_type: str = resp.headers.get("Content-Type", "")
                session_hdr: Optional[str] = resp.headers.get("Mcp-Session-Id")
                if "text/event-stream" in content_type:
                    parsed = _parse_sse(resp_data)
                else:
                    parsed = json.loads(resp_data)
            return parsed, session_hdr

        parsed, session_hdr = await asyncio.to_thread(_do_request)
        if session_hdr and self._session_id is None:
            self._session_id = session_hdr
        return parsed

    async def _request(self, method: str, params: Optional[dict[str, Any]] = None) -> Any:
        req_id = self._next_id()
        body: dict[str, Any] = {"jsonrpc": "2.0", "id": req_id, "method": method}
        if params is not None:
            body["params"] = params
        resp = await self._http_post(body)
        if "error" in resp:
            err = resp["error"]
            if isinstance(err, dict):
                raise MCPError(str(err.get("message", err)), code=str(err.get("code", "")))
            raise MCPError(str(err))
        return resp.get("result")

    async def _notify(self, method: str, params: Optional[dict[str, Any]] = None) -> None:
        body: dict[str, Any] = {"jsonrpc": "2.0", "method": method}
        if params is not None:
            body["params"] = params

        def _fire() -> None:
            raw = json.dumps(body).encode("utf-8")
            headers: dict[str, str] = {
                "Content-Type": "application/json",
                "Accept": "application/json, text/event-stream",
            }
            if self._session_id:
                headers["Mcp-Session-Id"] = self._session_id
            req = urllib.request.Request(
                f"http://127.0.0.1:{self._port}/mcp/",
                data=raw, headers=headers, method="POST",
            )
            try:
                with urllib.request.urlopen(req, timeout=self._timeout):  # noqa: S310
                    pass
            except urllib.error.HTTPError:
                pass

        await asyncio.to_thread(_fire)

    async def _initialize(self) -> None:
        await self._request(
            "initialize",
            {
                "protocolVersion": "2024-11-05",
                "capabilities": {},
                "clientInfo": {"name": "web-researcher-mcp-python", "version": "1.0"},
            },
        )
        await self._notify("notifications/initialized")

    async def _call_tool(self, name: str, arguments: dict[str, Any]) -> dict[str, Any]:
        result = await self._request("tools/call", {"name": name, "arguments": _strip_none(arguments)})
        if result is None:
            raise MCPError(f"Tool '{name}' returned a null result")
        is_error: bool = result.get("isError", False)
        content = result.get("content", [])
        text: str = ""
        for block in content:
            if block.get("type") == "text":
                text = block.get("text", "")
                break
        if not text:
            if is_error:
                raise MCPError(f"Tool '{name}' returned an error with no message")
            return result
        try:
            parsed = json.loads(text)
        except json.JSONDecodeError:
            return {"text": text, "isError": is_error}
        if is_error:
            raise MCPError(str(parsed.get("error", parsed)))
        return parsed

    # ------------------------------------------------------------------
    # Generic passthrough
    # ------------------------------------------------------------------

    async def call(self, tool_name: str, **params: Any) -> dict[str, Any]:
        """Generic tool call — strips None values from *params*."""
        return await self._call_tool(tool_name, params)

    async def list_tools(self) -> list[dict[str, Any]]:
        """Return the list of tool schemas available on this server."""
        result = await self._request("tools/list")
        return result.get("tools", [])

    # ------------------------------------------------------------------
    # Dynamic proxy: fills in any tool not yet in the static methods above.
    # Called once at start(); adds a callable attr for each unknown tool.
    # ------------------------------------------------------------------

    async def _install_dynamic_proxy(self) -> None:
        try:
            tool_list = await self.list_tools()
        except Exception:
            return
        for tool in tool_list:
            tname = tool.get("name", "")
            if not tname or hasattr(self, tname):
                continue

            def _make_proxy(n: str):
                async def _proxy(**kwargs: Any) -> dict[str, Any]:
                    return await self._call_tool(n, kwargs)
                _proxy.__name__ = n
                _proxy.__doc__ = tool.get("description", "")
                return _proxy

            setattr(self, tname, _make_proxy(tname))

    # ------------------------------------------------------------------
    # Tool methods (generated from outputSchema)
    # ------------------------------------------------------------------

    async def academic_search(
        self,
        query: str,
        num_results: int = 5,
        open_access: bool = False,
        pdf_only: bool = False,
        provider: str = None,
        sessionId: str = None,
        sort_by: str = None,
        source: str = None,
        year_from: int = None,
        year_to: int = None,
    ) -> AcademicSearchResponse:
        """Search peer-reviewed papers and scholarly literature using plain natural language — no special syntax needed"""
        d = await self._call_tool(
            "academic_search",
            {
                "query": query,
                "num_results": num_results,
                "open_access": open_access,
                "pdf_only": pdf_only,
                "provider": provider,
                "sessionId": sessionId,
                "sort_by": sort_by,
                "source": source,
                "year_from": year_from,
                "year_to": year_to,
            },
        )
        return AcademicSearchResponse.from_dict(d)
    async def answer(
        self,
        query: str,
        provider: str = None,
    ) -> AnswerResponse:
        """Ask a factual question and get one grounded, synthesized answer with source citations"""
        d = await self._call_tool(
            "answer",
            {
                "query": query,
                "provider": provider,
            },
        )
        return AnswerResponse.from_dict(d)
    async def archive_source(
        self,
        url: str,
    ) -> ArchiveSourceResponse:
        """Capture a fresh Internet Archive (Wayback Machine) snapshot of a URL via Save Page Now, so a source you intend to cite stays verifiable if the page later changes or disappears"""
        d = await self._call_tool(
            "archive_source",
            {
                "url": url,
            },
        )
        return ArchiveSourceResponse.from_dict(d)
    async def audit_bibliography(
        self,
        bibliography: str = None,
        entries: Optional[Optional[list]] = None,
        format: str = None,
        sessionId: str = None,
    ) -> AuditBibliographyResponse:
        """Audit a whole bibliography before you rely on it — paste a CSL-JSON, RIS, or BibTeX document (what format_bibliography exports), give an explicit list of references, or point at a sequential_search session, and this checks EVERY entry: does it exist, is it retracted, and does its link still resolve"""
        d = await self._call_tool(
            "audit_bibliography",
            {
                "bibliography": bibliography,
                "entries": entries,
                "format": format,
                "sessionId": sessionId,
            },
        )
        return AuditBibliographyResponse.from_dict(d)
    async def citation_graph(
        self,
        paper: str,
        direction: str = None,
        influential_only: bool = False,
        num_results: int = None,
        provider: str = None,
        sessionId: str = None,
    ) -> CitationGraphResponse:
        """Map a paper's citation neighborhood: find the works that cite it (forward) and the works it cites (backward), starting from a DOI or title"""
        d = await self._call_tool(
            "citation_graph",
            {
                "paper": paper,
                "direction": direction,
                "influential_only": influential_only,
                "num_results": num_results,
                "provider": provider,
                "sessionId": sessionId,
            },
        )
        return CitationGraphResponse.from_dict(d)
    async def clinical_search(
        self,
        condition: str = None,
        intervention: str = None,
        num_results: int = None,
        provider: str = None,
        query: str = None,
        sessionId: str = None,
        sponsor: str = None,
        status: str = None,
    ) -> ClinicalSearchResponse:
        """Search ClinicalTrials"""
        d = await self._call_tool(
            "clinical_search",
            {
                "condition": condition,
                "intervention": intervention,
                "num_results": num_results,
                "provider": provider,
                "query": query,
                "sessionId": sessionId,
                "sponsor": sponsor,
                "status": status,
            },
        )
        return ClinicalSearchResponse.from_dict(d)
    async def econ_search(
        self,
        country: str = None,
        date_from: str = None,
        date_to: str = None,
        frequency: str = None,
        num_results: int = None,
        provider: str = None,
        query: str = None,
        series_id: str = None,
        units: str = None,
    ) -> EconSearchResponse:
        """Look up macroeconomic and development data"""
        d = await self._call_tool(
            "econ_search",
            {
                "country": country,
                "date_from": date_from,
                "date_to": date_to,
                "frequency": frequency,
                "num_results": num_results,
                "provider": provider,
                "query": query,
                "series_id": series_id,
                "units": units,
            },
        )
        return EconSearchResponse.from_dict(d)
    async def filing_search(
        self,
        date_from: str = None,
        date_to: str = None,
        facts: bool = False,
        form_type: str = None,
        num_results: int = None,
        provider: str = None,
        query: str = None,
        sessionId: str = None,
        ticker: str = None,
    ) -> FilingSearchResponse:
        """Search SEC filings — the authoritative primary source for US public-company disclosures (10-K, 10-Q, 8-K, S-1, DEF 14A, and more)"""
        d = await self._call_tool(
            "filing_search",
            {
                "date_from": date_from,
                "date_to": date_to,
                "facts": facts,
                "form_type": form_type,
                "num_results": num_results,
                "provider": provider,
                "query": query,
                "sessionId": sessionId,
                "ticker": ticker,
            },
        )
        return FilingSearchResponse.from_dict(d)
    async def format_bibliography(
        self,
        sessionId: str = None,
        sources: Optional[Optional[list]] = None,
        style: str = None,
    ) -> FormatBibliographyResponse:
        """Turn a set of sources into a formatted bibliography"""
        d = await self._call_tool(
            "format_bibliography",
            {
                "sessionId": sessionId,
                "sources": sources,
                "style": style,
            },
        )
        return FormatBibliographyResponse.from_dict(d)
    async def get_my_analytics(
        self,) -> GetMyAnalyticsResponse:
        """Return YOUR OWN usage analytics (which tools you used such as web_search or sequential_search, counts, first/last seen) for this tenant"""
        d = await self._call_tool(
            "get_my_analytics",
            {

            },
        )
        return GetMyAnalyticsResponse.from_dict(d)
    async def get_research_session(
        self,
        sessionId: str,
        stepId: int = None,
    ) -> GetResearchSessionResponse:
        """Recover a sequential_search research session after context loss"""
        d = await self._call_tool(
            "get_research_session",
            {
                "sessionId": sessionId,
                "stepId": stepId,
            },
        )
        return GetResearchSessionResponse.from_dict(d)
    async def image_search(
        self,
        query: str,
        color_type: str = None,
        dominant_color: str = None,
        file_type: str = None,
        num_results: int = 5,
        provider: str = None,
        safe: str = None,
        size: str = None,
        type: str = None,
    ) -> ImageSearchResponse:
        """Find images on the web matching your description"""
        d = await self._call_tool(
            "image_search",
            {
                "query": query,
                "color_type": color_type,
                "dominant_color": dominant_color,
                "file_type": file_type,
                "num_results": num_results,
                "provider": provider,
                "safe": safe,
                "size": size,
                "type": type,
            },
        )
        return ImageSearchResponse.from_dict(d)
    async def legal_search(
        self,
        query: str,
        date_from: str = None,
        date_to: str = None,
        jurisdiction: str = None,
        num_results: int = None,
        provider: str = None,
        sessionId: str = None,
    ) -> LegalSearchResponse:
        """Search US court opinions (federal and state) for case-law research and precedent tracing"""
        d = await self._call_tool(
            "legal_search",
            {
                "query": query,
                "date_from": date_from,
                "date_to": date_to,
                "jurisdiction": jurisdiction,
                "num_results": num_results,
                "provider": provider,
                "sessionId": sessionId,
            },
        )
        return LegalSearchResponse.from_dict(d)
    async def memory_recall(
        self,
        limit: int = None,
        topic: str = None,
    ) -> MemoryRecallResponse:
        """Recall findings YOU previously saved with memory_save, across sessions (optionally filtered by topic)"""
        d = await self._call_tool(
            "memory_recall",
            {
                "limit": limit,
                "topic": topic,
            },
        )
        return MemoryRecallResponse.from_dict(d)
    async def memory_save(
        self,
        note: str,
        tags: Optional[Optional[list]] = None,
        topic: str = None,
        url: str = None,
    ) -> MemorySaveResponse:
        """Save a research finding to YOUR long-term memory so it can be recalled in future sessions (unlike sequential_search sessions, which expire after 4 hours)"""
        d = await self._call_tool(
            "memory_save",
            {
                "note": note,
                "tags": tags,
                "topic": topic,
                "url": url,
            },
        )
        return MemorySaveResponse.from_dict(d)
    async def news_search(
        self,
        query: str,
        news_source: str = None,
        num_results: int = 5,
        provider: str = None,
        sessionId: str = None,
        sort_by: str = None,
        time_range: str = None,
    ) -> NewsSearchResponse:
        """Find recent news articles on any topic, returning each article's headline, source, publish time, and snippet"""
        d = await self._call_tool(
            "news_search",
            {
                "query": query,
                "news_source": news_source,
                "num_results": num_results,
                "provider": provider,
                "sessionId": sessionId,
                "sort_by": sort_by,
                "time_range": time_range,
            },
        )
        return NewsSearchResponse.from_dict(d)
    async def patent_search(
        self,
        assignee: str = None,
        cpc_code: str = None,
        inventor: str = None,
        num_results: int = None,
        patent_office: str = None,
        provider: str = None,
        query: str = None,
        search_type: str = None,
        sessionId: str = None,
        year_from: int = None,
        year_to: int = None,
    ) -> PatentSearchResponse:
        """Search patents for prior art, competitive landscape mapping, or to look up a specific patent"""
        d = await self._call_tool(
            "patent_search",
            {
                "assignee": assignee,
                "cpc_code": cpc_code,
                "inventor": inventor,
                "num_results": num_results,
                "patent_office": patent_office,
                "provider": provider,
                "query": query,
                "search_type": search_type,
                "sessionId": sessionId,
                "year_from": year_from,
                "year_to": year_to,
            },
        )
        return PatentSearchResponse.from_dict(d)
    async def research_export(
        self,
        sessionId: str,
        format: str = None,
        verify_links: bool = False,
    ) -> ResearchExportResponse:
        """Export a completed sequential_search session as a shareable report"""
        d = await self._call_tool(
            "research_export",
            {
                "sessionId": sessionId,
                "format": format,
                "verify_links": verify_links,
            },
        )
        return ResearchExportResponse.from_dict(d)
    async def scrape_page(
        self,
        url: str,
        max_length: int = None,
        mode: str = None,
        sessionId: str = None,
    ) -> ScrapePageResponse:
        """Read a single URL and get back its content — web pages (including JavaScript-heavy sites), PDFs, Word/PowerPoint files, YouTube transcripts, and Hacker News item/user/list pages (read natively via the HN API) — picking the best extraction method automatically"""
        d = await self._call_tool(
            "scrape_page",
            {
                "url": url,
                "max_length": max_length,
                "mode": mode,
                "sessionId": sessionId,
            },
        )
        return ScrapePageResponse.from_dict(d)
    async def search_and_scrape(
        self,
        query: str,
        claim: str = None,
        deduplicate: Optional[bool] = None,
        filter_by_query: bool = False,
        include_sources: Optional[bool] = None,
        max_length_per_source: int = None,
        num_results: int = None,
        provider: str = None,
        sessionId: str = None,
        total_max_length: int = None,
    ) -> SearchAndScrapeResponse:
        """Search the web and read the full content from the top results, all in one step"""
        d = await self._call_tool(
            "search_and_scrape",
            {
                "query": query,
                "claim": claim,
                "deduplicate": deduplicate,
                "filter_by_query": filter_by_query,
                "include_sources": include_sources,
                "max_length_per_source": max_length_per_source,
                "num_results": num_results,
                "provider": provider,
                "sessionId": sessionId,
                "total_max_length": total_max_length,
            },
        )
        return SearchAndScrapeResponse.from_dict(d)
    async def sequential_search(
        self,
        search_step: str,
        step_number: int,
        next_step_needed: bool,
        branchFromStep: int = None,
        branchId: str = None,
        confidence: str = None,
        depth: str = None,
        isRevision: bool = False,
        knowledgeGap: str = None,
        reasoning: str = None,
        rejectedApproaches: Optional[Optional[list]] = None,
        researchGoal: str = None,
        responseMode: str = None,
        revisesStep: int = None,
        sessionId: str = None,
        sessionSummary: str = None,
        totalStepsEstimate: int = None,
    ) -> SequentialSearchResponse:
        """Keep track of a multi-step research project"""
        d = await self._call_tool(
            "sequential_search",
            {
                "searchStep": search_step,
                "stepNumber": step_number,
                "nextStepNeeded": next_step_needed,
                "branchFromStep": branchFromStep,
                "branchId": branchId,
                "confidence": confidence,
                "depth": depth,
                "isRevision": isRevision,
                "knowledgeGap": knowledgeGap,
                "reasoning": reasoning,
                "rejectedApproaches": rejectedApproaches,
                "researchGoal": researchGoal,
                "responseMode": responseMode,
                "revisesStep": revisesStep,
                "sessionId": sessionId,
                "sessionSummary": sessionSummary,
                "totalStepsEstimate": totalStepsEstimate,
            },
        )
        return SequentialSearchResponse.from_dict(d)
    async def structured_search(
        self,
        query: str,
        category: str = None,
        num_results: int = None,
        provider: str = None,
        schema: Optional[dict] = None,
    ) -> StructuredSearchResponse:
        """Search the web and extract structured data from each result"""
        d = await self._call_tool(
            "structured_search",
            {
                "query": query,
                "category": category,
                "num_results": num_results,
                "provider": provider,
                "schema": schema,
            },
        )
        return StructuredSearchResponse.from_dict(d)
    async def verify_citation(
        self,
        citation: str,
        claim: str = None,
    ) -> VerifyCitationResponse:
        """Verify a citation before you rely on it — confirm it actually exists, matches a real record, hasn't been retracted, and still resolves"""
        d = await self._call_tool(
            "verify_citation",
            {
                "citation": citation,
                "claim": claim,
            },
        )
        return VerifyCitationResponse.from_dict(d)
    async def verify_recommendation(
        self,
        recommendations: Optional[Optional[list]],
    ) -> VerifyRecommendationResponse:
        """Audit an AI recommendation list against anti-sloptimization signals"""
        d = await self._call_tool(
            "verify_recommendation",
            {
                "recommendations": recommendations,
            },
        )
        return VerifyRecommendationResponse.from_dict(d)
    async def web_search(
        self,
        query: str,
        claim: str = None,
        country: str = None,
        exact_terms: str = None,
        exclude_terms: str = None,
        language: str = None,
        lens: str = None,
        num_results: int = 5,
        provider: str = None,
        safe: str = None,
        sessionId: str = None,
        site: str = None,
        time_range: str = None,
    ) -> WebSearchResponse:
        """Search the web and get a list of relevant pages with titles and snippets — without reading the full page content"""
        d = await self._call_tool(
            "web_search",
            {
                "query": query,
                "claim": claim,
                "country": country,
                "exact_terms": exact_terms,
                "exclude_terms": exclude_terms,
                "language": language,
                "lens": lens,
                "num_results": num_results,
                "provider": provider,
                "safe": safe,
                "sessionId": sessionId,
                "site": site,
                "time_range": time_range,
            },
        )
        return WebSearchResponse.from_dict(d)
    async def workspace_contribute(
        self,
        workspace_id: str,
        note: str,
        tags: Optional[Optional[list]] = None,
        url: str = None,
    ) -> WorkspaceContributeResponse:
        """Share a research finding into a shared team workspace (a COPY is stored with your attribution — never a live link to your private data)"""
        d = await self._call_tool(
            "workspace_contribute",
            {
                "workspace_id": workspace_id,
                "note": note,
                "tags": tags,
                "url": url,
            },
        )
        return WorkspaceContributeResponse.from_dict(d)
    async def workspace_read(
        self,
        workspace_id: str,
    ) -> WorkspaceReadResponse:
        """Read the shared findings in a team workspace you belong to (contributed by members via workspace_contribute, each with attribution)"""
        d = await self._call_tool(
            "workspace_read",
            {
                "workspace_id": workspace_id,
            },
        )
        return WorkspaceReadResponse.from_dict(d)
    # ------------------------------------------------------------------
    # Optional: LangChain integration
    # ------------------------------------------------------------------

    def as_langchain_tools(self) -> list[Any]:
        """Return LangChain StructuredTool wrappers for every tool on the server.

        Requires ``langchain-core`` (``pip install langchain-core``).
        """
        try:
            from langchain_core.tools import StructuredTool  # type: ignore[import]
            _structured = True
        except ImportError:
            try:
                from langchain_core.tools import Tool  # type: ignore[import]
                _structured = False
            except ImportError as exc:
                raise ImportError(
                    "langchain-core is required: pip install langchain-core"
                ) from exc

        loop = asyncio.new_event_loop()
        try:
            tools_schemas: list[dict[str, Any]] = loop.run_until_complete(self.list_tools())
        finally:
            loop.close()

        lc_tools: list[Any] = []
        for schema in tools_schemas:
            tool_name: str = schema.get("name", "")
            description: str = schema.get("description", "")

            def _make_func(name: str) -> Any:
                async_client = self

                def _sync_func(**kwargs: Any) -> str:
                    coro = async_client.call(name, **kwargs)
                    _loop = asyncio.new_event_loop()
                    try:
                        result = _loop.run_until_complete(coro)
                    finally:
                        _loop.close()
                    return json.dumps(result)

                return _sync_func

            fn = _make_func(tool_name)

            if _structured:
                lc_tools.append(
                    StructuredTool.from_function(  # type: ignore[union-attr]
                        func=fn,
                        name=tool_name,
                        description=description,
                    )
                )
            else:
                def _make_str_func(name: str) -> Any:
                    async_client = self

                    def _str_func(query: str) -> str:
                        coro = async_client.call(name, query=query)
                        _loop = asyncio.new_event_loop()
                        try:
                            result = _loop.run_until_complete(coro)
                        finally:
                            _loop.close()
                        return json.dumps(result)

                    return _str_func

                lc_tools.append(
                    Tool(  # type: ignore[union-attr]
                        name=tool_name,
                        description=description,
                        func=_make_str_func(tool_name),
                    )
                )
        return lc_tools

    # ------------------------------------------------------------------
    # Factory
    # ------------------------------------------------------------------

    @classmethod
    def sync(cls, **kwargs: Any) -> "SyncWebResearcherClient":
        """Create a :class:`SyncWebResearcherClient` with the same keyword args."""
        return SyncWebResearcherClient(**kwargs)


# ---------------------------------------------------------------------------
# Sync wrapper
# ---------------------------------------------------------------------------

class SyncWebResearcherClient:
    """Synchronous wrapper around :class:`WebResearcherClient`.

    Runs the async event loop in a background daemon thread so the caller
    never needs to interact with asyncio directly.

    Usage::

        with SyncWebResearcherClient() as c:
            result = c.web_search("climate change")
    """

    def __init__(
        self,
        port: Optional[int] = None,
        timeout: float = 120.0,
        *,
        server_env: Optional[dict[str, str]] = None,
        startup_timeout: float = 30.0,
    ) -> None:
        self._async_client = WebResearcherClient(
            port=port,
            timeout=timeout,
            server_env=server_env,
            startup_timeout=startup_timeout,
        )
        self._loop: Optional[asyncio.AbstractEventLoop] = None
        self._thread: Optional[threading.Thread] = None

    def _start_loop(self) -> None:
        assert self._loop is not None
        self._loop.run_forever()

    def start(self) -> None:
        """Start the background event loop and perform the MCP handshake."""
        self._loop = asyncio.new_event_loop()
        self._thread = threading.Thread(
            target=self._start_loop,
            daemon=True,
            name="web-researcher-mcp-loop",
        )
        self._thread.start()
        self._run(self._async_client.start())

    def stop(self) -> None:
        """Stop the background event loop and the managed subprocess, if any."""
        if self._loop is None:
            return
        self._run(self._async_client.stop())
        if self._loop is not None and self._loop.is_running():
            self._loop.call_soon_threadsafe(self._loop.stop)
        if self._thread is not None:
            self._thread.join(timeout=5.0)
        self._loop = None
        self._thread = None

    def __enter__(self) -> "SyncWebResearcherClient":
        self.start()
        return self

    def __exit__(self, *_: Any) -> None:
        self.stop()

    def _run(self, coro: Any) -> Any:
        if self._loop is None or not self._loop.is_running():
            raise RuntimeError(
                "SyncWebResearcherClient is not started. "
                "Use 'with SyncWebResearcherClient() as c:' or call start() first."
            )
        future = asyncio.run_coroutine_threadsafe(coro, self._loop)
        return future.result()

    def call(self, tool_name: str, **params: Any) -> dict[str, Any]:
        return self._run(self._async_client.call(tool_name, **params))

    def list_tools(self) -> list[dict[str, Any]]:
        return self._run(self._async_client.list_tools())

    def as_langchain_tools(self) -> list[Any]:
        return self._async_client.as_langchain_tools()

    # ------------------------------------------------------------------
    # Sync tool methods
    # ------------------------------------------------------------------

    def academic_search(
        self,
        query: str,
        num_results: int = 5,
        open_access: bool = False,
        pdf_only: bool = False,
        provider: str = None,
        sessionId: str = None,
        sort_by: str = None,
        source: str = None,
        year_from: int = None,
        year_to: int = None,
    ) -> AcademicSearchResponse:
        return self._run(
            self._async_client.academic_search(
            query=query,
            num_results=num_results,
            open_access=open_access,
            pdf_only=pdf_only,
            provider=provider,
            sessionId=sessionId,
            sort_by=sort_by,
            source=source,
            year_from=year_from,
            year_to=year_to,
            )
        )
    def answer(
        self,
        query: str,
        provider: str = None,
    ) -> AnswerResponse:
        return self._run(
            self._async_client.answer(
            query=query,
            provider=provider,
            )
        )
    def archive_source(
        self,
        url: str,
    ) -> ArchiveSourceResponse:
        return self._run(
            self._async_client.archive_source(
            url=url,
            )
        )
    def audit_bibliography(
        self,
        bibliography: str = None,
        entries: Optional[Optional[list]] = None,
        format: str = None,
        sessionId: str = None,
    ) -> AuditBibliographyResponse:
        return self._run(
            self._async_client.audit_bibliography(
            bibliography=bibliography,
            entries=entries,
            format=format,
            sessionId=sessionId,
            )
        )
    def citation_graph(
        self,
        paper: str,
        direction: str = None,
        influential_only: bool = False,
        num_results: int = None,
        provider: str = None,
        sessionId: str = None,
    ) -> CitationGraphResponse:
        return self._run(
            self._async_client.citation_graph(
            paper=paper,
            direction=direction,
            influential_only=influential_only,
            num_results=num_results,
            provider=provider,
            sessionId=sessionId,
            )
        )
    def clinical_search(
        self,
        condition: str = None,
        intervention: str = None,
        num_results: int = None,
        provider: str = None,
        query: str = None,
        sessionId: str = None,
        sponsor: str = None,
        status: str = None,
    ) -> ClinicalSearchResponse:
        return self._run(
            self._async_client.clinical_search(
            condition=condition,
            intervention=intervention,
            num_results=num_results,
            provider=provider,
            query=query,
            sessionId=sessionId,
            sponsor=sponsor,
            status=status,
            )
        )
    def econ_search(
        self,
        country: str = None,
        date_from: str = None,
        date_to: str = None,
        frequency: str = None,
        num_results: int = None,
        provider: str = None,
        query: str = None,
        series_id: str = None,
        units: str = None,
    ) -> EconSearchResponse:
        return self._run(
            self._async_client.econ_search(
            country=country,
            date_from=date_from,
            date_to=date_to,
            frequency=frequency,
            num_results=num_results,
            provider=provider,
            query=query,
            series_id=series_id,
            units=units,
            )
        )
    def filing_search(
        self,
        date_from: str = None,
        date_to: str = None,
        facts: bool = False,
        form_type: str = None,
        num_results: int = None,
        provider: str = None,
        query: str = None,
        sessionId: str = None,
        ticker: str = None,
    ) -> FilingSearchResponse:
        return self._run(
            self._async_client.filing_search(
            date_from=date_from,
            date_to=date_to,
            facts=facts,
            form_type=form_type,
            num_results=num_results,
            provider=provider,
            query=query,
            sessionId=sessionId,
            ticker=ticker,
            )
        )
    def format_bibliography(
        self,
        sessionId: str = None,
        sources: Optional[Optional[list]] = None,
        style: str = None,
    ) -> FormatBibliographyResponse:
        return self._run(
            self._async_client.format_bibliography(
            sessionId=sessionId,
            sources=sources,
            style=style,
            )
        )
    def get_my_analytics(
        self,) -> GetMyAnalyticsResponse:
        return self._run(
            self._async_client.get_my_analytics(

            )
        )
    def get_research_session(
        self,
        sessionId: str,
        stepId: int = None,
    ) -> GetResearchSessionResponse:
        return self._run(
            self._async_client.get_research_session(
            sessionId=sessionId,
            stepId=stepId,
            )
        )
    def image_search(
        self,
        query: str,
        color_type: str = None,
        dominant_color: str = None,
        file_type: str = None,
        num_results: int = 5,
        provider: str = None,
        safe: str = None,
        size: str = None,
        type: str = None,
    ) -> ImageSearchResponse:
        return self._run(
            self._async_client.image_search(
            query=query,
            color_type=color_type,
            dominant_color=dominant_color,
            file_type=file_type,
            num_results=num_results,
            provider=provider,
            safe=safe,
            size=size,
            type=type,
            )
        )
    def legal_search(
        self,
        query: str,
        date_from: str = None,
        date_to: str = None,
        jurisdiction: str = None,
        num_results: int = None,
        provider: str = None,
        sessionId: str = None,
    ) -> LegalSearchResponse:
        return self._run(
            self._async_client.legal_search(
            query=query,
            date_from=date_from,
            date_to=date_to,
            jurisdiction=jurisdiction,
            num_results=num_results,
            provider=provider,
            sessionId=sessionId,
            )
        )
    def memory_recall(
        self,
        limit: int = None,
        topic: str = None,
    ) -> MemoryRecallResponse:
        return self._run(
            self._async_client.memory_recall(
            limit=limit,
            topic=topic,
            )
        )
    def memory_save(
        self,
        note: str,
        tags: Optional[Optional[list]] = None,
        topic: str = None,
        url: str = None,
    ) -> MemorySaveResponse:
        return self._run(
            self._async_client.memory_save(
            note=note,
            tags=tags,
            topic=topic,
            url=url,
            )
        )
    def news_search(
        self,
        query: str,
        news_source: str = None,
        num_results: int = 5,
        provider: str = None,
        sessionId: str = None,
        sort_by: str = None,
        time_range: str = None,
    ) -> NewsSearchResponse:
        return self._run(
            self._async_client.news_search(
            query=query,
            news_source=news_source,
            num_results=num_results,
            provider=provider,
            sessionId=sessionId,
            sort_by=sort_by,
            time_range=time_range,
            )
        )
    def patent_search(
        self,
        assignee: str = None,
        cpc_code: str = None,
        inventor: str = None,
        num_results: int = None,
        patent_office: str = None,
        provider: str = None,
        query: str = None,
        search_type: str = None,
        sessionId: str = None,
        year_from: int = None,
        year_to: int = None,
    ) -> PatentSearchResponse:
        return self._run(
            self._async_client.patent_search(
            assignee=assignee,
            cpc_code=cpc_code,
            inventor=inventor,
            num_results=num_results,
            patent_office=patent_office,
            provider=provider,
            query=query,
            search_type=search_type,
            sessionId=sessionId,
            year_from=year_from,
            year_to=year_to,
            )
        )
    def research_export(
        self,
        sessionId: str,
        format: str = None,
        verify_links: bool = False,
    ) -> ResearchExportResponse:
        return self._run(
            self._async_client.research_export(
            sessionId=sessionId,
            format=format,
            verify_links=verify_links,
            )
        )
    def scrape_page(
        self,
        url: str,
        max_length: int = None,
        mode: str = None,
        sessionId: str = None,
    ) -> ScrapePageResponse:
        return self._run(
            self._async_client.scrape_page(
            url=url,
            max_length=max_length,
            mode=mode,
            sessionId=sessionId,
            )
        )
    def search_and_scrape(
        self,
        query: str,
        claim: str = None,
        deduplicate: Optional[bool] = None,
        filter_by_query: bool = False,
        include_sources: Optional[bool] = None,
        max_length_per_source: int = None,
        num_results: int = None,
        provider: str = None,
        sessionId: str = None,
        total_max_length: int = None,
    ) -> SearchAndScrapeResponse:
        return self._run(
            self._async_client.search_and_scrape(
            query=query,
            claim=claim,
            deduplicate=deduplicate,
            filter_by_query=filter_by_query,
            include_sources=include_sources,
            max_length_per_source=max_length_per_source,
            num_results=num_results,
            provider=provider,
            sessionId=sessionId,
            total_max_length=total_max_length,
            )
        )
    def sequential_search(
        self,
        search_step: str,
        step_number: int,
        next_step_needed: bool,
        branchFromStep: int = None,
        branchId: str = None,
        confidence: str = None,
        depth: str = None,
        isRevision: bool = False,
        knowledgeGap: str = None,
        reasoning: str = None,
        rejectedApproaches: Optional[Optional[list]] = None,
        researchGoal: str = None,
        responseMode: str = None,
        revisesStep: int = None,
        sessionId: str = None,
        sessionSummary: str = None,
        totalStepsEstimate: int = None,
    ) -> SequentialSearchResponse:
        return self._run(
            self._async_client.sequential_search(
            search_step=search_step,
            step_number=step_number,
            next_step_needed=next_step_needed,
            branchFromStep=branchFromStep,
            branchId=branchId,
            confidence=confidence,
            depth=depth,
            isRevision=isRevision,
            knowledgeGap=knowledgeGap,
            reasoning=reasoning,
            rejectedApproaches=rejectedApproaches,
            researchGoal=researchGoal,
            responseMode=responseMode,
            revisesStep=revisesStep,
            sessionId=sessionId,
            sessionSummary=sessionSummary,
            totalStepsEstimate=totalStepsEstimate,
            )
        )
    def structured_search(
        self,
        query: str,
        category: str = None,
        num_results: int = None,
        provider: str = None,
        schema: Optional[dict] = None,
    ) -> StructuredSearchResponse:
        return self._run(
            self._async_client.structured_search(
            query=query,
            category=category,
            num_results=num_results,
            provider=provider,
            schema=schema,
            )
        )
    def verify_citation(
        self,
        citation: str,
        claim: str = None,
    ) -> VerifyCitationResponse:
        return self._run(
            self._async_client.verify_citation(
            citation=citation,
            claim=claim,
            )
        )
    def verify_recommendation(
        self,
        recommendations: Optional[Optional[list]],
    ) -> VerifyRecommendationResponse:
        return self._run(
            self._async_client.verify_recommendation(
            recommendations=recommendations,
            )
        )
    def web_search(
        self,
        query: str,
        claim: str = None,
        country: str = None,
        exact_terms: str = None,
        exclude_terms: str = None,
        language: str = None,
        lens: str = None,
        num_results: int = 5,
        provider: str = None,
        safe: str = None,
        sessionId: str = None,
        site: str = None,
        time_range: str = None,
    ) -> WebSearchResponse:
        return self._run(
            self._async_client.web_search(
            query=query,
            claim=claim,
            country=country,
            exact_terms=exact_terms,
            exclude_terms=exclude_terms,
            language=language,
            lens=lens,
            num_results=num_results,
            provider=provider,
            safe=safe,
            sessionId=sessionId,
            site=site,
            time_range=time_range,
            )
        )
    def workspace_contribute(
        self,
        workspace_id: str,
        note: str,
        tags: Optional[Optional[list]] = None,
        url: str = None,
    ) -> WorkspaceContributeResponse:
        return self._run(
            self._async_client.workspace_contribute(
            workspace_id=workspace_id,
            note=note,
            tags=tags,
            url=url,
            )
        )
    def workspace_read(
        self,
        workspace_id: str,
    ) -> WorkspaceReadResponse:
        return self._run(
            self._async_client.workspace_read(
            workspace_id=workspace_id,
            )
        )
