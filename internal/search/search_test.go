package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/config"
)

// =============================================================================
// Provider Factory Tests
// =============================================================================

func newTestDeps(client *http.Client) Deps {
	return Deps{
		HTTPClient: client,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	}
}

func TestNewProvider_Google(t *testing.T) {
	cfg := config.SearchConfig{Provider: "google", GoogleAPIKey: "key", GoogleCX: "cx"}
	p := NewProvider(cfg, newTestDeps(http.DefaultClient))
	if p.Name() != "google" {
		t.Errorf("expected provider name 'google', got %q", p.Name())
	}
}

func TestNewProvider_Brave(t *testing.T) {
	cfg := config.SearchConfig{Provider: "brave", BraveAPIKey: "key"}
	p := NewProvider(cfg, newTestDeps(http.DefaultClient))
	if p.Name() != "brave" {
		t.Errorf("expected provider name 'brave', got %q", p.Name())
	}
}

func TestNewProvider_Serper(t *testing.T) {
	cfg := config.SearchConfig{Provider: "serper", SerperAPIKey: "key"}
	p := NewProvider(cfg, newTestDeps(http.DefaultClient))
	if p.Name() != "serper" {
		t.Errorf("expected provider name 'serper', got %q", p.Name())
	}
}

func TestNewProvider_SearXNG(t *testing.T) {
	cfg := config.SearchConfig{Provider: "searxng", SearXNGURL: "http://localhost:8080"}
	p := NewProvider(cfg, newTestDeps(http.DefaultClient))
	if p.Name() != "searxng" {
		t.Errorf("expected provider name 'searxng', got %q", p.Name())
	}
}

func TestNewProvider_DefaultIsGoogle(t *testing.T) {
	cfg := config.SearchConfig{Provider: "unknown", GoogleAPIKey: "key", GoogleCX: "cx"}
	p := NewProvider(cfg, newTestDeps(http.DefaultClient))
	if p.Name() != "google" {
		t.Errorf("expected default provider 'google', got %q", p.Name())
	}
}

// =============================================================================
// Google Provider Tests
// =============================================================================

func TestGoogleProvider_WebSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify query parameters
		q := r.URL.Query()
		if q.Get("key") != "test-key" {
			t.Errorf("expected key 'test-key', got %q", q.Get("key"))
		}
		if q.Get("cx") != "test-cx" {
			t.Errorf("expected cx 'test-cx', got %q", q.Get("cx"))
		}
		if !strings.Contains(q.Get("q"), "golang testing") {
			t.Errorf("expected query to contain 'golang testing', got %q", q.Get("q"))
		}
		if q.Get("num") != "5" {
			t.Errorf("expected num '5', got %q", q.Get("num"))
		}

		resp := googleResponse{
			Items: []googleItem{
				{Title: "Result One", Link: "https://example.com/1", Snippet: "First result", DisplayLink: "example.com"},
				{Title: "Result Two", Link: "https://example.com/2", Snippet: "Second result", DisplayLink: "example.com"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	// Patch the Google API URL by using a custom HTTP client that rewrites requests
	client := &http.Client{
		Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport},
	}

	deps := Deps{
		HTTPClient: client,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5}),
	}

	g := NewGoogleProvider("test-key", "test-cx", deps)
	results, err := g.Web(context.Background(), WebSearchParams{
		Query:      "golang testing",
		NumResults: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Title != "Result One" {
		t.Errorf("expected first result title 'Result One', got %q", results[0].Title)
	}
	if results[0].URL != "https://example.com/1" {
		t.Errorf("expected first result URL 'https://example.com/1', got %q", results[0].URL)
	}
}

func TestGoogleProvider_WebSearch_WithSiteAndTimeRange(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		query := q.Get("q")
		if !strings.Contains(query, "site:example.com") {
			t.Errorf("expected site: operator in query, got %q", query)
		}
		if q.Get("dateRestrict") != "w1" {
			t.Errorf("expected dateRestrict 'w1', got %q", q.Get("dateRestrict"))
		}
		if q.Get("safe") != "active" {
			t.Errorf("expected safe 'active', got %q", q.Get("safe"))
		}
		if q.Get("lr") != "lang_en" {
			t.Errorf("expected lr 'lang_en', got %q", q.Get("lr"))
		}

		resp := googleResponse{Items: []googleItem{{Title: "R1", Link: "http://example.com", Snippet: "s", DisplayLink: "example.com"}}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	g := NewGoogleProvider("test-key", "test-cx", deps)

	_, err := g.Web(context.Background(), WebSearchParams{
		Query:      "test",
		NumResults: 5,
		Site:       "example.com",
		TimeRange:  "week",
		Safe:       "active",
		Language:   "en",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGoogleProvider_ImageSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("searchType") != "image" {
			t.Errorf("expected searchType 'image', got %q", q.Get("searchType"))
		}

		resp := googleResponse{
			Items: []googleItem{
				{
					Title:       "Image One",
					Link:        "https://example.com/img.png",
					DisplayLink: "example.com",
					Image:       &googleImage{ThumbnailLink: "https://thumb.example.com/img.png", ContextLink: "https://example.com", Width: 800, Height: 600},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	g := NewGoogleProvider("test-key", "test-cx", deps)

	results, err := g.Images(context.Background(), ImageSearchParams{
		Query:      "cats",
		NumResults: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Width != 800 {
		t.Errorf("expected width 800, got %d", results[0].Width)
	}
	if results[0].ThumbnailLink != "https://thumb.example.com/img.png" {
		t.Errorf("unexpected thumbnail link: %q", results[0].ThumbnailLink)
	}
}

func TestGoogleProvider_RateLimited(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	g := NewGoogleProvider("key", "cx", deps)

	_, err := g.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("expected rate limit error, got: %v", err)
	}
}

func TestGoogleProvider_NewsSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		// Default (relevance) must NOT force a date sort — the previous hardcoded
		// sort=date silently overrode the documented relevance default.
		if q.Get("sort") != "" {
			t.Errorf("relevance default should send no sort param, got %q", q.Get("sort"))
		}
		query := q.Get("q")
		if !strings.Contains(query, "site:nytimes.com") {
			t.Errorf("expected source in query, got %q", query)
		}

		resp := googleResponse{
			Items: []googleItem{
				{
					Title: "News Item", Link: "https://news.example.com/1",
					Snippet: "Breaking news", DisplayLink: "news.example.com",
					PageMap: &googlePageMap{MetaTags: []map[string]string{
						{"article:published_time": "2026-05-30T12:00:00Z"},
					}},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	g := NewGoogleProvider("key", "cx", deps)

	results, err := g.News(context.Background(), NewsSearchParams{
		Query:      "technology",
		NumResults: 5,
		Source:     "nytimes.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Source != "news.example.com" {
		t.Errorf("expected source 'news.example.com', got %q", results[0].Source)
	}
	// PublishedAt must be extracted from the pagemap (was previously dropped).
	if results[0].PublishedAt != "2026-05-30T12:00:00Z" {
		t.Errorf("expected PublishedAt from pagemap, got %q", results[0].PublishedAt)
	}
}

// TestGoogleProvider_NewsSearch_DateSort verifies sort_by=date applies Google's
// date sort, but only when explicitly requested.
func TestGoogleProvider_NewsSearch_DateSort(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("sort"); got != "date" {
			t.Errorf("expected sort 'date' when SortBy=date, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(googleResponse{Items: []googleItem{
			{Title: "Recent", Link: "https://e.com/1", Snippet: "x", DisplayLink: "e.com"},
		}})
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	g := NewGoogleProvider("key", "cx", deps)

	if _, err := g.News(context.Background(), NewsSearchParams{Query: "ai", SortBy: "date"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// =============================================================================
// Brave Provider Tests
// =============================================================================

func TestBraveProvider_WebSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Subscription-Token") != "brave-key" {
			t.Errorf("expected subscription token 'brave-key', got %q", r.Header.Get("X-Subscription-Token"))
		}

		resp := braveWebResponse{}
		resp.Web.Results = []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		}{
			{Title: "Brave Result", URL: "https://example.com/brave", Description: "Found via Brave"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("brave-key", deps)

	results, err := b.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "Brave Result" {
		t.Errorf("expected title 'Brave Result', got %q", results[0].Title)
	}
}

func TestBraveProvider_RateLimited(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("key", deps)

	_, err := b.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("expected rate limit error, got: %v", err)
	}
}

// =============================================================================
// Serper Provider Tests
// =============================================================================

func TestSerperProvider_WebSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("X-API-KEY") != "serper-key" {
			t.Errorf("expected API key 'serper-key', got %q", r.Header.Get("X-API-KEY"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected content-type 'application/json', got %q", r.Header.Get("Content-Type"))
		}

		resp := serperWebResponse{
			Organic: []struct {
				Title   string `json:"title"`
				Link    string `json:"link"`
				Snippet string `json:"snippet"`
			}{
				{Title: "Serper Result", Link: "https://example.com/serper", Snippet: "From Serper"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSerperProvider("serper-key", deps)

	results, err := s.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "Serper Result" {
		t.Errorf("expected title 'Serper Result', got %q", results[0].Title)
	}
}

func TestSerperProvider_ImageSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := serperImageResponse{
			Images: []struct {
				Title        string `json:"title"`
				ImageURL     string `json:"imageUrl"`
				ThumbnailURL string `json:"thumbnailUrl"`
				Source       string `json:"source"`
			}{
				{Title: "Image", ImageURL: "https://img.example.com/1.jpg", ThumbnailURL: "https://thumb.example.com/1.jpg", Source: "example.com"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSerperProvider("key", deps)

	results, err := s.Images(context.Background(), ImageSearchParams{Query: "cats", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Link != "https://img.example.com/1.jpg" {
		t.Errorf("unexpected image link: %q", results[0].Link)
	}
}

// =============================================================================
// SearXNG Provider Tests
// =============================================================================

func TestSearXNGProvider_WebSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("format") != "json" {
			t.Errorf("expected format 'json', got %q", q.Get("format"))
		}
		if q.Get("categories") != "general" {
			t.Errorf("expected categories 'general', got %q", q.Get("categories"))
		}

		resp := searxngResponse{
			Results: []searxngResult{
				{Title: "SearXNG Result", URL: "https://example.com/sx", Content: "From SearXNG", Engine: "google"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: ts.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSearXNGProvider(ts.URL, "", nil, deps)

	results, err := s.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "SearXNG Result" {
		t.Errorf("expected title 'SearXNG Result', got %q", results[0].Title)
	}
}

func TestSearXNGProvider_NewsSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("categories") != "news" {
			t.Errorf("expected categories 'news', got %q", q.Get("categories"))
		}
		if q.Get("time_range") != "week" {
			t.Errorf("expected time_range 'week', got %q", q.Get("time_range"))
		}

		resp := searxngResponse{
			Results: []searxngResult{
				{Title: "News", URL: "https://news.example.com/1", Content: "Breaking", Engine: "bing", PublishedDate: "2024-01-15"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: ts.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSearXNGProvider(ts.URL, "", nil, deps)

	results, err := s.News(context.Background(), NewsSearchParams{Query: "tech", NumResults: 5, Freshness: "week"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	// publishedAt is normalized to ISO-8601 (#234) — a bare date becomes midnight UTC.
	if results[0].PublishedAt != "2024-01-15T00:00:00Z" {
		t.Errorf("expected ISO-normalized published date '2024-01-15T00:00:00Z', got %q", results[0].PublishedAt)
	}
}

func TestSearXNGProvider_LimitsResults(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return more results than requested
		resp := searxngResponse{
			Results: []searxngResult{
				{Title: "R1", URL: "http://1.com", Content: "c1"},
				{Title: "R2", URL: "http://2.com", Content: "c2"},
				{Title: "R3", URL: "http://3.com", Content: "c3"},
				{Title: "R4", URL: "http://4.com", Content: "c4"},
				{Title: "R5", URL: "http://5.com", Content: "c5"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: ts.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSearXNGProvider(ts.URL, "", nil, deps)

	results, err := s.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results (capped), got %d", len(results))
	}
}

func TestSearXNGProvider_NoAuthWhenUnset(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("expected no Authorization header, got %q", got)
		}
		if got := r.Header.Get("X-Api-Key"); got != "" {
			t.Errorf("expected no custom header, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(searxngResponse{Results: []searxngResult{{Title: "R", URL: "http://x"}}})
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: ts.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSearXNGProvider(ts.URL, "", nil, deps)
	if _, err := s.Web(context.Background(), WebSearchParams{Query: "q", NumResults: 1}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSearXNGProvider_BasicAuth(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "alice" || p != "secret" {
			t.Errorf("expected basic auth alice/secret, got user=%q pass=%q ok=%v", u, p, ok)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(searxngResponse{Results: []searxngResult{{Title: "R", URL: "http://x"}}})
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: ts.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSearXNGProvider(ts.URL, "alice:secret", nil, deps)
	if _, err := s.Web(context.Background(), WebSearchParams{Query: "q", NumResults: 1}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSearXNGProvider_BasicAuthColonInPassword(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, _ := r.BasicAuth()
		if u != "u" || p != "a:b:c" {
			t.Errorf("expected user=u pass=a:b:c, got user=%q pass=%q", u, p)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(searxngResponse{Results: []searxngResult{{Title: "R", URL: "http://x"}}})
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: ts.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSearXNGProvider(ts.URL, "u:a:b:c", nil, deps)
	if _, err := s.Web(context.Background(), WebSearchParams{Query: "q", NumResults: 1}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSearXNGProvider_CustomHeadersAllPaths proves the headers map is injected
// on Web, Images, AND News — i.e. that all three share the doRequest choke point.
func TestSearXNGProvider_CustomHeadersAllPaths(t *testing.T) {
	headers := map[string]string{"X-Proxy-Token": "abc123", "CF-Access-Client-Id": "client.id"}
	check := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Proxy-Token") != "abc123" || r.Header.Get("CF-Access-Client-Id") != "client.id" {
			t.Errorf("missing custom headers on %s: %v", r.URL.Query().Get("categories"), r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(searxngResponse{Results: []searxngResult{{Title: "R", URL: "http://x", ImgSrc: "http://img"}}})
	}
	ts := httptest.NewServer(http.HandlerFunc(check))
	defer ts.Close()

	deps := Deps{HTTPClient: ts.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSearXNGProvider(ts.URL, "", headers, deps)
	if _, err := s.Web(context.Background(), WebSearchParams{Query: "q", NumResults: 1}); err != nil {
		t.Fatalf("Web error: %v", err)
	}
	if _, err := s.Images(context.Background(), ImageSearchParams{Query: "q", NumResults: 1}); err != nil {
		t.Fatalf("Images error: %v", err)
	}
	if _, err := s.News(context.Background(), NewsSearchParams{Query: "q", NumResults: 1}); err != nil {
		t.Fatalf("News error: %v", err)
	}
}

// TestSearXNGProvider_HeadersOverrideBasicAuth documents last-writer-wins: a
// custom Authorization header in SEARXNG_HEADERS overrides Basic auth.
func TestSearXNGProvider_HeadersOverrideBasicAuth(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer t0ken" {
			t.Errorf("expected custom bearer to win, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(searxngResponse{Results: []searxngResult{{Title: "R", URL: "http://x"}}})
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: ts.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSearXNGProvider(ts.URL, "alice:secret", map[string]string{"Authorization": "Bearer t0ken"}, deps)
	if _, err := s.Web(context.Background(), WebSearchParams{Query: "q", NumResults: 1}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSearXNGProvider_HalfFormedBasicAuthNotSent guards the wire-safety
// invariant: a half-formed credential that config flagged but (in STDIO mode)
// did not abort startup must still never reach the server.
func TestSearXNGProvider_HalfFormedBasicAuthNotSent(t *testing.T) {
	for _, bad := range []string{"user:", ":pass", "nocolon"} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "" {
				t.Errorf("half-formed basicAuth %q must not be sent, got %q", bad, got)
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(searxngResponse{Results: []searxngResult{{Title: "R", URL: "http://x"}}})
		}))
		deps := Deps{HTTPClient: ts.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
		s := NewSearXNGProvider(ts.URL, bad, nil, deps)
		if _, err := s.Web(context.Background(), WebSearchParams{Query: "q", NumResults: 1}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		ts.Close()
	}
}

func TestNewProvider_SearXNGThreadsAuth(t *testing.T) {
	cfg := config.SearchConfig{
		Provider:         "searxng",
		SearXNGURL:       "http://localhost:8080",
		SearXNGBasicAuth: "alice:secret",
		SearXNGHeaders:   map[string]string{"X-Api-Key": "abc"},
	}
	p := NewProvider(cfg, Deps{Breaker: circuit.New(circuit.Config{FailureThreshold: 5})})
	sx, ok := p.(*SearXNGProvider)
	if !ok {
		t.Fatalf("expected *SearXNGProvider, got %T", p)
	}
	if sx.basicAuth != "alice:secret" || sx.headers["X-Api-Key"] != "abc" {
		t.Errorf("auth not threaded to provider: basicAuth=%q headers=%v", sx.basicAuth, sx.headers)
	}
}

// =============================================================================
// Lens Registry Tests
// =============================================================================

func TestLensRegistry_LoadFromDir(t *testing.T) {
	dir := t.TempDir()

	// Create test lens files
	techLens := Lens{
		Name:        "tech",
		Description: "Technology sites",
		Domains:     []string{"arstechnica.com", "techcrunch.com", "theverge.com"},
	}
	techData, _ := json.Marshal(techLens)
	os.WriteFile(filepath.Join(dir, "tech.json"), techData, 0644)

	sciLens := Lens{
		Name:        "science",
		Description: "Science sites",
		Domains:     []string{"nature.com", "sciencemag.org"},
	}
	sciData, _ := json.Marshal(sciLens)
	os.WriteFile(filepath.Join(dir, "science.json"), sciData, 0644)

	// Create a non-JSON file that should be ignored
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("ignore me"), 0644)

	// Create a subdirectory that should be ignored
	os.MkdirAll(filepath.Join(dir, "subdir"), 0755)

	registry := &LensRegistry{lenses: make(map[string]*Lens)}
	err := registry.LoadFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFromDir error: %v", err)
	}

	names := registry.List()
	if len(names) != 2 {
		t.Errorf("expected 2 lenses loaded, got %d", len(names))
	}

	lens, ok := registry.Get("tech")
	if !ok {
		t.Fatal("expected 'tech' lens to exist")
	}
	if lens.Description != "Technology sites" {
		t.Errorf("expected description 'Technology sites', got %q", lens.Description)
	}
	if len(lens.Domains) != 3 {
		t.Errorf("expected 3 domains, got %d", len(lens.Domains))
	}
}

func TestLensRegistry_LoadFromDir_UsesFilenameIfNoName(t *testing.T) {
	dir := t.TempDir()

	// Lens without a name field
	data := []byte(`{"description": "No name", "domains": ["example.com"]}`)
	os.WriteFile(filepath.Join(dir, "custom.json"), data, 0644)

	registry := &LensRegistry{lenses: make(map[string]*Lens)}
	err := registry.LoadFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFromDir error: %v", err)
	}

	_, ok := registry.Get("custom")
	if !ok {
		t.Error("expected lens to use filename 'custom' when name field is empty")
	}
}

func TestLensRegistry_LoadFromDir_NonexistentDir(t *testing.T) {
	registry := &LensRegistry{lenses: make(map[string]*Lens)}
	err := registry.LoadFromDir("/nonexistent/path/to/lenses")
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestLensRegistry_Get_NotFound(t *testing.T) {
	registry := &LensRegistry{lenses: make(map[string]*Lens)}
	_, ok := registry.Get("nonexistent")
	if ok {
		t.Error("expected Get to return false for nonexistent lens")
	}
}

func TestLensRegistry_BuildSiteQuery(t *testing.T) {
	registry := &LensRegistry{lenses: make(map[string]*Lens)}
	lens := &Lens{
		Name:    "test",
		Domains: []string{"example.com", "test.org", "docs.io"},
	}

	result := registry.BuildSiteQuery("golang", lens)
	if !strings.Contains(result, "golang") {
		t.Error("expected query to contain original search term")
	}
	if !strings.Contains(result, "site:example.com") {
		t.Error("expected query to contain site:example.com")
	}
	if !strings.Contains(result, "site:test.org") {
		t.Error("expected query to contain site:test.org")
	}
	if !strings.Contains(result, " OR ") {
		t.Error("expected query to contain OR operators")
	}
}

func TestLensRegistry_BuildSiteQuery_EmptyDomains(t *testing.T) {
	registry := &LensRegistry{lenses: make(map[string]*Lens)}
	lens := &Lens{Name: "empty", Domains: nil}

	result := registry.BuildSiteQuery("golang", lens)
	if result != "golang" {
		t.Errorf("expected unchanged query 'golang', got %q", result)
	}
}

func TestLensRegistry_BuildSiteQuery_MaxDomains(t *testing.T) {
	domains := make([]string, 15)
	for i := range domains {
		domains[i] = "domain" + strings.Repeat("x", i) + ".com"
	}

	registry := &LensRegistry{lenses: make(map[string]*Lens)}
	lens := &Lens{Name: "big", Domains: domains}

	result := registry.BuildSiteQuery("test", lens)
	// Should only include up to 10 domains
	siteCount := strings.Count(result, "site:")
	if siteCount != 10 {
		t.Errorf("expected 10 site: operators (max), got %d", siteCount)
	}
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestBuildQuery(t *testing.T) {
	tests := []struct {
		params   WebSearchParams
		expected string
	}{
		{WebSearchParams{Query: "hello"}, "hello"},
		{WebSearchParams{Query: "hello", Site: "example.com"}, "hello site:example.com"},
	}
	for _, tt := range tests {
		got := buildQuery(tt.params)
		if got != tt.expected {
			t.Errorf("buildQuery(%+v) = %q, want %q", tt.params, got, tt.expected)
		}
	}
}

func TestMapTimeRange(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hour", "d1"},
		{"day", "d1"},
		{"week", "w1"},
		{"month", "m1"},
		{"year", "y1"},
		{"invalid", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := mapTimeRange(tt.input)
		if got != tt.expected {
			t.Errorf("mapTimeRange(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestClamp(t *testing.T) {
	tests := []struct {
		val, min, max, expected int
	}{
		{5, 1, 10, 5},
		{0, 1, 10, 1},
		{15, 1, 10, 10},
		{1, 1, 10, 1},
		{10, 1, 10, 10},
	}
	for _, tt := range tests {
		got := clamp(tt.val, tt.min, tt.max)
		if got != tt.expected {
			t.Errorf("clamp(%d, %d, %d) = %d, want %d", tt.val, tt.min, tt.max, got, tt.expected)
		}
	}
}

func TestMapBraveFreshness(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hour", "pd"},
		{"day", "pd"},
		{"week", "pw"},
		{"month", "pm"},
		{"year", "py"},
		{"invalid", ""},
	}
	for _, tt := range tests {
		got := mapBraveFreshness(tt.input)
		if got != tt.expected {
			t.Errorf("mapBraveFreshness(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestMapSearXNGTimeRange(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hour", "day"},
		{"day", "day"},
		{"week", "week"},
		{"month", "month"},
		{"year", "year"},
		{"unknown", ""},
	}
	for _, tt := range tests {
		got := mapSearXNGTimeRange(tt.input)
		if got != tt.expected {
			t.Errorf("mapSearXNGTimeRange(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// =============================================================================
// SearchAPI Provider Tests
// =============================================================================

func TestSearchAPIProvider_WebSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("api_key") != "searchapi-key" {
			t.Errorf("expected api_key 'searchapi-key', got %q", q.Get("api_key"))
		}
		if q.Get("engine") != "google" {
			t.Errorf("expected engine 'google', got %q", q.Get("engine"))
		}
		if !strings.Contains(q.Get("q"), "test query") {
			t.Errorf("expected query to contain 'test query', got %q", q.Get("q"))
		}
		if q.Get("num") != "5" {
			t.Errorf("expected num '5', got %q", q.Get("num"))
		}

		resp := searchAPIWebResponse{
			OrganicResults: []searchAPIOrganicResult{
				{Position: 1, Title: "SearchAPI Result", Link: "https://example.com/searchapi", Snippet: "Found via SearchAPI", DisplayedLink: "example.com"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: ts.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSearchAPIProvider("searchapi-key", deps)
	s.baseURL = ts.URL

	results, err := s.Web(context.Background(), WebSearchParams{Query: "test query", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "SearchAPI Result" {
		t.Errorf("expected title 'SearchAPI Result', got %q", results[0].Title)
	}
	if results[0].URL != "https://example.com/searchapi" {
		t.Errorf("expected URL 'https://example.com/searchapi', got %q", results[0].URL)
	}
}

func TestSearchAPIProvider_WebSearch_WithParams(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("gl") != "US" {
			t.Errorf("expected gl 'US', got %q", q.Get("gl"))
		}
		if q.Get("hl") != "en" {
			t.Errorf("expected hl 'en', got %q", q.Get("hl"))
		}
		if q.Get("safe") != "active" {
			t.Errorf("expected safe 'active', got %q", q.Get("safe"))
		}
		if q.Get("time_period") != "last_week" {
			t.Errorf("expected time_period 'last_week', got %q", q.Get("time_period"))
		}

		resp := searchAPIWebResponse{OrganicResults: []searchAPIOrganicResult{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: ts.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSearchAPIProvider("key", deps)
	s.baseURL = ts.URL

	_, err := s.Web(context.Background(), WebSearchParams{
		Query:      "test",
		NumResults: 5,
		Country:    "US",
		Language:   "en",
		Safe:       "high",
		TimeRange:  "week",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSearchAPIProvider_ImageSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("engine") != "google_images" {
			t.Errorf("expected engine 'google_images', got %q", q.Get("engine"))
		}

		resp := searchAPIImageResponse{
			Images: []searchAPIImageResult{
				{Title: "Cat Image", Original: "https://img.example.com/cat.jpg", Thumbnail: "https://thumb.example.com/cat.jpg", Source: "example.com", OriginalWidth: 1920, OriginalHeight: 1080},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: ts.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSearchAPIProvider("key", deps)
	s.baseURL = ts.URL

	results, err := s.Images(context.Background(), ImageSearchParams{Query: "cats", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Link != "https://img.example.com/cat.jpg" {
		t.Errorf("unexpected image link: %q", results[0].Link)
	}
	if results[0].Width != 1920 || results[0].Height != 1080 {
		t.Errorf("unexpected dimensions: %dx%d", results[0].Width, results[0].Height)
	}
}

func TestSearchAPIProvider_NewsSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("engine") != "google_news" {
			t.Errorf("expected engine 'google_news', got %q", q.Get("engine"))
		}
		if q.Get("time_period") != "last_day" {
			t.Errorf("expected time_period 'last_day', got %q", q.Get("time_period"))
		}

		resp := searchAPINewsResponse{
			NewsResults: []searchAPINewsResult{
				{Title: "Breaking News", Link: "https://news.example.com/1", Source: "Example News", Date: "2 hours ago", Snippet: "Something happened"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: ts.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSearchAPIProvider("key", deps)
	s.baseURL = ts.URL

	results, err := s.News(context.Background(), NewsSearchParams{Query: "tech", NumResults: 5, Freshness: "day"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Source != "Example News" {
		t.Errorf("expected source 'Example News', got %q", results[0].Source)
	}
}

func TestSearchAPIProvider_RateLimited(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: ts.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSearchAPIProvider("key", deps)
	s.baseURL = ts.URL

	_, err := s.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("expected rate limit error, got: %v", err)
	}
}

func TestSearchAPIProvider_AuthFailed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: ts.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSearchAPIProvider("bad-key", deps)
	s.baseURL = ts.URL

	_, err := s.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err == nil {
		t.Fatal("expected auth error")
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("expected auth error, got: %v", err)
	}
}

func TestNewProvider_SearchAPI(t *testing.T) {
	cfg := config.SearchConfig{Provider: "searchapi", SearchAPIKey: "key"}
	p := NewProvider(cfg, newTestDeps(http.DefaultClient))
	if p.Name() != "searchapi" {
		t.Errorf("expected provider name 'searchapi', got %q", p.Name())
	}
}

func TestSearchAPIProvider_PatentSearch(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("engine") != "google_patents" {
			t.Errorf("expected engine=google_patents, got %s", r.URL.Query().Get("engine"))
		}
		resp := searchAPIPatentResponse{
			OrganicResults: []searchAPIPatentResult{
				{
					Title:      "<b>Kaltura</b> Video Platform",
					PatentID:   "patent/US10165245B2/en",
					Link:       "https://patents.google.com/patent/US10165245B2/en",
					Snippet:    "A system for <b>pre-fetching</b> video content",
					Assignee:   "<b>Kaltura</b>, Inc.",
					Inventor:   "Christopher Hayes",
					FilingDate: "2013-07-03",
					GrantDate:  "2018-12-25",
					PDF:        "https://patentimages.storage.googleapis.com/US10165245.pdf",
				},
				{
					Title:             "Image Compression",
					PublicationNumber: "US8774534B2",
					Snippet:           "Method for image compression",
					Assignee:          "Watchitoo, Inc.",
					Inventor:          "Rony Zarom",
					FilingDate:        "2010-04-08",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: ts.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	s := NewSearchAPIProvider("key", deps)
	s.SetBaseURL(ts.URL)

	results, err := s.Patents(context.Background(), PatentSearchParams{
		Assignee:   "Kaltura",
		NumResults: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Verify HTML stripping
	if results[0].Title != "Kaltura Video Platform" {
		t.Errorf("expected HTML-stripped title, got %q", results[0].Title)
	}
	if results[0].Assignee != "Kaltura, Inc." {
		t.Errorf("expected HTML-stripped assignee, got %q", results[0].Assignee)
	}
	// Verify patent number extraction from path
	if results[0].Number != "US10165245B2" {
		t.Errorf("expected extracted patent number US10165245B2, got %q", results[0].Number)
	}
	// Verify URL is used directly from link field
	if results[0].URL != "https://patents.google.com/patent/US10165245B2/en" {
		t.Errorf("unexpected URL: %s", results[0].URL)
	}
	if results[0].PDF != "https://patentimages.storage.googleapis.com/US10165245.pdf" {
		t.Errorf("unexpected PDF: %s", results[0].PDF)
	}
	// Verify fallback to publication_number
	if results[1].Number != "US8774534B2" {
		t.Errorf("expected publication_number fallback, got %q", results[1].Number)
	}
}

func TestExtractPatentNumber(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"patent/US9270715B2/en", "US9270715B2"},
		{"patent/US20140149867A1/en", "US20140149867A1"},
		{"patent/HK1202995B/en", "HK1202995B"},
		{"patent/EP1234567A1/en", "EP1234567A1"},
		{"US10165245B2", "US10165245B2"},
		{"", ""},
		{"  patent/CN123456B/en  ", "CN123456B"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractPatentNumber(tt.input)
			if got != tt.want {
				t.Errorf("extractPatentNumber(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripHTMLTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"<b>Kaltura</b>, Inc.", "Kaltura, Inc."},
		{"No tags here", "No tags here"},
		{"<em>multiple</em> <strong>tags</strong>", "multiple tags"},
		{"", ""},
		{"plain text", "plain text"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := stripHTMLTags(tt.input)
			if got != tt.want {
				t.Errorf("stripHTMLTags(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Router Tests
// =============================================================================

type failingProvider struct {
	name string
}

func (f *failingProvider) Web(_ context.Context, _ WebSearchParams) ([]SearchResult, error) {
	return nil, fmt.Errorf("%s: web search unavailable", f.name)
}
func (f *failingProvider) Images(_ context.Context, _ ImageSearchParams) ([]ImageResult, error) {
	return nil, fmt.Errorf("%s: image search unavailable", f.name)
}
func (f *failingProvider) News(_ context.Context, _ NewsSearchParams) ([]NewsResult, error) {
	return nil, fmt.Errorf("%s: news search unavailable", f.name)
}
func (f *failingProvider) Name() string { return f.name }

type successProvider struct {
	name string
}

func (s *successProvider) Web(_ context.Context, _ WebSearchParams) ([]SearchResult, error) {
	return []SearchResult{{Title: s.name + " result", URL: "https://" + s.name + ".com"}}, nil
}
func (s *successProvider) Images(_ context.Context, _ ImageSearchParams) ([]ImageResult, error) {
	return []ImageResult{{Title: s.name + " image", Link: "https://" + s.name + ".com/img.png"}}, nil
}
func (s *successProvider) News(_ context.Context, _ NewsSearchParams) ([]NewsResult, error) {
	return []NewsResult{{Title: s.name + " news", URL: "https://" + s.name + ".com/news"}}, nil
}
func (s *successProvider) Name() string { return s.name }

func TestRouter_UsesFirstAvailableProvider(t *testing.T) {
	providers := map[string]Provider{
		"primary":   &successProvider{name: "primary"},
		"secondary": &successProvider{name: "secondary"},
	}
	r := NewRouter(providers, RouterConfig{
		Routing: RoutingConfig{Default: []string{"primary", "secondary"}},
	})

	results, err := r.Web(context.Background(), WebSearchParams{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results[0].Title != "primary result" {
		t.Errorf("expected primary result, got %q", results[0].Title)
	}
}

func TestRouter_FallsBackOnFailure(t *testing.T) {
	providers := map[string]Provider{
		"failing":   &failingProvider{name: "failing"},
		"secondary": &successProvider{name: "secondary"},
	}
	r := NewRouter(providers, RouterConfig{
		Routing: RoutingConfig{Default: []string{"failing", "secondary"}},
	})

	results, err := r.Web(context.Background(), WebSearchParams{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results[0].Title != "secondary result" {
		t.Errorf("expected secondary result after fallback, got %q", results[0].Title)
	}
}

func TestRouter_PerOperationRouting(t *testing.T) {
	providers := map[string]Provider{
		"brave":  &successProvider{name: "brave"},
		"google": &successProvider{name: "google"},
	}
	r := NewRouter(providers, RouterConfig{
		Routing: RoutingConfig{
			Web:    []string{"brave", "google"},
			Images: []string{"google", "brave"},
		},
	})

	webResults, err := r.Web(context.Background(), WebSearchParams{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if webResults[0].Title != "brave result" {
		t.Errorf("expected brave for web, got %q", webResults[0].Title)
	}

	imgResults, err := r.Images(context.Background(), ImageSearchParams{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if imgResults[0].Title != "google image" {
		t.Errorf("expected google for images, got %q", imgResults[0].Title)
	}
}

func TestRouter_FallsBackToDefault(t *testing.T) {
	providers := map[string]Provider{
		"brave":  &successProvider{name: "brave"},
		"google": &successProvider{name: "google"},
	}
	r := NewRouter(providers, RouterConfig{
		Routing: RoutingConfig{
			Default: []string{"google", "brave"},
		},
	})

	results, err := r.News(context.Background(), NewsSearchParams{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results[0].Title != "google news" {
		t.Errorf("expected google for news (via default), got %q", results[0].Title)
	}
}

func TestRouter_AllProvidersFail(t *testing.T) {
	providers := map[string]Provider{
		"a": &failingProvider{name: "a"},
		"b": &failingProvider{name: "b"},
	}
	r := NewRouter(providers, RouterConfig{
		Routing: RoutingConfig{Default: []string{"a", "b"}},
	})

	_, err := r.Web(context.Background(), WebSearchParams{Query: "test"})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

func TestRouter_NotifiesOnFallback(t *testing.T) {
	var notifications []string
	providers := map[string]Provider{
		"failing":   &failingProvider{name: "failing"},
		"secondary": &successProvider{name: "secondary"},
	}
	r := NewRouter(providers, RouterConfig{
		Routing: RoutingConfig{Default: []string{"failing", "secondary"}},
		Notifier: func(op Operation, from, to, reason string) {
			notifications = append(notifications, fmt.Sprintf("%s: %s->%s (%s)", op, from, to, reason))
		},
	})

	_, err := r.Web(context.Background(), WebSearchParams{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d: %v", len(notifications), notifications)
	}
	if !strings.Contains(notifications[0], "failing->secondary") {
		t.Errorf("unexpected notification: %s", notifications[0])
	}
}

func TestRouter_ProviderFor(t *testing.T) {
	providers := map[string]Provider{
		"searchapi": &successProvider{name: "searchapi"},
		"google":    &successProvider{name: "google"},
	}
	r := NewRouter(providers, RouterConfig{
		Routing: RoutingConfig{
			Academic: []string{"searchapi", "google"},
		},
	})

	p, name := r.ProviderFor(OpAcademic)
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	if name != "searchapi" {
		t.Errorf("expected 'searchapi', got %q", name)
	}
}

func TestRouter_Name(t *testing.T) {
	r := NewRouter(map[string]Provider{}, RouterConfig{})
	if r.Name() != "router" {
		t.Errorf("expected name 'router', got %q", r.Name())
	}
}

func TestRouter_SkipsUnknownProviders(t *testing.T) {
	providers := map[string]Provider{
		"google": &successProvider{name: "google"},
	}
	r := NewRouter(providers, RouterConfig{
		Routing: RoutingConfig{Default: []string{"nonexistent", "google"}},
	})

	results, err := r.Web(context.Background(), WebSearchParams{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results[0].Title != "google result" {
		t.Errorf("expected google result after skipping unknown, got %q", results[0].Title)
	}
}

func TestRouter_NoProviders(t *testing.T) {
	r := NewRouter(map[string]Provider{}, RouterConfig{})

	_, err := r.Web(context.Background(), WebSearchParams{Query: "test"})
	if err == nil {
		t.Fatal("expected error with no providers")
	}
	if !strings.Contains(err.Error(), "no providers available") {
		t.Errorf("unexpected error: %v", err)
	}
}

// =============================================================================
// Router Patent Search Tests
// =============================================================================

type mockPatentProvider struct {
	name    string
	meta    ProviderMeta
	results []PatentResult
	err     error
}

func (m *mockPatentProvider) Name() string           { return m.name }
func (m *mockPatentProvider) Metadata() ProviderMeta { return m.meta }
func (m *mockPatentProvider) Patents(_ context.Context, _ PatentSearchParams) ([]PatentResult, error) {
	return m.results, m.err
}

func TestRouter_PatentsUsesPatentProviders(t *testing.T) {
	epo := &mockPatentProvider{
		name: "epo",
		meta: ProviderMeta{Regions: []string{"*"}},
		results: []PatentResult{
			{Title: "EPO Patent", Number: "EP1234567"},
		},
	}
	router := NewRouter(map[string]Provider{}, RouterConfig{
		Routing:         RoutingConfig{Patents: []string{"epo"}},
		PatentProviders: map[string]PatentProvider{"epo": epo},
	})

	results, err := router.Patents(context.Background(), PatentSearchParams{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Title != "EPO Patent" {
		t.Errorf("unexpected results: %v", results)
	}
}

func TestRouter_PatentsRegionFiltering(t *testing.T) {
	usOnly := &mockPatentProvider{
		name:    "uspto",
		meta:    ProviderMeta{Regions: []string{"US"}},
		results: []PatentResult{{Title: "US Patent", Number: "US123"}},
	}
	worldwide := &mockPatentProvider{
		name:    "epo",
		meta:    ProviderMeta{Regions: []string{"*"}},
		results: []PatentResult{{Title: "EPO Patent", Number: "EP456"}},
	}

	router := NewRouter(map[string]Provider{}, RouterConfig{
		Routing:         RoutingConfig{Patents: []string{"uspto", "epo"}},
		PatentProviders: map[string]PatentProvider{"uspto": usOnly, "epo": worldwide},
	})

	// Searching for EP patents should skip USPTO
	results, err := router.Patents(context.Background(), PatentSearchParams{
		Query:        "video",
		PatentOffice: "EP",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Number != "EP456" {
		t.Errorf("expected EPO result (USPTO should be skipped for EP), got: %v", results)
	}
}

func TestRouter_PatentsFallbackOnError(t *testing.T) {
	failing := &mockPatentProvider{
		name: "epo",
		meta: ProviderMeta{Regions: []string{"*"}},
		err:  fmt.Errorf("epo: rate limited"),
	}
	fallback := &mockPatentProvider{
		name:    "lens",
		meta:    ProviderMeta{Regions: []string{"*"}},
		results: []PatentResult{{Title: "Lens Result", Number: "US789"}},
	}

	router := NewRouter(map[string]Provider{}, RouterConfig{
		Routing:         RoutingConfig{Patents: []string{"epo", "lens"}},
		PatentProviders: map[string]PatentProvider{"epo": failing, "lens": fallback},
	})

	results, err := router.Patents(context.Background(), PatentSearchParams{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Title != "Lens Result" {
		t.Errorf("expected fallback to lens, got: %v", results)
	}
}

func TestRouter_PatentsNoProviders(t *testing.T) {
	router := NewRouter(map[string]Provider{}, RouterConfig{})

	_, err := router.Patents(context.Background(), PatentSearchParams{Query: "test"})
	if err == nil {
		t.Fatal("expected error with no patent providers")
	}
	if !strings.Contains(err.Error(), "no providers available") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRouter_PatentProviderByName(t *testing.T) {
	epo := &mockPatentProvider{
		name: "epo",
		meta: ProviderMeta{Regions: []string{"*"}},
	}
	router := NewRouter(map[string]Provider{}, RouterConfig{
		PatentProviders: map[string]PatentProvider{"epo": epo},
	})

	ps, found := router.PatentProviderByName("epo")
	if !found || ps == nil {
		t.Fatal("expected to find epo patent provider")
	}

	_, found = router.PatentProviderByName("nonexistent")
	if found {
		t.Error("expected not to find nonexistent provider")
	}
}

func TestRouter_PatentsMixesFullAndPatentOnlyProviders(t *testing.T) {
	// SearchAPI is a full provider that also implements PatentSearcher
	searchapi := &mockPatentFullProvider{
		successProvider: successProvider{name: "searchapi"},
		results:         []PatentResult{{Title: "SearchAPI Patent", Number: "US111"}},
	}

	epo := &mockPatentProvider{
		name:    "epo",
		meta:    ProviderMeta{Regions: []string{"*"}},
		results: []PatentResult{{Title: "EPO Patent", Number: "EP222"}},
	}

	router := NewRouter(
		map[string]Provider{"searchapi": searchapi},
		RouterConfig{
			Routing:         RoutingConfig{Patents: []string{"searchapi", "epo"}},
			PatentProviders: map[string]PatentProvider{"epo": epo},
		},
	)

	results, err := router.Patents(context.Background(), PatentSearchParams{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// SearchAPI is first in priority and healthy → should get its results
	if len(results) != 1 || results[0].Title != "SearchAPI Patent" {
		t.Errorf("expected SearchAPI result (first in priority), got: %v", results)
	}
}

// mockPatentFullProvider implements both Provider and PatentSearcher
type mockPatentFullProvider struct {
	successProvider
	results []PatentResult
}

func (m *mockPatentFullProvider) Patents(_ context.Context, _ PatentSearchParams) ([]PatentResult, error) {
	return m.results, nil
}

// =============================================================================
// Router Academic Provider Tests
// =============================================================================

func TestRouter_ScholarlyUsesAcademicProviders(t *testing.T) {
	t.Parallel()

	openalex := &mockAcademicProvider{
		name: "openalex",
		results: []AcademicResult{
			{Title: "Attention Is All You Need", DOI: "10.48550/arXiv.1706.03762", Source: "openalex"},
		},
	}

	router := NewRouter(
		map[string]Provider{"brave": &successProvider{name: "brave"}},
		RouterConfig{
			AcademicProviders: map[string]AcademicProvider{"openalex": openalex},
		},
	)

	results, err := router.Scholarly(context.Background(), AcademicSearchParams{Query: "transformers"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Title != "Attention Is All You Need" {
		t.Errorf("unexpected results: %v", results)
	}
}

func TestRouter_ScholarlyFallbackOnError(t *testing.T) {
	t.Parallel()

	failing := &mockAcademicProvider{
		name: "openalex",
		err:  fmt.Errorf("openalex: rate limited"),
	}
	crossref := &mockAcademicProvider{
		name:    "crossref",
		results: []AcademicResult{{Title: "CrossRef Result", Source: "crossref"}},
	}

	router := NewRouter(
		map[string]Provider{"brave": &successProvider{name: "brave"}},
		RouterConfig{
			Routing:           RoutingConfig{Academic: []string{"openalex", "crossref"}},
			AcademicProviders: map[string]AcademicProvider{"openalex": failing, "crossref": crossref},
		},
	)

	results, err := router.Scholarly(context.Background(), AcademicSearchParams{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Title != "CrossRef Result" {
		t.Errorf("expected CrossRef fallback result, got: %v", results)
	}
}

func TestRouter_ScholarlyNoProviders(t *testing.T) {
	t.Parallel()

	router := NewRouter(
		map[string]Provider{"brave": &successProvider{name: "brave"}},
		RouterConfig{},
	)

	_, err := router.Scholarly(context.Background(), AcademicSearchParams{Query: "test"})
	if err == nil {
		t.Fatal("expected error when no academic providers configured")
	}
	if !strings.Contains(err.Error(), "no providers available") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRouter_AcademicProviderByName(t *testing.T) {
	t.Parallel()

	openalex := &mockAcademicProvider{name: "openalex"}
	router := NewRouter(
		map[string]Provider{"brave": &successProvider{name: "brave"}},
		RouterConfig{
			AcademicProviders: map[string]AcademicProvider{"openalex": openalex},
		},
	)

	ap, found := router.AcademicProviderByName("openalex")
	if !found || ap == nil {
		t.Error("expected to find openalex academic provider")
	}

	_, found = router.AcademicProviderByName("crossref")
	if found {
		t.Error("expected crossref to not be found")
	}
}

func TestRouter_RegisterAcademicProviders(t *testing.T) {
	t.Parallel()

	router := NewRouter(
		map[string]Provider{"brave": &successProvider{name: "brave"}},
		RouterConfig{},
	)

	crossref := &mockAcademicProvider{
		name:    "crossref",
		results: []AcademicResult{{Title: "Late Addition", Source: "crossref"}},
	}
	router.RegisterAcademicProviders(map[string]AcademicProvider{"crossref": crossref})

	results, err := router.Scholarly(context.Background(), AcademicSearchParams{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Source != "crossref" {
		t.Errorf("expected crossref result after registration, got: %v", results)
	}
}

type mockAcademicProvider struct {
	name    string
	results []AcademicResult
	err     error
}

func (m *mockAcademicProvider) Name() string { return m.name }
func (m *mockAcademicProvider) Metadata() ProviderMeta {
	return ProviderMeta{Regions: []string{"*"}, Capabilities: []string{"search"}, RateClass: "free"}
}
func (m *mockAcademicProvider) Scholarly(_ context.Context, _ AcademicSearchParams) ([]AcademicResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

// =============================================================================
// ParseRoutingConfig Tests
// =============================================================================

func TestParseRoutingConfig_SimpleList(t *testing.T) {
	cfg, err := ParseRoutingConfig("brave,google,serper")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Default) != 3 {
		t.Fatalf("expected 3 providers in default, got %d", len(cfg.Default))
	}
	if cfg.Default[0] != "brave" || cfg.Default[1] != "google" || cfg.Default[2] != "serper" {
		t.Errorf("unexpected default: %v", cfg.Default)
	}
}

func TestParseRoutingConfig_JSON(t *testing.T) {
	input := `{"web":"brave,google","news":"brave,serper","images":"google,brave","academic":"searchapi,google","default":"brave,google,serper"}`
	cfg, err := ParseRoutingConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Web) != 2 || cfg.Web[0] != "brave" {
		t.Errorf("unexpected web routing: %v", cfg.Web)
	}
	if len(cfg.News) != 2 || cfg.News[0] != "brave" {
		t.Errorf("unexpected news routing: %v", cfg.News)
	}
	if len(cfg.Images) != 2 || cfg.Images[0] != "google" {
		t.Errorf("unexpected images routing: %v", cfg.Images)
	}
	if len(cfg.Academic) != 2 || cfg.Academic[0] != "searchapi" {
		t.Errorf("unexpected academic routing: %v", cfg.Academic)
	}
	if len(cfg.Default) != 3 {
		t.Errorf("unexpected default routing: %v", cfg.Default)
	}
}

func TestParseRoutingConfig_Empty(t *testing.T) {
	cfg, err := ParseRoutingConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Default) != 0 {
		t.Errorf("expected empty config for empty input, got: %v", cfg)
	}
}

func TestParseRoutingConfig_InvalidJSON(t *testing.T) {
	_, err := ParseRoutingConfig("{invalid json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseRoutingConfig_SpacesHandled(t *testing.T) {
	cfg, err := ParseRoutingConfig(" brave , google , serper ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Default) != 3 {
		t.Fatalf("expected 3, got %d", len(cfg.Default))
	}
	if cfg.Default[0] != "brave" || cfg.Default[1] != "google" || cfg.Default[2] != "serper" {
		t.Errorf("spaces not trimmed: %v", cfg.Default)
	}
}

// =============================================================================
// AvailableProviders Tests
// =============================================================================

func TestAvailableProviders(t *testing.T) {
	cfg := config.SearchConfig{
		GoogleAPIKey: "gkey",
		GoogleCX:     "gcx",
		BraveAPIKey:  "bkey",
		SearchAPIKey: "skey",
		TavilyAPIKey: "tkey",
	}
	deps := newTestDeps(http.DefaultClient)
	providers := AvailableProviders(cfg, deps)

	if _, ok := providers["google"]; !ok {
		t.Error("expected google provider")
	}
	if _, ok := providers["brave"]; !ok {
		t.Error("expected brave provider")
	}
	if _, ok := providers["searchapi"]; !ok {
		t.Error("expected searchapi provider")
	}
	if _, ok := providers["tavily"]; !ok {
		t.Error("expected tavily provider")
	}
	if _, ok := providers["serper"]; ok {
		t.Error("did not expect serper provider (no key)")
	}
	if _, ok := providers["searxng"]; ok {
		t.Error("did not expect searxng provider (no URL)")
	}
}

func TestNewProviderByName_MissingCredentials(t *testing.T) {
	cfg := config.SearchConfig{}
	deps := newTestDeps(http.DefaultClient)

	if p := NewProviderByName("brave", cfg, deps); p != nil {
		t.Error("expected nil for brave without key")
	}
	if p := NewProviderByName("serper", cfg, deps); p != nil {
		t.Error("expected nil for serper without key")
	}
	if p := NewProviderByName("searchapi", cfg, deps); p != nil {
		t.Error("expected nil for searchapi without key")
	}
	if p := NewProviderByName("searxng", cfg, deps); p != nil {
		t.Error("expected nil for searxng without URL")
	}
	if p := NewProviderByName("google", cfg, deps); p != nil {
		t.Error("expected nil for google without both key and cx")
	}
	if p := NewProviderByName("tavily", cfg, deps); p != nil {
		t.Error("expected nil for tavily without key")
	}
	if p := NewProviderByName("tavily", config.SearchConfig{TavilyAPIKey: "k"}, deps); p == nil || p.Name() != "tavily" {
		t.Error("expected tavily provider when key is set")
	}
}

// =============================================================================
// SearchAPI Helper Tests
// =============================================================================

func TestMapSearchAPITimePeriod(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hour", "last_hour"},
		{"day", "last_day"},
		{"week", "last_week"},
		{"month", "last_month"},
		{"year", "last_year"},
		{"invalid", ""},
	}
	for _, tt := range tests {
		got := mapSearchAPITimePeriod(tt.input)
		if got != tt.expected {
			t.Errorf("mapSearchAPITimePeriod(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestMapSearchAPIImageSize(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"icon", "i"},
		{"small", "s"},
		{"medium", "m"},
		{"large", "l"},
		{"xlarge", "lt"},
		{"xxlarge", "lt"},
		{"huge", "lt"},
		{"unknown", ""},
	}
	for _, tt := range tests {
		got := mapSearchAPIImageSize(tt.input)
		if got != tt.expected {
			t.Errorf("mapSearchAPIImageSize(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// =============================================================================
// Tavily Provider Tests
// =============================================================================

func TestNewProvider_Tavily(t *testing.T) {
	cfg := config.SearchConfig{Provider: "tavily", TavilyAPIKey: "tkey"}
	p := NewProvider(cfg, newTestDeps(http.DefaultClient))
	if p.Name() != "tavily" {
		t.Errorf("expected provider name 'tavily', got %q", p.Name())
	}
}

// tavilyTestClient builds an http.Client that redirects Tavily's fixed POST URL
// to the given httptest server (Tavily has no base-URL override).
func tavilyTestClient(ts *httptest.Server) *http.Client {
	return &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
}

// decodeTavilyBody reads the JSON request body in a test handler.
func decodeTavilyBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("failed to decode request body: %v", err)
	}
	return body
}

func TestTavilyProvider_WebSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tavily-key" {
			t.Errorf("expected 'Bearer tavily-key', got %q", got)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected content-type application/json, got %q", r.Header.Get("Content-Type"))
		}
		body := decodeTavilyBody(t, r)
		if body["topic"] != "general" {
			t.Errorf("expected topic 'general', got %v", body["topic"])
		}
		if body["query"] != "test" {
			t.Errorf("expected query 'test', got %v", body["query"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[{"title":"Tavily Result","url":"https://example.com/t","content":"From Tavily","score":0.9}]}`)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: tavilyTestClient(ts), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	tv := NewTavilyProvider("tavily-key", deps)

	results, err := tv.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "Tavily Result" || results[0].Snippet != "From Tavily" {
		t.Errorf("unexpected result mapping: %+v", results[0])
	}
	if results[0].URL != "https://example.com/t" || results[0].DisplayLink != "https://example.com/t" {
		t.Errorf("expected URL == DisplayLink == https://example.com/t, got %+v", results[0])
	}
}

func TestTavilyProvider_WebSearch_WithSiteOperator(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeTavilyBody(t, r)
		q, _ := body["query"].(string)
		if !strings.Contains(q, "site:example.com") {
			t.Errorf("expected query to include injected site operator, got %q", q)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[]}`)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: tavilyTestClient(ts), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	tv := NewTavilyProvider("k", deps)
	if _, err := tv.Web(context.Background(), WebSearchParams{Query: "test", Site: "example.com", NumResults: 5}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestTavilyProvider_LongQueryWithSiteOperatorSurvives is the regression guard
// for the audit finding: when a long query plus a site: operator together exceed
// the 400-char cap, the cap must trim the user-query portion and keep the
// site: operator intact (never slice through it).
func TestTavilyProvider_LongQueryWithSiteOperatorSurvives(t *testing.T) {
	var sent string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent, _ = decodeTavilyBody(t, r)["query"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[]}`)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: tavilyTestClient(ts), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	tv := NewTavilyProvider("k", deps)

	// 390-char query + " site:example.com" would overflow 400 if capped naively.
	longQ := strings.Repeat("a", 390)
	if _, err := tv.Web(context.Background(), WebSearchParams{Query: longQ, Site: "example.com", NumResults: 5}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if utf8.RuneCountInString(sent) > tavilyMaxQueryLen {
		t.Errorf("query exceeds %d-char cap: %d", tavilyMaxQueryLen, utf8.RuneCountInString(sent))
	}
	if !strings.HasSuffix(sent, " site:example.com") {
		t.Errorf("site: operator was truncated — must survive intact; got tail %q", sent[max(0, len(sent)-30):])
	}
}

func TestTavilyProvider_NewsSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeTavilyBody(t, r)
		if body["topic"] != "news" {
			t.Errorf("expected topic 'news', got %v", body["topic"])
		}
		if body["time_range"] != "week" {
			t.Errorf("expected time_range 'week', got %v", body["time_range"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[{"title":"News Item","url":"https://www.wired.com/x","content":"Breaking","published_date":"Fri, 29 May 2026 12:00:00 GMT"}]}`)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: tavilyTestClient(ts), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	tv := NewTavilyProvider("k", deps)

	results, err := tv.News(context.Background(), NewsSearchParams{Query: "ai", NumResults: 3, Freshness: "week"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Source != "www.wired.com" {
		t.Errorf("expected source 'www.wired.com' (host extracted), got %q", results[0].Source)
	}
	// RFC1123 is normalized to ISO-8601 UTC (#234) for programmatic sorting.
	if results[0].PublishedAt != "2026-05-29T12:00:00Z" {
		t.Errorf("expected ISO-normalized published date '2026-05-29T12:00:00Z', got %q", results[0].PublishedAt)
	}
}

func TestTavilyProvider_EmptyResults(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[]}`)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: tavilyTestClient(ts), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	tv := NewTavilyProvider("k", deps)
	results, err := tv.Web(context.Background(), WebSearchParams{Query: "nothing", NumResults: 5})
	if err != nil {
		t.Fatalf("zero results must not be an error, got: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestTavilyProvider_RateLimited(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: tavilyTestClient(ts), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	tv := NewTavilyProvider("k", deps)
	_, err := tv.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err == nil {
		t.Fatal("expected error for 429")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("error must contain 'rate limited' for isRateLimitError classification, got: %v", err)
	}
}

func TestTavilyProvider_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"detail":{"error":"Query is missing."}}`)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: tavilyTestClient(ts), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	tv := NewTavilyProvider("k", deps)
	_, err := tv.Web(context.Background(), WebSearchParams{Query: "x", NumResults: 5})
	if err == nil || !strings.Contains(err.Error(), "tavily API error 400") {
		t.Errorf("expected 'tavily API error 400', got: %v", err)
	}
}

// TestTavilyProvider_ImagesEmpty locks the #54 convention: Images returns empty
// without error and makes NO HTTP call (server fails the test if hit).
func TestTavilyProvider_ImagesEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("Images must not make an HTTP request")
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: tavilyTestClient(ts), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	tv := NewTavilyProvider("k", deps)
	results, err := tv.Images(context.Background(), ImageSearchParams{Query: "cats"})
	if err != nil {
		t.Errorf("Images must return nil error (no breaker trip), got: %v", err)
	}
	if results != nil {
		t.Errorf("Images must return nil slice, got: %v", results)
	}
}

func TestTavilyProvider_QueryTruncation(t *testing.T) {
	var sent string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeTavilyBody(t, r)
		sent, _ = body["query"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[]}`)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: tavilyTestClient(ts), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	tv := NewTavilyProvider("k", deps)

	// ASCII: 500 chars must be capped to exactly 400, with no "..." suffix.
	if _, err := tv.Web(context.Background(), WebSearchParams{Query: strings.Repeat("a", 500), NumResults: 5}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := utf8.RuneCountInString(sent); n != tavilyMaxQueryLen {
		t.Errorf("expected query capped to %d runes, got %d", tavilyMaxQueryLen, n)
	}
	if strings.HasSuffix(sent, "...") {
		t.Errorf("query cap must not add an ellipsis suffix")
	}

	// Multibyte: 500 'é' runes must cap to 400 runes (rune-safe, valid UTF-8).
	if _, err := tv.Web(context.Background(), WebSearchParams{Query: strings.Repeat("é", 500), NumResults: 5}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := utf8.RuneCountInString(sent); n != tavilyMaxQueryLen {
		t.Errorf("expected multibyte query capped to %d runes, got %d", tavilyMaxQueryLen, n)
	}
	if !utf8.ValidString(sent) {
		t.Errorf("truncation produced invalid UTF-8")
	}
}

func TestMapTavilyTimeRange(t *testing.T) {
	cases := map[string]string{
		"hour": "day", "day": "day", "week": "week",
		"month": "month", "year": "year", "": "", "bogus": "",
	}
	for in, want := range cases {
		if got := mapTavilyTimeRange(in); got != want {
			t.Errorf("mapTavilyTimeRange(%q) = %q, want %q", in, got, want)
		}
	}
}

// =============================================================================
// Test Helpers
// =============================================================================

// rewriteTransport redirects all requests to a test server
type rewriteTransport struct {
	baseURL string
	inner   http.RoundTripper
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Preserve the original query string but redirect to test server
	newURL := t.baseURL + req.URL.Path + "?" + req.URL.RawQuery
	newReq, err := http.NewRequestWithContext(req.Context(), req.Method, newURL, req.Body)
	if err != nil {
		return nil, err
	}
	newReq.Header = req.Header
	return t.inner.RoundTrip(newReq)
}
