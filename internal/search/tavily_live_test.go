//go:build live

// Live external-API integration test — excluded from the default suite
// (non-deterministic external dependency). Run with `make test-live`.
package search

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func TestTavilyLiveIntegration(t *testing.T) {
	key := os.Getenv("TAVILY_API_KEY")
	if key == "" {
		t.Skip("TAVILY_API_KEY not set, skipping live integration test")
	}

	provider := NewTavilyProvider(key, Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	t.Run("web search returns results", func(t *testing.T) {
		results, err := provider.Web(context.Background(), WebSearchParams{
			Query:      "Go programming language concurrency patterns",
			NumResults: 3,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected at least one result")
		}
		r := results[0]
		t.Logf("first web result: %s — %s", r.Title, r.URL)
		if r.Title == "" || r.URL == "" {
			t.Errorf("expected non-empty title and URL, got %+v", r)
		}
	})

	t.Run("news search returns dated results", func(t *testing.T) {
		results, err := provider.News(context.Background(), NewsSearchParams{
			Query:      "artificial intelligence regulation",
			NumResults: 3,
			Freshness:  "week",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected at least one news result")
		}
		r := results[0]
		t.Logf("first news result: %s — %s (%s)", r.Title, r.Source, r.PublishedAt)
		if r.Source == "" {
			t.Error("expected non-empty source (host) on news result")
		}
	})

	t.Run("images returns empty without error", func(t *testing.T) {
		results, err := provider.Images(context.Background(), ImageSearchParams{Query: "cats"})
		if err != nil {
			t.Errorf("expected nil error from unsupported image search, got: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected empty image results, got %d", len(results))
		}
	})

	t.Run("oversized query is accepted (capped, not rejected)", func(t *testing.T) {
		long := ""
		for i := 0; i < 500; i++ {
			long += "a"
		}
		// Without the 400-char cap this would return HTTP 400 from Tavily.
		if _, err := provider.Web(context.Background(), WebSearchParams{Query: long, NumResults: 1}); err != nil {
			t.Errorf("expected capped query to succeed, got: %v", err)
		}
	})
}
