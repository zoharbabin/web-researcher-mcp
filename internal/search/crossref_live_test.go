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

func TestCrossRefLiveIntegration(t *testing.T) {
	email := os.Getenv("CROSSREF_EMAIL")
	if email == "" {
		t.Skip("CROSSREF_EMAIL not set, skipping live integration test")
	}

	provider := NewCrossRefProvider(email, Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	t.Run("basic search", func(t *testing.T) {
		results, err := provider.Scholarly(context.Background(), AcademicSearchParams{
			Query:      "protein structure prediction AlphaFold",
			NumResults: 3,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected at least one result")
		}

		r := results[0]
		t.Logf("First result: %s (DOI: %s, Year: %d, Citations: %d)", r.Title, r.DOI, r.Year, r.CitationCount)

		if r.Title == "" {
			t.Error("expected non-empty title")
		}
		if r.DOI == "" {
			t.Error("expected non-empty DOI")
		}
		if r.Source != "crossref" {
			t.Errorf("expected source=crossref, got %s", r.Source)
		}
	})

	t.Run("date filtering", func(t *testing.T) {
		results, err := provider.Scholarly(context.Background(), AcademicSearchParams{
			Query:      "deep learning natural language processing",
			YearFrom:   2022,
			YearTo:     2024,
			NumResults: 5,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, r := range results {
			if r.Year > 0 && (r.Year < 2022 || r.Year > 2024) {
				t.Errorf("result year %d outside filter range [2022, 2024]: %s", r.Year, r.Title)
			}
		}
	})

	t.Run("results have DOIs", func(t *testing.T) {
		results, err := provider.Scholarly(context.Background(), AcademicSearchParams{
			Query:      "quantum computing",
			NumResults: 5,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, r := range results {
			if r.DOI == "" {
				t.Errorf("CrossRef result missing DOI: %s", r.Title)
			}
		}
	})
}
