package tools

import (
	"context"
	"encoding/json"
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

// TestScrapePageRaw_LargeBody_SurfacesContentSizeBytes is the #508 end-to-end
// guard for scrape_page: a raw-mode body large enough to cross
// linkThresholdBytes must link out via resource_link AND carry a
// contentSizeBytes field on the inline summary, so a caller can judge whether
// the linked artifact is worth a follow-up read without one.
func TestScrapePageRaw_LargeBody_SurfacesContentSizeBytes(t *testing.T) {
	bigBody := strings.Repeat("raw page byte filler content. ", linkThresholdBytes/20)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(bigBody))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := Dependencies{
		Cache:    cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 8}),
		Search:   &mockProvider{},
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true}),
		Content:  content.NewProcessor(),
		Sessions: func() session.Manager { m, _ := session.NewManager(session.Config{MaxSessions: 100}); return m }(),
		Metrics:  metrics.NewCollector(),
		Auditor:  audit.NewNoop(),
		Logger:   slog.Default(),
	}
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": ts.URL, "mode": "raw"},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool-level error: %s", res.Content[0].(*mcp.TextContent).Text)
	}

	// The large raw body must link out (summary + resource_link, not the full body).
	if len(res.Content) != 2 {
		t.Fatalf("expected summary + resource_link (2 content items) for a large raw body, got %d", len(res.Content))
	}
	if _, ok := res.Content[1].(*mcp.ResourceLink); !ok {
		t.Fatalf("second content should be a ResourceLink, got %T", res.Content[1])
	}

	var summary map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &summary); err != nil {
		t.Fatalf("parse inline summary: %v", err)
	}
	if summary["linked"] != true {
		t.Fatalf("expected linked:true in summary, got %+v", summary)
	}
	csb, ok := summary["contentSizeBytes"].(float64)
	if !ok {
		t.Fatalf("expected numeric contentSizeBytes in inline summary, got %+v", summary)
	}
	if int(csb) != len(bigBody) {
		t.Errorf("contentSizeBytes = %v, want %d (raw content length)", csb, len(bigBody))
	}
}

// TestScrapePageRaw_SmallBody_NoContentSizeBytes confirms the new field is
// scoped to the link hand-off only: a small (non-linked) raw response must NOT
// gain a contentSizeBytes field — contentLength already covers it inline.
func TestScrapePageRaw_SmallBody_NoContentSizeBytes(t *testing.T) {
	const smallBody = "tiny raw body"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(smallBody))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := scrapeTestDeps()
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": ts.URL, "mode": "raw"},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool-level error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	if len(res.Content) != 1 {
		t.Fatalf("small raw body should inline (1 content item), got %d", len(res.Content))
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, present := out["contentSizeBytes"]; present {
		t.Errorf("small inline raw response must not carry contentSizeBytes, got %+v", out)
	}
	if out["contentLength"] == nil {
		t.Error("small inline raw response should still carry contentLength")
	}
}

// TestSearchAndScrape_LargeBundle_SurfacesSourceCountAndStatus is the #508
// end-to-end guard for search_and_scrape: a multi-page bundle large enough to
// cross linkThresholdBytes must link out AND carry sourceCount + status on the
// inline summary.
func TestSearchAndScrape_LargeBundle_SurfacesSourceCountAndStatus(t *testing.T) {
	bigArticle := strings.Repeat("Sufficiently long article body filler content for the pipeline. ", linkThresholdBytes/50)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body><article>" + bigArticle + "</article></body></html>"))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := Dependencies{
		Cache:    cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 8}),
		Search:   &mockProviderWithURL{url: ts.URL},
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true}),
		Content:  content.NewProcessor(),
		Sessions: func() session.Manager { m, _ := session.NewManager(session.Config{MaxSessions: 100}); return m }(),
		Metrics:  metrics.NewCollector(),
		Auditor:  audit.NewNoop(),
		Logger:   slog.Default(),
	}
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_and_scrape",
		Arguments: map[string]any{"query": "test query", "num_results": float64(1), "total_max_length": float64(1_000_000)},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool-level error: %s", res.Content[0].(*mcp.TextContent).Text)
	}

	if len(res.Content) != 2 {
		t.Fatalf("expected summary + resource_link (2 content items) for a large bundle, got %d", len(res.Content))
	}
	if _, ok := res.Content[1].(*mcp.ResourceLink); !ok {
		t.Fatalf("second content should be a ResourceLink, got %T", res.Content[1])
	}

	var summary map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &summary); err != nil {
		t.Fatalf("parse inline summary: %v", err)
	}
	if summary["linked"] != true {
		t.Fatalf("expected linked:true in summary, got %+v", summary)
	}
	sc, ok := summary["sourceCount"].(float64)
	if !ok {
		t.Fatalf("expected numeric sourceCount in inline summary, got %+v", summary)
	}
	if int(sc) != 1 {
		t.Errorf("sourceCount = %v, want 1", sc)
	}
	if summary["status"] != "complete" {
		t.Errorf("status = %v, want %q", summary["status"], "complete")
	}
}

// TestSearchAndScrape_SmallResult_NoSourceCountHint confirms the new fields are
// scoped to the link hand-off only: a small (non-linked) search_and_scrape
// response must NOT gain sourceCount — summary.urlsScraped already covers it.
func TestSearchAndScrape_SmallResult_NoSourceCountHint(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body><article>" + strings.Repeat("Short article content. ", 20) + "</article></body></html>"))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := Dependencies{
		Cache:    cache.NewNoop(),
		Search:   &mockProviderWithURL{url: ts.URL},
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true}),
		Content:  content.NewProcessor(),
		Sessions: func() session.Manager { m, _ := session.NewManager(session.Config{MaxSessions: 100}); return m }(),
		Metrics:  metrics.NewCollector(),
		Auditor:  audit.NewNoop(),
		Logger:   slog.Default(),
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
		t.Fatalf("unexpected tool-level error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	if len(res.Content) != 1 {
		t.Fatalf("small result should inline (1 content item), got %d", len(res.Content))
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, present := out["sourceCount"]; present {
		t.Errorf("small inline search_and_scrape response must not carry sourceCount, got %+v", out)
	}
	summaryObj, _ := out["summary"].(map[string]any)
	if summaryObj["urlsScraped"] == nil {
		t.Error("small inline response should still carry summary.urlsScraped")
	}
}

// TestScrapePageSchemaDeclaresContentSizeBytes and
// TestSearchAndScrapeSchemaDeclaresSourceCount are manual schema-declaration
// guards (#508). scrape_page and search_and_scrape are excluded from the
// generic TestOutputSchemaMatchesResponse gate in metadata_test.go (their
// resource_link hand-off path returns a hand-built *mcp.CallToolResult with a
// nil typed `out`, which the SDK never runs through OutputSchema validation —
// see largeResultOrInlineWithFields in artifacts.go), so these fields would
// otherwise be undocumented with no CI failure. Mirrors the existing
// TestScrapePageSchemaDeclaresDOIFields pattern in scrape_doi_test.go.
func TestScrapePageSchemaDeclaresContentSizeBytes(t *testing.T) {
	props, _ := scrapePageOutputSchema["properties"].(map[string]any)
	if props == nil {
		t.Fatal("scrapePageOutputSchema has no properties")
	}
	if _, declared := props["contentSizeBytes"]; !declared {
		t.Error("scrapePageOutputSchema must declare \"contentSizeBytes\"")
	}
}

func TestSearchAndScrapeSchemaDeclaresSourceCount(t *testing.T) {
	props, _ := searchAndScrapeOutputSchema["properties"].(map[string]any)
	if props == nil {
		t.Fatal("searchAndScrapeOutputSchema has no properties")
	}
	if _, declared := props["sourceCount"]; !declared {
		t.Error("searchAndScrapeOutputSchema must declare \"sourceCount\"")
	}
}
