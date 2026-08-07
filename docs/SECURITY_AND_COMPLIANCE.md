# Security, Privacy & Compliance

How this project protects your data, your infrastructure, and your users.

This document covers security architecture, privacy principles, compliance posture, contributor guidelines, and deployment guidance — everything in one place, for everyone who needs it.

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
   The tool ships safe out of the box. Advanced users unlock capabilities through explicit configuration — never the other way around.

2. **Never limit research capabilities for security theater.**  
   Security controls must protect without reducing the quality, speed, or breadth of results. If a control blocks legitimate research, it becomes configurable — not mandatory.

3. **Compliance through architecture, not bolt-on checklists.**  
   Data minimization, purpose limitation, and tenant isolation are structural properties of the codebase, not afterthoughts added for an audit.

4. **STDIO is zero-trust-by-default.**  
   The most common deployment (local CLI via Claude Code, Cursor, etc.) has no network listener, no auth to misconfigure, and no attack surface. Multi-tenant security features only activate in HTTP mode.

5. **Simple things should be simple; complex things should be possible.**  
   A developer running `go build && ./web-researcher-mcp` should not need to configure OAuth, encryption keys, or compliance profiles. An enterprise deploying for 10,000 users should have everything they need.

6. **Elegant code is secure code.**  
   Short, readable, well-tested functions are easier to audit and harder to exploit than clever abstractions. Prefer Go's standard library over third-party dependencies. One clear implementation over pluggable frameworks.

---

## What We Protect Against

This server operates in a unique threat environment:

| Threat | Severity | Defense |
|--------|----------|---------|
| SSRF (Server-Side Request Forgery) | Critical | Custom `DialContext` validating all resolved IPs against private/reserved CIDR blocks + cloud-metadata hostnames + DNS rebinding prevention |
| Prompt injection via scraped content | High | Content sanitization, size limits, and a `"trust": "untrusted-external-content"` boundary marker in the JSON envelope (the host enforces the prompt boundary — the model lives there) |
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

All components are wired through the `tools.Dependencies` struct — zero global state, fully testable, every dependency swappable via interfaces.

---

## Defense Layers (Technical Detail)

### SSRF Protection

The highest-severity risk for a scraping server. Our defense:

1. Check hostname against blocklist (cloud IMDS endpoints)
2. Resolve DNS
3. Validate ALL resolved IPs against private/reserved ranges
4. Connect directly to the resolved IP (prevents DNS rebinding)
5. Re-validate on each redirect hop (max 5 redirects)

Blocked ranges: RFC 1918, link-local (169.254.0.0/16), loopback, multicast, carrier-grade NAT, documentation ranges, IPv6 equivalents. See `internal/scraper/ssrf.go` for the canonical list.

Escape hatch: `ALLOW_PRIVATE_IPS=true` for development/intranet scraping. Domain allowlist: `ALLOWED_DOMAINS=a.com,b.com` for enterprise lock-down.

### Content Security

Scraped content passes through a configurable sanitization pipeline:

1. **Default mode**: HTML sanitization (bluemonday allowlist — strips scripts, iframes, event handlers), hidden content removal (display:none, zero-width chars), size enforcement (50KB per source, 300KB total)
2. **Raw mode** (`mode: "raw"`, `scrape_page` only): Returns the fetched bytes verbatim — scripts, styles, and markup intact — for inspecting source such as JSON, HTML, or JavaScript (code analysis, security research, web development). Only `content.Process` is skipped; the request still passes through `validateScrapeURL`, the SSRF-safe client, the `ALLOWED_DOMAINS` allowlist, and an `io.LimitReader` bounded by `max_length`. The returned content is UNTRUSTED (it may carry indirect prompt-injection payloads), so callers must never execute or render it. `search_and_scrape` has no raw mode and is always sanitized.
3. **Size limits**: Configurable per-request via `maxLength` parameter. The LLM decides how much content it needs based on context window and task. Defaults protect against context flooding; explicit overrides serve legitimate research needs (analyzing large codebases, full-page audits).
4. **Trust boundary marker**: every scrape response carries `"trust": "untrusted-external-content"` in the JSON envelope (not inside the `content` string, where a page could forge it) — an explicit signal to treat `content` as data, never as instructions. Alongside it, `contentType` reports the form: in sanitized modes the extracted type (e.g. `text/markdown`); in raw mode the server's real `Content-Type` header (which may be empty) with `"raw": true`. So downstream consumers always know both *what* they are handling and *that it is untrusted*.

The pipeline reduces prompt-injection exposure and context flooding by default, while allowing full access to page source when the research task requires it. One honest limit: the server **cannot** enforce the prompt boundary itself — the model and agent loop live in the host application, so neutralizing plain-text injection payloads is the host's job; the server's role is to sanitize, cap, and mark provenance so the host can. SSRF protection applies regardless of mode — the security boundary is what URLs you can reach, not what content you can read from them.

### Authentication (HTTP mode)

OAuth 2.1 Resource Server pattern:

- RS256 JWT signature verification via JWKS endpoint
- Auto-refreshing key cache (configurable interval, default 1 hour)
- Audience and issuer validation
- Token revocation via JTI — in-memory by default, optionally backed by the encrypted `persist.Store` so revocations survive restarts (fail-closed: a JTI is revoked if present in either layer)
- Scope-based per-tool authorization (opt-in via `ENFORCE_SCOPES`; see below)
- Constant-time comparison for admin key authentication
- Rejects HS256 from external issuers (algorithm confusion prevention)

**Scope-based authorization (RBAC).** When `ENFORCE_SCOPES=true`, a token that carries a `scope`/`scp` claim must hold one of `tool:*`, `tool:<name>`, or the coarse `research` scope to invoke a tool (plus any `REQUIRED_SCOPES`). It is permissive by design: `ENFORCE_SCOPES=false` (default) ignores scopes, and a token with no scope claim is always allowed (backward-compatible). It fails closed only on a present-but-insufficient scope. See `Middleware.EnforceScopes` in `internal/auth/middleware.go` and the crosswalk in `docs/SECURITY.md`.

**Session identifiers (accepted risk).** Sequential-search session IDs are static UUIDv4 values that do not rotate over the life of a session. This is an accepted, documented risk: a session ID is a research-continuity handle, not an authentication credential. Authorization is always derived from the validated JWT (tenant/user/scope), and sessions are keyed by the compound `{tenantID}:{userID}:{sessionID}` so a guessed or leaked session ID cannot cross a tenant or user boundary or grant access without a valid token. Rotation was deliberately not added to avoid breaking the recovery-after-context-loss workflow the IDs exist to support.

STDIO mode: no authentication (the calling process is the trust boundary).

**Design choice: RS256 over HMAC, and where DPoP fits.** This server is an OAuth 2.1 *resource server* — it verifies tokens minted by an external IdP (Auth0, Okta, Keycloak, Entra ID, etc.) and never issues them itself; no private signing key exists anywhere in this codebase. That shapes the algorithm choice:

```
HMAC (HS256): one shared secret both signs and verifies.        RS256: one private key signs, any number of public
Any verifier holding the secret can also forge tokens           keys (published via JWKS) verify. Compromising a
accepted by every other verifier that trusts it.                verifier's key material grants no signing ability.

  IdP --shared secret--> Server A                                 IdP --private key (never leaves IdP)--> signs JWT
  IdP --shared secret--> Server B                                 IdP --publishes public key--> JWKS endpoint
  (leak one verifier, forge tokens for all)                        Server A, Server B --fetch JWKS--> verify only
```

A single-service, single-trust-domain setup can use HMAC safely. A multi-tenant resource server that never issues its own tokens has a materially larger blast radius if it shares a symmetric verification secret than if it publishes a public key — so `internal/auth/middleware.go` pins to RS256 and rejects every other `alg`, including HS256 (closing the classic algorithm-confusion attack, where a token is resigned with HS256 using the public RS256 key as the HMAC secret).

RS256 verification proves a token was issued by the trusted IdP and is unmodified. It does not prove the *presenter* is the party the IdP issued it to — a stolen bearer token replays as-is until it expires or is revoked. DPoP (RFC 9449) closes that gap by binding a token to a client-held private key via a `cnf.jkt` claim the IdP embeds at issuance time:

```
Bearer token (this server today)                 DPoP (requires IdP-side cnf.jkt support)
Token stolen (log leak, XSS, proxy compromise)    Client signs a one-time proof JWT per request
  -> replayed as-is, indistinguishable from          with its own private key
     the legitimate client                         Access token carries cnf.jkt = SHA-256(pubkey)
  -> mitigated by short TTL, JTI revocation,         Server checks: does this request's proof-key
     per-tenant rate limits, audit logging           thumbprint match the token's cnf.jkt?
                                                     -> stolen token alone is useless
```

A resource-server-only architecture cannot retrofit the right-hand side unilaterally — `cnf.jkt` can only be embedded by the issuing IdP at mint time. Research into current standards and the MCP ecosystem (tracked on [issue #489](https://github.com/zoharbabin/web-researcher-mcp/issues/489)) found DPoP is recommended-but-optional (RFC 9700/BCP 240), unevenly supported across IdPs (GA at Auth0/Okta/Ping/Curity; absent at AWS Cognito), still a proposal rather than a ratified requirement in the MCP spec itself, and not named by FedRAMP, DoD Zero Trust guidance, NIST 800-63B, or HIPAA's Security Rule. The realistic threat — stolen-bearer-token replay — is mitigated today via short-lived tokens, JTI revocation that survives restarts, per-tenant rate limiting, circuit breakers, and secret-redacted, request-correlated audit logging.

**Design choice: mesh/ingress-terminated mTLS, not application-native.** For service-to-service traffic (the HTTP admin plane, and the Redis connection in `internal/redisbackend`), this server relies on the surrounding infrastructure to terminate mutual TLS rather than implementing a TLS client-cert handshake in application code. This is a deliberate architectural choice, not a gap deferred for later:

- **No standard surveyed requires application-native mTLS.** NIST SP 800-207 (Zero Trust Architecture) is technology-neutral by design; its cloud-native companion, SP 800-207A, explicitly names sidecar/service-mesh-terminated mTLS as a compliant Zero Trust enforcement pattern. PCI-DSS v4.0.1 Requirement 4, SOC 2's CC6.1/CC6.6/CC6.7, ISO/IEC 27001:2022 Annex A 8.20/8.24, and NIST SP 800-53 Rev 5 (SC-8, SC-13, SC-23) are all written as control outcomes — "protect transmitted information," "use strong cryptography" — and are silent on which component in the stack performs the cryptographic handshake. The one control that textually resembles mTLS, NIST 800-53's IA-3(1) ("cryptographically based bidirectional authentication"), is an optional enhancement selected per impact baseline, not a universal requirement.
- **This mirrors mainstream practice, not a compromise.** Google's BeyondProd architecture terminates mTLS-equivalent trust at the infrastructure layer specifically so "individual microservice developers aren't burdened with implementing" it. CNCF's 2024 survey puts service-mesh adoption at 42% of respondents — infra-terminated mTLS (mesh sidecar, gateway, or platform-provisioned identity) is the dominant pattern at scale, not an exception auditors tolerate reluctantly.
- **What an audit actually checks is the control outcome, not the binary.** Auditors evaluating internal-traffic mutual authentication push back on an undocumented or incorrectly-scoped trust boundary — not on the absence of native TLS code in the application process. A documented answer of "our service mesh/ingress terminates mTLS at trust-zone boundary X, backed by cert issuance/rotation policy Y" satisfies every framework above as thoroughly as native code would.
- **The one real gap: bare-VM / non-mesh deployments.** Where there is no sidecar or gateway to terminate mTLS (a plain VM outside Kubernetes), the standards-consistent answer is still not to build cert provisioning/rotation/revocation into this binary — it is to place a lightweight local TLS-terminating proxy in front of it, or adopt SPIFFE/SPIRE workload identity (which explicitly supports VM and bare-metal workloads via node/workload attestation, issuing short-lived X.509-SVIDs a local agent consumes). This keeps cert lifecycle management out of the application's own code, consistent with every other pattern surveyed here. Tracked on [issue #485](https://github.com/zoharbabin/web-researcher-mcp/issues/485).

### Rate Limiting (HTTP mode)

Three tiers preventing abuse while allowing legitimate high-throughput research:

| Tier | Default | Purpose |
|------|---------|---------|
| Global | 1000 req/s | Infrastructure protection |
| Per-tenant | 120 req/min | Fair use between tenants |
| Daily quota | 5000 req/day | Cost control |

All configurable. Returns 429 with `Retry-After` header.

### Encryption

- **At rest:** AES-256-GCM with random nonces for cache and sessions (when `CACHE_ENCRYPTION_KEY` is set). File permissions 0600.
- **In transit:** TLS 1.2+ for all outbound connections (Go stdlib default). HSTS header for inbound HTTP.
- **FIPS option:** Build with `GOFIPS140=latest` for Go's native FIPS 140-3 cryptographic module (`crypto/fips140`), backed by active CMVP certificate [#5247](https://csrc.nist.gov/projects/cryptographic-module-validation-program/certificate/5247). `GOEXPERIMENT=boringcrypto` is not the compliant path — BoringCrypto's own certificate is [Historical](https://csrc.nist.gov/projects/cryptographic-module-validation-program/certificate/3678), and Go's docs mark that build flag unsupported and slated for removal.

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

Design: async channel-based (never blocks tool calls), swap-to-disk overflow (never drops events under normal load), configurable output (stderr or file).

What is NOT logged, ever: raw query text. The query length (an integer) is always recorded; when `AUDIT_INCLUDE_REQUEST_BODY=true`, a SHA-256 hash of the query is additionally recorded so an operator can correlate repeated queries without the literal text ever reaching the audit sink — the literal query is never persisted regardless of this flag, so it carries no GDPR erasure gap (#486). Scraped content and full request parameters are also never logged (PII risk). Audit metadata and upstream error messages pass through `audit.MaskSecrets` so any credential echoed back by a provider is redacted before it reaches a sink. Audit files rotate at `AUDIT_MAX_BYTES` and are pruned after `AUDIT_RETENTION_DAYS` (default 180, clamped to `[180, 3650]`).

**Tamper-evident hash chain.** Every `AuditEvent` carries `prev_hash`/`hash` fields: `hash` is SHA-256 of the previous event's `hash` plus the current event's canonical JSON bytes (with `hash` cleared). Each `audit.Logger` instance keeps only the tail hash of its own chain (one fixed-size string, never the full history), so verifying integrity later is a matter of re-walking the log, not something the logger needs to keep growing state for. Call `audit.VerifyChain(io.Reader)` against a JSONL audit log to confirm every recorded hash matches what its `prev_hash` and bytes recompute to; it returns an error naming the first line whose content, hash, or ordering was altered. This detects tampering or edits made directly to the log file — it does not itself prevent an operator with filesystem access from rewriting or truncating the file, which is why write access to the audit output path should be restricted at the OS/filesystem level for genuinely adversarial threat models.

**Forwarding to a SIEM.** Set `AUDIT_WEBHOOK_URL` to have `internal/audit/logger.go` POST each event (including its hash-chain fields) as JSON to that URL — fire-and-forget, with a bounded timeout (default 5s) and a capped number of in-flight requests, so a slow or dead receiver never blocks the audit write path or grows goroutines without bound. This is a generic "ship every event somewhere" sink, not a bespoke alerting/notification pipeline — pairing it with your SIEM's own alerting rules (e.g. on `event_type`/`success`) gets you that behavior without this project needing to build it (see [issue #492](https://github.com/zoharbabin/web-researcher-mcp/issues/492), which declined a dedicated breach-notification pipeline as an enterprise-tier concern; a generic event-forwarding webhook is a different, smaller scope). Without `AUDIT_WEBHOOK_URL`, forward events with a standard log agent instead: point Fluentd, Vector, or a syslog forwarder at the configured audit output path (or stdout, if your container platform already collects it).

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
| Recent-errors ring (`diagnostics://errors/recent`) | Bounded in-memory ring, tenant-aware | None (memory-only, oldest overwritten) | No — causes redacted via `audit.MaskSecrets`; no query text, no full URLs |
| Operator dashboard / `diagnostics://health` | Derived on read from existing metrics + breaker state | None (computed per request) | No — aggregate-only, no per-user/per-query data |

The **operator-observability** surfaces added in the routing-observability work (`_meta.routing` on results, the `diagnostics://` resources, and the admin-gated `GET /dashboard`) are deliberately privacy-minimal: routing detail exposes only the provider *name* (never upstream URLs, credentials, or breaker internals), the recent-errors ring is memory-only/bounded/redacted/tenant-scoped, and the dashboard is aggregate-only and admin-gated. None of them is a personal-data store, so none adds a GDPR processing obligation. See `docs/DEPLOYMENT.md` → Operator Observability and `docs/TOOLS.md` → Routing Provenance.

### Purpose Limitation

Each feature declares its data processing purpose explicitly. Core principles:

- **Primary purpose**: always the user-facing function (search, scrape, analyze)
- **Legitimate secondary purposes** (when enabled): usage analytics for the user's own benefit, research session memory, trend insights — each opt-in and clearly disclosed
- **Never allowed**: selling data to third parties, training external models, undisclosed advertising, sharing across tenants without consent

When features use data for purposes beyond the immediate request (e.g., a search analytics dashboard showing trends over time), they are built with: informed activation, transparent data flows, configurable retention, and user-controlled deletion.

### Right to Erasure

For HTTP multi-tenant deployments: the `DELETE /admin/data` endpoint erases all personal data for a `(tenant_id, user_id)` subject across every registered store (sessions, analytics, memory, workspace) and simultaneously withdraws consent so processing cannot silently resume. `GET /admin/data` exports the same scope for portability. For STDIO: data is local to your machine — delete the cache directory. Any feature that stores data beyond the request lifecycle provides a deletion mechanism.

### User Insights Without Surveillance

The project offers features that help you understand your own research patterns — which tools you used, how often, and when (opt-in, consent-gated). These are designed as **user-owned insights, not profiling**:

- Data belongs to the user/tenant and is never shared across boundaries
- The user can view, export, and delete their own data at any time
- No behavioral predictions, scoring, or automated decisions about users
- No data leaves the user's control without explicit action
- STDIO mode stores everything locally — the user controls their own disk
- HTTP mode scopes all analytics to the authenticated tenant

The distinction: showing a user "here's what you searched for last month" is a productivity feature. Building a hidden model of user behavior to influence results without disclosure is profiling. We do the former, never the latter.

---

## Deployment Security

### STDIO Mode (default, zero-config)

No network listener. No attack surface. The binary communicates via stdin/stdout with the calling process. Security is provided by your OS (file permissions, process isolation).

Suitable for: individual developers, local AI assistants, testing.

### HTTP Mode (multi-tenant)

Activated by setting `PORT`. Enables OAuth, rate limiting, CORS, and all multi-tenant security features.

**Minimum secure configuration** requires: a port, OAuth issuer and audience for JWKS validation, an allowed origins list, an encryption key for the cache and sessions, an admin API key, and audit logging enabled with an output path. See [`docs/DEPLOYMENT.md`](DEPLOYMENT.md) for exact variable names and defaults.

**Production hardening** additionally enables per-tenant cache isolation, sets rate limits and daily quota, configures a domain allowlist if needed, and adjusts scrape concurrency. See [`docs/DEPLOYMENT.md`](DEPLOYMENT.md) for all variables.

**Healthcare deployments (HIPAA):** if the server processes or caches content containing Protected Health Information (PHI), enable encryption at rest, strict per-tenant cache isolation, and audit logging with a retention path sufficient for the 6-year HIPAA requirement (45 CFR 164.312(b) requires audit controls; AES-256-GCM encryption renders a breach non-reportable under Safe Harbor). See [`docs/DEPLOYMENT.md`](DEPLOYMENT.md) for exact variable names and defaults.

A Business Associate Agreement (BAA) is required between the entity deploying this server and any covered entity whose data it processes. The server's encryption at rest (AES-256-GCM), encryption in transit (TLS 1.2+), access controls (OAuth + tenant isolation), and audit logging satisfy HIPAA Technical Safeguards (45 CFR 164.312). The FIPS build option (`GOFIPS140=latest`, Go's native FIPS 140-3 module, CMVP certificate [#5247](https://csrc.nist.gov/projects/cryptographic-module-validation-program/certificate/5247)) satisfies NIST SP 800-111 requirements referenced by HIPAA.

### Container Security

The Docker image ships with full rendering capabilities:

- Non-root execution (UID 65534)
- Chromium for headless page rendering (JS-heavy sites, SPAs)
- Alpine base with ca-certificates and required runtime libraries
- Multi-stage build (no build tools in runtime image)
- All images signed with cosign (verify with `cosign verify`)

The image includes Chromium because accurate research requires rendering JavaScript-heavy pages. Chromium is launched with `--no-sandbox`; container-level isolation (non-root UID 65534, network policies) provides the sandboxing boundary. Chromium's attack surface is mitigated by: non-root execution, bounded concurrency (single browser instance shared across goroutines; page concurrency capped by MaxBrowserConcurrency, a pool independent of the fast-tier MaxConcurrency, #472), SSRF protection on all URLs before they reach the browser, and container-level network restrictions.

Deployment recommendations:

- Mount cache directory as tmpfs or encrypted volume
- Apply network policies limiting egress to search API endpoints + Chromium download endpoints (for auto-update if enabled)
- Apply a `seccomp` profile — `RuntimeDefault` is already set in the example manifests (`deploy/k8s-local/03-app.yaml`, `docs/DEPLOYMENT.md`), matching the Kubernetes Pod Security Standards "Restricted" baseline
- For environments where Chromium is not desired: set `CHROME_PATH=disabled` to skip browser-tier scraping entirely

---

## Configuration Reference

The security- and privacy-relevant settings are grouped into these knobs — **defaults and exact variable names live in [`.env.example`](../.env.example) and [`docs/DEPLOYMENT.md`](DEPLOYMENT.md), the two authoritative homes** (this guide does not duplicate them, so they cannot drift):

- **Encryption at rest** — a hex key turns on AES-256-GCM disk encryption for the cache, the TTL key/value store, and sessions; a previous-key slot enables zero-downtime rotation (decrypt-fallback + lazy re-encrypt).
- **SSRF / scrape egress** — toggles for allowing private/RFC1918 IPs and a domain allowlist that restricts scrape targets.
- **CORS** — an origin allowlist plus a fail-closed strict toggle (default deny on empty; see [`docs/MIGRATION.md`](MIGRATION.md)).
- **Auth & scopes (HTTP mode)** — OAuth issuer/audience for JWKS validation, JWKS refresh interval, and optional per-tool scope enforcement.
- **Admin surface** — the admin key that gates every `/admin/*` endpoint and the operator dashboard (constant-time compared; a deprecated alias is still accepted with a startup warning).
- **Tenant isolation** — per-tenant cache isolation mode.
- **Rate limiting (HTTP mode)** — global/second, per-tenant/minute, per-tenant/day, and a pre-auth per-IP ceiling.
- **Audit & observability** — audit logging on/off, output path, buffer size, log level, and the Prometheus `/metrics` toggle.

See `docs/DEPLOYMENT.md` for the table of every variable, its default, and its effect.

---

## Compliance Posture

> **Prefer slides?** The same story — how architecture, not paperwork, keeps this project aligned with 23 standards — is captured in a short visual deck: **[Compliance as Architecture](https://zoharbabin.com/web-researcher-mcp/decks/compliance/)** ([PDF](https://zoharbabin.com/web-researcher-mcp/decks/compliance/compliance-deck.pdf) · [source](https://github.com/zoharbabin/web-researcher-mcp/blob/main/decks/compliance/compliance-deck.md)). Each technical claim names the file that backs it — open any one and check.

### What We Target

This project is designed to satisfy security and privacy requirements across multiple international frameworks simultaneously. We achieve this through architectural choices that inherently satisfy shared requirements across standards — not through per-framework checkbox exercises.

### Standards Alignment

| Standard | Relevance | How we align |
|----------|-----------|-------------|
| **ISO 27001** | Foundation | Interface-driven architecture, access control, encryption, audit, supply chain |
| **SOC 2 Type II** | Enterprise trust | Audit logging, rate limiting, tenant isolation, change management |
| **NIST CSF 2.0** | US enterprise | Govern/Identify/Protect/Detect/Respond/Recover mapped to controls |
| **GDPR / UK GDPR** | Privacy | Data minimization, purpose limitation, TTL caches; data-subject access/portability/erasure endpoints (`/admin/data`); consent record-verify-honor for regulated features |
| **LGPD (Brazil) / PIPEDA (Canada) / DPDP Act (India) / POPIA (South Africa) / APPI (Japan) / PDPA (Singapore) / Privacy Act (Australia) / PIPL (China)** | Global privacy — **operator-owned, same shape as GDPR** | Each law's processor/intermediary-equivalent role has narrower direct statutory duties than its controller-equivalent, but every one of those duties (security safeguards, retention limitation, breach-notification support) maps to controls this project already ships (AES-256-GCM encryption, TTL caches, audit logging, `/admin/data` erasure). Cross-border-transfer mechanics and controller/processor role assignment remain the deploying entity's to configure, exactly as with GDPR above |
| **OWASP MCP Cheat Sheet** | MCP-specific | SSRF protection, content sanitization, tool annotations, supply chain |
| **OWASP Top 10 LLM (2025)** | AI security | Prompt injection defense, bounded agency, supply chain verification |
| **OWASP Agentic Top 10 (2026 draft)** | AI agent security — **tool-provider slice only** | What this server owns: read-only-default tool annotations (writes non-destructive; deletion is a separate endpoint), JWT scope authorization, consent-gated + tenant-isolated + erasable memory/workspace, SSRF + HTML sanitization + untrusted-content marker, rate limits + circuit breakers + size caps, per-call audit with request-ID correlation. **Host-owned (not us):** goal/intent manipulation, cascading hallucination, multi-agent trust, the agent permission model — those live in the client that drives the tools. |
| **NIST AI RMF** | AI governance | Risk-aware design, transparency, continuous monitoring |
| **EU AI Act** | EU regulation | Transparency, accuracy, robustness, tiered compliance model |
| **EU Cyber Resilience Act** | Software supply chain | SBOM, signed releases, vulnerability handling, PSIRT, 5yr updates |
| **NIS2** | EU critical infrastructure | Incident handling, supply chain, crypto, vulnerability management |
| **FedRAMP** | US government | FIPS crypto option, access control, audit, vulnerability scanning |
| **UK Cyber Essentials** | UK market access — **operator-owned** | This scheme certifies an organization's IT estate (device config, patching, access control), not an OSS artifact. There is no organization or IT estate here to certify. An operator seeking Cyber Essentials certifies their own deployment, using this project's boundary protection, secure config, access control, and patching as supporting evidence |
| **PCI-DSS** | Payment card industry — **not applicable** | This project never processes, stores, or transmits payment card or cardholder data — no tool, cache, or persisted store has a card-data field. PCI-DSS attaches only to systems in that data path; nothing here qualifies |
| **UK NCSC CAF v4.0** | UK critical infra | 14-principle cyber assessment (covers AI risks) |
| **BSIMM** | Security maturity | Code review, SAST, SCA, architecture analysis, vulnerability mgmt |
| **HIPAA** | US healthcare | Encryption (AES-256), audit controls, access controls, BAA support, breach notification |
| **NYDFS 23 NYCRR 500 / NAIC Insurance Data Security Model Law** | US financial/insurance — **operator-owned** | Both regulate the covered bank/insurer's Third-Party Service Provider oversight program, not an OSS artifact directly. This project supplies the technical evidence (encryption, access control, audit logging, vulnerability scanning) a covered entity cites in its own vendor risk assessment |
| **HITRUST CSF** | Healthcare + cross-industry | Maps to 40+ frameworks; combined SOC 2 + HITRUST assessment |
| **FIRST PSIRT** | Vulnerability handling | Structured triage, remediation, and disclosure for CVEs |
| **MITRE ATT&CK** | Threat modeling | Security controls mapped to adversary techniques |
| **Global CBPR** | Cross-border privacy — **operator-owned** | This scheme certifies a business's cross-border data transfer program (APAC/Americas), not an OSS artifact. This project has no cross-border data-transfer business to certify. An operator seeking CBPR certifies their own deployment/data flows, using this project's data minimization and purpose-limitation controls as supporting evidence |
| **IETF RFC 9700/9449** | OAuth security | Best current practice + DPoP proof-of-possession |
| **CSA MCP Security Framework** | MCP hardening | Provenance, runtime isolation, secrets, observability |
| **NSA MCP Security Guidance** | Government/military | Message signing, per-call scoping, trust chains |
| **ISO 22301** | Business continuity — **operator-owned** | This standard certifies an organization's Business Continuity Management System (leadership commitment, BIA, exercise program), not an OSS artifact. This project's stateless/HPA architecture and Redis-durability options give an operator's BIA strong RTO/RPO inputs — see [Recovery objectives](DEPLOYMENT.md#recovery-objectives-rtorpo) — but the BCMS itself is the operator's |
| **EU DORA** | EU financial-sector ICT risk — **out of scope unless hosted as SaaS** | DORA (Reg. (EU) 2022/2554) governs a financial entity's relationship with an "undertaking providing ICT services" it procures from. Self-hosting this OSS binary isn't procuring ICT services any more than self-hosting Postgres is. It attaches only if a *hosted SaaS* offering of this project sells to EU financial entities — then that SaaS operator owns Art. 28-44 obligations, the same split as the SOC 2/HIPAA rows above |
| **ISO/IEC 42001** | AI management system — **operator-owned** | This standard certifies an organization's AI Management System (governance, risk treatment, lifecycle controls across AI-related roles), not an OSS artifact. This project is closest to an AI-system component supplier feeding an operator's own AIMS — same shared-responsibility shape as ISO 27001/UK Cyber Essentials above; it supplies technical evidence (transparency, risk-aware design, audit trails), not the management system itself |

### What Compliance Means for This Project

We build compliance into the architecture, not as an afterthought. Our approach uses a **tiered compliance model** that scales with capability:

**Tier 1 — Core retrieval (always active):**  
Search, scrape, extract, and return web content. Security controls (SSRF, encryption, audit, rate limiting) protect infrastructure. Privacy controls (data minimization, TTL caches, no cross-tenant leakage) protect users. This tier satisfies the majority of regulatory requirements automatically.

**Tier 2 — User-facing analytics and insights (when activated):**  
Search history, topic trends, usage dashboards, session memory. These features give users visibility into their own research patterns. Built with: opt-in activation, user-owned data, tenant-scoped isolation, configurable retention, full deletion capability. Satisfies GDPR legitimate interest (user's own benefit) with transparency and control.

**Tier 3 — Machine-formatted output (when activated):**  
The server does **not** run any LLM or generate prose — synthesis is the client model's job (server-side summarization was deliberately not built; see #94). The only machine-shaped output is deterministic generative-UI components (`GENERATIVE_UI_ENABLED`): source cards and a quality-comparison table built by a deterministic transform of already-extracted data, plus consolidated bibliographies. Built with: a non-bypassable machine-readable marker (`"autoFormatted": true`, label `"mcp-auto-formatted"` — explicitly NOT "AI-generated", because no model is involved), source attribution back to the raw data, and raw content always present alongside. This transparency posture aligns with EU AI Act Art. 50 labeling expectations even though no AI content is produced.

**Tier 4 — Personalization and recommendations (when activated):**  
Cross-session intelligence, personalized ranking, smart suggestions. Built with: explicit consent, explanation capability, opt-out mechanism, bias auditing. Satisfies GDPR Art. 22, Quebec Law 25 s.12.1, PIPL Art. 24.

Each tier adds compliance infrastructure proportional to its regulatory exposure. Lower tiers never require higher-tier infrastructure. Features always ship with their compliance requirements met — not after.

### Our Compliance Principles (Evergreen)

These apply regardless of which features are active:

1. **Data belongs to the user/tenant.** Always viewable, exportable, deletable.
2. **No hidden data flows.** Every processing purpose is disclosed and justified.
3. **Opt-in for anything beyond the immediate request.** Storing data, analyzing patterns, generating content — each requires explicit activation.
4. **Proportional controls.** Simple features get simple compliance. Complex features get comprehensive governance. Nothing is over-engineered.
5. **Compliance infrastructure ships WITH the feature.** Never bolt-on, never "we'll add consent management later."
6. **Read the code, not the marketing.** Our compliance claims are verifiable in source code. Interfaces, tests, and architecture enforce what docs promise.

---

## Operator & Hosted-Service Responsibilities

The tiered model and Compliance Principles above describe the technical controls this project **ships**. They are necessary but not sufficient: every framework on the Standards Alignment table also has an organizational, process half that a source repository cannot contain. **"Aligned with" means this project provides the technical controls a standard requires — never that an organization has been audited and certified against it.** A binary can't clear a hospital's HIPAA bar; it gives that review its technical-controls evidence.

Responsibility splits three ways.

### Shared-responsibility model

| The project ships (in code) | The operator owns (process & controller role) | A hosted SaaS additionally owns (org program) |
|-----------------------------|-----------------------------------------------|-----------------------------------------------|
| SSRF-safe outbound client (`internal/scraper/ssrf.go`) | Being the **data controller** for data they persist | Staff trained on the controls (e.g. HIPAA workforce training) |
| AES-256-GCM at rest (opt-in via `CACHE_ENCRYPTION_KEY`) + TLS in transit | Generating & rotating encryption / admin keys | Controls **audited operating over a period** (SOC 2 Type II: 3–12 mo + an auditor) |
| Secrets-masked audit logging (`internal/audit`) | Monitoring & alerting on the audit stream; setting the retention **schedule** (the code gives TTL knobs, not policy) | 24/7 incident-response capability and on-call |
| Tenant isolation, OAuth 2.1 / JWKS / revocation, rate limits | Access reviews; an incident-response runbook; **executing** breach notification | Signed customer DPAs / BAAs at scale; a named DPO |
| Consent + erasure **primitives** (`internal/consent`, `internal/datasubject`, `/admin/data`) | Choosing lawful basis; running DPIAs; maintaining a Record of Processing Activities (GDPR Art. 30) | The actual SOC 2 Type II / HITRUST / ISO 27001 **audit engagement** |
| SBOM + cosign signatures + CodeQL + `govulncheck` + a PSIRT process (root `SECURITY.md`) | Signing BAAs (HIPAA) / DPAs (GDPR) with covered entities and their own users | Management review, Statement of Applicability, continual improvement (ISO 27001 Clauses 4–10) |

### To actually reach a given standard, the deploying entity must…

- **SOC 2 Type II** — engage an auditor to observe the shipped controls operating over a 3–12 month period, plus a management assertion. The code supplies the control evidence (OAuth, audit logs, change history); it is not the audited program.
- **HIPAA** — sign a **Business Associate Agreement** with each covered entity, train its workforce, run a risk analysis, and operate a breach-notification process. The project ships the Technical Safeguards (encryption, audit controls, access controls); the Administrative Safeguards are the operator's.
- **GDPR / UK GDPR** — name the **data controller**, choose and document a lawful basis, run DPIAs where required, maintain a Record of Processing Activities (Art. 30), and execute 72-hour breach reporting (Art. 33). The project ships the data-subject-rights primitives (access/portability/erasure via `/admin/data`, consent record-verify-honor) — the operator owns the controller obligations.
- **ISO 27001** — operate an Information Security Management System: documented scope, risk-treatment methodology, Statement of Applicability, internal-audit program, and management review (Clauses 4–10). The project satisfies the relevant Annex A technical controls only.
- **NYDFS 23 NYCRR 500.11 / NAIC Insurance Data Security Model Law** — a bank or insurer deploying this server must run its own Third-Party Service Provider due-diligence and hold the vendor (this project) to its written information security policy, same shape as a HIPAA BAA or GDPR DPA above. NAIC's Model Law text itself treats NYDFS 500 compliance as satisfying the Model Law, so a covered entity meeting one has effectively met both. The project supplies the technical evidence (encryption, access control, audit logging, vulnerability scanning); the covered entity owns the vendor-oversight program and regulatory filing.

This split is not a disclaimer — it is the honest shape of "compliance as architecture." The repository can make the technical half **provably** true; the operating organization owns the process half. Both are required.

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
# 1. Verify the cosign signature on checksums.txt (keyless / Fulcio cert).
#    The release signs checksums.txt — not each binary — then the checksum
#    file vouches for every artifact.
cosign verify-blob \
  --signature checksums.txt.sig \
  --certificate checksums.txt.pem \
  --certificate-identity-regexp 'https://github.com/zoharbabin/web-researcher-mcp' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  checksums.txt

# 2. Verify your downloaded artifacts against the (now-trusted) checksums.
sha256sum -c checksums.txt

# 3. Verify a container image signature directly.
cosign verify ghcr.io/zoharbabin/web-researcher-mcp:latest \
  --certificate-identity-regexp 'https://github.com/zoharbabin/web-researcher-mcp' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
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

`govulncheck`, `gosec`, and `golangci-lint` are pinned as `tool` directives in `go.mod`, so CI and local runs (`make verify`) use identical versions. The Go toolchain version is pinned in `go.mod`.

### Dependency Policy

- All dependencies: actively maintained, no unpatched CVEs
- Licenses: permissive only (MIT, Apache 2.0, BSD)
- Preference for Go standard library over third-party
- `go.sum` pins exact dependency hashes
- Minimum dependency footprint (fewer deps = smaller attack surface)

---

## Contributor Security Rules

Every contributor (human or AI agent) must follow these rules. They are non-negotiable and enforced in code review.

### The Rules

1. **Never introduce OWASP Top 10 vulnerabilities.**  
   No command injection, XSS, SQL injection, SSRF, path traversal, or insecure deserialization. If unsure whether code is safe, ask.

2. **Validate all external input at system boundaries.**  
   Tool handler inputs, HTTP request parameters, environment variables, scraped content — validate at the boundary, trust within.

3. **Never log secrets.**  
   API keys, tokens, encryption keys, and credentials must never appear in logs, error messages, or audit events. Even in debug mode.

4. **Errors are values, never panics.**  
   Return `toolError()` or `upstreamErrorResponse()`. Never `panic()` in production paths. Never expose internal error details to clients.

5. **Encrypt sensitive data at rest.**  
   Any new persistent storage of potentially-sensitive data must use the existing encryption infrastructure (`cache.DiskCache` for cached responses, `persist.DiskStore` for TTL key/value state, or the session store for research sessions — all AES-256-GCM).

6. **Respect tenant boundaries.**  
   Any new feature touching shared state must consider multi-tenant isolation. Ask: "Can tenant A see tenant B's data?" The answer must be no.

7. **Use the SSRF-safe client for all outbound HTTP.**  
   Never use `http.DefaultClient` or `&http.Client{}` directly for fetching user-specified URLs. Always use `scraper.NewSSRFSafeClient()`.

8. **Add annotations to new tools.**  
   Read tools must declare `readOnlyAnnotations(idempotent, openWorld)`. Write tools (those that mutate server-side state or trigger an external side-effect, e.g. `memory_save`, `workspace_contribute`, `archive_source`) must use `writeAnnotations(idempotent)` instead. The `TestAllToolsHaveAnnotations` test enforces this in CI.

9. **Don't accumulate data beyond the request lifecycle.**  
   New features should not store data indefinitely. Use TTLs. If long-term storage is genuinely needed, it requires explicit opt-in + retention policy.

10. **Keep the dependency footprint minimal.**  
    Prefer standard library. Each new dependency is a supply chain risk. Justify in the PR description why stdlib isn't sufficient.

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

When AI coding agents (Claude Code, Copilot, Cursor, etc.) work on this codebase, they must follow the contributor rules above PLUS these additional constraints:

### Security-First Coding

1. **Never disable security checks** to make tests pass or code compile. Fix the underlying issue instead.

2. **Never use `--no-verify`** on git commits. Pre-commit hooks exist for a reason. If a hook fails, investigate and fix.

3. **Never generate or guess URLs** for fetching unless explicitly instructed. SSRF can be introduced through hardcoded URLs that happen to resolve to internal services.

4. **Never add backdoors, debug endpoints, or admin shortcuts** that bypass authentication or authorization. Even "temporary" ones.

5. **Never commit secrets, API keys, or credentials.** Even example values that look like real keys. Use obviously-fake placeholders: `your-key-here`.

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
- [ ] `make lint` (or `go tool golangci-lint run`) passes
- [ ] New tools have annotations + tests

---

## Vulnerability Management

### Reporting

Report security vulnerabilities privately via [GitHub Security Advisories](https://github.com/zoharbabin/web-researcher-mcp/security/advisories/new).

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

All published advisories include: CVSS v4.0 score, affected versions, CWE identifier, and mitigation guidance.

### Threat Model References

Our security controls map to MITRE ATT&CK techniques:

| Control | Mitigates |
|---------|-----------|
| SSRF protection | T1190 (Exploit Public-Facing App), T1557 (Adversary-in-the-Middle) |
| Input validation | T1059 (Command/Scripting Interpreter) |
| Content sanitization | T1059.007 (JavaScript), prompt injection vectors |
| Rate limiting | T1499 (Endpoint DoS) |
| Auth/JWKS | T1078 (Valid Accounts), T1550 (Use Alternate Auth Material) |
| Audit logging | T1070 (Indicator Removal) — structured, secret-redacted, request-ID-correlated events (incl. auth failures) aid detection & attribution. Each event carries a SHA-256 hash chain (`prev_hash`/`hash`) verifiable with `audit.VerifyChain`, so an edited or reordered log line is detectable — see [Audit Logging](#audit-logging) above. This detects tampering; it doesn't replace OS-level write protection on the audit output path. |

### Operational Incident Response vs. PSIRT

The FIRST PSIRT framework above governs *vulnerability disclosure*: someone reports a flaw, we triage and patch it before it's exploited. That is a different discipline from *operational incident response* — what an operator does when a running deployment is actively compromised, breached, or under attack — which NIST SP 800-61r2 defines as four phases: Preparation, Detection & Analysis, Containment/Eradication/Recovery, and Post-Incident Activity. This project supplies the technical primitives an operator's IR runbook (Preparation) draws on for the Containment phase; it does not run the runbook itself — same shared-responsibility split as the vendor-oversight rows above.

Concrete containment actions available today, without a code change:

- **Revoke a compromised token or key immediately** — the `persist.Store`-backed JTI revocation list (`internal/auth`) takes effect fleet-wide (Redis-backed) or per-pod (memory-backed) on the next request; rotate `ADMIN_API_KEY` to cut off admin-plane access.
- **Flush cache and sessions** — the admin cache/session flush endpoints (`internal/server`) let an operator clear potentially-poisoned cached content or a hijacked session without a restart.
- **Isolate a tenant** — `/admin/data` export/erasure can pull or purge one tenant's stored data (memory, analytics, workspace, sessions) independently of the rest of the fleet.
- **Correlate the timeline** — `internal/audit`'s structured, request-ID-correlated, secret-redacted JSON events (PodID-tagged for cross-pod correlation) are the raw material for the Detection & Analysis and Post-Incident phases; forward them to a SIEM as described under [Audit Logging](#audit-logging) above.

Preparation (an IR plan naming roles and escalation paths) and Post-Incident Activity (the retrospective, regulatory breach notification) remain operator-owned, same as the incident-response runbook already listed in the [shared-responsibility model](#shared-responsibility-model).

---

## Current Limitations

- **Native mTLS for service-to-service traffic** — the server does not implement mutual-TLS client-cert authentication itself, for either the HTTP admin plane or the Redis connection (`internal/redisbackend`); connections are encrypted in transit (`REDIS_URL` supports `rediss://`, and the HTTP server can run behind TLS) but not mutually authenticated at the application layer. This is a deliberate design choice, not a deferred gap — see [Authentication (HTTP mode)](#authentication-http-mode) for the standards research behind it. Deployments requiring mTLS today should terminate it at a surrounding service mesh or ingress; bare-VM/non-mesh deployments should use a local TLS-terminating proxy or SPIFFE/SPIRE workload identity instead of native in-binary cert handling. Status tracked on [issue #485](https://github.com/zoharbabin/web-researcher-mcp/issues/485).
- **No DPoP / sender-constrained tokens** — the server validates bearer tokens only; it does not check a `cnf.jkt` proof-of-possession claim, because embedding that claim is the issuing IdP's responsibility and no supported IdP integration currently requires it. Stolen-bearer-token replay is mitigated via short token TTLs, JTI-based revocation, per-tenant rate limiting, and audit logging (see the design-choice discussion under [Authentication (HTTP mode)](#authentication-http-mode)). Status tracked on [issue #489](https://github.com/zoharbabin/web-researcher-mcp/issues/489).

### Multi-tenant shared Kubernetes clusters

Operators running one deployment of this server behind a shared, multi-tenant HTTP endpoint (many `tenant_id` values through one process) should apply these controls in addition to the baseline hardening above:

- **Set `CACHE_ISOLATION=tenant` explicitly.** The server requires an explicit choice once `OAUTH_ISSUER_URL` is configured — `shared` is only safe for single-tenant deployments. See `docs/DEPLOYMENT.md`'s Multi-Tenancy section.
- **Encrypt shared state at rest and in transit.** If sharing Redis across replicas (`REDIS_URL`), also set `CACHE_ENCRYPTION_KEY` and use `rediss://`; Redis itself has no per-tenant ACL in this integration, so cross-pod isolation depends entirely on the application-layer `tenant_id` key prefixing plus transport/at-rest encryption, not on Redis-side access control.
- **Namespace or NetworkPolicy per trust boundary.** If different tenants have different compliance requirements (e.g., one tenant is HIPAA-regulated, another isn't), run them in separate namespaces with a `NetworkPolicy` denying pod-to-pod traffic between them — the application enforces logical tenant isolation, not network isolation.
- **Scope the admin plane to operators only.** `/admin/*` endpoints (`ADMIN_API_KEY`-gated) can read and erase any tenant's data (`/admin/data` GDPR export/erasure). In a shared cluster, restrict this route at the ingress/NetworkPolicy layer to an operator-only network path — a leaked admin key affects every tenant sharing the deployment, not just one.
- **One OAuth audience per trust domain, not per tenant.** `OAUTH_AUDIENCE` validates that a token was minted for this deployment; tenant separation itself comes from the `tenant_id` claim inside an already-validated token, not from a separate audience per tenant. Don't rely on audience scoping as a substitute for verifying `tenant_id` presence and format.
- **Size rate limits per tenant, not just per pod.** The per-tenant limiter (see Rate Limiting above) bounds one noisy tenant's blast radius on shared infrastructure; the global limiter alone does not, since a single busy tenant can otherwise starve every other tenant on the same pod.

### Architecture decisions that won't change:

- Zero global state (dependency injection via struct)
- Interface-driven design (swap implementations without touching callers)
- Read-only-default tool semantics (reads are the default; write tools exist but are non-destructive — deletion is always a separate admin endpoint, never a tool flag)
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
