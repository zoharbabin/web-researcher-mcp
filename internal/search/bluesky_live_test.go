//go:build live

// Live external-API integration test — excluded from the default suite
// (non-deterministic external dependency, real network calls to
// public.api.bsky.app). Run with `make test-live`.
//
// Proves the zero-config Bluesky AT Protocol search provider (#279) actually
// reaches Bluesky's real, unauthenticated public AppView API — no API key
// required.
package search

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func TestBlueskyProviderLive(t *testing.T) {
	p := NewBlueskyProvider(Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	// public.api.bsky.app deliberately 403s app.bsky.feed.searchPosts as
	// load-shedding on that one endpoint (confirmed by a Bluesky maintainer:
	// https://github.com/bluesky-social/bsky-docs/issues/332) while every
	// other AT Protocol endpoint it fronts responds normally. Web() retries
	// against api.bsky.app (same AppView backend, no caching layer) on a 403,
	// so this call should transparently succeed rather than needing a skip.
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
