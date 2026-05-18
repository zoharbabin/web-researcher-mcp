# Contributing Guide

## Getting Started

### Prerequisites

- Go 1.23+ (uses `net/http` ServeMux patterns, `log/slog`)
- Chrome/Chromium (for headless scraping — optional for development)
- Redis (optional, only for multi-instance testing)

### Setup

```bash
git clone https://github.com/zoharbabin/web-researcher-mcp.git
cd web-researcher-mcp

# Download dependencies
go mod download

# Run tests (no API keys needed for unit/integration tests)
go test ./...

# Build
go build -o web-researcher-mcp ./cmd/web-researcher-mcp

# Run with minimal config
GOOGLE_CUSTOM_SEARCH_API_KEY=your-key \
GOOGLE_CUSTOM_SEARCH_ID=your-cx \
./web-researcher-mcp
```

### Development Workflow

```bash
# Run with hot reload (use air or similar)
go install github.com/air-verse/air@latest
air

# Format
gofmt -w .

# Lint
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
golangci-lint run

# Vet
go vet ./...

# Vulnerability check
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...
```

---

## Code Style

### General Rules

1. **Follow [Effective Go](https://go.dev/doc/effective_go)** — the official style guide.
2. **No comments that repeat the code.** Only comment WHY, never WHAT.
3. **Error messages are lowercase, no punctuation** — `fmt.Errorf("invalid query: %w", err)`.
4. **Exported names have doc comments.** Unexported names don't need them.
5. **One package per concern.** No "utils" or "helpers" packages.
6. **Accept interfaces, return structs** — for testability and clarity.
7. **Context is always the first parameter** — `func DoThing(ctx context.Context, ...)`.

### Package Organization

```
internal/         # Private to this module
├── tools/        # MCP tool handlers (one file per tool)
├── search/       # Search provider interface + adapters
├── scraper/      # Scraping pipeline
├── cache/        # Caching layer
├── auth/         # OAuth middleware
├── session/      # Session management
├── content/      # Content processing (sanitize, dedup, truncate)
├── metrics/      # Observability
├── ratelimit/    # Rate limiting
├── circuit/      # Circuit breaker
└── resources/    # MCP resources + prompts
```

**Why `internal/`?** Prevents external packages from importing our implementation details. Only `cmd/web-researcher-mcp/main.go` and tests can import these.

### Error Handling

```go
// DO: wrap with context
return fmt.Errorf("brave search failed for %q: %w", query, err)

// DO: use sentinel errors for expected conditions
var ErrRateLimited = errors.New("rate limited")
var ErrSSRFBlocked = errors.New("SSRF: private IP blocked")

// DO: use custom error types for rich errors
type ToolError struct {
    Tool    string
    Code    string // "rate_limited", "timeout", "invalid_input"
    Message string
    Err     error
}

// DON'T: panic
// DON'T: ignore errors
// DON'T: log AND return (pick one)
```

### Concurrency

```go
// DO: use errgroup for parallel operations
g, ctx := errgroup.WithContext(ctx)
for _, url := range urls {
    url := url
    g.Go(func() error {
        return scrape(ctx, url)
    })
}
if err := g.Wait(); err != nil { ... }

// DO: use semaphore for bounded concurrency
sem := make(chan struct{}, maxConcurrency)
sem <- struct{}{}        // acquire
defer func() { <-sem }() // release

// DON'T: launch unbounded goroutines
// DON'T: use sync.Mutex where a channel suffices
// DON'T: share memory between goroutines without synchronization
```

### Testing

```go
// DO: table-driven tests
func TestThing(t *testing.T) {
    tests := []struct {
        name string
        // ...
    }{ /* cases */ }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            // ...
        })
    }
}

// DO: use testify/assert for readability
assert.Equal(t, expected, actual)
require.NoError(t, err)

// DO: use httptest for HTTP testing
srv := httptest.NewServer(handler)
defer srv.Close()

// DON'T: depend on test execution order
// DON'T: use global state in tests
// DON'T: skip error checks in tests
```

---

## Adding a New Tool

1. **Create the handler** in `internal/tools/`:

```go
// internal/tools/mytool.go
package tools

type MyToolInput struct {
    Query string `json:"query" jsonschema:"description=The search query,required"`
}

type MyToolOutput struct {
    Results []string `json:"results"`
}

type MyTool struct {
    deps MyToolDeps
}

func (t *MyTool) Definition() *mcp.Tool {
    return &mcp.Tool{
        Name:        "my_tool",
        Description: "Does the thing",
    }
}

func (t *MyTool) Handle(ctx context.Context, req *mcp.CallToolRequest, input MyToolInput) (*mcp.CallToolResult, MyToolOutput, error) {
    // Implementation
}
```

2. **Register it** in `internal/tools/registry.go`:

```go
func RegisterAll(srv *mcp.Server, deps Dependencies) {
    // ... existing tools ...
    mcp.AddTool(srv, myTool.Definition(), myTool.Handle)
}
```

3. **Add tests** in `internal/tools/mytool_test.go`

4. **Update documentation** in `docs/TOOLS.md`

---

## Adding a New Search Provider

1. Implement the `search.Provider` interface in `internal/search/`:

```go
// internal/search/myprovider.go
type MyProvider struct {
    apiKey     string
    httpClient *http.Client
}

func (p *MyProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) { ... }
func (p *MyProvider) Images(ctx context.Context, params ImageSearchParams) ([]ImageResult, error) { ... }
func (p *MyProvider) News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) { ... }
func (p *MyProvider) Name() string { return "myprovider" }
```

2. Register in the provider factory (`internal/search/factory.go`)
3. Add env var to config
4. Add tests with `httptest` mock
5. Document in `docs/SEARCH_PROVIDERS.md`

---

## Adding a New Search Lens

1. Create `lenses/mylens.json`:

```json
{
  "name": "mylens",
  "description": "Description of what this lens covers",
  "domains": [
    "example.com",
    "*.example.org",
    "docs.example.dev"
  ],
  "cx": ""
}
```

2. That's it. The server auto-discovers lens files at startup.

### Lens Guidelines
- Max 50 domains per lens (for `site:` query injection)
- Use wildcards sparingly (`*.github.com` counts as 1 domain)
- Focus on authoritative, high-quality sources
- Include a clear description

---

## Pull Request Process

1. **Branch** from `main`: `feat/my-feature` or `fix/the-bug`
2. **Tests pass**: `go test -race ./...`
3. **Lint clean**: `golangci-lint run`
4. **No vulnerabilities**: `govulncheck ./...`
5. **Benchmarks** (if touching hot paths): include before/after
6. **Documentation** updated if behavior changes

### Commit Messages

```
feat: add Brave Search provider
fix: SSRF bypass via IPv6-mapped IPv4 address
docs: update deployment guide for Redis configuration
test: add benchmark for cache operations
refactor: extract content pipeline into separate package
```

---

## Release Process

```bash
# Tag
git tag v1.0.0
git push origin v1.0.0

# GoReleaser handles the rest:
# - Cross-compile for linux/darwin/windows (amd64, arm64)
# - Create GitHub release with binaries
# - Push Docker image
# - Update homebrew tap (if configured)
goreleaser release
```
