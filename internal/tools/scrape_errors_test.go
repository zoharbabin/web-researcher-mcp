package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

func TestScrapeErrorResponse_BrowserError(t *testing.T) {
	t.Parallel()
	err := &scraper.ScrapeError{
		Kind:    scraper.ErrBrowser,
		Message: "chrome launch failed: exec: chromium: not found",
		URL:     "https://x.com/user/status/123",
		Tier:    "browser",
	}
	result := scrapeErrorResponse(err, "https://x.com/user/status/123")
	text := result.Content[0].(*mcp.TextContent).Text

	if !result.IsError {
		t.Fatal("expected IsError=true")
	}
	if !strings.Contains(text, "Chrome is not available") {
		t.Errorf("expected Chrome guidance, got: %s", text)
	}
	if !strings.Contains(text, "CHROME_PATH") {
		t.Errorf("expected CHROME_PATH mention, got: %s", text)
	}
	if !strings.Contains(text, issueURL) {
		t.Errorf("expected GitHub issue URL, got: %s", text)
	}
}

func TestScrapeErrorResponse_BlockedError(t *testing.T) {
	t.Parallel()
	err := &scraper.ScrapeError{
		Kind:    scraper.ErrBlocked,
		Message: "access blocked: HTTP 403",
		URL:     "https://example.com/page",
		Tier:    "stealth",
	}
	result := scrapeErrorResponse(err, "https://example.com/page")
	text := result.Content[0].(*mcp.TextContent).Text

	if !result.IsError {
		t.Fatal("expected IsError=true")
	}
	if !strings.Contains(text, "bot detection") {
		t.Errorf("expected bot detection hint, got: %s", text)
	}
	if !strings.Contains(text, issueURL) {
		t.Errorf("expected GitHub issue URL for blocked errors, got: %s", text)
	}
}

func TestScrapeErrorResponse_ContentError(t *testing.T) {
	t.Parallel()
	err := &scraper.ScrapeError{
		Kind:    scraper.ErrContent,
		Message: "no content extracted from https://spa.example.com (markdown: empty, stealth: 20 bytes, html: 15 bytes)",
		URL:     "https://spa.example.com",
	}
	result := scrapeErrorResponse(err, "https://spa.example.com")
	text := result.Content[0].(*mcp.TextContent).Text

	if !result.IsError {
		t.Fatal("expected IsError=true")
	}
	if !strings.Contains(text, "no readable content") && !strings.Contains(text, "no content extracted") {
		t.Errorf("expected content extraction hint, got: %s", text)
	}
	if !strings.Contains(text, issueURL) {
		t.Errorf("expected GitHub issue URL for content errors, got: %s", text)
	}
}

func TestScrapeErrorResponse_AuthError(t *testing.T) {
	t.Parallel()
	err := &scraper.ScrapeError{
		Kind:    scraper.ErrAuth,
		Message: "HTTP 401: authentication required",
		URL:     "https://private.example.com",
		Tier:    "html",
	}
	result := scrapeErrorResponse(err, "https://private.example.com")
	text := result.Content[0].(*mcp.TextContent).Text

	if !result.IsError {
		t.Fatal("expected IsError=true")
	}
	if !strings.Contains(text, "login wall") {
		t.Errorf("expected login wall message, got: %s", text)
	}
	if strings.Contains(text, issueURL) {
		t.Errorf("auth errors should NOT include issue URL, got: %s", text)
	}
}

func TestScrapeErrorResponse_RateLimitError(t *testing.T) {
	t.Parallel()
	err := &scraper.ScrapeError{
		Kind:    scraper.ErrRateLimit,
		Message: "HTTP 429: rate limited",
		URL:     "https://example.com",
		Tier:    "stealth",
	}
	result := scrapeErrorResponse(err, "https://example.com")
	text := result.Content[0].(*mcp.TextContent).Text

	if !result.IsError {
		t.Fatal("expected IsError=true")
	}
	if !strings.Contains(text, "rate limited") {
		t.Errorf("expected rate limit message, got: %s", text)
	}
	if !strings.Contains(text, "60 seconds") {
		t.Errorf("expected retry guidance, got: %s", text)
	}
	if strings.Contains(text, issueURL) {
		t.Errorf("rate limit errors should NOT include issue URL, got: %s", text)
	}
}

func TestScrapeErrorResponse_NetworkError(t *testing.T) {
	t.Parallel()
	err := &scraper.ScrapeError{
		Kind:    scraper.ErrNetwork,
		Message: "network error: dial tcp: lookup invalid.example: no such host",
		URL:     "https://invalid.example",
		Tier:    "html",
	}
	result := scrapeErrorResponse(err, "https://invalid.example")
	text := result.Content[0].(*mcp.TextContent).Text

	if !result.IsError {
		t.Fatal("expected IsError=true")
	}
	if !strings.Contains(text, "Check connectivity") {
		t.Errorf("expected connectivity hint, got: %s", text)
	}
	if strings.Contains(text, issueURL) {
		t.Errorf("network errors should NOT include issue URL, got: %s", text)
	}
}

func TestScrapeErrorResponse_NonScrapeError(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("some random error")
	result := scrapeErrorResponse(err, "https://example.com")
	text := result.Content[0].(*mcp.TextContent).Text

	if !result.IsError {
		t.Fatal("expected IsError=true")
	}
	if !strings.Contains(text, "scrape failed: some random error") {
		t.Errorf("expected generic fallback message, got: %s", text)
	}
}

// Integration test: call scrape_page tool via MCP against a 403 server
func TestScrapeTool_403_ReturnsBlockedError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Access Denied"))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := Dependencies{
		Cache:    cache.NewNoop(),
		Search:   &mockProvider{},
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true}),
		Content:  content.NewProcessor(),
		Sessions: func() *session.Manager { m, _ := session.NewManager(session.Config{MaxSessions: 100}); return m }(),
		Metrics:  metrics.NewCollector(),
		Auditor:  audit.NewNoop(),
		Logger:   slog.Default(),
	}
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": ts.URL},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error response for 403 site")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "bot detection") {
		t.Errorf("expected blocked/bot-detection message, got: %s", text)
	}
	if !strings.Contains(text, issueURL) {
		t.Errorf("expected issue URL in blocked error, got: %s", text)
	}
}

// Integration test: call scrape_page tool via MCP against a 401 server
func TestScrapeTool_401_ReturnsAuthError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("Unauthorized"))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := Dependencies{
		Cache:    cache.NewNoop(),
		Search:   &mockProvider{},
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true}),
		Content:  content.NewProcessor(),
		Sessions: func() *session.Manager { m, _ := session.NewManager(session.Config{MaxSessions: 100}); return m }(),
		Metrics:  metrics.NewCollector(),
		Auditor:  audit.NewNoop(),
		Logger:   slog.Default(),
	}
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": ts.URL},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error response for 401 site")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "login wall") {
		t.Errorf("expected auth/login message, got: %s", text)
	}
}

// Integration test: call scrape_page tool via MCP against a 429 server
func TestScrapeTool_429_ReturnsRateLimitError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("Rate Limited"))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := Dependencies{
		Cache:    cache.NewNoop(),
		Search:   &mockProvider{},
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true}),
		Content:  content.NewProcessor(),
		Sessions: func() *session.Manager { m, _ := session.NewManager(session.Config{MaxSessions: 100}); return m }(),
		Metrics:  metrics.NewCollector(),
		Auditor:  audit.NewNoop(),
		Logger:   slog.Default(),
	}
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": ts.URL},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error response for 429 site")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "rate limited") {
		t.Errorf("expected rate limit message, got: %s", text)
	}
	if !strings.Contains(text, "60 seconds") {
		t.Errorf("expected retry hint, got: %s", text)
	}
}

// Integration test: page loads but returns thin content (no useful text)
func TestScrapeTool_ThinContent_ReturnsContentError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><script>app.init()</script></body></html>`))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := Dependencies{
		Cache:    cache.NewNoop(),
		Search:   &mockProvider{},
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true}),
		Content:  content.NewProcessor(),
		Sessions: func() *session.Manager { m, _ := session.NewManager(session.Config{MaxSessions: 100}); return m }(),
		Metrics:  metrics.NewCollector(),
		Auditor:  audit.NewNoop(),
		Logger:   slog.Default(),
	}
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": ts.URL},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error response for thin-content page")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, issueURL) {
		t.Errorf("expected issue link in content error, got: %s", text)
	}
	if !strings.Contains(text, "no content extracted") && !strings.Contains(text, "no readable content") {
		t.Errorf("expected content error message, got: %s", text)
	}
}

// Regression test: successful scrape still works end-to-end
func TestScrapeTool_Success_NotAffected(t *testing.T) {
	articleContent := strings.Repeat("This is a substantial article paragraph with useful information. ", 20)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>Test Article</title></head><body><article>%s</article></body></html>`, articleContent)
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := Dependencies{
		Cache:    cache.NewNoop(),
		Search:   &mockProvider{},
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true}),
		Content:  content.NewProcessor(),
		Sessions: func() *session.Manager { m, _ := session.NewManager(session.Config{MaxSessions: 100}); return m }(),
		Metrics:  metrics.NewCollector(),
		Auditor:  audit.NewNoop(),
		Logger:   slog.Default(),
	}
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": ts.URL},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		text := res.Content[0].(*mcp.TextContent).Text
		t.Fatalf("unexpected error for successful scrape: %s", text)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "article paragraph") {
		t.Errorf("expected article content in response, got: %s", text[:200])
	}
}

// Regression test: DNS failure returns network error
func TestScrapeTool_DNSFailure_ReturnsNetworkError(t *testing.T) {
	ctx := context.Background()
	deps := Dependencies{
		Cache:    cache.NewNoop(),
		Search:   &mockProvider{},
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2}),
		Content:  content.NewProcessor(),
		Sessions: func() *session.Manager { m, _ := session.NewManager(session.Config{MaxSessions: 100}); return m }(),
		Metrics:  metrics.NewCollector(),
		Auditor:  audit.NewNoop(),
		Logger:   slog.Default(),
	}
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": "https://this-domain-definitely-does-not-exist-xyz123.invalid/page"},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error response for invalid domain")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if strings.Contains(text, issueURL) {
		t.Errorf("network errors should NOT suggest filing an issue, got: %s", text)
	}
}

// Integration test: search_and_scrape surfaces per-URL failure diagnostics
func TestSearchAndScrape_AllFail_SurfacesFailures(t *testing.T) {
	// Mock search returns URLs that will all 403
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer failServer.Close()

	ctx := context.Background()
	deps := Dependencies{
		Cache:   cache.NewNoop(),
		Search:  &mockProviderWithURL{url: failServer.URL},
		Scraper: scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true}),
		Content: content.NewProcessor(),
		Sessions: func() *session.Manager {
			m, _ := session.NewManager(session.Config{MaxSessions: 100})
			return m
		}(),
		Metrics: metrics.NewCollector(),
		Auditor: audit.NewNoop(),
		Logger:  slog.Default(),
	}
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_and_scrape",
		Arguments: map[string]any{"query": "test query", "num_results": float64(1)},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool-level error: %s", res.Content[0].(*mcp.TextContent).Text)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	// Should have scrapeFailures array
	failures, ok := output["scrapeFailures"]
	if !ok {
		t.Fatal("expected scrapeFailures field when scrapes fail")
	}
	failArr, ok := failures.([]any)
	if !ok || len(failArr) == 0 {
		t.Fatal("expected non-empty scrapeFailures array")
	}

	// Each failure should have url, reason, and kind
	firstFail, ok := failArr[0].(map[string]any)
	if !ok {
		t.Fatal("expected failure to be an object")
	}
	if firstFail["url"] == nil || firstFail["url"] == "" {
		t.Error("expected url in failure")
	}
	if firstFail["reason"] == nil || firstFail["reason"] == "" {
		t.Error("expected reason in failure")
	}
	if firstFail["kind"] != "blocked" {
		t.Errorf("expected kind=blocked for 403, got %v", firstFail["kind"])
	}

	// Should have a note about all pages failing with issue URL
	note, ok := output["note"].(string)
	if !ok || note == "" {
		t.Fatal("expected note field when all scrapes fail")
	}
	if !strings.Contains(note, issueURL) {
		t.Errorf("expected issue URL in note, got: %s", note)
	}

	// urlsScraped should be 0
	summary := output["summary"].(map[string]any)
	if summary["urlsScraped"].(float64) != 0 {
		t.Errorf("expected 0 urls scraped, got %v", summary["urlsScraped"])
	}
}

// Integration test: search_and_scrape with partial success doesn't include note
func TestSearchAndScrape_PartialSuccess_NoNote(t *testing.T) {
	callCount := 0
	mixedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/html")
		content := strings.Repeat("Good article content with sufficient length. ", 20)
		fmt.Fprintf(w, `<html><body><article>%s</article></body></html>`, content)
	}))
	defer mixedServer.Close()

	ctx := context.Background()
	deps := Dependencies{
		Cache:   cache.NewNoop(),
		Search:  &mockProviderWithURL{url: mixedServer.URL},
		Scraper: scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true}),
		Content: content.NewProcessor(),
		Sessions: func() *session.Manager {
			m, _ := session.NewManager(session.Config{MaxSessions: 100})
			return m
		}(),
		Metrics: metrics.NewCollector(),
		Auditor: audit.NewNoop(),
		Logger:  slog.Default(),
	}
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_and_scrape",
		Arguments: map[string]any{"query": "test", "num_results": float64(1)},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].(*mcp.TextContent).Text)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	// Should NOT have a note when scraping succeeds
	if _, ok := output["note"]; ok {
		t.Error("should not include note when scrapes succeed")
	}

	// Should have content
	if output["combinedContent"] == "" {
		t.Error("expected non-empty combined content")
	}
}

// Regression test: SSRF blocked returns appropriate error
func TestScrapeTool_SSRF_ReturnsBlockedError(t *testing.T) {
	ctx := context.Background()
	deps := Dependencies{
		Cache:    cache.NewNoop(),
		Search:   &mockProvider{},
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: false}),
		Content:  content.NewProcessor(),
		Sessions: func() *session.Manager { m, _ := session.NewManager(session.Config{MaxSessions: 100}); return m }(),
		Metrics:  metrics.NewCollector(),
		Auditor:  audit.NewNoop(),
		Logger:   slog.Default(),
	}
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": "http://169.254.169.254/latest/meta-data"},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error response for SSRF-blocked URL")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "Scrape failed") {
		t.Errorf("expected scrape failed message, got: %s", text)
	}
}
