package search

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func TestOpenAlexLiveIntegration(t *testing.T) {
	email := os.Getenv("OPENALEX_EMAIL")
	if email == "" {
		t.Skip("OPENALEX_EMAIL not set, skipping live integration test")
	}

	provider := NewOpenAlexProvider(email, Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	t.Run("basic search", func(t *testing.T) {
		results, err := provider.Scholarly(context.Background(), AcademicSearchParams{
			Query:      "transformer attention mechanism",
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
		if r.Source != "openalex" {
			t.Errorf("expected source=openalex, got %s", r.Source)
		}
	})

	t.Run("date filtering", func(t *testing.T) {
		results, err := provider.Scholarly(context.Background(), AcademicSearchParams{
			Query:      "CRISPR gene editing",
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

	t.Run("open access filter", func(t *testing.T) {
		results, err := provider.Scholarly(context.Background(), AcademicSearchParams{
			Query:      "machine learning",
			OpenAccess: true,
			NumResults: 3,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, r := range results {
			if !r.OpenAccess {
				t.Logf("WARNING: result not marked OA (may be API lag): %s", r.Title)
			}
		}
	})
}
