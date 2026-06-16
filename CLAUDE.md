# CLAUDE.md — web-researcher-mcp

An MCP server in Go that gives AI assistants web search, content extraction, and multi-source research capabilities over STDIO or HTTP transport.

## Commands

```bash
go build -o web-researcher-mcp ./cmd/web-researcher-mcp    # Build
go test ./...                                               # Unit + integration tests
go test -race ./...                                         # Race detector
go test -v ./tests/e2e/...                                  # E2E (needs API keys)
go tool golangci-lint run                                   # Lint (pinned version)
go tool govulncheck ./...                                    # Vulnerability scan (pinned version)
make verify                                                  # fmt-check + vet + lint + gosec + vuln + validate-lenses + test-race + test-e2e + check-python-drift + test-python + build (CI gate; `make all` aliases it)
make test-python                                             # Python SDK unit + integration tests (no binary; mock HTTP server)
make test-python-live                                        # Python SDK live E2E tests (builds Go binary; needs internet)
```

### Local rebuild for IRL testing

`make rebuild-local` (or `scripts/rebuild-local.sh`, or the `/rebuild-local` slash command) does the full **clear-caches → rebuild → reinstall** flow deterministically in one shell call — no LLM roundtrips. Use it after a scraper/server change before restarting the MCP client to test live.

```bash
make rebuild-local                      # clear caches + build + reinstall over the binary on PATH
make rebuild-local ARGS="--no-install"  # build only
make rebuild-local ARGS="--help"        # flags + INSTALL_PATH/CACHE_DIR env overrides
```

It clears the Go build cache (from-scratch compile) and the MCP **response** cache (`*.cache` + `.version`) so a live tool call hits the network fresh, then rebuilds with version ldflags and reinstalls using the macOS-SIGKILL-safe `rm`+`cp`+`codesign` sequence. It deliberately **never** touches personal-data dirs under the cache root (`sessions/`, `persist/`). Restart the MCP client afterward to load the new binary.

## Architecture

```text
cmd/web-researcher-mcp/main.go   # Wiring only — constructs deps, starts server
cmd/gen-python-client/  # Emits Go tool schemas as JSON; piped through scripts/gen_python_client.py to regenerate the Python client
internal/
├── tools/        # One file per tool, typed input structs, registered in registry.go; large-payload resource_link store (artifacts.go: research://artifact/{id})
├── search/       # Provider interface + adapters + Router (multi-provider fallback); DOI enrichment: open-access (Unpaywall) + retraction status (Crossref Retraction Watch); custom/validated lenses
├── scraper/      # 4-tier pipeline: markdown → stealth → HTML → browser (go-rod); SSRF-safe client; link verifier (liveness + Wayback archive)
├── documents/    # PDF, DOCX, PPTX extraction
├── cache/        # Cache interface + hybrid impl (memory + AES-encrypted disk)
├── persist/      # Generic TTL key/value Store (memory or AES-256-GCM disk) — backs token revocation + rate-quota durability
├── redisbackend/ # SOLE go-redis importer: Redis impls of cache/persist/session for HTTP distributed state — gated, fail-fast, encrypted (opt-in via REDIS_URL)
├── content/      # Sanitize, dedup, truncate, quality score, typed source classification + domain-reputation signal, claim-evidence extraction, citation extraction + bibliography read/write (APA/MLA/BibTeX/RIS/CSL-JSON round-trip), content recommendations + auto-formatted components
├── config/       # Env-based config — all vars documented in .env.example
├── server/       # MCP server lifecycle (STDIO + Streamable HTTP) + admin endpoints (cache/session flush, /admin/data, /admin/consent, /admin/analytics, /admin/workspace/members) + operator dashboard (/dashboard HTML + admin-gated /dashboard/data, nonce-CSP)
├── auth/         # OAuth 2.1 middleware (JWKS, audience/issuer validation)
├── audit/        # Auditor interface + structured JSON logging (PodID for cross-pod correlation)
├── session/      # Per-tenant session persistence — Manager interface (memory+disk default, or Redis impl)
├── consent/      # Consent record-verify-honor for regulated features (Checker + typed purposes + Noop default)
├── datasubject/  # GDPR access/erasure registry: (tenantID,userID) Exporter/Eraser fan-out across all personal-data stores
├── useranalytics/# Opt-in consent-gated per-user usage analytics (Recorder + Noop)
├── memory/       # Opt-in consent-gated long-term cross-session memory (Store + Noop, retention TTL)
├── workspace/    # Opt-in shared research workspaces — server enforces data-plane + isolation, host owns membership (Store + Noop)
├── metrics/      # Prometheus counters/histograms per tool + per-tenant aggregate analytics + bounded recent-errors ring (diagnostics://errors/recent)
├── ratelimit/    # Token bucket (per-tenant + global) + optional atomic cross-pod daily quota (Redis)
├── circuit/      # Circuit breaker for external APIs
└── resources/    # MCP Resources (stats:// + diagnostics:// errors/health + lenses://catalog + research://artifact/{id}) + Prompts (research templates) + completion/complete handler (lens/provider/enum arg autocompletion, DI suppliers)
lenses/           # JSON files defining domain lists for site-restricted search (CANONICAL source; go:embed'd into the binary via internal/search/lenses_embed/ so lenses work from any CWD/install — keep in sync with `make sync-lenses`, guarded by TestEmbeddedLensesMatchRoot)
tests/e2e/        # Full process E2E tests
tests/benchmark/  # Performance benchmarks
```

Non-obvious top-level dirs (so they're not mistaken for clutter or duplicates):

```text
docs/             # Published docs (mkdocs site); docs/internal/ = local-only working docs (gitignored, never published)
decks/            # Marp presentation decks (source + self-contained html/pdf), one folder per deck — published to the site under /decks/<name>/ by docs.yml (see decks/README.md)
assets/           # Logos, social-preview, favicon, ProductHunt gallery — consumed by README, the docs site, and registries
scripts/          # Build/release helpers (build-mcpb, build_wheels.py [PyPI wheels], gen_python_client.py [Python client codegen], docker-smoke, sync-version, rebuild-local, sign-windows) — wired into Makefile/CI
.githooks/        # Git pre-commit hook (enabled via `make hooks`) — fmt/lint/vet on staged Go files
hooks/            # Claude Code PLUGIN hook manifest (hooks.json) — NOT a git hook; runs bin/install.sh on session start
bin/              # Claude Code plugin installer (bin/install.sh) — distinct from the root curl installer install.sh
.claude-plugin/   # Claude Code plugin manifest
mcpb/             # .mcpb bundle manifest template (consumed by scripts/build-mcpb.sh at release)
overrides/        # mkdocs-material theme overrides (main.html — OG/Twitter card meta)
```

Registry/manifest files (root, each read by a different external tool): `server.json` (MCP registry), `smithery.yaml` (Smithery), `glama.json` (Glama), `.mcp.json` (local MCP client config), `VERSION` (source of truth, synced by `scripts/sync-version.sh` into server.json + .claude-plugin). Two Dockerfiles are intentional: `Dockerfile` (local/Makefile) and `Dockerfile.release` (GoReleaser). Two installers are intentional: root `install.sh`/`install.ps1` (curl installers) and `bin/install.sh` (plugin hook).

## Design Rules

1. **Zero global state** — all deps flow through `tools.Dependencies` struct (constructed in `main.go`)
2. **Interface-driven** — `cache.Cache`, `search.Provider`, `audit.Auditor` are interfaces; swap implementations without touching callers. Specialized capability interfaces (each a `…Searcher` + `…Provider` pair): `search.PatentSearcher`/`PatentProvider`, `search.AcademicSearcher`/`AcademicProvider`, `search.CitationSearcher` (on academic providers), `search.AnswerSearcher`/`AnswerProvider`, `search.StructuredSearcher`/`StructuredProvider`, and the structured-domain set `search.FilingSearcher`/`FilingProvider`, `search.CaseSearcher`/`CaseProvider`, `search.EconSearcher`/`EconProvider`, `search.TrialSearcher`/`TrialProvider` (`internal/search/structured_domains.go`). Enrichment resolver interfaces: `search.DOIResolver` (exact-entity DOI lookup, `domain.go`), `search.OAResolver` (Unpaywall open-access enrichment, `unpaywall.go`), `search.RetractionResolver` (Crossref retraction status, `retraction.go`)
3. **Errors are values** — tool handlers return `toolError("message")` which sets `IsError: true` on the MCP result; never panic. Upstream errors use `upstreamErrorResponse()`. Scrape errors use typed `ScrapeError{Kind}`. Full error architecture: see `docs/ERROR_HANDLING.md`
4. **Bounded concurrency** — scraping semaphore (5 slots), mutex-serialized browser, per-tenant rate limits
5. **Lens routing** — if `lens` is set, `site:` operators are injected and routed to the configured provider; lenses with a dedicated `cx` route directly to that Google PSE engine
6. **Multi-provider routing** — when `SEARCH_ROUTING` is set, the Router wraps all available providers with per-provider circuit breakers and priority-ordered fallback; transparent to tools via the `search.Provider` interface
7. **Explicit provider honoring** — when a user explicitly requests a provider via the `provider` field, that provider is used exclusively; if it returns empty results (e.g., USPTO for non-US patents), the tool returns empty — no silent fallback
8. **Provider maps** — `deps.SearchProviders`, `deps.PatentProviders`, `deps.AcademicProviders`, `deps.AnswerProviders`, `deps.StructuredProviders` hold all configured providers by name; built at startup via `Available…Providers()`, independent of routing config

## How to Add a Tool

1. Create `internal/tools/<name>.go`:
   - Define a typed input struct with `json` + `jsonschema` tags
   - Write a `register<Name>(srv *mcp.Server, deps Dependencies)` function
   - Use `deps.Cache` for caching, `deps.Metrics` for telemetry, `deps.Auditor` for audit
   - Return validation errors via `toolError(msg)`, upstream errors via `upstreamErrorResponse(toolName, err)`, success via `structuredResult(jsonBytes)`
   - Add `Annotations: readOnlyAnnotations(idempotent, openWorld)` to the tool definition. **Write tools** (mutate state or create an external artifact, e.g. `memory_save` or `archive_source` which triggers an Internet-Archive capture) use `writeAnnotations(idempotent)` instead — `ReadOnlyHint:false`, `DestructiveHint:false` (deletion is the `/admin/data` erasure endpoint, never a tool flag); add a `case` for the tool in `TestAllToolsHaveAnnotations`.
2. Add `register<Name>(srv, deps)` to `RegisterAll()` in `internal/tools/registry.go`. **Regulated/opt-in tools** register conditionally (only when their feature dep is non-Noop) and gate every personal-data operation on `deps.Consent.HasConsent(ctx, consent.Purpose…)`; register the store's `Exporter`/`Eraser` into the `datasubject` registry in `main.go`.
3. Add tests to `internal/tools/tools_test.go`; add the tool name to `expectedTools` (`metadata_test.go`). For a conditionally-registered tool, also wire its feature dep into `setupTestDeps()` so the drift gates exercise it.
4. Document the schema in `docs/TOOLS.md` as a `## Tool N: \`name\`` section (the drift test parses these headers).
5. Run `make gen-python-client` — regenerates `python/web_researcher_mcp/{models.py,client.py,__init__.py}` from the live Go schemas. Stage and commit the result. The `python-drift` CI job and the pre-commit hook both fail if you skip this step.

## How to Add a Search Provider

1. Create `internal/search/<name>.go` implementing `search.Provider` interface (Web, Images, News, Name methods); add a `var _ Provider = (*XProvider)(nil)` assertion. Return `(nil, nil)` from any unsupported sub-capability (e.g. Images) — never an error (that would trip the breaker).
2. Add the name to `search.SupportedProviders` and a case to `NewProvider()`/`NewProviderByName()` in `internal/search/provider.go`. `AvailableProviders()` ranges over `SupportedProviders`, so no edit there.
3. Add the env var to `internal/config/config.go` (field + required-when-selected check) and `.env.example`.
4. To also offer a specialized capability (academic / answer / structured / citation / filing / case / econ / trial), implement the matching `…Provider` interface and register the name in its `Supported…Providers` list + `New…ProviderByName` switch + `Available…Providers` constructor: `internal/search/domain.go` for academic + citation, `synthesis.go` for answer/structured, `structured_domains.go` for the filing/case/econ/trial set (EDGAR/CourtListener/FRED+WorldBank/ClinicalTrials.gov). The matching tool (`academic_search`/`answer`/`structured_search`/`citation_graph`/`filing_search`/`legal_search`/`econ_search`/`clinical_search`) then picks it up with no tool-layer change. A new provider behind an existing interface (e.g. World Bank under `EconProvider`) needs only its `Supported…`/`New…ByName` entry — no tool change. Every provider gets its own `circuit.New(...)` breaker via the `Available…Providers` constructor.

## Key Patterns

- **Tool handler signature**: `func(ctx context.Context, req *mcp.CallToolRequest, input T) (*mcp.CallToolResult, any, error)`
- **Error responses**: `structuredError(msg, ToolError{})` for dual-format errors (text + JSON); `toolError(msg)` for validation-only; `upstreamErrorResponse(toolName, err)` for provider failures; `scrapeErrorResponse(err, url)` for scrape failures. Defined across `internal/tools/errors.go` (`structuredError`), `search.go` (`toolError`, `upstreamErrorResponse`, `structuredResult`), and `scrape.go` (`scrapeErrorResponse`).
- **Provider resolution**: `resolveProvider()` for web search; `resolvePatentSearcher()` for patents; `resolveAcademicSearcher()` for academic; `resolveAnswerSearcher()`/`resolveStructuredSearcher()` for the synthesis tools — all return `*mcp.CallToolResult` errors with full provider list on unknown provider
- **Cache key**: SHA-256 of deterministic params → `deps.Cache.Get/Set`
- **Audit**: every tool call logs `audit.AuditEvent{ToolName, Duration, Success, Metadata, ...}` via `deps.Auditor.Log()`
- **SSRF protection**: `scraper.NewSSRFSafeClient()` validates all resolved IPs before connecting
- **Content pipeline**: raw HTML → sanitize (bluemonday) → dedup (paragraph hashing) → truncate (sentence boundary) → quality score
- **Tool annotations**: read tools use `readOnlyAnnotations(idempotent, openWorld)`; the rare write tool uses `writeAnnotations(idempotent)` (never destructive) — both enforced by `TestAllToolsHaveAnnotations` in CI
- **Consent gate (regulated tools)**: `deps.Consent.HasConsent(ctx, consent.PurposeMemory|PurposeAnalytics|PurposeWorkspace)` is fail-closed; subject identity comes from `auth.UserIDFromContext`/`auth.TenantIDFromContext`, never a tool param
- **Data-subject rights**: per-user stores register an `Exporter`/`Eraser` in `internal/datasubject` (keyed `(tenantID,userID)`) so `/admin/data` export+erasure reaches them
- **Redis isolation**: `internal/redisbackend` is the ONLY package importing go-redis; constructed in one gated place in `main.go` (`Port>0 && REDIS_URL!=""`), fail-fast, encryption-mandatory

## Environment

Required: None — DuckDuckGo works as zero-config fallback (no API key needed).  
For better results: `GOOGLE_CUSTOM_SEARCH_API_KEY`, `GOOGLE_CUSTOM_SEARCH_ID`  
Optional: `SEARCH_PROVIDER` (google|brave|serper|searxng|searchapi|duckduckgo|tavily|exa|hackernews), `SEARCH_ROUTING`, `BRAVE_API_KEY`, `SEARCHAPI_API_KEY`, `TAVILY_API_KEY`, `EXA_API_KEY`, `PORT` (enables HTTP)  
Full list: see `.env.example`

## Release Process

Push a `v*` tag → CI runs GoReleaser → cross-platform binaries + Docker multi-arch (GHCR + Docker Hub) + .mcpb bundles + SBOM + cosign signatures. Downstream gated jobs (each `needs: release`, off the critical path so a publisher failure can't abort the core release): Docker signing, MCP Registry, Smithery (`SMITHERY_ENABLED`), and PyPI platform wheels for uvx/uv/pip (`PYPI_PUBLISH_ENABLED`, Trusted Publishing OIDC). All automated via `.github/workflows/release.yml` + `.goreleaser.yml`.

## Testing

- Unit tests (no network): mock interfaces, table-driven, `t.Parallel()`
- Integration tests: `httptest` servers, real components, mock external APIs
- E2E tests: real binary, real MCP transport, require API keys
- Live tests (`//go:build live`, `make test-live`): hit real provider APIs; skip when creds absent
- Trust-suite accuracy eval (`make test-eval`, `internal/tools/trust_eval_live_test.go`): labeled gold set (fabricated/retracted/real/mischaracterized) → precision/recall per signal; fails on any false positive (mislabeling a legitimate source)
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
- `internal/tools/metadata_test.go` fails CI on drift: `TestToolsDocMatchesRegistry` (docs/TOOLS.md ↔ registry), `TestAllToolsHaveAnnotations`, `TestOutputSchemaMatchesResponse`, `TestToolDescriptionQuality`. These run in the full `test` job AND in a standalone always-run `docs-drift` job (`.github/workflows/ci.yml`) so they fire even on **docs-only** PRs — a `docs/TOOLS.md` edit that drifts from the registry fails CI even when no `.go` file changed.

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
9. **Encrypt sensitive persistent data** — reuse existing AES-256-GCM disk infrastructure when storing to disk: `cache.DiskCache` for cached responses, `persist.DiskStore` for TTL key/value state (token revocation, rate quotas), `session` store for research sessions.
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
