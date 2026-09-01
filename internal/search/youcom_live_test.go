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

func TestYouComLiveIntegration(t *testing.T) {
	key := os.Getenv("YOUDOTCOM_API_KEY")
	if key == "" {
		t.Skip("YOUDOTCOM_API_KEY not set, skipping live integration test")
	}

	provider := NewYouComProvider(key, Deps{
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
			Query:      "latest news today artificial intelligence regulation",
			NumResults: 3,
			Freshness:  "week",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected at least one news result (query has news intent)")
		}
		r := results[0]
		t.Logf("first news result: %s — %s (%s)", r.Title, r.Source, r.PublishedAt)
		if r.Source == "" {
			t.Errorf("expected non-empty source, got %+v", r)
		}
	})
}
