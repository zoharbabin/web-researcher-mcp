# CLAUDE.md — web-researcher-mcp

An MCP server in Go that gives AI assistants web search, content extraction, and multi-source research capabilities over STDIO or HTTP transport.

## Commands

```bash
go build -o web-researcher-mcp ./cmd/web-researcher-mcp    # Build
go test ./...                                               # Unit + integration tests
go test -race ./...                                         # Race detector
go test -tags=e2e -count=1 ./tests/e2e/...                  # E2E (needs e2e build tag + API keys)
go test -tags=live ./...                                    # Live external-API tests (needs API keys)
go tool golangci-lint run                                   # Lint (pinned version)
go tool govulncheck ./...                                   # Vulnerability scan (pinned version)
make verify                                                 # Full CI gate: fmt-check + vet + lint + sec + vuln + validate-lenses + test-race + test-e2e + check-python-drift + test-python + build
make test-python                                            # Python SDK unit + integration tests (mock HTTP server, no binary)
make test-python-live                                       # Python SDK live E2E tests (builds Go binary; needs internet)
make rebuild-local                                          # Clear caches + rebuild + reinstall (macOS-SIGKILL-safe); ARGS="--no-install" to build only
```

Prefer `make` for CI-gated targets — they carry the right flags, pinned tools, and correct sequencing. Use raw `go` commands for one-off interactive use only.

## Contribution Rules

- **Never push directly to `main`.** All changes go through a branch and a pull request — no exceptions, including chore commits and one-liners.
- **Commit format**: `type(scope): subject [(#PR)]` — types: `feat`, `fix`, `chore`, `docs`, `refactor`, `test`, `ci`. Examples: `fix(config): validate CACHE_ENCRYPTION_KEY` · `chore: bump VERSION to 1.36.4` · `docs: full zero-drift audit`.
- **After any edit to `lenses/`**: run `make sync-lenses` before committing — copies changes into `internal/search/lenses_embed/`. `TestEmbeddedLensesMatchRoot` fails CI if they diverge.
- **After any tool schema change**: run `make gen-python-client`, then stage and commit the regenerated `python/web_researcher_mcp/{models.py,client.py,__init__.py}` in the same commit. The `python-drift` CI job and the pre-commit hook both fail if you skip this.

## Architecture

```text
cmd/web-researcher-mcp/main.go   # Wiring only — constructs deps, starts server
cmd/gen-python-client/           # Emits Go tool schemas as JSON → piped through scripts/gen_python_client.py to regenerate the Python client
internal/
├── tools/        # One file per tool, typed input structs, registered in registry.go; large-payload resource_link store (artifacts.go: research://artifact/{id})
├── search/       # Provider interface + adapters + Router (multi-provider fallback); DOI enrichment: open-access (Unpaywall) + retraction status (Crossref Retraction Watch); custom/validated lenses
├── scraper/      # 4-tier pipeline by default (markdown → stealth → HTML → browser/go-rod), optional 5th Exa tier when EXA_API_KEY is set; SSRF-safe client; link verifier (liveness + Wayback archive)
├── documents/    # PDF, DOCX, PPTX extraction
├── cache/        # Cache interface + hybrid impl (memory L1 + optional Redis L2 + AES-encrypted disk L3)
├── persist/      # Generic TTL key/value Store (memory or AES-256-GCM disk) — backs token revocation + rate-quota durability
├── redisbackend/ # SOLE go-redis importer: Redis impls of cache/persist/session for HTTP distributed state — gated, fail-fast, encrypted (opt-in via REDIS_URL)
├── content/      # Sanitize, dedup, truncate, quality score, typed source classification + domain-reputation signal, claim-evidence extraction, citation extraction + bibliography read/write (APA/MLA/BibTeX/RIS/CSL-JSON round-trip), content recommendations + auto-formatted components
├── config/       # Env-based config — all vars documented in .env.example
├── server/       # MCP server lifecycle (STDIO + Streamable HTTP) + admin endpoints (cache/session flush, /admin/data, /admin/consent, /admin/analytics, /admin/workspace/members) + operator dashboard (/dashboard HTML + admin-gated /dashboard/data, nonce-CSP)
├── auth/         # OAuth 2.1 middleware (JWKS, audience/issuer validation)
├── audit/        # Auditor interface + structured JSON logging (PodID for cross-pod correlation)
├── session/      # Per-tenant session persistence — Manager interface (disk-backed default, or Redis impl)
├── consent/      # Consent record-verify-honor for regulated features (Checker + typed purposes + Noop default)
├── datasubject/  # GDPR access/erasure registry: (tenantID,userID) Exporter/Eraser fan-out across all personal-data stores
├── useranalytics/# Opt-in consent-gated per-user usage analytics (Recorder + Noop)
├── memory/       # Opt-in consent-gated long-term cross-session memory (Store + Noop, retention TTL)
├── workspace/    # Opt-in shared research workspaces — server enforces data-plane + isolation, host owns membership (Store + Noop)
├── metrics/      # Prometheus counters/histograms per tool + per-tenant aggregate analytics + bounded recent-errors ring (diagnostics://errors/recent)
├── ratelimit/    # Token bucket (per-IP pre-auth + per-tenant + global) + optional atomic cross-pod daily quota (Redis)
├── circuit/      # Circuit breaker for external APIs
└── resources/    # MCP Resources (stats:// + diagnostics:// errors/health + lenses://catalog + research://artifact/{id}) + Prompts (research templates) + completion/complete handler (lens/provider/enum arg autocompletion, DI suppliers)
lenses/           # JSON files defining domain lists for site-restricted search (CANONICAL source; go:embed'd into the binary via internal/search/lenses_embed/ — keep in sync with `make sync-lenses`, guarded by TestEmbeddedLensesMatchRoot)
tests/e2e/        # Full-process E2E tests (build tag: e2e)
tests/benchmark/  # Performance benchmarks
# non-obvious top-level dirs
docs/             # Published docs (mkdocs site); docs/internal/ = local-only working docs (gitignored, never published)
decks/            # Marp presentation decks (source + self-contained html/pdf) — published to /decks/<name>/ by docs.yml
assets/           # Logos, social-preview, favicon, ProductHunt gallery
scripts/          # Build/release helpers (build-mcpb, build_wheels.py, gen_python_client.py, docker-smoke, sync-version, rebuild-local, sign-windows)
.githooks/        # Git pre-commit hook (enabled via `make hooks`) — fmt/lint/vet on staged Go files
hooks/            # Claude Code PLUGIN hook manifest (hooks.json) — NOT a git hook; runs bin/install.sh on session start
bin/              # Claude Code plugin installer (bin/install.sh) — distinct from the root curl installer install.sh
.claude-plugin/   # Claude Code plugin manifest — do not modify without understanding the plugin bootstrap
mcpb/             # .mcpb bundle manifest template (consumed by scripts/build-mcpb.sh at release)
```

Registry/manifest files (root, each read by a different external tool): `server.json` (MCP registry), `smithery.yaml` (Smithery), `glama.json` (Glama), `.mcp.json` (local MCP client config — edit this to wire env vars for local testing), `VERSION` (source of truth, synced by `scripts/sync-version.sh`). Two Dockerfiles are intentional: `Dockerfile` (local/Makefile) and `Dockerfile.release` (GoReleaser). Two installers are intentional: root `install.sh`/`install.ps1` (curl installers) and `bin/install.sh` (plugin hook).

## Design Rules

1. **Zero global state** — all deps flow through `tools.Dependencies` struct (constructed in `main.go`)
2. **Interface-driven** — `cache.Cache`, `search.Provider`, `audit.Auditor` are interfaces; swap implementations without touching callers. Specialized capability interfaces (each a `…Searcher` + `…Provider` pair): `search.PatentSearcher`/`PatentProvider`, `search.AcademicSearcher`/`AcademicProvider`, `search.CitationSearcher` (on academic providers), `search.AnswerSearcher`/`AnswerProvider`, `search.StructuredSearcher`/`StructuredProvider`, and the structured-domain set `search.FilingSearcher`/`FilingProvider`, `search.CaseSearcher`/`CaseProvider`, `search.EconSearcher`/`EconProvider`, `search.TrialSearcher`/`TrialProvider` (`internal/search/structured_domains.go`). Enrichment resolver interfaces: `search.DOIRegistry` (cross-registrar DOI existence check, `doi_registry.go`), `search.DOIResolver` (exact-entity DOI lookup, `domain.go`), `search.OAResolver` (Unpaywall open-access enrichment, `unpaywall.go`), `search.RetractionResolver` (Crossref retraction status, `retraction.go`)
3. **Errors are values** — tool handlers return `toolError("message")` which sets `IsError: true` on the MCP result; never panic. Upstream errors use `upstreamErrorResponse()`. Scrape errors use typed `ScrapeError{Kind}`. Full error architecture: see `docs/ERROR_HANDLING.md`
4. **Bounded concurrency** — scraping semaphore (default 5 slots, configurable via `MAX_SCRAPE_CONCURRENCY`), mutex-serialized browser, per-tenant rate limits
5. **Lens routing** — if `lens` is set, `site:` operators are injected and routed to the configured provider; lenses with a dedicated `cx` route directly to that Google PSE engine
6. **Multi-provider routing** — when `SEARCH_ROUTING` is set, the Router wraps all available providers with per-provider circuit breakers and priority-ordered fallback; transparent to tools via the `search.Provider` interface
7. **Explicit provider honoring** — when a user explicitly requests a provider via the `provider` field, that provider is used exclusively; if it returns empty results (e.g., USPTO for non-US patents), the tool returns empty — no silent fallback
8. **Provider maps** — `deps.SearchProviders`, `deps.PatentProviders`, `deps.AcademicProviders`, `deps.AnswerProviders`, `deps.StructuredProviders`, `deps.FilingProviders`, `deps.CaseProviders`, `deps.EconProviders`, `deps.TrialProviders`, `deps.LocalProviders`, `deps.ContextProviders` hold all configured providers by name; built at startup via `Available…Providers()`, independent of routing config

## How to Add a Tool

1. Create `internal/tools/<name>.go`:
   - Define a typed input struct with `json` + `jsonschema` tags
   - Write a `register<Name>(srv *mcp.Server, deps Dependencies)` function
   - Use `deps.Cache` for caching, `deps.Metrics` for telemetry, `deps.Auditor` for audit
   - Return validation errors via `toolError(msg)`, upstream errors via `upstreamErrorResponse(toolName, err)`, success via `structuredResult(jsonBytes)`
   - Add `Annotations: readOnlyAnnotations(idempotent, openWorld)` to the tool definition. **Write tools** (mutate state or create an external artifact, e.g. `memory_save` or `archive_source`) use `writeAnnotations(idempotent)` instead — `ReadOnlyHint:false`, `DestructiveHint:false`; add a `case` for the tool in `TestAllToolsHaveAnnotations`.
2. Add `register<Name>(srv, deps)` to `RegisterAll()` in `internal/tools/registry.go`. **Regulated/opt-in tools** register conditionally (only when their feature dep is non-Noop) and gate every personal-data operation on `deps.Consent.HasConsent(ctx, consent.PurposeMemory)` (single purpose per call site); register the store's `Exporter`/`Eraser` into the `datasubject` registry in `main.go`. `filing_search` is the one always-conditionally-registered structured-domain tool — it only registers when `EDGAR_CONTACT_EMAIL` or `OPENALEX_EMAIL` is set; mirror this pattern (not `legal_search`) for any new tool with a required email contact.
3. Add tests to `internal/tools/tools_test.go`; add the tool name to `expectedTools` in `metadata_test.go`. For a conditionally-registered tool, also wire its feature dep into `setupTestDeps()` (in `tools_test.go`) so the drift gates exercise it.
4. Document the schema in `docs/TOOLS.md` as a `## Tool N: \`name\`` section (the drift test parses these headers). **When modifying an existing tool** (renaming params, changing descriptions, adding/removing fields), update the matching `docs/TOOLS.md` section in the same commit — `TestToolsDocMatchesRegistry` fails CI on any drift, including edits to existing tools.
5. Run `make gen-python-client`, then stage and commit the result. The `python-drift` CI job and the pre-commit hook both fail if you skip this.

## How to Add a Search Provider

1. Create `internal/search/<name>.go` implementing `search.Provider` (Web, Images, News, Name methods); add `var _ Provider = (*XProvider)(nil)`. Return `(nil, nil)` from unsupported sub-capabilities (e.g. Images) — never an error (that would trip the breaker).
2. Add the name to `search.SupportedProviders` and a case to `NewProvider()`/`NewProviderByName()` in `internal/search/provider.go`. `AvailableProviders()` ranges over `SupportedProviders` — no edit there.
3. Add the env var to `internal/config/config.go` (field + required-when-selected check) and `.env.example`.
4. For specialized capabilities (academic / answer / structured / citation / filing / case / econ / trial): implement the matching `…Provider` interface and register in its `Supported…Providers` list + `New…ProviderByName` switch + `Available…Providers` constructor — `internal/search/domain.go` (academic + citation), `synthesis.go` (answer/structured), `structured_domains.go` (filing/case/econ/trial). The matching tool picks it up with no tool-layer change. Every provider gets its own `circuit.New(...)` breaker via the `Available…Providers` constructor.

## Key Patterns

- **Tool handler signature**: `func(ctx context.Context, req *mcp.CallToolRequest, input T) (*mcp.CallToolResult, any, error)`
- **Error responses**: `structuredError(msg, ToolError{})` for dual-format errors (text + JSON); `toolError(msg)` for validation-only; `upstreamErrorResponse(toolName, err)` for provider failures; `scrapeErrorResponse(err, url)` for scrape failures. Defined across `internal/tools/errors.go` (`structuredError`), `search.go` (`toolError`, `upstreamErrorResponse`, `structuredResult`), `scrape.go` (`scrapeErrorResponse`).
- **Provider resolution**: `resolveProvider()` for web search; `resolvePatentSearcher()` for patents; `resolveAcademicSearcher()` for academic; `resolveAnswerSearcher()`/`resolveStructuredSearcher()` for synthesis tools — all return `*mcp.CallToolResult` errors with full provider list on unknown provider
- **Cache key**: SHA-256 of deterministic params → `deps.Cache.Get/Set`
- **Audit**: every tool call logs `audit.AuditEvent{ToolName, Duration, Success, Metadata, ...}` via `deps.Auditor.Log()`
- **SSRF protection**: `scraper.NewSSRFSafeClient()` validates all resolved IPs before connecting
- **Content pipeline**: raw HTML → sanitize (bluemonday) → dedup (paragraph hashing) → truncate (sentence boundary) → quality score
- **Tool annotations**: read tools use `readOnlyAnnotations(idempotent, openWorld)`; write tools (`memory_save`, `archive_source`, `workspace_contribute`) use `writeAnnotations(idempotent)` — both enforced by `TestAllToolsHaveAnnotations` in CI
- **Consent gate (regulated tools)**: `deps.Consent.HasConsent(ctx, consent.PurposeMemory)` (pass one `Purpose` constant per call site) is fail-closed; subject identity comes from `auth.UserIDFromContext`/`auth.TenantIDFromContext`, never a tool param
- **Data-subject rights**: per-user stores register an `Exporter`/`Eraser` in `internal/datasubject` (keyed `(tenantID,userID)`) so `/admin/data` export+erasure reaches them
- **Redis isolation**: `internal/redisbackend` is the ONLY package importing go-redis; constructed in one gated place in `main.go` (`Port>0 && REDIS_URL!=""`) — fail-fast, encryption-mandatory
- **Regulated-feature env var naming**: shell/`.zshenv` uses `WEB_RESEARCHER_MCP_` prefix (`WEB_RESEARCHER_MCP_MEMORY_ENABLED`, `WEB_RESEARCHER_MCP_STDIO_USER_ID`, etc.); `.mcp.json` maps these to unprefixed names inside the process. `STDIO_USER_ID` is required for memory/analytics/workspaces to function in STDIO mode.

## Environment

Required: none — DuckDuckGo works as zero-config fallback (no API key needed).  
For better results: set `GOOGLE_CUSTOM_SEARCH_API_KEY` + `GOOGLE_CUSTOM_SEARCH_ID`.  
Full list: `.env.example` (authoritative) and `docs/DEPLOYMENT.md`.

## Testing

- Unit tests (no network): mock interfaces, table-driven, `t.Parallel()`
- Integration tests: `httptest` servers, real components, mock external APIs
- E2E tests (`-tags=e2e`): real binary, real MCP transport, require API keys
- Live tests (`-tags=live`, `make test-live`): hit real provider APIs; skip when creds absent
- Trust-suite accuracy eval (`make test-eval`, `internal/tools/trust_eval_live_test.go`): labeled gold set → precision/recall per signal; fails on any false positive
- Always run `go test -race ./...` before submitting

## Release Process

Push a `v*` tag → CI runs GoReleaser → cross-platform binaries + Docker multi-arch (GHCR + Docker Hub) + .mcpb bundles + SBOM + cosign signatures. Downstream gated jobs (each `needs: release`): Docker signing, MCP Registry, Smithery, and PyPI platform wheels (Trusted Publishing OIDC). All automated via `.github/workflows/release.yml` + `.goreleaser.yml`.

## Security Rules

Non-negotiable for all code changes:

1. **No OWASP Top 10 vulnerabilities** — no command injection, XSS, SQL injection, SSRF, path traversal. If unsure, ask.
2. **Use `scraper.NewSSRFSafeClient()`** for all outbound HTTP fetching user-specified URLs. Never `http.DefaultClient`.
3. **Never log secrets** — API keys, tokens, encryption keys must never appear in logs or error messages, even at debug level.
4. **Errors are values, never panics** — return `toolError()` / `upstreamErrorResponse()` / `structuredError()`. No `panic()` in production paths.
5. **Validate at system boundaries** — tool inputs, HTTP params, env vars, scraped content. Trust within, validate at the edge.
6. **Respect tenant boundaries** — any new shared state must answer "Can tenant A see tenant B's data?" with no.
7. **Don't accumulate data** — new features must not store data beyond the request lifecycle without TTLs and explicit opt-in.
8. **Constant-time comparison for secrets** — use `subtle.ConstantTimeCompare()`, never `==` for auth tokens/keys.
9. **Encrypt sensitive persistent data** — reuse existing AES-256-GCM disk infrastructure: `cache.DiskCache` for cached responses, `persist.DiskStore` for TTL key/value state, `session` store for research sessions.
10. **Minimal dependencies** — Go stdlib preferred. Each new dependency is a supply chain risk. Justify in PR.
11. **Annotate all tools** — every tool must declare `readOnlyAnnotations(idempotent, openWorld)` or `writeAnnotations(idempotent)`. CI test `TestAllToolsHaveAnnotations` enforces this.
12. **Consent-gate all personal-data access** — any feature that stores or reads per-user data (memory, analytics, workspace) must call `deps.Consent.HasConsent(ctx, consent.Purpose…)` before touching the store. Fail-closed: no consent record means no access. Never infer consent from a non-Noop dep or an env var.

Security-sensitive changes (auth, SSRF, cache keys, Dockerfile, CI) require focused review.  
Full security and compliance guidelines: `docs/SECURITY_AND_COMPLIANCE.md`.

## Documentation Guidelines

Every doc and inline comment must reflect the current codebase exactly. No drift, no stale claims. Never include secrets or real keys — placeholders only.

### Structure (for agent-readable docs):

1. **One-line description** at the top — the reader knows what this is without reading further
2. **Commands** are copy-paste ready — no prose to parse
3. **Architecture as a map** — each package gets a single-line annotation so the reader navigates without grepping
4. **Design Rules are mechanical constraints**, not aspirations
5. **How-to sections give exact file paths + function names**
6. **Key Patterns show real signatures** — actual helper names, handler signature, pipeline stages
7. **Environment says required vs optional** in one or two lines, then defers to `.env.example`
8. **Reference Docs table has a "when to read" column**

### Constraints (verified in CI):

- Tool descriptions match code including side effects; read-only/idempotency explicitly marked
- `internal/tools/metadata_test.go` fails CI on drift: `TestToolsDocMatchesRegistry` (docs/TOOLS.md ↔ registry), `TestAllToolsHaveAnnotations`, `TestOutputSchemaMatchesResponse`, `TestToolDescriptionQuality`. These also run in a standalone `docs-drift` CI job on docs-only PRs.
- **Deliberately exclude**: hardcoded tool/provider counts, version numbers, line counts, env var tables (`.env.example` + `docs/DEPLOYMENT.md` are authoritative), dependency lists, architecture diagrams duplicated from `ARCHITECTURE.md`, inlined code that mirrors source
- **Drift-resistant**: reference structural paths and function names (stable); reference interfaces by name; point to other docs for detail instead of duplicating
- **Markdown/GFM**: blank lines between block elements; two trailing spaces or `<br>` for intra-paragraph line breaks; tables have a header separator row; fenced code blocks carry a language identifier

## Reference Docs

| File | When to read |
|---|---|
| `ARCHITECTURE.md` | Design decisions, tech stack, concurrency model |
| `docs/EXTENSION_GUIDE.md` | When to add a tool vs. provider vs. lens vs. enrichment vs. prompt vs. resource |
| `CONTRIBUTING.md` | Code style, commit format, PR process, adding tools/providers |
| `docs/TOOLS.md` | Tool parameter schemas and behavior contracts |
| `docs/ERROR_HANDLING.md` | Error taxonomy, LLM-facing messages, contributor patterns |
| `docs/SECURITY_AND_COMPLIANCE.md` | Security, privacy & compliance guide (all audiences) |
| `docs/SECURITY.md` | Technical security architecture (threat model, defense layers) |
| `docs/DEPLOYMENT.md` | Docker, K8s, client configs, env vars, admin endpoints, scaling |
| `docs/API_SETUP.md` | Getting API keys for each provider |
| `docs/PROVIDERS.md` | Provider comparison: capability matrix, free tiers, quick-pick guide |
| `docs/EXAMPLES.md` | Example tool calls and expected response shapes |
| `docs/CICD.md` | CI/CD pipeline overview, job graph, release steps, debugging |
| `.github/workflows/release.yml` + `.goreleaser.yml` | Cutting a release |
