package tools

import (
	"context"
	"encoding/json"
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

func scrapeTestDeps() Dependencies {
	return Dependencies{
		Cache:    cache.NewNoop(),
		Search:   &mockProvider{},
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true}),
		Content:  content.NewProcessor(),
		Sessions: func() session.Manager { m, _ := session.NewManager(session.Config{MaxSessions: 100}); return m }(),
		Metrics:  metrics.NewCollector(),
		Auditor:  audit.NewNoop(),
	}
}

// TestScrapePage_StructuredDataPresent is the end-to-end #46/#48 guard: a page
// with JSON-LD, OG meta, and a data table yields a structuredData object AND a
// GFM pipe table inside content, surfaced through the scrape_page tool.
func TestScrapePage_StructuredDataPresent(t *testing.T) {
	page := `<html><head>
		<title>Doc</title>
		<script type="application/ld+json">{"@type":"Article","headline":"Hello"}</script>
		<meta property="og:title" content="OG Doc">
		<meta name="citation_doi" content="10.1234/abc">
	</head><body><article>
		<p>` + strings.Repeat("Sufficiently long body so the HTML tier is the one that wins. ", 5) + `</p>
		<table><tr><th>Metric</th><th>Value</th></tr><tr><td>Speed</td><td>Fast</td></tr></table>
	</article></body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer ts.Close()

	ctx := context.Background()
	srv := createTestServer(scrapeTestDeps())
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "scrape_page", Arguments: map[string]any{"url": ts.URL}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}

	sd, ok := out["structuredData"].(map[string]any)
	if !ok {
		t.Fatalf("expected structuredData object, got %v", out["structuredData"])
	}
	if _, ok := sd["jsonLd"]; !ok {
		t.Errorf("expected jsonLd in structuredData, got %v", sd)
	}
	og, _ := sd["openGraph"].(map[string]any)
	if og["og:title"] != "OG Doc" {
		t.Errorf("expected og:title in structuredData, got %v", sd["openGraph"])
	}
	cit, _ := sd["citation"].(map[string]any)
	if cit["citation_doi"] != "10.1234/abc" {
		t.Errorf("expected citation_doi, got %v", sd["citation"])
	}
	// #48: content carries a GFM table.
	contentStr, _ := out["content"].(string)
	if !strings.Contains(contentStr, "| Metric | Value |") || !strings.Contains(contentStr, "| --- | --- |") {
		t.Errorf("expected GFM table in content, got:\n%s", contentStr)
	}
}

// TestScrapePage_StructuredDataOmittedWhenAbsent confirms the field is omitted
// for a plain page (additive, no empty object).
func TestScrapePage_StructuredDataOmittedWhenAbsent(t *testing.T) {
	page := `<html><head><title>Plain</title></head><body><article><p>` +
		strings.Repeat("Just prose, no structured markup at all here. ", 6) + `</p></article></body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer ts.Close()

	ctx := context.Background()
	srv := createTestServer(scrapeTestDeps())
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "scrape_page", Arguments: map[string]any{"url": ts.URL}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, present := out["structuredData"]; present {
		t.Errorf("structuredData must be omitted for a plain page, got %v", out["structuredData"])
	}
}

// TestScrapePage_RawModeNoStructuredData confirms raw mode never carries it.
func TestScrapePage_RawModeNoStructuredData(t *testing.T) {
	page := `<html><head><script type="application/ld+json">{"@type":"Thing"}</script></head><body>x</body></html>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer ts.Close()

	ctx := context.Background()
	srv := createTestServer(scrapeTestDeps())
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "scrape_page", Arguments: map[string]any{"url": ts.URL, "mode": "raw"}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, present := out["structuredData"]; present {
		t.Errorf("raw mode must not carry structuredData, got %v", out["structuredData"])
	}
}
