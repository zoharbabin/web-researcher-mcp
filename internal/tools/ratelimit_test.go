package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// rateLimitProvider always returns a rate limit error.
type rateLimitProvider struct{}

func (p *rateLimitProvider) Web(_ context.Context, _ search.WebSearchParams) ([]search.SearchResult, error) {
	return nil, fmt.Errorf("google API rate limited")
}

func (p *rateLimitProvider) Images(_ context.Context, _ search.ImageSearchParams) ([]search.ImageResult, error) {
	return nil, fmt.Errorf("brave API rate limited")
}

func (p *rateLimitProvider) News(_ context.Context, _ search.NewsSearchParams) ([]search.NewsResult, error) {
	return nil, fmt.Errorf("searchapi: rate limited")
}

func (p *rateLimitProvider) Name() string { return "rate-limited-mock" }

// genericErrorProvider returns a non-rate-limit error.
type genericErrorProvider struct{}

func (p *genericErrorProvider) Web(_ context.Context, _ search.WebSearchParams) ([]search.SearchResult, error) {
	return nil, fmt.Errorf("connection timeout")
}

func (p *genericErrorProvider) Images(_ context.Context, _ search.ImageSearchParams) ([]search.ImageResult, error) {
	return nil, fmt.Errorf("connection timeout")
}

func (p *genericErrorProvider) News(_ context.Context, _ search.NewsSearchParams) ([]search.NewsResult, error) {
	return nil, fmt.Errorf("connection timeout")
}

func (p *genericErrorProvider) Name() string { return "error-mock" }

// =============================================================================
// isRateLimitError detection
// =============================================================================

func TestIsRateLimitError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		err    error
		expect bool
	}{
		{"nil error", nil, false},
		{"google rate limited", fmt.Errorf("google API rate limited"), true},
		{"brave rate limited", fmt.Errorf("brave API rate limited"), true},
		{"searchapi rate limited", fmt.Errorf("searchapi: rate limited"), true},
		{"http 429 in message", fmt.Errorf("HTTP 429: too many requests"), true},
		{"quota exceeded", fmt.Errorf("daily quota exceeded"), true},
		{"generic timeout", fmt.Errorf("connection timeout"), false},
		{"dns error", fmt.Errorf("no such host"), false},
		{"empty error", fmt.Errorf(""), false},
		{"wrapped rate limit", fmt.Errorf("search for query: %w", fmt.Errorf("google API rate limited")), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isRateLimitError(tc.err)
			if got != tc.expect {
				t.Errorf("isRateLimitError(%v) = %v, want %v", tc.err, got, tc.expect)
			}
		})
	}
}

// =============================================================================
// rateLimitError response format
// =============================================================================

func TestRateLimitErrorFormat(t *testing.T) {
	t.Parallel()
	result := rateLimitError(fmt.Errorf("google API rate limited"))

	if !result.IsError {
		t.Fatal("expected IsError=true")
	}

	text := result.Content[0].(*mcp.TextContent).Text

	if !strings.HasPrefix(text, "The search service is temporarily busy") {
		t.Errorf("error should start with 'The search service is temporarily busy', got: %s", text)
	}

	if !strings.Contains(text, "60 seconds") {
		t.Error("error should include retry timing")
	}

	if !strings.Contains(text, "provider") {
		t.Error("error should mention switching providers")
	}

	if result.StructuredContent != nil {
		t.Error("rate limit errors should NOT have structured content (isError responses never do)")
	}
}

// =============================================================================
// Tool-level rate limit error propagation
// =============================================================================

func TestWebSearchRateLimitError(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &rateLimitProvider{}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "test"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if !res.IsError {
		t.Fatal("expected IsError=true for rate-limited provider")
	}

	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.HasPrefix(text, "The search service is temporarily busy") {
		t.Errorf("expected rate limit error format, got: %s", text)
	}
}

func TestImageSearchRateLimitError(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &rateLimitProvider{}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "image_search",
		Arguments: map[string]any{"query": "test"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if !res.IsError {
		t.Fatal("expected IsError=true for rate-limited provider")
	}

	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.HasPrefix(text, "The search service is temporarily busy") {
		t.Errorf("expected rate limit error format, got: %s", text)
	}
}

func TestNewsSearchRateLimitError(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &rateLimitProvider{}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "news_search",
		Arguments: map[string]any{"query": "test"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if !res.IsError {
		t.Fatal("expected IsError=true for rate-limited provider")
	}

	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.HasPrefix(text, "The search service is temporarily busy") {
		t.Errorf("expected rate limit error format, got: %s", text)
	}
}

func TestAcademicSearchRateLimitError(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &rateLimitProvider{}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "academic_search",
		Arguments: map[string]any{"query": "test"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if !res.IsError {
		t.Fatal("expected IsError=true for rate-limited provider")
	}

	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.HasPrefix(text, "The search service is temporarily busy") {
		t.Errorf("expected rate limit error format, got: %s", text)
	}
}

func TestPatentSearchRateLimitError(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &rateLimitProvider{}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "patent_search",
		Arguments: map[string]any{"query": "test"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if !res.IsError {
		t.Fatal("expected IsError=true for rate-limited provider")
	}

	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.HasPrefix(text, "The search service is temporarily busy") {
		t.Errorf("expected rate limit error format, got: %s", text)
	}
}

func TestSearchAndScrapeRateLimitError(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &rateLimitProvider{}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_and_scrape",
		Arguments: map[string]any{"query": "test"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if !res.IsError {
		t.Fatal("expected IsError=true for rate-limited provider")
	}

	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.HasPrefix(text, "The search service is temporarily busy") {
		t.Errorf("expected rate limit error format, got: %s", text)
	}
}

// =============================================================================
// Non-rate-limit errors should NOT use rate limit format
// =============================================================================

func TestWebSearchGenericErrorNotRateLimit(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &genericErrorProvider{}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "test"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if !res.IsError {
		t.Fatal("expected IsError=true")
	}

	text := res.Content[0].(*mcp.TextContent).Text
	if strings.HasPrefix(text, "Rate limited") {
		t.Errorf("generic errors should NOT use rate limit format, got: %s", text)
	}
	if !strings.Contains(text, "search failed") {
		t.Errorf("expected generic error format, got: %s", text)
	}
}

// =============================================================================
// Cache hit bypasses rate limit concern
// =============================================================================

func TestCacheHitBypassesUpstreamCall(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Cache = cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 1})
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	// First call populates cache
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "cached query"},
	})
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if res.IsError {
		t.Fatal("first call should succeed")
	}

	// Second identical call should hit cache (no upstream API call)
	res2, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "cached query"},
	})
	if err != nil {
		t.Fatalf("cached call failed: %v", err)
	}
	if res2.IsError {
		t.Fatal("cache hit should succeed without calling upstream")
	}

	// Verify cache hit was recorded in metrics
	stats := deps.Metrics.GetToolStats()
	webStats := stats["web_search"]
	if webStats.CacheHits != 1 {
		t.Fatalf("expected 1 cache hit, got %d", webStats.CacheHits)
	}
}

// =============================================================================
// Metrics tracking for rate limit errors
// =============================================================================

func TestRateLimitMetricsTracking(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &rateLimitProvider{}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	_, _ = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "test"},
	})

	stats := deps.Metrics.GetToolStats()
	webStats, ok := stats["web_search"]
	if !ok {
		t.Fatal("expected web_search stats")
	}

	if webStats.TotalCalls != 1 {
		t.Fatalf("expected 1 total call, got %d", webStats.TotalCalls)
	}
	if webStats.ErrorCalls != 1 {
		t.Fatalf("expected 1 error call, got %d", webStats.ErrorCalls)
	}
}
