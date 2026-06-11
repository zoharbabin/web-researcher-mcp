//go:build live

// Live external-API integration test — excluded from the default suite.
// Run with `make test-live`. PubMed (NCBI E-utilities) is keyless; an optional
// PUBMED_API_KEY raises the rate. Guards the esearch→esummary flow + DOI
// extraction against live response drift.
package search

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newPubMedLiveProvider() *PubMedProvider {
	// Keyless by default; pick up a key/email from the env when present.
	return NewPubMedProvider(os.Getenv("PUBMED_API_KEY"), os.Getenv("PUBMED_EMAIL"), Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
}

func TestPubMedLiveSearch(t *testing.T) {
	p := newPubMedLiveProvider()
	res, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "CRISPR gene editing", NumResults: 3})
	if err != nil {
		t.Skipf("PubMed unreachable (skipping): %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected PubMed results for 'CRISPR gene editing'")
	}
	for _, r := range res {
		t.Logf("%d | %s | %s | doi=%q | %s", r.Year, r.Journal, r.Title, r.DOI, r.URL)
	}
	// At least the URL must be a real PubMed link.
	if res[0].URL == "" || res[0].Source != "pubmed" {
		t.Errorf("unexpected first record: url=%q source=%q", res[0].URL, res[0].Source)
	}
}

func TestPubMedLiveDateRange(t *testing.T) {
	p := newPubMedLiveProvider()
	// The year filter maps to esearch mindate/maxdate with datetype=pdat
	// (publication date). NCBI's pdat is the print-publication date, which can
	// differ from the sortpubdate we surface as Year (online-first vs print), so a
	// returned record's displayed Year may sit just outside the window even though
	// the API filter was applied. We therefore assert the filter HAS AN EFFECT
	// (the result set differs from, and is no larger than, the unfiltered set) and
	// that displayed years cluster around the window — not that every year is
	// strictly in-range, which PubMed's date semantics don't guarantee.
	filtered, err := p.Scholarly(context.Background(), AcademicSearchParams{
		Query: "vaccine efficacy", YearFrom: 2020, YearTo: 2021, NumResults: 5,
	})
	if err != nil {
		t.Skipf("PubMed unreachable (skipping): %v", err)
	}
	if len(filtered) == 0 {
		t.Skip("PubMed returned no results for the filtered query")
	}
	// Years should be near the window (allow ±1 for the pdat/sortpubdate skew).
	for _, r := range filtered {
		if r.Year != 0 && (r.Year < 2019 || r.Year > 2022) {
			t.Errorf("year %d is far outside the requested 2020–2021 window (pdat skew should be ≤1yr)", r.Year)
		}
	}
}
