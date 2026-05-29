# Error Handling Enhancement Roadmap

Research-backed plan for making web-researcher-mcp the first MCP server with truly LLM-native error handling — where errors teach the AI client how to recover autonomously.

---

## Executive Summary

9 parallel research agents analyzed 96 sources (MCP spec, RFC 9457, Cloudflare, Google AIP-193, PALADIN paper, academic research on LLM self-correction, production agent frameworks, and the codebase itself) to validate and refine 10 proposed enhancements. The research surfaced a critical architectural constraint, a clear implementation sequence, and several "do NOT do this" warnings.

**Key constraint discovered:** The MCP spec and all major SDKs (TypeScript, Python/pydantic-ai) require `structuredContent` to be nil/absent when `isError: true`. Structured error metadata must be delivered as JSON within the text `Content` field — not in `structuredContent`.

**Recommended sequence:** Enhancements form a dependency chain. Implement in three phases to avoid rework.

---

## Phase 1: Foundation (Structured Errors + Partial Success + Cache Freshness)

These three are independent of each other and provide the base layer for everything else.

### 1A. Structured Error Responses

**What:** Embed machine-readable JSON alongside human-readable text in error responses.

**Format (dual-channel, backward-compatible):**
```
Rate limit exceeded for web_search (provider: google). Wait 60 seconds and retry, or try a different provider.

{"error":{"kind":"rate_limited","retryable":true,"retryAfterSeconds":60,"provider":"google","suggestedAction":"retry_after_delay"}}
```

**Evidence supporting this approach:**
- Cloudflare RFC 9457 agent errors cut token costs 98% vs unstructured (blog.cloudflare.com, March 2026)
- Pydantic-AI issue #5217: MCP's `isError` boolean is insufficient — agents retry non-retryable errors without structured metadata
- Anthropic Certified Architect guide recommends errorCategory + isRetryable fields
- Google AIP-193: "the decision about whether two errors are the same should be considered in terms of expected client action"
- MCP TypeScript SDK explicitly exempts isError from outputSchema validation

**Schema (minimal, append-only):**
```go
type ToolErrorMeta struct {
    Kind             string  `json:"kind"`             // rate_limited, auth_required, blocked, network, content_empty, browser_unavailable, validation, upstream_unavailable
    Retryable        bool    `json:"retryable"`
    RetryAfterSeconds *int   `json:"retryAfterSeconds,omitempty"`
    SuggestedAction  string  `json:"suggestedAction"`  // retry_after_delay, try_different_provider, check_api_key, inform_user, report_bug
    Provider         string  `json:"provider,omitempty"`
}
```

**SuggestedAction vocabulary** (closed set — LLM can branch deterministically):
| Action | When | LLM Should |
|--------|------|------------|
| `retry_after_delay` | Rate limited | Wait N seconds, call again |
| `try_different_provider` | Provider down/auth failed | Re-call with `provider` param changed |
| `check_api_key` | Auth error | Tell user to verify their API key config |
| `broaden_query` | Zero results, query too specific | Remove filters or use broader terms |
| `inform_user` | Auth wall, permanent block | Tell user this content is inaccessible |
| `report_bug` | Content/browser issues the MCP could fix | Suggest filing a GitHub issue |

**Risks & mitigations:**
- Risk: Pydantic-AI blindly retries isError responses → mitigation: first line remains natural language; structured JSON is a bonus
- Risk: Schema drift becomes breaking → mitigation: start minimal, only add fields
- Risk: JSON delimiter parsing → mitigation: separator is a blank line; JSON always starts with `{"error":`

**Effort:** Medium (~100 lines). Refactor `toolError()`, `rateLimitError()`, `upstreamErrorResponse()`, `scrapeErrorResponse()`.

---

### 1B. Partial Success Status

**What:** Add a top-level `status` field to `search_and_scrape` output.

**Values:** `"complete"` | `"partial"` | `"failed"`

**Evidence:**
- AWS/Strands agents research: ambiguous completion signals cause 7x more tool calls (infinite retry loops)
- SERF paper (arXiv:2603.13417): structured error semantics is one of three missing MCP primitives
- Elasticsearch bulk API: top-level `errors: bool` enables fast triage without array scanning
- GraphQL: returns `data` + `errors` simultaneously with a `dataState` enum
- MCP spec: NO partial-success mechanism exists; must be in structured output (isError stays false for partial)

**Implementation:**
```go
// In buildSources(), after computing failures:
status := "complete"
if scraped == 0 && len(failures) > 0 {
    status = "failed"
} else if len(failures) > 0 {
    status = "partial"
}
output["status"] = status
```

Extend `scrapeFailureOutput` with `Retryable bool` and `SuggestedAction string` per failure.

**Critical design decision:** The response MUST be explicitly terminal — the LLM must understand "this operation is done, here is what you have." Without this, LLMs retry identical queries hoping for better results.

**Effort:** Low (~40 lines in searchandscrape.go + schema update).

---

### 1C. Cache Freshness Signals

**What:** Add `_meta` fields to cached responses so LLMs can assess data staleness.

**Format (in MCP `_meta` field, invisible to LLM but available to framework):**
```json
{"cached": true, "ageSeconds": 780, "maxAgeSeconds": 900, "freshness": "expiring"}
```

**Evidence:**
- MCP spec explicitly supports `_meta` for response metadata (go-sdk CallToolResult.Meta field)
- HTTP Age/max-age ratio is the universal cache freshness signal (RFC 9111)
- Research shows LLMs cannot detect data staleness without explicit signals
- Anthropic context engineering: every token is expensive; `_meta` avoids context pollution

**Freshness labels:**
- `"fresh"` — under 50% TTL consumed
- `"aging"` — 50-80% TTL consumed  
- `"expiring"` — over 80% TTL consumed

**Key insight:** LLMs reason better with semantic labels ("expiring") than raw arithmetic (780/900). Include both for framework-level and LLM-level consumption.

**Implementation requires:**
1. Extend Cache interface with `GetWithMeta()` returning `CacheEntry{StoredAt, TTL}`
2. Add `cachedResultWithMeta()` helper alongside existing `structuredResult()`
3. Apply to all cache-hit paths

**Effort:** Medium (~80 lines across cache + tools). No output schema changes needed since `_meta` is transport-layer.

---

## Phase 2: Intelligence (Zero-Result Hints + Recovery Context + Diagnostic Tiering)

Built on Phase 1's structured error foundation.

### 2A. Zero-Result Hints + Provider Capabilities

**What:** When `resultCount == 0`, return a `hints` object explaining why and suggesting what to try.

**Structure:**
```json
{
  "resultCount": 0,
  "hints": {
    "reason": "coverage_miss",
    "providersAttempted": ["uspto"],
    "providerCoverage": {"regions": ["US"], "capabilities": ["search", "biblio"]},
    "suggestedActions": [
      {"action": "switch_provider", "value": "epo", "reason": "covers all regions"},
      {"action": "remove_filter", "parameter": "patent_office"}
    ]
  }
}
```

**Reason taxonomy:**
| Reason | Trigger | Typical Suggestion |
|--------|---------|-------------------|
| `no_match` | Provider searched successfully, 0 results | Broaden query, remove filters |
| `coverage_miss` | Provider doesn't cover this region/type | Switch provider |
| `filters_too_restrictive` | Year range or source filter eliminated all results | Remove the narrowest filter |
| `rate_limited` | Provider throttled before returning results | Wait, try different provider |
| `provider_error` | Upstream returned an error | Try different provider |

**Evidence:**
- Google/Bing zero-results UX: "did you mean" + "try fewer keywords" — adapted for LLMs
- Elasticsearch _explain API: structured breakdown of why results didn't match
- Research: structured "why no results" context leads to better query reformulation than generic advice
- Existing ProviderMeta struct already contains regions/capabilities — just needs exposure

**Cross-dependency:** Uses Phase 1A's structured error schema for consistency. When hints suggest `switch_provider`, the suggestion format matches 1A's `suggestedAction` vocabulary.

**Effort:** Medium (~120 lines across patent.go, academic.go, search.go + schema updates).

---

### 2B. Recovery Hints (NOT Full Suggested Actions)

**What:** Provide contextual recovery metadata — NOT pre-built tool call specifications.

**Key research finding:** Full pre-built suggested actions are dangerous:
- **Security risk (AARM paper, arXiv:2602.09433):** Malicious upstream could craft errors suggesting data-exfiltrating tool calls
- **Agency loss (PALADIN paper):** LLMs blindly follow structured suggestions without contextual reasoning
- **Context gap:** Server lacks conversation context needed for truly informed suggestions
- **Coupling:** Full tool call specs in errors create maintenance burden when tool schemas change

**Instead, provide structured context for the LLM to reason about:**
```json
{"error": {"kind": "blocked", "provider": "google", "recovery": {"alternatives": ["brave", "serper"], "retryable": true, "suggestion": "Site uses bot detection. Try a different search provider or check if a cached version exists."}}}
```

**Design principle (Google AIP-193):** Include `availableAlternatives` as metadata — let the LLM pick. Don't prescribe.

**When alternatives ARE listed:** Only include providers that are:
1. Actually configured (in `deps.SearchProviders` map)
2. Currently healthy (circuit breaker not open)
3. Capable of the requested operation

**Effort:** Low (~50 lines). Extends Phase 1A's error format with an optional `recovery` object.

---

### 2C. Diagnostic Detail Tiering (Progressive Disclosure)

**What:** Keep error messages compact by default; offer full diagnostics on demand via MCP Resource.

**Evidence:**
- Anthropic context engineering: models exhibit "context rot" — accuracy decreases as context grows
- Memory condensation research (arXiv:2605.18854): compact-at-source is better than verbose-then-compress
- Solo.io agentgateway: progressive disclosure achieved 91% token reduction
- Information overload research: excess information degrades decision quality in both humans and AI agents

**Three layers:**
1. **Compact actionable error** (always in tool result) — under 150 chars: kind + cause + action
2. **Error code enum** (in structured JSON) — enables programmatic branching
3. **Full diagnostics via MCP Resource** (on demand) — `diagnostics://errors/recent`

**Current state vs. proposed:**
| | Current | Proposed |
|---|---------|----------|
| Rate limit | "The search service is temporarily busy: searxng error 429. Please wait about 60 seconds..." (118 chars) | "Rate limited (searxng). Retry in 60s or try different provider." (63 chars) + JSON |
| Scrape blocked | "Scrape failed for https://x.com/...: access was blocked (HTTP 403). The site may use bot detection..." (200+ chars) | "Blocked: x.com (bot detection). Try alternative source." (55 chars) + JSON |

**The Resource pattern:**
```
diagnostics://errors/recent  →  Last 50 errors with full tier breakdown, timestamps, config state
```

LLM reads this only when actively debugging with the user. Follows existing `stats://tools` pattern.

**Effort:** Medium (Phase 1: shorten messages ~40 lines; Phase 2: add Resource ~100 lines).

---

## Phase 3: Adaptive (Session Aggregation + Error Deduplication)

Depends on Phase 1A (structured errors) and Phase 2 working well in production.

### 3A. Error Deduplication (Negative Cache)

**What:** Track seen errors in cache with short TTLs. On repeat occurrence, return compact response without redundant GitHub issue suggestions.

**Implementation:** Reuse existing Cache interface:
```go
key := "neg:" + domain + ":" + kindName
// First occurrence: full verbose error + issue URL
// Repeat (cache hit): compact error + {previouslySeen: true, firstSeenAt, occurrenceCount, errorId}
```

**TTLs by error kind:**
| Kind | TTL | Rationale |
|------|-----|-----------|
| Blocked/Auth | 30 min | Unlikely to change quickly |
| Rate Limited | 90 sec | Matches retry guidance |
| Network | 2 min | Transient |
| Browser | Session lifetime | Chrome won't appear mid-session |
| Content | 10 min | Page structure rarely changes fast |

**Critical rule:** Never suppress errors (still return `isError: true`). Dedup signal is metadata ON the error, not elimination of it.

**Effort:** Low-medium (~60 lines). Reuses existing cache, no new packages.

---

### 3B. Session Error Aggregation

**What:** Track error patterns across a multi-step research session. Surface in `get_research_session`.

**When to surface:** Only when count >= 3 of same ErrorKind.

**Output format (in get_research_session):**
```json
{
  "errorPatterns": [
    {"kind": "auth_required", "count": 4, "affectedUrls": ["url1","url2","..."], "suggestion": "Consider open_access=true or target preprint servers", "lastSeen": "2026-05-28T10:15:00Z"}
  ],
  "providerStats": {"brave": {"attempts": 5, "successes": 5}, "searxng": {"attempts": 4, "successes": 2}}
}
```

**Evidence:**
- Skill-RAG paper: proactive failure detection reduced cascading failures by 40%
- LangSmith/Langfuse: production standard is per-session error pattern surfacing
- Multi-agent failure research (arXiv:2503.13657): "failure propagation" is the dominant failure mode — early detection is key

**Suggestion map (ErrorKind → remediation):**
| Kind | Session-Level Suggestion |
|------|------------------------|
| auth_required | "Consider open_access=true or target preprint servers (arxiv, biorxiv)" |
| blocked | "Try alternative sources or use web_search for cached versions" |
| rate_limited | "Switch to a different provider or space requests further apart" |
| browser_unavailable | "Set CHROME_PATH for JavaScript-heavy sites" |

**Effort:** Medium (~200 lines across session/types.go, session/manager.go, tools/sequential.go, tools/getsession.go).

---

## Phase 4: Observability (Provider Health Resource)

### 4A. Circuit Breaker Visibility via MCP Resource

**What:** Expose provider health as a read-only MCP Resource. Do NOT embed in tool results.

**Key research finding (overwhelming consensus):** Circuit breaker state should NOT be in tool response content:
- Anthropic: "information that does not help the LLM accomplish the task wastes tokens"
- Envoy pattern: binary header (degraded yes/no), detailed state in observability plane
- Security (CNCF TAG-Security): exposing per-provider health reveals internal topology
- The Router already handles fallback transparently — exposing state undermines the abstraction

**Instead:**
1. `stats://providers` MCP Resource (operator/debug use)
2. `_meta.served_by` on successful responses (invisible to LLM, available to client apps)
3. Abstract error messages ("service temporarily unavailable") rather than infrastructure detail

**Effort:** Low (~60 lines). Extends existing `stats://tools` Resource pattern.

---

## Cross-Dependency Map

```
Phase 1A (Structured Errors) ─────┐
                                   ├── Phase 2A (Zero-Result Hints) uses same schema
Phase 1B (Partial Success) ───────┤
                                   ├── Phase 2B (Recovery Hints) extends error format
Phase 1C (Cache Freshness) ───────┘
                                   ├── Phase 2C (Diagnostic Tiering) shortens messages from 1A
                                   │
                                   ├── Phase 3A (Error Dedup) requires 1A's stable error codes
                                   │
                                   └── Phase 3B (Session Aggregation) requires 1A's error taxonomy
                                        └── Phase 4A (Provider Resource) independent, can start anytime
```

---

## What NOT to Do (Research-Validated Warnings)

1. **Do NOT put structured errors in `structuredContent`** — MCP SDK validation rejects this on `isError: true` responses. Test `TestStructuredContentAbsentOnError` correctly enforces this.

2. **Do NOT include full pre-built tool call suggestions** — security risk (AARM paper), agency loss (PALADIN), coupling, and context-gap problems outweigh benefits.

3. **Do NOT expose circuit breaker state in tool responses** — the Router's job IS to abstract this away; exposing it undermines the abstraction and wastes tokens.

4. **Do NOT add a `verbose_errors` config toggle** — progressive disclosure adapts to context; static config cannot.

5. **Do NOT declare 'patterns' from fewer than 3 errors** — small samples produce false positives that mislead the LLM.

6. **Do NOT suppress errors on dedup** — always return `isError: true`; dedup is metadata, not suppression.

7. **Do NOT suggest providers that aren't configured or healthy** — verify against `deps.SearchProviders` map and circuit breaker state before including in alternatives.

---

## Implementation Priority

| Enhancement | Phase | Effort | LLM Impact | Risk |
|------------|-------|--------|------------|------|
| Structured error JSON | 1A | Medium | Highest | Low (backward-compatible) |
| Partial success status | 1B | Low | High | Very low (additive field) |
| Cache freshness _meta | 1C | Medium | Medium-high | Very low (uses _meta) |
| Zero-result hints | 2A | Medium | High | Low |
| Recovery hints | 2B | Low | Medium-high | Low |
| Diagnostic tiering | 2C | Medium | Medium | Low |
| Error deduplication | 3A | Low-medium | Medium | Low |
| Session aggregation | 3B | Medium | Medium | Medium (state complexity) |
| Provider health resource | 4A | Low | Low (intentionally) | Very low |

**Total estimated effort:** ~700-900 lines of new/modified code across all phases.

---

## Source References

| Source | Key Insight |
|--------|------------|
| MCP Spec (2025-06-18) | isError responses exempt from outputSchema; errors go in Content text |
| Cloudflare RFC 9457 for agents | Structured errors cut token costs 98%; retryable/retry_after/category fields |
| Pydantic-AI #5217 | MCP's binary isError causes blind retries; needs retryable distinction |
| Google AIP-193 | "Same error = same expected client action" — principle for error taxonomy |
| PALADIN (ICLR 2026) | LLMs recover better from context than prescriptive suggestions |
| AARM (arXiv:2602.09433) | Suggested actions can be weaponized as indirect prompt injection |
| AWS/Strands agents | Ambiguous completion signals cause 7x reasoning loops |
| SERF (arXiv:2603.13417) | Structured error semantics is a missing MCP primitive |
| Anthropic context engineering | Treat every token as expensive; curate aggressively |
| Solo.io agentgateway | Progressive disclosure = 91% token reduction |
| Skill-RAG (arXiv:2604.15771) | Proactive failure detection reduces cascading failures 40% |
