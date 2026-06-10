package tools

import (
	"context"
	"strings"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
)

// claimCoverageResult is the shared, caller-agnostic claim-coverage bundle used by
// both audit_bibliography (#174, corpus) and verify_citation (#195, single). It is
// EVIDENCE, never a verdict: lexical term-coverage + the relevant sentences + a
// negation-cue heads-up — no model, no support/refute stance.
type claimCoverageResult struct {
	Support   string   // claimAddressed / claimPartiallyAddressed / claimNotAddressed / claimSourceUnavailable
	Evidence  []string // ExtractClaimEvidence.KeySentences (claim-relevant sentences)
	SourceURL string   // the URL actually fetched ("" when unavailable)
	Contrast  bool     // a matched sentence carries a negation/contrast cue (read it yourself)
}

// claimCoverageFor fetches fetchURL and runs the lexical, model-free claim-coverage
// check. The caller selects fetchURL (live URL vs Wayback snapshot vs a matched
// record's URL — that logic differs per caller, so it stays out of here) and must
// pass an already-clampClaim'd claim. Best-effort: a nil scraper, an empty
// fetchURL, an unfetchable source, or empty content all yield
// {Support: claimSourceUnavailable} — never an error, never a panic.
//
// The claim-coverage consts (claimAddressed/…/claimSourceUnavailable,
// claimAddressedThreshold, auditClaimScrapeMaxBytes) live in audit_bibliography.go
// in this same package; referenced directly, not duplicated.
func claimCoverageFor(ctx context.Context, deps Dependencies, fetchURL, claim string) claimCoverageResult {
	if deps.Scraper == nil || fetchURL == "" {
		return claimCoverageResult{Support: claimSourceUnavailable}
	}

	res, err := deps.Scraper.Scrape(ctx, fetchURL, auditClaimScrapeMaxBytes)
	if err != nil || res == nil || strings.TrimSpace(res.Content) == "" {
		return claimCoverageResult{Support: claimSourceUnavailable}
	}

	// Term coverage is the transparent, dependency-free measure of topical overlap,
	// measured as PEAK coverage within a sentence window (#177) so a narrow claim
	// whose terms are merely scattered across a long page is not over-counted. Zero
	// local overlap → not_addressed (the only flagged end, and only when the source
	// was actually read). Partial overlap → evidence shown, NOT flagged (the human
	// judges). Strong overlap → addressed.
	matched, total := content.ClaimTermCoverageWindowed(res.Content, claim, 0)
	ev := content.ExtractClaimEvidence(res.Content, claim)
	out := claimCoverageResult{
		Evidence:  ev.KeySentences,
		SourceURL: fetchURL,
		// A matched evidence sentence carrying a negation/contrast cue may REFUTE the
		// claim while sharing its terms (the lexical "false-addressed" hole). Surface
		// it as a neutral "read this yourself" signal — never as a refutes verdict.
		Contrast: content.HasContrastCue(ev.KeySentences),
	}
	switch {
	case total == 0:
		// The claim had no significant terms to match (e.g. all stop words) — we
		// can't make a coverage judgment, so don't accuse.
		out.Support = claimPartiallyAddressed
	case matched == 0:
		out.Support = claimNotAddressed
	case float64(matched)/float64(total) >= claimAddressedThreshold:
		out.Support = claimAddressed
	default:
		out.Support = claimPartiallyAddressed
	}
	return out
}
