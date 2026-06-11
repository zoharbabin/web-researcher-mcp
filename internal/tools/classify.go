package tools

import (
	"net/url"
	"strings"

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
	fields := map[string]any{
		"sourceType":     c.SourceType,
		"authorityTier":  c.AuthorityTier,
		"domainCategory": c.DomainCategory,
	}
	// Reputation (#159) is surfaced only when known — an unlisted host carries no
	// reputation signal, so the key is omitted rather than asserting "unknown".
	if c.DomainReputation != nil {
		fields["domainReputation"] = c.DomainReputation
	}
	return fields
}

// enrichResultsWithReputation returns web_search results as JSON objects,
// always attaching a sourceReputation field when the host is in the reputation
// dataset (#198). The field is omitted for unknown hosts (no false confidence).
// When claim is non-empty, a claimSignal (the most claim-relevant snippet
// sentence) is also added (#66).
func enrichResultsWithReputation(results []search.SearchResult, claim string) []map[string]any {
	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		m := map[string]any{
			"title":       r.Title,
			"url":         r.URL,
			"snippet":     r.Snippet,
			"displayLink": r.DisplayLink,
		}
		if rep := reputationForURL(r.URL); rep != nil {
			m["sourceReputation"] = rep
		}
		if claim != "" {
			if ev := content.ExtractClaimEvidence(r.Snippet, claim); ev.Signal != "" {
				m["claimSignal"] = ev.Signal
			}
		}
		out = append(out, m)
	}
	return out
}

// reputationForURL returns the domain reputation for a URL's host, or nil when
// the host is unknown (ReputationUnknown). Strips "www." before lookup.
func reputationForURL(rawURL string) *content.DomainReputation {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return nil
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	rep := content.LookupDomainReputation(host)
	if rep.Tier == "" || rep.Tier == content.ReputationUnknown {
		return nil
	}
	return &rep
}
