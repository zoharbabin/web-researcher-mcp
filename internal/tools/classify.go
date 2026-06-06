package tools

import (
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// classifySource computes the typed source classification (#62) for a scraped
// page: source_type, authority_tier, domain_category. It derives the numeric
// authority from ScoreQuality (reusing the shipped scorer, no new scoring) and
// feeds the decoupled structured-data signals + active lens into
// content.ClassifySource. `lens` may be "" (no lens active).
//
// Shared by scrape_page and search_and_scrape so both surfaces classify
// identically.
func classifySource(url, title, body, query, lens string, structured *scraper.StructuredData) content.SourceClassification {
	score := content.ScoreQuality(content.QualityInput{
		Content: body,
		URL:     url,
		Title:   title,
		Query:   query,
	})
	return content.ClassifySource(url, score.Authority, structured.Signals(), lens)
}

// classificationFields renders a SourceClassification as the additive output
// keys, so the three fields are written identically across tools.
func classificationFields(c content.SourceClassification) map[string]any {
	return map[string]any{
		"sourceType":     c.SourceType,
		"authorityTier":  c.AuthorityTier,
		"domainCategory": c.DomainCategory,
	}
}

// enrichResultsWithClaim renders web_search results as JSON objects, adding a
// claimSignal (the most claim-relevant sentence from the snippet) when one is
// found (#66). Snippets are short, so only the single Signal is surfaced — for
// full-text key sentences callers use search_and_scrape with claim. Preserves
// the SearchResult field shape so the only difference vs the default path is the
// added claimSignal key.
func enrichResultsWithClaim(results []search.SearchResult, claim string) []map[string]any {
	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		m := map[string]any{
			"title":       r.Title,
			"url":         r.URL,
			"snippet":     r.Snippet,
			"displayLink": r.DisplayLink,
		}
		if ev := content.ExtractClaimEvidence(r.Snippet, claim); ev.Signal != "" {
			m["claimSignal"] = ev.Signal
		}
		out = append(out, m)
	}
	return out
}
