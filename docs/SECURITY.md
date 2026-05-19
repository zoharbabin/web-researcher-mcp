# Security Architecture

## Threat Model

This MCP server operates in a unique threat environment:
1. It fetches arbitrary URLs from the internet on behalf of an LLM
2. Scraped content is returned to the LLM which may interpret it as instructions (indirect prompt injection)
3. Multiple users/agents may share a single server instance (multi-tenancy)
4. The server holds API keys with billing implications (cost abuse)

## Defense Layers

### Layer 1: SSRF Protection

Server-Side Request Forgery is the highest-severity risk for a scraping server.

**Implementation: Custom `DialContext` on `http.Transport`**

```go
// ssrf.go — the canonical Go pattern
func newSSRFSafeTransport(allowPrivate bool) *http.Transport {
    return &http.Transport{
        DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
            host, port, _ := net.SplitHostPort(addr)
            
            // 1. Protocol validation (already handled by http.Client)
            // 2. Hostname blocklist
            if isBlockedHostname(host) {
                return nil, ErrSSRFBlocked
            }
            
            // 3. DNS resolution
            ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
            if err != nil {
                return nil, err
            }
            
            // 4. Check ALL resolved IPs against deny-list
            for _, ip := range ips {
                if !allowPrivate && isPrivateIP(ip.IP) {
                    return nil, ErrSSRFBlocked
                }
            }
            
            // 5. Connect to the first valid IP (prevents DNS rebinding)
            return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(
                ctx, network, net.JoinHostPort(ips[0].IP.String(), port),
            )
        },
        // Redirect validation happens in CheckRedirect
    }
}
```

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
2. Search history (`search://recent` resource) is per-tenant
3. Cache can be shared for public content (search results, scraped pages are not user-specific)
4. Audit logs include tenant ID for filtering

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
| Per-Tenant | Per JWT `sub` | 30 req/min | Fair use |
| Per-Session | Per MCP session | 5 concurrent | Backpressure |

**Implementation:**
- Global: `golang.org/x/time/rate` token bucket
- Per-Tenant: `sync.Map[tenantID]*rate.Limiter` with TTL cleanup
- Per-Session: Buffered channel as semaphore

**Cost Quotas:**
- Track Google API call count per tenant per day
- Configurable daily limit (default: 1000 queries/day)
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

**Configuration:**
- Failure threshold: 5 consecutive failures
- Reset timeout: 60 seconds
- Half-open attempts: 1

**Per-Provider Breakers:**
- Google PSE: separate breaker
- Brave Search: separate breaker
- Scraping (per domain): optional, prevent hammering broken sites

---

### Layer 7: Audit Logging

**Every tool invocation produces an audit record:**

```go
type AuditEntry struct {
    Timestamp   time.Time `json:"timestamp"`
    TenantID    string    `json:"tenantId"`
    UserID      string    `json:"userId"`
    SessionID   string    `json:"sessionId"`
    TraceID     string    `json:"traceId"`
    ToolName    string    `json:"toolName"`
    ParamsHash  string    `json:"paramsHash"`  // SHA-256 of params (not raw params — PII)
    ResponseSize int      `json:"responseSize"`
    LatencyMs   int64     `json:"latencyMs"`
    Success     bool      `json:"success"`
    ErrorType   string    `json:"errorType,omitempty"`
    IPAddress   string    `json:"ipAddress"`
    UserAgent   string    `json:"userAgent"`
}
```

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
