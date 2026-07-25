//go:build live

// Live external-API integration test — excluded from the default suite.
// Run with `make test-live`. CORE.ac.uk is keyless (works at a lower shared
// rate); an optional CORE_API_KEY raises the limit. No skip on a missing key
// — keyless mode must be exercised (see issue #267 Standards Checklist).
package search

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newCORELiveProvider() *COREProvider {
	return NewCOREProvider(os.Getenv("CORE_API_KEY"), Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
}

func TestCOREProvider_Live_Scholarly(t *testing.T) {
	p := newCORELiveProvider()
	res, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "climate change", NumResults: 3})
	if err != nil {
		t.Skipf("CORE unreachable (skipping): %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected CORE results for 'climate change'")
	}
	for _, r := range res {
		t.Logf("%d | %s | openAccess=%v | %s", r.Year, r.Title, r.OpenAccess, r.URL)
		if r.URL == "" {
			t.Errorf("result missing URL: %+v", r)
		}
		if !r.OpenAccess {
			t.Errorf("every CORE result must be OpenAccess=true: %+v", r)
		}
	}
}

func TestCOREProvider_Live_Scholarly_WithYearFilter(t *testing.T) {
	p := newCORELiveProvider()
	res, err := p.Scholarly(context.Background(), AcademicSearchParams{
		Query: "renewable energy", YearFrom: 2020, YearTo: 2023, NumResults: 5,
	})
	if err != nil {
		t.Skipf("CORE unreachable (skipping): %v", err)
	}
	if len(res) == 0 {
		t.Skip("CORE returned no results for the filtered query")
	}
	for _, r := range res {
		if r.Year != 0 && r.Year < 2020 {
			t.Errorf("year %d is below the requested 2020 floor", r.Year)
		}
	}
}
