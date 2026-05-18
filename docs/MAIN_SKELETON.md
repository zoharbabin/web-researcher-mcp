# Main Entry Point — Design Pattern

This document shows the wiring pattern for `cmd/web-researcher-mcp/main.go`. It demonstrates how all components connect without implementing the internals.

## Pattern: Explicit Dependency Injection

```go
// cmd/web-researcher-mcp/main.go
package main

import (
    "context"
    "log/slog"
    "os"
    "os/signal"
    "syscall"

    "github.com/zoharbabin/web-researcher-mcp/internal/auth"
    "github.com/zoharbabin/web-researcher-mcp/internal/cache"
    "github.com/zoharbabin/web-researcher-mcp/internal/circuit"
    "github.com/zoharbabin/web-researcher-mcp/internal/config"
    "github.com/zoharbabin/web-researcher-mcp/internal/content"
    "github.com/zoharbabin/web-researcher-mcp/internal/metrics"
    "github.com/zoharbabin/web-researcher-mcp/internal/ratelimit"
    "github.com/zoharbabin/web-researcher-mcp/internal/resources"
    "github.com/zoharbabin/web-researcher-mcp/internal/scraper"
    "github.com/zoharbabin/web-researcher-mcp/internal/search"
    "github.com/zoharbabin/web-researcher-mcp/internal/server"
    "github.com/zoharbabin/web-researcher-mcp/internal/session"
    "github.com/zoharbabin/web-researcher-mcp/internal/tools"
)

var version = "dev"

func main() {
    // 1. Load configuration from environment
    cfg, err := config.Load()
    if err != nil {
        slog.Error("configuration error", "err", err)
        // Don't exit — allow MCP handshake for health checks
    }

    // 2. Setup structured logging
    logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
        Level: cfg.LogLevel,
    }))
    slog.SetDefault(logger)

    // 3. Build infrastructure layer
    cacheStore := cache.NewHybrid(cache.HybridConfig{
        Memory:     cache.MemoryConfig{MaxSizeMB: cfg.CacheMaxMemoryMB},
        Disk:       cache.DiskConfig{Dir: cfg.CacheDir, EncryptionKey: cfg.CacheEncryptionKey},
        RedisURL:   cfg.RedisURL,
    })
    defer cacheStore.Close()

    metricsCollector := metrics.NewCollector()
    rateLimiter := ratelimit.New(cfg.RateLimit)
    searchBreaker := circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60})

    // 4. Build service layer
    searchProvider := search.NewProvider(cfg.Search, search.Deps{
        HTTPClient: scraper.NewSSRFSafeClient(cfg.AllowPrivateIPs),
        Breaker:    searchBreaker,
    })

    scraperPipeline := scraper.NewPipeline(scraper.PipelineConfig{
        MaxConcurrency: cfg.MaxScrapeConcurrency,
        AllowPrivateIPs: cfg.AllowPrivateIPs,
        AllowedDomains:  cfg.AllowedDomains,
        ChromePath:      cfg.ChromePath,
    })
    defer scraperPipeline.Close()

    contentProcessor := content.NewProcessor()
    sessionManager := session.NewManager(session.Config{
        MaxSessions: 50,
        SessionTTL:  cfg.SessionTTL,
    })
    defer sessionManager.Close()

    // 5. Build tool dependencies
    toolDeps := tools.Dependencies{
        Cache:     cacheStore,
        Search:    searchProvider,
        Scraper:   scraperPipeline,
        Content:   contentProcessor,
        Sessions:  sessionManager,
        Metrics:   metricsCollector,
        Logger:    logger,
    }

    // 6. Create MCP server and register tools
    srv := server.New(server.Config{
        Name:    "web-researcher-mcp",
        Version: version,
    })

    tools.RegisterAll(srv.MCP(), toolDeps)
    resources.RegisterAll(srv.MCP(), metricsCollector, cacheStore, sessionManager)

    // 7. Setup context with signal handling
    ctx, cancel := signal.NotifyContext(context.Background(),
        syscall.SIGINT, syscall.SIGTERM)
    defer cancel()

    // 8. Start transport(s)
    if cfg.Port > 0 {
        // HTTP mode: start HTTP server with OAuth + rate limiting
        httpCfg := server.HTTPConfig{
            Port:           cfg.Port,
            Auth:           auth.NewMiddleware(cfg.OAuth),
            RateLimiter:    rateLimiter,
            AllowedOrigins: cfg.AllowedOrigins,
            Metrics:        metricsCollector,
        }
        go srv.ServeHTTP(ctx, httpCfg)
        logger.Info("HTTP transport started", "port", cfg.Port)
    }

    // STDIO transport: always active (primary for Claude Code)
    logger.Info("STDIO transport starting", "version", version)
    if err := srv.RunSTDIO(ctx); err != nil {
        // Run returns on stdin EOF, signal, or context cancellation
        // All of these are normal shutdown conditions
        if ctx.Err() == nil {
            logger.Error("server error", "err", err)
            os.Exit(1)
        }
    }

    logger.Info("shutdown complete")
}
```

## Key Design Points

### 1. No Global State

Every component is constructed explicitly and passed to consumers. No `init()` functions, no package-level `var`. This makes testing trivial — just construct with different config.

### 2. Shutdown Propagation

```
Signal (SIGINT/SIGTERM) or stdin EOF
    │
    ▼
ctx cancelled
    │
    ├─── srv.RunSTDIO returns
    ├─── srv.ServeHTTP stops accepting
    ├─── In-flight requests finish (30s deadline)
    ├─── cacheStore.Close() → flush to disk
    ├─── scraperPipeline.Close() → kill Chrome instances
    └─── sessionManager.Close() → persist active sessions
```

### 3. Error Resilience

- Config errors → log, continue (tools fail at call time)
- Cache unavailable → fall through to direct API calls
- Redis down → fall back to local memory cache
- Chrome not installed → skip browser scraping tier
- Search provider error → circuit breaker, try fallback

### 4. Stdin EOF Handling (The Orphan Problem — Solved)

The MCP SDK's `StdioTransport` reads from stdin in a blocking goroutine. When stdin closes:
1. `os.Stdin.Read()` returns `(0, io.EOF)`
2. Transport closes
3. `srv.RunSTDIO(ctx)` returns `nil`
4. `defer` functions run (cache flush, cleanup)
5. `main()` returns → process exits

If stdout is broken (parent died), writing returns EPIPE → Go delivers SIGPIPE → process exits.

**No watchdog. No polling. No worker threads.** The language runtime handles it correctly.

---

## Server Struct Pattern

```go
// internal/server/server.go
package server

import (
    "context"
    "github.com/modelcontextprotocol/go-sdk/mcp"
)

type Server struct {
    mcp *mcp.Server
}

func New(cfg Config) *Server {
    mcpServer := mcp.NewServer(
        &mcp.Implementation{Name: cfg.Name, Version: cfg.Version},
        &mcp.ServerOptions{
            Capabilities: &mcp.ServerCapabilities{
                Tools:     &mcp.ToolCapabilities{},
                Resources: &mcp.ResourceCapabilities{Subscribe: true},
                Prompts:   &mcp.PromptCapabilities{},
            },
        },
    )
    return &Server{mcp: mcpServer}
}

func (s *Server) MCP() *mcp.Server { return s.mcp }

func (s *Server) RunSTDIO(ctx context.Context) error {
    return s.mcp.Run(ctx, &mcp.StdioTransport{})
}

func (s *Server) ServeHTTP(ctx context.Context, cfg HTTPConfig) error {
    handler := mcp.NewStreamableHTTPHandler(
        func(r *http.Request) *mcp.Server { return s.mcp },
        nil, // default StreamableHTTPOptions
    )
    // Wrap with auth, rate limiting, CORS, metrics
    mux := http.NewServeMux()
    mux.Handle("/mcp", cfg.Auth.Wrap(cfg.RateLimiter.Wrap(handler)))
    mux.HandleFunc("GET /health/live", healthLive)
    mux.HandleFunc("GET /health/ready", healthReady)
    mux.Handle("GET /metrics", cfg.Metrics.HTTPHandler())

    srv := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Port), Handler: mux}
    go func() { <-ctx.Done(); srv.Shutdown(context.Background()) }()
    return srv.ListenAndServe()
}
```

---

## Tool Registration Pattern

```go
// internal/tools/registry.go
package tools

import "github.com/modelcontextprotocol/go-sdk/mcp"

type Dependencies struct {
    Cache    cache.Cache
    Search   search.Provider
    Scraper  scraper.Pipeline
    Content  content.Processor
    Sessions session.Manager
    Metrics  *metrics.Collector
    Logger   *slog.Logger
}

func RegisterAll(srv *mcp.Server, deps Dependencies) {
    // Each tool is a standalone struct with typed input/output
    searchTool := &SearchTool{search: deps.Search, cache: deps.Cache, metrics: deps.Metrics}
    scrapeTool := &ScrapeTool{scraper: deps.Scraper, cache: deps.Cache, content: deps.Content, metrics: deps.Metrics}
    // ...

    mcp.AddTool(srv, searchTool.Definition(), searchTool.Handle)
    mcp.AddTool(srv, scrapeTool.Definition(), scrapeTool.Handle)
    mcp.AddTool(srv, imageSearchTool.Definition(), imageSearchTool.Handle)
    mcp.AddTool(srv, newsSearchTool.Definition(), newsSearchTool.Handle)
    mcp.AddTool(srv, academicTool.Definition(), academicTool.Handle)
    mcp.AddTool(srv, patentTool.Definition(), patentTool.Handle)
    mcp.AddTool(srv, searchAndScrapeTool.Definition(), searchAndScrapeTool.Handle)
    mcp.AddTool(srv, sequentialTool.Definition(), sequentialTool.Handle)
}
```

---

## Testing a Tool in Isolation

```go
// internal/tools/search_test.go
func TestSearchTool_Handle(t *testing.T) {
    // Mock the search provider
    mockProvider := &MockProvider{
        WebFunc: func(ctx context.Context, p search.WebSearchParams) ([]search.SearchResult, error) {
            return []search.SearchResult{
                {Title: "Go tutorial", URL: "https://go.dev/tour"},
            }, nil
        },
    }
    
    // Mock the cache (always miss)
    mockCache := cache.NewNoop()
    
    tool := &SearchTool{
        search:  mockProvider,
        cache:   mockCache,
        metrics: metrics.NewCollector(),
    }
    
    _, output, err := tool.Handle(context.Background(), nil, SearchInput{
        Query:      "golang tutorial",
        NumResults: 5,
    })
    
    require.NoError(t, err)
    assert.Equal(t, []string{"https://go.dev/tour"}, output.URLs)
    assert.Equal(t, 1, output.ResultCount)
}
```

This pattern enables fast, deterministic unit tests with no network, no disk, and no MCP protocol overhead.
