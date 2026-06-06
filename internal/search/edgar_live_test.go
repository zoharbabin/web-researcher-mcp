//go:build live

// Live external-API integration test — excluded from the default suite.
// Run with `make test-live`. EDGAR needs only a contact email for its User-Agent.
package search

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newEDGARLiveProvider(t *testing.T) *EDGARProvider {
	email := os.Getenv("EDGAR_CONTACT_EMAIL")
	if email == "" {
		email = os.Getenv("OPENALEX_EMAIL")
	}
	if email == "" {
		t.Skip("EDGAR_CONTACT_EMAIL/OPENALEX_EMAIL not set, skipping EDGAR live test")
	}
	return NewEDGARProvider("web-researcher-mcp/live-test ("+email+")", Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
}

func TestEDGARLiveByTicker(t *testing.T) {
	p := newEDGARLiveProvider(t)
	res, err := p.Filings(context.Background(), FilingSearchParams{Ticker: "AAPL", FormType: "10-K", NumResults: 3})
	if err != nil {
		t.Fatalf("Filings error: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected Apple 10-K filings")
	}
	t.Logf("first: %s %s %s (%s)", res[0].Company, res[0].FormType, res[0].FilingDate, res[0].URL)
	if res[0].FormType != "10-K" {
		t.Errorf("form_type filter failed: %s", res[0].FormType)
	}
}

func TestEDGARLiveFacts(t *testing.T) {
	p := newEDGARLiveProvider(t)
	res, err := p.Filings(context.Background(), FilingSearchParams{Ticker: "AAPL", Facts: true, NumResults: 5})
	if err != nil {
		t.Fatalf("Filings(facts) error: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected XBRL facts")
	}
	for _, f := range res {
		t.Logf("fact: %s = %.0f %s (period %s)", f.Concept, f.Value, f.Unit, f.PeriodOf)
	}
}

func TestEDGARLiveFullText(t *testing.T) {
	p := newEDGARLiveProvider(t)
	res, err := p.Filings(context.Background(), FilingSearchParams{Query: "climate risk disclosure", FormType: "10-K", NumResults: 3})
	if err != nil {
		t.Fatalf("full-text Filings error: %v", err)
	}
	t.Logf("full-text hits: %d", len(res))
}
