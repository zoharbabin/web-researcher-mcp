//go:build live

// Live external-API integration test — excluded from the default suite.
// Run with `make test-live`. CourtListener works keyless (lower rate);
// COURTLISTENER_API_TOKEN raises the limit when set.
package search

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func TestCourtListenerLiveSearch(t *testing.T) {
	p := NewCourtListenerProvider(os.Getenv("COURTLISTENER_API_TOKEN"), Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	res, err := p.Cases(context.Background(), CaseSearchParams{Query: "miranda v. arizona", NumResults: 3})
	if err != nil {
		t.Fatalf("Cases error: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected case results")
	}
	c := res[0]
	t.Logf("first: %q [%s] %s (%s)", c.CaseName, c.Citation, c.Court, c.URL)
	if c.CaseName == "" || c.URL == "" || c.Source != "courtlistener" {
		t.Errorf("unexpected mapping: %+v", c)
	}
}

func TestCourtListenerLiveJurisdiction(t *testing.T) {
	p := NewCourtListenerProvider(os.Getenv("COURTLISTENER_API_TOKEN"), Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	res, err := p.Cases(context.Background(), CaseSearchParams{Query: "first amendment", Jurisdiction: "scotus", NumResults: 3})
	if err != nil {
		t.Fatalf("Cases error: %v", err)
	}
	t.Logf("scotus hits: %d", len(res))
}
