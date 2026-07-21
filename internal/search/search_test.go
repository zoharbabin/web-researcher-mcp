package search

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
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

// TestGoogleProvider_WebSearch_PublishedAt (#356): a pagemap metatag carrying
// a publish date must populate SearchResult.PublishedAt, normalized to RFC3339.
func TestGoogleProvider_WebSearch_PublishedAt(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := googleResponse{
			Items: []googleItem{
				{
					Title: "Dated", Link: "https://example.com/1", Snippet: "s", DisplayLink: "example.com",
					PageMap: &googlePageMap{MetaTags: []map[string]string{{"article:published_time": "2026-05-01T12:00:00Z"}}},
				},
				{Title: "Undated", Link: "https://example.com/2", Snippet: "s", DisplayLink: "example.com"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	g := NewGoogleProvider("test-key", "test-cx", deps)

	results, err := g.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].PublishedAt != "2026-05-01T12:00:00Z" {
		t.Errorf("expected normalized PublishedAt, got %q", results[0].PublishedAt)
	}
	if results[1].PublishedAt != "" {
		t.Errorf("expected empty PublishedAt when no pagemap date, got %q", results[1].PublishedAt)
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
			Title         string   `json:"title"`
			URL           string   `json:"url"`
			Description   string   `json:"description"`
			ExtraSnippets []string `json:"extra_snippets"`
		}{
			{Title: "Brave Result", URL: "https://example.com/brave", Description: "Found via Brave"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("brave-key", BraveConfig{}, deps)

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
	// #356: Brave provider does not populate PublishedAt
	if results[0].PublishedAt != "" {
		t.Errorf("expected empty PublishedAt, got %q", results[0].PublishedAt)
	}
}

func TestBraveProvider_RateLimited(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("key", BraveConfig{}, deps)

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
	// #356: Serper provider does not populate PublishedAt
	if results[0].PublishedAt != "" {
		t.Errorf("expected empty PublishedAt, got %q", results[0].PublishedAt)
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

// TestSearXNGProvider_WebSearch_PublishedAt (#356): a result's publishedDate
// must populate SearchResult.PublishedAt, normalized to RFC3339.
func TestSearXNGProvider_WebSearch_PublishedAt(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := searxngResponse{
			Results: []searxngResult{
				{Title: "Dated", URL: "https://example.com/sx", Content: "c", PublishedDate: "2026-05-01T12:00:00Z"},
				{Title: "Undated", URL: "https://example.com/sy", Content: "c"},
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
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].PublishedAt != "2026-05-01T12:00:00Z" {
		t.Errorf("expected normalized PublishedAt, got %q", results[0].PublishedAt)
	}
	if results[1].PublishedAt != "" {
		t.Errorf("expected empty PublishedAt when absent, got %q", results[1].PublishedAt)
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
		{WebSearchParams{Query: "hello", Sites: []string{"example.com"}}, "hello (site:example.com)"},
		{WebSearchParams{Query: "hello", Sites: []string{"example.com", "github.com"}}, "hello (site:example.com OR site:github.com)"},
		// Site takes precedence when both are set (the tool layer rejects this
		// combination before it reaches here; this only pins buildQuery's own
		// fallback order in case that guard is ever bypassed).
		{WebSearchParams{Query: "hello", Site: "example.com", Sites: []string{"github.com"}}, "hello site:example.com"},
	}
	for _, tt := range tests {
		got := buildQuery(tt.params)
		if got != tt.expected {
			t.Errorf("buildQuery(%+v) = %q, want %q", tt.params, got, tt.expected)
		}
	}
}

func TestBuildSitesQuery(t *testing.T) {
	tests := []struct {
		query    string
		domains  []string
		expected string
	}{
		{"hello", nil, "hello"},
		{"hello", []string{}, "hello"},
		{"hello", []string{"example.com"}, "hello (site:example.com)"},
		{"hello", []string{"example.com", "github.com"}, "hello (site:example.com OR site:github.com)"},
	}
	for _, tt := range tests {
		got := BuildSitesQuery(tt.query, tt.domains)
		if got != tt.expected {
			t.Errorf("BuildSitesQuery(%q, %v) = %q, want %q", tt.query, tt.domains, got, tt.expected)
		}
	}

	// Cap enforcement: more than MaxSiteDomains domains still produces a valid
	// query using only the first MaxSiteDomains entries.
	many := make([]string, MaxSiteDomains+5)
	for i := range many {
		many[i] = fmt.Sprintf("d%d.com", i)
	}
	got := BuildSitesQuery("hello", many)
	count := strings.Count(got, "site:")
	if count != MaxSiteDomains {
		t.Errorf("BuildSitesQuery with %d domains produced %d site: operators, want %d", len(many), count, MaxSiteDomains)
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

// =============================================================================
// ValidateLens Goggle Tests
// =============================================================================

func TestValidateLens_GoggleOnly(t *testing.T) {
	lens := &Lens{
		Name:        "goggle-only",
		Description: "re-ranked by a Goggle",
		Domains:     []string{},
		Goggle:      "https://raw.githubusercontent.com/brave/goggles-quickstart/main/goggles/programming.goggle",
	}
	if err := ValidateLens(lens, "test"); err != nil {
		t.Errorf("goggle-only lens should pass validation, got: %v", err)
	}
}

func TestValidateLens_GoggleInvalidURL(t *testing.T) {
	lens := &Lens{
		Name:   "bad-goggle",
		Goggle: "http://raw.githubusercontent.com/brave/goggles-quickstart/main/goggles/programming.goggle",
	}
	err := ValidateLens(lens, "test")
	if err == nil {
		t.Fatal("expected error for goggle with http:// (not https://)")
	}
	if !strings.Contains(err.Error(), "https://") {
		t.Errorf("expected error to mention https://, got: %v", err)
	}
}

func TestBraveProvider_GoggleInjection(t *testing.T) {
	var capturedQuery url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		resp := braveWebResponse{}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("key", BraveConfig{}, deps)

	goggleURL := "https://raw.githubusercontent.com/brave/goggles-quickstart/main/goggles/programming.goggle"
	_, err := b.Web(context.Background(), WebSearchParams{
		Query:      "golang channels",
		NumResults: 5,
		Goggles:    []string{goggleURL},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// F1: the live param is `goggles` (string|string[]); `goggles_id` is deprecated
	// and on Brave's removal path, so it must NOT be emitted.
	if got := capturedQuery.Get("goggles"); got != goggleURL {
		t.Errorf("expected goggles=%q in request, got %q", goggleURL, got)
	}
	if got := capturedQuery.Get("goggles_id"); got != "" {
		t.Errorf("deprecated goggles_id must not be sent, got %q", got)
	}
}

func TestBraveProvider_NoGoggleWhenUnset(t *testing.T) {
	var capturedQuery url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		resp := braveWebResponse{}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("key", BraveConfig{}, deps)

	_, err := b.Web(context.Background(), WebSearchParams{Query: "golang", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := capturedQuery.Get("goggles"); got != "" {
		t.Errorf("expected no goggles when Goggles is unset, got %q", got)
	}
	if got := capturedQuery.Get("goggles_id"); got != "" {
		t.Errorf("expected no goggles_id (deprecated) ever, got %q", got)
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
		// Custom date range passthrough
		{"2024-01-01..2024-12-31", "2024-01-01to2024-12-31"},
		{"2023-06-01..2023-06-30", "2023-06-01to2023-06-30"},
	}
	for _, tt := range tests {
		got := mapBraveFreshness(tt.input)
		if got != tt.expected {
			t.Errorf("mapBraveFreshness(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestBraveProvider_ExtraSnippets(t *testing.T) {
	var capturedQuery url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		resp := braveWebResponse{}
		resp.Web.Results = []struct {
			Title         string   `json:"title"`
			URL           string   `json:"url"`
			Description   string   `json:"description"`
			ExtraSnippets []string `json:"extra_snippets"`
		}{
			{
				Title:         "Result with snippets",
				URL:           "https://example.com/1",
				Description:   "Main snippet",
				ExtraSnippets: []string{"extra one", "extra two"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("key", BraveConfig{ExtraSnippets: true}, deps)

	results, err := b.Web(context.Background(), WebSearchParams{Query: "snippets test", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedQuery.Get("extra_snippets") != "1" {
		t.Errorf("expected extra_snippets=1 in request, got %q", capturedQuery.Get("extra_snippets"))
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(results[0].ExtraSnippets) != 2 {
		t.Errorf("expected 2 extra snippets, got %d", len(results[0].ExtraSnippets))
	}
	if results[0].ExtraSnippets[0] != "extra one" {
		t.Errorf("unexpected extra snippet[0]: %q", results[0].ExtraSnippets[0])
	}
}

func TestBraveProvider_Pagination(t *testing.T) {
	var capturedQuery url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		resp := braveWebResponse{}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("key", BraveConfig{}, deps)

	// F8: Brave's documented offset range is 0–9; an out-of-range request must be
	// clamped to 9, not passed through (Brave rejects/ignores larger values).
	_, err := b.Web(context.Background(), WebSearchParams{Query: "paginate", NumResults: 10, Offset: 20})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedQuery.Get("offset") != "9" {
		t.Errorf("expected offset clamped to 9, got %q", capturedQuery.Get("offset"))
	}
}

func TestBraveProvider_ResultFilter(t *testing.T) {
	var capturedQuery url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		resp := braveWebResponse{}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("key", BraveConfig{}, deps)

	_, err := b.Web(context.Background(), WebSearchParams{Query: "filter test", NumResults: 5, ResultFilter: "web,discussions"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedQuery.Get("result_filter") != "web,discussions" {
		t.Errorf("expected result_filter=web,discussions in request, got %q", capturedQuery.Get("result_filter"))
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
	// #356: SearchAPI provider does not populate PublishedAt
	if results[0].PublishedAt != "" {
		t.Errorf("expected empty PublishedAt, got %q", results[0].PublishedAt)
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
	if _, ok := providers["github"]; !ok {
		t.Error("expected github provider (zero-config, always available)")
	}
	if _, ok := providers["reddit"]; !ok {
		t.Error("expected reddit provider (zero-config, always available)")
	}
}

// TestProviderSupportedListContainsReddit (#277): reddit must be a recognized
// provider name so config validation and Router wiring accept it.
func TestProviderSupportedListContainsReddit(t *testing.T) {
	found := false
	for _, name := range SupportedProviders {
		if name == "reddit" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected \"reddit\" in SupportedProviders")
	}
}

// TestNewProviderByNameReddit (#277): reddit is zero-config — always returns
// a non-nil provider regardless of config contents.
func TestNewProviderByNameReddit(t *testing.T) {
	p := NewProviderByName("reddit", config.SearchConfig{}, newTestDeps(http.DefaultClient))
	if p == nil {
		t.Fatal("expected non-nil reddit provider")
	}
	if _, ok := p.(*RedditProvider); !ok {
		t.Errorf("expected *RedditProvider, got %T", p)
	}
}

// TestNewProviderRedditCase (#277): NewProvider's explicit-selection switch
// also recognizes "reddit".
func TestNewProviderRedditCase(t *testing.T) {
	p := NewProvider(config.SearchConfig{Provider: "reddit"}, newTestDeps(http.DefaultClient))
	if p.Name() != "reddit" {
		t.Errorf("expected provider name 'reddit', got %q", p.Name())
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
	if p := NewProviderByName("github", cfg, deps); p == nil || p.Name() != "github" {
		t.Error("expected github provider even without token (zero-config)")
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

// TestTavilyProvider_WebSearch_PublishedAt (#356): the web-search response's
// published_date must populate SearchResult.PublishedAt, normalized to RFC3339.
func TestTavilyProvider_WebSearch_PublishedAt(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[{"title":"Dated","url":"https://example.com/d","content":"c","published_date":"Fri, 29 May 2026 12:00:00 GMT"}]}`)
	}))
	defer ts.Close()

	deps := Deps{HTTPClient: tavilyTestClient(ts), Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	tv := NewTavilyProvider("k", deps)

	results, err := tv.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].PublishedAt != "2026-05-29T12:00:00Z" {
		t.Errorf("expected normalized PublishedAt, got %q", results[0].PublishedAt)
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
// BraveProvider Local Search Tests
// =============================================================================

// TestBraveProvider_Local exercises the three-call Brave local pipeline:
//
//	web/search?result_filter=locations → location IDs
//	local/pois?ids=…                  → POI details
//	local/descriptions?ids=…          → AI descriptions (best-effort)
func TestBraveProvider_Local(t *testing.T) {
	t.Parallel()

	var (
		poisCalled  int
		descsCalled int
		poisIDs     []string
		descsIDs    []string
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/res/v1/web/search"):
			// Step 1: return two location IDs.
			if r.URL.Query().Get("result_filter") != "locations" {
				t.Errorf("web/search: expected result_filter=locations, got %q", r.URL.Query().Get("result_filter"))
			}
			fmt.Fprint(w, `{"locations":{"results":[{"id":"abc123"},{"id":"def456"}]}}`)

		case strings.HasPrefix(r.URL.Path, "/res/v1/local/pois"):
			poisCalled++
			// Brave requires repeated ids= params, not a comma-joined value.
			poisIDs = r.URL.Query()["ids"]
			// Step 2: POI details in Brave's real shape (title, url, coordinates
			// array, postal_address.displayAddress, contact.telephone, rating
			// with reviewCount, structured opening_hours). Only abc123 has data.
			fmt.Fprint(w, `{"type":"local_pois","results":[{
				"id":"abc123",
				"title":"Mock Coffee",
				"url":"https://mock.example.com",
				"coordinates":[47.6062,-122.3321],
				"postal_address":{"type":"PostalAddress","displayAddress":"1 Main St, Seattle, WA 98101"},
				"contact":{"telephone":"+12065550100"},
				"categories":["coffee shop","cafe"],
				"rating":{"ratingValue":4.5,"bestRating":5.0,"reviewCount":120},
				"opening_hours":{
					"current_day":[{"abbr_name":"Thu","full_name":"Thursday","opens":"07:00","closes":"19:00"}],
					"days":[[{"abbr_name":"Fri","full_name":"Friday","opens":"07:00","closes":"18:00"}]]
				}
			}]}`)

		case strings.HasPrefix(r.URL.Path, "/res/v1/local/descriptions"):
			descsCalled++
			descsIDs = r.URL.Query()["ids"]
			// Step 3: description is a single string (not an array).
			fmt.Fprint(w, `{"type":"local_descriptions","results":[{"type":"local_description","id":"abc123","description":"A cozy coffee shop."}]}`)

		default:
			t.Errorf("unexpected request path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("brave-key", BraveConfig{}, deps)

	results, err := b.Local(context.Background(), LocalSearchParams{Query: "coffee", Near: "Seattle", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if poisCalled != 1 {
		t.Errorf("expected pois endpoint called once, got %d", poisCalled)
	}
	if descsCalled != 1 {
		t.Errorf("expected descriptions endpoint called once, got %d", descsCalled)
	}
	// Step 2 (POIs) gets both location IDs as separate repeated ids= params.
	wantPOIIDs := []string{"abc123", "def456"}
	if !reflect.DeepEqual(poisIDs, wantPOIIDs) {
		t.Errorf("pois ids = %v, want %v (repeated params)", poisIDs, wantPOIIDs)
	}
	// Step 3 (descriptions) is keyed off the POIs that actually came back, not
	// the original ID list: def456 returned no POI, so only abc123 is enriched.
	wantDescIDs := []string{"abc123"}
	if !reflect.DeepEqual(descsIDs, wantDescIDs) {
		t.Errorf("descriptions ids = %v, want %v (only realized POIs)", descsIDs, wantDescIDs)
	}

	r := results[0]
	if r.ID != "abc123" {
		t.Errorf("ID = %q, want %q", r.ID, "abc123")
	}
	if r.Name != "Mock Coffee" {
		t.Errorf("Name = %q, want %q", r.Name, "Mock Coffee")
	}
	wantAddr := "1 Main St, Seattle, WA 98101"
	if r.Address != wantAddr {
		t.Errorf("Address = %q, want %q", r.Address, wantAddr)
	}
	if r.Lat != 47.6062 {
		t.Errorf("Lat = %v, want 47.6062", r.Lat)
	}
	if r.Lon != -122.3321 {
		t.Errorf("Lon = %v, want -122.3321", r.Lon)
	}
	if r.Phone != "+12065550100" {
		t.Errorf("Phone = %q, want %q", r.Phone, "+12065550100")
	}
	if r.Website != "https://mock.example.com" {
		t.Errorf("Website = %q, want %q", r.Website, "https://mock.example.com")
	}
	if len(r.Categories) != 2 || r.Categories[0] != "coffee shop" {
		t.Errorf("Categories = %v, want [coffee shop cafe]", r.Categories)
	}
	if r.Rating != 4.5 {
		t.Errorf("Rating = %v, want 4.5", r.Rating)
	}
	if r.RatingCount != 120 {
		t.Errorf("RatingCount = %v, want 120", r.RatingCount)
	}
	wantHours := []string{"Thursday: 07:00-19:00", "Friday: 07:00-18:00"}
	if !reflect.DeepEqual(r.Hours, wantHours) {
		t.Errorf("Hours = %v, want %v", r.Hours, wantHours)
	}
	if r.Description != "A cozy coffee shop." {
		t.Errorf("Description = %q, want %q", r.Description, "A cozy coffee shop.")
	}
	if r.Source != "brave" {
		t.Errorf("Source = %q, want %q", r.Source, "brave")
	}
}

// TestBraveProvider_Local_CapsToNumResults verifies that when the locations
// filter returns more IDs than requested (Brave ignores `count` there), only
// NumResults IDs are forwarded to the POI endpoint — keeping the request URL
// bounded and honoring the caller's limit.
func TestBraveProvider_Local_CapsToNumResults(t *testing.T) {
	t.Parallel()

	var poisIDs []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/res/v1/web/search"):
			// Return 10 location IDs regardless of the requested count.
			var ids []string
			for i := 0; i < 10; i++ {
				ids = append(ids, fmt.Sprintf(`{"id":"loc%d"}`, i))
			}
			fmt.Fprintf(w, `{"locations":{"results":[%s]}}`, strings.Join(ids, ","))
		case strings.HasPrefix(r.URL.Path, "/res/v1/local/pois"):
			poisIDs = r.URL.Query()["ids"]
			fmt.Fprint(w, `{"type":"local_pois","results":[]}`)
		case strings.HasPrefix(r.URL.Path, "/res/v1/local/descriptions"):
			fmt.Fprint(w, `{"type":"local_descriptions","results":[]}`)
		default:
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("brave-key", BraveConfig{}, deps)

	if _, err := b.Local(context.Background(), LocalSearchParams{Query: "coffee", NumResults: 3}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(poisIDs) != 3 {
		t.Errorf("forwarded %d ids to pois, want 3 (capped to NumResults)", len(poisIDs))
	}
}

// TestBraveProvider_GzipErrorBody verifies that an error response whose body is
// gzip-encoded (Brave gzips everything when we send Accept-Encoding: gzip) is
// decompressed before being surfaced — otherwise the message is unreadable binary.
func TestBraveProvider_GzipErrorBody(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, _ = gw.Write([]byte(`{"error":"quota exceeded"}`))
	_ = gw.Close()
	gzipped := buf.Bytes()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(gzipped)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("brave-key", BraveConfig{}, deps)

	_, err := b.Web(context.Background(), WebSearchParams{Query: "q", NumResults: 1})
	if err == nil {
		t.Fatal("expected an error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "quota exceeded") {
		t.Errorf("error body not decompressed: %q", err.Error())
	}
}

// TestBraveProvider_Local_EmptyLocations verifies that when the web/search
// response contains no location IDs, the provider returns an empty slice
// immediately without making POI or description requests.
func TestBraveProvider_Local_EmptyLocations(t *testing.T) {
	t.Parallel()

	var extraCalls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/res/v1/web/search") {
			fmt.Fprint(w, `{"locations":{"results":[]}}`)
			return
		}
		// Any other endpoint is unexpected.
		extraCalls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("brave-key", BraveConfig{}, deps)

	results, err := b.Local(context.Background(), LocalSearchParams{Query: "nonexistent place"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
	if extraCalls != 0 {
		t.Errorf("expected no POI/description calls for empty locations, got %d extra calls", extraCalls)
	}
}

// =============================================================================
// BraveProvider Context Tests
// =============================================================================

// TestBraveProvider_Context verifies that the Context method correctly calls
// the /res/v1/llm/context endpoint, marshals the response, and returns a
// ContextResult with the assembled context text and per-snippet provenance.
func TestBraveProvider_Context(t *testing.T) {
	t.Parallel()

	var contextCalled int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/res/v1/llm/context" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		contextCalled++

		q := r.URL.Query()
		if q.Get("q") == "" {
			t.Error("expected non-empty q param")
		}
		// F5: Brave's documented /llm/context param names (the old max_tokens/
		// threshold spellings were silently dropped by Brave).
		if q.Get("maximum_number_of_tokens") != "8192" {
			t.Errorf("maximum_number_of_tokens = %q, want %q", q.Get("maximum_number_of_tokens"), "8192")
		}
		if q.Get("context_threshold_mode") != "balanced" {
			t.Errorf("context_threshold_mode = %q, want %q", q.Get("context_threshold_mode"), "balanced")
		}
		// The deprecated spellings must NOT be sent.
		if got := q.Get("max_tokens"); got != "" {
			t.Errorf("deprecated max_tokens must not be sent, got %q", got)
		}
		if got := q.Get("threshold"); got != "" {
			t.Errorf("deprecated threshold must not be sent, got %q", got)
		}
		// F5: the remaining documented /llm/context params must use Brave's exact
		// spellings (the old lang/max_urls/etc. spellings were silently dropped).
		for param, want := range map[string]string{
			"country":                            "fr",
			"search_lang":                        "en",
			"maximum_number_of_urls":             "10",
			"maximum_number_of_snippets":         "20",
			"maximum_number_of_tokens_per_url":   "1024",
			"maximum_number_of_snippets_per_url": "5",
			"enable_local":                       "true",
		} {
			if got := q.Get(param); got != want {
				t.Errorf("%s = %q, want %q", param, got, want)
			}
		}
		// The deprecated `lang` spelling must NOT be sent (Brave uses search_lang).
		if got := q.Get("lang"); got != "" {
			t.Errorf("deprecated lang must not be sent, got %q", got)
		}
		// Verify API key is present.
		if tok := r.Header.Get("X-Subscription-Token"); tok != "brave-ctx-key" {
			t.Errorf("X-Subscription-Token = %q, want %q", tok, "brave-ctx-key")
		}

		w.Header().Set("Content-Type", "application/json")
		// Brave's real /llm/context shape: grounding.generic[] (url/title/snippets)
		// plus a sources map keyed by URL. There is no top-level "context" string;
		// the provider assembles it from the generic snippets.
		fmt.Fprint(w, `{
			"grounding": {
				"generic": [
					{"url": "https://en.wikipedia.org/wiki/France", "title": "France - Wikipedia", "snippets": ["Paris is the capital of France.", "It sits on the Seine."]},
					{"url": "https://example.com/paris", "title": "Paris Guide", "snippets": ["Paris is a major European city."]}
				],
				"map": []
			},
			"sources": {
				"https://en.wikipedia.org/wiki/France": {"title": "France - Wikipedia", "hostname": "en.wikipedia.org", "age": ["Friday, May 30, 2025", "2025-05-30", "383 days ago"], "snippet": "Paris is the capital of France."},
				"https://example.com/paris": {"title": "Paris Guide", "hostname": "example.com", "snippet": "Paris is a major European city."}
			}
		}`)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("brave-ctx-key", BraveConfig{}, deps)

	enableLocal := true
	result, err := b.Context(context.Background(), ContextParams{
		Query:             "capital of France",
		MaxTokens:         8192,
		ThresholdMode:     "balanced",
		Country:           "fr",
		Language:          "en",
		MaxURLs:           10,
		MaxSnippets:       20,
		MaxTokensPerURL:   1024,
		MaxSnippetsPerURL: 5,
		EnableLocal:       &enableLocal,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if contextCalled != 1 {
		t.Errorf("context endpoint called %d times, want 1", contextCalled)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Assembled context = each source's snippets joined by space, sources by \n\n.
	wantContext := "Paris is the capital of France. It sits on the Seine.\n\nParis is a major European city."
	if result.Context != wantContext {
		t.Errorf("Context = %q, want %q", result.Context, wantContext)
	}
	if result.Source != "brave" {
		t.Errorf("Source = %q, want %q", result.Source, "brave")
	}
	if len(result.Snippets) != 2 {
		t.Fatalf("Snippets len = %d, want 2", len(result.Snippets))
	}
	sn := result.Snippets[0]
	if sn.Title != "France - Wikipedia" {
		t.Errorf("Snippets[0].Title = %q, want %q", sn.Title, "France - Wikipedia")
	}
	if sn.URL != "https://en.wikipedia.org/wiki/France" {
		t.Errorf("Snippets[0].URL = %q, want %q", sn.URL, "https://en.wikipedia.org/wiki/France")
	}
	if sn.Age != "2025-05-30" {
		t.Errorf("Snippets[0].Age = %q, want %q (ISO element of age tuple)", sn.Age, "2025-05-30")
	}
	if sn.Text != "Paris is the capital of France. It sits on the Seine." {
		t.Errorf("Snippets[0].Text = %q", sn.Text)
	}
	if sn.Source != "brave" {
		t.Errorf("Snippets[0].Source = %q, want %q", sn.Source, "brave")
	}
	// Second source has no age tuple — Age should be empty.
	sn2 := result.Snippets[1]
	if sn2.Age != "" {
		t.Errorf("Snippets[1].Age = %q, want empty", sn2.Age)
	}
}

// TestBraveProvider_Context_Error verifies that when the /res/v1/llm/context
// endpoint returns a non-2xx status, Context() returns an error so the
// search_and_scrape caller can fall through to normal scraping.
func TestBraveProvider_Context_Error(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a 403 from a plan that does not include the endpoint.
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":"plan does not include LLM context"}`)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("brave-key", BraveConfig{}, deps)

	result, err := b.Context(context.Background(), ContextParams{Query: "test"})
	if err == nil {
		t.Fatalf("expected error for 403 response, got nil (result=%+v)", result)
	}
}

// TestBraveProvider_Context_OptionalParams verifies that when optional params
// (country, language) are supplied they appear in the query string, and when
// absent they are omitted (no empty-string params polluting the URL).
func TestBraveProvider_Context_OptionalParams(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("country") != "US" {
			t.Errorf("country = %q, want %q", q.Get("country"), "US")
		}
		// F5: language scopes via search_lang, not the old `lang` spelling.
		if q.Get("search_lang") != "en" {
			t.Errorf("search_lang = %q, want %q", q.Get("search_lang"), "en")
		}
		// No token/threshold params when not set (documented names).
		if q.Get("maximum_number_of_tokens") != "" {
			t.Errorf("expected maximum_number_of_tokens absent, got %q", q.Get("maximum_number_of_tokens"))
		}
		if q.Get("context_threshold_mode") != "" {
			t.Errorf("expected context_threshold_mode absent, got %q", q.Get("context_threshold_mode"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"grounding":{"generic":[],"map":[]},"sources":{}}`)
	}))
	defer ts.Close()

	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5})}
	b := NewBraveProvider("brave-key", BraveConfig{}, deps)

	_, err := b.Context(context.Background(), ContextParams{
		Query:    "test",
		Country:  "US",
		Language: "en",
		// MaxTokens and Threshold intentionally left zero/empty.
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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
