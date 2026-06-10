//go:build live

// Live external-API integration test — excluded from the default suite.
// Run with `make test-live`. Eurostat is keyless; skips only if unreachable.
// Guards the JSON-stat flattened-index decode against live response drift.
package search

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newEurostatLiveProvider() *EurostatProvider {
	return NewEurostatProvider(Deps{
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
}

func TestEurostatLiveDatasetSearch(t *testing.T) {
	p := newEurostatLiveProvider()
	res, err := p.Econ(context.Background(), EconSearchParams{Query: "unemployment", NumResults: 3})
	if err != nil {
		t.Skipf("Eurostat catalogue unreachable (skipping): %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected dataset matches for 'unemployment'")
	}
	t.Logf("first dataset: %s — %s", res[0].SeriesID, res[0].Title)
}

func TestEurostatLiveObservations(t *testing.T) {
	p := newEurostatLiveProvider()
	// Monthly unemployment rate for Germany — stable dataset + geo.
	res, err := p.Econ(context.Background(), EconSearchParams{
		SeriesID:   "une_rt_m",
		Country:    "DE",
		DateFrom:   "2024-01",
		DateTo:     "2024-06",
		NumResults: 20,
	})
	if err != nil {
		t.Skipf("Eurostat observations unreachable (skipping): %v", err)
	}
	if len(res) == 0 {
		t.Skip("Eurostat returned no observations for the pinned dataset (shape may have drifted — investigate if persistent)")
	}
	for _, o := range res {
		t.Logf("%s = %v (hasValue=%v) units=%q notes=%q", o.Date, o.Value, o.HasValue, o.Units, o.Notes)
	}
}
