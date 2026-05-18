package tools

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

type mockProvider struct{}

func (m *mockProvider) Web(_ context.Context, params search.WebSearchParams) ([]search.SearchResult, error) {
	return []search.SearchResult{
		{Title: "Test Result", URL: "https://example.com", Snippet: "A test snippet"},
	}, nil
}

func (m *mockProvider) Images(_ context.Context, params search.ImageSearchParams) ([]search.ImageResult, error) {
	return []search.ImageResult{
		{Title: "Test Image", Link: "https://example.com/img.png", DisplayLink: "example.com"},
	}, nil
}

func (m *mockProvider) News(_ context.Context, params search.NewsSearchParams) ([]search.NewsResult, error) {
	return []search.NewsResult{
		{Title: "Test News", URL: "https://news.example.com/story", Source: "Example News", Snippet: "News snippet"},
	}, nil
}

func (m *mockProvider) Name() string { return "mock" }

// mockProviderWithURL returns search results pointing to a specified URL.
type mockProviderWithURL struct {
	url string
}

func (m *mockProviderWithURL) Web(_ context.Context, params search.WebSearchParams) ([]search.SearchResult, error) {
	return []search.SearchResult{
		{Title: "Test Result", URL: m.url, Snippet: "A test snippet"},
	}, nil
}

func (m *mockProviderWithURL) Images(_ context.Context, params search.ImageSearchParams) ([]search.ImageResult, error) {
	return []search.ImageResult{}, nil
}

func (m *mockProviderWithURL) News(_ context.Context, params search.NewsSearchParams) ([]search.NewsResult, error) {
	return []search.NewsResult{}, nil
}

func (m *mockProviderWithURL) Name() string { return "mock-with-url" }

func setupTestDeps() Dependencies {
	return Dependencies{
		Cache:    cache.NewNoop(),
		Search:   &mockProvider{},
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2}),
		Content:  content.NewProcessor(),
		Sessions: session.NewManager(session.Config{MaxSessions: 100}),
		Metrics:  metrics.NewCollector(),
		Auditor:  audit.NewNoop(),
		Logger:   slog.Default(),
	}
}

func TestRegisterAllDoesNotPanic(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	RegisterAll(srv, deps)
}

func TestWebSearchTool(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	RegisterAll(srv, deps)

	result := callTool(t, srv, "web_search", map[string]any{"query": "golang testing"})

	var output map[string]any
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if output["query"] != "golang testing" {
		t.Fatalf("expected query 'golang testing', got %v", output["query"])
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result, got %v", output["resultCount"])
	}
}

func TestWebSearchEmptyQuery(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	RegisterAll(srv, deps)

	result, isError := callToolRaw(t, srv, "web_search", map[string]any{"query": ""})
	if !isError {
		t.Fatal("expected error for empty query")
	}
	if result != "query is required" {
		t.Fatalf("expected 'query is required', got %q", result)
	}
}

func TestWebSearchLongQuery(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	RegisterAll(srv, deps)

	longQuery := ""
	for i := 0; i < 501; i++ {
		longQuery += "x"
	}
	_, isError := callToolRaw(t, srv, "web_search", map[string]any{"query": longQuery})
	if !isError {
		t.Fatal("expected error for query exceeding 500 chars")
	}
}

func TestImageSearchTool(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	RegisterAll(srv, deps)

	result := callTool(t, srv, "image_search", map[string]any{"query": "cats"})

	var output map[string]any
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result, got %v", output["resultCount"])
	}
}

func TestNewsSearchTool(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	RegisterAll(srv, deps)

	result := callTool(t, srv, "news_search", map[string]any{"query": "technology"})

	var output map[string]any
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result, got %v", output["resultCount"])
	}
}

func TestAcademicSearchTool(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	RegisterAll(srv, deps)

	result := callTool(t, srv, "academic_search", map[string]any{"query": "machine learning"})

	var output map[string]any
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["query"] != "machine learning" {
		t.Fatalf("expected query 'machine learning', got %v", output["query"])
	}
}

func TestSequentialSearchTool(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	RegisterAll(srv, deps)

	// Step 1: Create session
	result := callTool(t, srv, "sequential_search", map[string]any{
		"searchStep":     "Initial search for topic X",
		"stepNumber":     float64(1),
		"nextStepNeeded": true,
	})

	var output map[string]any
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	sessionID, ok := output["sessionId"].(string)
	if !ok || sessionID == "" {
		t.Fatal("expected sessionId in response")
	}

	// Step 2: Continue session
	result = callTool(t, srv, "sequential_search", map[string]any{
		"searchStep":     "Found relevant paper on topic X",
		"stepNumber":     float64(2),
		"nextStepNeeded": false,
		"sessionId":      sessionID,
	})

	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("failed to parse step 2 output: %v", err)
	}
	if output["isComplete"] != true {
		t.Fatal("expected isComplete=true on final step")
	}
}

func TestPatentSearchTool(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	RegisterAll(srv, deps)

	result := callTool(t, srv, "patent_search", map[string]any{
		"query":       "neural network acceleration",
		"search_type": "prior_art",
	})

	var output map[string]any
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if output["query"] != "neural network acceleration" {
		t.Fatalf("expected query 'neural network acceleration', got %v", output["query"])
	}
	if output["searchType"] != "prior_art" {
		t.Fatalf("expected searchType 'prior_art', got %v", output["searchType"])
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result, got %v", output["resultCount"])
	}
}

func TestPatentSearchEmptyQuery(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	RegisterAll(srv, deps)

	_, isError := callToolRaw(t, srv, "patent_search", map[string]any{"query": ""})
	if !isError {
		t.Fatal("expected error for empty query")
	}
}

func TestPatentSearchWithFilters(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	deps.Search = &mockProviderWithURL{url: "https://patents.google.com/patent/US20200012345A1/en"}
	RegisterAll(srv, deps)

	result := callTool(t, srv, "patent_search", map[string]any{
		"query":         "machine learning",
		"assignee":      "Google Inc",
		"patent_office": "US",
		"year_from":     float64(2020),
		"year_to":       float64(2024),
	})

	var output map[string]any
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result, got %v", output["resultCount"])
	}
}

func TestScrapePageTool(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html><head><title>Test Page</title><meta property="og:title" content="Test Title"/></head>
<body><article>
<h1>Main Heading</h1>
<p>This is test content from the httptest server. It contains enough text to be extracted properly by the scraping pipeline and should pass the minimum content length threshold of 100 characters for successful extraction.</p>
<p>Additional paragraph with more relevant information for the scraper to pick up during testing.</p>
</article></body></html>`))
	}))
	defer ts.Close()

	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})
	RegisterAll(srv, deps)

	result := callTool(t, srv, "scrape_page", map[string]any{"url": ts.URL})

	var output map[string]any
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if output["url"] != ts.URL {
		t.Fatalf("expected url %q, got %v", ts.URL, output["url"])
	}
	if output["contentType"] != "html" {
		t.Fatalf("expected contentType 'html', got %v", output["contentType"])
	}
	contentStr, _ := output["content"].(string)
	if !strings.Contains(contentStr, "Main Heading") {
		t.Fatal("expected content to contain 'Main Heading'")
	}
}

func TestScrapePageEmptyURL(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	RegisterAll(srv, deps)

	_, isError := callToolRaw(t, srv, "scrape_page", map[string]any{"url": ""})
	if !isError {
		t.Fatal("expected error for empty url")
	}
}

func TestScrapePageHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})
	RegisterAll(srv, deps)

	_, isError := callToolRaw(t, srv, "scrape_page", map[string]any{"url": ts.URL})
	if !isError {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestSearchAndScrapeTool(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html><head><title>Scraped Page</title></head>
<body><article>
<h1>Search Result Content</h1>
<p>This page was found by search and then scraped. It has enough content to pass the minimum threshold for content extraction and should be included in the combined output of search_and_scrape.</p>
<p>Second paragraph with additional detail about the topic being researched via the combined pipeline.</p>
</article></body></html>`))
	}))
	defer ts.Close()

	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	deps.Search = &mockProviderWithURL{url: ts.URL}
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})
	RegisterAll(srv, deps)

	result := callTool(t, srv, "search_and_scrape", map[string]any{"query": "test topic"})

	var output map[string]any
	if err := json.Unmarshal([]byte(result), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if output["query"] != "test topic" {
		t.Fatalf("expected query 'test topic', got %v", output["query"])
	}

	combined, _ := output["combinedContent"].(string)
	if combined == "" {
		t.Fatal("expected non-empty combinedContent")
	}
	if !strings.Contains(combined, "Search Result Content") {
		t.Fatal("expected combinedContent to include scraped content")
	}
}

func TestSearchAndScrapeEmptyQuery(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	RegisterAll(srv, deps)

	_, isError := callToolRaw(t, srv, "search_and_scrape", map[string]any{"query": ""})
	if !isError {
		t.Fatal("expected error for empty query")
	}
}

func TestWebSearchCaching(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	metricsCollector := metrics.NewCollector()
	deps := Dependencies{
		Cache:    cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 1}),
		Search:   &mockProvider{},
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2}),
		Content:  content.NewProcessor(),
		Sessions: session.NewManager(session.Config{MaxSessions: 100}),
		Metrics:  metricsCollector,
		Auditor:  audit.NewNoop(),
		Logger:   slog.Default(),
	}
	RegisterAll(srv, deps)

	// First call: should not be from cache
	_ = callTool(t, srv, "web_search", map[string]any{"query": "cache test"})

	stats := metricsCollector.GetToolStats()
	s := stats["web_search"]
	if s.CacheHits != 0 {
		t.Fatalf("expected 0 cache hits after first call, got %d", s.CacheHits)
	}

	// Second call with same query: should hit cache
	_ = callTool(t, srv, "web_search", map[string]any{"query": "cache test"})

	stats = metricsCollector.GetToolStats()
	s = stats["web_search"]
	if s.CacheHits != 1 {
		t.Fatalf("expected 1 cache hit after second call, got %d", s.CacheHits)
	}
}

func TestImageSearchEmptyQuery(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	RegisterAll(srv, deps)

	_, isError := callToolRaw(t, srv, "image_search", map[string]any{"query": ""})
	if !isError {
		t.Fatal("expected error for empty query")
	}
}

func TestNewsSearchEmptyQuery(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	RegisterAll(srv, deps)

	_, isError := callToolRaw(t, srv, "news_search", map[string]any{"query": ""})
	if !isError {
		t.Fatal("expected error for empty query")
	}
}

func TestAcademicSearchEmptyQuery(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	RegisterAll(srv, deps)

	_, isError := callToolRaw(t, srv, "academic_search", map[string]any{"query": ""})
	if !isError {
		t.Fatal("expected error for empty query")
	}
}

func TestSequentialSearchEmptyStep(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	deps := setupTestDeps()
	RegisterAll(srv, deps)

	_, isError := callToolRaw(t, srv, "sequential_search", map[string]any{
		"searchStep":     "",
		"stepNumber":     float64(1),
		"nextStepNeeded": true,
	})
	if !isError {
		t.Fatal("expected error for empty searchStep")
	}
}

func TestToolError(t *testing.T) {
	result := toolError("something went wrong")
	if !result.IsError {
		t.Fatal("expected IsError=true")
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(result.Content))
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatal("expected TextContent")
	}
	if tc.Text != "something went wrong" {
		t.Fatalf("expected error text, got %q", tc.Text)
	}
}

func TestIntParam(t *testing.T) {
	args := map[string]any{
		"num_results": float64(7),
		"zero":        float64(0),
	}

	if got := intParam(args, "num_results", 5); got != 7 {
		t.Fatalf("expected 7, got %d", got)
	}
	if got := intParam(args, "missing", 10); got != 10 {
		t.Fatalf("expected fallback 10, got %d", got)
	}
	if got := intParam(args, "zero", 5); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

func TestBoolParam(t *testing.T) {
	args := map[string]any{
		"enabled":  true,
		"disabled": false,
	}

	if got := boolParam(args, "enabled", false); !got {
		t.Fatal("expected true")
	}
	if got := boolParam(args, "disabled", true); got {
		t.Fatal("expected false")
	}
	if got := boolParam(args, "missing", true); !got {
		t.Fatal("expected fallback true")
	}
}

// callTool invokes a tool on the MCP server and returns the text result.
func callTool(t *testing.T, srv *server.MCPServer, name string, args map[string]any) string {
	t.Helper()
	text, isError := callToolRaw(t, srv, name, args)
	if isError {
		t.Fatalf("tool %s returned error: %s", name, text)
	}
	return text
}

// callToolRaw invokes a tool and returns (text, isError).
func callToolRaw(t *testing.T, srv *server.MCPServer, name string, args map[string]any) (string, bool) {
	t.Helper()

	result := srv.HandleMessage(context.Background(), mustMarshalToolCall(name, args))

	resp, ok := result.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("expected JSONRPCResponse, got %T", result)
	}

	// Parse the result as raw JSON to avoid interface unmarshaling issues
	resultBytes, _ := json.Marshal(resp.Result)
	var raw struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resultBytes, &raw); err != nil {
		t.Fatalf("failed to unmarshal tool result: %v", err)
	}

	if len(raw.Content) == 0 {
		return "", raw.IsError
	}

	return raw.Content[0].Text, raw.IsError
}

func mustMarshalToolCall(name string, args map[string]any) json.RawMessage {
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}
	b, _ := json.Marshal(msg)
	return b
}
