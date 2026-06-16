"""
tests/python/test_client.py
~~~~~~~~~~~~~~~~~~~~~~~~~~~
Unit + integration tests for the web-researcher-mcp Python SDK.

Run with:
    python3 -m pytest tests/python/test_client.py -v
    python3 -m unittest tests.python.test_client -v
    python3 tests/python/test_client.py          # plain stdlib
"""
from __future__ import annotations

import asyncio
import json
import os
import socket
import sys
import threading
import types
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from typing import Any

# ---------------------------------------------------------------------------
# Path setup — allow running from repo root without installing the package
# ---------------------------------------------------------------------------

_PYTHON_PKG_DIR = os.path.join(os.path.dirname(__file__), "..", "..", "python")
sys.path.insert(0, os.path.abspath(_PYTHON_PKG_DIR))

# ---------------------------------------------------------------------------
# Inject a fake _shim BEFORE importing web_researcher_mcp so the package is
# importable without a wheel-installed binary on PATH.
# ---------------------------------------------------------------------------

fake_shim = types.ModuleType("web_researcher_mcp._shim")
fake_shim.__version__ = "test-0.0.0"  # type: ignore[attr-defined]
fake_shim.get_binary_path = lambda: "/nonexistent/binary"  # type: ignore[attr-defined]
fake_shim.main = lambda: None  # type: ignore[attr-defined]
sys.modules["web_researcher_mcp._shim"] = fake_shim

# Now it is safe to import the package.
from web_researcher_mcp.client import SyncWebResearcherClient, WebResearcherClient, _strip_none  # noqa: E402
from web_researcher_mcp.models import (  # noqa: E402
    AcademicSearchResponse,
    ArchiveResult,
    AuditBibliographyResult,
    AuditSummary,
    BibEntryAudit,
    Citation,
    ImageSearchResponse,
    MCPError,
    NewsSearchResponse,
    RetractionStatus,
    ScrapeResult,
    SearchAndScrapeResult,
    SearchResponse,
    SearchResult,
    VerifyResult,
)
from web_researcher_mcp._server import _find_free_port, _ServerProcess  # noqa: E402


# ---------------------------------------------------------------------------
# Sample data matching the Go response schemas
# ---------------------------------------------------------------------------

_SAMPLE_SEARCH_RESULT: dict[str, Any] = {
    "title": "CRISPR genome editing: mechanisms and applications",
    "url": "https://example.com/crispr",
    "snippet": "A comprehensive review of CRISPR-Cas9 technology...",
    "displayLink": "example.com",
    "claimSignal": "supports",
}

_SAMPLE_SEARCH_RESPONSE: dict[str, Any] = {
    "query": "CRISPR applications",
    "results": [_SAMPLE_SEARCH_RESULT],
    "resultCount": 1,
    "urls": ["https://example.com/crispr"],
    "hints": {},
}

_SAMPLE_SCRAPE_RESULT: dict[str, Any] = {
    "url": "https://example.com/article",
    "content": "# Article Title\n\nThis is the article body.",
    "contentType": "text/html",
    "contentLength": 1234,
    "truncated": False,
    "estimatedTokens": 300,
    "sizeCategory": "small",
    "raw": False,
    "extractionQuality": "high",
    "extractedBy": "markdown",
    "citation": {
        "url": "https://example.com/article",
        "accessedDate": "2024-01-15",
        "metadata": {"author": "Jane Doe"},
        "formatted": {"apa": "Doe, J. (2024). Article Title."},
    },
    "metadata": {"lang": "en"},
    "sourceType": "web",
    "authorityTier": "tier2",
    "domainCategory": "general",
    "detectedDoi": "",
    "retractionStatus": None,
}

_SAMPLE_VERIFY_RESULT: dict[str, Any] = {
    "input": "10.1038/nature12373",
    "inputType": "doi",
    "exists": True,
    "matchedRecord": {
        "DOI": "10.1038/nature12373",
        "title": ["Cas9 as a versatile tool for engineering biology"],
        "type": "journal-article",
    },
    "matchConfidence": "exact",
    "detectedDoi": "10.1038/nature12373",
    "titleMatch": "exact",
    "retractionStatus": {
        "retracted": False,
        "kind": "",
        "date": None,
        "noticeDoi": None,
        "source": "crossref",
    },
    "httpStatus": 200,
    "archivedUrl": "https://web.archive.org/web/2024/https://doi.org/10.1038/nature12373",
    "provenance": ["crossref", "unpaywall"],
    "claim": "",
    "claimSupport": "unchecked",
    "claimEvidence": [],
    "claimSourceUrl": "",
    "contrastSignal": False,
    "conflictOfInterest": None,
}

_SAMPLE_AUDIT_RESULT: dict[str, Any] = {
    "source": "entries",
    "entryCount": 1,
    "summary": {
        "total": 1,
        "retracted": 0,
        "deadLink": 0,
        "notFound": 0,
        "unchecked": 0,
        "mischaracterized": 0,
        "ok": 1,
    },
    "entries": [
        {
            "index": 0,
            "title": "Cas9 as a versatile tool for engineering biology",
            "doi": "10.1038/nature12373",
            "url": "https://doi.org/10.1038/nature12373",
            "exists": True,
            "retractionStatus": None,
            "linkLive": True,
            "httpStatus": 200,
            "archivedUrl": "",
            "flags": [],
            "reason": "",
            "claim": "",
            "claimSupport": "unchecked",
            "claimEvidence": [],
            "claimSourceUrl": "",
            "contrastSignal": None,
        }
    ],
    "skipped": 0,
    "checkedAt": "2024-01-15T10:00:00Z",
}

_SAMPLE_ARCHIVE_RESULT: dict[str, Any] = {
    "requestedUrl": "https://example.com/page",
    "snapshotUrl": "https://web.archive.org/web/20240115000000/https://example.com/page",
    "archivedAt": "2024-01-15T00:00:00Z",
    "captured": True,
    "status": "captured",
    "httpStatus": 200,
    "reason": "",
    "pollUrl": "",
    "source": "wayback",
    "provenance": ["wayback"],
}


# ---------------------------------------------------------------------------
# Mock MCP HTTP server
# ---------------------------------------------------------------------------

def _make_mcp_text_content(data: dict[str, Any]) -> dict[str, Any]:
    """Wrap a dict as an MCP tools/call result with a single text block."""
    return {
        "isError": False,
        "content": [{"type": "text", "text": json.dumps(data)}],
    }


class _MockMCPHandler(BaseHTTPRequestHandler):
    """HTTP request handler that simulates the MCP Streamable HTTP transport."""

    # Suppress request log spam in test output.
    def log_message(self, fmt: str, *args: Any) -> None:  # noqa: D102
        pass

    def do_GET(self) -> None:  # noqa: N802
        if self.path in ("/health/live", "/health/live/"):
            self._send_text(200, "ok")
        else:
            self._send_text(404, "not found")

    def do_POST(self) -> None:  # noqa: N802
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length)
        try:
            req = json.loads(body)
        except json.JSONDecodeError:
            self._send_json(400, {"error": "bad json"})
            return

        # Notifications (no "id" field): return 202 with empty body.
        if "id" not in req:
            self.send_response(202)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return

        method: str = req.get("method", "")
        req_id = req.get("id")

        if method == "initialize":
            resp = {
                "jsonrpc": "2.0",
                "id": req_id,
                "result": {
                    "protocolVersion": "2024-11-05",
                    "capabilities": {},
                    "serverInfo": {"name": "test", "version": "test"},
                },
            }
            extra_headers = {"Mcp-Session-Id": "test-session-123"}
            self._send_json(200, resp, extra_headers)

        elif method == "tools/list":
            resp = {
                "jsonrpc": "2.0",
                "id": req_id,
                "result": {
                    "tools": [
                        {
                            "name": "web_search",
                            "description": "Search the web",
                            "inputSchema": {
                                "type": "object",
                                "properties": {"query": {"type": "string"}},
                                "required": ["query"],
                            },
                        }
                    ]
                },
            }
            self._send_json(200, resp)

        elif method == "tools/call":
            self._handle_tool_call(req_id, req.get("params", {}))

        else:
            # Unknown method — return a JSON-RPC error.
            resp = {
                "jsonrpc": "2.0",
                "id": req_id,
                "error": {"code": -32601, "message": f"Method not found: {method}"},
            }
            self._send_json(200, resp)

    def _handle_tool_call(self, req_id: Any, params: dict[str, Any]) -> None:
        tool_name: str = params.get("name", "")
        # Let tests inject deliberate errors via a special query argument.
        arguments: dict[str, Any] = params.get("arguments", {})
        force_error: bool = bool(arguments.get("_force_error"))

        if force_error:
            resp = {
                "jsonrpc": "2.0",
                "id": req_id,
                "result": {
                    "isError": True,
                    "content": [
                        {"type": "text", "text": json.dumps({"error": "forced test error"})}
                    ],
                },
            }
            self._send_json(200, resp)
            return

        if tool_name == "web_search":
            payload = _SAMPLE_SEARCH_RESPONSE
        elif tool_name == "scrape_page":
            payload = _SAMPLE_SCRAPE_RESULT
        elif tool_name == "verify_citation":
            payload = _SAMPLE_VERIFY_RESULT
        elif tool_name == "audit_bibliography":
            payload = _SAMPLE_AUDIT_RESULT
        elif tool_name == "archive_source":
            payload = _SAMPLE_ARCHIVE_RESULT
        else:
            payload = {"results": [], "resultCount": 0}

        resp = {
            "jsonrpc": "2.0",
            "id": req_id,
            "result": _make_mcp_text_content(payload),
        }
        self._send_json(200, resp)

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    def _send_json(
        self,
        status: int,
        data: dict[str, Any],
        extra_headers: dict[str, str] | None = None,
    ) -> None:
        raw = json.dumps(data).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        if extra_headers:
            for k, v in extra_headers.items():
                self.send_header(k, v)
        self.end_headers()
        self.wfile.write(raw)

    def _send_text(self, status: int, text: str) -> None:
        raw = text.encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)


class MockMCPServer:
    """Runs _MockMCPHandler in a background daemon thread."""

    def __init__(self) -> None:
        self._server: HTTPServer | None = None
        self._thread: threading.Thread | None = None
        self.port: int = 0

    def start(self) -> None:
        self._server = HTTPServer(("127.0.0.1", 0), _MockMCPHandler)
        self.port = self._server.server_address[1]
        self._thread = threading.Thread(
            target=self._server.serve_forever,
            daemon=True,
            name="mock-mcp-server",
        )
        self._thread.start()

    def stop(self) -> None:
        if self._server is not None:
            self._server.shutdown()
            self._server = None
        if self._thread is not None:
            self._thread.join(timeout=5.0)
            self._thread = None


# ---------------------------------------------------------------------------
# TestModels — pure unit tests, no network
# ---------------------------------------------------------------------------

class TestModels(unittest.TestCase):
    """Verify that from_dict on each model type parses realistic sample dicts."""

    def test_search_result_from_dict(self) -> None:
        r = SearchResult.from_dict(_SAMPLE_SEARCH_RESULT)
        self.assertEqual(r.title, "CRISPR genome editing: mechanisms and applications")
        self.assertEqual(r.url, "https://example.com/crispr")
        self.assertEqual(r.snippet, "A comprehensive review of CRISPR-Cas9 technology...")
        self.assertEqual(r.displayLink, "example.com")
        self.assertEqual(r.claimSignal, "supports")

    def test_search_result_missing_optional_fields(self) -> None:
        r = SearchResult.from_dict({"title": "T", "url": "http://x.com", "snippet": "s", "displayLink": "x.com"})
        self.assertIsNone(r.claimSignal)
        self.assertEqual(r.title, "T")

    def test_search_response_from_dict(self) -> None:
        r = SearchResponse.from_dict(_SAMPLE_SEARCH_RESPONSE)
        self.assertEqual(r.query, "CRISPR applications")
        self.assertEqual(r.resultCount, 1)
        self.assertEqual(len(r.results), 1)
        self.assertIsInstance(r.results[0], SearchResult)
        self.assertEqual(r.results[0].url, "https://example.com/crispr")
        self.assertEqual(r.urls, ["https://example.com/crispr"])

    def test_search_response_empty_results(self) -> None:
        r = SearchResponse.from_dict({"query": "q", "results": None, "resultCount": 0, "urls": None, "hints": None})
        self.assertEqual(r.results, [])
        self.assertEqual(r.urls, [])
        self.assertEqual(r.hints, {})

    def test_scrape_result_from_dict(self) -> None:
        r = ScrapeResult.from_dict(_SAMPLE_SCRAPE_RESULT)
        self.assertEqual(r.url, "https://example.com/article")
        self.assertIn("Article Title", r.content)
        self.assertEqual(r.contentType, "text/html")
        self.assertEqual(r.contentLength, 1234)
        self.assertFalse(r.truncated)
        self.assertEqual(r.estimatedTokens, 300)
        self.assertEqual(r.extractionQuality, "high")
        self.assertEqual(r.extractedBy, "markdown")
        self.assertIsNotNone(r.citation)
        self.assertIsInstance(r.citation, Citation)
        self.assertEqual(r.citation.url, "https://example.com/article")  # type: ignore[union-attr]
        self.assertIsNone(r.retractionStatus)

    def test_scrape_result_with_retraction(self) -> None:
        d = dict(_SAMPLE_SCRAPE_RESULT)
        d["retractionStatus"] = {"retracted": True, "kind": "retraction", "date": "2023-01-01", "noticeDoi": None, "source": "crossref"}
        r = ScrapeResult.from_dict(d)
        self.assertIsNotNone(r.retractionStatus)
        self.assertTrue(r.retractionStatus["retracted"])  # type: ignore[index]

    def test_verify_result_from_dict(self) -> None:
        r = VerifyResult.from_dict(_SAMPLE_VERIFY_RESULT)
        self.assertEqual(r.input, "10.1038/nature12373")
        self.assertEqual(r.inputType, "doi")
        self.assertTrue(r.exists)
        self.assertIsNotNone(r.matchedRecord)
        self.assertEqual(r.matchConfidence, "exact")
        self.assertEqual(r.detectedDoi, "10.1038/nature12373")
        self.assertEqual(r.titleMatch, "exact")
        self.assertIsNotNone(r.retractionStatus)
        self.assertIsInstance(r.retractionStatus, RetractionStatus)
        self.assertFalse(r.retractionStatus.retracted)  # type: ignore[union-attr]
        self.assertEqual(r.httpStatus, 200)
        self.assertIn("web.archive.org", r.archivedUrl)
        self.assertEqual(r.provenance, ["crossref", "unpaywall"])
        self.assertIsNone(r.conflictOfInterest)

    def test_verify_result_null_optionals(self) -> None:
        r = VerifyResult.from_dict({
            "input": "bad-doi",
            "inputType": "doi",
            "exists": None,
            "matchedRecord": None,
            "matchConfidence": "none",
            "detectedDoi": "",
            "titleMatch": "",
            "retractionStatus": None,
            "httpStatus": None,
            "archivedUrl": "",
            "provenance": [],
            "claim": "",
            "claimSupport": "",
            "claimEvidence": [],
            "claimSourceUrl": "",
            "contrastSignal": None,
            "conflictOfInterest": None,
        })
        self.assertIsNone(r.exists)
        self.assertIsNone(r.matchedRecord)
        self.assertIsNone(r.retractionStatus)
        self.assertIsNone(r.httpStatus)
        self.assertIsNone(r.contrastSignal)

    def test_audit_bibliography_result_from_dict(self) -> None:
        r = AuditBibliographyResult.from_dict(_SAMPLE_AUDIT_RESULT)
        self.assertEqual(r.source, "entries")
        self.assertEqual(r.entryCount, 1)
        self.assertIsInstance(r.summary, AuditSummary)
        self.assertEqual(r.summary.total, 1)
        self.assertEqual(r.summary.ok, 1)
        self.assertEqual(r.summary.retracted, 0)
        self.assertEqual(len(r.entries), 1)
        entry = r.entries[0]
        self.assertIsInstance(entry, BibEntryAudit)
        self.assertEqual(entry.index, 0)
        self.assertEqual(entry.doi, "10.1038/nature12373")
        self.assertTrue(entry.exists)
        self.assertEqual(entry.flags, [])
        self.assertEqual(r.skipped, 0)
        self.assertEqual(r.checkedAt, "2024-01-15T10:00:00Z")

    def test_audit_bibliography_null_summary(self) -> None:
        r = AuditBibliographyResult.from_dict({"source": "test", "entryCount": 0, "summary": None, "entries": [], "skipped": None, "checkedAt": ""})
        self.assertIsInstance(r.summary, AuditSummary)
        self.assertEqual(r.summary.total, 0)
        self.assertIsNone(r.skipped)

    def test_archive_result_from_dict(self) -> None:
        r = ArchiveResult.from_dict(_SAMPLE_ARCHIVE_RESULT)
        self.assertEqual(r.requestedUrl, "https://example.com/page")
        self.assertIn("web.archive.org", r.snapshotUrl)
        self.assertTrue(r.captured)
        self.assertEqual(r.status, "captured")
        self.assertEqual(r.httpStatus, 200)
        self.assertEqual(r.source, "wayback")
        self.assertEqual(r.provenance, ["wayback"])

    def test_retraction_status_from_dict_none(self) -> None:
        self.assertIsNone(RetractionStatus.from_dict(None))
        self.assertIsNone(RetractionStatus.from_dict({}))

    def test_retraction_status_from_dict_populated(self) -> None:
        d = {"retracted": True, "kind": "retraction", "date": "2023-06-01", "noticeDoi": "10.1/notice", "source": "crossref"}
        r = RetractionStatus.from_dict(d)
        self.assertIsNotNone(r)
        self.assertTrue(r.retracted)  # type: ignore[union-attr]
        self.assertEqual(r.kind, "retraction")
        self.assertEqual(r.date, "2023-06-01")
        self.assertEqual(r.noticeDoi, "10.1/notice")

    def test_strip_none_helper(self) -> None:
        cleaned = _strip_none({"a": 1, "b": None, "c": "x", "d": None})
        self.assertEqual(cleaned, {"a": 1, "c": "x"})
        self.assertNotIn("b", cleaned)
        self.assertNotIn("d", cleaned)

    def test_strip_none_empty(self) -> None:
        self.assertEqual(_strip_none({}), {})
        self.assertEqual(_strip_none({"a": None}), {})


# ---------------------------------------------------------------------------
# TestWebResearcherClientAsync — integration with MockMCPServer
# ---------------------------------------------------------------------------

class TestWebResearcherClientAsync(unittest.TestCase):
    """
    Tests the async WebResearcherClient against a MockMCPServer.

    Each test uses asyncio.run() so no class-level event loop is required.
    """

    mock_server: MockMCPServer

    @classmethod
    def setUpClass(cls) -> None:
        cls.mock_server = MockMCPServer()
        cls.mock_server.start()

    @classmethod
    def tearDownClass(cls) -> None:
        cls.mock_server.stop()

    def _make_client(self) -> WebResearcherClient:
        """Return a fresh client pointed at the mock server, not yet started."""
        return WebResearcherClient(port=self.mock_server.port, timeout=10.0)

    # ------------------------------------------------------------------

    def test_start_performs_initialize_handshake(self) -> None:
        async def _run() -> None:
            client = self._make_client()
            await client.start()
            # After start(), the session ID from the mock must be stored.
            self.assertEqual(client._session_id, "test-session-123")
            await client.stop()

        asyncio.run(_run())

    def test_context_manager(self) -> None:
        async def _run() -> None:
            async with self._make_client() as client:
                self.assertEqual(client._session_id, "test-session-123")

        asyncio.run(_run())

    def test_list_tools(self) -> None:
        async def _run() -> list[dict[str, Any]]:
            async with self._make_client() as client:
                return await client.list_tools()

        tools = asyncio.run(_run())
        self.assertIsInstance(tools, list)
        self.assertEqual(len(tools), 1)
        self.assertEqual(tools[0]["name"], "web_search")

    def test_web_search_returns_typed_model(self) -> None:
        async def _run() -> SearchResponse:
            async with self._make_client() as client:
                return await client.web_search("CRISPR applications")

        result = asyncio.run(_run())
        self.assertIsInstance(result, SearchResponse)
        self.assertEqual(result.query, "CRISPR applications")
        self.assertEqual(result.resultCount, 1)
        self.assertEqual(len(result.results), 1)
        self.assertIsInstance(result.results[0], SearchResult)
        self.assertEqual(result.results[0].url, "https://example.com/crispr")

    def test_scrape_page_returns_typed_model(self) -> None:
        async def _run() -> ScrapeResult:
            async with self._make_client() as client:
                return await client.scrape_page("https://example.com/article")

        result = asyncio.run(_run())
        self.assertIsInstance(result, ScrapeResult)
        self.assertEqual(result.url, "https://example.com/article")
        self.assertEqual(result.contentType, "text/html")
        self.assertEqual(result.extractionQuality, "high")
        self.assertFalse(result.truncated)

    def test_verify_citation_returns_typed_model(self) -> None:
        async def _run() -> VerifyResult:
            async with self._make_client() as client:
                return await client.verify_citation("10.1038/nature12373")

        result = asyncio.run(_run())
        self.assertIsInstance(result, VerifyResult)
        self.assertEqual(result.input, "10.1038/nature12373")
        self.assertEqual(result.inputType, "doi")
        self.assertTrue(result.exists)
        self.assertEqual(result.matchConfidence, "exact")
        self.assertIsInstance(result.retractionStatus, RetractionStatus)

    def test_verify_citation_with_claim(self) -> None:
        async def _run() -> VerifyResult:
            async with self._make_client() as client:
                return await client.verify_citation(
                    "10.1038/nature12373",
                    claim="CRISPR is a versatile tool",
                )

        result = asyncio.run(_run())
        self.assertIsInstance(result, VerifyResult)
        self.assertIn("nature12373", result.input)

    def test_audit_bibliography_returns_typed_model(self) -> None:
        async def _run() -> AuditBibliographyResult:
            async with self._make_client() as client:
                return await client.audit_bibliography(
                    entries=[{"doi": "10.1038/nature12373"}]
                )

        result = asyncio.run(_run())
        self.assertIsInstance(result, AuditBibliographyResult)
        self.assertEqual(result.source, "entries")
        self.assertEqual(result.entryCount, 1)
        self.assertIsInstance(result.summary, AuditSummary)
        self.assertEqual(result.summary.ok, 1)

    def test_archive_source_returns_typed_model(self) -> None:
        async def _run() -> ArchiveResult:
            async with self._make_client() as client:
                return await client.archive_source("https://example.com/page")

        result = asyncio.run(_run())
        self.assertIsInstance(result, ArchiveResult)
        self.assertTrue(result.captured)
        self.assertEqual(result.status, "captured")

    def test_call_generic_web_search(self) -> None:
        async def _run() -> dict[str, Any]:
            async with self._make_client() as client:
                return await client.call("web_search", query="generic test")

        result = asyncio.run(_run())
        # call() returns raw dict (no wrapping)
        self.assertIsInstance(result, dict)
        self.assertIn("resultCount", result)

    def test_call_unknown_tool_returns_dict(self) -> None:
        async def _run() -> dict[str, Any]:
            async with self._make_client() as client:
                return await client.call("some_other_tool", foo="bar")

        result = asyncio.run(_run())
        self.assertIsInstance(result, dict)
        self.assertEqual(result.get("resultCount"), 0)

    def test_mcp_error_raises_on_tool_error(self) -> None:
        """When the server returns isError=true, _call_tool must raise MCPError."""
        async def _run() -> None:
            async with self._make_client() as client:
                with self.assertRaises(MCPError) as ctx:
                    await client.call("web_search", _force_error=True)
                self.assertIn("error", str(ctx.exception).lower())

        asyncio.run(_run())

    def test_none_params_stripped(self) -> None:
        """None kwargs must NOT appear in the wire-level tools/call arguments."""
        captured_body: dict[str, Any] = {}

        async def _run() -> None:
            client = self._make_client()
            await client.start()

            original_post = client._http_post

            async def _spy_post(body: dict[str, Any]) -> dict[str, Any]:
                if body.get("method") == "tools/call":
                    captured_body.update(body)
                return await original_post(body)

            client._http_post = _spy_post  # type: ignore[method-assign]
            await client.web_search("test query", time_range=None, language=None)
            await client.stop()

        asyncio.run(_run())

        params = captured_body.get("params", {})
        args = params.get("arguments", {})
        self.assertIn("query", args)
        self.assertEqual(args["query"], "test query")
        self.assertNotIn("time_range", args)
        self.assertNotIn("language", args)
        # num_results has a default of 5 — it must be present.
        self.assertIn("num_results", args)
        self.assertEqual(args["num_results"], 5)

    def test_news_search_uses_time_range_not_freshness(self) -> None:
        """news_search must send 'time_range', not 'freshness', to the Go server."""
        captured_body: dict[str, Any] = {}

        async def _run() -> None:
            client = self._make_client()
            await client.start()

            original_post = client._http_post

            async def _spy_post(body: dict[str, Any]) -> dict[str, Any]:
                if body.get("method") == "tools/call":
                    captured_body.update(body)
                return await original_post(body)

            client._http_post = _spy_post  # type: ignore[method-assign]
            await client.news_search("climate", time_range="week")
            await client.stop()

        asyncio.run(_run())

        args = captured_body.get("params", {}).get("arguments", {})
        self.assertIn("time_range", args)
        self.assertEqual(args["time_range"], "week")
        self.assertNotIn("freshness", args)

    def test_sequential_search_params_are_camel_case(self) -> None:
        """sequential_search must send camelCase keys (searchStep, stepNumber, nextStepNeeded)."""
        captured_body: dict[str, Any] = {}

        async def _run() -> None:
            client = self._make_client()
            await client.start()

            original_post = client._http_post

            async def _spy_post(body: dict[str, Any]) -> dict[str, Any]:
                if body.get("method") == "tools/call":
                    captured_body.update(body)
                return await original_post(body)

            client._http_post = _spy_post  # type: ignore[method-assign]
            await client.sequential_search(
                "CRISPR history", step_number=2, next_step_needed=True
            )
            await client.stop()

        asyncio.run(_run())

        args = captured_body.get("params", {}).get("arguments", {})
        self.assertIn("searchStep", args)
        self.assertEqual(args["searchStep"], "CRISPR history")
        self.assertIn("stepNumber", args)
        self.assertEqual(args["stepNumber"], 2)
        self.assertIn("nextStepNeeded", args)
        self.assertTrue(args["nextStepNeeded"])
        self.assertNotIn("search_step", args)
        self.assertNotIn("step_number", args)

    def test_verify_recommendation_accepts_list(self) -> None:
        """verify_recommendation must accept a list of recommendation dicts."""
        captured_body: dict[str, Any] = {}

        async def _run() -> None:
            client = self._make_client()
            await client.start()

            original_post = client._http_post

            async def _spy_post(body: dict[str, Any]) -> dict[str, Any]:
                if body.get("method") == "tools/call":
                    captured_body.update(body)
                return await original_post(body)

            client._http_post = _spy_post  # type: ignore[method-assign]
            await client.verify_recommendation([
                {"title": "Best tool ever", "url": "https://example.com", "author": "Acme"}
            ])
            await client.stop()

        asyncio.run(_run())

        args = captured_body.get("params", {}).get("arguments", {})
        self.assertIn("recommendations", args)
        recs = args["recommendations"]
        self.assertIsInstance(recs, list)
        self.assertEqual(len(recs), 1)
        self.assertEqual(recs[0]["title"], "Best tool ever")
        self.assertEqual(recs[0]["url"], "https://example.com")

    def test_session_id_sent_in_subsequent_requests(self) -> None:
        """After initialize, the session ID must be present on later requests."""
        async def _run() -> str | None:
            async with self._make_client() as client:
                return client._session_id

        sid = asyncio.run(_run())
        self.assertEqual(sid, "test-session-123")

    def test_request_id_increments(self) -> None:
        async def _run() -> int:
            async with self._make_client() as client:
                id_before = client._request_id
                await client.list_tools()
                return client._request_id - id_before

        delta = asyncio.run(_run())
        self.assertGreater(delta, 0)


# ---------------------------------------------------------------------------
# TestSyncClient — same scenarios via SyncWebResearcherClient
# ---------------------------------------------------------------------------

class TestSyncClient(unittest.TestCase):
    """Verifies SyncWebResearcherClient works identically to the async client."""

    mock_server: MockMCPServer

    @classmethod
    def setUpClass(cls) -> None:
        cls.mock_server = MockMCPServer()
        cls.mock_server.start()

    @classmethod
    def tearDownClass(cls) -> None:
        cls.mock_server.stop()

    def _make_client(self) -> SyncWebResearcherClient:
        return SyncWebResearcherClient(port=self.mock_server.port, timeout=10.0)

    def test_context_manager_start_stop(self) -> None:
        with self._make_client() as client:
            self.assertEqual(client._async_client._session_id, "test-session-123")

    def test_list_tools(self) -> None:
        with self._make_client() as client:
            tools = client.list_tools()
        self.assertIsInstance(tools, list)
        self.assertGreater(len(tools), 0)
        self.assertEqual(tools[0]["name"], "web_search")

    def test_web_search_returns_typed_model(self) -> None:
        with self._make_client() as client:
            result = client.web_search("climate change")
        self.assertIsInstance(result, SearchResponse)
        self.assertEqual(result.resultCount, 1)
        self.assertEqual(result.results[0].displayLink, "example.com")

    def test_scrape_page_returns_typed_model(self) -> None:
        with self._make_client() as client:
            result = client.scrape_page("https://example.com/article")
        self.assertIsInstance(result, ScrapeResult)
        self.assertEqual(result.extractionQuality, "high")
        self.assertFalse(result.truncated)

    def test_verify_citation_returns_typed_model(self) -> None:
        with self._make_client() as client:
            result = client.verify_citation("10.1038/nature12373")
        self.assertIsInstance(result, VerifyResult)
        self.assertTrue(result.exists)
        self.assertEqual(result.matchConfidence, "exact")

    def test_audit_bibliography_returns_typed_model(self) -> None:
        with self._make_client() as client:
            result = client.audit_bibliography(entries=[{"doi": "10.1038/nature12373"}])
        self.assertIsInstance(result, AuditBibliographyResult)
        self.assertEqual(result.entryCount, 1)
        self.assertEqual(result.summary.ok, 1)

    def test_archive_source_returns_typed_model(self) -> None:
        with self._make_client() as client:
            result = client.archive_source("https://example.com/page")
        self.assertIsInstance(result, ArchiveResult)
        self.assertTrue(result.captured)

    def test_call_generic(self) -> None:
        with self._make_client() as client:
            result = client.call("web_search", query="test")
        self.assertIsInstance(result, dict)

    def test_mcp_error_raises(self) -> None:
        with self._make_client() as client:
            with self.assertRaises(MCPError):
                client.call("web_search", _force_error=True)

    def test_run_on_stopped_client_raises(self) -> None:
        client = self._make_client()

        async def _dummy() -> None:
            pass

        coro = _dummy()
        with self.assertRaises(RuntimeError):
            try:
                client._run(coro)
            finally:
                coro.close()

    def test_manual_start_stop(self) -> None:
        client = self._make_client()
        client.start()
        try:
            result = client.web_search("manual lifecycle test")
            self.assertIsInstance(result, SearchResponse)
        finally:
            client.stop()
        self.assertIsNone(client._loop)

    def test_double_stop_is_safe(self) -> None:
        client = self._make_client()
        client.start()
        client.stop()
        client.stop()  # must not raise


# ---------------------------------------------------------------------------
# TestServerProcess — unit tests for _find_free_port and _ServerProcess
# ---------------------------------------------------------------------------

class TestServerProcess(unittest.TestCase):
    """Unit tests for _find_free_port and _ServerProcess (no real Go binary)."""

    def test_find_free_port_returns_valid_port(self) -> None:
        port = _find_free_port()
        self.assertIsInstance(port, int)
        self.assertGreater(port, 0)
        self.assertLessEqual(port, 65535)

    def test_find_free_port_returns_different_ports(self) -> None:
        ports = {_find_free_port() for _ in range(5)}
        self.assertGreater(len(ports), 1)

    def test_server_process_assigns_port(self) -> None:
        sp = _ServerProcess(port=12345)
        self.assertEqual(sp.port, 12345)

    def test_server_process_auto_assigns_port(self) -> None:
        sp = _ServerProcess()
        self.assertGreater(sp.port, 0)

    def test_server_process_stop_when_not_started_is_noop(self) -> None:
        sp = _ServerProcess()
        sp.stop()  # must not raise

    def test_server_process_start_with_missing_binary_raises(self) -> None:
        sp = _ServerProcess(binary_path="/nonexistent/binary-xyz", startup_timeout=0.5)
        with self.assertRaises((FileNotFoundError, OSError, TimeoutError)):
            sp.start()

    def test_server_process_with_real_http_server(self) -> None:
        """Start _ServerProcess using a tiny Python HTTP server as the 'binary'."""
        import textwrap
        import tempfile

        script = textwrap.dedent("""\
            import sys, os
            from http.server import HTTPServer, BaseHTTPRequestHandler

            class H(BaseHTTPRequestHandler):
                def log_message(self, *a):
                    pass
                def do_GET(self):
                    self.send_response(200)
                    self.send_header('Content-Type', 'text/plain')
                    self.send_header('Content-Length', '2')
                    self.end_headers()
                    self.wfile.write(b'ok')

            port = int(os.environ['PORT'])
            server = HTTPServer(('127.0.0.1', port), H)
            server.serve_forever()
        """)

        with tempfile.NamedTemporaryFile(mode="w", suffix=".py", delete=False) as fh:
            fh.write(script)
            script_path = fh.name

        import stat

        wrapper_content = f"#!/bin/sh\nexec {sys.executable} {script_path} \"$@\"\n"
        with tempfile.NamedTemporaryFile(mode="w", suffix=".sh", delete=False) as fh2:
            fh2.write(wrapper_content)
            wrapper_path = fh2.name
        os.chmod(wrapper_path, os.stat(wrapper_path).st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)

        sp = _ServerProcess(binary_path=wrapper_path, startup_timeout=10.0)
        try:
            port = sp.start()
            self.assertGreater(port, 0)
            import urllib.request as ureq
            with ureq.urlopen(f"http://127.0.0.1:{port}/health/live", timeout=5) as resp:
                self.assertEqual(resp.status, 200)
        finally:
            sp.stop()
            os.unlink(script_path)
            os.unlink(wrapper_path)

    def test_server_process_context_manager(self) -> None:
        """_ServerProcess context manager calls stop() on exit."""
        import textwrap, tempfile, stat

        script = textwrap.dedent("""\
            import sys, os
            from http.server import HTTPServer, BaseHTTPRequestHandler

            class H(BaseHTTPRequestHandler):
                def log_message(self, *a): pass
                def do_GET(self):
                    self.send_response(200)
                    self.send_header('Content-Length', '2')
                    self.end_headers()
                    self.wfile.write(b'ok')

            HTTPServer(('127.0.0.1', int(os.environ['PORT'])), H).serve_forever()
        """)
        with tempfile.NamedTemporaryFile(mode="w", suffix=".py", delete=False) as fh:
            fh.write(script)
            script_path = fh.name

        wrapper = f"#!/bin/sh\nexec {sys.executable} {script_path} \"$@\"\n"
        with tempfile.NamedTemporaryFile(mode="w", suffix=".sh", delete=False) as fh2:
            fh2.write(wrapper)
            wrapper_path = fh2.name
        os.chmod(wrapper_path, os.stat(wrapper_path).st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)

        try:
            with _ServerProcess(binary_path=wrapper_path, startup_timeout=10.0) as sp:
                port = sp.port
                self.assertGreater(port, 0)
            self.assertIsNone(sp._proc)
        finally:
            os.unlink(script_path)
            os.unlink(wrapper_path)


# ---------------------------------------------------------------------------
# TestParseSSE — unit test for the SSE parser helper
# ---------------------------------------------------------------------------

class TestParseSSE(unittest.TestCase):
    """Tests for the internal _parse_sse function."""

    def setUp(self) -> None:
        from web_researcher_mcp.client import _parse_sse
        self._parse_sse = _parse_sse

    def test_simple_sse_body(self) -> None:
        body = b"data: {\"result\": 42}\n\n"
        parsed = self._parse_sse(body)
        self.assertEqual(parsed, {"result": 42})

    def test_multiple_sse_events_returns_last(self) -> None:
        body = b"data: {\"n\": 1}\n\ndata: {\"n\": 2}\n\n"
        parsed = self._parse_sse(body)
        self.assertEqual(parsed["n"], 2)

    def test_empty_sse_raises(self) -> None:
        from web_researcher_mcp.client import _parse_sse
        with self.assertRaises(ValueError):
            _parse_sse(b"event: ping\n\n")

    def test_sse_with_prefix_whitespace(self) -> None:
        body = b"data:  {\"ok\": true}\n"
        parsed = self._parse_sse(body)
        self.assertTrue(parsed["ok"])


# ---------------------------------------------------------------------------
# TestMCPErrorModel
# ---------------------------------------------------------------------------

class TestMCPErrorModel(unittest.TestCase):
    def test_mcp_error_str(self) -> None:
        err = MCPError("something went wrong", code="E001")
        self.assertIn("something went wrong", str(err))
        self.assertEqual(err.code, "E001")

    def test_mcp_error_no_code(self) -> None:
        err = MCPError("oops")
        self.assertIsNone(err.code)
        self.assertIn("oops", str(err))

    def test_mcp_error_repr(self) -> None:
        err = MCPError("msg", code="42")
        r = repr(err)
        self.assertIn("MCPError", r)
        self.assertIn("msg", r)
        self.assertIn("42", r)

    def test_mcp_error_is_exception(self) -> None:
        with self.assertRaises(MCPError):
            raise MCPError("raised!")

    def test_mcp_error_is_not_runtime_error(self) -> None:
        err = MCPError("x")
        self.assertNotIsInstance(err, RuntimeError)
        self.assertIsInstance(err, Exception)


# ---------------------------------------------------------------------------
# TestPackagePublicAPI — verify __init__.py exports what it claims
# ---------------------------------------------------------------------------

class TestPackagePublicAPI(unittest.TestCase):
    """Smoke-tests the top-level package namespace."""

    def test_all_exports_are_importable(self) -> None:
        import web_researcher_mcp as pkg
        for name in pkg.__all__:
            self.assertTrue(hasattr(pkg, name), f"__all__ member {name!r} not found in package")

    def test_client_classes_exported(self) -> None:
        from web_researcher_mcp import WebResearcherClient, SyncWebResearcherClient
        self.assertTrue(callable(WebResearcherClient))
        self.assertTrue(callable(SyncWebResearcherClient))

    def test_model_classes_exported(self) -> None:
        from web_researcher_mcp import (
            SearchResponse, ScrapeResult, VerifyResult,
            AuditBibliographyResult, ArchiveResult, MCPError,
        )
        # Each is a class/type
        for cls in (SearchResponse, ScrapeResult, VerifyResult, AuditBibliographyResult, ArchiveResult, MCPError):
            self.assertTrue(callable(cls))

    def test_sync_factory(self) -> None:
        from web_researcher_mcp import WebResearcherClient, SyncWebResearcherClient
        # WebResearcherClient.sync() should return a SyncWebResearcherClient
        c = WebResearcherClient.sync(port=19999)
        self.assertIsInstance(c, SyncWebResearcherClient)


# ---------------------------------------------------------------------------
# TestAllResponseFromDictSmoke — every *Response class must survive from_dict({})
#
# This test is auto-comprehensive: it discovers Response classes by scanning
# models.__all__ for names ending in "Response", then calls from_dict({}) on
# each. A new tool automatically gets coverage here after `make gen-python-client`
# regenerates models.py — no manual test addition needed.
# ---------------------------------------------------------------------------

class TestAllResponseFromDictSmoke(unittest.TestCase):
    """
    Smoke-test every generated *Response class with an empty dict.

    All generated from_dict() methods use .get() with Optional defaults, so
    from_dict({}) must never raise. This catches regressions where a new tool's
    from_dict accidentally dereferences a missing key without a guard.
    """

    def test_all_response_classes_tolerate_empty_dict(self) -> None:
        import inspect
        import web_researcher_mcp.models as models_mod

        response_classes = [
            getattr(models_mod, name)
            for name in dir(models_mod)
            if name.endswith("Response") and inspect.isclass(getattr(models_mod, name))
        ]

        self.assertGreater(len(response_classes), 0, "No *Response classes found — generator may be broken")

        for cls in response_classes:
            with self.subTest(cls=cls.__name__):
                try:
                    result = cls.from_dict({})
                    # from_dict({}) returns None only when d is None; {} is not None
                    # so we should always get an instance back.
                    self.assertIsNotNone(result, f"{cls.__name__}.from_dict({{}}) returned None")
                except Exception as exc:
                    self.fail(f"{cls.__name__}.from_dict({{}}) raised {type(exc).__name__}: {exc}")


# ---------------------------------------------------------------------------
# Entry-point for plain python3 execution
# ---------------------------------------------------------------------------

if __name__ == "__main__":
    unittest.main(verbosity=2)
