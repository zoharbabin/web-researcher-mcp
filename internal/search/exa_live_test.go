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

func newExaLiveProvider(t *testing.T) *ExaProvider {
	t.Helper()
	key := os.Getenv("EXA_API_KEY")
	if key == "" {
		t.Skip("EXA_API_KEY not set, skipping live integration test")
	}
	return NewExaProvider(key, Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
}

func TestExaLiveIntegration(t *testing.T) {
	provider := newExaLiveProvider(t)

	t.Run("web search returns results", func(t *testing.T) {
		results, err := provider.Web(context.Background(), WebSearchParams{
			Query: "Go programming language concurrency", NumResults: 3,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected at least one result")
		}
		t.Logf("first web result: %s — %s", results[0].Title, results[0].URL)
		if results[0].URL == "" {
			t.Errorf("expected non-empty URL, got %+v", results[0])
		}
	})

	t.Run("news search returns dated results", func(t *testing.T) {
		results, err := provider.News(context.Background(), NewsSearchParams{
			Query: "artificial intelligence", NumResults: 3, Freshness: "month",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		t.Logf("got %d news results", len(results))
	})

	t.Run("images returns empty without error", func(t *testing.T) {
		results, err := provider.Images(context.Background(), ImageSearchParams{Query: "cats"})
		if err != nil || results != nil {
			t.Errorf("images must be nil/nil, got %v / %v", results, err)
		}
	})

	t.Run("scholarly returns papers", func(t *testing.T) {
		results, err := provider.Scholarly(context.Background(), AcademicSearchParams{
			Query: "transformer neural network attention", NumResults: 3,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		t.Logf("got %d papers", len(results))
	})

}
