# Deployment Guide

This guide covers how to build, configure, and run web-researcher-mcp — whether locally on your machine or deployed to a server. Most users only need the Quick Start in the README; this doc is for production deployments and advanced configuration.

## Package Manager Distribution

### AUR (Arch Linux)

```bash
yay -S web-researcher-mcp         # or paru, trizen, etc.
```

Manual install:

```bash
git clone https://aur.archlinux.org/web-researcher-mcp.git
cd web-researcher-mcp
makepkg -si
```

The `PKGBUILD` and `.SRCINFO` in [`packaging/aur/`](../packaging/aur/) are updated automatically on every release by the `update-packaging` CI job. To update a manual install: `yay -Syu web-researcher-mcp`.

### Nix / NixOS

**Run without installing:**

```bash
nix run github:zoharbabin/web-researcher-mcp
```

**Add to a flake:**

```nix
inputs.web-researcher-mcp.url = "github:zoharbabin/web-researcher-mcp";

# In your environment packages:
packages = [ inputs.web-researcher-mcp.packages.${system}.default ];
```

**Profile install:**

```bash
nix profile install github:zoharbabin/web-researcher-mcp
```

The flake in [`packaging/nix/flake.nix`](../packaging/nix/flake.nix) ships pre-built binaries for `x86_64-linux`, `aarch64-linux`, `x86_64-darwin`, and `aarch64-darwin`. Hashes are updated automatically on every release.

### Continue.dev

Continue.dev does not have a package marketplace; configure the server via your `~/.continue/config.json`:

```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "uvx",
      "args": ["web-researcher-mcp"]
    }
  }
}
```

A ready-to-copy snippet is in [`packaging/continue/config.json`](../packaging/continue/config.json).

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
- `GET /health/live` — Liveness probe (always 200, `ok`; a degraded-but-alive
  process must not be killed)
- `GET /health/ready` — Readiness probe. When multi-provider routing is
  configured, returns `503` (with the health snapshot JSON) **only when every
  provider's circuit breaker is open** — the pod cannot serve any query and
  should be pulled from the load balancer; `200` otherwise (`healthy` or
  `degraded`, since fallback providers still serve). With no routing
  (single-provider / zero-config), it is a static `200 ready` — there is no
  breaker ladder to gate on and the process is ready by construction.
- `GET /metrics` — Prometheus metrics
- `GET /dashboard` — read-only operator dashboard (HTML); its data endpoint `GET /dashboard/data` is admin-gated. Both are registered only when `ADMIN_API_KEY` is set. See [Operator Dashboard](#operator-dashboard-http-mode)
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

### Connecting an AI client to a remote HTTP endpoint

Once you have a server running with `PORT` set (locally or in the cloud), point your MCP client at the `/mcp/` endpoint. The path is the same regardless of host.

**Claude Code** (`~/.claude.json`):

```json
{
  "mcpServers": {
    "web-researcher-remote": {
      "type": "http",
      "url": "https://your-server.example.com/mcp/"
    }
  }
}
```

**Cursor** (`~/.cursor/mcp.json`) and **VS Code** (`.vscode/mcp.json`):

```json
{
  "servers": {
    "web-researcher": {
      "type": "http",
      "url": "https://your-server.example.com/mcp/"
    }
  }
}
```

**Claude Desktop** (`~/Library/Application Support/Claude/claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "web-researcher": {
      "type": "http",
      "url": "https://your-server.example.com/mcp/"
    }
  }
}
```

When OAuth is enabled (`OAUTH_ISSUER_URL` set), the client must present a valid Bearer token on each request. When OAuth is not configured, the endpoint is open to any request — restrict access via firewall or reverse-proxy auth if needed.

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
| `SEARCH_PROVIDER` | Primary provider: google, brave, serper, searxng, searchapi, duckduckgo, tavily, exa, hackernews | `google` (variable default); at runtime, when `google` is selected but no Google key is set, the server falls back to the zero-config `duckduckgo` provider |
| `SEARCH_ROUTING` | Multi-provider routing (see below) | — |
| `BRAVE_API_KEY` | Brave Search API key | — |
| `BRAVE_EXTRA_SNIPPETS` | Return up to 5 extra snippets per Brave result | `false` |
| `SERPER_API_KEY` | Serper.dev API key | — |
| `SEARCHAPI_API_KEY` | SearchAPI.io API key | — |
| `TAVILY_API_KEY` | Tavily API key (AI-agent search; sent as a Bearer token) | — |
| `EXA_API_KEY` | Exa API key (neural/semantic search; sent as `x-api-key`). Also backs `academic_search`, the `answer`/`structured_search` tools, and a paid `/contents` scrape fallback tier | — |
| `SEARXNG_URL` | SearXNG instance URL | — |
| `SEARXNG_BASIC_AUTH` | HTTP Basic credential `user:password` for a SearXNG behind Basic auth (malformed value fails startup; never logged) | — |
| `SEARXNG_HEADERS` | Static request headers for SearXNG as comma-separated `Name: Value` pairs (no commas/newlines in a value; a custom `Authorization` overrides `SEARXNG_BASIC_AUTH`) | — |
| `CUSTOM_LENSES_PATH` | Directory of custom lens JSON files, loaded after (and able to override) the bundled lenses; schema-validated at startup. See `lenses/README.md` | — |

### Patent Providers (Optional)

These enable structured patent search via official APIs. Without them, `patent_search` falls back to web search discovery.

| Variable | Description | Coverage |
|----------|-------------|----------|
| `USPTO_API_KEY` | USPTO API key ([data.uspto.gov](https://data.uspto.gov)) | US patents |
| `EPO_OPS_CONSUMER_KEY` | EPO OPS consumer key ([developers.epo.org](https://developers.epo.org)) | Worldwide |
| `EPO_OPS_CONSUMER_SECRET` | EPO OPS consumer secret | Worldwide |
| `LENS_API_TOKEN` | The Lens API token ([lens.org](https://www.lens.org)) | Worldwide + scholarly |

Each configured provider gets an independent circuit breaker. The `patent_search` tool automatically selects providers based on the requested `patent_office` region.

### Academic Providers (Optional)

These enable rich scholarly metadata (DOIs, authors, citation counts, abstracts, OA status) for `academic_search`, and back the `citation_graph` tool. Without them, `academic_search` falls back to site-restricted web search.

| Variable | Description | Default |
|----------|-------------|---------|
| `OPENALEX_EMAIL` | Contact email for the OpenAlex polite pool (287M+ works). Also enables `citation_graph` (counts-only) | — |
| `CROSSREF_EMAIL` | Contact email for the CrossRef polite pool (140M+ DOI-registered works) | — |
| `SEMANTIC_SCHOLAR_API_KEY` | Semantic Scholar API key (200M+ papers + `tldr` + citation intent/influence). Works **without** a key at a lower shared rate; also powers `citation_graph` (rich edges) | — |
| `PUBMED_API_KEY` | NCBI E-utilities API key for PubMed (biomedical literature). **PubMed is always available** keyless (~3 req/s); this key raises the rate (~10 req/s) | — |
| `PUBMED_EMAIL` | Optional NCBI contact for PubMed requests (recommended by NCBI). Falls back to `OPENALEX_EMAIL` | — (falls back to `OPENALEX_EMAIL`) |
| `UNPAYWALL_EMAIL` | Contact email enabling Unpaywall open-access enrichment (fills free-PDF links on DOI-bearing results that lack one). Falls back to `OPENALEX_EMAIL` when unset; no-op when neither is set | — (falls back to `OPENALEX_EMAIL`) |

`citation_graph` registers only when a citation-capable academic provider (Semantic Scholar or OpenAlex) is configured. Open-access enrichment is best-effort and never fails or slows a search beyond its own bounded request.

### Structured-Domain Providers (Optional)

These enable dedicated structured-research tools. Each provider is independent. `filing_search` registers only when its provider is configured; `legal_search`, `econ_search`, `clinical_search`, and `awesome_list_search` each have a keyless provider, so they are **always available** (a key/token only adds coverage or raises limits).

| Variable | Tool | Description | Default |
|----------|------|-------------|---------|
| `EDGAR_CONTACT_EMAIL` | `filing_search` | Contact email for SEC EDGAR's required User-Agent (no API key). Falls back to `OPENALEX_EMAIL`; `filing_search` registers only when one is set | — (falls back to `OPENALEX_EMAIL`) |
| `COURTLISTENER_API_TOKEN` | `legal_search` | Optional token raising the CourtListener rate limit (~100→~5000 req/day). **`legal_search` is always available** (CourtListener works keyless) | — |
| `FRED_API_KEY` | `econ_search` | Federal Reserve Economic Data API key (free at fred.stlouisfed.org). **`econ_search` is always available** via keyless World Bank / OECD / Eurostat providers; this key *adds* FRED's US macro series | — |
| — (none) | `econ_search` | World Bank Open Data (global development indicators, 200+ economies), OECD (SDMX economy indicators), and Eurostat (European official statistics). All keyless; no configuration | — |
| — (none) | `clinical_search` | ClinicalTrials.gov v2 — 400K+ clinical-trial registrations as typed data. Keyless; no configuration. **Always available** | — |
| `ECOSYSTEMS_EMAIL` | `awesome_list_search` | Opts into ecosyste.ms's "polite pool" (mailto=) for a real rate-limit increase on the Free plan. Falls back to `OPENALEX_EMAIL`. **`awesome_list_search` is always available** keyless | — (falls back to `OPENALEX_EMAIL`) |
| `ECOSYSTEMS_API_KEY` | `awesome_list_search` | Forward-compatible: ecosyste.ms's Free plan uses shared pools, not API-key auth, so this is a no-op today — it only takes effect on ecosyste.ms's paid Develop/Scale plans | — |
| `IA_ACCESS_KEY` + `IA_SECRET_KEY` | `archive_source` | Optional Internet Archive S3-style credentials for Save Page Now. **`archive_source` is always available** keyless; both keys together authenticate captures for higher reliability. Never logged. Get a pair at archive.org/account/s3.php | — |
| `GITHUB_TOKEN` | `awesome_list_search`, `github_search`, `scrape_page` | Optional token raising GitHub's public REST API rate limits (Search API, Contents/Gists API). Every surface it touches **is always available** unauthenticated at GitHub's documented public rate limit; the token only raises that ceiling, never gates functionality. No `gh` CLI, no subprocess. Never logged. Create a scopeless fine-grained PAT at github.com/settings/tokens | — |

Each structured-domain provider gets an independent circuit breaker and uses the SSRF-safe HTTP client. `filing_search` returns XBRL company facts (with `facts=true`); `econ_search` returns observations passed through exactly as the source provides them — no rounding; `clinical_search` returns trial metadata for discovery (not medical advice).

### BrandFetch (Optional)

These enable the Tier 1 BrandFetch API for `brand_research`. The tool always works without them — it falls back to CSS extraction, homepage meta, and web search.

| Variable | Description |
|----------|-------------|
| `BRANDFETCH_API_KEY` | BrandFetch Brand + Context API key (`pk_*`). Free tier: 100 req/month. Never logged | 
| `BRANDFETCH_CLIENT_ID` | BrandFetch logo CDN client ID. Free tier: 500K req/month |

### Multi-Provider Routing

When `SEARCH_ROUTING` is set, the server uses all configured providers with priority-ordered fallback:

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

**Operation types:** `web`, `images`, `news`, `academic`, `patents`, `default`. The `academic` and `patents` lists are filtered to providers that implement the academic/patent interface — `academic` accepts `openalex`, `crossref`, `pubmed`, `semanticscholar`, `exa`; `patents` accepts `searchapi`, `epo`, `lens`, `uspto`. Names that don't implement the interface are silently dropped, so use the example values above.

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
| `HTTP_SHUTDOWN_TIMEOUT` | Grace period to drain in-flight requests on SIGINT/SIGTERM before a hard close | `30s` |
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

The per-tenant, global, and per-IP limits below apply **only in HTTP mode** (when `PORT` is set). `MAX_CALLS_PER_DAY` is the exception: it is a transport-agnostic in-process cap that **also applies in STDIO mode** (and to all tools, not just web requests). All other STDIO calls are subject only to upstream API quotas.

| Variable | Description | Default |
|----------|-------------|---------|
| `MAX_CALLS_PER_DAY` | Tool calls per day per `(tenant, user)` pair (STDIO + HTTP). In-process denial-of-wallet backstop; resets at UTC midnight. | — (disabled) |
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
| `CHROME_PATH` | Custom Chrome/Chromium binary path; set to `"disabled"` to turn the browser tier off entirely (no autodetect, no download) | auto-detect |
| `MAX_SCRAPE_CONCURRENCY` | Parallel scrape limit | `5` |
| `MAX_HTML_BYTES` | Decompressed HTML body read cap per scrape tier | `8388608` (8 MB) |
| `MAX_DOCUMENT_BYTES` | Document (PDF/DOCX/PPTX) download cap | `52428800` (50 MB) |

### Features (Opt-In)

Additive output features (content-only, no personal data, no model calls):

| Variable | Description | Default |
|----------|-------------|---------|
| `SOURCE_RECOMMENDATIONS` | Surface advisory "related higher-quality sources" on `search_and_scrape`, derived from the existing transparent quality signals. Content-based; never re-ranks or hides results. Set `false` to omit the field | `true` |
| `GENERATIVE_UI_ENABLED` | Emit additive, deterministic `mcp-auto-formatted` components (source cards, quality-comparison table) built from already-extracted data — no model call. Off → output byte-for-byte unchanged | `false` |

Regulated features (per-user personal data; each activates the consent subsystem and is covered by the data-subject rights endpoints). All default off. Consent is normally host-asserted over HTTP via `POST /admin/consent`; in STDIO it is reachable only by setting `STDIO_USER_ID` (see that row). Per-variable rows note any mode-specific behavior:

| Variable | Description | Default |
|----------|-------------|---------|
| `MEMORY_ENABLED` | Opt-in long-term cross-session memory (`memory_save`/`memory_recall`). Consent-gated on the `memory` purpose | `false` |
| `MEMORY_RETENTION` | Max lifetime of a saved memory before auto-expiry | `2160h` (90d) |
| `USER_ANALYTICS_ENABLED` | Opt-in per-user usage analytics (`get_my_analytics`). Consent-gated on the `analytics` purpose | `false` |
| `WORKSPACES_ENABLED` | Opt-in shared research workspaces (`workspace_contribute`/`workspace_read` + `/admin/workspace/members`). Consent-gated on the `workspace` purpose; membership host-managed | `false` |
| `WORKSPACE_TTL` | Max lifetime of shared-workspace data | `720h` (30d) |
| `STDIO_USER_ID` | **STDIO-only.** Names the single local user so the per-user regulated features (memory, analytics) work without OAuth. When set (+ the feature flag), consent for `memory`/`analytics` (never `workspace`) is auto-granted at startup — grant-only-if-absent (a later withdrawal is never re-granted), audited via `consent.grant`. Data keyed `(tenant=default, user=<value>)`. Allowed: `A-Za-z0-9._@-`, len 1–128, not `anonymous`. Ignored in HTTP mode | _(unset → `anonymous`)_ |

> Enabling any regulated feature activates the consent subsystem automatically — there is no standalone `CONSENT_ENABLED` knob. Consent is asserted by the host (via `POST /admin/consent`) and recorded/verified/honored by the server. See `docs/SECURITY.md` and `docs/SECURITY_AND_COMPLIANCE.md`.
>
> **STDIO single-user exception:** STDIO has no OAuth, so by default the user is `anonymous` and the per-user regulated features (memory, analytics) stay off (fail-closed). Setting `STDIO_USER_ID` is the operator asserting their own identity — in that single-user model the host, operator, and subject are the same person, so the server auto-grants consent for `memory`/`analytics` (never `workspace`) at startup. The grant is **grant-only-if-absent**: a consent decision the user later changes (e.g. a withdrawal recorded out-of-band) is never overwritten on restart, and each grant emits an audited `consent.grant` event.

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

**With `REDIS_URL` set (HTTP mode), distributed state is enabled:**

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

Before running multiple instances behind a load balancer, work through this checklist. Items marked **(per-pod without Redis)** behave differently across pods unless `REDIS_URL` is set.

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

## PyPI (uvx / uv / pip)

The server is published to PyPI as **platform wheels that vendor the prebuilt, signed Go binary** — no Go toolchain, no compilation. This is the broadest, fastest path for Python-native users (the `uvx` one-liner is the officially recommended way to run Python MCP servers):

```bash
# Run on demand (no install) — uv fetches the right binary for your platform:
uvx web-researcher-mcp

# Or install as a persistent tool:
uv tool install web-researcher-mcp

# Or via pip:
pip install web-researcher-mcp
```

**Claude Code config (uvx):**
```json
{
  "mcpServers": {
    "web-researcher": {
      "command": "uvx",
      "args": ["web-researcher-mcp"],
      "env": { "GOOGLE_CUSTOM_SEARCH_API_KEY": "...", "GOOGLE_CUSTOM_SEARCH_ID": "..." }
    }
  }
}
```

The wheels are `py3-none-<platform>` (one per OS/arch; the `none` ABI means any Python 3.10+), built by `scripts/build_wheels.py` (stdlib-only — no build backend) from the same GoReleaser binaries every other channel ships, and published on each release via PyPI Trusted Publishing (OIDC). Publishing is gated on the `PYPI_PUBLISH_ENABLED` GitHub Actions **repository variable** (a CI knob, like `SMITHERY_ENABLED`/`AZURE_SIGNING_ENABLED` — not a runtime env var); an unset repo is a clean no-op. The PyPI side uses Trusted Publishing configured against this repo + the release workflow + the `pypi` environment. The wheel is a thin launcher that `exec`s the bundled binary, so behavior is identical to running it directly.

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
| `/health/ready` | GET | `200 OK` (`ready`/snapshot); `503` when all provider breakers are open | K8s readiness probe |

`/health/live` is a static process-up check: a `200` means the process is running
and the HTTP listener is bound (the server completes all initialization —
providers, cache, sessions, audit — before binding the port, so a successful
connection already implies a fully-constructed server). A degraded-but-alive
process must not be killed, so liveness never flips on dependency state.

`/health/ready` reflects whether the pod can serve a query. With multi-provider
routing configured, it returns `503` (body `{"status":"unhealthy"}`) **only when
every provider's circuit breaker is open** — the pod can serve nothing and should
be pulled from the load balancer — and `200` otherwise (`healthy`/`degraded`,
since fallback providers still serve). With no routing (single-provider /
zero-config) there is no breaker ladder, so it stays a static `200`. The body is
the aggregate status only; the per-provider breaker list is operator data behind
the admin-gated dashboard and `diagnostics://health`, not this unauthenticated probe.

---

## Graceful Shutdown

On SIGINT/SIGTERM (HTTP mode), or SIGINT/SIGTERM/stdin EOF (STDIO mode):
1. Stop accepting new connections
2. Drain in-flight requests (`HTTP_SHUTDOWN_TIMEOUT`, default 30s; hard close on timeout)
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
| GET | `/dashboard/data` | Aggregate JSON powering the operator dashboard (tool stats, active sessions, rate-limit config, provider health, recent errors). Aggregate-only — no per-user/per-query data. Registered with the dashboard (admin key required) |

These are HTTP-only operational endpoints, not exposed via MCP tools. The `/admin/data` endpoints exist only when a personal-data store is registered; `/admin/consent` and `/admin/workspace/members` only when the corresponding regulated feature is enabled.

---

## Operator Observability

Three operator-facing surfaces expose runtime behavior **without leaking infrastructure into LLM content**. They share one rule: routing/health/error internals are operator/debug data, never part of a tool's model-facing result body. The provider *name* is the disclosure boundary — no upstream URLs, credentials, or breaker counts are surfaced anywhere.

### Per-call routing (`_meta.routing`)

When `SEARCH_ROUTING` is active, search-family tool results carry a `routing` block on the MCP `_meta` channel (LLM-invisible, client-app visible): `provider_used`, `providers_attempted`, `fallback`, a coarse `fallback_reason` (`circuit_open` / `primary_unavailable`), `cache_hit`, and `latency_ms`. It answers "why did I get Google when I expected Brave?". Full field contract: see [Routing Provenance](TOOLS.md#routing-provenance-_metarouting) in `docs/TOOLS.md`. The same summary is mirrored to `audit.AuditEvent.Metadata["routing"]`.

### On-demand diagnostics (MCP Resources)

Read-only Resources beside `stats://*`, for operators to read on demand:

| URI | Returns |
|-----|---------|
| `diagnostics://errors/recent` | The most recent tool errors (bounded ring, newest-first): tool, error kind, provider, redacted cause. Memory-only and bounded — no unbounded accumulation, no disk. Scoped to the caller's tenant when authenticated. Causes pass through `audit.MaskSecrets`, so no secrets, user queries, or full URLs appear |
| `diagnostics://health` | Live provider health: an overall status (`healthy` / `degraded` / `unhealthy`) plus each routed provider's circuit-breaker state. Complements `stats://providers` (which lists *configured* providers) with *current* availability. Empty/`healthy` when multi-provider routing is not enabled (no breaker ladder to observe) |

### Operator dashboard (HTTP mode)

A lightweight, read-only, **aggregate-only** dashboard at `GET /dashboard` for self-hosters who don't run their own Grafana/Prometheus stack. It is a single self-contained HTML page (no CDN, no build step) that polls the admin-gated `GET /dashboard/data` and renders per-tool call counts / latency (avg, p95) / error rates, active session count, rate-limit configuration, live provider/breaker health, and the recent-errors ring.

- **Auth:** the page is an inert shell that prompts for the admin key client-side; `GET /dashboard/data` is gated by `X-Admin-Key` exactly like `/admin/*`. Both routes register **only when `ADMIN_API_KEY` is set**.
- **CSP:** each page response sets a per-request nonce-based `Content-Security-Policy` (`default-src 'none'`; nonce'd inline script/style; `connect-src 'self'`; `frame-ancestors 'none'`) — no `unsafe-inline`, no third-party origins.
- **STDIO unaffected:** the dashboard is HTTP-only by construction (it lives in `ServeHTTP`).
- **No new data:** it visualizes aggregate operational data that already exists via `/metrics` and the Resources above — no per-user, per-query, or tenant-identifiable data, and no new collection.

---

## Key Rotation

The server uses two independent secrets. Both rotate without downtime.

### Admin key (`ADMIN_API_KEY`)

The admin key is stateless — rotating it is a single env-var change:

1. Generate a new key: `openssl rand -hex 32`.
2. Update `ADMIN_API_KEY` in your deployment and restart (or rolling-restart) the pods.
3. Update any operational scripts/dashboards that send `X-Admin-Key`.

There is no stored state encrypted under the admin key, so no migration is needed. In a rolling deployment, in-flight admin calls against an old pod use that pod's old key until it cycles; admin endpoints are operational, not user-facing, so a brief overlap is harmless.

### Encryption key (`CACHE_ENCRYPTION_KEY`) — zero-downtime re-encryption

Disk-persisted data (cache, sessions, and any encrypted `persist.Store` namespace) is sealed with AES-256-GCM under `CACHE_ENCRYPTION_KEY`. Rotating it without stranding existing data uses the previous-key fallback:

1. Move the current key to `CACHE_ENCRYPTION_KEY_PREV` and set a new 64-hex `CACHE_ENCRYPTION_KEY` (generate with `openssl rand -hex 32`).
2. Restart. On every read, data sealed with the previous key is **decrypt-fall-back** decrypted and **lazily re-encrypted** with the new key — so hot data migrates automatically with no flush and no downtime.
3. After at least one full data lifetime (e.g. `SESSION_TTL` for sessions, the cache TTL for cache) has elapsed, remove `CACHE_ENCRYPTION_KEY_PREV`. Any still-unread blobs from before the rotation expire naturally.

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
| `lenses://catalog` | All registered lenses with their names, domains, and descriptions |
| `diagnostics://errors/recent` | Bounded ring of recent errors for operator diagnostics |
| `diagnostics://health` | Server health — version, uptime, provider availability |
| `research://artifact/{id}` | Large-payload resource store for tool results that exceed inline size limits |

### Prompts

| Prompt | Description | Required Args |
|--------|-------------|---------------|
| `comprehensive-research` | Multi-step research process | `topic` |
| `fact-check` | Verify a claim from multiple sources | `claim` |
| `competitive-analysis` | Research competitors in a market | `company` |
| `literature-review` | Systematic academic literature review | `topic` |
| `brand-guidelines` | Extract and document a company's brand identity | `company` |
| `company-recon` | OSINT company reconnaissance profile | `company` |
