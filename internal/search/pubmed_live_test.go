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
	res, err := p.Scholarly(context.Background(), AcademicSearchParams{
		Query: "vaccine efficacy", YearFrom: 2020, YearTo: 2021, NumResults: 3,
	})
	if err != nil {
		t.Skipf("PubMed unreachable (skipping): %v", err)
	}
	for _, r := range res {
		if r.Year != 0 && (r.Year < 2020 || r.Year > 2021) {
			t.Errorf("year %d outside requested 2020–2021 range", r.Year)
		}
	}
}
