//go:build live

// Labeled accuracy eval for web_search relevance (#483). Unlike the hermetic
// provider unit tests (mocked HTTP responses) and the GEO-defense containment
// eval (geo_eval_reputation_test.go's suite, which measures site: scoping, not
// relevance), this drives the REAL web_search tool against live providers for
// a curated gold set of (query, expected-relevant-hosts) pairs — turning
// "does the top of the result list actually answer the query" into a measured
// recall number and a permanent regression guard, the search-relevance
// counterpart to trust_eval_live_test.go's citation-integrity eval.
//
// Run with: make test-relevance-eval (or: go test -tags=live -run TestSearchRelevance ./internal/tools/)
// Network required; uses Google Custom Search when configured (generous
// quota, no rate-limit skips), falling back to keyless DuckDuckGo otherwise.
package tools

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// relevanceGoldCase is one labeled (query, expected-relevant-hosts) case.
// wantHosts are hosts where a top-N result landing there is unambiguous
// evidence the query was answered correctly — not the only correct answer,
// but one no plausible engine should fail to surface near the top.
type relevanceGoldCase struct {
	name      string
	query     string
	wantHosts []string // any ONE of these appearing in the top N results counts as a hit
}

// relevanceGoldSet spans query types (factual lookup, technical reference,
// current-institution lookup) with well-known, stable canonical sources, so
// a miss reflects a real relevance problem rather than gold-set drift.
var relevanceGoldSet = []relevanceGoldCase{
	{"Go language spec", "Go programming language official website", []string{"go.dev", "golang.org"}},
	{"Python official docs", "Python programming language official documentation", []string{"python.org", "docs.python.org"}},
	{"Kubernetes docs", "Kubernetes documentation", []string{"kubernetes.io"}},
	{"PostgreSQL docs", "PostgreSQL documentation", []string{"postgresql.org"}},
	{"MDN JavaScript reference", "JavaScript Array.prototype.map documentation", []string{"developer.mozilla.org"}},
	{"Wikipedia CRISPR", "what is CRISPR gene editing", []string{"wikipedia.org"}},
	{"Model Context Protocol spec", "Model Context Protocol specification", []string{"modelcontextprotocol.io"}},
	{"Go module reference", "Go modules reference documentation", []string{"go.dev", "golang.org"}},
}

// newRelevanceEvalProvider picks the live provider for the suite: Google
// Custom Search when configured (paid, generously quota'd — no rate-limit
// skips), falling back to the keyless DuckDuckGo scraper otherwise. Both
// implement search.Provider, so the eval logic is provider-agnostic; only the
// eval's own inter-request sleep varies, since Google's quota tolerates a
// much shorter gap. Mirrors internal/search/geo_eval_live_test.go's
// newGeoEvalProvider (unexported there, so reimplemented here for this
// package's own live eval).
func newRelevanceEvalProvider(t *testing.T) (search.Provider, time.Duration) {
	t.Helper()
	deps := search.Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	}
	if key, cx := os.Getenv("GOOGLE_CUSTOM_SEARCH_API_KEY"), os.Getenv("GOOGLE_CUSTOM_SEARCH_ID"); key != "" && cx != "" {
		return search.NewGoogleProvider(key, cx, deps), 500 * time.Millisecond
	}
	return search.NewDuckDuckGoProvider(deps), 6 * time.Second
}

// hostOfRelevance extracts and normalizes a result URL's host the same way
// the production reputation enrichment path does (strip "www.", lowercase),
// so gold-set host matching is judged consistently with real ranking signals.
func hostOfRelevance(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// TestSearchRelevance_TopResultsAnswerQuery drives the real search.Provider
// (the same one web_search would resolve to) for each gold-set query and
// checks whether ANY of the top-N results lands on one of the query's
// expected-relevant hosts. This is a recall measurement, not a false-positive
// gate (unlike the trust suite): a miss means the query went unanswered by an
// obviously-correct source, which is the failure mode that matters for search
// relevance.
func TestSearchRelevance_TopResultsAnswerQuery(t *testing.T) {
	provider, sleep := newRelevanceEvalProvider(t)

	const topN = 5
	var hits, total int
	for i, c := range relevanceGoldSet {
		if i > 0 {
			time.Sleep(sleep)
		}
		c := c
		t.Run(c.name, func(t *testing.T) {
			results, err := provider.Web(context.Background(), search.WebSearchParams{
				Query:      c.query,
				NumResults: topN,
			})
			skipIfSearchNetworkUnreachable(t, err)
			if err != nil {
				t.Fatalf("provider.Web(%q) error: %v", c.query, err)
			}
			if len(results) == 0 {
				t.Fatalf("expected at least one result for query %q, got none", c.query)
			}

			total++
			hit := false
			var gotHosts []string
			for _, r := range results {
				host := hostOfRelevance(r.URL)
				gotHosts = append(gotHosts, host)
				for _, want := range c.wantHosts {
					if host == want {
						hit = true
					}
				}
			}
			if hit {
				hits++
			} else {
				t.Logf("no expected host %v found in top-%d results %v for query %q", c.wantHosts, topN, gotHosts, c.query)
			}
			t.Logf("%s: hit=%v (want one of %v, got %v)", c.name, hit, c.wantHosts, gotHosts)
		})
	}

	if total > 0 {
		recall := float64(hits) / float64(total)
		t.Logf("=== search relevance: %d/%d queries answered by an expected host in the top %d (%.2f) ===", hits, total, topN, recall)
		// A relevance regression severe enough to miss the majority of
		// well-known, unambiguous canonical sources indicates a real ranking
		// or query-construction problem, not gold-set noise.
		const minAggregateRecall = 0.5
		if recall < minAggregateRecall {
			t.Errorf("aggregate relevance recall %.2f is below the floor %.2f — search results are not answering well-known queries", recall, minAggregateRecall)
		}
	}
}

// skipIfSearchNetworkUnreachable skips (rather than fails) on a genuine
// network-layer problem, mirroring the pattern used by the GEO-defense eval
// and the other live provider tests in this repo — a DNS/timeout failure
// means "no network in this environment," not "relevance regressed."
func skipIfSearchNetworkUnreachable(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	s := err.Error()
	if strings.Contains(s, "no such host") ||
		strings.Contains(s, "connection refused") ||
		strings.Contains(s, "network is unreachable") ||
		strings.Contains(s, "timeout") ||
		strings.Contains(s, "i/o timeout") {
		t.Skipf("network unreachable: %v", err)
	}
}
