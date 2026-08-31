package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newYouComTestProvider(t *testing.T, handler http.HandlerFunc) *YouComProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	deps := Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5}),
	}
	p := NewYouComProvider("test-key", deps)
	p.SetBaseURL(srv.URL)
	return p
}

func TestYouComProvider_WebSearch(t *testing.T) {
	t.Parallel()

	p := newYouComTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("X-API-Key"); got != "test-key" {
			t.Fatalf("X-API-Key = %q, want test-key", got)
		}
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "web-researcher-mcp/") {
			t.Fatalf("User-Agent = %q, want project User-Agent", ua)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		if got := body["query"]; got != "golang testing site:example.com" {
			t.Fatalf("query = %v, want query with site operator", got)
		}
		if got := body["count"]; got != float64(10) {
			t.Fatalf("count = %v, want 10", got)
		}
		if got := body["freshness"]; got != "week" {
			t.Fatalf("freshness = %v, want week", got)
		}
		if got := body["country"]; got != "US" {
			t.Fatalf("country = %v, want US", got)
		}
		if got := body["language"]; got != "en" {
			t.Fatalf("language = %v, want en", got)
		}
		if got := body["safesearch"]; got != "strict" {
			t.Fatalf("safesearch = %v, want strict", got)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": map[string]any{
				"web": []map[string]any{
					{
						"url":         "https://example.com/1",
						"title":       "First result",
						"description": "Primary description",
						"snippets":    []string{"Snippet one", "Snippet two"},
					},
				},
			},
		})
	})

	results, err := p.Web(context.Background(), WebSearchParams{
		Query:      "golang testing",
		NumResults: 10,
		TimeRange:  "week",
		Safe:       "high",
		Country:    "us",
		Language:   "en",
		Site:       "example.com",
	})
	if err != nil {
		t.Fatalf("Web() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	got := results[0]
	if got.Title != "First result" || got.URL != "https://example.com/1" {
		t.Fatalf("result = %+v", got)
	}
	if got.Snippet != "Snippet one" {
		t.Fatalf("Snippet = %q, want Snippet one", got.Snippet)
	}
	if got.DisplayLink != "example.com" {
		t.Fatalf("DisplayLink = %q, want example.com", got.DisplayLink)
	}
	if len(got.ExtraSnippets) != 1 || got.ExtraSnippets[0] != "Snippet two" {
		t.Fatalf("ExtraSnippets = %#v, want [Snippet two]", got.ExtraSnippets)
	}
}

func TestYouComProvider_NewsSearch(t *testing.T) {
	t.Parallel()

	p := newYouComTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		if got := body["freshness"]; got != "month" {
			t.Fatalf("freshness = %v, want month", got)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": map[string]any{
				"news": []map[string]any{
					{
						"url":         "https://news.example.com/story",
						"title":       "News headline",
						"description": "News summary",
						"page_age":    "2026-08-24T15:04:05",
					},
				},
			},
		})
	})

	results, err := p.News(context.Background(), NewsSearchParams{
		Query:     "example news",
		Freshness: "month",
	})
	if err != nil {
		t.Fatalf("News() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	got := results[0]
	if got.Title != "News headline" || got.URL != "https://news.example.com/story" {
		t.Fatalf("result = %+v", got)
	}
	if got.Source != "news.example.com" {
		t.Fatalf("Source = %q, want news.example.com", got.Source)
	}
	if got.Snippet != "News summary" {
		t.Fatalf("Snippet = %q, want News summary", got.Snippet)
	}
	if got.PublishedAt == "" || !strings.HasPrefix(got.PublishedAt, "2026-08-24T") {
		t.Fatalf("PublishedAt = %q, want RFC3339 timestamp for 2026-08-24", got.PublishedAt)
	}
}

func TestYouComProvider_ImagesNoop(t *testing.T) {
	t.Parallel()

	p := NewYouComProvider("test-key", Deps{Breaker: circuit.New(circuit.Config{FailureThreshold: 5})})
	images, err := p.Images(context.Background(), ImageSearchParams{Query: "cats"})
	if err != nil {
		t.Fatalf("Images() error = %v", err)
	}
	if images != nil {
		t.Fatalf("Images() = %#v, want nil", images)
	}
}
