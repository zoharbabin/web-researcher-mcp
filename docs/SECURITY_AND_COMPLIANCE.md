# Security, Privacy & Compliance

How this project protects your data, your infrastructure, and your users.

This document covers security architecture, privacy principles, compliance
posture, contributor guidelines, and deployment guidance — everything in one
place, for everyone who needs it.

---

## Who This Is For

| You are a... | Start with |
|-------------|-----------|
| User evaluating the tool | [Principles](#principles), [What We Protect Against](#what-we-protect-against) |
| Administrator deploying it | [Deployment Security](#deployment-security), [Configuration Reference](#configuration-reference) |
| Developer contributing code | [Contributor Security Rules](#contributor-security-rules), [AI Agent Coding Rules](#ai-agent-coding-rules) |
| Compliance officer | [Compliance Posture](#compliance-posture), [Standards Alignment](#standards-alignment) |

---

## Principles

Six rules that govern every design decision:

1. **Secure by default, permissive by configuration.**  
   The tool ships safe out of the box. Advanced users unlock capabilities
   through explicit configuration — never the other way around.

2. **Never limit research capabilities for security theater.**  
   Security controls must protect without reducing the quality, speed, or
   breadth of results. If a control blocks legitimate research, it becomes
   configurable — not mandatory.

3. **Compliance through architecture, not bolt-on checklists.**  
   Data minimization, purpose limitation, and tenant isolation are structural
   properties of the codebase, not afterthoughts added for an audit.

4. **STDIO is zero-trust-by-default.**  
   The most common deployment (local CLI via Claude Code, Cursor, etc.) has
   no network listener, no auth to misconfigure, and no attack surface.
   Multi-tenant security features only activate in HTTP mode.

5. **Simple things should be simple; complex things should be possible.**  
   A developer running `go build && ./web-researcher-mcp` should not need
   to configure OAuth, encryption keys, or compliance profiles. An enterprise
   deploying for 10,000 users should have everything they need.

6. **Elegant code is secure code.**  
   Short, readable, well-tested functions are easier to audit and harder to
   exploit than clever abstractions. Prefer Go's standard library over third-
   party dependencies. One clear implementation over pluggable frameworks.

---

## What We Protect Against

This server operates in a unique threat environment:

| Threat | Severity | Defense |
|--------|----------|---------|
| SSRF (Server-Side Request Forgery) | Critical | Custom `DialContext` validating all resolved IPs against 19 CIDR blocks + DNS rebinding prevention |
| Prompt injection via scraped content | High | Content sanitization, size limits, boundary marking, `contentType` signaling |
| Cost abuse via API key theft | High | Rate limiting (global + per-tenant + daily quota), circuit breakers |
| Cross-tenant data leakage | High | Namespace isolation, per-tenant sessions, configurable cache isolation |
| Denial of Service | High | HTTP timeouts, request size limits, bounded concurrency (semaphore) |
| Cloud metadata credential theft | High | Hostname and IP blocklist for IMDS endpoints (AWS, GCP, Azure, etc.) |
| Supply chain compromise | Medium | cosign-signed binaries, SBOM, CodeQL, govulncheck, dependency pinning |
| Unauthorized access (HTTP mode) | Medium | OAuth 2.1 JWT validation, JWKS auto-refresh, token revocation |

---

## Architecture at a Glance

```
User/LLM → [STDIO or HTTP+TLS] → MCP Server
                                      │
                ┌─────────────────────┴──────────────────────┐
                │                                             │
          Authentication              Tool Handler
          (JWT/JWKS if HTTP)          (search, scrape, etc.)
                │                          │
          Rate Limiting               Input Validation
          (token bucket)              (URL scheme, body size)
                │                          │
          Audit Logging               SSRF Protection
          (async, structured)         (IP validation, DNS pin)
                │                          │
          Tenant Isolation            Content Sanitization
          (per-session, cache)        (bluemonday, size limits)
                │                          │
          Metrics                     Circuit Breaker
          (Prometheus)                (per-provider)
                │                          │
                └──────────┬───────────────┘
                           │
                    Encrypted Cache
                    (AES-256-GCM, TTL)
```

All components are wired through the `tools.Dependencies` struct — zero global
state, fully testable, every dependency swappable via interfaces.

---

## Defense Layers (Technical Detail)

### SSRF Protection

The highest-severity risk for a scraping server. Our defense:

1. Check hostname against blocklist (cloud IMDS endpoints)
2. Resolve DNS
3. Validate ALL resolved IPs against private/reserved ranges
4. Connect directly to the resolved IP (prevents DNS rebinding)
5. Re-validate on each redirect hop (max 5 redirects)

Blocked ranges: RFC 1918, link-local (169.254.0.0/16), loopback, multicast,
carrier-grade NAT, documentation ranges, IPv6 equivalents. See
`internal/scraper/ssrf.go` for the canonical list.

Escape hatch: `ALLOW_PRIVATE_IPS=true` for development/intranet scraping.
Domain allowlist: `ALLOWED_DOMAINS=a.com,b.com` for enterprise lock-down.

### Content Security

Scraped content passes through a configurable sanitization pipeline:

1. **Default mode**: HTML sanitization (bluemonday allowlist — strips scripts,
   iframes, event handlers), hidden content removal (display:none, zero-width
   chars), size enforcement (50KB per source, 300KB total)
2. **Raw mode** (`mode: "raw"`, `scrape_page` only): Returns the fetched bytes
   verbatim — scripts, styles, and markup intact — for inspecting source such as
   JSON, HTML, or JavaScript (code analysis, security research, web development).
   Only `content.Process` is skipped; the request still passes through
   `validateScrapeURL`, the SSRF-safe client, the `ALLOWED_DOMAINS` allowlist,
   and an `io.LimitReader` bounded by `max_length`. The returned content is
   UNTRUSTED (it may carry indirect prompt-injection payloads), so callers must
   never execute or render it. `search_and_scrape` has no raw mode and is always
   sanitized.
3. **Size limits**: Configurable per-request via `maxLength` parameter. The LLM
   decides how much content it needs based on context window and task.
   Defaults protect against context flooding; explicit overrides serve
   legitimate research needs (analyzing large codebases, full-page audits).
4. **Structured output**: in sanitized modes the `contentType` field reflects
   the extracted form (e.g. `text/markdown`); in raw mode it carries the
   server's real `Content-Type` header (which may be empty) and the output sets
   `"raw": true` — downstream consumers always know what they are handling.

The pipeline defends against prompt injection and context flooding by default,
while allowing full access to page source when the research task requires it.
SSRF protection applies regardless of mode — the security boundary is what
URLs you can reach, not what content you can read from them.

### Authentication (HTTP mode)

OAuth 2.1 Resource Server pattern:

- RS256 JWT signature verification via JWKS endpoint
- Auto-refreshing key cache (configurable interval, default 1 hour)
- Audience and issuer validation
- Token revocation via JTI — in-memory by default, optionally backed by the
  encrypted `persist.Store` so revocations survive restarts (fail-closed: a JTI
  is revoked if present in either layer)
- Scope-based per-tool authorization (opt-in via `ENFORCE_SCOPES`; see below)
- Constant-time comparison for admin key authentication
- Rejects HS256 from external issuers (algorithm confusion prevention)

**Scope-based authorization (RBAC).** When `ENFORCE_SCOPES=true`, a token that
carries a `scope`/`scp` claim must hold one of `tool:*`, `tool:<name>`, or the
coarse `research` scope to invoke a tool (plus any `REQUIRED_SCOPES`). It is
permissive by design: `ENFORCE_SCOPES=false` (default) ignores scopes, and a
token with no scope claim is always allowed (backward-compatible). It fails
closed only on a present-but-insufficient scope. See `Middleware.EnforceScopes`
in `internal/auth/middleware.go` and the crosswalk in `docs/SECURITY.md`.

**Session identifiers (accepted risk).** Sequential-search session IDs are
static UUIDv4 values that do not rotate over the life of a session. This is an
accepted, documented risk: a session ID is a research-continuity handle, not an
authentication credential. Authorization is always derived from the validated
JWT (tenant/user/scope), and sessions are keyed by the compound
`{tenantID}:{sessionID}` so a guessed or leaked session ID cannot cross a tenant
boundary or grant access without a valid token. Rotation was deliberately not
added to avoid breaking the recovery-after-context-loss workflow the IDs exist
to support.

STDIO mode: no authentication (the calling process is the trust boundary).

### Rate Limiting (HTTP mode)

Three tiers preventing abuse while allowing legitimate high-throughput research:

| Tier | Default | Purpose |
|------|---------|---------|
| Global | 1000 req/s | Infrastructure protection |
| Per-tenant | 120 req/min | Fair use between tenants |
| Daily quota | 5000 req/day | Cost control |

All configurable. Returns 429 with `Retry-After` header.

### Encryption

- **At rest:** AES-256-GCM with random nonces for cache and sessions (when
  `CACHE_ENCRYPTION_KEY` is set). File permissions 0600.
- **In transit:** TLS 1.2+ for all outbound connections (Go stdlib default).
  HSTS header for inbound HTTP.
- **FIPS option:** Build with `GOEXPERIMENT=boringcrypto` for FIPS 140-2
  validated cryptographic module.

### Audit Logging

Every tool invocation produces a structured JSON event:

```json
{
  "timestamp": "2026-01-15T10:30:00Z",
  "event_type": "tool_call",
  "tenant_id": "acme-corp",
  "user_id": "user@example.com",
  "tool_name": "web_search",
  "request_id": "550e8400-e29b-41d4-a716-446655440000",
  "duration_ms": 342,
  "success": true
}
```

Design: async channel-based (never blocks tool calls), swap-to-disk overflow
(never drops events under normal load), configurable output (stderr or file).

What is NOT logged by default: raw query text (a length/hash is recorded
instead, unless `AUDIT_INCLUDE_REQUEST_BODY=true`), scraped content, and full
request parameters (PII risk). Audit metadata and upstream error messages pass
through `audit.MaskSecrets` so any credential echoed back by a provider is
redacted before it reaches a sink. Audit files rotate at `AUDIT_MAX_BYTES` and
are pruned after `AUDIT_RETENTION_DAYS` (default 180, clamped to `[180, 3650]`).

---

## Privacy by Design

### Data Minimization

We deliberately minimize what we store:

| Data type | Storage | Retention | Contains PII? |
|-----------|---------|-----------|---------------|
| Cached search results | Keyed by query hash (not user ID) | TTL-based (hours) | Rarely |
| Cached scraped pages | Keyed by URL hash | TTL-based (hours) | Possibly |
| Research sessions | Per-tenant:session compound key | Session TTL (default 4h) | Query context |
| Audit logs | Structured JSON | Configurable (default 180 days) | User/tenant IDs only |
| Rate limit counters | Per-tenant in memory | Resets daily | Tenant ID only |

### Purpose Limitation

Each feature declares its data processing purpose explicitly. Core principles:

- **Primary purpose**: always the user-facing function (search, scrape, analyze)
- **Legitimate secondary purposes** (when enabled): usage analytics for the
  user's own benefit, research session memory, trend insights — each opt-in
  and clearly disclosed
- **Never allowed**: selling data to third parties, training external models,
  undisclosed advertising, sharing across tenants without consent

When features use data for purposes beyond the immediate request (e.g., a
search analytics dashboard showing trends over time), they are built with:
informed activation, transparent data flows, configurable retention, and
user-controlled deletion.

### Right to Erasure

For HTTP multi-tenant deployments: admin endpoints allow per-tenant data
purging (sessions, cache entries, rate state, analytics). For STDIO: data is
local to your machine — delete the cache directory. Any feature that stores
data beyond the request lifecycle provides a deletion mechanism.

### User Insights Without Surveillance

The project may offer features that help users understand their own research
patterns (search trends, topic analysis, session history). These are designed
as **user-owned insights, not profiling**:

- Data belongs to the user/tenant and is never shared across boundaries
- The user can view, export, and delete their own data at any time
- No behavioral predictions, scoring, or automated decisions about users
- No data leaves the user's control without explicit action
- STDIO mode stores everything locally — the user controls their own disk
- HTTP mode scopes all analytics to the authenticated tenant

The distinction: showing a user "here's what you searched for last month" is
a productivity feature. Building a hidden model of user behavior to influence
results without disclosure is profiling. We do the former, never the latter.

---

## Deployment Security

### STDIO Mode (default, zero-config)

No network listener. No attack surface. The binary communicates via stdin/stdout
with the calling process. Security is provided by your OS (file permissions,
process isolation).

Suitable for: individual developers, local AI assistants, testing.

### HTTP Mode (multi-tenant)

Activated by setting `PORT`. Enables OAuth, rate limiting, CORS, and all
multi-tenant security features.

**Minimum secure configuration:**

```bash
PORT=3000
OAUTH_ISSUER_URL=https://your-idp.example.com
OAUTH_AUDIENCE=https://your-api.example.com
ALLOWED_ORIGINS=https://your-app.example.com
CACHE_ENCRYPTION_KEY=$(openssl rand -hex 32)
ADMIN_API_KEY=$(openssl rand -hex 32)
AUDIT_ENABLED=true
AUDIT_OUTPUT_PATH=/var/log/web-researcher-mcp/audit.jsonl
```

**Production hardening (recommended):**

```bash
CACHE_ISOLATION=tenant
RATE_LIMIT_PER_TENANT=120
DAILY_QUOTA_PER_TENANT=5000
ALLOWED_DOMAINS=                    # empty = unrestricted (or set for lock-down)
MAX_SCRAPE_CONCURRENCY=5
```

**Healthcare deployments (HIPAA):**

If the MCP server processes or caches content containing Protected Health
Information (PHI), additional configuration is required:

```bash
CACHE_ENCRYPTION_KEY=$(openssl rand -hex 32)   # Encryption renders breach non-reportable (Safe Harbor)
CACHE_ISOLATION=tenant                          # Strict data isolation between tenants
AUDIT_ENABLED=true                              # HIPAA requires audit controls (45 CFR 164.312(b))
AUDIT_OUTPUT_PATH=/var/log/audit.jsonl          # Retain for 6 years (HIPAA retention requirement)
```

A Business Associate Agreement (BAA) is required between the entity deploying
this server and any covered entity whose data it processes. The server's
encryption at rest (AES-256-GCM), encryption in transit (TLS 1.2+), access
controls (OAuth + tenant isolation), and audit logging satisfy HIPAA Technical
Safeguards (45 CFR 164.312). The FIPS build option (`GOEXPERIMENT=boringcrypto`)
satisfies NIST SP 800-111 requirements referenced by HIPAA.

### Container Security

The Docker image ships with full rendering capabilities:

- Non-root execution (UID 65534)
- Chromium for headless page rendering (JS-heavy sites, SPAs)
- Alpine base with ca-certificates and required runtime libraries
- Multi-stage build (no build tools in runtime image)
- All images signed with cosign (verify with `cosign verify`)

The image includes Chromium because accurate research requires rendering
JavaScript-heavy pages. The browser runs sandboxed (`--no-sandbox` is NOT
used — we rely on the container's own isolation). Chromium's attack surface
is mitigated by: non-root execution, bounded concurrency (one browser
instance at a time via mutex), SSRF protection on all URLs before they reach
the browser, and container-level network restrictions.

Deployment recommendations:

- Mount cache directory as tmpfs or encrypted volume
- Apply network policies limiting egress to search API endpoints + Chromium
  download endpoints (for auto-update if enabled)
- Consider seccomp profile (see `deploy/` directory when available)
- For environments where Chromium is not desired: set `CHROME_PATH=disabled`
  to skip browser-tier scraping entirely

---

## Configuration Reference

### Security Controls

| Env Var | Default | Effect |
|---------|---------|--------|
| `CACHE_ENCRYPTION_KEY` | (unset = plaintext) | 64-char hex key enables AES-256-GCM disk encryption |
| `CACHE_ENCRYPTION_KEY_PREV` | (unset) | 64-char hex previous key for zero-downtime rotation (decrypt fallback + lazy re-encrypt) |
| `ALLOW_PRIVATE_IPS` | `false` | When `true`, allows scraping private/RFC1918 IPs |
| `ALLOWED_DOMAINS` | (unset = all) | Comma-separated allowlist restricts scraping targets |
| `ALLOWED_ORIGINS` | (unset) | CORS allowlist for HTTP mode (browser-only). Empty behavior depends on `CORS_STRICT` |
| `CORS_STRICT` | `true` | Default fail-closed: empty `ALLOWED_ORIGINS` denies all cross-origin browser requests. Set `false` to restore the legacy reflect-any-Origin behavior (see `docs/MIGRATION.md`) |
| `ENFORCE_SCOPES` | `false` | When `true`, scoped tokens need `tool:*`/`tool:<name>`/`research` per tool (permissive for unscoped tokens) |
| `REQUIRED_SCOPES` | (unset) | CSV of scopes every request must carry when `ENFORCE_SCOPES=true` |
| `CACHE_ISOLATION` | `shared` | Set `tenant` for per-tenant cache isolation |
| `ADMIN_API_KEY` | (unset = no admin API) | Enables all `/admin/*` endpoints when set (min 16 chars); sent as `X-Admin-Key`, constant-time compared |
| `CACHE_ADMIN_KEY` | (unset) | **Deprecated** alias for `ADMIN_API_KEY` (still accepted; logs a startup warning) |
| `RATE_LIMIT_PER_IP` | `0` (disabled) | Pre-auth per-IP request ceiling (req/min); `TRUST_PROXY` selects the client-IP source |

### Authentication (HTTP mode only)

| Env Var | Default | Effect |
|---------|---------|--------|
| `OAUTH_ISSUER_URL` | (unset = no auth) | JWT issuer for JWKS discovery |
| `OAUTH_AUDIENCE` | (unset) | Expected JWT audience claim |
| `JWKS_REFRESH_INTERVAL` | `1h` | Background JWKS key refresh |

### Rate Limiting (HTTP mode only)

| Env Var | Default | Effect |
|---------|---------|--------|
| `RATE_LIMIT_GLOBAL` | `1000` | Global requests/second |
| `RATE_LIMIT_PER_TENANT` | `120` | Per-tenant requests/minute |
| `DAILY_QUOTA_PER_TENANT` | `5000` | Per-tenant requests/day |

### Audit & Observability

| Env Var | Default | Effect |
|---------|---------|--------|
| `AUDIT_ENABLED` | `true` | Structured audit logging |
| `AUDIT_OUTPUT_PATH` | (unset = stderr) | File path for audit JSONL |
| `AUDIT_BUFFER_SIZE` | `1000` | Async channel buffer size |
| `LOG_LEVEL` | `info` | Log verbosity (debug/info/warn/error) |
| `METRICS_ENABLED` | `true` | Prometheus metrics on `/metrics` |

Full configuration reference: see `.env.example`.

---

## Compliance Posture

> **Prefer slides?** The same story — how architecture, not paperwork, keeps
> this project aligned with 23 standards — is captured in a short visual deck:
> **[Compliance as Architecture](https://zoharbabin.github.io/web-researcher-mcp/decks/compliance/)**
> ([PDF](https://zoharbabin.github.io/web-researcher-mcp/decks/compliance/compliance-deck.pdf) ·
> [source](https://github.com/zoharbabin/web-researcher-mcp/blob/main/decks/compliance/compliance-deck.md)).
> Each technical claim names the file that backs it — open any one and check.

### What We Target

This project is designed to satisfy security and privacy requirements across
multiple international frameworks simultaneously. We achieve this through
architectural choices that inherently satisfy shared requirements across
standards — not through per-framework checkbox exercises.

### Standards Alignment

| Standard | Relevance | How we align |
|----------|-----------|-------------|
| **ISO 27001** | Foundation | Interface-driven architecture, access control, encryption, audit, supply chain |
| **SOC 2 Type II** | Enterprise trust | Audit logging, rate limiting, tenant isolation, change management |
| **NIST CSF 2.0** | US enterprise | Govern/Identify/Protect/Detect/Respond/Recover mapped to controls |
| **GDPR / UK GDPR** | Privacy | Data minimization, purpose limitation, TTL caches; data-subject access/portability/erasure endpoints (`/admin/data`); consent record-verify-honor for regulated features |
| **OWASP MCP Cheat Sheet** | MCP-specific | SSRF protection, content sanitization, tool annotations, supply chain |
| **OWASP Top 10 LLM (2025)** | AI security | Prompt injection defense, bounded agency, supply chain verification |
| **OWASP Agentic Top 10 (2026)** | AI agent security | Read-only tools, privilege separation, content boundaries |
| **NIST AI RMF** | AI governance | Risk-aware design, transparency, continuous monitoring |
| **EU AI Act** | EU regulation | Transparency, accuracy, robustness, tiered compliance model |
| **EU Cyber Resilience Act** | Software supply chain | SBOM, signed releases, vulnerability handling, PSIRT, 5yr updates |
| **NIS2** | EU critical infrastructure | Incident handling, supply chain, crypto, vulnerability management |
| **FedRAMP** | US government | FIPS crypto option, access control, audit, vulnerability scanning |
| **UK Cyber Essentials** | UK market access | Boundary protection, secure config, access control, patching |
| **UK NCSC CAF v4.0** | UK critical infra | 14-principle cyber assessment (covers AI risks) |
| **BSIMM** | Security maturity | Code review, SAST, SCA, architecture analysis, vulnerability mgmt |
| **HIPAA** | US healthcare | Encryption (AES-256), audit controls, access controls, BAA support, breach notification |
| **HITRUST CSF** | Healthcare + cross-industry | Maps to 40+ frameworks; combined SOC 2 + HITRUST assessment |
| **FIRST PSIRT** | Vulnerability handling | Structured triage, remediation, and disclosure for CVEs |
| **MITRE ATT&CK** | Threat modeling | Security controls mapped to adversary techniques |
| **Global CBPR** | Cross-border privacy | Data transfer certification for APAC/Americas markets |
| **IETF RFC 9700/9449** | OAuth security | Best current practice + DPoP proof-of-possession |
| **CSA MCP Security Framework** | MCP hardening | Provenance, runtime isolation, secrets, observability |
| **NSA MCP Security Guidance** | Government/military | Message signing, per-call scoping, trust chains |

### What Compliance Means for This Project

We build compliance into the architecture, not as an afterthought. Our approach
uses a **tiered compliance model** that scales with capability:

**Tier 1 — Core retrieval (always active):**  
Search, scrape, extract, and return web content. Security controls (SSRF,
encryption, audit, rate limiting) protect infrastructure. Privacy controls
(data minimization, TTL caches, no cross-tenant leakage) protect users. This
tier satisfies the majority of regulatory requirements automatically.

**Tier 2 — User-facing analytics and insights (when activated):**  
Search history, topic trends, usage dashboards, session memory. These features
give users visibility into their own research patterns. Built with: opt-in
activation, user-owned data, tenant-scoped isolation, configurable retention,
full deletion capability. Satisfies GDPR legitimate interest (user's own
benefit) with transparency and control.

**Tier 3 — Machine-formatted output (when activated):**  
The server does **not** run any LLM or generate prose — synthesis is the client
model's job (server-side summarization was deliberately not built; see #94).
The only machine-shaped output is deterministic generative-UI components
(`GENERATIVE_UI_ENABLED`): source cards and a quality-comparison table built by
a deterministic transform of already-extracted data, plus consolidated
bibliographies. Built with: a non-bypassable machine-readable marker
(`"autoFormatted": true`, label `"mcp-auto-formatted"` — explicitly NOT
"AI-generated", because no model is involved), source attribution back to the
raw data, and raw content always present alongside. This transparency posture
aligns with EU AI Act Art. 50 labeling expectations even though no AI content
is produced.

**Tier 4 — Personalization and recommendations (when activated):**  
Cross-session intelligence, personalized ranking, smart suggestions. Built
with: explicit consent, explanation capability, opt-out mechanism, bias
auditing. Satisfies GDPR Art. 22, Quebec Law 25 s.12.1, PIPL Art. 24.

Each tier adds compliance infrastructure proportional to its regulatory
exposure. Lower tiers never require higher-tier infrastructure. Features
always ship with their compliance requirements met — not after.

### Our Compliance Principles (Evergreen)

These apply regardless of which features are active:

1. **Data belongs to the user/tenant.** Always viewable, exportable, deletable.
2. **No hidden data flows.** Every processing purpose is disclosed and justified.
3. **Opt-in for anything beyond the immediate request.** Storing data, analyzing
   patterns, generating content — each requires explicit activation.
4. **Proportional controls.** Simple features get simple compliance. Complex
   features get comprehensive governance. Nothing is over-engineered.
5. **Compliance infrastructure ships WITH the feature.** Never bolt-on, never
   "we'll add consent management later."
6. **Read the code, not the marketing.** Our compliance claims are verifiable
   in source code. Interfaces, tests, and architecture enforce what docs promise.

---

## Supply Chain Security

### What We Ship

Every release includes:

- Cross-platform binaries (Linux/macOS/Windows, amd64/arm64)
- Multi-arch Docker images (GHCR + Docker Hub)
- Software Bill of Materials (SPDX format)
- cosign signatures on all binaries and container images
- SHA-256 checksums

### Verification

```bash
# Verify binary signature
cosign verify-blob --signature web-researcher-mcp.sig \
  --certificate web-researcher-mcp.pem web-researcher-mcp

# Verify container image
cosign verify ghcr.io/zoharbabin/web-researcher-mcp:latest

# Verify checksums
sha256sum -c checksums.txt
```

### Continuous Security Scanning

| Tool | What it checks | When |
|------|---------------|------|
| `govulncheck` | Known Go vulnerabilities | Every CI run (`make vuln`) |
| `gosec` | Go security scanner (injection, weak crypto, SSRF sinks, unsafe file ops) | Every CI run (`make sec`) |
| `golangci-lint` | Static analysis + lint rules | Every CI run (`make lint`) |
| CodeQL | Semantic code analysis (security-extended) | Every PR + weekly |
| Dependabot | Dependency version vulnerabilities | Continuous |
| `go mod verify` | Dependency integrity (checksum match) | Every build |
| cosign | Release artifact signatures | Every release |
| Syft | SBOM generation | Every release |

`govulncheck`, `gosec`, and `golangci-lint` are pinned as `tool` directives in
`go.mod`, so CI and local runs (`make verify`) use identical versions. The Go
toolchain version is pinned in `go.mod`.

### Dependency Policy

- All dependencies: actively maintained, no unpatched CVEs
- Licenses: permissive only (MIT, Apache 2.0, BSD)
- Preference for Go standard library over third-party
- `go.sum` pins exact dependency hashes
- Minimum dependency footprint (fewer deps = smaller attack surface)

---

## Contributor Security Rules

Every contributor (human or AI agent) must follow these rules. They are
non-negotiable and enforced in code review.

### The Rules

1. **Never introduce OWASP Top 10 vulnerabilities.**  
   No command injection, XSS, SQL injection, SSRF, path traversal, or
   insecure deserialization. If unsure whether code is safe, ask.

2. **Validate all external input at system boundaries.**  
   Tool handler inputs, HTTP request parameters, environment variables,
   scraped content — validate at the boundary, trust within.

3. **Never log secrets.**  
   API keys, tokens, encryption keys, and credentials must never appear in
   logs, error messages, or audit events. Even in debug mode.

4. **Errors are values, never panics.**  
   Return `toolError()` or `upstreamErrorResponse()`. Never `panic()` in
   production paths. Never expose internal error details to clients.

5. **Encrypt sensitive data at rest.**  
   Any new persistent storage of potentially-sensitive data must use the
   existing encryption infrastructure (`cache.DiskCache` with GCM).

6. **Respect tenant boundaries.**  
   Any new feature touching shared state must consider multi-tenant isolation.
   Ask: "Can tenant A see tenant B's data?" The answer must be no.

7. **Use the SSRF-safe client for all outbound HTTP.**  
   Never use `http.DefaultClient` or `&http.Client{}` directly for fetching
   user-specified URLs. Always use `scraper.NewSSRFSafeClient()`.

8. **Add annotations to new tools.**  
   Every tool must declare `readOnlyAnnotations(idempotent, openWorld)`.
   The `TestAllToolsHaveAnnotations` test enforces this in CI.

9. **Don't accumulate data beyond the request lifecycle.**  
   New features should not store data indefinitely. Use TTLs. If long-term
   storage is genuinely needed, it requires explicit opt-in + retention policy.

10. **Keep the dependency footprint minimal.**  
    Prefer standard library. Each new dependency is a supply chain risk.
    Justify in the PR description why stdlib isn't sufficient.

### Security Review Triggers

These changes REQUIRE security-focused code review:

- Changes to `internal/auth/` or `internal/scraper/ssrf.go`
- New outbound HTTP calls
- Changes to cache key generation or tenant isolation
- New environment variables accepting secrets
- Changes to the `Dockerfile` or CI/CD pipeline
- Any use of `unsafe`, `reflect`, or `os/exec`

### Testing Requirements

- All new code: unit tests with `t.Parallel()`
- Security-sensitive code: negative test cases (what happens with malicious input?)
- Race conditions: `go test -race ./...` must pass
- SSRF: test with private IPs, metadata endpoints, redirect chains
- Auth: test with expired/invalid/missing tokens

---

## AI Agent Coding Rules

When AI coding agents (Claude Code, Copilot, Cursor, etc.) work on this
codebase, they must follow the contributor rules above PLUS these additional
constraints:

### Security-First Coding

1. **Never disable security checks** to make tests pass or code compile.
   Fix the underlying issue instead.

2. **Never use `--no-verify`** on git commits. Pre-commit hooks exist for
   a reason. If a hook fails, investigate and fix.

3. **Never generate or guess URLs** for fetching unless explicitly instructed.
   SSRF can be introduced through hardcoded URLs that happen to resolve to
   internal services.

4. **Never add backdoors, debug endpoints, or admin shortcuts** that bypass
   authentication or authorization. Even "temporary" ones.

5. **Never commit secrets, API keys, or credentials.** Even example values
   that look like real keys. Use obviously-fake placeholders: `your-key-here`.

### Secure Patterns to Follow

```go
// DO: Use the SSRF-safe client
client := scraper.NewSSRFSafeClient(cfg.AllowPrivateIPs)

// DON'T: Create an unrestricted client
client := &http.Client{}

// DO: Return typed errors
return toolError("query is required")

// DON'T: Panic
panic("unexpected state")

// DO: Validate URL schemes
if parsed.Scheme != "http" && parsed.Scheme != "https" {
    return toolError("URL must use http:// or https://")
}

// DON'T: Pass user input to os/exec
exec.Command("curl", userURL) // NEVER

// DO: Use constant-time comparison for secrets
subtle.ConstantTimeCompare([]byte(provided), []byte(expected))

// DON'T: Direct string comparison for auth
if provided == expected { // timing attack!
```

### What AI Agents Must Check Before Submitting

- [ ] No new dependencies added without justification
- [ ] No `panic()` calls in non-test code
- [ ] No hardcoded IPs, URLs, or credentials
- [ ] No `http.DefaultClient` usage for external URLs
- [ ] No raw SQL or shell command construction from user input
- [ ] `go test -race ./...` passes
- [ ] `golangci-lint run` passes
- [ ] New tools have annotations + tests

---

## Vulnerability Management

### Reporting

Report security vulnerabilities privately via
[GitHub Security Advisories](https://github.com/zoharbabin/web-researcher-mcp/security/advisories/new).

Do not open public issues for security vulnerabilities.

| SLA | Timeline |
|-----|----------|
| Acknowledgment | 48 hours |
| Fix plan | 7 days |
| Patch release | 30 days (critical: 72 hours) |

### How We Handle Vulnerabilities

Our vulnerability handling follows the FIRST PSIRT Services Framework:

1. **Receive** — reports via GitHub Security Advisories or direct contact
2. **Triage** — assess severity using CVSS v4.0, assign CWE identifier
3. **Remediate** — develop and test fix, request CVE if applicable
4. **Disclose** — coordinated disclosure with reporter, publish advisory
5. **Learn** — post-mortem, update threat model, improve defenses

All published advisories include: CVSS v4.0 score, affected versions, CWE
identifier, and mitigation guidance.

### Threat Model References

Our security controls map to MITRE ATT&CK techniques:

| Control | Mitigates |
|---------|-----------|
| SSRF protection | T1190 (Exploit Public-Facing App), T1557 (Adversary-in-the-Middle) |
| Input validation | T1059 (Command/Scripting Interpreter) |
| Content sanitization | T1059.007 (JavaScript), prompt injection vectors |
| Rate limiting | T1499 (Endpoint DoS) |
| Auth/JWKS | T1078 (Valid Accounts), T1550 (Use Alternate Auth Material) |
| Audit logging | T1070 (Indicator Removal) — tamper-evident logs prevent coverage |

---

## Roadmap Considerations

### Implemented

These items shipped and are no longer roadmap candidates:

- **Scope-based authorization (RBAC)** — JWT `scope`/`scp` claims mapped to tool
  permissions, opt-in via `ENFORCE_SCOPES` (`internal/auth/middleware.go`)
- **Key rotation** — `CACHE_ENCRYPTION_KEY_PREV` provides versioned keys with
  lazy re-encryption on read, with key-bound GCM AAD
- **Restart-durable revocation & quota** — token revocation and daily quota can
  persist via the encrypted `persist.Store` (`RATE_LIMIT_PERSIST`)
- **W3C Trace Context ingress** — requests adopt a sanitized `X-Request-Id` or
  `traceparent` trace-id for audit correlation, echoed on the response
- **NIST CSF 2.0 crosswalk + MITRE ATT&CK mapping** — see `docs/SECURITY.md`
- **HTTP hardening** — server timeouts, body/header caps, security headers,
  `CORS_STRICT`, and a pre-auth per-IP rate limit

### Security features planned or under consideration:

- **DPoP token binding (RFC 9449)** — proof-of-possession prevents token theft
- **Hash-chained audit logs** — tamper-evident logging for government deployments
- **Breach notification pipeline** — webhook alerting on security anomalies
- **in-toto build attestations** — full supply chain provenance (SLSA Level 3)
- **Seccomp profiles** — container syscall restriction for hardened deployments
- **UK Cyber Essentials certification** — UK public sector market access
- **Global CBPR certification** — cross-border data transfer for APAC markets

### Architecture decisions that won't change:

- Zero global state (dependency injection via struct)
- Interface-driven design (swap implementations without touching callers)
- Read-only tool semantics (tools fetch and return — never write externally)
- STDIO as the zero-config default
- Go standard library preference over third-party
- Compliance proportional to activated features (tiered model)

---

## Further Reading

| Document | Content |
|----------|---------|
| `docs/SECURITY.md` | Detailed technical security architecture (threat model, defense layers, crypto specs) |
| `docs/DEPLOYMENT.md` | Production deployment guide (Docker, K8s, env vars, scaling) |
| `docs/ERROR_HANDLING.md` | Error taxonomy and LLM-facing message design |
| `CONTRIBUTING.md` | Full contributor guide (setup, style, PR process) |
| `SECURITY.md` (root) | Vulnerability reporting policy |
| `.env.example` | All configuration options with descriptions |
	
