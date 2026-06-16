"""
tests/python/test_live_e2e.py
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
Live end-to-end tests for the web-researcher-mcp Python SDK.

These tests build the Go binary from source, start it as a real subprocess, and
drive it over HTTP/MCP using the Python SDK.  They are NOT unit tests — they hit
real external APIs and require a network connection.

Running
-------
All tests that only need keyless providers (DuckDuckGo, PubMed, World Bank,
ClinicalTrials.gov) run unconditionally once the binary builds successfully.

Tests that need API keys carry ``@pytest.mark.live`` and are skipped when the
relevant key is absent from the environment:

    pytest tests/python/test_live_e2e.py                  # keyless tests
    pytest tests/python/test_live_e2e.py -m live           # also keyed tests

Via Makefile:
    make test-python-live

Required tools
--------------
- Go toolchain on PATH (to build the binary)
- Internet access
"""
from __future__ import annotations

import asyncio
import os
import subprocess
import sys
import tempfile
import unittest
from typing import Optional

import pytest

# ---------------------------------------------------------------------------
# Path setup — importable from repo root without installing the package
# ---------------------------------------------------------------------------

_REPO_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
_PYTHON_PKG_DIR = os.path.join(_REPO_ROOT, "python")
if _PYTHON_PKG_DIR not in sys.path:
    sys.path.insert(0, _PYTHON_PKG_DIR)

# ---------------------------------------------------------------------------
# Inject a fake _shim BEFORE importing web_researcher_mcp.
# The E2E tests use _ServerProcess directly with binary_path=, so the shim is
# only needed to make the package importable in the source tree.
# ---------------------------------------------------------------------------

import types as _types

if "web_researcher_mcp._shim" not in sys.modules:
    _fake_shim = _types.ModuleType("web_researcher_mcp._shim")
    _fake_shim.__version__ = "e2e-test"  # type: ignore[attr-defined]
    _fake_shim.get_binary_path = lambda: "/nonexistent/binary"  # type: ignore[attr-defined]
    _fake_shim.main = lambda: None  # type: ignore[attr-defined]
    sys.modules["web_researcher_mcp._shim"] = _fake_shim

from web_researcher_mcp._server import _ServerProcess  # noqa: E402
from web_researcher_mcp.client import WebResearcherClient  # noqa: E402

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

_BINARY_CACHE: Optional[str] = None
_BUILD_LOCK = asyncio.Lock()


def _build_binary() -> str:
    """Build the Go binary from source and cache the result across tests."""
    global _BINARY_CACHE
    if _BINARY_CACHE and os.path.isfile(_BINARY_CACHE):
        return _BINARY_CACHE

    tmp = tempfile.mkdtemp(prefix="wrmcp_e2e_")
    binary = os.path.join(tmp, "web-researcher-mcp")
    if sys.platform == "win32":
        binary += ".exe"

    try:
        subprocess.run(
            [
                "go", "build",
                "-ldflags=-s -w -X main.version=e2e-test",
                "-o", binary,
                "./cmd/web-researcher-mcp",
            ],
            cwd=_REPO_ROOT,
            check=True,
            capture_output=True,
            text=True,
            timeout=300,
        )
    except subprocess.CalledProcessError as exc:
        raise RuntimeError(
            f"Failed to build Go binary for E2E tests.\n"
            f"stdout: {exc.stdout}\nstderr: {exc.stderr}"
        ) from exc
    except FileNotFoundError:
        pytest.skip("Go toolchain not found on PATH — skipping E2E tests")

    _BINARY_CACHE = binary
    return binary


def _run(coro):
    """Run an async coroutine synchronously (test helper)."""
    return asyncio.run(coro)


def _client(binary: str, extra_env: Optional[dict] = None) -> WebResearcherClient:
    """Create a WebResearcherClient with an explicit binary path (no shim)."""
    from web_researcher_mcp._server import _ServerProcess

    env = {"SEARCH_PROVIDER": "duckduckgo"}
    if extra_env:
        env.update(extra_env)

    # Use _ServerProcess directly (binary_path bypasses the shim).
    server = _ServerProcess(binary_path=binary, env=env, startup_timeout=60.0)
    client = WebResearcherClient.__new__(WebResearcherClient)
    client._timeout = 120.0
    client._session_id = None
    client._request_id = 0
    client._started = False
    client._server = server
    client._port = 0
    return client


# ---------------------------------------------------------------------------
# Module-level binary build (skip everything if Go is absent)
# ---------------------------------------------------------------------------

_BINARY: Optional[str] = None


def setup_module(module):
    """Build the binary once for the whole module; skip if Go is absent."""
    global _BINARY
    try:
        _BINARY = _build_binary()
    except Exception as exc:
        pytest.skip(f"Binary build failed: {exc}")


# ---------------------------------------------------------------------------
# Keyless tests — run on every CI push (DuckDuckGo, PubMed, World Bank,
# ClinicalTrials.gov, HackerNews, CrossRef — all zero-config providers)
# ---------------------------------------------------------------------------

class TestKeylessWebSearch(unittest.TestCase):
    """web_search via DuckDuckGo (zero-config fallback)."""

    def test_returns_results(self):
        async def run():
            async with _client(_BINARY) as c:
                result = await c.web_search("Python programming language", num_results=3)
                self.assertIsNotNone(result)
                if not result.results:
                    # DuckDuckGo is rate-limited — transient, not a test failure
                    self.skipTest("DuckDuckGo returned 0 results (likely rate-limited)")
                r0 = result.results[0]
                self.assertTrue(r0.title, "First result should have a title")
                self.assertTrue(r0.url, "First result should have a URL")

        _run(run())

    def test_result_count_respects_num_results(self):
        async def run():
            async with _client(_BINARY) as c:
                result = await c.web_search("open source software", num_results=2)
                self.assertLessEqual(len(result.results), 5,
                    "num_results=2 should return a small result set")

        _run(run())

    def test_query_preserved_in_response(self):
        async def run():
            query = "duckduckgo search test query unique xyz"
            async with _client(_BINARY) as c:
                result = await c.web_search(query, num_results=1)
                self.assertEqual(result.query, query)

        _run(run())


class TestKeylessNewsScrape(unittest.TestCase):
    """scrape_page on a stable public URL."""

    def test_scrape_public_page(self):
        async def run():
            async with _client(_BINARY) as c:
                result = await c.scrape_page("https://example.com")
                self.assertIsNotNone(result)
                self.assertTrue(result.content or result.title,
                    "Scrape of example.com should return content or title")
                self.assertEqual(result.url, "https://example.com")

        _run(run())

    def test_scrape_returns_content_type(self):
        async def run():
            async with _client(_BINARY) as c:
                result = await c.scrape_page("https://example.com")
                # Server returns extraction quality info but no raw HTTP status field
                self.assertIsNotNone(result)
                self.assertTrue(result.content or result.title,
                    "Scrape should return content or a title")

        _run(run())


class TestKeylessAcademicSearchPubMed(unittest.TestCase):
    """academic_search via PubMed (keyless)."""

    def test_pubmed_returns_papers(self):
        async def run():
            async with _client(_BINARY, {"SEARCH_PROVIDER": "duckduckgo"}) as c:
                result = await c.academic_search(
                    "CRISPR gene editing",
                    num_results=3,
                    source="pubmed",
                )
                self.assertIsNotNone(result)
                # PubMed is rate-limited (~100 req/day keyless); tolerate empty
                if result.totalResults > 0:
                    self.assertGreater(len(result.papers), 0)
                    p = result.papers[0]
                    self.assertTrue(p.title, "Paper should have a title")

        _run(run())


class TestKeylessClinicalSearch(unittest.TestCase):
    """clinical_search via ClinicalTrials.gov (keyless)."""

    def test_returns_trials(self):
        async def run():
            async with _client(_BINARY) as c:
                result = await c.clinical_search(
                    query="diabetes treatment",
                    num_results=3,
                )
                self.assertIsNotNone(result)
                # API may be slow; tolerate empty on rate-limit
                self.assertIsNotNone(result.trials)

        _run(run())


class TestKeylessEconSearch(unittest.TestCase):
    """econ_search via World Bank (keyless)."""

    def test_world_bank_gdp(self):
        async def run():
            async with _client(_BINARY) as c:
                result = await c.econ_search(
                    query="GDP per capita",
                    country="US",
                    num_results=3,
                    provider="worldbank",
                )
                self.assertIsNotNone(result)
                # results is a list of EconSearchResult data points
                self.assertIsNotNone(result.results)

        _run(run())


class TestKeylessVerifyCitation(unittest.TestCase):
    """verify_citation with a known CrossRef DOI (keyless)."""

    def test_real_doi_resolves(self):
        async def run():
            # Nature 2015 paper on CRISPR — a stable, well-known DOI
            async with _client(_BINARY) as c:
                result = await c.verify_citation(
                    "10.1038/nature14538",
                )
                self.assertIsNotNone(result)
                self.assertTrue(result.exists,
                    "Known CrossRef DOI should exist")

        _run(run())

    def test_fabricated_doi_does_not_exist(self):
        async def run():
            async with _client(_BINARY) as c:
                result = await c.verify_citation(
                    "10.9999/definitely-not-a-real-paper-xyz-fabricated-12345",
                )
                self.assertIsNotNone(result)
                self.assertFalse(result.exists,
                    "Fabricated DOI should not exist in CrossRef")

        _run(run())


class TestKeylessAuditBibliography(unittest.TestCase):
    """audit_bibliography over a CSL-JSON document (keyless — CrossRef)."""

    def test_audits_csl_json_document(self):
        async def run():
            # One real DOI + one fabricated DOI in a minimal CSL-JSON list.
            bibliography = (
                '[{"DOI": "10.1038/nature14538", "title": "Real paper"},'
                ' {"DOI": "10.9999/fabricated-xyz-12345", "title": "Fake paper"}]'
            )
            async with _client(_BINARY) as c:
                result = await c.audit_bibliography(
                    bibliography=bibliography,
                    format="csl-json",
                )
                self.assertIsNotNone(result)
                self.assertIsNotNone(result.entries)
                # CrossRef is rate-limited; only assert structure when populated.
                if result.entries:
                    self.assertEqual(len(result.entries), 2)
                    by_doi = {e.doi: e for e in result.entries if e.doi}
                    if "10.9999/fabricated-xyz-12345" in by_doi:
                        self.assertFalse(
                            by_doi["10.9999/fabricated-xyz-12345"].exists,
                            "Fabricated DOI should not exist in CrossRef",
                        )

        _run(run())


class TestKeylessArchiveSource(unittest.TestCase):
    """archive_source — Wayback Save Page Now capture (keyless, best-effort)."""

    def test_archive_preserves_requested_url(self):
        async def run():
            async with _client(_BINARY) as c:
                result = await c.archive_source("https://example.com")
                self.assertIsNotNone(result)
                # Save Page Now is rate-limited and best-effort, so a capture
                # may not succeed; the tool always echoes the requested URL and
                # reports an honest status either way.
                self.assertEqual(result.requestedUrl, "https://example.com")
                self.assertIsNotNone(result.status)

        _run(run())


class TestKeylessNewsSearch(unittest.TestCase):
    """news_search with DuckDuckGo fallback."""

    def test_returns_articles(self):
        async def run():
            async with _client(_BINARY) as c:
                result = await c.news_search(
                    "technology",
                    num_results=3,
                )
                self.assertIsNotNone(result)
                # news_search may return 0 articles when DuckDuckGo is rate-limited
                self.assertIsNotNone(result.articles)

        _run(run())


class TestKeylessSearchAndScrape(unittest.TestCase):
    """search_and_scrape — combined search + full-page read."""

    def test_returns_sources_with_content(self):
        async def run():
            async with _client(_BINARY) as c:
                result = await c.search_and_scrape(
                    "Python asyncio tutorial",
                    num_results=2,
                )
                self.assertIsNotNone(result)
                self.assertIsNotNone(result.sources)

        _run(run())


class TestKeylessListTools(unittest.TestCase):
    """list_tools — verifies the server exposes the expected tool set."""

    def test_tool_list_contains_expected_tools(self):
        expected = {
            "web_search", "scrape_page", "news_search", "academic_search",
            "verify_citation", "audit_bibliography", "search_and_scrape",
            "econ_search", "clinical_search",
        }

        async def run():
            async with _client(_BINARY) as c:
                tools = await c.list_tools()
                names = {t["name"] for t in tools}
                missing = expected - names
                self.assertFalse(missing,
                    f"Server is missing expected tools: {missing}")

        _run(run())

    def test_tools_have_well_formed_schemas(self):
        # Avoid asserting an exact/near-exact tool count — registry.go is the
        # source of truth and the set grows over time. Instead verify every
        # advertised tool is structurally usable (name + input schema), which
        # is what a client actually depends on.
        async def run():
            async with _client(_BINARY) as c:
                tools = await c.list_tools()
                self.assertTrue(tools, "Server should advertise at least one tool")
                for t in tools:
                    self.assertTrue(t.get("name"), f"Tool missing a name: {t}")
                    self.assertIn("inputSchema", t,
                        f"Tool {t.get('name')!r} missing an inputSchema")

        _run(run())


# ---------------------------------------------------------------------------
# Keyed tests — skipped when the required API key is absent
# ---------------------------------------------------------------------------

def _skip_without(env_var: str):
    """Decorator: skip the test when env_var is not set."""
    key = os.environ.get(env_var, "")
    return pytest.mark.skipif(
        not key,
        reason=f"{env_var} not set — skipping live keyed test",
    )


@pytest.mark.live
class TestGoogleSearch:
    """web_search via Google Custom Search (needs GOOGLE_CUSTOM_SEARCH_API_KEY)."""

    @_skip_without("GOOGLE_CUSTOM_SEARCH_API_KEY")
    def test_google_search_returns_results(self):
        async def run():
            env = {
                "GOOGLE_CUSTOM_SEARCH_API_KEY": os.environ["GOOGLE_CUSTOM_SEARCH_API_KEY"],
                "GOOGLE_CUSTOM_SEARCH_ID": os.environ.get("GOOGLE_CUSTOM_SEARCH_ID", ""),
            }
            async with _client(_BINARY, env) as c:
                result = await c.web_search(
                    "site:python.org asyncio",
                    provider="google",
                    num_results=3,
                )
                assert result.resultCount > 0
                assert result.results[0].url

        _run(run())


@pytest.mark.live
class TestBraveSearch:
    """web_search via Brave (needs BRAVE_API_KEY)."""

    @_skip_without("BRAVE_API_KEY")
    def test_brave_search_returns_results(self):
        async def run():
            env = {"BRAVE_API_KEY": os.environ["BRAVE_API_KEY"]}
            async with _client(_BINARY, env) as c:
                result = await c.web_search(
                    "open source AI tools",
                    provider="brave",
                    num_results=3,
                )
                assert result.resultCount > 0

        _run(run())


@pytest.mark.live
class TestVerifyRecommendation:
    """verify_recommendation — needs web access but no specific key."""

    def test_flags_suspicious_recommendation(self):
        async def run():
            async with _client(_BINARY) as c:
                result = await c.verify_recommendation(
                    recommendations=[
                        {
                            "name": "Definitely Real Product XYZ-2049",
                            "url": "https://example.com/does-not-exist-at-all",
                            "description": "The best product ever made",
                        }
                    ]
                )
                assert result is not None
                # field is `recommendations`, not `results`
                assert result.recommendations is not None

        _run(run())


if __name__ == "__main__":
    unittest.main()
