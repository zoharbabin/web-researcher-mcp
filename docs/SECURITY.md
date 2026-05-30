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

Matched case-insensitively as an exact hostname or a dot-bounded suffix, so `svc.cluster.local` matches `foo.svc.cluster.local` but NOT `svc.cluster.local.evil.com` (a different registrable domain). See `blockedHostnames` and `isBlockedHostname` in `internal/scraper/ssrf.go`.

- `metadata.google.internal` (GCP IMDS)
- `metadata.azure.com` (Azure IMDS)
- `metadata.tencentyun.com` (Tencent Cloud IMDS)
- `169.254.169.254` (AWS / Azure / GCP / DigitalOcean / OpenStack link-local)
- `192.0.0.192` (Oracle Cloud metadata)
- `100.100.100.200` (Alibaba Cloud metadata)
- `instance-data`
- `kubernetes.default.svc` (in-cluster API server)
- `svc.cluster.local` (any in-cluster service, matched as a suffix)

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

**Scope Enforcement (per-tool authorization):**

Scope enforcement is opt-in via `ENFORCE_SCOPES=true` and remains permissive by design. The gate parses the union of the OAuth `scope` (space-delimited) and `scp` (array or space-delimited) claims, attaches them to the request context, and applies the policy in `Middleware.EnforceScopes` (`internal/auth/middleware.go`):

- `ENFORCE_SCOPES=false` (default) — scope claims are ignored; every authenticated caller may invoke every tool.
- `ENFORCE_SCOPES=true`, token carries **no** scope claim — allowed (backward-compatible: tokens issued before scopes existed keep working).
- `ENFORCE_SCOPES=true`, token **carries** a scope claim — the caller must hold one of `tool:*` (wildcard), `tool:<toolName>` (exact), or the coarse-grained `research` scope; AND every entry in `REQUIRED_SCOPES` (if configured) must be present. Otherwise the call is rejected.

This fails closed only for present-but-insufficient scopes — it never silently downgrades a token that simply predates scope issuance. The gate is wired as an SDK receiving-middleware (registered in `main.go`) inside the HTTP-mode block only; STDIO is unaffected.

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
- File output: set `AUDIT_OUTPUT_PATH` (JSONL). The active file is rotated to a timestamped sibling once it reaches `AUDIT_MAX_BYTES` (default 100 MB); rotation runs on the audit processor goroutine and never blocks a `Log()` call.
- Production: ship to SIEM via syslog/fluentd
- Retention: rotated files older than `AUDIT_RETENTION_DAYS` are deleted on startup and hourly. The default is 180 days; any non-zero value is clamped to `[180, 3650]` per NIS2/HGB retention floors. `0` disables cleanup.

**What is NOT logged (by default):**
- **Raw query text** — omitted unless `AUDIT_INCLUDE_REQUEST_BODY=true`. When that flag is false (default), only a length/hash is recorded, never the literal query.
- Scraped content (too large, PII risk)
- Full request parameters (may contain PII)

**Secret redaction:** audit metadata and upstream error messages pass through `audit.MaskSecrets` (`internal/audit/mask.go`) before they are written. It redacts Google (`AIza…`), OpenAI/Anthropic (`sk-…`), Brave (`BSA…`) keys, `Bearer` tokens, sensitive query-string params (`api_key=`, `token=`, `secret=`, `password=`, `key=`, …), and bare 64-hex key material. This is defense-in-depth so a credential echoed back by an upstream provider never reaches a sink or an LLM-facing error.

**Request correlation:** every HTTP request is assigned a correlation ID by the transport ingress middleware (adopting a sanitized inbound `X-Request-Id`, else the W3C `traceparent` trace-id, else a fresh UUIDv4). All audit events for one tool call share that `RequestID`, and it is echoed back on the response `X-Request-Id` header.

---

## Encryption

### At Rest
- Cache on disk, sessions, and the persist store: AES-256-GCM encryption (configurable)
- Key: 64-char hex from `CACHE_ENCRYPTION_KEY` env var
- If unset: disk cache is plaintext (acceptable for STDIO single-user mode)
- **Key rotation:** set `CACHE_ENCRYPTION_KEY_PREV` to the prior 64-hex key for zero-downtime rotation. The disk cache and session/persist stores decrypt-fall-back to the previous key and lazily re-encrypt with the current key on read, so a key swap never strands existing data.
- **AAD binding:** each on-disk blob binds its key (SHA-256 of the logical key) as GCM additional authenticated data, so a ciphertext cannot be moved to a different key's file.

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

## HTTP Transport Hardening

All controls in this section apply **only in HTTP mode** (`PORT` set). STDIO mode does not start an `http.Server` and is unaffected. Defaults are permissive so legitimate long research responses are never truncated. Implementation: `internal/server/server.go`.

### Connection & Body Limits

| Control | Variable | Default | Purpose |
|---------|----------|---------|---------|
| Header read timeout | `HTTP_READ_HEADER_TIMEOUT` | `5s` | Primary slowloris guard |
| Request read timeout | `HTTP_READ_TIMEOUT` | `30s` | Bounds full-request read |
| Response write timeout | `HTTP_WRITE_TIMEOUT` | `0` (unlimited) | Kept permissive so long scrape/research responses are never truncated |
| Idle timeout | `HTTP_IDLE_TIMEOUT` | `120s` | Frees idle keep-alive connections |
| Max header bytes | `HTTP_MAX_HEADER_BYTES` | `1 MB` | Guards against header-flood memory exhaustion |
| Max request body | `MAX_REQUEST_BODY_BYTES` | `10 MB` | `/mcp` and `/admin` bodies over the cap are rejected with `413` via `http.MaxBytesReader` |

### Response Security Headers

Applied to every HTTP response by the `securityHeaders` middleware. The three configurable headers omit themselves when their value is empty.

| Header | Value | Configurable via |
|--------|-------|------------------|
| `X-Content-Type-Options` | `nosniff` | (fixed) |
| `X-Frame-Options` | `DENY` | (fixed) |
| `Cache-Control` | `no-store` | (fixed) |
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` | (fixed) |
| `Content-Security-Policy` | `default-src 'none'; frame-ancestors 'none'` | `HTTP_CSP` |
| `Referrer-Policy` | `no-referrer` | `HTTP_REFERRER_POLICY` |
| `Permissions-Policy` | `geolocation=(), camera=(), microphone=()` | `HTTP_PERMISSIONS_POLICY` |

### CORS

The `corsMiddleware` reflects an allowed `Origin`. With a non-empty `ALLOWED_ORIGINS` it reflects only listed origins (or any when `*` is listed). With an **empty** `ALLOWED_ORIGINS` the behavior is governed by `CORS_STRICT`:

- `CORS_STRICT=false` (current default) — permissive: reflect any `Origin`.
- `CORS_STRICT=true` — fail-closed: deny all cross-origin requests.

It never reflects the literal `*` together with credentials. A future release will flip the default to fail-closed — see [MIGRATION.md](MIGRATION.md).

### Pre-Auth Per-IP Rate Limit

`RATE_LIMIT_PER_IP` (default `0` = disabled) enforces a per-client-IP request ceiling as the **outermost** middleware, so an unauthenticated flood is shed before it reaches auth or the mux. When `TRUST_PROXY=true`, the client IP is read from the leftmost `X-Forwarded-For` entry (for use behind a trusted load balancer); otherwise `RemoteAddr` is used, preventing spoofed-IP bypass.

---

## Persistence

Two HTTP-mode subsystems durably persist state across restarts through a single `persist.Store` interface (`internal/persist/store.go`):

- **Token revocation (H2)** — revoked JTIs are written through to the store with a TTL matching natural token expiry, so a revoked token stays revoked across restarts. The in-memory set remains authoritative; the store is consulted as an additional source of truth (a JTI is revoked if present in **either** layer — fail-closed).
- **Daily quota counters (H7)** — enabled by `RATE_LIMIT_PERSIST=true`, so per-tenant daily quotas survive restarts.

The default implementation is the encrypted-disk pattern generalized from the session store: AES-256-GCM, atomic temp-file-and-rename writes, `0600` permissions, an 8-byte big-endian expiry prefix, SHA-256-hashed filenames, and key-bound GCM AAD. Local (memory) and disk backends behave identically — no drift between STDIO and HTTP. `REDIS_URL` is a documented **no-op** reserved for a future `RedisStore` that will satisfy this same interface; setting it changes no behavior today, and no `go-redis` dependency is in the build.

---

## Compliance Frameworks

### MITRE ATT&CK Technique Coverage

How the server's controls counter the ATT&CK techniques most relevant to an internet-facing scraping service.

| Tactic | Technique | ID | Mitigation in this server |
|--------|-----------|----|----|
| Reconnaissance | Active Scanning / internal service discovery | T1595 | SSRF guard blocks private/reserved IP ranges and in-cluster hostnames (`svc.cluster.local`, `kubernetes.default.svc`) |
| Initial Access | Exploit Public-Facing Application | T1190 | HTTP timeouts, `MAX_REQUEST_BODY_BYTES`, header-byte cap, per-IP pre-auth rate limit |
| Credential Access | Unsecured Credentials in cloud metadata | T1552.005 | SSRF blocklist for AWS/GCP/Azure/Oracle/Alibaba/Tencent IMDS endpoints + link-local IP blocking |
| Credential Access | Steal Application Access Token | T1528 | RS256 JWT validation (iss/aud/exp/nbf), revocation list, OAuth scope gate |
| Defense Evasion | DNS rebinding / redirect to internal host | T1090 | Resolve-once-connect-to-IP, re-validation on every redirect hop (max 5) |
| Impact | Endpoint/Network Denial of Service | T1499 / T1498 | Slowloris-guarding timeouts, per-IP and per-tenant rate limits, circuit breakers, body/header caps |
| Impact | Resource Hijacking (cost abuse) | T1496 | Per-tenant daily quota (optionally persisted), global rate limit |
| Collection / Exfiltration | Indirect prompt injection via scraped content | T1059 (analog) | Content sanitization pipeline, boundary markers, `contentType` untrusted-data signal; raw mode is opt-in and clearly flagged |
| Defense Evasion | Credential leakage in logs/errors | T1552 (analog) | `audit.MaskSecrets` redacts keys/tokens before any sink |

### NIST Cybersecurity Framework 2.0 Crosswalk

| CSF 2.0 Function | Outcome | Implementation |
|------------------|---------|----------------|
| **GOVERN (GV)** | Roles, policy, supply chain | PSIRT process ([SECURITY.md](../SECURITY.md)), `govulncheck`/`go mod verify`/SBOM in CI, documented design rules |
| **IDENTIFY (ID)** | Asset & risk awareness | This threat model, `DATA_REGION` residency labeling, per-tool audit inventory |
| **PROTECT (PR)** | Access control & data security | OAuth 2.1 + scope gate, SSRF guard, AES-256-GCM at rest with key rotation, TLS in transit, security headers, CORS, rate limits |
| **DETECT (DE)** | Continuous monitoring | Structured audit logs with request correlation IDs, Prometheus metrics, circuit-breaker state |
| **RESPOND (RS)** | Incident handling | PSIRT triage with CVSS v4.0/CWE, token revocation (persisted), structured error taxonomy for triage |
| **RECOVER (RC)** | Resilience & restoration | Graceful shutdown with buffer drain, encrypted persist store survives restarts, zero-downtime key rotation |

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
