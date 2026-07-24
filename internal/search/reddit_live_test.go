//go:build live

// Live external-API integration test — excluded from the default suite
// (non-deterministic external dependency, real network calls to
// www.reddit.com). Run with `make test-live`.
//
// Proves the zero-config Reddit RSS search provider (#277) actually reaches
// Reddit's real, unauthenticated Atom search feed — no API key required.
package search

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func TestRedditProviderLive(t *testing.T) {
	p := NewRedditProvider(Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	res, err := p.Web(context.Background(), WebSearchParams{Query: "golang", NumResults: 5})
	skipIfNetworkUnreachable(t, err)
	if err != nil {
		t.Fatalf("Web() error: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected at least one result for query 'golang'")
	}
	t.Logf("first result: %s — %s", res[0].Title, res[0].URL)
	if res[0].URL == "" {
		t.Errorf("expected non-empty URL, got %+v", res[0])
	}
}
