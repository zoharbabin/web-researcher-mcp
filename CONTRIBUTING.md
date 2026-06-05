# Contributing to web-researcher-mcp

Thank you for your interest in contributing to web-researcher-mcp! This project aims to provide reliable, high-quality research tools for AI assistants via the Model Context Protocol, and we welcome contributions from everyone.

Whether you're fixing a typo, adding a search provider, improving documentation, or proposing a new tool — your help makes this project better.

## Table of Contents

- [Development Setup](#development-setup)
- [Running Tests](#running-tests)
- [Code Style](#code-style)
- [Commit Messages](#commit-messages)
- [Pull Request Process](#pull-request-process)
- [Issue Guidelines](#issue-guidelines)
- [Code of Conduct](#code-of-conduct)

## Development Setup

### Prerequisites

- **Go** — version requirement is specified in `go.mod` (the `toolchain` directive
  pins the exact patched release; Go auto-downloads it, so you never build with
  an unpatched compiler)
- **API keys** (for integration/E2E testing):
  - Google Custom Search: `GOOGLE_CUSTOM_SEARCH_API_KEY` and `GOOGLE_CUSTOM_SEARCH_ID`
  - Brave Search (optional): `BRAVE_API_KEY`
- **Chrome/Chromium** — optional, only needed for headless scraping features

Linters and the vulnerability scanner are **not** separate installs — they are
pinned in `go.mod` as `tool` directives and invoked via `go tool`, so every
contributor and CI run uses byte-identical versions (no drift, no "works on my
machine").

### One-time setup

```bash
make tools   # warms the pinned golangci-lint + govulncheck + gosec (go tool fetches on first use anyway)
make hooks   # installs the git pre-commit hook (fmt + vet + lint on staged files)
```

The pre-commit hook keeps commits fast by checking only staged Go files with the
quick gates; the full suite (race, vuln, e2e) runs in CI. Bypass a hook in an
emergency with `git commit --no-verify` — CI still enforces everything.

### Getting Started

```bash
# Clone the repository
git clone https://github.com/zoharbabin/web-researcher-mcp.git
cd web-researcher-mcp

# Download dependencies
go mod download

# Build the binary
go build -o web-researcher-mcp ./cmd/web-researcher-mcp

# Verify everything works
go test ./...
```

### Environment Setup

Copy the environment variables you need for testing:

```bash
export GOOGLE_CUSTOM_SEARCH_API_KEY="your-key"
export GOOGLE_CUSTOM_SEARCH_ID="your-cx"
# Optional:
export BRAVE_API_KEY="your-brave-key"
export SEARCH_PROVIDER="google"  # or brave, serper, searxng, searchapi, duckduckgo, tavily, exa (see search.SupportedProviders)
```

Unit and integration tests do not require API keys. Only E2E tests that hit live services need them.

## Running Tests

```bash
# Unit and integration tests (no API keys needed)
go test ./...

# With race detector (recommended before submitting)
go test -race ./...

# E2E tests (requires API keys)
go test -v ./tests/e2e/...

# Benchmarks
go test -bench=. ./tests/benchmark/

# Specific package
go test ./internal/scraper/...

# With coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Linting

Tools are pinned in `go.mod` and invoked through `go tool` so every contributor and CI run uses byte-identical versions. Use the `make` targets, or the `go tool …` form if running directly — never the bare globally-installed binaries.

```bash
# Run all linters (Makefile target wraps `go tool golangci-lint run`)
make lint

# Auto-fix where possible
go tool golangci-lint run --fix

# Vet
go vet ./...

# Static security analysis (gosec)
make sec

# Dependency vulnerability check
make vuln
```

### Build

```bash
# Standard build
go build -o web-researcher-mcp ./cmd/web-researcher-mcp

# With version info (reads from VERSION file)
go build -ldflags "-X main.version=$(cat VERSION)" -o web-researcher-mcp ./cmd/web-researcher-mcp

# Docker build
docker build -t web-researcher-mcp .
```

## Code Style

This project follows [Effective Go](https://go.dev/doc/effective_go) and enforces style via **golangci-lint**. Key principles:

1. **Accept interfaces, return structs** — for testability and clarity
2. **Context is always the first parameter** — `func DoThing(ctx context.Context, ...)`
3. **Error messages are lowercase, no punctuation** — `fmt.Errorf("invalid query: %w", err)`
4. **Exported names have doc comments** — unexported names generally don't need them
5. **One package per concern** — no "utils" or "helpers" packages
6. **Wrap errors with context** — `fmt.Errorf("brave search for %q: %w", query, err)`
7. **Table-driven tests** — with `t.Parallel()` where possible
8. **No global state** — all dependencies are injected

See `ARCHITECTURE.md` for package organization and `docs/TOOLS.md` for tool specifications.

### Before Submitting

Run the full check suite — the same gate CI enforces:

```bash
make verify   # fmt-check + vet + lint + gosec + vuln + test-race + test-e2e + build
```

## Commit Messages

This project uses [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/). Each commit message should follow this format:

```
<type>(<optional scope>): <description>

[optional body]

[optional footer(s)]
```

### Types

| Type | Purpose |
|------|---------|
| `feat` | A new feature |
| `fix` | A bug fix |
| `docs` | Documentation only changes |
| `style` | Formatting, missing semicolons, etc. (no code change) |
| `refactor` | Code change that neither fixes a bug nor adds a feature |
| `perf` | Performance improvement |
| `test` | Adding or updating tests |
| `build` | Build system or external dependency changes |
| `ci` | CI configuration changes |
| `chore` | Other changes that don't modify src or test files |

### Examples

```
feat(search): add Brave Search provider
fix(scraper): handle timeout on large PDF downloads
docs: update deployment guide for patent providers
test(cache): add benchmark for hybrid cache operations
refactor(content): extract sanitization into pipeline pattern
perf(scraper): reduce allocations in HTML parsing
```

### Breaking Changes

Append `!` after the type/scope, and include a `BREAKING CHANGE:` footer:

```
feat(auth)!: require OAuth 2.1 for HTTP transport

BREAKING CHANGE: HTTP transport now requires a valid JWT token.
STDIO transport is unaffected.
```

## Pull Request Process

1. **Fork and branch** — create a branch from `main` with a descriptive name:
   - `feat/brave-search-provider`
   - `fix/ssrf-ipv6-bypass`
   - `docs/quickstart-guide`

2. **Keep changes focused** — one logical change per PR. Split large features into smaller, reviewable pieces.

3. **Ensure quality** before requesting review — one command runs the full gate:
   - `make verify` — formatting, vet, lint, gosec, govulncheck, race tests, e2e, build
   - (individual targets exist too: `make test-race`, `make lint`, `make sec`, `make vuln`)
   - New code has tests; documentation updated if behavior changes

   `main` is branch-protected: the **Lint**, **Test**, **Security** (govulncheck +
   gosec), and **E2E** CI checks must all pass before a PR can merge, the branch
   must be up to date with `main`, linear history is required, and all PR
   conversations must be resolved. Running `make verify` locally reproduces the
   CI checks exactly (same pinned tool versions via `go tool`). Human approval is
   **not** required (the repo is maintainer-driven — see the merge policy below).

4. **Write a clear PR description** — explain what changed and why. Include:
   - Summary of changes
   - Motivation/context
   - Testing done
   - Screenshots (if UI-related)

5. **Respond to review feedback** — push additional commits (don't force-push during review). Squash will happen at merge.

6. **Benchmarks** — if your change touches hot paths (cache, scraping pipeline, content processing), include before/after benchmark results.

### PR Checklist

- [ ] Full gate passes locally (`make verify`)
- [ ] New functionality has tests
- [ ] Documentation updated (if applicable)
- [ ] Commit messages follow Conventional Commits

### Maintainer Merge Policy

`main` requires **zero human approvals** (this is a maintainer-driven repo, so a
required-reviewer rule would just block the maintainer's own PRs). Quality is
held by two gates instead: the CI checks above, and a mandatory **Copilot review
as a second set of eyes**. Every PR is reviewed by Copilot and every finding is
either fixed or rebutted before merge.

**How Copilot review is triggered:** by the repo setting *Settings → Rules →
Rulesets → "Request pull request review from Copilot"* (a one-time UI toggle).
Copilot **cannot** be requested per-PR via the API or `gh` — it is not a
collaborator, so `gh pr edit --add-reviewer` and the `requested_reviewers`
REST/GraphQL endpoints all reject it. The automatic setting is the only
mechanism; if a fast PR merges before Copilot posts, address its findings in a
follow-up PR.

Per-PR cycle the maintainer follows:

1. Open the PR; CI runs and Copilot review is auto-requested.
2. Wait for Copilot's review to post (`copilot-pull-request-reviewer[bot]`).
3. For **each** Copilot finding: fix it, or reply in-thread explaining why it's
   incorrect — then resolve the conversation. (Copilot only ever `COMMENTED`,
   never `APPROVED`, so its review can't satisfy an approval gate by design.)
4. Confirm all CI checks are green and every Copilot thread is resolved.
5. Merge: `gh pr merge <N> --squash --admin`.

```bash
# Inspect Copilot's findings on a PR. Note the two different bot logins:
# the review summary is authored by `copilot-pull-request-reviewer`, but the
# inline review comments are authored by `Copilot`.
gh pr view <N> --json reviews \
  --jq '.reviews[] | select(.author.login=="copilot-pull-request-reviewer") | .body'
gh api repos/zoharbabin/web-researcher-mcp/pulls/<N>/comments \
  --jq '.[] | select(.user.login=="Copilot") | "\(.path):\(.line // .original_line)  \(.body)"'

# After CI is green and every finding is addressed/resolved:
gh pr merge <N> --squash --admin
```

`--admin` clears the conversation-resolution/up-to-date formalities at merge
time; it is **not** a substitute for steps 2–3 — never run it before Copilot's
findings are genuinely addressed.

## Issue Guidelines

### Reporting Bugs

Please include:
- Go version (`go version`)
- Operating system and architecture
- Steps to reproduce
- Expected vs. actual behavior
- Relevant logs or error messages (redact any API keys)

### Requesting Features

Please include:
- Use case description — what problem does this solve?
- Proposed solution (if you have one)
- Alternatives considered
- Whether you'd be willing to implement it

### Security Issues

**Do NOT report security vulnerabilities via public issues.** See [SECURITY.md](SECURITY.md) for responsible disclosure instructions.

## Code of Conduct

This project follows the [Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md). By participating, you are expected to uphold this code. To report unacceptable behavior, see the confidential reporting instructions in [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md#reporting-concerns).

## Adding a New Tool

Adding a tool requires three files:

1. **Create the handler** in `internal/tools/<toolname>.go`:

```go
package tools

type myToolInput struct {
    Query string `json:"query" jsonschema:"Search query,required"`
}

func registerMyTool(srv *mcp.Server, deps Dependencies) {
    mcp.AddTool(srv, &mcp.Tool{
        Name:         "my_tool",
        Description:  "One-line description for the AI assistant",
        Annotations:  readOnlyAnnotations(true, true),
        OutputSchema: myToolOutputSchema,
    }, func(ctx context.Context, req *mcp.CallToolRequest, input myToolInput) (*mcp.CallToolResult, any, error) {
        start := time.Now()
        // Implementation here — use deps.Cache, deps.Search, etc.
        deps.Metrics.RecordToolCall("my_tool", time.Since(start), nil, "", false)
        auditToolCall(ctx, deps, "my_tool", time.Since(start), nil, "")
        return structuredResult(jsonBytes), nil, nil
    })
}
```

2. **Register it** in `internal/tools/registry.go` — add `registerMyTool(srv, deps)` to `RegisterAll()`.

3. **Add tests** in `internal/tools/tools_test.go` or a dedicated `<toolname>_test.go`.

Key conventions:
- All tool inputs use typed structs with `jsonschema` tags (the SDK auto-generates JSON Schema from these)
- Use `deps.Cache` for caching, `deps.Metrics` for telemetry, `deps.Auditor` for audit logging
- Return validation errors via `toolError(msg)`, upstream errors via `upstreamErrorResponse(toolName, err)`, success via `structuredResult(jsonBytes)` (see `internal/tools/errors.go` and `docs/ERROR_HANDLING.md` for the full pattern)
- Update `docs/TOOLS.md` with a `## Tool N: \`name\`` section — the drift test `TestToolsDocMatchesRegistry` (`internal/tools/metadata_test.go`) fails CI if a registered tool is undocumented or vice-versa

### Write tools and consent-gated (regulated) tools

Most tools are read-only. For the rare tool that mutates server-side state (e.g. `memory_save`, `workspace_contribute`):

- Annotate with `writeAnnotations(idempotent)` instead of `readOnlyAnnotations(...)`. `ReadOnlyHint` becomes `false`; `DestructiveHint` stays `false` — **deletion is never a tool flag**, it is the GDPR erasure endpoint (`DELETE /admin/data`). Update the `writeTools` set and add a `case` in `TestAllToolsHaveAnnotations` (`internal/tools/metadata_test.go`).
- If the tool processes per-user personal data, gate it on consent: `if deps.Consent == nil || !deps.Consent.HasConsent(ctx, consent.PurposeXxx) { return structuredResult(... "status":"no_consent" ...) }`. Take the subject from `auth.UserIDFromContext(ctx)` / `auth.TenantIDFromContext(ctx)` — never from a tool parameter. Refuse `anonymous`.
- Register **conditionally** in `RegisterAll()` — only when the feature dependency is non-Noop (mirror the `if _, isNoop := deps.X.(*pkg.Noop); deps.X != nil && !isNoop` pattern), so the default tool surface is unchanged.
- Register the store's `Exporter`/`Eraser` into the data-subject registry (`internal/datasubject`) in `main.go` so the data is covered by `/admin/data` export + erasure.
- **Add the feature dependency to `setupTestDeps()`** (`internal/tools/tools_test.go`) so the conditionally-registered tool is visible to the drift tests, and add the tool name to `expectedTools` (`metadata_test.go`).

> **Docs-only PRs skip the Go drift gates.** CI sets `code=false` and skips the `test` job when every changed file is docs/meta. A pure-doc edit to `docs/TOOLS.md` will NOT run the drift tests on that PR (the standalone `docs-drift` job covers this — see `.github/workflows/ci.yml`). When a doc edit pairs with a tool/schema change, keep the code file in the same PR so the gates fire.

## Adding a Search Provider

Web search providers implement the `search.Provider` interface (`Web`, `Images`, `News`, `Name`) — the core extension path.

1. **Implement** `search.Provider` in `internal/search/<name>.go` (add a `var _ Provider = (*XProvider)(nil)` assertion; return `(nil, nil)` from any unsupported sub-capability such as `Images` — never an error, which would trip the breaker).
2. **Wire the factory** — add a `case` to both `NewProvider()` and `NewProviderByName()` in `internal/search/provider.go`. The credential check lives in the `NewProviderByName()` case (return the provider only when its key is set).
3. **Add the credential/config** env var to `internal/config/config.go` and document it in `.env.example`.
4. **Make it discoverable** — add the name to `search.SupportedProviders`. `AvailableProviders()` ranges over that list (constructing each via `NewProviderByName()`), so no edit there — the Router picks it up automatically.

Academic providers implement `search.AcademicProvider` and register via `NewAcademicProviderByName()` (`internal/search/domain.go`) + `AvailableAcademicProviders()`. See the existing `openalex.go` / `crossref.go` for the pattern.

## Adding a Patent Provider

Patent providers implement the `PatentProvider` interface for structured patent search from authoritative APIs.

1. **Create the provider** in `internal/search/<provider>.go`:

```go
package search

type MyProvider struct {
    apiKey  string
    deps    Deps
}

func NewMyProvider(apiKey string, deps Deps) *MyProvider {
    return &MyProvider{apiKey: apiKey, deps: deps}
}

func (p *MyProvider) Name() string { return "myprovider" }

func (p *MyProvider) Metadata() ProviderMeta {
    return ProviderMeta{
        Regions:      []string{"US"},       // or []string{"*"} for worldwide
        Capabilities: []string{"search", "biblio"},
        RateClass:    "metered",
        Description:  "My Provider — brief description",
    }
}

func (p *MyProvider) Patents(ctx context.Context, params PatentSearchParams) ([]PatentResult, error) {
    // Wrap in circuit breaker, call API, parse response
    var results []PatentResult
    err := p.deps.Breaker.Execute(func() error {
        // API call and parsing here
        return nil
    })
    return results, err
}
```

2. **Register it** — add a case to `NewPatentProviderByName()` in `internal/search/domain.go` and add the env var to `internal/config/config.go`.

3. **Add tests** — create `internal/search/<provider>_test.go` with httptest mocks and a `_live_test.go` that skips without credentials.

4. **Document** — add the env var to `.env.example` and setup instructions to `docs/API_SETUP.md`.

The `ProviderMeta.Regions` field controls intelligent routing — set it to the jurisdictions your provider covers so queries for other regions skip it automatically.

## Getting Help

- **Questions and discussions**: [GitHub Discussions](https://github.com/zoharbabin/web-researcher-mcp/discussions)
- **Bug reports**: [GitHub Issues](https://github.com/zoharbabin/web-researcher-mcp/issues)
- **Architecture questions**: See [ARCHITECTURE.md](ARCHITECTURE.md)

## Recognition

Contributors are recognized in release notes. Significant contributions may be highlighted in the README.

Thank you for helping make web-researcher-mcp better!
