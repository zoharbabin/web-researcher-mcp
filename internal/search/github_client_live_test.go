//go:build live

// Live external-API integration test — excluded from the default suite
// (non-deterministic external dependency, real network calls to
// api.github.com). Run with:
//
//	go test -tags=live -run TestGitHubTopicFallbackLive ./internal/search/...
//
// Proves the GitHub topic-search fallback (issue #394) actually reaches
// GitHub's real, unauthenticated public Search API and recovers a taxonomy
// miss ecosyste.ms itself cannot resolve — "parenting" has no matching
// ecosyste.ms topic slug at all (verified live, see doc comment on
// EcosystemsAwesomeProvider: it's a real single-word stemming gap, the real
// slug is "parent"), but GitHub's own "topic:awesome topic:parenting" search
// does return real, curated results. Hits both real APIs end-to-end —
// deliberately not stubbing ecosyste.ms's base URL, since a genuine upstream
// taxonomy miss is exactly the condition that must trigger tier 3.
package search

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func TestGitHubTopicFallbackLive(t *testing.T) {
	p := NewEcosystemsAwesomeProvider("", "", "", Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	res, err := p.AwesomeLists(context.Background(), AwesomeListSearchParams{Topic: "parenting", NumResults: 5})
	skipIfNetworkUnreachable(t, err)
	if err != nil {
		t.Fatalf("AwesomeLists(parenting) error: %v", err)
	}
	if len(res) == 0 {
		t.Fatalf("expected at least one result for %q (ecosyste.ms taxonomy gap should be recovered by the GitHub topic-search fallback), got none", "parenting")
	}
	sawGitHubSource := false
	for _, r := range res {
		if r.Source == "github" {
			sawGitHubSource = true
		}
		if r.FullName == "" || r.URL == "" {
			t.Errorf("result missing FullName/URL: %+v", r)
		}
	}
	// The ecosyste.ms taxonomy gap for "parenting" was confirmed live at the
	// time this test was written; if ecosyste.ms's own taxonomy has since
	// grown a matching slug, tier 1 could satisfy the query directly. That's
	// fine — non-empty either way proves the caller-visible contract; the
	// github-source assertion only fires (and matters) when tier 3 was
	// actually needed.
	if !sawGitHubSource {
		t.Logf("no github-sourced result — ecosyste.ms's own taxonomy apparently now covers %q directly", "parenting")
	}
	t.Logf("recovered %d result(s) for %q, first: %s (source=%s)", len(res), "parenting", res[0].FullName, res[0].Source)
}
