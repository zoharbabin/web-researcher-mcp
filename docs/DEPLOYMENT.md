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

Output: single static binary, ~20MB. No runtime dependencies.

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

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /web-researcher-mcp ./cmd/web-researcher-mcp

FROM gcr.io/distroless/static-debian12
COPY --from=builder /web-researcher-mcp /web-researcher-mcp
COPY lenses/ /lenses/
ENTRYPOINT ["/web-researcher-mcp"]
```

```bash
docker build -t web-researcher-mcp .
docker run -p 3000:3000 \
  -e GOOGLE_CUSTOM_SEARCH_API_KEY=... \
  -e GOOGLE_CUSTOM_SEARCH_ID=... \
  -e PORT=3000 \
  -e OAUTH_ISSUER_URL=... \
  -e OAUTH_AUDIENCE=... \
  web-researcher-mcp
```

**For headless browser (chromedp):**
```dockerfile
FROM chromedp/headless-shell:latest AS chrome
FROM gcr.io/distroless/static-debian12
COPY --from=chrome /headless-shell/ /headless-shell/
COPY --from=builder /web-researcher-mcp /web-researcher-mcp
ENV CHROME_PATH=/headless-shell/headless-shell
```

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
| `GOOGLE_CUSTOM_SEARCH_API_KEY` | Google API key | `AIzaSy...` (39 chars) |
| `GOOGLE_CUSTOM_SEARCH_ID` | Search engine ID | `017576662512468239146:omuauf_gy1x` |

### Search Provider

| Variable | Description | Default |
|----------|-------------|---------|
| `SEARCH_PROVIDER` | Primary provider | `google` (until Brave adapter ships) |
| `SEARCH_FALLBACK_PROVIDER` | Fallback provider | — |
| `BRAVE_API_KEY` | Brave Search API key | — |
| `SERPER_API_KEY` | Serper.dev API key | — |
| `SEARXNG_URL` | SearXNG instance URL | — |
| `CUSTOM_LENSES_PATH` | External lenses directory | — |

### HTTP Transport

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | HTTP listen port (enables HTTP mode) | — (STDIO only) |
| `OAUTH_ISSUER_URL` | JWT issuer URL | — |
| `OAUTH_AUDIENCE` | Expected JWT audience | — |
| `ALLOWED_ORIGINS` | CORS origins (comma-separated) | `*` |
| `TLS_CERT_FILE` | TLS certificate path | — |
| `TLS_KEY_FILE` | TLS key path | — |

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

---

## Horizontal Scaling

When running multiple instances behind a load balancer:

1. **Set `REDIS_URL`** — Enables shared cache, rate limit counters, and session state across instances.
2. **Use session-affinity** for SSE connections (sticky sessions at L7 LB), OR use the stateless Streamable HTTP transport where clients reconnect and re-fetch state from Redis.
3. **Chromedp instances** are per-pod. No shared browser pool. Each pod handles its own headless Chrome.

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
