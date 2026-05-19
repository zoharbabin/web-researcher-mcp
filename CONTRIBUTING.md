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

- **Go** — version requirement is specified in `go.mod`
- **API keys** (for integration/E2E testing):
  - Google Custom Search: `GOOGLE_CUSTOM_SEARCH_API_KEY` and `GOOGLE_CUSTOM_SEARCH_ID`
  - Brave Search (optional): `BRAVE_API_KEY`
- **Chrome/Chromium** — optional, only needed for headless scraping features
- **Redis** — optional, only for multi-instance cache testing
- **golangci-lint** — for linting (`go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`)

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
export SEARCH_PROVIDER="google"  # or brave, serper, searxng
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

```bash
# Run all linters
golangci-lint run

# Auto-fix where possible
golangci-lint run --fix

# Vet
go vet ./...

# Security vulnerability check
govulncheck ./...
```

### Build

```bash
# Standard build
go build -o web-researcher-mcp ./cmd/web-researcher-mcp

# With version info
go build -ldflags "-X main.version=$(git describe --tags)" -o web-researcher-mcp ./cmd/web-researcher-mcp

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

Run the full check suite:

```bash
golangci-lint run && go test -race ./... && govulncheck ./...
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
docs: update deployment guide for Redis configuration
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

3. **Ensure quality** before requesting review:
   - All tests pass: `go test -race ./...`
   - Lint is clean: `golangci-lint run`
   - No vulnerabilities: `govulncheck ./...`
   - New code has tests
   - Documentation is updated if behavior changes

4. **Write a clear PR description** — explain what changed and why. Include:
   - Summary of changes
   - Motivation/context
   - Testing done
   - Screenshots (if UI-related)

5. **Respond to review feedback** — push additional commits (don't force-push during review). Squash will happen at merge.

6. **Benchmarks** — if your change touches hot paths (cache, scraping pipeline, content processing), include before/after benchmark results.

### PR Checklist

- [ ] Tests pass (`go test -race ./...`)
- [ ] Lint clean (`golangci-lint run`)
- [ ] No new vulnerabilities (`govulncheck ./...`)
- [ ] New functionality has tests
- [ ] Documentation updated (if applicable)
- [ ] Commit messages follow Conventional Commits

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

This project follows the [Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md). By participating, you are expected to uphold this code. Please report unacceptable behavior to zohar.babin@gmail.com.

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
        Name:        "my_tool",
        Description: "One-line description for the AI assistant",
    }, func(ctx context.Context, req *mcp.CallToolRequest, input myToolInput) (*mcp.CallToolResult, any, error) {
        // Implementation here — use deps.Cache, deps.Search, etc.
    })
}
```

2. **Register it** in `internal/tools/registry.go` — add `registerMyTool(srv, deps)` to `RegisterAll()`.

3. **Add tests** in `internal/tools/tools_test.go` or a dedicated `<toolname>_test.go`.

Key conventions:
- All tool inputs use typed structs with `jsonschema` tags (the SDK auto-generates JSON Schema from these)
- Use `deps.Cache` for caching, `deps.Metrics` for telemetry, `deps.Auditor` for audit logging
- Return errors via `&mcp.CallToolResult{IsError: true, Content: [...]}` for user-facing errors
- Update `docs/TOOLS.md` with the parameter schema

## Getting Help

- **Questions and discussions**: [GitHub Discussions](https://github.com/zoharbabin/web-researcher-mcp/discussions)
- **Bug reports**: [GitHub Issues](https://github.com/zoharbabin/web-researcher-mcp/issues)
- **Architecture questions**: See [ARCHITECTURE.md](ARCHITECTURE.md)

## Recognition

Contributors are recognized in release notes. Significant contributions may be highlighted in the README.

Thank you for helping make web-researcher-mcp better!
