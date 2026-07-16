//go:build live

// Live external-API integration test — excluded from the default suite
// (non-deterministic external dependency, real network calls to
// api.github.com). Run with:
//
//	go test -tags=live -run TestGitHubProviderLive ./internal/search/...
//
// Proves the GitHub search.Provider (#282) actually reaches GitHub's real
// public Search API and returns structured issue/PR results. Skipped when
// GITHUB_TOKEN is absent, to avoid burning the low unauthenticated rate
// limit in CI.
package search

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func TestGitHubProviderLive(t *testing.T) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		t.Skip("GITHUB_TOKEN not set — skipping to avoid burning the unauthenticated rate limit in CI")
	}

	p := NewGitHubProvider(token, Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	res, err := p.Web(context.Background(), WebSearchParams{Query: "repo:golang/go is:issue memory leak", NumResults: 5})
	skipIfNetworkUnreachable(t, err)
	if err != nil {
		t.Fatalf("Web() error: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected at least one result from GitHub's real Search API, got none")
	}
	if res[0].DisplayLink != "github.com" {
		t.Errorf("DisplayLink = %q, want github.com", res[0].DisplayLink)
	}
	t.Logf("recovered %d result(s), first: %s — %s", len(res), res[0].Title, res[0].URL)
}
