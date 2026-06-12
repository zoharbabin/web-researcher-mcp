//go:build live

// Live external-API integration test — excluded from the default suite.
// Run with `make test-live`. FRED requires FRED_API_KEY.
package search

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newFREDLiveProvider(t *testing.T) *FREDProvider {
	key := os.Getenv("FRED_API_KEY")
	if key == "" {
		t.Skip("FRED_API_KEY not set, skipping FRED live test")
	}
	return NewFREDProvider(key, Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
}

func TestFREDLiveSeriesSearch(t *testing.T) {
	p := newFREDLiveProvider(t)
	res, err := p.Econ(context.Background(), EconSearchParams{Query: "unemployment rate", NumResults: 3})
	if err != nil {
		t.Fatalf("series search error: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected series results")
	}
	t.Logf("first: %s — %s (%s)", res[0].SeriesID, res[0].Title, res[0].Units)
}

func TestFREDLiveObservations(t *testing.T) {
	p := newFREDLiveProvider(t)
	res, err := p.Econ(context.Background(), EconSearchParams{SeriesID: "UNRATE", NumResults: 5})
	if err != nil {
		t.Fatalf("observations error: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected observations")
	}
	for _, o := range res {
		t.Logf("%s = %v (hasValue=%v)", o.Date, o.Value, o.HasValue)
	}
}

// #233: date_from must anchor the start of the returned window — the first
// observation should be at/after date_from, not the latest-N regardless.
func TestFREDLiveObservationsDateFromAnchors(t *testing.T) {
	p := newFREDLiveProvider(t)
	res, err := p.Econ(context.Background(), EconSearchParams{
		SeriesID: "UNRATE", DateFrom: "2020-01-01", DateTo: "2022-12-31", NumResults: 5,
	})
	if err != nil {
		t.Fatalf("observations error: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected observations in the requested range")
	}
	first := res[0].Date
	t.Logf("first observation = %s (anchored at date_from=2020-01-01)", first)
	if first < "2020-01-01" || first > "2022-12-31" {
		t.Errorf("first observation %s outside requested [2020-01-01, 2022-12-31] — date_from not anchoring", first)
	}
}
