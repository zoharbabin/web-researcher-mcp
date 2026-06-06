//go:build live

// Live external-API integration test — excluded from the default suite.
// Run with `make test-live`. Unpaywall requires a contact email.
package search

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func TestUnpaywallLiveResolve(t *testing.T) {
	email := os.Getenv("UNPAYWALL_EMAIL")
	if email == "" {
		email = os.Getenv("OPENALEX_EMAIL")
	}
	if email == "" {
		t.Skip("UNPAYWALL_EMAIL/OPENALEX_EMAIL not set, skipping live test")
	}

	r := NewUnpaywallResolver(email, Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	if r == nil {
		t.Fatal("resolver should be non-nil when email is set")
	}

	t.Run("known open-access DOI", func(t *testing.T) {
		// BERT — open access (CC-BY) in Unpaywall.
		oa, pdf, found, err := r.Resolve(context.Background(), "10.18653/v1/n19-1423")
		if err != nil {
			t.Fatalf("Resolve error: %v", err)
		}
		t.Logf("found=%v oa=%v pdf=%q", found, oa, pdf)
		if !found {
			t.Skip("DOI not in Unpaywall (may vary); skipping assertions")
		}
		// Note: an OA paper may expose only a landing page (no direct PDF); we
		// surface the best PDF when one exists but don't require it.
	})

	t.Run("nonexistent DOI is no-op not error", func(t *testing.T) {
		_, _, found, err := r.Resolve(context.Background(), "10.0000/does-not-exist-xyz")
		if err != nil {
			t.Fatalf("nonexistent DOI should not error, got: %v", err)
		}
		if found {
			t.Error("nonexistent DOI should report found=false")
		}
	})
}
