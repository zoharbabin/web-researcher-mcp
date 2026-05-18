# Testing Strategy

## Philosophy

- **Table-driven tests** for all business logic (idiomatic Go)
- **Interface mocking** for external dependencies (search APIs, HTTP, browser)
- **Real HTTP servers** (`httptest`) for integration tests
- **In-memory MCP transport** for end-to-end tool testing
- **No flaky tests** — all tests deterministic, no network calls in unit tests

## Test Pyramid

```
        ┌──────────┐
        │   E2E    │  ~20 tests (real process, real STDIO/HTTP)
        ├──────────┤
        │Integration│  ~100 tests (real components, mock HTTP)
        ├──────────┤
        │   Unit   │  ~800 tests (pure functions, table-driven)
        └──────────┘
```

## Directory Structure

```
internal/
├── tools/
│   ├── search_test.go          # Unit: search handler logic
│   ├── scrape_test.go          # Unit: scraping pipeline
│   └── ...
├── search/
│   ├── brave_test.go           # Unit: response parsing
│   ├── google_test.go          # Unit: query construction
│   └── provider_test.go        # Integration: provider switching
├── scraper/
│   ├── html_test.go            # Unit: HTML extraction
│   ├── markdown_test.go        # Unit: markdown negotiation
│   ├── ssrf_test.go            # Unit: IP blocking
│   ├── pipeline_test.go        # Integration: tiered fallback
│   └── testdata/
│       ├── simple.html
│       ├── spa.html
│       ├── sample.pdf
│       └── sample.docx
├── cache/
│   ├── memory_test.go          # Unit: cache operations
│   ├── disk_test.go            # Unit: persistence
│   └── hybrid_test.go          # Integration: L1+L2
├── content/
│   ├── sanitize_test.go        # Unit: XSS, hidden text removal
│   ├── dedup_test.go           # Unit: paragraph hashing
│   ├── truncate_test.go        # Unit: breakpoint detection
│   └── quality_test.go         # Unit: scoring algorithm
└── auth/
    ├── middleware_test.go       # Unit: JWT validation
    └── jwks_test.go            # Unit: key caching

tests/
├── e2e/
│   ├── stdio_test.go           # E2E: STDIO transport
│   ├── http_test.go            # E2E: HTTP transport + OAuth
│   ├── orphan_test.go          # E2E: process lifecycle
│   ├── concurrent_test.go      # E2E: parallel sessions
│   └── watchdog_test.go        # E2E: parent death detection (Go makes this trivial)
├── integration/
│   ├── search_integration_test.go
│   ├── scrape_integration_test.go
│   └── pipeline_integration_test.go
└── benchmark/
    ├── cache_bench_test.go
    ├── scraper_bench_test.go
    └── search_bench_test.go
```

## Running Tests

```bash
# All tests
go test ./...

# With verbose output
go test -v ./...

# Specific package
go test -v ./internal/scraper/...

# With coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Race detector (catch concurrency bugs)
go test -race ./...

# Benchmarks
go test -bench=. -benchmem ./tests/benchmark/

# E2E only
go test -v ./tests/e2e/...

# Integration only (uses httptest servers, no real network)
go test -v ./tests/integration/...

# Short mode (skip slow tests)
go test -short ./...
```

## Test Patterns

### Unit Test: Table-Driven

```go
func TestIsPrivateIP(t *testing.T) {
    tests := []struct {
        name    string
        ip      string
        want    bool
    }{
        {"loopback v4", "127.0.0.1", true},
        {"loopback v6", "::1", true},
        {"private 10.x", "10.0.0.1", true},
        {"private 172.16.x", "172.16.0.1", true},
        {"private 192.168.x", "192.168.1.1", true},
        {"link-local", "169.254.169.254", true},
        {"public google", "8.8.8.8", false},
        {"public cloudflare", "1.1.1.1", false},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            ip := net.ParseIP(tt.ip)
            got := isPrivateIP(ip)
            assert.Equal(t, tt.want, got)
        })
    }
}
```

### Integration Test: Mock HTTP Server

```go
func TestBraveProvider_Web(t *testing.T) {
    // Create a fake Brave API server
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        assert.Equal(t, "/res/v1/web/search", r.URL.Path)
        assert.Equal(t, "test query", r.URL.Query().Get("q"))
        
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(braveResponse{
            Web: braveWebResults{
                Results: []braveResult{
                    {Title: "Result 1", URL: "https://example.com"},
                },
            },
        })
    }))
    defer srv.Close()

    provider := NewBraveProvider("test-key", WithBaseURL(srv.URL))
    results, err := provider.Web(context.Background(), WebSearchParams{Query: "test query"})
    
    require.NoError(t, err)
    assert.Len(t, results, 1)
    assert.Equal(t, "https://example.com", results[0].URL)
}
```

### E2E Test: Real MCP Transport

```go
func TestSearchTool_E2E(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping E2E test in short mode")
    }

    // Use in-memory transport (provided by go-sdk)
    clientTransport, serverTransport := mcp.NewInMemoryTransports()
    
    // Build server with test config
    srv := server.New(testConfig(), testDeps())
    
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    
    go srv.MCP().Run(ctx, serverTransport)
    
    client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
    session, err := client.Connect(ctx, clientTransport, nil)
    require.NoError(t, err)
    
    result, err := session.CallTool(ctx, &mcp.CallToolParams{
        Name: "web_search",
        Arguments: map[string]any{
            "query":       "golang testing",
            "num_results": 3,
        },
    })
    require.NoError(t, err)
    assert.NotEmpty(t, result.Content)
}
```

### E2E Test: Process Lifecycle (Orphan Prevention)

```go
func TestProcessExitsOnStdinClose(t *testing.T) {
    // Build the binary
    binary := buildTestBinary(t)
    
    // Start as subprocess
    cmd := exec.CommandContext(context.Background(), binary)
    cmd.Env = append(os.Environ(), "GOOGLE_CUSTOM_SEARCH_API_KEY=test", "GOOGLE_CUSTOM_SEARCH_ID=test")
    stdin, _ := cmd.StdinPipe()
    cmd.Start()
    
    // Send initialize
    stdin.Write([]byte(`{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}` + "\n"))
    
    time.Sleep(2 * time.Second)
    assert.True(t, isProcessAlive(cmd.Process.Pid))
    
    // Close stdin (simulate parent death)
    stdin.Close()
    
    // Process should exit within 5 seconds
    done := make(chan error, 1)
    go func() { done <- cmd.Wait() }()
    
    select {
    case <-done:
        // Process exited — success
    case <-time.After(5 * time.Second):
        cmd.Process.Kill()
        t.Fatal("process did not exit after stdin close")
    }
}
```

### Benchmark Test

```go
func BenchmarkCache_Set(b *testing.B) {
    c := cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 64})
    value := []byte(strings.Repeat("x", 1024)) // 1KB value
    
    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        i := 0
        for pb.Next() {
            c.Set(context.Background(), fmt.Sprintf("key-%d", i), value, 30*time.Minute)
            i++
        }
    })
}
```

## Mocking Strategy

**Interfaces for all external dependencies:**

```go
// These interfaces enable testing without network calls:

type SearchProvider interface { ... }   // Mock: return fixed results
type HTTPClient interface { ... }       // Mock: return fixed responses
type BrowserPool interface { ... }      // Mock: return rendered HTML
type CacheStore interface { ... }       // Mock: in-memory map
type JWKSFetcher interface { ... }      // Mock: return test keys
```

**testify/mock for complex mocks:**

```go
type MockSearchProvider struct {
    mock.Mock
}

func (m *MockSearchProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
    args := m.Called(ctx, params)
    return args.Get(0).([]SearchResult), args.Error(1)
}
```

## CI Integration

```yaml
# .github/workflows/ci.yml
jobs:
  test:
    strategy:
      matrix:
        go-version: ['1.23']
        os: [ubuntu-latest, macos-latest]
    steps:
    - uses: actions/setup-go@v5
      with:
        go-version: ${{ matrix.go-version }}
    - run: go test -race -coverprofile=coverage.out ./...
    - run: go test -v ./tests/e2e/...
    - uses: codecov/codecov-action@v4
      with:
        file: coverage.out
        
  benchmark:
    runs-on: ubuntu-latest
    steps:
    - run: go test -bench=. -benchmem ./tests/benchmark/ | tee bench.txt
    - uses: benchmark-action/github-action-benchmark@v1
      with:
        tool: 'go'
        output-file-path: bench.txt
```

## Coverage Target

- Unit tests: >90% line coverage on business logic
- Integration tests: cover all tool handlers with mock HTTP
- E2E tests: cover both transports, process lifecycle, concurrent sessions
- Benchmarks: track regression for cache, scraper, search operations

## Test Environment Variables

Tests use `t.Setenv()` for isolated env manipulation:

```go
func TestConfig_RequiresAPIKey(t *testing.T) {
    t.Setenv("GOOGLE_CUSTOM_SEARCH_API_KEY", "")
    _, err := config.Load()
    assert.ErrorContains(t, err, "GOOGLE_CUSTOM_SEARCH_API_KEY")
}
```

No global test state. No test ordering dependencies. All tests parallelizable (`t.Parallel()`).
