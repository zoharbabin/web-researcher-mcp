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
| `SEARCH_PROVIDER` | Primary provider: google, brave, serper, searxng, searchapi, duckduckgo | `duckduckgo` (if no API keys set) |
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
SEARCH_ROUTING='{"web":"brave,google","news":"brave,serper","images":"google,brave","academic":"searchapi,google","patents":"epo,lens,searchapi,uspto","default":"brave,google,searchapi"}'
```

**How it works:**
- Requests route to the first healthy provider in the priority list
- If a provider fails (timeout, rate limit, 5xx), the next provider is tried automatically
- Each provider has an independent circuit breaker (opens after 3 consecutive failures, resets after 30s)
- Lenses can override routing via the `"routing"` field in their JSON definition

**Operation types:** `web`, `images`, `news`, `academic`, `patents`, `default`

When no explicit routing is configured for an operation, the `default` list is used. When `SEARCH_ROUTING` is not set at all, the server uses `SEARCH_PROVIDER` as a single provider (backward compatible).

### HTTP Transport

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | HTTP listen port (enables HTTP mode) | — (STDIO only) |
| `OAUTH_ISSUER_URL` | JWT issuer URL | — |
| `OAUTH_AUDIENCE` | Expected JWT audience | — |
| `ALLOWED_ORIGINS` | CORS origins (comma-separated) | — (reflect any origin when `CORS_STRICT=false`) |
| `CORS_STRICT` | When `false`, an empty `ALLOWED_ORIGINS` reflects any Origin (permissive). When `true`, an empty `ALLOWED_ORIGINS` denies all cross-origin (fail-closed). A future release will flip this default — see [MIGRATION.md](MIGRATION.md). | `false` |
| `ENFORCE_SCOPES` | When `true`, a token that carries a `scope`/`scp` claim must include `tool:*`, `tool:<name>`, or the coarse `research` scope to invoke a tool. Tokens with no scope claim are still allowed (permissive; fail-closed only on present-but-insufficient scopes). | `false` |
| `REQUIRED_SCOPES` | Optional comma-separated scopes that every request must carry when `ENFORCE_SCOPES=true`. Only meaningful with `ENFORCE_SCOPES`. | — |

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
| `REDIS_URL` | Reserved for a future `RedisStore` backend; currently a documented no-op. Setting it does not change behavior — see [persistence](#persistence) | — |
| `SESSION_TTL` | Session idle timeout (resets on every step addition) | `4h` |
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
| `CACHE_ADMIN_KEY` | Shared secret for admin endpoints (min 16 chars) | — |

---

## Horizontal Scaling

**Current state:** The server uses in-memory session state and per-instance rate limit counters. This means:

- **Cache:** Each instance has its own memory + disk cache. Cache hits are local only. This is acceptable since search results are deterministic (same query = same results).
- **Sessions:** Sequential search sessions persist to local encrypted disk with an in-memory index. Sessions survive server restarts within the TTL window (default 4 hours). If a client reconnects to a different instance, the session is not available on the new instance. Use session-affinity (sticky sessions) at your load balancer.
- **Rate limits:** Per-instance, not distributed. A tenant hitting N instances gets N times the per-tenant limit.
- **go-rod browser instances** are per-pod. No shared browser pool. Each pod manages its own headless Chrome.

**Recommendations for multi-instance HTTP deployments:**

1. Use sticky sessions at your L7 load balancer (route by `X-Session-ID` header or MCP session)
2. Set rate limits conservatively (divide by expected instance count)
3. Accept that cache miss rates will be higher than single-instance (each pod warms independently)

**Note:** `REDIS_URL` is accepted in configuration but is a documented no-op (see [Persistence](#persistence)). It is not yet wired into cache, sessions, rate limiting, or revocation. Distributed state support is planned for a future release.

---

## Persistence

Two HTTP-mode subsystems can durably persist state across restarts via a single internal `persist.Store` interface (`internal/persist`):

- **Token revocation** — revoked JWT IDs (JTIs) survive a restart so a revoked token stays revoked.
- **Daily quota counters** — enabled by `RATE_LIMIT_PERSIST=true`, so per-tenant daily quotas are not reset by a restart.

The default `persist.Store` implementation is the same proven encrypted-disk pattern as the session store: AES-256-GCM (using `CACHE_ENCRYPTION_KEY`, with `CACHE_ENCRYPTION_KEY_PREV` fallback), atomic temp-file-and-rename writes, `0600` file permissions, an 8-byte big-endian expiry prefix, and an in-memory index. Keys are SHA-256-hashed for the on-disk filename and bound as GCM additional authenticated data so a blob cannot be swapped to a different key's file. Local (memory) and disk implementations behave identically, so there is no behavioral drift between STDIO and HTTP deployments.

`REDIS_URL` is **reserved** for a future `RedisStore` backend that will satisfy this same interface. Until that backend ships, setting `REDIS_URL` does not change any behavior — memory or disk is selected by the constructor, not by this variable. There is no `go-redis` dependency in the build today.

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

All admin endpoints require the `X-Admin-Key` header matching `CACHE_ADMIN_KEY` env var. They are separate from OAuth — admin auth is a simple shared secret for operational use.

| Method | Path | Purpose |
|--------|------|---------|
| DELETE | `/admin/cache` | Flush all cache (memory + disk) |
| DELETE | `/admin/sessions` | Kill all active sessions |

These are HTTP-only operational endpoints, not exposed via MCP tools.

---

## MCP Resources & Prompts

### Resources

| URI | Description |
|-----|-------------|
| `stats://tools` | Per-tool execution metrics (totalCalls, avgLatencyMs, etc.) |
| `stats://sessions` | Count of active sequential research sessions |
| `stats://rate-limits` | Rate limit config and usage (per-tenant limits, daily quota remaining, reset time) |

### Prompts

| Prompt | Description | Required Args |
|--------|-------------|---------------|
| `comprehensive-research` | Multi-step research process | `topic` |
| `fact-check` | Verify a claim from multiple sources | `claim` |
| `competitive-analysis` | Research competitors in a market | `company` |
| `literature-review` | Systematic academic literature review | `topic` |
