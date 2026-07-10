//go:build live

// Eval 1 + Eval 5 of the GEO-defense eval suite (see the suite-level header
// comment in internal/tools/geo_eval_reputation_test.go for the full 6-eval
// map and its honesty constraints). Targets arXiv:2607.05217's headline
// finding that a prompt-level "prefer trusted domains" instruction only moved
// citation share from 12% to 21% — soft steering barely works. This test
// measures the equivalent number for our lens system's hard site: scoping: the
// % of a lensed query's result hosts that land inside the lens's own domain
// list. Domains are injected into the query BEFORE it reaches the provider, so
// (unlike a prompt instruction) an out-of-list host cannot rank at all unless
// the search engine itself ignores the site: operator.
//
// Run with: go test -tags=live -run TestGeoEval ./internal/search/...
// (also covered by `make test-live`, which has no -run filter)
package search

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// geoEvalCase is one lens+query gold-set entry for the containment eval.
type geoEvalCase struct {
	lens  string
	query string
}

// geoEvalCases spans lenses that map onto the paper's "trusted domain" framing
// most directly — news/journalism/tech (the paper's own trust-domain
// instruction targeted news outlets) plus academic/legal/security/clinical/
// government as the repo's broader structured-domain coverage.
var geoEvalCases = []geoEvalCase{
	{"news", "central bank interest rate decision"},
	{"journalism", "corporate ownership investigation"},
	{"tech", "large language model safety research"},
	{"academic", "CRISPR gene editing mechanism"},
	{"legal", "supreme court free speech ruling"},
	{"security", "critical vulnerability disclosure"},
	{"clinical", "statin drug cardiovascular outcomes"},
	{"government", "immigration policy regulation"},
}

func newGeoEvalDDGProvider() *DuckDuckGoProvider {
	return NewDuckDuckGoProvider(Deps{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
}

// hostOf strips "www." the same way reputationForURL/hostForURL do in the
// tools package, so containment is judged by the same host normalization the
// production enrichment path uses.
func hostOf(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// inLensDomains reports whether host equals or is a subdomain of one of the
// lens's domains — mirroring the intent of a site: operator match.
func inLensDomains(host string, domains []string) bool {
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// TestGeoEval_HardScopingContainment is Eval 1: for each lens+query gold-set
// case, issues the lens-built query and computes the in-domain containment
// ratio (results whose host is actually inside the lens's domain list, out of
// all non-empty-host results returned). A perfectly enforced site: operator
// should yield 100% containment; anything less is logged as a concrete,
// reproducible number — the same kind of measurement the paper used for its
// 12%->21% soft-steering figure, but for a mechanism that scopes the query
// itself rather than merely instructing the model.
func TestGeoEval_HardScopingContainment(t *testing.T) {
	registry := &LensRegistry{lenses: make(map[string]*Lens)}
	if err := registry.LoadFromDir("../../lenses"); err != nil {
		t.Fatalf("LoadFromDir(../../lenses): %v", err)
	}
	provider := newGeoEvalDDGProvider()

	var totalResults, totalContained int
	for i, c := range geoEvalCases {
		if i > 0 {
			time.Sleep(6 * time.Second) // avoid tripping DuckDuckGo's rate limiter
		}
		c := c
		t.Run(c.lens+"/"+c.query, func(t *testing.T) {
			lens, ok := registry.Get(c.lens)
			if !ok {
				t.Fatalf("lens %q not found in lenses/ — gold-set case references a lens that no longer exists", c.lens)
			}
			built := registry.BuildSiteQuery(c.query, lens)
			results, err := provider.Web(context.Background(), WebSearchParams{
				Query:      built,
				NumResults: 8,
			})
			skipIfNetworkUnreachable(t, err)
			if err != nil {
				t.Fatalf("provider.Web(%q) error: %v", built, err)
			}
			if len(results) == 0 {
				t.Fatalf("expected at least one result for lensed query %q (built: %q), got none", c.query, built)
			}

			contained, counted := 0, 0
			for _, r := range results {
				host := hostOf(r.URL)
				if host == "" {
					continue
				}
				counted++
				if inLensDomains(host, lens.Domains) {
					contained++
				} else {
					t.Logf("out-of-lens host surfaced despite site: scoping: %s (%s)", host, r.URL)
				}
			}
			if counted == 0 {
				t.Fatalf("no result carried a parseable host for lens %q query %q", c.lens, c.query)
			}
			ratio := float64(contained) / float64(counted)
			t.Logf("lens=%-11s query=%-40q containment=%.0f%% (%d/%d)", c.lens, c.query, ratio*100, contained, counted)

			totalResults += counted
			totalContained += contained

			// The paper's soft-steering instruction only reached 21% trusted-domain
			// share. A hard site: filter that fails to beat that bar has, in effect,
			// degraded to a soft suggestion — the search engine is ignoring the
			// operator. This is the eval's one hard invariant.
			const softSteeringCeiling = 0.21
			if ratio <= softSteeringCeiling {
				t.Errorf("lens %q containment %.0f%% did not clear the paper's soft-steering ceiling (21%%) — site: scoping is not behaving as a hard filter", c.lens, ratio*100)
			}
		})
	}

	if totalResults > 0 {
		t.Logf("=== Eval 1 summary: overall containment %.0f%% (%d/%d) across %d lens+query cases, vs. the paper's measured soft-steering improvement of 12%%->21%% ===",
			float64(totalContained)/float64(totalResults)*100, totalContained, totalResults, len(geoEvalCases))
	}
}

// TestGeoEval_AuthoritativeSourceRecall is Eval 5: logs (does NOT assert
// pass/fail on) how often a lensed query for a well-known, canonical topic in
// that lens's domain surfaces at least one of a small set of obviously
// authoritative hosts for that topic. Recall against any single expected host
// is not a correctness invariant a live web index can guarantee call to call
// (ranking varies, hosts change), so this eval is reported honestly as a rate
// for visibility, never asserted as pass/fail.
func TestGeoEval_AuthoritativeSourceRecall(t *testing.T) {
	registry := &LensRegistry{lenses: make(map[string]*Lens)}
	if err := registry.LoadFromDir("../../lenses"); err != nil {
		t.Fatalf("LoadFromDir(../../lenses): %v", err)
	}
	provider := newGeoEvalDDGProvider()

	cases := []struct {
		lens        string
		query       string
		wantAnyHost []string
	}{
		{"clinical", "COVID-19 vaccine efficacy trial results", []string{"pubmed.ncbi.nlm.nih.gov", "clinicaltrials.gov", "fda.gov", "cochranelibrary.com"}},
		{"legal", "Roe v Wade Supreme Court opinion", []string{"courtlistener.com", "supremecourt.gov", "law.cornell.edu"}},
		{"security", "Log4Shell CVE vulnerability", []string{"nvd.nist.gov", "cve.org", "cvedetails.com"}},
	}

	hit := 0
	for i, c := range cases {
		if i > 0 {
			time.Sleep(3 * time.Second)
		}
		lens, ok := registry.Get(c.lens)
		if !ok {
			t.Logf("lens %q not found — skipping recall case", c.lens)
			continue
		}
		built := registry.BuildSiteQuery(c.query, lens)
		results, err := provider.Web(context.Background(), WebSearchParams{Query: built, NumResults: 8})
		skipIfNetworkUnreachable(t, err)
		if err != nil {
			t.Logf("query %q error: %v (skipped from recall rate)", c.query, err)
			continue
		}
		found := ""
		for _, r := range results {
			host := hostOf(r.URL)
			if inLensDomains(host, c.wantAnyHost) {
				found = host
				break
			}
		}
		if found != "" {
			hit++
			t.Logf("recall hit: lens=%s query=%q found=%s", c.lens, c.query, found)
		} else {
			t.Logf("recall miss: lens=%s query=%q none of %v present in top results", c.lens, c.query, c.wantAnyHost)
		}
	}
	t.Logf("=== Eval 5 summary (logged rate, not a pass/fail assertion): %d/%d authoritative-source recall hits ===", hit, len(cases))
}
