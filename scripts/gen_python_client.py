#!/usr/bin/env python3
"""Generate python/web_researcher_mcp/client.py and models.py from Go tool schemas.

Usage:
    go run ./cmd/gen-python-client | python3 scripts/gen_python_client.py
    go run ./cmd/gen-python-client | python3 scripts/gen_python_client.py --dry-run
    go run ./cmd/gen-python-client | python3 scripts/gen_python_client.py --report

The generator reads the tools/list JSON array from stdin (produced by
cmd/gen-python-client) and writes:
  python/web_researcher_mcp/client.py   — async client + sync wrapper
  python/web_researcher_mcp/models.py   — dataclasses from output schemas

Flags:
  --dry-run   Print a diff of what would change; exit 1 if files would change.
  --report    Print a summary of new/changed/unchanged files; exit 1 if stale.
  --help      Show this message.

Naming rules applied to output schemas:
  Rule 1  Tool result root class:    PascalCase(tool_name) + "Response"
          e.g. web_search → WebSearchResponse
  Rule 2  Array item:                PascalCase(tool_name) + PascalCase(singular(key))
          e.g. web_search results[] → WebSearchResult
  Rule 3  Nested object:             PascalCase(key)
  Rule 4  Fingerprint dedup:         identical schema shapes share one class
  Rule 5  Collision escape:          numeric suffix appended if name is taken
          e.g. Foo → Foo2, Foo3, ...
"""
from __future__ import annotations

import hashlib
import json
import os
import re
import sys
import textwrap
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

_IRREGULARS = {
    "results": "result",
    "entries": "entry",
    "items": "item",
    "studies": "study",
    "series": "series",
    "indices": "index",
    "analyses": "analysis",
    "urls": "url",
    "authors": "author",
    "tags": "tag",
    "providers": "provider",
    "citations": "citation",
    "references": "reference",
    "phases": "phase",
    "conditions": "condition",
    "interventions": "intervention",
    "provenance": "provenance_item",
    "steps": "step",
    "sources": "source",
    "recommendations": "recommendation",
    "issues": "issue",
    "signals": "signal",
    "hints": "hint",
    "nodes": "node",
    "edges": "edge",
    "indicators": "indicator",
    "highlights": "highlight",
    "data": "data_item",
    "observations": "observation",
    "periods": "period",
    "dimensions": "dimension",
    "values": "value_item",
}


def _singular(word: str) -> str:
    w = word.lower()
    if w in _IRREGULARS:
        return _IRREGULARS[w]
    if w.endswith("ies"):
        return w[:-3] + "y"
    if w.endswith("ses") or w.endswith("xes") or w.endswith("ches") or w.endswith("shes"):
        return w[:-2]
    if w.endswith("s") and not w.endswith("ss"):
        return w[:-1]
    return w


def _pascal(s: str) -> str:
    """Convert snake_case or camelCase or hyphen-case to PascalCase."""
    s = re.sub(r"[-_]([a-z])", lambda m: m.group(1).upper(), s)
    if s:
        s = s[0].upper() + s[1:]
    return s


def _schema_fingerprint(schema: dict[str, Any]) -> str:
    # Hash the full canonical schema, not just top-level property name:type
    # pairs — two object schemas that share top-level keys but differ in nested
    # structure (e.g. an `items` object's properties) must NOT collide into the
    # same generated dataclass.
    canonical = json.dumps(schema, sort_keys=True, separators=(",", ":"))
    return hashlib.sha1(canonical.encode()).hexdigest()[:12]


def _pytype(schema: dict[str, Any], class_name: str | None = None) -> str:
    """Return the Python type annotation string for a JSON schema node."""
    t = schema.get("type")
    if isinstance(t, list):
        # ["null", "string"] → Optional[str]
        non_null = [x for x in t if x != "null"]
        if non_null:
            return f"Optional[{_pytype({**schema, 'type': non_null[0]})}]"
        return "Any"
    if t == "string":
        return "str"
    if t == "integer":
        return "int"
    if t == "number":
        return "float"
    if t == "boolean":
        return "bool"
    if t == "array":
        items = schema.get("items", {})
        if items.get("type") == "object" or "properties" in items:
            return f"list[{class_name}]" if class_name else "list[Any]"
        return f"list[{_pytype(items)}]"
    if t == "object" or "properties" in schema:
        return class_name if class_name else "dict[str, Any]"
    if "enum" in schema:
        return "str"
    return "Any"


def _param_pytype(schema: dict[str, Any]) -> str:
    t = schema.get("type")
    if isinstance(t, list):
        non_null = [x for x in t if x != "null"]
        if non_null:
            inner = _param_pytype({**schema, "type": non_null[0]})
            # The array/object branches already return an Optional[...]; don't
            # double-wrap a nullable array/object into Optional[Optional[...]].
            return inner if inner.startswith("Optional[") else f"Optional[{inner}]"
        return "Any"
    if t == "string":
        return "str"
    if t == "integer":
        return "int"
    if t == "number":
        return "float"
    if t == "boolean":
        return "bool"
    if t == "array":
        return "Optional[list]"
    if t == "object":
        return "Optional[dict]"
    return "Any"


def _param_default(schema: dict[str, Any], is_required: bool) -> str:
    if is_required:
        return ""  # positional
    t = schema.get("type")
    if isinstance(t, list) and "null" in t:
        return " = None"
    if t == "boolean":
        return " = False"
    if t == "integer":
        d = schema.get("default")
        if d is not None:
            return f" = {d}"
        return " = None"
    return " = None"


# ---------------------------------------------------------------------------
# Class registry: tracks all generated dataclasses, deduplicating by schema
# fingerprint.
# ---------------------------------------------------------------------------

@dataclass
class ClassDef:
    name: str
    fields: list[tuple[str, str, str]]  # (field_name, type_str, default)
    from_dict_body: list[str]
    source_schema: dict[str, Any]


class ClassRegistry:
    def __init__(self) -> None:
        # fingerprint → ClassDef
        self._by_fp: dict[str, ClassDef] = {}
        # name → ClassDef  (for collision detection)
        self._by_name: dict[str, ClassDef] = {}

    def pre_register(self, name: str, schema: dict[str, Any]) -> None:
        """Reserve *name* for *schema* before any child schemas are walked.

        Called for all root-level classes first so that child schemas can
        never steal a root's canonical name (Rule 5 collision escape).
        """
        fp = _schema_fingerprint(schema)
        if fp in self._by_fp:
            return  # already registered (shouldn't happen for roots)
        cd = ClassDef(name=name, fields=[], from_dict_body=[], source_schema=schema)
        self._by_fp[fp] = cd
        self._by_name[name] = cd

    def resolve(
        self,
        name: str,
        schema: dict[str, Any],
        tool_prefix: str = "",
    ) -> str:
        """Intern *schema* under *name* and return the canonical class name."""
        fp = _schema_fingerprint(schema)

        # Same fingerprint → reuse existing class (Rule 4).
        if fp in self._by_fp:
            return self._by_fp[fp].name

        # Name collision with a different schema → apply Rule 5 prefix (Rule 5).
        candidate = name
        if candidate in self._by_name:
            candidate = _pascal(tool_prefix) + name
        # If that's also taken, append a numeric suffix to guarantee uniqueness.
        base = candidate
        counter = 2
        while candidate in self._by_name:
            candidate = f"{base}{counter}"
            counter += 1

        cd = ClassDef(
            name=candidate,
            fields=[],
            from_dict_body=[],
            source_schema=schema,
        )
        self._by_fp[fp] = cd
        self._by_name[candidate] = cd
        return candidate

    def get(self, fp: str) -> ClassDef | None:
        return self._by_fp.get(fp)

    def all_classes(self) -> list[ClassDef]:
        # Sort by name for deterministic output.
        return sorted(self._by_name.values(), key=lambda c: c.name)


# ---------------------------------------------------------------------------
# Schema walker: recursively builds ClassDefs from a JSON schema.
# ---------------------------------------------------------------------------

class SchemaWalker:
    def __init__(self, registry: ClassRegistry) -> None:
        self._reg = registry

    def walk(
        self,
        schema: dict[str, Any],
        class_name: str,
        tool_prefix: str,
        field_coerce: dict[str, str] | None = None,
    ) -> str:
        """Process *schema* recursively. Returns the canonical class name.

        field_coerce: optional {field_name: python_expression} injected into
        the root class's from_dict body instead of the auto-generated line.
        Only applied at the call site that passes field_coerce (root schema).
        """
        if schema.get("type") != "object" and "properties" not in schema:
            return _pytype(schema)

        props = schema.get("properties", {})
        fp = _schema_fingerprint(schema)

        # Intern the class first (may rename on collision).
        canonical = self._reg.resolve(class_name, schema, tool_prefix)
        cd = self._reg.get(fp)
        assert cd is not None

        if cd.fields:
            # Already populated — schema dedup case, nothing to do.
            return canonical

        fld_lines = []
        fd_lines: list[str] = []

        for key, prop_schema in props.items():
            py_field = key  # keep field names as-is (camelCase from server)

            # Apply per-field coercion override if supplied.
            if field_coerce and key in field_coerce:
                coerce_expr = field_coerce[key]
                # Determine annotation: retractionStatus → Optional[RetractionStatus],
                # summary → Optional[Summary].  Use a heuristic: if the expression
                # starts with a class name followed by '.from_dict', use that class.
                m = re.match(r"(\w+)\.from_dict", coerce_expr)
                coerce_type = m.group(1) if m else "Any"
                fld_lines.append((py_field, f"Optional[{coerce_type}]", "None"))
                fd_lines.append(f"            {py_field}={coerce_expr},")
                continue

            prop_type = prop_schema.get("type")

            if isinstance(prop_type, list):
                non_null = [x for x in prop_type if x != "null"]
                if non_null and (non_null[0] == "array" or non_null[0] == "object"):
                    prop_type = non_null[0]

            if prop_type == "array" or (
                isinstance(prop_schema.get("type"), list)
                and any(x == "array" for x in prop_schema.get("type", []))
            ):
                items = prop_schema.get("items", {})
                if items.get("type") == "object" or "properties" in items:
                    # Use tool-prefixed item name by default to avoid cross-tool collisions.
                    # Falls back to the generic singular name only if dedup resolves to it first.
                    item_class_name = _pascal(tool_prefix) + _pascal(_singular(key))
                    item_canonical = self.walk(items, item_class_name, tool_prefix)
                    fld_lines.append((py_field, f"list[{item_canonical}]", "field(default_factory=list)"))
                    fd_lines.append(
                        f"            {py_field}=[{item_canonical}.from_dict(i) for i in (d.get('{key}') or [])],"
                    )
                else:
                    item_pytype = _pytype(items)
                    fld_lines.append((py_field, f"list[{item_pytype}]", "field(default_factory=list)"))
                    fd_lines.append(f"            {py_field}=list(d.get('{key}') or []),")

            elif prop_type == "object" or (
                prop_type is None and ("properties" in prop_schema or prop_schema.get("additionalProperties"))
            ):
                if "properties" in prop_schema:
                    nested_name = _pascal(key)
                    nested_canonical = self.walk(prop_schema, nested_name, tool_prefix)
                    fld_lines.append((py_field, f"Optional[{nested_canonical}]", "None"))
                    fd_lines.append(
                        f"            {py_field}={nested_canonical}.from_dict(d.get('{key}')) if d.get('{key}') else None,"
                    )
                else:
                    fld_lines.append((py_field, "dict[str, Any]", "field(default_factory=dict)"))
                    fd_lines.append(f"            {py_field}=dict(d.get('{key}') or {{}}),")

            else:
                ann = _pytype(prop_schema)
                # Wrap in Optional for any non-required field.
                if "enum" in prop_schema:
                    # Trust marker etc — always Optional[str] with None default
                    fld_lines.append((py_field, "Optional[str]", "None"))
                    fd_lines.append(f"            {py_field}=d.get('{key}'),")
                elif ann == "bool" or ann == "Optional[bool]":
                    fld_lines.append((py_field, "Optional[bool]", "None"))
                    fd_lines.append(f"            {py_field}=d.get('{key}'),")
                elif ann in ("int", "float"):
                    fld_lines.append((py_field, f"Optional[{ann}]", "None"))
                    fd_lines.append(f"            {py_field}=d.get('{key}'),")
                else:
                    fld_lines.append((py_field, "Optional[str]", "None"))
                    fd_lines.append(f"            {py_field}=d.get('{key}'),")

        cd.fields = fld_lines
        cd.from_dict_body = fd_lines
        return canonical


# ---------------------------------------------------------------------------
# Python-friendly API overrides
#
# These tables let specific tools expose a cleaner Python API without changing
# the Go schema.  Each override survives generator re-runs because it lives
# here in the generator, not in the generated file.
# ---------------------------------------------------------------------------

# Per-tool parameter default overrides.  key → (python_type, python_default)
# Used when the schema default is None but a concrete default improves ergonomics.
_PARAM_DEFAULTS: dict[str, dict[str, tuple[str, str]]] = {
    "web_search": {
        "num_results": ("int", "5"),
    },
    "news_search": {
        "num_results": ("int", "5"),
    },
    "academic_search": {
        "num_results": ("int", "5"),
    },
    "image_search": {
        "num_results": ("int", "5"),
    },
}

# Per-tool parameter renames: python_name → wire_name (name sent to Go server).
# Lets the Python API expose a different name from the JSON schema.
_PARAM_RENAMES: dict[str, dict[str, str]] = {
    # sequential_search exposes snake_case aliases for its camelCase params.
    "sequential_search": {
        "search_step": "searchStep",
        "step_number": "stepNumber",
        "next_step_needed": "nextStepNeeded",
    },
}

# Per-tool output field post-processors emitted directly into from_dict bodies.
# key = tool_name, value = dict of {field_name: python_expression_template}
# The template receives the raw d.get(field) value as {raw}.
_OUTPUT_FIELD_COERCE: dict[str, dict[str, str]] = {
    # Optional entity dicts — return None when absent so callers can `if x is None`.
    "scrape_page": {
        "retractionStatus": "d.get('retractionStatus') or None",
    },
    "verify_citation": {
        "matchedRecord": "d.get('matchedRecord') or None",
        "retractionStatus": (
            "RetractionStatus.from_dict(d.get('retractionStatus')) "
            "if d.get('retractionStatus') else None"
        ),
    },
    # audit_bibliography.summary must never be None — return empty Summary() if absent.
    "audit_bibliography": {
        "summary": (
            "AuditBibliographySummary.from_dict(d.get('summary')) "
            "if d.get('summary') else AuditBibliographySummary()"
        ),
    },
}


def _build_method_params(
    tool_name: str,
    input_schema: dict[str, Any],
) -> tuple[list[str], list[str]]:
    """Return (sig_parts, body_parts) for the async method.

    Applies _PARAM_RENAMES (python alias → wire name) and _PARAM_DEFAULTS
    (explicit Python-side defaults not present in the JSON schema).
    """
    props = input_schema.get("properties", {})
    required = set(input_schema.get("required", []))
    renames = _PARAM_RENAMES.get(tool_name, {})  # py_name → wire_name
    wire_to_py = {v: k for k, v in renames.items()}  # reverse: wire_name → py_name
    defaults_override = _PARAM_DEFAULTS.get(tool_name, {})

    sig: list[str] = []
    body: list[str] = []

    # Required positional params first — iterate in schema `required` array order
    # (not props dict order) so callers can pass them positionally in a stable,
    # predictable sequence that matches the Go struct field order.
    required_ordered = input_schema.get("required", [])
    for wire_key in required_ordered:
        if wire_key not in props:
            continue
        schema = props[wire_key]
        py_key = wire_to_py.get(wire_key, wire_key)
        ann = _param_pytype(schema)
        sig.append(f"{py_key}: {ann}")
        body.append(f'                "{wire_key}": {py_key},')

    # Params from _PARAM_RENAMES that add new python-only aliases for schema params.
    # These get inserted at the top of optional params (already skipped below if wire key not in props).
    added_renames: set[str] = set()
    for py_key, wire_key in renames.items():
        if wire_key in required:
            continue  # handled above
        if wire_key not in props:
            continue  # orphaned rename — skip
        schema = props[wire_key]
        ann = _param_pytype(schema)
        if py_key in defaults_override:
            ann_override, default_override = defaults_override[py_key]
            sig.append(f"{py_key}: {ann_override} = {default_override}")
        else:
            default = _param_default(schema, False)
            sig.append(f"{py_key}: {ann}{default}")
        body.append(f'                "{wire_key}": {py_key},')
        added_renames.add(wire_key)

    # Optional keyword params (skip renamed wire keys — they're handled above).
    for wire_key, schema in props.items():
        if wire_key in required:
            continue
        if wire_key in added_renames:
            continue  # already emitted via rename
        ann = _param_pytype(schema)
        py_key = wire_key  # no rename for this param
        if py_key in defaults_override:
            ann_override, default_override = defaults_override[py_key]
            sig.append(f"{py_key}: {ann_override} = {default_override}")
        else:
            default = _param_default(schema, False)
            sig.append(f"{py_key}: {ann}{default}")
        body.append(f'                "{wire_key}": {py_key},')

    return sig, body


# ---------------------------------------------------------------------------
# Code emitters
# ---------------------------------------------------------------------------

_MODELS_HEADER = '''\
"""
web_researcher_mcp.models
~~~~~~~~~~~~~~~~~~~~~~~~~
Dataclasses for web-researcher-mcp tool responses.

AUTO-GENERATED — do not edit by hand.
Run: make gen-python-client
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Optional


class MCPError(Exception):
    """Raised when a tool call returns isError: true or a JSON-RPC error."""

    def __init__(self, message: str, code: str | None = None) -> None:
        super().__init__(message)
        self.message = message
        self.code = code

'''


def _emit_class(cd: ClassDef) -> str:
    lines = ["@dataclass", f"class {cd.name}:"]
    for fname, ftype, fdefault in cd.fields:
        if fdefault in ("None",):
            lines.append(f"    {fname}: {ftype} = None")
        elif fdefault.startswith("field("):
            lines.append(f"    {fname}: {ftype} = {fdefault}")
        else:
            lines.append(f"    {fname}: {ftype} = None")

    lines.append("")
    lines.append("    @classmethod")
    lines.append(f"    def from_dict(cls, d: dict[str, Any] | None) -> \"{cd.name} | None\":")
    lines.append("        if d is None:")
    lines.append("            return None")
    lines.append("        return cls(")
    for bl in cd.from_dict_body:
        lines.append(bl)
    lines.append("        )")
    lines.append("")
    return "\n".join(lines)


# Names added by _MODELS_COMPAT_FOOTER that should be re-exported from __init__.py.
# Keep in sync with the footer content below.
_COMPAT_EXPORTS: list[str] = [
    "RetractionStatus",
    "AuditBibliographySummary",
    # Root response aliases (short names)
    "SearchResponse",
    "ScrapeResult",
    "ArchiveResult",
    "AuditBibliographyResult",
    "VerifyResult",
    "SearchAndScrapeResult",
    # Item / nested aliases
    "SearchResult",
    "AcademicPaper",
    "ImageResult",
    "NewsArticle",
    "BibEntryAudit",
    "AuditSummary",
]

_MODELS_COMPAT_FOOTER = '''\


# ---------------------------------------------------------------------------
# Backward-compatible aliases — old names used by existing tests/code.
# The canonical names are the ones above (generated from the Go output schema).
# ---------------------------------------------------------------------------

# MCPError repr — include code so repr(err) shows the error code.
MCPError.__repr__ = lambda self: f"MCPError({self.message!r}, code={self.code!r})"


# RetractionStatus — the server exposes retractionStatus as a plain dict; this
# typed helper lets callers opt in to structured access without schema churn.
@dataclass
class RetractionStatus:
    """Typed view of a Crossref retraction record (plain dict in the server schema)."""

    retracted: Optional[bool] = None
    kind: Optional[str] = None
    date: Optional[str] = None
    noticeDoi: Optional[str] = None
    source: Optional[str] = None

    @classmethod
    def from_dict(cls, d: "dict[str, Any] | None") -> "RetractionStatus | None":
        if not d:
            return None
        return cls(
            retracted=d.get("retracted"),
            kind=d.get("kind"),
            date=d.get("date"),
            noticeDoi=d.get("noticeDoi"),
            source=d.get("source"),
        )


# Root response aliases (canonical: {ToolName}Response)
SearchResponse = WebSearchResponse
ScrapeResult = ScrapePageResponse
ArchiveResult = ArchiveSourceResponse
AuditBibliographyResult = AuditBibliographyResponse
VerifyResult = VerifyCitationResponse
SearchAndScrapeResult = SearchAndScrapeResponse

# AuditBibliographySummary — the audit_bibliography.summary schema has different
# fields from search_and_scrape.summary; define it explicitly to avoid dedup collision.
@dataclass
class AuditBibliographySummary:
    """Corpus-level counts from audit_bibliography."""

    total: int = 0
    retracted: int = 0
    deadLink: int = 0
    notFound: int = 0
    unchecked: int = 0
    mischaracterized: int = 0
    ok: int = 0
    claimCheckSkippedCount: int = 0
    thinContentCount: int = 0

    @classmethod
    def from_dict(cls, d: "dict[str, Any] | None") -> "AuditBibliographySummary":
        if not d:
            return cls()
        return cls(
            total=d.get("total") or 0,
            retracted=d.get("retracted") or 0,
            deadLink=d.get("deadLink") or 0,
            notFound=d.get("notFound") or 0,
            unchecked=d.get("unchecked") or 0,
            mischaracterized=d.get("mischaracterized") or 0,
            ok=d.get("ok") or 0,
            claimCheckSkippedCount=d.get("claimCheckSkippedCount") or 0,
            thinContentCount=d.get("thinContentCount") or 0,
        )


# Item / nested type aliases
SearchResult = WebSearchResult          # results[] item in web_search
AcademicPaper = AcademicSearchPaper     # papers[] item in academic_search
ImageResult = ImageSearchImage          # images[] item in image_search
NewsArticle = NewsSearchArticle         # articles[] item in news_search
BibEntryAudit = AuditBibliographyEntry  # entries[] item in audit_bibliography
AuditSummary = AuditBibliographySummary  # summary object in audit_bibliography
'''


def _emit_models(registry: ClassRegistry) -> str:
    parts = [_MODELS_HEADER]
    for cd in registry.all_classes():
        parts.append(_emit_class(cd))
    parts.append(_MODELS_COMPAT_FOOTER)
    return "\n".join(parts).rstrip() + "\n"


def _emit_init(tools: list[dict[str, Any]], registry: ClassRegistry) -> str:
    """Generate __init__.py re-exporting every public name from the package.

    Keeps the static docstring, shim import, and client import unchanged;
    rebuilds the models import block and __all__ from the registry and
    _COMPAT_EXPORTS so new tools appear automatically.
    """
    # All root *Response classes from the registry.
    root_classes = sorted(
        c.name for c in registry.all_classes()
        if c.name.endswith("Response")
    )
    # Key item/nested classes that are part of the public API (non-Response).
    # We expose every class whose name ends with a common item suffix, plus
    # well-known shared classes like Citation, MCPError.
    item_classes = sorted(
        c.name for c in registry.all_classes()
        if not c.name.endswith("Response")
        and c.name not in {"MCPError"}
    )
    # All names from the compat footer (aliases + manual dataclasses).
    compat = _COMPAT_EXPORTS

    # Build deduplicated, sorted import list.
    all_model_names = sorted(set(root_classes) | set(item_classes) | set(compat) | {"MCPError"})
    import_lines = "\n".join(f"    {n}," for n in all_model_names)

    all_entries = (
        ["__version__", "get_binary_path", "main",
         "WebResearcherClient", "SyncWebResearcherClient"]
        + all_model_names
    )
    all_lines = "\n".join(f'    "{n}",' for n in all_entries)

    return f'''\
"""web-researcher-mcp — typed Python SDK for web research, citation verification, and bibliography auditing.

CLI usage:
    uvx web-researcher-mcp          # MCP server over STDIO
    PORT=8080 uvx web-researcher-mcp # MCP server over HTTP

Python SDK usage:
    from web_researcher_mcp import WebResearcherClient

    async with WebResearcherClient() as client:
        results = await client.web_search("CRISPR off-target effects 2024")

    # Sync (no async/await needed):
    with WebResearcherClient.sync() as client:
        results = client.web_search("climate change 2024")
"""
# AUTO-GENERATED — do not edit by hand.
# Run: make gen-python-client
from __future__ import annotations

from web_researcher_mcp._shim import __version__, get_binary_path, main  # noqa: F401
from web_researcher_mcp.client import SyncWebResearcherClient, WebResearcherClient  # noqa: F401
from web_researcher_mcp.models import (  # noqa: F401
{import_lines}
)

__all__ = [
{all_lines}
]
'''


# Return type → (class name, import hint)  — used by the async client emitter.
def _resolve_return_type(tool_name: str, registry: ClassRegistry) -> str | None:
    """Return the Python class name for this tool's return type, or None (→ dict)."""
    candidate = _pascal(tool_name) + "Response"
    if candidate in registry._by_name:
        return candidate
    return None


# ---------------------------------------------------------------------------
# Per-tool method emitter
# ---------------------------------------------------------------------------

def _emit_async_method(tool: dict[str, Any], registry: ClassRegistry) -> str:
    name = tool["name"]
    desc = tool.get("description", "")
    # First sentence only for inline doc.
    short_desc = desc.split(".")[0].strip() if desc else name

    input_schema = tool.get("inputSchema") or {}
    sig_parts, body_parts = _build_method_params(name, input_schema)

    ret_class = _resolve_return_type(name, registry)
    ret_ann = ret_class if ret_class else "dict[str, Any]"

    sig_inner = ",\n        ".join(sig_parts)
    if sig_inner:
        sig_str = f"\n        {sig_inner},\n    "
    else:
        sig_str = ""

    body_inner = "\n".join(body_parts)

    if ret_class:
        return_expr = f"        return {ret_class}.from_dict(d)"
    else:
        return_expr = "        return d"

    return f"""\
    async def {name}(
        self,{sig_str}) -> {ret_ann}:
        \"\"\"{ short_desc }\"\"\"
        d = await self._call_tool(
            "{name}",
            {{
{body_inner}
            }},
        )
{return_expr}
"""


def _emit_sync_method(tool: dict[str, Any], registry: ClassRegistry) -> str:
    name = tool["name"]
    input_schema = tool.get("inputSchema") or {}
    sig_parts, _ = _build_method_params(name, input_schema)

    ret_class = _resolve_return_type(name, registry)
    ret_ann = ret_class if ret_class else "dict[str, Any]"

    # Build the forwarding call using the Python param names (py aliases included).
    # sig_parts is ["py_name: type = default", ...] so extract the name before ': '.
    forward_parts = []
    for sp in sig_parts:
        py_name = sp.split(":")[0].strip()
        forward_parts.append(f"            {py_name}={py_name},")

    sig_inner = ",\n        ".join(sig_parts)
    if sig_inner:
        sig_str = f"\n        {sig_inner},\n    "
    else:
        sig_str = ""

    forward_inner = "\n".join(forward_parts)

    return f"""\
    def {name}(
        self,{sig_str}) -> {ret_ann}:
        return self._run(
            self._async_client.{name}(
{forward_inner}
            )
        )
"""


# ---------------------------------------------------------------------------
# Full client.py emitter
# ---------------------------------------------------------------------------

_CLIENT_HEADER = '''\
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
'''


def _emit_client(tools: list[dict[str, Any]], registry: ClassRegistry) -> str:
    # Collect all return classes that exist in registry.
    import_classes = sorted(
        {_resolve_return_type(t["name"], registry) for t in tools if _resolve_return_type(t["name"], registry)}
        | {"MCPError"}
    )

    import_block = "\n".join(f"    {c}," for c in import_classes)

    parts = [_CLIENT_HEADER + import_block + "\n)\n\n"]

    parts.append('''\

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
        Seconds to wait for the binary\'s ``/health/live`` endpoint when
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
            except OSError:
                # Best-effort notification: swallow any connection-level failure
                # (URLError, HTTPError, timeout, connection refused all subclass
                # OSError) so it can never break the caller.
                pass

        await asyncio.to_thread(_fire)

    async def _initialize(self) -> None:
        await self._request(
            "initialize",
            {
                # Negotiate the current MCP protocol revision the server speaks
                # (unlocks the resource_link content type for large payloads).
                # The go-sdk server negotiates down for older peers.
                "protocolVersion": "2025-06-18",
                "capabilities": {},
                "clientInfo": {"name": "web-researcher-mcp-python", "version": "1.0"},
            },
        )
        await self._notify("notifications/initialized")

    async def _call_tool(self, name: str, arguments: dict[str, Any]) -> dict[str, Any]:
        result = await self._request("tools/call", {"name": name, "arguments": _strip_none(arguments)})
        if result is None:
            raise MCPError(f"Tool \'{name}\' returned a null result")
        is_error: bool = result.get("isError", False)
        content = result.get("content", [])
        text: str = ""
        for block in content:
            if block.get("type") == "text":
                text = block.get("text", "")
                break
        if not text:
            if is_error:
                raise MCPError(f"Tool \'{name}\' returned an error with no message")
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
    # Tool methods (generated from outputSchema)
    # ------------------------------------------------------------------

''')

    # Emit all async methods.
    for tool in tools:
        parts.append(_emit_async_method(tool, registry))

    parts.append('''\
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
                "Use \'with SyncWebResearcherClient() as c:\' or call start() first."
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

''')

    for tool in tools:
        parts.append(_emit_sync_method(tool, registry))

    return "".join(parts).rstrip() + "\n"


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def _diff(path: Path, new_content: str) -> str | None:
    """Return a unified diff string if *new_content* differs from *path*, else None."""
    import difflib
    old = path.read_text(encoding="utf-8") if path.exists() else ""
    if old == new_content:
        return None
    diff = difflib.unified_diff(
        old.splitlines(keepends=True),
        new_content.splitlines(keepends=True),
        fromfile=str(path),
        tofile=str(path) + " (generated)",
        n=3,
    )
    return "".join(diff)


def main() -> None:
    dry_run = "--dry-run" in sys.argv
    report = "--report" in sys.argv
    if "--help" in sys.argv:
        print(__doc__)
        sys.exit(0)

    try:
        tools_json = json.load(sys.stdin)
    except json.JSONDecodeError as exc:
        print(f"ERROR: could not parse JSON from stdin: {exc}", file=sys.stderr)
        sys.exit(1)

    if not tools_json:
        print("ERROR: no tools in input", file=sys.stderr)
        sys.exit(1)

    registry = ClassRegistry()
    walker = SchemaWalker(registry)

    # Pass 1: pre-register all root-level classes so that child schemas can
    # never steal a root's canonical name during collision escape (Rule 5).
    for tool in tools_json:
        tname = tool["name"]
        out = tool.get("outputSchema")
        if out and isinstance(out, dict) and "properties" in out:
            root_class = _pascal(tname) + "Response"
            registry.pre_register(root_class, out)

    # Pass 2: walk all output schemas to populate fields.
    for tool in tools_json:
        tname = tool["name"]
        out = tool.get("outputSchema")
        if out and isinstance(out, dict):
            root_class = _pascal(tname) + "Response"
            field_coerce = _OUTPUT_FIELD_COERCE.get(tname)
            walker.walk(out, root_class, tname, field_coerce=field_coerce)

    # Generate file contents.
    models_content = _emit_models(registry)
    client_content = _emit_client(tools_json, registry)
    init_content = _emit_init(tools_json, registry)

    repo_root = Path(__file__).parent.parent
    pkg = repo_root / "python" / "web_researcher_mcp"

    targets = {
        pkg / "models.py": models_content,
        pkg / "client.py": client_content,
        pkg / "__init__.py": init_content,
    }

    if dry_run or report:
        stale = False
        for path, content in targets.items():
            d = _diff(path, content)
            if d:
                stale = True
                if dry_run:
                    print(d)
                else:
                    print(f"STALE: {path.relative_to(repo_root)}")
            else:
                if report:
                    print(f"ok:    {path.relative_to(repo_root)}")
        if stale:
            if not dry_run:
                print("\nRun 'make gen-python-client' to regenerate.")
            sys.exit(1)
        return

    for path, content in targets.items():
        path.write_text(content, encoding="utf-8")
        print(f"wrote {path}")


if __name__ == "__main__":
    main()
