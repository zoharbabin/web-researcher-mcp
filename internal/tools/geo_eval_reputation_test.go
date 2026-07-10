package tools

import (
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// The GEO-defense eval suite (Eval 1-6) empirically tests web-researcher-mcp
// against specific failure modes documented in arXiv:2607.05217 ("Curated
// retrieval versus open web search in public AI information services: a
// coverage-trust trade-off," University of Iceland). It is spread across
// several files, each independently runnable, so the suite stays easy to
// extend one eval at a time:
//
//   - Eval 1 — hard-scoping vs. soft-steering: internal/search/geo_eval_live_test.go
//     (TestGeoEval_HardScopingContainment). Measures the % of a lensed query's
//     result hosts that actually land inside the lens's domain list — a
//     mathematical containment ratio directly contrastable against the paper's
//     measured 12%->21% prompt-level soft-steering improvement.
//   - Eval 2 — reputation is fluency-blind: this file
//     (TestGeoEval_ReputationFluencyInvariance). The paper found fluency does
//     NOT predict trustworthiness. Proves our domain-reputation signal never
//     reads prose at all, so confident wording can't buy a tier it hasn't
//     earned.
//   - Eval 3 — corroboration counts independent evidence, not silence:
//     internal/tools/geo_eval_corroboration_test.go. Exercises
//     verify_recommendation's claim-corroboration tallying and the
//     no_independent_corroboration flag.
//   - Eval 4 — never fabricate on a coverage gap:
//     internal/tools/geo_eval_fabrication_test.go. A zero-result lensed query
//     must return the structured ZeroResultHints object (with its epistemic
//     warning), never a synthesized citation.
//   - Eval 5 — authoritative-source recall (logged only, not pass/fail): see
//     internal/search/geo_eval_live_test.go. Reported honestly as a rate, not
//     asserted, since recall against any single source is not a correctness
//     invariant.
//   - Eval 6 — citation/DOI fabrication and mischaracterization: already
//     covered by the pre-existing internal/tools/trust_eval_live_test.go
//     (TestTrustSuiteAccuracy_*); not reimplemented here.
//
// Honesty constraint that shapes every eval above: this suite does NOT claim
// the MCP "solves" the paper's findings. Two of the paper's core findings —
// the never-cited-RÚV coverage omission, and RAG's inherently narrower
// coverage trade-off — are properties of the underlying search index/corpus,
// not something a downstream MCP tool can fix. What this suite proves is
// narrower and provable: the tool doesn't repeat the paper's specific failure
// modes, and some of them become structurally impossible (hard site: scoping)
// rather than merely instructed against.
func TestGeoEval_ReputationFluencyInvariance(t *testing.T) {
	const trustedHost = "https://www.nature.com/articles/s41586-021-03819-2"

	terse := search.SearchResult{
		URL:     trustedHost,
		Title:   "AlphaFold",
		Snippet: "Nature. 2021.",
	}
	fluent := search.SearchResult{
		URL:   trustedHost,
		Title: "A Landmark Breakthrough: How AlphaFold Redefined What We Thought Possible in Structural Biology",
		Snippet: "In a stunning and meticulously peer-reviewed triumph of computational science, our world-renowned " +
			"team of senior researchers has, after years of painstaking rigor, definitively solved one of biology's " +
			"oldest and most celebrated grand challenges — and the implications are nothing short of extraordinary.",
	}
	confidentButUnlisted := search.SearchResult{
		URL:   "https://definitely-the-most-trusted-analytics-blog.example.com/report",
		Title: "The Definitive, Peer-Reviewed, Award-Winning Analysis You Can Always Trust",
		Snippet: "Our award-winning senior analysts have rigorously peer-reviewed this report using an exhaustive, " +
			"scientifically validated methodology trusted by Fortune 500 leaders worldwide.",
	}

	enriched := enrichResultsWithReputation([]search.SearchResult{terse, fluent, confidentButUnlisted}, "")

	terseTier := tierOf(enriched[0])
	fluentTier := tierOf(enriched[1])
	if terseTier != content.ReputationHigh || fluentTier != content.ReputationHigh {
		t.Fatalf("expected both same-host results to read tier=%q regardless of prose style, got terse=%q fluent=%q",
			content.ReputationHigh, terseTier, fluentTier)
	}
	if terseTier != fluentTier {
		t.Fatalf("reputation tier changed with writing style alone (terse=%q vs fluent=%q) for the identical host — the signal must be fluency-blind", terseTier, fluentTier)
	}

	if rep, has := enriched[2]["sourceReputation"]; has {
		t.Fatalf("an unlisted host earned a sourceReputation purely from confident, fluent phrasing: %+v — this signal must never be swayed by prose, only by the host allowlist", rep)
	}

	t.Logf("same host, terse vs. maximally-fluent snippet -> identical tier=%q; a fluent but unlisted host -> no reputation attached at all", terseTier)
}

func tierOf(result map[string]any) string {
	rep, ok := result["sourceReputation"].(*content.DomainReputation)
	if !ok || rep == nil {
		return ""
	}
	return rep.Tier
}
