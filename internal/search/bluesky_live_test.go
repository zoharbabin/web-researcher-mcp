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
	"strings"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func TestBlueskyProviderLive(t *testing.T) {
	p := NewBlueskyProvider(Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	res, err := p.Web(context.Background(), WebSearchParams{Query: "golang", NumResults: 5})
	skipIfNetworkUnreachable(t, err)
	// public.api.bsky.app's edge (BunnyCDN) intermittently 403s the
	// app.bsky.feed.searchPosts endpoint specifically from some source IPs
	// (e.g. shared CI/datacenter ranges) while every other AT Protocol
	// endpoint (getProfile, getAuthorFeed, getPostThread) it fronts responds
	// normally — an edge-level condition of the test environment, not a
	// defect in the request this provider sends.
	if err != nil && strings.Contains(err.Error(), "HTTP 403") {
		t.Skipf("bsky.app searchPosts edge returned 403 from this environment: %v", err)
	}
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
