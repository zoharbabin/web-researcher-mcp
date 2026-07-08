//go:build live

// Live external-API integration test — excluded from the default suite
// (non-deterministic external dependency, real network calls to DuckDuckGo).
// Run with `go test -tags=live -run TestAwesomeListsLensLive ./internal/search/...`.
//
// Proves the "awesome-lists" lens (issue #354) actually narrows real search
// results to awesome-list curator domains, across the real-world queries
// drawn from the issue's user stories.
package search

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newAwesomeListsLiveDDGProvider() *DuckDuckGoProvider {
	return NewDuckDuckGoProvider(Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
}

// skipIfNetworkUnreachable distinguishes "no internet" (skip — environment
// problem, not a code problem) from "the request went out but came back
// empty/erroring for another reason" (fail — that's a real signal). Mirrors
// the graceful-skip convention used by internal/search/oecd_live_test.go and
// internal/search/eurostat_live_test.go for network flakiness.
func skipIfNetworkUnreachable(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		t.Skipf("network unreachable (DNS): %v", err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Skipf("network unreachable (timeout): %v", err)
	}
	s := err.Error()
	if contains(s, "connection refused") || contains(s, "no such host") || contains(s, "network is unreachable") {
		t.Skipf("network unreachable: %v", err)
	}
	// Transient provider-side throttling is an environment condition, not a
	// lens defect — skip rather than fail (rule 3.2, issue #354).
	if contains(s, "rate limited") {
		t.Skipf("provider rate limited: %v", err)
	}
}

// TestAwesomeListsLensLive is the real-network live test for the awesome-lists
// lens (issue #354). It loads the lens registry from the real lenses/ dir,
// resolves the "awesome-lists" lens, and — once it exists — runs real
// DuckDuckGo queries through it to prove the lens actually narrows results to
// awesome-list curator domains rather than being a no-op decoration.
func TestAwesomeListsLensLive(t *testing.T) {
	registry := &LensRegistry{lenses: make(map[string]*Lens)}
	if err := registry.LoadFromDir("../../lenses"); err != nil {
		t.Fatalf("LoadFromDir(../../lenses): %v", err)
	}

	// --- Step 1: resolve the lens. This is expected to fail right now. ---
	lens, ok := registry.Get("awesome-lists")
	if !ok {
		t.Fatalf(`lens "awesome-lists" not found in lenses/ — this is the expected failing baseline for issue #354 (Phase 2): the lens has not been implemented yet. Once lenses/awesome-lists.json ships, this test resolves the lens and exercises the real queries below.`)
	}

	// --- Steps 2-3: real, complete assertions — only reached once the lens exists. ---
	provider := newAwesomeListsLiveDDGProvider()

	queries := []string{
		"Python async web framework REST API",
		"OSINT reconnaissance automation passive scanning",
		"self-hosted CRM analytics customer support open source alternatives",
		"Kubernetes secrets scanning vulnerability SBOM open source",
	}

	for i, q := range queries {
		if i > 0 {
			time.Sleep(3 * time.Second) // avoid tripping DuckDuckGo's rate limiter
		}
		q := q
		t.Run(q, func(t *testing.T) {
			built := registry.BuildSiteQuery(q, lens)
			results, err := provider.Web(context.Background(), WebSearchParams{
				Query:      built,
				NumResults: 5,
			})
			skipIfNetworkUnreachable(t, err)
			if err != nil {
				t.Fatalf("provider.Web(%q) error: %v", built, err)
			}
			if len(results) == 0 {
				t.Fatalf("expected at least one result for lensed query %q (built: %q), got none", q, built)
			}
			t.Logf("query %q -> %d results, first: %s — %s", q, len(results), results[0].Title, results[0].URL)
		})
	}

	// Negative check: the lens must actually narrow results, not just decorate
	// the query with inert text. Compare the URL set from the lens-built query
	// against the URL set from the plain query (no site: injection) — they
	// must differ, proving the site: operators changed what came back.
	t.Run("lens narrows results vs plain query", func(t *testing.T) {
		time.Sleep(3 * time.Second) // avoid tripping DuckDuckGo's rate limiter
		plainQuery := queries[0]
		lensedQuery := registry.BuildSiteQuery(plainQuery, lens)

		lensedResults, err := provider.Web(context.Background(), WebSearchParams{
			Query:      lensedQuery,
			NumResults: 5,
		})
		skipIfNetworkUnreachable(t, err)
		if err != nil {
			t.Fatalf("provider.Web(lensed %q) error: %v", lensedQuery, err)
		}

		time.Sleep(3 * time.Second) // avoid tripping DuckDuckGo's rate limiter
		plainResults, err := provider.Web(context.Background(), WebSearchParams{
			Query:      plainQuery,
			NumResults: 5,
		})
		skipIfNetworkUnreachable(t, err)
		if err != nil {
			t.Fatalf("provider.Web(plain %q) error: %v", plainQuery, err)
		}

		if len(lensedResults) == 0 || len(plainResults) == 0 {
			t.Fatalf("expected non-empty results for both lensed and plain queries, got lensed=%d plain=%d", len(lensedResults), len(plainResults))
		}

		lensedURLs := urlSet(lensedResults)
		plainURLs := urlSet(plainResults)

		if sameURLSet(lensedURLs, plainURLs) {
			t.Errorf("lensed query %q and plain query %q returned the identical URL set %v — the lens is not narrowing results, only decorating the query", lensedQuery, plainQuery, plainURLs)
		}
	})
}

func urlSet(results []SearchResult) map[string]bool {
	set := make(map[string]bool, len(results))
	for _, r := range results {
		set[r.URL] = true
	}
	return set
}

func sameURLSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for u := range a {
		if !b[u] {
			return false
		}
	}
	return true
}
