package tools

import (
	"context"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// Shared OA-location resolution (#657): paper_fulltext and verify_citation each
// independently trusted a different provider's cached "best open-access copy"
// field — paper_fulltext preferred Semantic Scholar's openAccessPdf, falling
// back to a bare Unpaywall lookup; verify_citation used only OpenAlex's cached
// open_access.oa_url, never consulting Unpaywall at all. Because these are three
// separately crawled/cached snapshots maintained on different refresh cadences,
// the same DOI can legitimately resolve to a different (and independently
// stale) URL from each tool. gatherOACandidates collects every signal in one
// documented priority order; firstLiveOACandidate verifies and falls through a
// dead/403 pick instead of surfacing it unconditionally.

// maxOALivenessChecks bounds how many OA candidates get a liveness check before
// trusting the top-ranked one — unbounded checking would multiply latency
// across every paper_fulltext call.
const maxOALivenessChecks = 3

// unpaywallPDFCandidate returns Unpaywall's live best_oa_location PDF for doi,
// or "" when unresolved, not found, or deps.OAResolver is unconfigured.
// Best-effort: an Unpaywall error is treated the same as "no candidate",
// never propagated.
func unpaywallPDFCandidate(ctx context.Context, deps Dependencies, doi string) string {
	if doi == "" || deps.OAResolver == nil {
		return ""
	}
	if _, pdf, found, err := deps.OAResolver.Resolve(ctx, doi); err == nil && found {
		return pdf
	}
	return ""
}

// gatherOACandidates returns deduped OA-location URL candidates for doi, in
// priority order: seed's own PDFUrl (the caller's provider-native pick, e.g.
// Semantic Scholar's cached openAccessPdf), then Unpaywall's live
// best_oa_location, then openAlexRec's cached open_access.oa_url (an exact-DOI
// entity lookup via the DOIResolver capability). Either seed or openAlexRec may
// be nil. Best-effort throughout — an unconfigured or erroring resolver just
// omits that candidate.
func gatherOACandidates(ctx context.Context, deps Dependencies, doi string, seed, openAlexRec *search.AcademicResult) []string {
	var out []string
	seen := make(map[string]struct{}, 3)
	add := func(u string) {
		if u == "" {
			return
		}
		if _, dup := seen[u]; dup {
			return
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	if seed != nil {
		add(seed.PDFUrl)
	}
	add(unpaywallPDFCandidate(ctx, deps, doi))
	if openAlexRec != nil {
		add(openAlexRec.PDFUrl)
	}
	return out
}

// firstLiveOACandidate verifies candidates (capped at maxOALivenessChecks) via
// the configured link verifier and returns the first one reported live — a
// dead or 403'd top-ranked cache entry falls through to the next candidate
// instead of being returned unconditionally. When no LinkVerifier is
// configured, degrades to "trust the first candidate" (never a regression for a
// deployment without one). When none verify live, still returns the top-ranked
// candidate rather than nothing — a false-negative liveness check (e.g. a
// HEAD-blocking host) must not turn a real, scrapeable URL into no URL at all.
// Returns "" only when candidates is empty.
func firstLiveOACandidate(ctx context.Context, deps Dependencies, candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	if deps.LinkVerifier == nil {
		return candidates[0]
	}
	checked := candidates
	if len(checked) > maxOALivenessChecks {
		checked = checked[:maxOALivenessChecks]
	}
	statuses := verifyLinkStatuses(ctx, deps, checked)
	for i, st := range statuses {
		if st.Live {
			return checked[i]
		}
	}
	return candidates[0]
}

// mergeOAResultMeta returns a copy of meta with PDFUrl/OpenAccess set to the OA
// candidate that actually won liveness verification (which may differ from
// meta's own cached pick when that one didn't verify live), so the tool
// output's pdfUrl field always agrees with resolvedUrl. meta may be nil (no
// metadata available at all); a bare DOI-only result is synthesized in that
// case, mirroring the existing Source:"unpaywall" convention.
func mergeOAResultMeta(meta *search.AcademicResult, doi, pdf string) *search.AcademicResult {
	if meta == nil {
		return &search.AcademicResult{DOI: doi, PDFUrl: pdf, OpenAccess: true, Source: "unpaywall"}
	}
	out := *meta
	out.PDFUrl = pdf
	out.OpenAccess = true
	return &out
}

// prependCandidate inserts u at the front of candidates, deduped — if u already
// appears later in the list, it is moved to the front rather than duplicated.
// A no-op when u is "".
func prependCandidate(candidates []string, u string) []string {
	if u == "" {
		return candidates
	}
	out := make([]string, 0, len(candidates)+1)
	out = append(out, u)
	for _, c := range candidates {
		if c != u {
			out = append(out, c)
		}
	}
	return out
}
