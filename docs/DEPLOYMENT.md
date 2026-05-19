# Deployment Guide

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

### HTTP/SSE (Multi-client, web apps)

```bash
PORT=3000 \
OAUTH_ISSUER_URL=https://auth.example.com \
OAUTH_AUDIENCE=https://api.example.com \
./web-researcher-mcp
```

When `PORT` is set, the server starts an HTTP listener in addition to STDIO.

**Endpoints:**
- `POST /mcp` — Streamable HTTP MCP endpoint
- `GET /mcp/sse` — SSE endpoint for streaming
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

### Multi-Provider Routing

When `SEARCH_ROUTING` is set, the server uses all configured providers with intelligent fallback:

```bash
# Simple: comma-separated priority list (applies to all operations)
SEARCH_ROUTING=brave,google,serper

# Advanced: per-operation routing (JSON)
SEARCH_ROUTING='{"web":"brave,google","news":"brave,serper","images":"google,brave","academic":"searchapi,google","patents":"searchapi,google","default":"brave,google,searchapi"}'
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
| `ALLOWED_ORIGINS` | CORS origins (comma-separated) | `*` |

### Cache

| Variable | Description | Default |
|----------|-------------|---------|
| `CACHE_DIR` | Disk cache directory | `./cache` |
| `CACHE_MAX_MEMORY_MB` | Max memory cache size | `64` |
| `CACHE_ENCRYPTION_KEY` | 64 hex chars for AES-256-GCM | — (plaintext) |
| `REDIS_URL` | Redis connection string | — (local cache only) |

### Rate Limiting

| Variable | Description | Default |
|----------|-------------|---------|
| `RATE_LIMIT_PER_TENANT` | Requests per minute per tenant | `30` |
| `RATE_LIMIT_GLOBAL` | Total requests per second | `1000` |
| `DAILY_QUOTA_PER_TENANT` | Max API calls per tenant per day | `1000` |

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
| `AUDIT_OUTPUT_PATH` | File path for audit log output (JSONL format) | — (stdout) |
| `AUDIT_BUFFER_SIZE` | Internal event buffer size | `1000` |
| `AUDIT_INCLUDE_REQUEST_BODY` | Include full request bodies in audit records | `false` |

### Multi-Tenancy

| Variable | Description | Default |
|----------|-------------|---------|
| `CACHE_ISOLATION` | Cache isolation mode (`shared` or `tenant`) | `shared` |

### Auth (Advanced)

| Variable | Description | Default |
|----------|-------------|---------|
| `JWKS_REFRESH_INTERVAL` | How often to refresh JWKS keys | `1h` |
| `CACHE_ADMIN_KEY` | Shared secret for admin endpoints (min 16 chars) | — |

---

## Horizontal Scaling

When running multiple instances behind a load balancer:

1. **Set `REDIS_URL`** — Enables shared cache, rate limit counters, and session state across instances.
2. **Use session-affinity** for SSE connections (sticky sessions at L7 LB), OR use the stateless Streamable HTTP transport where clients reconnect and re-fetch state from Redis.
3. **go-rod browser instances** are per-pod. No shared browser pool. Each pod handles its own headless Chrome.

**Architecture (multi-instance):**

```
                  ┌─────────────┐
                  │ Load Balancer│
                  └──────┬──────┘
              ┌──────────┼──────────┐
              │          │          │
         ┌────▼───┐ ┌───▼────┐ ┌──▼─────┐
         │ Pod 1  │ │ Pod 2  │ │ Pod 3  │
         │(server)│ │(server)│ │(server)│
         └────┬───┘ └───┬────┘ └──┬─────┘
              │          │         │
              └──────────┼─────────┘
                         │
                  ┌──────▼──────┐
                  │    Redis    │
                  │  (shared)   │
                  └─────────────┘
```

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
- Google API key is configured and non-empty
- Redis is reachable (if configured)
- Disk cache directory is writable

---

## Graceful Shutdown

On SIGINT/SIGTERM or stdin EOF:
1. Stop accepting new connections
2. Drain in-flight requests (30s timeout)
3. Flush cache to disk
4. Close Redis connections
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
| DELETE | `/admin/tenant/{id}` | Purge all data for a tenant |
| GET | `/admin/audit` | Query audit logs (`tenant_id`, `from`, `to`) |
| GET | `/users/{id}/data` | GDPR Art. 15 — export user data |
| DELETE | `/users/{id}/data` | GDPR Art. 17 — purge user data |

These are HTTP-only operational endpoints, not exposed via MCP tools.

---

## MCP Resources & Prompts

### Resources

| URI | Description |
|-----|-------------|
| `stats://tools` | Per-tool execution metrics (totalCalls, avgLatencyMs, etc.) |
| `stats://sessions` | Count of active sequential research sessions |

### Prompts

| Prompt | Description | Required Args |
|--------|-------------|---------------|
| `comprehensive-research` | Multi-step research process | `topic` |
| `fact-check` | Verify a claim from multiple sources | `claim` |
| `competitive-analysis` | Research competitors in a market | `company` |
| `literature-review` | Systematic academic literature review | `topic` |
