package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
)

func TestScrapeMetadata(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		res  scraper.ScrapeResult
		want map[string]any
	}{
		{"nothing set", scraper.ScrapeResult{}, nil},
		{"title only keeps author key", scraper.ScrapeResult{Title: "T"}, map[string]any{"title": "T", "author": ""}},
		{"date only still writes block", scraper.ScrapeResult{PublishDate: "2026-09-14"}, map[string]any{"title": "", "author": "", "publishedDate": "2026-09-14"}},
		{"author only still writes block", scraper.ScrapeResult{Author: "A"}, map[string]any{"title": "", "author": "A"}},
		{"site name only still writes block", scraper.ScrapeResult{SiteName: "S"}, map[string]any{"title": "", "author": "", "siteName": "S"}},
		{"all fields", scraper.ScrapeResult{Title: "T", Author: "A", PublishDate: "2026-09-14", SiteName: "S"}, map[string]any{"title": "T", "author": "A", "publishedDate": "2026-09-14", "siteName": "S"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := scrapeMetadata(&tc.res)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("got %v, want nil", got)
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("metadata[%q] = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

// scrapeServedPage scrapes a page served from a local test server through the
// scrape_page tool and returns the parsed output.
func scrapeServedPage(t *testing.T, page string) map[string]any {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(ts.Close)

	ctx := context.Background()
	client := connectTestClient(ctx, t, createTestServer(scrapeTestDeps()))
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
	return out
}

const metadataTestBody = `<p>The new standard covers key management and operations for entities that handle cryptographic keys. It is long enough for the extractor to accept it as the page body.</p>
<p>A second paragraph adds depth so the content comfortably clears every minimum length check in the pipeline tiers.</p>`

// The PCI blog shape: author meta tag, date only in the visible byline.
func TestScrapePage_MetadataFromByline(t *testing.T) {
	page := `<!doctype html><html><head>
<meta name="author" content="Alicia Malone">
<title>Just Published: PCI KMO Standard v1.0</title></head><body>
<div class="post-details-meta">Posted by
<a class="author-link" href="/author/alicia-malone">Alicia Malone</a> on 14 Sep, 2026 in
<a class="topic-link" href="/topic/x">Topic</a></div>
<article>` + metadataTestBody + `</article></body></html>`

	out := scrapeServedPage(t, page)

	md, ok := out["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata object, got %v", out["metadata"])
	}
	if md["author"] != "Alicia Malone" {
		t.Errorf("metadata.author = %v", md["author"])
	}
	if md["publishedDate"] != "2026-09-14" {
		t.Errorf("metadata.publishedDate = %v", md["publishedDate"])
	}
	if _, has := md["siteName"]; has {
		t.Errorf("siteName should be omitted when empty, got %v", md["siteName"])
	}
	cit, _ := out["citation"].(map[string]any)
	formatted, _ := cit["formatted"].(map[string]any)
	apa, _ := formatted["apa"].(string)
	if strings.Contains(apa, "n.d.") || !strings.Contains(apa, "2026-09-14") {
		t.Errorf("APA citation should carry the date, got %q", apa)
	}
}

func TestScrapePage_MetadataFromJSONLD(t *testing.T) {
	page := `<!doctype html><html><head><title>LD Page</title>
<meta property="og:site_name" content="Example News">
<script type="application/ld+json">{"@type":"Article","datePublished":"2020-01-15T08:00:00Z","author":{"@type":"Person","name":"Dana Lee"}}</script>
</head><body><article>` + metadataTestBody + `</article></body></html>`

	md, _ := scrapeServedPage(t, page)["metadata"].(map[string]any)
	if md["publishedDate"] != "2020-01-15" || md["author"] != "Dana Lee" || md["siteName"] != "Example News" {
		t.Errorf("unexpected metadata: %v", md)
	}
}

// A page with no author, date or site name keeps today's output: title and an
// empty author, nothing else.
func TestScrapePage_MetadataNoSignal(t *testing.T) {
	page := `<!doctype html><html><head><title>Plain</title></head><body><article>` + metadataTestBody + `</article></body></html>`

	out := scrapeServedPage(t, page)
	md, ok := out["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata object, got %v", out["metadata"])
	}
	if md["title"] != "Plain" || md["author"] != "" {
		t.Errorf("unexpected metadata: %v", md)
	}
	if _, has := md["publishedDate"]; has {
		t.Errorf("publishedDate should be omitted: %v", md)
	}
	if _, has := md["siteName"]; has {
		t.Errorf("siteName should be omitted: %v", md)
	}
}
