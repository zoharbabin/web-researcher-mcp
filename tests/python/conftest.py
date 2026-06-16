"""
tests/python/conftest.py
~~~~~~~~~~~~~~~~~~~~~~~~
Pytest configuration for the Python SDK test suite.

Registers the custom ``live`` mark so ``pytest -m live`` selects only the
live-network E2E tests and ``--strict-markers`` does not fail on an unknown mark.
"""
import pytest


def pytest_configure(config: pytest.Config) -> None:
    config.addinivalue_line(
        "markers",
        "live: marks tests that call real external APIs and require API keys (deselect with -m 'not live')",
    )
