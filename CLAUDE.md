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
├── search/       # Provider interface + adapters + Router (multi-provider fallback)
├── scraper/      # 4-tier pipeline: markdown → stealth → HTML → browser (go-rod)
├── documents/    # PDF, DOCX, PPTX extraction
├── cache/        # Cache interface + hybrid impl (memory + AES-encrypted disk)
├── content/      # Sanitize, dedup, truncate, quality score, citation extraction
├── config/       # Env-based config — all vars documented in .env.example
├── server/       # MCP server lifecycle (STDIO + Streamable HTTP)
├── auth/         # OAuth 2.1 middleware (JWKS, audience/issuer validation)
├── audit/        # Auditor interface + structured JSON logging
├── session/      # Per-tenant session persistence (memory index + encrypted disk)
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
2. **Interface-driven** — `cache.Cache`, `search.Provider`, `audit.Auditor` are interfaces; swap implementations without touching callers. Specialized interfaces: `search.PatentSearcher`, `search.AcademicSearcher`, `search.PatentProvider`, `search.AcademicProvider`
3. **Errors are values** — tool handlers return `toolError("message")` which sets `IsError: true` on the MCP result; never panic. Upstream errors use `upstreamErrorResponse()`. Scrape errors use typed `ScrapeError{Kind}`. Full error architecture: see `docs/ERROR_HANDLING.md`
4. **Bounded concurrency** — scraping semaphore (5 slots), mutex-serialized browser, per-tenant rate limits
5. **Lens routing** — if `lens` is set, `site:` operators are injected and routed to the configured provider; lenses with a dedicated `cx` route directly to that Google PSE engine
6. **Multi-provider routing** — when `SEARCH_ROUTING` is set, the Router wraps all available providers with per-provider circuit breakers and priority-ordered fallback; transparent to tools via the `search.Provider` interface
7. **Explicit provider honoring** — when a user explicitly requests a provider via the `provider` field, that provider is used exclusively; if it returns empty results (e.g., USPTO for non-US patents), the tool returns empty — no silent fallback
8. **Provider maps** — `deps.SearchProviders`, `deps.PatentProviders`, `deps.AcademicProviders` hold all configured providers by name; built at startup via `AvailableProviders()`, independent of routing config

## How to Add a Tool

1. Create `internal/tools/<name>.go`:
   - Define a typed input struct with `json` + `jsonschema` tags
   - Write a `register<Name>(srv *mcp.Server, deps Dependencies)` function
   - Use `deps.Cache` for caching, `deps.Metrics` for telemetry, `deps.Auditor` for audit
   - Return validation errors via `toolError(msg)`, upstream errors via `upstreamErrorResponse(toolName, err)`, success via `structuredResult(jsonBytes)`
   - Add `Annotations: readOnlyAnnotations(idempotent, openWorld)` to the tool definition
2. Add `register<Name>(srv, deps)` to `RegisterAll()` in `internal/tools/registry.go`
3. Add tests to `internal/tools/tools_test.go`
4. Document the schema in `docs/TOOLS.md`

## How to Add a Search Provider

1. Create `internal/search/<name>.go` implementing `search.Provider` interface (Web, Images, News, Name methods)
2. Add a case to the switch in `search.NewProvider()` and `NewProviderByName()` in `internal/search/provider.go`
3. Add the env var to `internal/config/config.go` and `.env.example`
4. Add a credential check in `AvailableProviders()` so the Router can discover it

## Key Patterns

- **Tool handler signature**: `func(ctx context.Context, req *mcp.CallToolRequest, input T) (*mcp.CallToolResult, any, error)`
- **Error responses**: `structuredError(msg, ToolError{})` for dual-format errors (text + JSON); `toolError(msg)` for validation-only; `upstreamErrorResponse(toolName, err)` for provider failures; `scrapeErrorResponse(err, url)` for scrape failures. All defined in `internal/tools/errors.go`
- **Provider resolution**: `resolveProvider()` for web search; `resolvePatentSearcher()` for patents; `resolveAcademicSearcher()` for academic — all return `*mcp.CallToolResult` errors with full provider list on unknown provider
- **Cache key**: SHA-256 of deterministic params → `deps.Cache.Get/Set`
- **Audit**: every tool call logs `audit.AuditEvent{ToolName, Duration, Success, Metadata, ...}` via `deps.Auditor.Log()`
- **SSRF protection**: `scraper.NewSSRFSafeClient()` validates all resolved IPs before connecting
- **Content pipeline**: raw HTML → sanitize (bluemonday) → dedup (paragraph hashing) → truncate (sentence boundary) → quality score
- **Tool annotations**: all tools use `readOnlyAnnotations(idempotent, openWorld)` — enforced by `TestAllToolsHaveAnnotations` in CI

## Environment

Required: None — DuckDuckGo works as zero-config fallback (no API key needed).  
For better results: `GOOGLE_CUSTOM_SEARCH_API_KEY`, `GOOGLE_CUSTOM_SEARCH_ID`  
Optional: `SEARCH_PROVIDER` (google|brave|serper|searxng|searchapi|duckduckgo), `SEARCH_ROUTING`, `BRAVE_API_KEY`, `SEARCHAPI_API_KEY`, `PORT` (enables HTTP)  
Full list: see `.env.example`

## Release Process

Push a `v*` tag → CI runs GoReleaser → cross-platform binaries + Docker multi-arch (GHCR + Docker Hub) + .mcpb bundles + SBOM + cosign signatures. All automated via `.github/workflows/release.yml` + `.goreleaser.yml`.

## Testing

- Unit tests (no network): mock interfaces, table-driven, `t.Parallel()`
- Integration tests: `httptest` servers, real components, mock external APIs
- E2E tests: real binary, real MCP transport, require API keys
- Always run `go test -race ./...` before submitting

## Documentation Guidelines

**TOP RULE — accuracy above all:** every doc (file *and* inline comment) must reflect the current codebase exactly. No drift, no hallucinations, no stale claims. Every feature, config, architecture flow, and business workflow must be documented, easy to start with, easy to follow, and easy to extend. Never include secrets, private data, or real keys — placeholders only.

### Write docs for an agent (structure):
1. **One-line description** at the top — the reader knows what this is without reading further
2. **Commands** are copy-paste ready — no prose to parse
3. **Architecture as a map, not a lecture** — each package gets a single-line purpose annotation so the reader navigates to the right file in one step
4. **Design Rules are mechanical constraints**, not aspirations — "if `lens` is set, inject `site:` and route to the PSE", not "we value flexibility"
5. **How-to sections give exact file paths + function names** — the reader opens the right file and follows the pattern without grepping
6. **Key Patterns show real signatures** — the actual helper names, handler signature, and pipeline stages the reader will encounter
7. **Environment says required vs optional** in one or two lines, then defers to `.env.example` for the full list
8. **Reference Docs table has a "when to read" column** — the reader opens the one doc relevant to the task

### Tool-doc correctness (verified in CI):
- Tool descriptions match code, including side effects; read-only/idempotency explicitly marked
- Output schemas surface freshness/provenance where relevant (`source`, `citation`, cache `_meta`)
- Destructive operations are **separate tools**, never a flag on a read tool
- Auth/tenant scope is visible in the result or audit receipt (`tenant_id`, `user_id`)
- `internal/tools/metadata_test.go` fails CI on drift: `TestToolsDocMatchesRegistry` (docs/TOOLS.md ↔ registry), `TestAllToolsHaveAnnotations`, `TestOutputSchemaMatchesResponse`, `TestToolDescriptionQuality`

### Deliberately EXCLUDE (these change → they drift):
- No hardcoded counts (tool/provider count — `registry.go` / `search.SupportedProviders` are the truth)
- No version numbers (`go.mod` is the truth)
- No line counts
- No env var tables outside `.env.example` + `docs/DEPLOYMENT.md` (those are authoritative)
- No dependency lists (`go.mod` is the truth)
- No architecture diagram duplicated from `ARCHITECTURE.md` (one home, too large to maintain twice)
- No inlined code that mirrors source — describe the pattern, point to the canonical file

### Drift-resistant by design:
- Reference structural file paths and function names (unlikely to change)
- Reference interfaces by name (stable contracts)
- Point to other docs for detail instead of duplicating

### Markdown (GitHub compliance):
- Valid GFM; blank lines between block elements (headings, paragraphs, lists, tables, code blocks)
- Two trailing spaces or `<br>` for intra-paragraph line breaks (bare newlines do NOT break on GitHub)
- Tables have a header separator row (`|---|---|`); fenced code blocks carry a language identifier
- No trailing whitespace except intentional line breaks; standard `[text](url)` / `![alt](url)` links

## Security Rules

Non-negotiable rules for all code changes (human or AI agent):

1. **No OWASP Top 10 vulnerabilities** — no command injection, XSS, SQL injection, SSRF, path traversal. If unsure, ask.
2. **Use `scraper.NewSSRFSafeClient()`** for all outbound HTTP fetching user-specified URLs. Never `http.DefaultClient`.
3. **Never log secrets** — API keys, tokens, encryption keys must never appear in logs or error messages, even at debug level.
4. **Errors are values, never panics** — return `toolError()` / `upstreamErrorResponse()` / `structuredError()`. No `panic()` in production paths.
5. **Validate at system boundaries** — tool inputs, HTTP params, env vars, scraped content. Trust within, validate at the edge.
6. **Respect tenant boundaries** — any new shared state must consider: "Can tenant A see tenant B's data?" Answer must be no.
7. **Don't accumulate data** — new features should not store data beyond the request lifecycle without TTLs and explicit opt-in.
8. **Constant-time comparison for secrets** — use `subtle.ConstantTimeCompare()`, never `==` for auth tokens/keys.
9. **Encrypt sensitive persistent data** — use existing `cache.DiskCache` GCM infrastructure when storing to disk.
10. **Minimal dependencies** — prefer Go stdlib. Each new dependency is a supply chain risk. Justify in PR.
11. **Annotate all tools** — every tool must declare `readOnlyAnnotations(idempotent, openWorld)`. CI test enforces this.

Security-sensitive changes (auth, SSRF, cache keys, Dockerfile, CI) require focused review.  
Full security and compliance guidelines: see `docs/SECURITY_AND_COMPLIANCE.md`.

## Reference Docs

| File | When to read |
|------|--------------|
| `ARCHITECTURE.md` | Understanding design decisions, tech stack, concurrency model |
| `CONTRIBUTING.md` | Code style, commit format, PR process, adding tools/providers |
| `docs/TOOLS.md` | Tool parameter schemas and behavior contracts |
| `docs/ERROR_HANDLING.md` | Error taxonomy, LLM-facing messages, GitHub issue guidance, contributor patterns |
| `docs/SECURITY_AND_COMPLIANCE.md` | **Comprehensive security, privacy & compliance guide** (all audiences) |
| `docs/SECURITY.md` | Detailed technical security architecture (threat model, defense layers) |
| `docs/DEPLOYMENT.md` | Docker, K8s, client configs, env vars, admin endpoints, scaling |
| `docs/API_SETUP.md` | Getting API keys for each provider (step-by-step) |
| `docs/EXAMPLES.md` | Example tool calls and expected response shapes |
