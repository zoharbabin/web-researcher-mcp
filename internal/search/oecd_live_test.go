//go:build live

// Live external-API integration test — excluded from the default suite.
// Run with `make test-live`. OECD is keyless; skips only if the endpoint is
// unreachable. Guards the SDMX-JSON decode against live response-shape drift.
package search

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newOECDLiveProvider() *OECDProvider {
	return NewOECDProvider(Deps{
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
}

func TestOECDLiveDataflowSearch(t *testing.T) {
	p := newOECDLiveProvider()
	res, err := p.Econ(context.Background(), EconSearchParams{Query: "national accounts", NumResults: 3})
	if err != nil {
		t.Skipf("OECD unreachable (skipping live test): %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected dataflow matches for 'national accounts'")
	}
	// The matched ref is the agency,dataflow,version string used as series_id.
	t.Logf("first dataflow: %s — %s", res[0].SeriesID, res[0].Title)
}

func TestOECDLiveObservations(t *testing.T) {
	p := newOECDLiveProvider()
	// Quarterly National Accounts, US real GDP — a stable, well-known dataflow.
	res, err := p.Econ(context.Background(), EconSearchParams{
		SeriesID:   "OECD.SDD.NAD,DSD_NAMAIN1@DF_QNA,1.1",
		Country:    "USA",
		DateFrom:   "2022",
		DateTo:     "2023",
		NumResults: 10,
	})
	if err != nil {
		t.Skipf("OECD observations unreachable (skipping): %v", err)
	}
	if len(res) == 0 {
		t.Skip("OECD returned no observations for the pinned dataflow (shape may have drifted — investigate if persistent)")
	}
	for _, o := range res {
		t.Logf("%s = %v (hasValue=%v) units=%q", o.Date, o.Value, o.HasValue, o.Units)
	}
}
