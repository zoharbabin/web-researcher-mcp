# CLAUDE.md — web-researcher-mcp

An MCP server in Go that gives AI assistants web search, content extraction, and multi-source research capabilities over STDIO or HTTP transport.

## Commands

```bash
go build -o web-researcher-mcp ./cmd/web-researcher-mcp    # Build
go test ./...                                               # Unit + integration tests
go test -race ./...                                         # Race detector
go test -v ./tests/e2e/...                                  # E2E (needs API keys)
golangci-lint run                                           # Lint
govulncheck ./...                                           # Vulnerability scan
make all                                                    # lint + vet + vuln + test + build
```

## Architecture

```
cmd/web-researcher-mcp/main.go   # Wiring only — constructs deps, starts server
internal/
├── tools/        # One file per tool, typed input structs, registered in registry.go
├── search/       # Provider interface + 4 adapters (Google, Brave, Serper, SearXNG)
├── scraper/      # 4-tier pipeline: markdown → stealth → HTML → browser (go-rod)
├── documents/    # PDF, DOCX, PPTX extraction
├── cache/        # Cache interface + hybrid impl (memory LRU + AES-encrypted disk)
├── content/      # Sanitize, dedup, truncate, quality score, citation extraction
├── config/       # Env-based config — all vars documented in .env.example
├── server/       # MCP server lifecycle (STDIO + HTTP/SSE)
├── auth/         # OAuth 2.1 middleware (JWKS, audience/issuer validation)
├── audit/        # Auditor interface + structured JSON logging
├── session/      # Per-tenant session state (sync.Map or Redis)
├── metrics/      # Prometheus counters/histograms per tool
├── ratelimit/    # Token bucket (per-tenant + global)
├── circuit/      # Circuit breaker for external APIs
└── resources/    # MCP Resources (stats) + Prompts (research templates)
lenses/           # JSON files defining domain lists for site-restricted search
tests/e2e/        # Full process E2E tests
tests/benchmark/  # Performance benchmarks
```

## Design Rules

1. **Zero global state** — all deps flow through `tools.Dependencies` struct (constructed in `main.go`)
2. **Interface-driven** — `cache.Cache`, `search.Provider`, `audit.Auditor` are interfaces; swap implementations without touching callers
3. **Errors are values** — tool handlers return `toolError("message")` which sets `IsError: true` on the MCP result; never panic
4. **Bounded concurrency** — scraping semaphore (5 slots), browser pool (3 slots), per-tenant rate limits
5. **Lens routing** — if `lens` is set, `site:` operators are injected and routed to the configured provider; lenses with a dedicated `cx` route directly to that Google PSE engine

## How to Add a Tool

1. Create `internal/tools/<name>.go`:
   - Define a typed input struct with `json` + `jsonschema` tags
   - Write a `register<Name>(srv *mcp.Server, deps Dependencies)` function
   - Use `deps.Cache` for caching, `deps.Metrics` for telemetry, `deps.Auditor` for audit
   - Return errors via `toolError(msg)`, success via `textResult(json)`
2. Add `register<Name>(srv, deps)` to `RegisterAll()` in `internal/tools/registry.go`
3. Add tests to `internal/tools/tools_test.go`
4. Document the schema in `docs/TOOLS.md`

## How to Add a Search Provider

1. Create `internal/search/<name>.go` implementing `search.Provider` interface (Web, Images, News, Name methods)
2. Add a case to the switch in `search.NewProvider()` in `internal/search/provider.go`
3. Add the env var to `internal/config/config.go` and `.env.example`

## Key Patterns

- **Tool handler signature**: `func(ctx context.Context, req *mcp.CallToolRequest, input T) (*mcp.CallToolResult, any, error)`
- **Cache key**: SHA-256 of deterministic params → `deps.Cache.Get/Set`
- **Audit**: every tool call logs `audit.AuditEvent{ToolName, Duration, Success, Metadata, ...}` via `deps.Auditor.Log()`
- **SSRF protection**: `scraper.NewSSRFSafeClient()` validates all resolved IPs before connecting
- **Content pipeline**: raw HTML → sanitize (bluemonday) → dedup (paragraph hashing) → truncate (sentence boundary) → quality score

## Environment

Required: `GOOGLE_CUSTOM_SEARCH_API_KEY`, `GOOGLE_CUSTOM_SEARCH_ID`
Optional: `SEARCH_PROVIDER` (brave|google|serper|searxng), `BRAVE_API_KEY`, `PORT` (enables HTTP), `REDIS_URL` (shared state)
Full list: see `.env.example`

## Release Process

Push a `v*` tag → CI runs GoReleaser → cross-platform binaries + Docker multi-arch (GHCR + Docker Hub) + .mcpb bundles + SBOM + cosign signatures. All automated via `.github/workflows/release.yml` + `.goreleaser.yml`.

## Testing

- Unit tests (no network): mock interfaces, table-driven, `t.Parallel()`
- Integration tests: `httptest` servers, real components, mock external APIs
- E2E tests: real binary, real MCP transport, require API keys
- Always run `go test -race ./...` before submitting

## Documentation Guidelines

Docs must be **drift-resistant by design**:

1. **No hardcoded counts** — don't write "8 tools" or "4 providers"; use "multiple" or let a table speak for itself
2. **No version numbers** — `go.mod` is the source of truth for dependency versions; never inline them in prose or tables
3. **No duplicated content** — each fact lives in one place; other docs point to it (e.g., env vars live in `docs/DEPLOYMENT.md`, not also in README)
4. **No detailed file listings** — use package-level overviews; readers can run `tree` or `find` for the full picture
5. **No inlined code snippets that mirror source** — describe the pattern and point to the canonical file (e.g., "see `internal/tools/search.go`") instead of copy-pasting structs that will rot
6. **Source-of-truth pointers** — when referencing something that changes (versions, schemas, domains), name the file where it's defined

## Reference Docs

| File | When to read |
|------|--------------|
| `ARCHITECTURE.md` | Understanding design decisions, tech stack, concurrency model |
| `CONTRIBUTING.md` | Code style, commit format, PR process, adding tools |
| `docs/TOOLS.md` | Tool parameter schemas and behavior contracts |
| `docs/SECURITY.md` | Threat model, SSRF, auth, compliance (SOC2/GDPR/FedRAMP) |
| `docs/DEPLOYMENT.md` | Docker, K8s, client configs, admin endpoints, scaling |
