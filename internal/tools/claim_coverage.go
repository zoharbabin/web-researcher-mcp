package tools

import (
	"context"
	"errors"
	"strings"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
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
	// ContentWords and SparsityNote (#358) annotate — never change — Support.
	// A thin source (e.g. a paywall/bot-wall stub) can still show lexical term
	// overlap with the claim and land on addressed/partially_addressed; this
	// flags that the coverage result may not reflect the full document.
	// ContentWords is 0 and SparsityNote is "" when Support is
	// claimSourceUnavailable (no content was fetched to count) or when the
	// fetched content clears the sparse-word threshold.
	ContentWords int
	SparsityNote string
	// FetchError (#631) is the sanitized scrape-error message when Support is
	// claimSourceUnavailable because the fetch itself failed (network error,
	// blocked/bot-wall, redirect-cap abort, parse error) — as opposed to no
	// fetch being attempted at all (nil scraper, empty fetchURL) or a fetch
	// that succeeded but returned empty content. Distinguishing these matters:
	// without it, a true claim and a false claim against a source whose fetch
	// merely failed both report the same uninformative
	// claimSourceUnavailable, indistinguishable from "not attempted". Empty
	// when no scrape error occurred.
	FetchError string
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
	if err != nil {
		return claimCoverageResult{Support: claimSourceUnavailable, SourceURL: fetchURL, FetchError: sanitizeClaimFetchError(err)}
	}
	if res == nil || strings.TrimSpace(res.Content) == "" {
		// A fetch that succeeded but returned no usable content (e.g. a
		// bot-wall/challenge page served as a plain 200) is attributable —
		// SourceURL records that a fetch was actually attempted here, so this
		// is distinguishable from fetchURL=="" (never attempted) (#681).
		return claimCoverageResult{Support: claimSourceUnavailable, SourceURL: fetchURL}
	}
	return claimCoverageFromContent(res.Content, fetchURL, claim)
}

// sanitizeClaimFetchError renders a scrape error as a short, attributable
// reason (#631) — "<kind>: <message>", capped so a tiered-fallback composite
// error (which lists every tier's outcome) never balloons the tool response.
// Falls back to err.Error() capped the same way when err isn't a
// *scraper.ScrapeError (e.g. a bare context error). A ScrapeError's Message
// frequently embeds the fetchURL verbatim (pipeline.go's composite
// tiered-fallback error), so it is masked exactly like every other
// scrape-error-to-LLM-facing-field path (errors.go's scrapeErrorToToolError /
// failureFromScrapeError) before being returned.
func sanitizeClaimFetchError(err error) string {
	const maxLen = 200
	msg := audit.MaskSecrets(err.Error())
	var se *scraper.ScrapeError
	if errors.As(err, &se) {
		msg = string(mapScrapeErrorKind(se.Kind)) + ": " + audit.MaskSecrets(se.Message)
	}
	runes := []rune(msg)
	if len(runes) > maxLen {
		msg = string(runes[:maxLen]) + "…"
	}
	return msg
}

// sparseWordThreshold mirrors scraper's content-volume floor (#358): below this
// many words, a claim-coverage result is annotated as unreliable. Kept as an
// independent constant (not imported from internal/scraper) because the two
// packages' thresholds are conceptually related but not contractually coupled —
// each package owns its own quality-signal cutoff.
const sparseWordThreshold = 150

// claimCoverageFromContent runs the lexical, model-free coverage check against
// already-fetched content — no scrape. It lets a caller that already has the
// page body (e.g. verify_citation's URL path, which fetches once to detect a DOI)
// reuse that single fetch for the claim check instead of fetching twice. Empty
// body → source_unavailable.
func claimCoverageFromContent(body, fetchURL, claim string) claimCoverageResult {
	if strings.TrimSpace(body) == "" {
		return claimCoverageResult{Support: claimSourceUnavailable}
	}
	// Term coverage is the transparent, dependency-free measure of topical overlap,
	// measured as PEAK coverage within a sentence window (#177) so a narrow claim
	// whose terms are merely scattered across a long page is not over-counted. Zero
	// local overlap → not_addressed (the only flagged end, and only when the source
	// was actually read). Partial overlap → evidence shown, NOT flagged (the human
	// judges). Strong overlap → addressed.
	matched, total, coverageWindow := content.ClaimTermCoverageWindowedSpan(body, claim, 0)
	ev := content.ExtractClaimEvidence(body, claim)
	// content.WordCount, not strings.Fields: CJK/Thai/Lao/Khmer/Myanmar text has
	// no inter-word spaces, so a complete non-Latin-script source would otherwise
	// collapse to a handful of "words" and trip a false SparsityNote.
	wordCount := content.WordCount(body)
	out := claimCoverageResult{
		Evidence:  ev.KeySentences,
		SourceURL: fetchURL,
		// A matched evidence sentence carrying a negation/contrast cue may REFUTE the
		// claim while sharing its terms (the lexical "false-addressed" hole). Surface
		// it as a neutral "read this yourself" signal — never as a refutes verdict.
		Contrast:     content.HasContrastCue(ev.KeySentences),
		ContentWords: wordCount,
	}
	if wordCount < sparseWordThreshold {
		out.SparsityNote = "Source content was thin (<150 words); coverage result may not reflect the full document."
	}
	switch {
	case total == 0:
		// The claim had no significant terms to match (e.g. all stop words) — we
		// can't make a coverage judgment, so don't accuse.
		out.Support = claimPartiallyAddressed
	case matched == 0:
		out.Support = claimNotAddressed
	case float64(matched)/float64(total) >= claimAddressedThreshold:
		// The ratio alone treats every claim term as equally significant —
		// generic vocabulary ("study", "demonstrates") and the claim's one
		// truly distinguishing entity ("Alzheimer's") count the same, so a
		// claim can clear the ratio on shared topic jargon alone while the
		// one term that would make it TRUE or FALSE never matched (#675).
		// Cap at partially_addressed unless either the claim has no such
		// distinguishing term (ratio-only behavior, unchanged) or at least
		// one of its distinguishing terms actually matched.
		if content.ClaimHasMatchedDistinguishingTerm(coverageWindow, claim) {
			out.Support = claimAddressed
		} else {
			out.Support = claimPartiallyAddressed
		}
	default:
		out.Support = claimPartiallyAddressed
	}
	return out
}
