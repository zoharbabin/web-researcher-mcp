package tools

import (
	"context"
	"log/slog"

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
	if err := deps.Sessions.AddSources(tenantID, sessionID, sources); err != nil {
		slog.Debug("session source tracking skipped", "sessionId", sessionID, "err", err)
	}
}

func searchResultsToSources(results []search.SearchResult) []session.ResearchSource {
	sources := make([]session.ResearchSource, 0, len(results))
	for _, r := range results {
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
		sources = append(sources, session.ResearchSource{
			URL:   r.URL,
			Title: r.Title,
		})
	}
	return sources
}

func academicResultsToSources(results []search.AcademicResult) []session.ResearchSource {
	sources := make([]session.ResearchSource, 0, len(results))
	for _, r := range results {
		sources = append(sources, session.ResearchSource{
			URL:       r.URL,
			Title:     r.Title,
			Relevance: "academic",
		})
	}
	return sources
}

func patentResultsToSources(results []scraper.PatentResult) []session.ResearchSource {
	sources := make([]session.ResearchSource, 0, len(results))
	for _, r := range results {
		sources = append(sources, session.ResearchSource{
			URL:       r.URL,
			Title:     r.Title,
			Relevance: "patent",
		})
	}
	return sources
}
