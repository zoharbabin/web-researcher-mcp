package tools

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

func trackSources(ctx context.Context, deps Dependencies, sessionID string, sources []session.ResearchSource) {
	if len(sources) == 0 {
		return
	}
	tenantID := auth.TenantIDFromContext(ctx)
	userID := auth.UserIDFromContext(ctx)
	if err := deps.Sessions.AddSources(tenantID, userID, sessionID, sources); err != nil {
		slog.Debug("session source tracking skipped", "sessionId", sessionID, "err", err)
	}
}

// trackOutcome records one tool-outcome event against a session for cross-call
// error-pattern aggregation (#99). No-op when sessionID is empty. Best-effort:
// the session layer silently ignores a missing/expired session, and any error
// here never affects the calling tool's own result.
func trackOutcome(ctx context.Context, deps Dependencies, sessionID, provider string, success bool, errorKind, url string) {
	if sessionID == "" {
		return
	}
	tenantID := auth.TenantIDFromContext(ctx)
	userID := auth.UserIDFromContext(ctx)
	ev := session.OutcomeEvent{
		Provider:  provider,
		Success:   success,
		ErrorKind: errorKind,
		URL:       url,
		Timestamp: time.Now().Format(time.RFC3339),
	}
	if err := deps.Sessions.RecordOutcome(tenantID, userID, sessionID, ev); err != nil {
		slog.Debug("session outcome tracking skipped", "sessionId", sessionID, "err", err)
	}
}

// trackScrapeOutcome records a failed scrape against a session, mapping the
// typed ScrapeError kind to the shared ErrorKind taxonomy so session-level
// patterns (auth walls, blocks, browser gaps) line up with per-call errors.
func trackScrapeOutcome(ctx context.Context, deps Dependencies, sessionID, url string, err error) {
	if sessionID == "" {
		return
	}
	kind := string(ErrKindUpstream)
	var se *scraper.ScrapeError
	if errors.As(err, &se) {
		kind = string(mapScrapeErrorKind(se.Kind))
	}
	trackOutcome(ctx, deps, sessionID, "", false, kind, url)
}

func searchResultsToSources(results []search.SearchResult) []session.ResearchSource {
	sources := make([]session.ResearchSource, 0, len(results))
	for _, r := range results {
		if r.URL == "" {
			continue
		}
		sources = append(sources, session.ResearchSource{
			URL:   r.URL,
			Title: r.Title,
		})
	}
	return sources
}

func sourceOutputsToSources(outputs []sourceOutput) []session.ResearchSource {
	sources := make([]session.ResearchSource, 0, len(outputs))
	for _, o := range outputs {
		sources = append(sources, session.ResearchSource{
			URL:       o.URL,
			Title:     o.Title,
			Relevance: "scraped",
		})
	}
	return sources
}

func newsResultsToSources(results []search.NewsResult) []session.ResearchSource {
	sources := make([]session.ResearchSource, 0, len(results))
	for _, r := range results {
		if r.URL == "" {
			continue
		}
		sources = append(sources, session.ResearchSource{
			URL:   r.URL,
			Title: r.Title,
		})
	}
	return sources
}

// academicResultsToSources carries Author/Date/DOI onto the session source
// (#532) so format_bibliography on a session produces citation-complete
// entries without the caller re-supplying them explicitly.
func academicResultsToSources(results []search.AcademicResult) []session.ResearchSource {
	sources := make([]session.ResearchSource, 0, len(results))
	for _, r := range results {
		src := session.ResearchSource{
			URL:       r.URL,
			Title:     r.Title,
			Relevance: "academic",
			DOI:       r.DOI,
		}
		if len(r.Authors) > 0 {
			src.Author = strings.Join(r.Authors, "; ")
		}
		if r.Year > 0 {
			src.Date = strconv.Itoa(r.Year)
		}
		sources = append(sources, src)
	}
	return sources
}

// patentResultsToSources carries Author (inventor) and Date (filing date)
// onto the session source (#532) so a session-derived bibliography of patents
// isn't reduced to a bare title/URL.
func patentResultsToSources(results []scraper.PatentResult) []session.ResearchSource {
	sources := make([]session.ResearchSource, 0, len(results))
	for _, r := range results {
		sources = append(sources, session.ResearchSource{
			URL:       r.URL,
			Title:     r.Title,
			Relevance: "patent",
			Author:    r.Inventor,
			Date:      r.Filed,
		})
	}
	return sources
}
