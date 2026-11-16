package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
)

// cmvpShapedPage is a trimmed page shaped like a CMVP certificate: each
// algorithm name is followed by the same short algorithm ID, and a long
// cookie notice repeats.
func cmvpShapedPage(algs []string, id string) string {
	notice := "<p>This site uses cookies to improve your experience. By continuing to browse you accept the use of cookies on this site.</p>"
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><title>Certificate 5314</title></head><body><article>")
	b.WriteString("<h1>Certificate 5314</h1>")
	b.WriteString("<p>The module below is validated against the listed approved algorithms, as recorded by the validation program for this certificate.</p>")
	b.WriteString(notice)
	for _, a := range algs {
		fmt.Fprintf(&b, "<h3>%s</h3><p>%s</p><p>Active</p>", a, id)
	}
	b.WriteString(notice)
	b.WriteString(notice)
	b.WriteString("</article></body></html>")
	return b.String()
}

func TestScrapePage_KeepsRepeatedShortValuesAndCountsDeliveredWords(t *testing.T) {
	algs := []string{"AES-CCM", "AES-CMAC", "AES-GCM", "AES-ECB", "HMAC-SHA2-256", "SHA2-256", "DRBG", "KAS-ECC"}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(cmvpShapedPage(algs, "A6283")))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := setupTestDeps()
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": ts.URL, "max_length": 30000},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	var output map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	body, _ := output["content"].(string)
	if n := strings.Count(body, "A6283"); n != len(algs) {
		t.Errorf("A6283 appears %d times in content, want %d\n%s", n, len(algs), body)
	}
	if n := strings.Count(body, "This site uses cookies"); n != 1 {
		t.Errorf("cookie notice appears %d times in content, want 1", n)
	}
	// wordCount must describe the delivered content, not the pre-dedup scrape.
	wc, ok := output["wordCount"].(float64)
	if !ok {
		t.Fatalf("wordCount missing: %v", output["wordCount"])
	}
	if want := content.WordCount(body); int(wc) != want {
		t.Errorf("wordCount = %d, want %d (words in delivered content)", int(wc), want)
	}
}

func TestSearchAndScrape_WordCountMatchesDeliveredContent(t *testing.T) {
	// The long notice repeats, so Process removes copies. The per-source
	// wordCount must match the source content that is returned.
	notice := "<p>This site uses cookies to improve your experience. By continuing to browse you accept the use of cookies on this site.</p>"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!DOCTYPE html><html><head><title>Repeats</title></head><body><article>" +
			"<p>Opening paragraph that explains what this short page is about, with enough detail to pass the admission gate.</p>" +
			notice + "<p>ID</p><p>A6283</p>" + notice + "<p>ID</p><p>A6283</p>" + notice +
			"</article></body></html>"))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &mockProviderWithURL{url: ts.URL}
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_and_scrape",
		Arguments: map[string]any{"query": "test topic"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	var output map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	sources, _ := output["sources"].([]any)
	if len(sources) == 0 {
		t.Fatal("expected at least one source")
	}
	for i, s := range sources {
		src, _ := s.(map[string]any)
		body, _ := src["content"].(string)
		wc, _ := src["wordCount"].(float64)
		if want := content.WordCount(body); int(wc) != want {
			t.Errorf("source %d wordCount = %d, want %d", i, int(wc), want)
		}
		if n := strings.Count(body, "A6283"); n != 2 {
			t.Errorf("source %d: A6283 appears %d times, want 2", i, n)
		}
	}
}
