package scraper

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withExaEndpoint points the Exa contents URL at a test server for the duration
// of the test, restoring it afterward.
func withExaEndpoint(t *testing.T, url string) {
	t.Helper()
	prev := exaContentsURL
	exaContentsURL = url
	t.Cleanup(func() { exaContentsURL = prev })
}

func TestScrapeExaSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "k" {
			t.Errorf("missing x-api-key, got %q", r.Header.Get("x-api-key"))
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "maxCharacters") {
			t.Errorf("request should set text.maxCharacters, got %s", body)
		}
		_, _ = w.Write([]byte(`{
			"results":[{"title":"T","url":"https://x.example","author":"A","text":"extracted body text"}],
			"statuses":[{"id":"https://x.example","status":"success","source":"crawled"}],
			"costDollars":{"total":0.001}
		}`))
	}))
	defer srv.Close()
	withExaEndpoint(t, srv.URL)

	// AllowPrivateIPs so the SSRF-safe client can reach the 127.0.0.1 test server.
	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ExaAPIKey: "k"})
	res, err := p.scrapeExa(context.Background(), "https://x.example", 5000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Content != "extracted body text" {
		t.Errorf("content mismatch: %q", res.Content)
	}
	if res.Tier != "exa:crawled" {
		t.Errorf("tier should carry crawled provenance, got %q", res.Tier)
	}
	if res.Title != "T" || res.Author != "A" {
		t.Errorf("title/author mismatch: %+v", res)
	}
}

func TestScrapeExaCachedProvenance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"results":[{"url":"https://x.example","text":"cached text body here"}],
			"statuses":[{"id":"https://x.example","status":"success","source":"cached"}],
			"costDollars":{"total":0.001}
		}`))
	}))
	defer srv.Close()
	withExaEndpoint(t, srv.URL)

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ExaAPIKey: "k"})
	res, err := p.scrapeExa(context.Background(), "https://x.example", 5000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Tier != "exa:cached" {
		t.Errorf("tier should be exa:cached, got %q", res.Tier)
	}
}

func TestScrapeExaEmptyContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"url":"https://x.example","text":""}],"costDollars":{"total":0}}`))
	}))
	defer srv.Close()
	withExaEndpoint(t, srv.URL)

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ExaAPIKey: "k"})
	_, err := p.scrapeExa(context.Background(), "https://x.example", 5000)
	if err == nil {
		t.Fatal("empty content should error so the orchestrator can keep falling back")
	}
}

func TestScrapeExaRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))
	defer srv.Close()
	withExaEndpoint(t, srv.URL)

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ExaAPIKey: "k"})
	_, err := p.scrapeExa(context.Background(), "https://x.example", 5000)
	se, ok := err.(*ScrapeError)
	if !ok || se.Kind != ErrRateLimit {
		t.Errorf("429 should map to ErrRateLimit, got %v", err)
	}
}

func TestScrapeExaNotConfigured(t *testing.T) {
	p := NewPipeline(PipelineConfig{MaxConcurrency: 2})
	_, err := p.scrapeExa(context.Background(), "https://x.example", 5000)
	if err == nil {
		t.Fatal("unconfigured Exa tier should error")
	}
}

// TestScrapeExaFallthrough verifies the Exa tier actually fires as the LAST
// resort: a page that the free tiers cannot extract (empty body) falls through
// to Exa, and the result carries the exa provenance tier. The page and Exa both
// live on 127.0.0.1 test servers, so AllowPrivateIPs is required.
func TestScrapeExaFallthrough(t *testing.T) {
	// The page returns an empty body so every free tier (markdown/stealth/html)
	// fails the >100-byte threshold.
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body></body></html>"))
	}))
	defer page.Close()

	exa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"results":[{"url":"` + page.URL + `","text":"recovered by exa neural extraction tier"}],
			"statuses":[{"id":"` + page.URL + `","status":"success","source":"crawled"}],
			"costDollars":{"total":0.001}
		}`))
	}))
	defer exa.Close()
	withExaEndpoint(t, exa.URL)

	// ChromePath:"disabled" removes the browser tier so the run is deterministic;
	// Exa is the only configured fallback after markdown/stealth/html fail.
	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ChromePath: "disabled", ExaAPIKey: "k"})
	res, err := p.Scrape(context.Background(), page.URL, 5000)
	if err != nil {
		t.Fatalf("expected Exa fallback to recover content, got: %v", err)
	}
	if res.Content != "recovered by exa neural extraction tier" {
		t.Errorf("content should come from Exa tier, got %q", res.Content)
	}
	if res.Tier != "exa:crawled" {
		t.Errorf("tier should be exa:crawled, got %q", res.Tier)
	}
}

// TestScrapeExaTierAbsentWhenUnconfigured confirms the Exa tier is NOT consulted
// when EXA_API_KEY is empty — the common path never touches the paid API.
func TestScrapeExaTierAbsentWhenUnconfigured(t *testing.T) {
	exaCalled := false
	exa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exaCalled = true
		_, _ = w.Write([]byte(`{"results":[{"url":"x","text":"should not be reached"}]}`))
	}))
	defer exa.Close()
	withExaEndpoint(t, exa.URL)

	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body></body></html>"))
	}))
	defer page.Close()

	// No ExaAPIKey ⇒ tier not appended.
	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ChromePath: "disabled"})
	_, _ = p.Scrape(context.Background(), page.URL, 5000)
	if exaCalled {
		t.Error("Exa endpoint must NOT be called when EXA_API_KEY is unset")
	}
}

func TestStampTier(t *testing.T) {
	r := &ScrapeResult{Content: "x"}
	stampTier(r, "stealth")
	if r.Tier != "stealth" {
		t.Errorf("stampTier should set empty tier, got %q", r.Tier)
	}
	// must not overwrite an already-set tier (e.g. exa:cached)
	stampTier(r, "markdown")
	if r.Tier != "stealth" {
		t.Errorf("stampTier must not overwrite an existing tier, got %q", r.Tier)
	}
	if stampTier(nil, "x") != nil {
		t.Error("stampTier must be nil-safe")
	}
}
