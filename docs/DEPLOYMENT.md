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

**Claude Code config** (`~/.claude/settings.json`):
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

When `PORT` is set, the server starts an HTTP listener in addition to STDIO.

**Endpoints:**
- `/mcp/` — Streamable HTTP MCP endpoint (handles POST and streaming)
- `GET /health/live` — Liveness probe (always 200)
- `GET /health/ready` — Readiness probe (checks dependencies)
- `GET /metrics` — Prometheus metrics
- `GET /.well-known/oauth-authorization-server` — OAuth metadata

---

## Docker

The project includes two Dockerfiles in the repo root:
- `Dockerfile` — multi-stage build (builder + Alpine runtime), used for local builds
- `Dockerfile.release` — slim Alpine image used by GoReleaser (expects pre-built binary)

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

**For headless browser (go-rod):** Set `CHROME_PATH` to a Chromium binary inside the container, or extend the Dockerfile to include `chromedp/headless-shell`.

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
        - name: REDIS_URL
          value: "redis://redis-svc:6379"
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

| Variable | Description | Example |
|----------|-------------|---------|
| `GOOGLE_CUSTOM_SEARCH_API_KEY` | Google API key (required unless `SEARCH_ROUTING` is set) | `AIzaSy...` (39 chars) |
| `GOOGLE_CUSTOM_SEARCH_ID` | Search engine ID (required unless `SEARCH_ROUTING` is set) | `017576662512468239146:omuauf_gy1x` |

### Search Provider

| Variable | Description | Default |
|----------|-------------|---------|
| `SEARCH_PROVIDER` | Primary provider: google, brave, serper, searxng, searchapi | `google` |
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
| `ALLOWED_ORIGINS` | CORS origins (comma-separated) | — (all origins) |

### Cache

| Variable | Description | Default |
|----------|-------------|---------|
| `CACHE_DIR` | Disk cache directory | Platform cache dir (e.g., `~/Library/Caches/web-researcher-mcp`) |
| `CACHE_MAX_MEMORY_MB` | Max memory cache size | `64` |
| `CACHE_ENCRYPTION_KEY` | 64 hex chars for AES-256-GCM | — (plaintext) |
| `REDIS_URL` | Redis connection string (accepted but not yet used — reserved for future distributed sessions) | — |

### Rate Limiting

Rate limiting applies **only in HTTP mode** (when `PORT` is set). STDIO mode has no internal rate limiting — only upstream API quotas apply.

| Variable | Description | Default |
|----------|-------------|---------|
| `RATE_LIMIT_PER_TENANT` | Requests per minute per tenant | `120` |
| `RATE_LIMIT_GLOBAL` | Total requests per second | `1000` |
| `DAILY_QUOTA_PER_TENANT` | Max API calls per tenant per day | `5000` |

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
| `AUDIT_INCLUDE_REQUEST_BODY` | Include full request bodies in audit records | `false` |

### Multi-Tenancy

| Variable | Description | Default |
|----------|-------------|---------|
| `CACHE_ISOLATION` | Cache isolation mode (`shared` or `tenant`) | `shared` |

**Note:** `CACHE_ISOLATION=tenant` is accepted but not yet enforced in the cache implementation. Cache keys are content-addressed and shared across tenants. For search results this is safe (same query returns same results), but deployments requiring strict tenant data isolation should be aware of this limitation.

### Auth (Advanced)

| Variable | Description | Default |
|----------|-------------|---------|
| `JWKS_REFRESH_INTERVAL` | How often to refresh JWKS keys | `1h` |
| `CACHE_ADMIN_KEY` | Shared secret for admin endpoints (min 16 chars) | — |

---

## Horizontal Scaling

**Current state:** The server uses in-memory session state and per-instance rate limit counters. This means:

- **Cache:** Each instance has its own memory + disk cache. Cache hits are local only. This is acceptable since search results are deterministic (same query = same results).
- **Sessions:** Sequential search sessions are stored in-memory (`sync.Map`). If a client reconnects to a different instance mid-session, the session is lost. Use session-affinity (sticky sessions) at your load balancer.
- **Rate limits:** Per-instance, not distributed. A tenant hitting N instances gets N times the per-tenant limit.
- **go-rod browser instances** are per-pod. No shared browser pool. Each pod manages its own headless Chrome.

**Recommendations for multi-instance HTTP deployments:**

1. Use sticky sessions at your L7 load balancer (route by `X-Session-ID` header or MCP session)
2. Set rate limits conservatively (divide by expected instance count)
3. Accept that cache miss rates will be higher than single-instance (each pod warms independently)

**Note:** `REDIS_URL` is accepted in configuration but not yet wired into cache, sessions, or rate limiting. Distributed state support is planned for a future release.

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
| `/health/live` | GET | `200 OK` always | K8s liveness probe |
| `/health/ready` | GET | `200` if dependencies up, `503` otherwise | K8s readiness probe |

Readiness checks:
- At least one search provider is configured with valid credentials
- Disk cache directory is writable

---

## Graceful Shutdown

On SIGINT/SIGTERM or stdin EOF:
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
