# Security Architecture

Your research stays private and your infrastructure stays protected. This document describes how the server defends against the threats specific to AI-powered web research.

## Threat Model

This server operates in a unique threat environment:
1. It fetches arbitrary URLs from the internet on behalf of an LLM
2. Scraped content is returned to the LLM which may interpret it as instructions (indirect prompt injection)
3. Multiple users/agents may share a single server instance (multi-tenancy)
4. The server holds API keys with billing implications (cost abuse)

## Defense Layers

### Layer 1: SSRF Protection

Server-Side Request Forgery is the highest-severity risk for a scraping server.

**Implementation:** Custom `DialContext` on `http.Transport` — see `internal/scraper/ssrf.go`.

The approach:
1. Check hostname against blocklist (cloud metadata endpoints)
2. Resolve DNS
3. Validate ALL resolved IPs against private/reserved ranges
4. Connect directly to the resolved IP (prevents DNS rebinding)
5. Re-validate on each redirect hop (max 5 redirects)

**Blocked IP Ranges:**

| Range | Reason |
|-------|--------|
| `127.0.0.0/8` | Loopback |
| `10.0.0.0/8` | RFC 1918 private |
| `172.16.0.0/12` | RFC 1918 private |
| `192.168.0.0/16` | RFC 1918 private |
| `169.254.0.0/16` | Link-local / cloud metadata (AWS, GCP, Azure IMDS) |
| `100.64.0.0/10` | Carrier-grade NAT |
| `0.0.0.0/8` | Current network |
| `192.0.0.0/24` | IETF protocol assignments |
| `192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24` | Documentation |
| `198.18.0.0/15` | Benchmark testing |
| `224.0.0.0/4` | Multicast |
| `240.0.0.0/4` | Reserved |
| `::1/128` | IPv6 loopback |
| `fc00::/7` | IPv6 ULA |
| `fe80::/10` | IPv6 link-local |
| `ff00::/8` | IPv6 multicast |
| `::/128` | IPv6 unspecified |

**Blocked Hostnames:**
- `metadata.google.internal`
- `metadata.azure.com`
- `169.254.169.254` (AWS/GCP/Azure IMDS)
- `instance-data`

**DNS Rebinding Prevention:**
- Resolve once, connect to the resolved IP directly
- Re-validate on every redirect hop
- Max redirect depth: 5

**Configuration:**
- `ALLOW_PRIVATE_IPS=true` — Disable for local development only
- `ALLOWED_DOMAINS=a.com,b.com` — Whitelist mode for enterprise

---

### Layer 2: Authentication & Authorization (HTTP Transport)

**OAuth 2.1 Resource Server**

```
Client → [Authorization: Bearer <token>] → MCP Server
                                              │
                                              ▼
                                      ┌─────────────┐
                                      │ Validate JWT │
                                      │ - Signature  │
                                      │ - iss, aud   │
                                      │ - exp, nbf   │
                                      │ - scope      │
                                      └──────┬──────┘
                                             │
                                      ┌──────▼──────┐
                                      │Extract claims│
                                      │ - sub (user) │
                                      │ - tenant_id  │
                                      │ - session_id │
                                      └─────────────┘
```

**JWKS Management:**
- Fetch from `{issuerURL}/.well-known/jwks.json`
- Cache with auto-refresh (configurable interval, default 1 hour)
- Graceful degradation: serve from cache if JWKS endpoint is down
- Implementation: custom RS256 validation (no external JWT library dependency)

**Token Requirements:**
- Algorithm: RS256 or ES256 (reject HS256 from external issuers)
- Required claims: `iss`, `aud`, `exp`, `sub`
- Audience must match `OAUTH_AUDIENCE` env var
- Issuer must match `OAUTH_ISSUER_URL` env var

**STDIO Transport:**
- No authentication. Credentials come from environment.
- The calling process (Claude Code, Cursor) is trusted.

---

### Layer 3: Session Isolation

**Per-Tenant Data Boundaries:**

```
┌─────────────────────────────────┐
│          Tenant A               │
│  ┌──────────┐  ┌────────────┐  │
│  │ Session 1│  │ Session 2  │  │
│  │ cache ns │  │ cache ns   │  │
│  │ seq state│  │ seq state  │  │
│  └──────────┘  └────────────┘  │
└─────────────────────────────────┘
┌─────────────────────────────────┐
│          Tenant B               │
│  ┌──────────┐                   │
│  │ Session 3│  (isolated)       │
│  └──────────┘                   │
└─────────────────────────────────┘
```

**Rules:**
1. Sequential search sessions are keyed by `{tenantID}:{sessionID}` — never shared
2. Cache can be shared for public content (search results, scraped pages are not user-specific)
3. Audit logs include tenant ID for filtering

**Shared vs. Isolated Cache:**
- Search results: SHARED (same query = same results regardless of who asked)
- Scraped pages: SHARED (public URLs return same content)
- Sequential search state: ISOLATED (per-session, per-tenant)
- Rate limit counters: ISOLATED (per-tenant)

---

### Layer 4: Content Security

**Sanitization Pipeline (applied to all scraped content before return):**

```
Raw HTML/Content
    │
    ▼
┌────────────────────────────────────┐
│ 1. Strip dangerous HTML            │
│    - <script>, <style>, <iframe>   │
│    - <object>, <embed>, <applet>   │
│    - event handlers (onclick, etc) │
│    - data: URIs                    │
│    - javascript: URIs              │
└────────────────┬───────────────────┘
                 │
    ▼
┌────────────────────────────────────┐
│ 2. Remove hidden content           │
│    - display:none / visibility:    │
│      hidden (inline CSS)           │
│    - font-size:0 / color matching  │
│      background                    │
│    - HTML comments                 │
│    - Zero-width characters         │
│      (U+200B, U+200C, U+200D,     │
│       U+FEFF, U+2060)             │
└────────────────┬───────────────────┘
                 │
    ▼
┌────────────────────────────────────┐
│ 3. Size enforcement                │
│    - Max 50KB per source           │
│    - Max 300KB total               │
│    - Truncate at paragraph boundary│
│    - Set truncated flag            │
└────────────────┬───────────────────┘
                 │
    ▼
┌────────────────────────────────────┐
│ 4. Content boundary marking        │
│    [BEGIN EXTERNAL CONTENT]        │
│    ...content...                   │
│    [END EXTERNAL CONTENT]          │
└────────────────────────────────────┘
```

**Prompt Injection Mitigations:**
- Content boundary markers in structured output (not in content itself — that would be easily bypassed)
- The `contentType` field signals to the client that content is untrusted external data
- Response metadata (tool name, schema) is never derived from scraped content
- Size limits prevent context flooding attacks

---

### Layer 5: Rate Limiting

**Three-Tier Architecture:**

| Tier | Scope | Default | Purpose |
|------|-------|---------|---------|
| Global | Per-server | 1000 req/s | Infrastructure protection |
| Per-Tenant | Per JWT `sub` | 120 req/min | Fair use |
| Per-Session | Per MCP session | 5 concurrent | Backpressure |

**Implementation:**
- Global: `golang.org/x/time/rate` token bucket
- Per-Tenant: `sync.Map[tenantID]*rate.Limiter` with TTL cleanup
- Per-Session: Buffered channel as semaphore

**Cost Quotas:**
- Track Google API call count per tenant per day
- Configurable daily limit (default: 5000 queries/day)
- Reject with informative error when exceeded

**Sub-Agent Handling:**
When a single agent spawns many parallel tool calls:
- Queue excess requests (up to buffer limit)
- Apply per-session concurrency limit
- Return 429 with `Retry-After` header when queue is full

---

### Layer 6: Circuit Breaker

Protects against cascading failures when upstream APIs are down.

**States:** CLOSED → OPEN → HALF_OPEN → CLOSED

**Configuration (per-provider breaker):**
- Failure threshold: 5 consecutive failures
- Reset timeout: 60 seconds
- Half-open attempts: 1

**Configuration (multi-provider router breaker):**
- Failure threshold: 3 consecutive failures
- Reset timeout: 30 seconds
- Half-open attempts: 1

**Per-Provider Breakers:**
- Each search provider (Google, Brave, Serper, SearXNG, SearchAPI) gets an independent circuit breaker
- When using multi-provider routing (`SEARCH_ROUTING`), the router adds a second breaker layer with tighter thresholds for faster failover
- Failures in one provider don't affect others
- Scraping (per domain): optional, prevent hammering broken sites

---

### Layer 7: Audit Logging

**Every tool invocation produces an audit record.**

See `internal/audit/logger.go` for the canonical `AuditEvent` struct. Key fields include: timestamp, tenant/user/session IDs, tool name, request ID, duration, success/error status, and extensible metadata.

**Storage:**
- Default: structured log to stderr (slog JSON)
- Production: ship to SIEM via syslog/fluentd
- Retention: 90 days (SOC 2 minimum)

**What is NOT logged:**
- Raw query text (PII risk)
- Scraped content (too large, PII risk)
- Full request parameters (may contain PII)
- Only parameter hashes for correlation

---

## Encryption

### At Rest
- Cache on disk: AES-256-GCM encryption (configurable)
- Key: 64-char hex from `CACHE_ENCRYPTION_KEY` env var
- If unset: disk cache is plaintext (acceptable for STDIO single-user mode)

### In Transit
- All outbound HTTP: TLS 1.2+ (Go's default)
- HTTP transport: TLS termination at load balancer or direct
- No sensitive data in URL query parameters

### FIPS Compliance (Optional)
- Build with `GOEXPERIMENT=boringcrypto` for FIPS 140-2 validated crypto
- Affects: TLS, AES, SHA, RSA operations

---

## Configuration Security

**Sensitive Environment Variables:**
- API keys, OAuth secrets, encryption keys
- Never logged (even at debug level)
- Validated at startup with pattern matching
- Clear error messages on format violation (without echoing the value)

**Startup Validation:**
- Pattern-check all known env vars
- Warn (don't exit) on format issues — allows MCP handshake even with bad credentials
- Tools fail gracefully at call time with actionable error messages

---

## Compliance Frameworks

### SOC 2 Type II

| Criterion | How We Satisfy |
|-----------|----------------|
| **CC6.1** Access Control | OAuth 2.1 middleware, per-tenant RBAC via JWT scopes |
| **CC6.2** Logical Access | Session isolation, cache namespace per tenant |
| **CC6.6** Threat Mitigation | SSRF protection, rate limiting, circuit breakers |
| **CC7.1** Monitoring | Prometheus metrics, structured audit logs |
| **CC7.3** Incident Response | Structured error types, trace IDs for correlation |
| **CC8.1** Change Management | Git history, CI/CD pipeline, tagged releases |
| **A1.2** Availability | Health checks, HPA scaling, circuit breakers |

### GDPR

| Right | Status |
|-------|--------|
| **Access** (Art. 15) | Not yet implemented. Planned: `GET /users/{id}/data` endpoint |
| **Erasure** (Art. 17) | Not yet implemented. Planned: `DELETE /users/{id}/data` endpoint |
| **Portability** (Art. 20) | Not yet implemented. Planned: JSON export of user-associated data |
| **Restriction** (Art. 18) | Set `CACHE_ISOLATION=tenant` to scope all cache keys by tenant ID — prevents cross-tenant cache access |

Data minimization (implemented): audit logs store parameter hashes (not raw queries), no persistent PII storage beyond cache TTLs.

### FedRAMP (Moderate Baseline)

| Control | Implementation |
|---------|----------------|
| **SC-8** Transmission Confidentiality | TLS 1.2+ on all connections |
| **SC-13** Cryptographic Protection | FIPS 140-2 via `GOEXPERIMENT=boringcrypto` |
| **SC-28** Protection at Rest | AES-256-GCM for disk cache |
| **AC-3** Access Enforcement | OAuth middleware on all HTTP endpoints |
| **AU-2** Audit Events | All tool calls, auth failures, config changes |
| **SI-2** Flaw Remediation | Automated `govulncheck` in CI |

```bash
# FIPS-compliant build
GOEXPERIMENT=boringcrypto CGO_ENABLED=0 \
  go build -ldflags="-s -w -X main.version=${VERSION}" \
  -o web-researcher-mcp ./cmd/web-researcher-mcp
```

### Multi-Tenancy Isolation

| Boundary | Shared | Isolated |
|----------|--------|----------|
| Binary code, HTTP client pool | Yes | — |
| Public content cache (search results, scraped pages) | Yes | — |
| Rate limit counters | — | Per-tenant |
| Sequential search sessions | — | Per-tenant:session |
| Search history | — | Per-tenant |
| Audit logs | — | Filterable by tenant |

**Note:** Set `CACHE_ISOLATION=tenant` to enforce per-tenant cache isolation. When enabled, all cache keys are prefixed with the authenticated tenant ID, preventing cross-tenant cache access. Default is `shared` (cache keys are content-addressed, identical queries share results across tenants). For search results sharing is safe (same query returns same results), but scrape cache may contain tenant-specific content — use `tenant` mode for strict data isolation deployments.

### Supply Chain Security

```bash
govulncheck ./...        # Audit for known vulnerabilities
go mod verify            # Pin dependency hashes
cyclonedx-gomod mod -json -output sbom.json  # Generate SBOM
```

All dependencies: actively maintained, no unpatched CVEs, permissive licenses (MIT/Apache/BSD), >1000 stars or official/stdlib.
