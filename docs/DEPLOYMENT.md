# Deployment Guide

This guide covers how to build, configure, and run web-researcher-mcp — whether locally on your machine or deployed to a server. Most users only need the Quick Start in the README; this doc is for production deployments and advanced configuration.

## Build

```bash
# Development build
go build -o web-researcher-mcp ./cmd/web-researcher-mcp

# Production build (static, stripped)
CGO_ENABLED=0 go build -ldflags="-s -w" -o web-researcher-mcp ./cmd/web-researcher-mcp

# FIPS-compliant build (government/enterprise)
GOEXPERIMENT=boringcrypto CGO_ENABLED=0 go build -ldflags="-s -w" -o web-researcher-mcp ./cmd/web-researcher-mcp

# Cross-compile
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o web-researcher-mcp-linux-amd64 ./cmd/web-researcher-mcp
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o web-researcher-mcp-darwin-arm64 ./cmd/web-researcher-mcp
```

Output: single static binary. No runtime dependencies.

## Transport Modes

### STDIO (Default — Claude Code, Cursor, Claude Desktop)

```bash
# Direct
./web-researcher-mcp

# With env
GOOGLE_CUSTOM_SEARCH_API_KEY=AIza... GOOGLE_CUSTOM_SEARCH_ID=017... ./web-researcher-mcp
```

The server reads MCP JSON-RPC from stdin, writes to stdout. No port, no network.

**Claude Code config** (`~/.claude.json`):
```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "/path/to/web-researcher-mcp",
      "env": {
        "GOOGLE_CUSTOM_SEARCH_API_KEY": "AIza...",
        "GOOGLE_CUSTOM_SEARCH_ID": "017...",
        "SEARCH_PROVIDER": "brave",
        "BRAVE_API_KEY": "BSA..."
      }
    }
  }
}
```

### HTTP (Multi-client, web apps)

```bash
PORT=3000 \
OAUTH_ISSUER_URL=https://auth.example.com \
OAUTH_AUDIENCE=https://api.example.com \
./web-researcher-mcp
```

When `PORT` is set, the server runs the HTTP (Streamable) transport exclusively
and does not read STDIO; when `PORT` is unset it runs STDIO exclusively. The two
transports are mutually exclusive, so a container started with `PORT` set but no
stdin attached (`docker run -p ... -e PORT=...`) stays up serving HTTP.

**Endpoints:**
- `/mcp/` — Streamable HTTP MCP endpoint (handles POST and streaming)
- `GET /health/live` — Liveness probe (always 200, `ok`)
- `GET /health/ready` — Readiness probe (always 200, `ready` — a static
  process-up check, not a dependency health check; the server is fully
  initialized before the listener binds)
- `GET /metrics` — Prometheus metrics
- `GET /.well-known/oauth-authorization-server` — OAuth metadata

### Transport Mode Differences

| Behavior | STDIO (Local) | HTTP (Cloud/Team) |
|----------|:---:|:---:|
| Tool functionality | Identical | Identical |
| Tool descriptions | Identical | Identical |
| Auth | No | OAuth 2.1 when `OAUTH_ISSUER_URL` is set; open otherwise |
| Rate limiting (server-side) | None | Per-tenant + global |
| Rate limiting (upstream APIs) | Applies | Applies |
| Session persistence | Local disk | Local disk (use sticky sessions for multi-instance) |
| Audit logging | Yes | Yes |
| SSRF protection | Yes | Yes |
| Cache | Local memory + disk | Local memory + disk |

**Design intent:** STDIO mode trusts the local user implicitly (it runs as their process). HTTP mode adds auth and rate limiting for untrusted network clients. Tool handlers execute identically regardless of transport.

---

## Docker

The project includes two Dockerfiles in the repo root:
- `Dockerfile` — multi-stage build (builder + Alpine runtime), used for local builds
- `Dockerfile.release` — slim Alpine image used by GoReleaser (expects pre-built binary)

Both images bundle Chromium plus the fonts/libraries go-rod needs for full browser-tier rendering, run as a non-root UID (`65534`), and set `CHROME_PATH=/usr/bin/chromium-browser` so the browser scrape tier works out of the box — no extra layers required.

```bash
# Build and run locally
docker build -t web-researcher-mcp .
docker run -i --rm \
  -e GOOGLE_CUSTOM_SEARCH_API_KEY=... \
  -e GOOGLE_CUSTOM_SEARCH_ID=... \
  web-researcher-mcp

# HTTP mode
docker run -p 3000:3000 \
  -e PORT=3000 \
  -e GOOGLE_CUSTOM_SEARCH_API_KEY=... \
  -e GOOGLE_CUSTOM_SEARCH_ID=... \
  -e OAUTH_ISSUER_URL=... \
  -e OAUTH_AUDIENCE=... \
  web-researcher-mcp
```

**For headless browser (go-rod):** The bundled images already ship Chromium and set `CHROME_PATH`. Override `CHROME_PATH` only if you mount a different Chromium/Chrome binary.

---

## Kubernetes

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web-researcher-mcp
spec:
  replicas: 3
  selector:
    matchLabels:
      app: web-researcher-mcp
  template:
    metadata:
      labels:
        app: web-researcher-mcp
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "3000"
        prometheus.io/path: "/metrics"
    spec:
      containers:
      - name: server
        image: web-researcher-mcp:latest
        ports:
        - containerPort: 3000
        env:
        - name: PORT
          value: "3000"
        - name: GOOGLE_CUSTOM_SEARCH_API_KEY
          valueFrom:
            secretKeyRef:
              name: mcp-secrets
              key: google-api-key
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 1000m
            memory: 512Mi
        livenessProbe:
          httpGet:
            path: /health/live
            port: 3000
          initialDelaySeconds: 5
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /health/ready
            port: 3000
          initialDelaySeconds: 5
          periodSeconds: 5
---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: web-researcher-mcp
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: web-researcher-mcp
  minReplicas: 2
  maxReplicas: 10
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70
  - type: Pods
    pods:
      metric:
        name: mcp_active_connections
      target:
        type: AverageValue
        averageValue: "50"
```

---

## Environment Variables

### Required

**None.** With no configuration at all, the server falls back to DuckDuckGo (zero-config, no API key). For higher-quality results configure a provider below. `.env.example` is the authoritative source for the full variable set.

| Variable | Description | Example |
|----------|-------------|---------|
| `GOOGLE_CUSTOM_SEARCH_API_KEY` | Google API key. Required **only** when `SEARCH_PROVIDER=google` and routing is unset; otherwise optional | `AIzaSy...` (39 chars) |
| `GOOGLE_CUSTOM_SEARCH_ID` | Search engine ID (paired with the key above) | From [PSE console](https://programmablesearchengine.google.com/) |

Note: Google keys are validated as required only when you explicitly select `SEARCH_PROVIDER=google` without multi-provider routing. With `SEARCH_PROVIDER` unset (or any other value), the server starts keyless and falls back to the zero-config DuckDuckGo provider — in both STDIO and HTTP mode. A genuine misconfiguration (e.g. `SEARCH_PROVIDER=google` with no key) is fatal in HTTP mode (`PORT` set) and logged-but-non-fatal in STDIO mode so local use is never blocked.

### Search Provider

| Variable | Description | Default |
|----------|-------------|---------|
| `SEARCH_PROVIDER` | Primary provider: google, brave, serper, searxng, searchapi, duckduckgo | `google` (variable default); at runtime, when `google` is selected but no Google key is set, the server falls back to the zero-config `duckduckgo` provider |
| `SEARCH_FALLBACK_PROVIDER` | Fallback provider (simple fallback) | — |
| `SEARCH_ROUTING` | Multi-provider routing (see below) | — |
| `BRAVE_API_KEY` | Brave Search API key | — |
| `SERPER_API_KEY` | Serper.dev API key | — |
| `SEARCHAPI_API_KEY` | SearchAPI.io API key | — |
| `SEARXNG_URL` | SearXNG instance URL | — |
| `CUSTOM_LENSES_PATH` | External lenses directory | — |

### Patent Providers (Optional)

These enable structured patent search via official APIs. Without them, `patent_search` falls back to web search discovery.

| Variable | Description | Coverage |
|----------|-------------|----------|
| `USPTO_API_KEY` | USPTO API key ([data.uspto.gov](https://data.uspto.gov)) | US patents |
| `EPO_OPS_CONSUMER_KEY` | EPO OPS consumer key ([developers.epo.org](https://developers.epo.org)) | Worldwide |
| `EPO_OPS_CONSUMER_SECRET` | EPO OPS consumer secret | Worldwide |
| `LENS_API_TOKEN` | The Lens API token ([lens.org](https://www.lens.org)) | Worldwide + scholarly |

Each configured provider gets an independent circuit breaker. The `patent_search` tool automatically selects providers based on the requested `patent_office` region.

### Multi-Provider Routing

When `SEARCH_ROUTING` is set, the server uses all configured providers with intelligent fallback:

```bash
# Simple: comma-separated priority list (applies to all operations)
SEARCH_ROUTING=brave,google,serper

# Advanced: per-operation routing (JSON)
SEARCH_ROUTING='{"web":"brave,google","news":"brave,serper","images":"google,brave","academic":"openalex,crossref","patents":"epo,lens,searchapi,uspto","default":"brave,google,searchapi"}'
```

**How it works:**
- Requests route to the first healthy provider in the priority list
- If a provider fails (timeout, rate limit, 5xx), the next provider is tried automatically
- Each provider gets an independent circuit breaker. The routing-layer breakers that govern fallback (web, patent, and academic alike) open after 3 consecutive failures and reset after 30s (`internal/search/router.go`). Domain providers additionally wrap their own upstream HTTP calls in an inner breaker (5 failures / 60s, `internal/search/domain.go`) — a separate, deeper layer, not the effective routing breaker. See those files for the authoritative values.
- Lenses can override routing via the `"routing"` field in their JSON definition

**Operation types:** `web`, `images`, `news`, `academic`, `patents`, `default`. The `academic` and `patents` lists are filtered to providers that implement the academic/patent interface — `academic` accepts only `openalex`, `crossref`; `patents` accepts only `searchapi`, `epo`, `lens`, `uspto`. Names that don't implement the interface are silently dropped, so use the example values above.

When no explicit routing is configured for an operation, the `default` list is used. When `SEARCH_ROUTING` is not set at all, the server uses `SEARCH_PROVIDER` as a single provider (backward compatible).

### HTTP Transport

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | HTTP listen port (enables HTTP mode) | — (STDIO only) |
| `OAUTH_ISSUER_URL` | JWT issuer URL | — |
| `OAUTH_AUDIENCE` | Expected JWT audience | — |
| `ALLOWED_ORIGINS` | CORS origins (comma-separated). Browser-only; backend connectors and STDIO are unaffected | — (deny cross-origin by default; see `CORS_STRICT`) |
| `CORS_STRICT` | When `true` (default), an empty `ALLOWED_ORIGINS` denies all cross-origin browser requests (fail-closed). When `false`, an empty `ALLOWED_ORIGINS` reflects any Origin (legacy permissive escape hatch). See [MIGRATION.md](MIGRATION.md) for the breaking change. | `true` |
| `ENFORCE_SCOPES` | When `true`, a token that carries a `scope`/`scp` claim must include `tool:*`, `tool:<name>`, or the coarse `research` scope to invoke a tool. Tokens with no scope claim are still allowed (permissive; fail-closed only on present-but-insufficient scopes). | `false` |
| `REQUIRED_SCOPES` | Optional comma-separated scopes that every request must carry when `ENFORCE_SCOPES=true`. Only meaningful with `ENFORCE_SCOPES`. | — |

### Connecting browser-based clients (CORS)

CORS is a **browser-only** mechanism — it governs whether JavaScript running on one origin may read responses from your server. It is not an authentication layer (that is OAuth). Two cases:

- **Hosted connectors (ChatGPT, Claude.ai, and most agent platforms).** When a user adds your remote server as a connector, the platform's **backend** opens the connection, not the user's browser tab. These requests carry no enforced `Origin`, so CORS never applies and the fail-closed default has no effect. You do **not** need to control the client app — just configure OAuth. This is the common case.
- **A genuine in-browser MCP client** (JavaScript calling your server directly with `fetch`). Here CORS applies. The operator allow-lists the client's public origin — you don't need to own the app to do this:

  ```bash
  ALLOWED_ORIGINS=https://claude.ai,https://chatgpt.com
  ```

To restore the legacy permissive behavior wholesale, set `CORS_STRICT=false` (see [MIGRATION.md](MIGRATION.md)).

### HTTP Hardening

These tune the embedded `http.Server` and response security headers. **All are ignored in STDIO mode** (when `PORT` is unset). Defaults are permissive so long scrape/research responses are never truncated — `HTTP_WRITE_TIMEOUT=0` (unlimited) in particular keeps multi-minute responses intact.

| Variable | Description | Default |
|----------|-------------|---------|
| `HTTP_READ_HEADER_TIMEOUT` | Max time to read request headers (primary slowloris guard) | `5s` |
| `HTTP_READ_TIMEOUT` | Max time to read the full request | `30s` |
| `HTTP_WRITE_TIMEOUT` | Max time to write the response. `0` = unlimited (keep permissive for long responses) | `0` |
| `HTTP_IDLE_TIMEOUT` | Frees idle keep-alive connections | `120s` |
| `HTTP_MAX_HEADER_BYTES` | Caps total request header size against header-flood memory exhaustion | `1048576` (1 MB) |
| `MAX_REQUEST_BODY_BYTES` | Caps `/mcp` and `/admin` request body size; oversized bodies are rejected with `413`. Set higher for large MCP payloads | `10485760` (10 MB) |
| `HTTP_CSP` | `Content-Security-Policy` response header. Safe for a JSON-only API (no HTML served). An empty value omits the header | `default-src 'none'; frame-ancestors 'none'` |
| `HTTP_REFERRER_POLICY` | `Referrer-Policy` response header | `no-referrer` |
| `HTTP_PERMISSIONS_POLICY` | `Permissions-Policy` response header (empty-deny set). An empty value omits the header | `geolocation=(), camera=(), microphone=()` |

### Cache

| Variable | Description | Default |
|----------|-------------|---------|
| `CACHE_DIR` | Disk cache directory | Platform cache dir (e.g., `~/Library/Caches/web-researcher-mcp`) |
| `CACHE_MAX_MEMORY_MB` | Max memory cache size | `64` |
| `CACHE_ENCRYPTION_KEY` | 64 hex chars for AES-256-GCM | — (plaintext) |
| `CACHE_ENCRYPTION_KEY_PREV` | Optional 64-hex previous key for zero-downtime key rotation. When set, the disk cache and session store decrypt-fallback to it and lazily re-encrypt with the current key on read. Empty = no fallback | — |
| `REDIS_URL` | **HTTP mode only.** When set, enables distributed state across pods (shared cache L2, cross-pod sessions, atomic daily-quota). Requires `CACHE_ENCRYPTION_KEY` (personal data is encrypted at rest in Redis). Fail-fast: an unreachable Redis at startup is fatal. Unset = per-pod in-memory/disk (unchanged). Ignored in STDIO mode | — |
| `SESSION_TTL` | Session idle timeout (the in-memory TTL resets on every read or write of the session) | `4h` |
| `SESSION_DATA_DIR` | Directory for encrypted session files | `{CACHE_DIR}/sessions` |
| `SESSION_MAX_STEPS` | Maximum steps per research session before auto-completion | `200` |

### Rate Limiting

Rate limiting applies **only in HTTP mode** (when `PORT` is set). STDIO mode has no internal rate limiting — only upstream API quotas apply.

| Variable | Description | Default |
|----------|-------------|---------|
| `RATE_LIMIT_PER_TENANT` | Requests per minute per tenant | `120` |
| `RATE_LIMIT_GLOBAL` | Total requests per second | `1000` |
| `DAILY_QUOTA_PER_TENANT` | Max API calls per tenant per day | `5000` |
| `RATE_LIMIT_PER_IP` | Requests per minute per client IP, enforced **pre-auth** (outermost middleware). `0` disables it (default), so zero-config use is never blocked. Set generous (hundreds) for public HTTP | `0` (disabled) |
| `TRUST_PROXY` | When `true`, the per-IP limiter reads the leftmost `X-Forwarded-For` entry (behind a trusted load balancer). Default `false` uses `RemoteAddr` only, preventing spoofed-IP bypass | `false` |
| `RATE_LIMIT_PERSIST` | When `true`, daily-quota counters write through to the encrypted persist store and survive restarts. Default `false` keeps the pure in-memory zero-config behavior | `false` |

**How tenant identity works:**
- With OAuth configured: tenant ID is extracted from the JWT `tenant_id` claim. Each authenticated tenant gets independent rate limit buckets.
- Without OAuth: all requests share a single "default" tenant bucket. This means multiple AI sessions hitting the same HTTP instance share 120 req/min by default.

**Recommended settings for common scenarios:**

```bash
# Single developer, multiple AI sessions (no OAuth)
RATE_LIMIT_PER_TENANT=200
DAILY_QUOTA_PER_TENANT=5000

# Team server with OAuth (each team member gets their own bucket)
RATE_LIMIT_PER_TENANT=60
DAILY_QUOTA_PER_TENANT=2000

# High-throughput automation
RATE_LIMIT_PER_TENANT=500
RATE_LIMIT_GLOBAL=5000
DAILY_QUOTA_PER_TENANT=10000
```

**Note:** These limits protect the server, not your upstream API quota. Google PSE free tier is 100 queries/day regardless of what you set here. Configure `SEARCH_ROUTING` with multiple providers if you need higher throughput.

### Scraping

| Variable | Description | Default |
|----------|-------------|---------|
| `ALLOW_PRIVATE_IPS` | Disable SSRF protection | `false` |
| `ALLOWED_DOMAINS` | Domain whitelist (comma-separated) | — (all allowed) |
| `CHROME_PATH` | Custom Chrome/Chromium binary path | auto-detect |
| `MAX_SCRAPE_CONCURRENCY` | Parallel scrape limit | `5` |

### Features (Opt-In)

Additive output features (content-only, no personal data, no model calls):

| Variable | Description | Default |
|----------|-------------|---------|
| `SOURCE_RECOMMENDATIONS` | Surface advisory "related higher-quality sources" on `search_and_scrape`, derived from the existing transparent quality signals. Content-based; never re-ranks or hides results. Set `false` to omit the field | `true` |
| `GENERATIVE_UI_ENABLED` | Emit additive, deterministic `mcp-auto-formatted` components (source cards, quality-comparison table) built from already-extracted data — no model call. Off → output byte-for-byte unchanged | `false` |

Regulated features (HTTP mode; per-user personal data; each activates the consent subsystem and is covered by the data-subject rights endpoints). All default off:

| Variable | Description | Default |
|----------|-------------|---------|
| `MEMORY_ENABLED` | Opt-in long-term cross-session memory (`memory_save`/`memory_recall`). Consent-gated on the `memory` purpose | `false` |
| `MEMORY_RETENTION` | Max lifetime of a saved memory before auto-expiry | `2160h` (90d) |
| `USER_ANALYTICS_ENABLED` | Opt-in per-user usage analytics (`get_my_analytics`). Consent-gated on the `analytics` purpose | `false` |
| `WORKSPACES_ENABLED` | Opt-in shared research workspaces (`workspace_contribute`/`workspace_read` + `/admin/workspace/members`). Consent-gated on the `workspace` purpose; membership host-managed | `false` |
| `WORKSPACE_TTL` | Max lifetime of shared-workspace data | `720h` (30d) |

> Enabling any regulated feature activates the consent subsystem automatically — there is no standalone `CONSENT_ENABLED` knob. Consent is asserted by the host (via `POST /admin/consent`) and recorded/verified/honored by the server. See `docs/SECURITY.md` and `docs/SECURITY_AND_COMPLIANCE.md`.

### Observability

| Variable | Description | Default |
|----------|-------------|---------|
| `LOG_LEVEL` | slog level | `info` |
| `LOG_FORMAT` | Output format | `json` |
| `METRICS_ENABLED` | Enable Prometheus metrics | `true` |

### Audit

| Variable | Description | Default |
|----------|-------------|---------|
| `AUDIT_ENABLED` | Enable structured audit logging | `true` |
| `AUDIT_OUTPUT_PATH` | File path for audit log output (JSONL format) | — (stderr) |
| `AUDIT_BUFFER_SIZE` | Internal event buffer size | `1000` |
| `AUDIT_INCLUDE_REQUEST_BODY` | When `true`, raw query text is attached to audit metadata. When `false`, only a length/hash is recorded — raw query text is omitted | `false` |
| `AUDIT_MAX_BYTES` | Rotate the active audit file to a timestamped sibling at this size. File output only; ignored for stderr/STDIO | `104857600` (100 MB) |
| `AUDIT_RETENTION_DAYS` | Rotated audit files older than this are deleted on startup and hourly. `0` disables cleanup. Any non-zero value is clamped to `[180, 3650]` per NIS2/HGB retention floors | `180` |

### Multi-Tenancy

| Variable | Description | Default |
|----------|-------------|---------|
| `CACHE_ISOLATION` | Cache isolation mode (`shared` or `tenant`) | `shared` |
| `DATA_REGION` | Advisory label for where cache/session/audit data resides; surfaced in stats/audit. No functional restriction | — (unset) |

When `CACHE_ISOLATION=tenant`, all cache keys are prefixed with the authenticated tenant ID from the JWT token. This ensures tenant A's cached results are invisible to tenant B. Default (`shared`) is appropriate for single-tenant deployments or when search results are inherently public. Use `tenant` for multi-tenant deployments with strict data isolation requirements.

`DATA_REGION` is an operator-supplied label only (e.g. `eu-central`, `us-east`). It is echoed in stats and audit records for residency documentation but does not move, restrict, or constrain where data is physically stored — that is governed by `CACHE_DIR`, `SESSION_DATA_DIR`, and `AUDIT_OUTPUT_PATH`.

### Auth (Advanced)

| Variable | Description | Default |
|----------|-------------|---------|
| `JWKS_REFRESH_INTERVAL` | How often to refresh JWKS keys | `1h` |
| `ADMIN_API_KEY` | Shared secret gating all `/admin/*` endpoints, sent as `X-Admin-Key` (min 16 chars). Generate with `openssl rand -hex 32` | — |
| `CACHE_ADMIN_KEY` | **Deprecated** alias for `ADMIN_API_KEY` (still accepted; logs a startup warning). `ADMIN_API_KEY` wins if both are set | — |

---

## Horizontal Scaling

**Two modes.** Without `REDIS_URL`, the server uses in-memory + encrypted-disk state, per-instance:

- **Cache:** Each instance has its own memory + disk cache. Cache hits are local only. Acceptable since search results are deterministic (same query = same results).
- **Sessions:** Persist to local encrypted disk with an in-memory index; survive restarts within the TTL window (default 4h). If a client reconnects to a *different* instance, the session is not there — use sticky sessions (the typed `session_not_found` error lets clients recover cleanly otherwise).
- **Rate limits:** Per-instance. A tenant hitting N instances gets up to N× the per-tenant limit.
- **go-rod browser instances** are per-pod. No shared browser pool.

**With `REDIS_URL` set (HTTP mode), distributed state is enabled (#42):**

- **Cache** gains a shared Redis L2 tier (memory L1 → Redis L2 → disk L3), so a query warmed by one pod is served from Redis by the others — upstream quota is burned once, not once-per-pod.
- **Sessions** live in Redis with a server-side `EXPIRE`, so they survive pod restarts and a client reaching any pod finds its research (sticky sessions become optional).
- **Daily rate quota** is enforced fleet-wide via an atomic Redis counter (single `INCR` keyed to a midnight-UTC TTL), so N pods share one limit — no N× over-spend, no double-spend under concurrency.
- **Token revocation** is shared across pods via the same Redis-backed persist store.
- All personal-data namespaces (sessions, persist) are **AES-256-GCM encrypted before write** — Redis holds only ciphertext, identical at-rest protection to disk. `REDIS_URL` therefore **requires** `CACHE_ENCRYPTION_KEY`.
- **Fail-fast:** if `REDIS_URL` is set but Redis is unreachable at startup, the server exits rather than silently degrading to per-pod mode.

**Recommendations for multi-instance HTTP deployments:**

1. **Preferred:** set `REDIS_URL` (+ `CACHE_ENCRYPTION_KEY`) for correct cross-pod sessions, cache, and rate limits.
2. Without Redis: use sticky sessions at your L7 load balancer and divide rate limits by expected instance count.
3. `go-rod` browser rendering remains per-pod regardless (stateless, no shared pool needed).

### Production Readiness Checklist

Before running multiple instances behind a load balancer, work through this checklist. Items marked **(per-pod without Redis)** behave differently across pods unless `REDIS_URL` is set (#42).

- [ ] **Distributed state** — set `REDIS_URL` (+ `CACHE_ENCRYPTION_KEY`) to share sessions, cache, and rate limits across pods. This is the recommended multi-instance configuration; the items below are only concerns when Redis is *not* used.
- [ ] **Sticky sessions** — without Redis, configure session affinity at the L7 load balancer so a client's follow-up `sequential_search` steps reach the pod holding its session. A step routed to another pod returns a typed `session_not_found` error with a `recoveryHint` (last known step) so the client can restart cleanly rather than silently forking. **(per-pod without Redis)**
- [ ] **Rate-limit math for N pods** — without Redis, per-tenant and global limits are per-instance, so N pods allow up to N× the configured value. Set `RATE_LIMIT_PER_TENANT` / `RATE_LIMIT_GLOBAL` to `desired_total / N`, or use `REDIS_URL` for fleet-wide atomic enforcement. **(per-pod without Redis)**
- [ ] **Log aggregation** — ship each pod's structured JSON audit/log output (stderr or `AUDIT_OUTPUT_PATH`) to a central sink. Every audit event carries `pod_id` (from `HOSTNAME`/`os.Hostname()`) for cross-pod correlation — filter or group by it to trace a request or identify a pod dropping events under backpressure.
- [ ] **Monitoring & dashboards** — scrape `/metrics` (Prometheus) from every pod; alert on error rate, upstream-provider failures (circuit-breaker trips), and latency percentiles. Liveness `/health/live` and readiness `/health/ready` are wired for orchestrator probes.
- [ ] **Encryption key** — set `CACHE_ENCRYPTION_KEY` (and rotate per [Key Rotation](#key-rotation)) so disk-persisted sessions/cache/quota are encrypted at rest on every pod.
- [ ] **Admin key** — set `ADMIN_API_KEY` if you use the `/admin/*` operational endpoints; it is required to enable them.
- [ ] **CORS** — set `ALLOWED_ORIGINS` if a browser client connects directly; the default is fail-closed (see [Connecting browser-based clients](#connecting-browser-based-clients-cors)).

---

## Persistence

Two HTTP-mode subsystems can durably persist state across restarts via a single internal `persist.Store` interface (`internal/persist`):

- **Token revocation** — revoked JWT IDs (JTIs) survive a restart so a revoked token stays revoked.
- **Daily quota counters** — enabled by `RATE_LIMIT_PERSIST=true`, so per-tenant daily quotas are not reset by a restart.

The default `persist.Store` implementation is the same proven encrypted-disk pattern as the session store: AES-256-GCM (using `CACHE_ENCRYPTION_KEY`, with `CACHE_ENCRYPTION_KEY_PREV` fallback), atomic temp-file-and-rename writes, `0600` file permissions, an 8-byte big-endian expiry prefix, and an in-memory index. Keys are SHA-256-hashed for the on-disk filename and bound as GCM additional authenticated data so a blob cannot be swapped to a different key's file. Local (memory) and disk implementations behave identically, so there is no behavioral drift between STDIO and HTTP deployments.

When `REDIS_URL` is set (HTTP mode), a `RedisStore` satisfying this same interface backs token revocation and the daily quota, so both are shared across pods and survive restarts. Redis-stored values are AES-256-GCM encrypted (parity with disk). All Redis code is isolated in `internal/redisbackend` — the only package that imports the Redis client — and is constructed in exactly one gated place in `main.go`, so STDIO and the zero-config path never touch it.

---

## go install

```bash
# Install globally
go install github.com/zoharbabin/web-researcher-mcp/cmd/web-researcher-mcp@latest

# The binary is available as:
web-researcher-mcp
```

**Claude Code config (go install):**
```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "web-researcher-mcp",
      "env": {
        "GOOGLE_CUSTOM_SEARCH_API_KEY": "...",
        "GOOGLE_CUSTOM_SEARCH_ID": "..."
      }
    }
  }
}
```

The Go binary runs directly with no wrapper process — clean process lifecycle with immediate EOF detection on parent exit.

---

## Client Configurations

### Claude Code / Cursor

Add to your project root as `.mcp.json` or run:

```bash
claude mcp add --scope user --transport stdio web-researcher -- web-researcher-mcp
```

Project config (`.mcp.json`):
```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "web-researcher-mcp",
      "args": [],
      "env": {
        "GOOGLE_CUSTOM_SEARCH_API_KEY": "${GOOGLE_CUSTOM_SEARCH_API_KEY}",
        "GOOGLE_CUSTOM_SEARCH_ID": "${GOOGLE_CUSTOM_SEARCH_ID}"
      }
    }
  }
}
```

### VS Code / GitHub Copilot

Add `.vscode/mcp.json` to your project:

```json
{
  "inputs": [
    {
      "id": "google_api_key",
      "type": "promptString",
      "description": "Google Custom Search API key",
      "password": true
    },
    {
      "id": "google_cx",
      "type": "promptString",
      "description": "Google Custom Search engine ID"
    }
  ],
  "servers": {
    "web-researcher": {
      "command": "web-researcher-mcp",
      "args": [],
      "env": {
        "GOOGLE_CUSTOM_SEARCH_API_KEY": "${input:google_api_key}",
        "GOOGLE_CUSTOM_SEARCH_ID": "${input:google_cx}"
      }
    }
  }
}
```

### Claude Desktop

Download the `.mcpb` bundle for your platform from [GitHub Releases](https://github.com/zoharbabin/web-researcher-mcp/releases) and open it in Claude Desktop. It will prompt for your API keys.

Or manually add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "/usr/local/bin/web-researcher-mcp",
      "env": {
        "GOOGLE_CUSTOM_SEARCH_API_KEY": "...",
        "GOOGLE_CUSTOM_SEARCH_ID": "..."
      }
    }
  }
}
```

### Windsurf

Add to `./codeium/windsurf/model_config.json`:

```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "web-researcher-mcp",
      "args": [],
      "env": {
        "GOOGLE_CUSTOM_SEARCH_API_KEY": "...",
        "GOOGLE_CUSTOM_SEARCH_ID": "..."
      }
    }
  }
}
```

### Docker (any client)

```bash
docker run -i --rm \
  -e GOOGLE_CUSTOM_SEARCH_API_KEY=... \
  -e GOOGLE_CUSTOM_SEARCH_ID=... \
  zoharbabin/web-researcher-mcp:latest
```

Use with any MCP client that supports Docker transports or pipe STDIO to the container.

---

## MCP Registry & Marketplace

This server is distributed via:

| Registry | Config File | Status |
|----------|-------------|--------|
| Official MCP Registry | `server.json` | Publish with `mcp-publisher publish` |
| Smithery.ai | `smithery.yaml` | Auto-detected from repo root |
| Docker Hub | `docker.io/zoharbabin/web-researcher-mcp` | Published on release |
| GHCR | `ghcr.io/zoharbabin/web-researcher-mcp` | Published on release |
| GitHub Releases | `.mcpb` bundles | Attached per-platform on release |

---

## Health Checks

| Endpoint | Method | Response | Use |
|----------|--------|----------|-----|
| `/health/live` | GET | `200 OK` always (`ok`) | K8s liveness probe |
| `/health/ready` | GET | `200 OK` always (`ready`) | K8s readiness probe |

Both probes are static process-up checks: a `200` means the process is running
and the HTTP listener is bound (the server completes all initialization —
providers, cache, sessions, audit — before binding the port, so a successful
connection already implies a fully-constructed server). They do not perform live
dependency health checks; upstream provider availability is handled at call time
via per-provider circuit breakers and graceful tool errors.

---

## Graceful Shutdown

On SIGINT/SIGTERM (HTTP mode), or SIGINT/SIGTERM/stdin EOF (STDIO mode):
1. Stop accepting new connections
2. Drain in-flight requests (30s timeout)
3. Flush cache to disk
4. Close audit logger (drains buffered events including swap file)
5. Terminate headless browsers
6. Exit 0

No orphan processes. No watchdog needed.

---

## Admin Endpoints (HTTP Mode)

All admin endpoints require the `X-Admin-Key` header matching the `ADMIN_API_KEY` env var (the deprecated `CACHE_ADMIN_KEY` is still accepted). The header is compared in constant time. They are separate from OAuth — admin auth is a simple shared secret for operational use.

| Method | Path | Purpose |
|--------|------|---------|
| DELETE | `/admin/cache` | Flush all cache (memory + disk) |
| DELETE | `/admin/sessions` | Kill all active sessions |
| GET | `/admin/analytics` | Per-tenant **aggregate** usage (calls, error/cache-hit rates, provider breakdown, latency percentiles) for billing/capacity. Optional `?tenant_id=` filter. Aggregate-only — no per-query or per-user content |
| GET | `/admin/data?tenant_id=&user_id=` | **GDPR access/portability** (Art. 15/20): JSON export of all data held for a data subject across every registered store. `tenant_id` required; `user_id` optional |
| DELETE | `/admin/data?tenant_id=&user_id=` | **GDPR erasure** (Art. 17): purge the subject's data across all stores and withdraw their consent; records a `data.erasure` audit event |
| POST | `/admin/consent` | Record a host-asserted consent decision. Body: `{tenant_id, user_id, purpose, granted, terms_version?}`. Only present when a regulated feature is enabled |
| GET | `/admin/consent?tenant_id=&user_id=&purpose=` | Query the current consent decision for a subject + purpose |
| POST | `/admin/workspace/members` | Add a member to a shared workspace (host's RBAC hook). Body: `{workspace_id, tenant_id, user_id}`. Only present when `WORKSPACES_ENABLED` |
| DELETE | `/admin/workspace/members` | Remove a member from a shared workspace. Body: `{workspace_id, tenant_id, user_id}`. Only present when `WORKSPACES_ENABLED` |

These are HTTP-only operational endpoints, not exposed via MCP tools. The `/admin/data` endpoints exist only when a personal-data store is registered; `/admin/consent` and `/admin/workspace/members` only when the corresponding regulated feature is enabled.

---

## Key Rotation

The server uses two independent secrets. Both rotate without downtime.

### Admin key (`ADMIN_API_KEY`)

The admin key is stateless — rotating it is a single env-var change:

1. Generate a new key: `openssl rand -hex 32`.
2. Update `ADMIN_API_KEY` in your deployment and restart (or rolling-restart) the pods.
3. Update any operational scripts/dashboards that send `X-Admin-Key`.

There is no stored state encrypted under the admin key, so no migration is needed. In a rolling deployment, in-flight admin calls against an old pod simply use that pod's old key until it cycles; admin endpoints are operational, not user-facing, so a brief overlap is harmless.

### Encryption key (`CACHE_ENCRYPTION_KEY`) — zero-downtime re-encryption

Disk-persisted data (cache, sessions, and any encrypted `persist.Store` namespace) is sealed with AES-256-GCM under `CACHE_ENCRYPTION_KEY`. Rotating it without stranding existing data uses the previous-key fallback:

1. Move the current key to `CACHE_ENCRYPTION_KEY_PREV` and set a new 64-hex `CACHE_ENCRYPTION_KEY` (generate with `openssl rand -hex 32`).
2. Restart. On every read, data sealed with the previous key is **decrypt-fall-back** decrypted and **lazily re-encrypted** with the new key — so hot data migrates automatically with no flush and no downtime.
3. After at least one full data lifetime (e.g. `SESSION_TTL` for sessions, the cache TTL for cache) has elapsed, remove `CACHE_ENCRYPTION_KEY_PREV`. Any still-unread blobs from before the rotation simply expire.

To force immediate re-encryption rather than waiting for natural reads, flush the affected store (`DELETE /admin/cache`, `DELETE /admin/sessions`) after step 2 — data repopulates under the new key on demand.

> **Compliance note:** rotating `CACHE_ENCRYPTION_KEY` periodically (and immediately on suspected exposure) satisfies common key-lifecycle controls (e.g. NIST SP 800-57 crypto-period guidance). The previous-key window should be kept as short as your longest TTL; never keep more than one previous key.

---

## MCP Resources & Prompts

### Resources

| URI | Description |
|-----|-------------|
| `stats://tools` | Per-tool execution metrics (totalCalls, avgLatencyMs, etc.) |
| `stats://sessions` | Count of active sequential research sessions |
| `stats://rate-limits` | Rate limit config and usage (per-tenant limits, daily quota remaining, reset time) |
| `stats://providers` | Search, patent, and academic providers currently configured and available |

### Prompts

| Prompt | Description | Required Args |
|--------|-------------|---------------|
| `comprehensive-research` | Multi-step research process | `topic` |
| `fact-check` | Verify a claim from multiple sources | `claim` |
| `competitive-analysis` | Research competitors in a market | `company` |
| `literature-review` | Systematic academic literature review | `topic` |
