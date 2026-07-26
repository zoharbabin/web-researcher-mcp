package scraper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// TestMain redirects the package-wide default JinaReaderURL to a local stub
// server for the whole test binary (#270): the Jina tier is unconditionally in
// scrapeWithTieredFallback's ladder, so without this every existing pipeline
// test that doesn't already stub a specific tier would otherwise make a real
// network call to r.jina.ai. The stub returns empty content, so it behaves
// like Jina having nothing to add — tiers earlier/later in the ladder decide
// the result exactly as they did before this tier existed. Individual tests
// override it via withJinaEndpoint when they need to assert Jina-specific
// behavior.
func TestMain(m *testing.M) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"status":20000,"data":{"content":""}}`))
	}))
	JinaReaderURL = stub.URL + "/"
	code := m.Run()
	stub.Close()
	os.Exit(code)
}

// withJinaEndpoint points the Jina Reader base URL at a test server for the
// duration of the test, restoring it afterward.
func withJinaEndpoint(t *testing.T, url string) {
	t.Helper()
	prev := JinaReaderURL
	JinaReaderURL = url
	t.Cleanup(func() { JinaReaderURL = prev })
}

func TestScrapeJinaSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Return-Format") != "markdown" {
			t.Errorf("missing X-Return-Format: markdown, got %q", r.Header.Get("X-Return-Format"))
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("no key configured, Authorization must be absent, got %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"code":200,"status":20000,"data":{"title":"T","url":"https://x.example","content":"extracted markdown body"}}`))
	}))
	defer srv.Close()
	withJinaEndpoint(t, srv.URL+"/")

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	res, err := p.scrapeJina(context.Background(), "https://x.example", 5000, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Content != "extracted markdown body" {
		t.Errorf("content mismatch: %q", res.Content)
	}
	if res.Tier != "jina" {
		t.Errorf("tier should be jina, got %q", res.Tier)
	}
	if res.Title != "T" {
		t.Errorf("title mismatch: %+v", res)
	}
}

func TestScrapeJinaWithAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k123" {
			t.Errorf("missing bearer key, got %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"code":200,"status":20000,"data":{"content":"keyed body"}}`))
	}))
	defer srv.Close()
	withJinaEndpoint(t, srv.URL+"/")

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, JinaAPIKey: "k123"})
	res, err := p.scrapeJina(context.Background(), "https://x.example", 5000, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Content != "keyed body" {
		t.Errorf("content mismatch: %q", res.Content)
	}
}

func TestScrapeJinaEmptyContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"status":20000,"data":{"content":""}}`))
	}))
	defer srv.Close()
	withJinaEndpoint(t, srv.URL+"/")

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	_, err := p.scrapeJina(context.Background(), "https://x.example", 5000, false)
	if err == nil {
		t.Fatal("empty content should error so the orchestrator can keep falling back")
	}
}

func TestScrapeJinaRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))
	defer srv.Close()
	withJinaEndpoint(t, srv.URL+"/")

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	_, err := p.scrapeJina(context.Background(), "https://x.example", 5000, false)
	se, ok := err.(*ScrapeError)
	if !ok || se.Kind != ErrRateLimit {
		t.Errorf("429 should map to ErrRateLimit, got %v", err)
	}
}

func TestScrapeJinaHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
	}))
	defer srv.Close()
	withJinaEndpoint(t, srv.URL+"/")

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	_, err := p.scrapeJina(context.Background(), "https://x.example", 5000, false)
	se, ok := err.(*ScrapeError)
	if !ok || se.Kind != ErrBlocked {
		t.Errorf("403 should map to ErrBlocked, got %v", err)
	}
}

func TestScrapeJinaTruncation(t *testing.T) {
	longContent := ""
	for i := 0; i < 100; i++ {
		longContent += "0123456789"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"status":20000,"data":{"content":"` + longContent + `"}}`))
	}))
	defer srv.Close()
	withJinaEndpoint(t, srv.URL+"/")

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	res, err := p.scrapeJina(context.Background(), "https://x.example", 50, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Truncated {
		t.Error("expected Truncated=true")
	}
	if len(res.Content) != 50 {
		t.Errorf("expected content capped at 50 bytes, got %d", len(res.Content))
	}
}

func TestScrapeJinaNetworkError(t *testing.T) {
	withJinaEndpoint(t, "http://127.0.0.1:1/")

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	_, err := p.scrapeJina(context.Background(), "https://x.example", 5000, false)
	se, ok := err.(*ScrapeError)
	if !ok || se.Kind != ErrNetwork {
		t.Errorf("connection refused should map to ErrNetwork, got %v", err)
	}
}

// TestScrapeJinaDisabled verifies the JinaDisabled kill switch short-circuits
// before any network call, so a deployment or test context can opt out of
// the tier's dependency on r.jina.ai entirely (mirrors ChromePath="disabled").
func TestScrapeJinaDisabled(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{"code":200,"status":20000,"data":{"content":"should not be reached"}}`))
	}))
	defer srv.Close()
	withJinaEndpoint(t, srv.URL+"/")

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, JinaDisabled: true})
	_, err := p.scrapeJina(context.Background(), "https://x.example", 5000, false)
	if err == nil {
		t.Fatal("expected error when Jina tier is disabled")
	}
	if called {
		t.Error("scrapeJina made a network call despite JinaDisabled=true")
	}
}

// TestScrapeJinaFallthrough verifies the Jina tier fires between stealth and
// html: a page the free stealth/markdown tiers cannot extract (empty body)
// falls through to Jina, and the result carries the jina tier.
func TestScrapeJinaFallthrough(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body></body></html>"))
	}))
	defer page.Close()

	jina := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"status":20000,"data":{"content":"recovered by jina reader tier"}}`))
	}))
	defer jina.Close()
	withJinaEndpoint(t, jina.URL+"/")

	// ChromePath:"disabled" removes the browser tier so the run is deterministic.
	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ChromePath: "disabled"})
	res, err := p.Scrape(context.Background(), page.URL, 5000)
	if err != nil {
		t.Fatalf("expected Jina fallback to recover content, got: %v", err)
	}
	if res.Content != "recovered by jina reader tier" {
		t.Errorf("content should come from Jina tier, got %q", res.Content)
	}
	if res.Tier != "jina" {
		t.Errorf("tier should be jina, got %q", res.Tier)
	}
}
