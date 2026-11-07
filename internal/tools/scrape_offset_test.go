package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
)

var offsetMarkerRe = regexp.MustCompile(`\n\n\[content truncated, continue with offset=\d+\]$`)

// longArticleHandler serves an article of about 120 KB of unique paragraphs and
// counts the requests it receives.
func longArticleHandler(hits *atomic.Int64) http.HandlerFunc {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><title>Long page</title></head><body><article><h1>Long page</h1>")
	for i := 0; b.Len() < 125000; i++ {
		fmt.Fprintf(&b, "<p>Paragraph %d: données 日本語 about topic %d. It has a second sentence with enough words to look like prose.</p>", i, i*7)
	}
	b.WriteString("</article></body></html>")
	page := b.String()
	return func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}
}

func scrapeOffsetSession(t *testing.T, ts *httptest.Server) (context.Context, *mcp.ClientSession) {
	t.Helper()
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Cache = cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 16})
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	t.Cleanup(func() { session.Close() })
	return ctx, session
}

func callScrape(ctx context.Context, t *testing.T, session *mcp.ClientSession, args map[string]any) (map[string]any, *mcp.CallToolResult) {
	t.Helper()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "scrape_page", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError {
		return nil, res
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse output: %v", err)
	}
	return out, res
}

func TestScrapePage_OffsetReadsLongPageInPartsWithOneScrape(t *testing.T) {
	var hits atomic.Int64
	ts := httptest.NewServer(longArticleHandler(&hits))
	defer ts.Close()
	ctx, session := scrapeOffsetSession(t, ts)

	// One large read is the reference.
	whole, _ := callScrape(ctx, t, session, map[string]any{"url": ts.URL, "max_length": 500000})
	if whole["truncated"] != false {
		t.Fatalf("large read: truncated = %v, want false", whole["truncated"])
	}
	if _, has := whole["nextOffset"]; has {
		t.Error("large read: nextOffset must be omitted on the last page")
	}
	want, _ := whole["content"].(string)
	total := int(whole["totalLength"].(float64))
	if total != len(want) || total < 100000 {
		t.Fatalf("totalLength = %d, len(content) = %d, want equal and >= 100000", total, len(want))
	}
	scrapeHits := hits.Load()
	if scrapeHits == 0 {
		t.Fatal("expected at least one request to the test server")
	}

	var got strings.Builder
	offset, pages := 0, 0
	for {
		pages++
		if pages > 10 {
			t.Fatal("paging did not finish")
		}
		out, _ := callScrape(ctx, t, session, map[string]any{"url": ts.URL, "max_length": 40000, "offset": offset})
		if int(out["offset"].(float64)) != offset {
			t.Errorf("page %d: offset = %v, want %d", pages, out["offset"], offset)
		}
		if int(out["totalLength"].(float64)) != total {
			t.Errorf("page %d: totalLength = %v, want %d", pages, out["totalLength"], total)
		}
		page := out["content"].(string)
		next, hasNext := out["nextOffset"]
		if out["truncated"] != hasNext {
			t.Errorf("page %d: truncated = %v but nextOffset present = %v", pages, out["truncated"], hasNext)
		}
		if int(out["contentLength"].(float64)) != len(page) {
			t.Errorf("page %d: contentLength = %v, want %d", pages, out["contentLength"], len(page))
		}
		if !hasNext {
			got.WriteString(page)
			break
		}
		got.WriteString(offsetMarkerRe.ReplaceAllString(page, ""))
		if !strings.HasSuffix(page, fmt.Sprintf("[content truncated, continue with offset=%d]", int(next.(float64)))) {
			t.Errorf("page %d: marker does not name nextOffset %v", pages, next)
		}
		offset = int(next.(float64))
	}
	if pages < 3 {
		t.Errorf("read in %d pages, want at least 3", pages)
	}
	if got.String() != want {
		t.Errorf("joined pages differ from the single read (len %d vs %d)", got.Len(), len(want))
	}
	if n := hits.Load(); n != scrapeHits {
		t.Errorf("server saw %d requests after the first scrape, want %d: later pages must come from the cache", n, scrapeHits)
	}
}

func TestScrapePage_OffsetPastEndAndFieldsOnShortPage(t *testing.T) {
	var hits atomic.Int64
	ts := httptest.NewServer(longArticleHandler(&hits))
	defer ts.Close()
	ctx, session := scrapeOffsetSession(t, ts)

	first, _ := callScrape(ctx, t, session, map[string]any{"url": ts.URL, "max_length": 1000})
	if first["truncated"] != true || first["nextOffset"] == nil {
		t.Fatalf("small max_length must cut: truncated=%v nextOffset=%v", first["truncated"], first["nextOffset"])
	}
	total := int(first["totalLength"].(float64))

	for _, off := range []int{total, total + 5000} {
		out, _ := callScrape(ctx, t, session, map[string]any{"url": ts.URL, "offset": off})
		if out["content"] != "" || out["truncated"] != false || out["contentLength"].(float64) != 0 {
			t.Errorf("offset %d: content=%q truncated=%v contentLength=%v", off, out["content"], out["truncated"], out["contentLength"])
		}
		if _, has := out["nextOffset"]; has {
			t.Errorf("offset %d: nextOffset must be omitted", off)
		}
		if int(out["totalLength"].(float64)) != total {
			t.Errorf("offset %d: totalLength = %v, want %d", off, out["totalLength"], total)
		}
	}
}

func TestScrapePage_OffsetValidation(t *testing.T) {
	var hits atomic.Int64
	ts := httptest.NewServer(longArticleHandler(&hits))
	defer ts.Close()
	ctx, session := scrapeOffsetSession(t, ts)

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"negative offset", map[string]any{"offset": -1}, "offset must be 0 or greater"},
		{"raw with offset", map[string]any{"mode": "raw", "offset": 10}, "only supported with mode=full"},
		{"preview with offset", map[string]any{"mode": "preview", "offset": 10}, "only supported with mode=full"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{"url": ts.URL}
			for k, v := range tc.args {
				args[k] = v
			}
			out, res := callScrape(ctx, t, session, args)
			if out != nil || !res.IsError {
				t.Fatalf("want a tool error, got %v", out)
			}
			if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, tc.want) {
				t.Errorf("error %q does not contain %q", text, tc.want)
			}
		})
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("validation errors must not scrape: server saw %d requests", n)
	}
}

func TestScrapePage_RawAndPreviewHaveNoPagingFields(t *testing.T) {
	var hits atomic.Int64
	ts := httptest.NewServer(longArticleHandler(&hits))
	defer ts.Close()
	ctx, session := scrapeOffsetSession(t, ts)

	for _, mode := range []string{"raw", "preview"} {
		out, _ := callScrape(ctx, t, session, map[string]any{"url": ts.URL, "mode": mode})
		for _, f := range []string{"totalLength", "offset", "nextOffset"} {
			if _, has := out[f]; has {
				t.Errorf("mode %s: field %s must be omitted", mode, f)
			}
		}
	}
}

func TestPageScrapeEnvelope(t *testing.T) {
	t.Parallel()
	cached := []byte(`{"content":"aaaa bbbb cccc","truncated":false,"id":9007199254740993,"nextOffset":3}`)
	out, ok := pageScrapeEnvelope(cached, 0, 9)
	if !ok {
		t.Fatal("expected ok")
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["content"] != "aaaa bbbb\n\n[content truncated, continue with offset=9]" || got["truncated"] != true || got["nextOffset"].(float64) != 9 {
		t.Errorf("unexpected envelope: %v", got)
	}
	if !strings.Contains(string(out), "9007199254740993") {
		t.Errorf("large number lost precision: %s", out)
	}
	if _, ok := pageScrapeEnvelope([]byte("not json"), 0, 9); ok {
		t.Error("invalid cache body must report not ok")
	}
	if _, ok := pageScrapeEnvelope([]byte(`{"x":1}`), 0, 9); ok {
		t.Error("cache body without content must report not ok")
	}
}
