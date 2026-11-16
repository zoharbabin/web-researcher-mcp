package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

const sessionTestTitle = "Session tracking page"

func sessionTestServer(hits *atomic.Int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, "<!DOCTYPE html><html><head><title>%s</title></head><body><article><h1>%s</h1>"+
			"<p>This page has enough words to count as real content for the scraper. It talks about caching, sessions and research sources in plain sentences.</p>"+
			"<p>A second paragraph keeps the extraction well above any sparsity threshold, with a few more words about the same topic.</p>"+
			"</article></body></html>", sessionTestTitle, sessionTestTitle)
	}))
}

func sessionScrapeDeps() Dependencies {
	deps := setupTestDeps()
	deps.Cache = cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 16})
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	return deps
}

func newResearchSession(t *testing.T, deps Dependencies) string {
	t.Helper()
	idx, err := deps.Sessions.Create("default", "")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return idx.ID
}

func fullSession(t *testing.T, deps Dependencies, id string) *session.Session {
	t.Helper()
	full, err := deps.Sessions.GetFull("default", "", id)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	return full
}

func scrapeWith(ctx context.Context, t *testing.T, cs *mcp.ClientSession, args map[string]any) {
	t.Helper()
	if _, res := callScrape(ctx, t, cs, args); res.IsError {
		t.Fatalf("scrape_page returned an error: %+v", res.Content)
	}
}

// A second scrape of the same URL is a cache hit. It must still add the page
// to the session (#849).
func TestScrapePage_CacheHitRecordsSessionSource(t *testing.T) {
	for _, mode := range []string{"full", "preview", "raw"} {
		for _, firstWithSession := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/first_with_session=%v", mode, firstWithSession), func(t *testing.T) {
				var hits atomic.Int64
				ts := sessionTestServer(&hits)
				defer ts.Close()
				ctx := context.Background()
				deps := sessionScrapeDeps()
				cs := connectTestClient(ctx, t, createTestServer(deps))
				defer cs.Close()
				id := newResearchSession(t, deps)

				first := map[string]any{"url": ts.URL, "mode": mode}
				if firstWithSession {
					first["sessionId"] = id
				}
				scrapeWith(ctx, t, cs, first)
				afterFirst := hits.Load()
				if afterFirst == 0 {
					t.Fatal("first scrape did not reach the server")
				}

				scrapeWith(ctx, t, cs, map[string]any{"url": ts.URL, "mode": mode, "sessionId": id})
				if got := hits.Load(); got != afterFirst {
					t.Fatalf("second scrape hit the server again: %d requests, want %d (cache hit)", got, afterFirst)
				}

				full := fullSession(t, deps, id)
				if len(full.Sources) != 1 || full.Sources[0].URL != ts.URL {
					t.Fatalf("session sources = %+v, want exactly one for %s", full.Sources, ts.URL)
				}
				if full.Sources[0].Title != sessionTestTitle {
					t.Errorf("source title = %q, want %q", full.Sources[0].Title, sessionTestTitle)
				}
				if full.Sources[0].Relevance != "scraped" {
					t.Errorf("source relevance = %q, want scraped", full.Sources[0].Relevance)
				}
				wantOutcomes := 1
				if firstWithSession {
					wantOutcomes = 2
				}
				if len(full.Outcomes) != wantOutcomes {
					t.Errorf("outcomes = %d, want %d", len(full.Outcomes), wantOutcomes)
				}
			})
		}
	}
}

// A cache hit read in parts (offset) keeps the title from the stored envelope.
func TestScrapePage_CacheHitWithOffsetKeepsTitle(t *testing.T) {
	var hits atomic.Int64
	ts := httptest.NewServer(longArticleHandler(&hits))
	defer ts.Close()
	ctx := context.Background()
	deps := sessionScrapeDeps()
	cs := connectTestClient(ctx, t, createTestServer(deps))
	defer cs.Close()
	id := newResearchSession(t, deps)

	scrapeWith(ctx, t, cs, map[string]any{"url": ts.URL})
	scrapeWith(ctx, t, cs, map[string]any{"url": ts.URL, "offset": 1000, "sessionId": id})

	full := fullSession(t, deps, id)
	if len(full.Sources) != 1 || full.Sources[0].Title != "Long page" {
		t.Fatalf("session sources = %+v, want one titled %q", full.Sources, "Long page")
	}
}

// A negative-cache hit records the failed outcome, as a fresh failure does.
func TestScrapePage_NegativeCacheHitRecordsSessionOutcome(t *testing.T) {
	for _, mode := range []string{"full", "raw"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			deps := sessionScrapeDeps()
			cs := connectTestClient(ctx, t, createTestServer(deps))
			defer cs.Close()
			id := newResearchSession(t, deps)
			const target = "https://example.com/blocked"
			writeNegCache(ctx, deps, target, &scraper.ScrapeError{Kind: scraper.ErrAuth, Message: "HTTP 401: authentication required"})

			_, res := callScrape(ctx, t, cs, map[string]any{"url": target, "mode": mode, "sessionId": id})
			if !res.IsError {
				t.Fatal("expected the cached failure to be returned as an error")
			}
			full := fullSession(t, deps, id)
			if len(full.Outcomes) != 1 || full.Outcomes[0].Success || full.Outcomes[0].URL != target {
				t.Fatalf("outcomes = %+v, want one failed outcome for %s", full.Outcomes, target)
			}
			if len(full.Sources) != 0 {
				t.Errorf("sources = %+v, want none", full.Sources)
			}
		})
	}
}

func TestCachedScrapeTitle(t *testing.T) {
	cases := map[string]string{
		`{"citation":{"metadata":{"title":"T"}}}`: "T",
		`{"citation":{"metadata":{}}}`:            "",
		`{"content":"x"}`:                         "",
		`not json`:                                "",
	}
	for in, want := range cases {
		if got := cachedScrapeTitle([]byte(in)); got != want {
			t.Errorf("cachedScrapeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
