//go:build live

// Live external-API integration test — excluded from the default suite
// (non-deterministic external dependency). Run with `make test-live`.
// Semantic Scholar works WITHOUT a key at a lower shared rate; SEMANTIC_SCHOLAR_API_KEY
// (when set) raises the limit. These tests run keyless if the var is absent.
package search

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// skipIfRateLimited turns Semantic Scholar's keyless shared-pool rate limiting
// into a skip rather than a failure — without a key the public rate is genuinely
// throttled, which is expected behavior, not a regression.
func skipIfRateLimited(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "rate limited") {
		t.Skipf("Semantic Scholar keyless rate limit hit (set SEMANTIC_SCHOLAR_API_KEY to run): %v", err)
	}
}

func newS2LiveProvider() *SemanticScholarProvider {
	return NewSemanticScholarProvider(os.Getenv("SEMANTIC_SCHOLAR_API_KEY"), Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
}

func TestSemanticScholarLiveSearch(t *testing.T) {
	p := newS2LiveProvider()
	results, err := p.Scholarly(context.Background(), AcademicSearchParams{
		Query:      "attention is all you need",
		NumResults: 3,
	})
	skipIfRateLimited(t, err)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	r := results[0]
	t.Logf("First: %q (DOI=%s year=%d cites=%d tldr=%q)", r.Title, r.DOI, r.Year, r.CitationCount, r.TLDR)
	if r.Title == "" {
		t.Error("expected non-empty title")
	}
	if r.Source != "semanticscholar" {
		t.Errorf("source = %s, want semanticscholar", r.Source)
	}
}

func TestSemanticScholarLiveCitations(t *testing.T) {
	p := newS2LiveProvider()
	// BERT DOI — heavily cited, stable, and indexed by both S2 and OpenAlex.
	const seedDOI = "10.18653/v1/n19-1423"

	cites, err := p.Citations(context.Background(), seedDOI, 5)
	skipIfRateLimited(t, err)
	if err != nil {
		t.Fatalf("Citations error: %v", err)
	}
	if len(cites) == 0 {
		t.Fatal("expected forward citations for a highly-cited seed")
	}
	t.Logf("citedBy[0]=%q influential=%v intents=%v", cites[0].Title, cites[0].IsInfluential, cites[0].CitationIntents)

	refs, err := p.References(context.Background(), seedDOI, 5)
	skipIfRateLimited(t, err)
	if err != nil {
		t.Fatalf("References error: %v", err)
	}
	t.Logf("references count=%d", len(refs))
}
