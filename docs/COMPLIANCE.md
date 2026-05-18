# Compliance & Governance

## Compliance Frameworks

This document specifies how the Go MCP server satisfies requirements from SOC 2, GDPR, FedRAMP, and enterprise security standards.

---

## SOC 2 Type II

### Trust Service Criteria Mapping

| Criterion | How We Satisfy |
|-----------|----------------|
| **CC6.1** Access Control | OAuth 2.1 middleware, per-tenant RBAC via JWT scopes |
| **CC6.2** Logical Access | Session isolation, cache namespace per tenant |
| **CC6.3** System Boundaries | Defined boundary: server binary + cache + Redis |
| **CC6.6** Threat Mitigation | SSRF protection, rate limiting, circuit breakers |
| **CC7.1** Monitoring | Prometheus metrics, structured audit logs |
| **CC7.2** Anomaly Detection | Rate limit alerts, unusual API usage patterns |
| **CC7.3** Incident Response | Structured error types, trace IDs for correlation |
| **CC8.1** Change Management | Git history, CI/CD pipeline, tagged releases |
| **A1.2** Availability | Health checks, HPA scaling, circuit breakers |
| **PI1.1** Processing Integrity | Input validation, output schemas, deterministic behavior |

### Audit Log Requirements

Every tool invocation MUST produce an audit record containing:

```go
type AuditRecord struct {
    // Who
    TenantID  string    `json:"tenant_id"`
    UserID    string    `json:"user_id"`
    SessionID string    `json:"session_id"`
    
    // What
    ToolName   string   `json:"tool_name"`
    ParamsHash string   `json:"params_hash"` // SHA-256, not raw params
    
    // When
    Timestamp  time.Time `json:"timestamp"`
    LatencyMs  int64     `json:"latency_ms"`
    
    // Where
    IPAddress  string   `json:"ip_address"`
    UserAgent  string   `json:"user_agent"`
    ServerID   string   `json:"server_id"` // Pod/instance identifier
    
    // Result
    Success      bool   `json:"success"`
    ErrorType    string `json:"error_type,omitempty"`
    ResponseSize int    `json:"response_size"`
    CacheHit     bool   `json:"cache_hit"`
    
    // Correlation
    TraceID    string   `json:"trace_id"`
}
```

**Storage:** Append-only structured log (JSON lines). Ship to SIEM.
**Retention:** 90 days minimum, configurable.
**Immutability:** Application cannot modify past records.

---

## GDPR Compliance

### Data Categories

| Data Type | Where Stored | Legal Basis | Retention |
|-----------|-------------|-------------|-----------|
| Search queries | Cache (ephemeral) | Legitimate interest | Cache TTL (30 min) |
| Scraped content | Cache (ephemeral) | Legitimate interest | Cache TTL (1 hour) |
| Session state | Memory/Redis | Contract performance | 30 min inactivity |
| Audit logs | Log store | Legitimate interest | 90 days |
| JWT claims | In-memory only | Contract performance | Request duration |

### Data Subject Rights

| Right | Implementation |
|-------|----------------|
| **Access** (Art. 15) | `GET /api/v1/users/{id}/data` — returns all stored data for a user |
| **Erasure** (Art. 17) | `DELETE /api/v1/users/{id}/data` — purges cache, sessions, audit logs |
| **Portability** (Art. 20) | JSON export of all user-associated data |
| **Restriction** (Art. 18) | Disable caching for specific tenant via config |

### Data Minimization

- Cache stores content hashes for deduplication, not redundant copies
- Audit logs store parameter hashes, NOT raw query text
- No persistent storage of personal data beyond cache TTLs
- Option to disable ALL caching per tenant (`CACHE_DISABLED_TENANTS`)

### International Data Transfer

- Default: all processing in-region (deploy regionally)
- Google API calls route through regional endpoints where available
- No third-party data sharing beyond configured search providers

---

## FedRAMP (Moderate Baseline)

### Cryptographic Controls

| Control | Implementation |
|---------|----------------|
| **SC-8** Transmission Confidentiality | TLS 1.2+ on all connections |
| **SC-13** Cryptographic Protection | FIPS 140-2 via `GOEXPERIMENT=boringcrypto` |
| **SC-28** Protection at Rest | AES-256-GCM for disk cache (CACHE_ENCRYPTION_KEY) |
| **SC-12** Key Management | Env vars from secret manager, rotatable |

### Access Control

| Control | Implementation |
|---------|----------------|
| **AC-2** Account Management | JWT-based, delegated to IdP |
| **AC-3** Access Enforcement | OAuth middleware on all HTTP endpoints |
| **AC-7** Unsuccessful Logon | Rate limit on auth (10 req/s) |
| **AC-17** Remote Access | TLS mutual auth option |

### Audit & Accountability

| Control | Implementation |
|---------|----------------|
| **AU-2** Audit Events | All tool calls, auth failures, config changes |
| **AU-3** Content of Records | See AuditRecord struct above |
| **AU-6** Audit Review | Prometheus alerts on anomalies |
| **AU-9** Protection of Audit Info | Append-only, separate from application data |
| **AU-12** Audit Generation | Structured logging (slog) with trace correlation |

### System & Information Integrity

| Control | Implementation |
|---------|----------------|
| **SI-2** Flaw Remediation | Automated dependency scanning (govulncheck) |
| **SI-3** Malicious Code Protection | SSRF blocking, content sanitization |
| **SI-4** Monitoring | Prometheus + alerting |
| **SI-10** Input Validation | Typed schemas, Zod-equivalent validation |

### Build Requirements for FedRAMP

```bash
# FIPS-compliant build
GOEXPERIMENT=boringcrypto CGO_ENABLED=0 \
  go build -ldflags="-s -w -X main.version=${VERSION}" \
  -o web-researcher-mcp ./cmd/web-researcher-mcp

# Verify FIPS
go test -v ./internal/auth/ -run TestFIPSCrypto
```

---

## Multi-Tenancy Isolation

### Isolation Boundaries

```
┌─────────────────────────────────────────────┐
│               Shared (safe)                  │
│  - Binary code                               │
│  - Google API client (shared HTTP pool)      │
│  - Chromedp browser pool (shared)            │
│  - Public content cache (search results,     │
│    scraped pages — same URL = same content)  │
└─────────────────────────────────────────────┘

┌─────────────────────────────────────────────┐
│           Per-Tenant (isolated)              │
│  - Rate limit counters                       │
│  - Daily quota counters                      │
│  - Sequential search sessions                │
│  - Search history (search://recent)          │
│  - Audit logs (filterable)                   │
│  - API key usage tracking                    │
└─────────────────────────────────────────────┘

┌─────────────────────────────────────────────┐
│           Per-Session (ephemeral)            │
│  - Request context                           │
│  - In-flight tool execution state            │
│  - SSE connection state                      │
└─────────────────────────────────────────────┘
```

### Why Public Cache is Shared

Search results and scraped web pages are NOT user-specific data. The query "golang tutorials" returns the same results regardless of who asks. Sharing this cache:
- Reduces API costs (fewer Google/Brave calls)
- Improves latency for all tenants
- Does NOT leak private data (all content is publicly accessible)

If a tenant requires isolated caching (compliance), set `CACHE_ISOLATION=tenant` to prefix all cache keys with tenant ID.

### Session Hijacking Prevention

| Threat | Mitigation |
|--------|------------|
| Token theft via XSS | Not applicable (MCP clients are not browsers) |
| Token replay | Short-lived tokens (15 min), refresh rotation |
| Session fixation | Server-generated session IDs only |
| MITM | TLS required for HTTP transport |
| Cross-session access | All state keyed by `{tenantID}:{sessionID}` |

---

## Supply Chain Security

### Dependency Management

```bash
# Audit for known vulnerabilities
govulncheck ./...

# Pin dependency hashes
go mod verify

# Minimize dependencies
# Target: <20 direct dependencies
```

### Build Reproducibility

```bash
# Deterministic builds (same source → same binary)
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" ./cmd/web-researcher-mcp
```

### SBOM Generation

```bash
# CycloneDX SBOM
go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@latest
cyclonedx-gomod mod -json -output sbom.json
```

---

## Security Headers (HTTP Mode)

```go
func securityHeaders(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("Cache-Control", "no-store")
        w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
        next.ServeHTTP(w, r)
    })
}
```

---

## Incident Response

### Error Classification

| Severity | Condition | Action |
|----------|-----------|--------|
| Critical | Auth bypass detected | Log + alert + block IP |
| High | SSRF attempt | Log + alert |
| Medium | Rate limit exceeded | Log + 429 |
| Low | Invalid input | Log + 400 |

### Trace Correlation

Every request gets a UUID trace ID. Propagated through:
- `context.Context` (internal)
- `X-Trace-ID` response header (external)
- All log entries for the request

```go
ctx = context.WithValue(ctx, traceIDKey, uuid.New().String())
logger.Info("tool_call", "trace_id", traceID, "tool", "web_search", ...)
```

---

## Configuration Validation

At startup, validate all environment variables:

```go
type ValidationRule struct {
    Name        string
    Required    bool
    Pattern     *regexp.Regexp
    Description string
    Example     string
    Validate    func(string) error
}
```

**Behavior on invalid config:**
- Required vars missing → log error, server starts, tools fail at call time
- Optional vars with bad format → log warning, use defaults
- Never exit on startup (allows MCP handshake for health checks)
- Clear, actionable error messages with examples

---

## Revocation & Access Control

### Token Revocation

```
POST /revoke
Authorization: Bearer <admin-token>
Content-Type: application/json

{"token": "<token-to-revoke>"}
```

Implementation:
- Add token JTI to Redis blacklist
- TTL on blacklist entry = remaining token lifetime
- Check blacklist on every request (O(1) Redis lookup)

### Admin Operations

Protected by `CACHE_ADMIN_KEY` header:
- `DELETE /admin/cache` — Flush all cache
- `DELETE /admin/sessions` — Kill all sessions
- `DELETE /admin/tenant/{id}` — Purge tenant data
- `GET /admin/audit?tenant_id=...` — Query audit logs
