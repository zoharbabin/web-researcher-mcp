package tools

import (
	"context"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

// verifyLinkStatuses runs the SSRF-safe link verifier over a set of URLs and
// returns the results, or nil when there's nothing to do. Shared by
// research_export, search_and_scrape, and verify_citation so liveness/archive
// behavior is identical everywhere. Bounded + best-effort by the verifier's own
// contract; deps.LinkVerifier is nil-safe (returns nil → callers no-op).
func verifyLinkStatuses(ctx context.Context, deps Dependencies, urls []string) []scraper.LinkStatus {
	if deps.LinkVerifier == nil || len(urls) == 0 {
		return nil
	}
	return deps.LinkVerifier.VerifyAll(ctx, urls)
}

// setLinkOutcomeFields adds the additive link-check fields and keeps a URL that
// could not be verified honest: a blocked, root-redirected or unreachable link is
// neither confirmed nor not_found, so it reports verificationStatus "uncertain"
// (setVerificationStatus never overwrites it). Only a live link confirms, and only
// a dead link (HTTP error or DNS failure) stays not_found.
func setLinkOutcomeFields(out map[string]any, st scraper.LinkStatus) {
	if st.Outcome != "" {
		out["linkOutcome"] = string(st.Outcome)
	}
	if st.FinalURL != "" && st.FinalURL != st.URL {
		out["finalUrl"] = st.FinalURL
	}
	if st.FailureCause != "" {
		out["linkFailure"] = st.FailureCause
	}
	switch st.Outcome {
	case scraper.LinkOutcomeBlocked, scraper.LinkOutcomeRedirectedToRoot, scraper.LinkOutcomeUnreachable:
		out["verificationStatus"] = verificationUncertain
	}
}

// archiveURL triggers a fresh Internet Archive (Save Page Now) capture of rawURL
// via the SSRF-safe verifier (#196). nil-safe: returns a zero ArchiveResult and
// ok=false when no verifier is configured, so the archive_source tool can report
// status "unavailable" gracefully rather than erroring.
func archiveURL(ctx context.Context, deps Dependencies, rawURL string) (scraper.ArchiveResult, bool) {
	if deps.LinkVerifier == nil {
		return scraper.ArchiveResult{}, false
	}
	return deps.LinkVerifier.Archive(ctx, rawURL), true
}

// annotateSourcesWithLiveness verifies each source's URL and writes the liveness
// provenance (httpStatus/verified/archivedUrl/verifiedAt) back onto the source
// in place. Best-effort: a nil verifier or empty input is a no-op. The session
// is NOT persisted by this — verification annotates the export/response only, so
// repeated exports re-check freshly rather than caching a stale verdict.
func annotateSourcesWithLiveness(ctx context.Context, deps Dependencies, sources []session.ResearchSource) {
	if deps.LinkVerifier == nil || len(sources) == 0 {
		return
	}
	urls := make([]string, len(sources))
	for i := range sources {
		urls[i] = sources[i].URL
	}
	statuses := deps.LinkVerifier.VerifyAll(ctx, urls)
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range statuses {
		s := &sources[i]
		st := statuses[i]
		s.HTTPStatus = st.HTTPStatus
		live := st.Live
		s.Verified = &live
		s.VerifiedAt = now
		if !st.Live && st.ArchivedURL != "" {
			s.ArchivedURL = st.ArchivedURL
		}
	}
}
