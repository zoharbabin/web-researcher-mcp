package tools

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
)

// verify_recommendation audits an AI recommendation (e.g. a product list or
// listicle) against anti-sloptimization signals. Given a list of recommendations
// with optional URLs and authors, it returns per-recommendation evidence:
// self-promotion patterns, conflicts of interest, source reputation, and dead
// links — helping you decide whether the recommendation is trustworthy or suspect.
//
// Read-only, openWorld (queries external sources for link liveness, domain
// reputation, and author conflicts).

type verifyRecommendationInput struct {
	Recommendations []recommendationItem `json:"recommendations" jsonschema:"Array of recommendations to audit. Each has: title (the recommendation), url (optional), author (optional), authorBio (optional). At least 1 required."`
}

type recommendationItem struct {
	Title     string `json:"title"`     // The recommended item (e.g. "Shopify" for a "best e-commerce platforms" listicle)
	URL       string `json:"url"`       // Optional: URL where the recommendation points
	Author    string `json:"author"`    // Optional: author name
	AuthorBio string `json:"authorBio"` // Optional: author affiliation or bio
}

type recommendationResult struct {
	Title               string                            `json:"title"`
	URL                 string                            `json:"url,omitempty"`
	Author              string                            `json:"author,omitempty"`
	SelfPromotionSignal *content.SelfPromotionSignal      `json:"selfPromotionSignal,omitempty"`
	ConflictOfInterest  *content.ConflictOfInterestSignal `json:"conflictOfInterest,omitempty"`
	DomainReputation    *content.DomainReputation         `json:"domainReputation,omitempty"`
	LinkLive            *bool                             `json:"linkLive,omitempty"`
	HTTPStatus          int                               `json:"httpStatus,omitempty"`
	Flags               []string                          `json:"flags"`
	Reasons             []string                          `json:"reasons"`
}

func registerVerifyRecommendation(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "verify_recommendation",
		Description:  "Audit an AI recommendation list against anti-sloptimization signals. Given a list of recommended items (products, services, articles), returns per-item evidence: self-promotion patterns (a brand ranking itself first), conflicts of interest (author employed by the recommended company), domain reputation (is this a known trustworthy source), and link liveness. Flags suspect recommendations so you can decide whether the list is gaming you or genuinely helpful. Built for catching GEO (Generative Engine Optimization) and brand-favoring listicles. Use alongside web_search + verify_citation to audit sources and claims.",
		Annotations:  readOnlyAnnotations(true, true),
		OutputSchema: verifyRecommendationOutputSchema,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input verifyRecommendationInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		if len(input.Recommendations) == 0 {
			return toolError("recommendations list is required and must not be empty"), nil, nil
		}
		if len(input.Recommendations) > 100 {
			return toolError("recommendations list is limited to 100 items"), nil, nil
		}

		results := []recommendationResult{}
		for _, rec := range input.Recommendations {
			result := verifyOneRecommendation(ctx, deps, rec)
			results = append(results, result)
		}

		out := map[string]any{
			"itemCount":       len(results),
			"recommendations": results,
			"trust":           untrustedContentTrust,
		}

		jsonBytes, _ := json.Marshal(out)
		recordToolCall(deps, "verify_recommendation", time.Since(start), nil, "", false)
		auditToolCallQuery(ctx, deps, "verify_recommendation", time.Since(start), nil, "", "", nil)
		return structuredResult(jsonBytes), nil, nil
	})
}

func verifyOneRecommendation(ctx context.Context, deps Dependencies, rec recommendationItem) recommendationResult {
	result := recommendationResult{
		Title:   rec.Title,
		URL:     rec.URL,
		Author:  rec.Author,
		Flags:   []string{},
		Reasons: []string{},
	}

	// Check conflict of interest: author affiliated with the recommended entity
	if rec.AuthorBio != "" && rec.Title != "" {
		if coi := content.DetectConflictOfInterest(rec.AuthorBio, rec.Title); coi != nil {
			result.ConflictOfInterest = coi
			result.Flags = append(result.Flags, "conflict_of_interest")
			result.Reasons = append(result.Reasons, "Author has a financial stake in the recommended entity: "+coi.Evidence)
		}
	}

	// Check domain reputation for the URL
	if rec.URL != "" {
		rep := reputationForURL(rec.URL)
		if rep != nil {
			result.DomainReputation = rep
		}

		// Check link liveness
		statuses := verifyLinkStatuses(ctx, deps, []string{rec.URL})
		if len(statuses) == 1 {
			st := statuses[0]
			result.LinkLive = &st.Live
			result.HTTPStatus = st.HTTPStatus
			if !st.Live {
				result.Flags = append(result.Flags, "dead_link")
				result.Reasons = append(result.Reasons, "Link does not resolve (HTTP "+strconv.Itoa(st.HTTPStatus)+")")
			}
		}

		// Check self-promotion: fetch the page and detect whether it is a ranking
		// list that puts its own host's brand first (e.g. shopify.com ranking
		// "1. Shopify"). Best-effort and fail-open — any fetch miss leaves the
		// signal unset, preserving the reputation/liveness result.
		if sp := detectSelfPromotionForURL(ctx, deps, rec.URL); sp != nil {
			result.SelfPromotionSignal = sp
			result.Flags = append(result.Flags, "self_promotion")
			result.Reasons = append(result.Reasons,
				"Source ranks its own brand (\""+sp.BrandToken+"\") at position "+strconv.Itoa(sp.RankPosition)+" in its list")
		}
	}

	return result
}

// detectSelfPromotionForURL fetches rawURL and reports whether the page is a
// ranking list that puts the page's own-domain brand first. Returns nil on any
// fetch error, empty body, or when the pattern is absent (conservative).
func detectSelfPromotionForURL(ctx context.Context, deps Dependencies, rawURL string) *content.SelfPromotionSignal {
	if deps.Scraper == nil || rawURL == "" {
		return nil
	}
	res, err := deps.Scraper.Scrape(ctx, rawURL, auditClaimScrapeMaxBytes)
	if err != nil || res == nil || res.Content == "" {
		return nil
	}
	host := hostForURL(rawURL)
	if host == "" {
		return nil
	}
	return content.DetectSelfPromotion(host, res.Content)
}

var verifyRecommendationOutputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"itemCount": map[string]any{"type": "integer", "description": "Number of recommendations audited."},
		"recommendations": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":               map[string]any{"type": "string", "description": "The recommended item (echoed)."},
					"url":                 map[string]any{"type": "string", "description": "URL of the recommendation (echoed when provided)."},
					"author":              map[string]any{"type": "string", "description": "Author name (echoed when provided)."},
					"selfPromotionSignal": map[string]any{"type": "object", "description": "Present when the linked page is a ranking list that places its own host's brand first (e.g. a brand blog ranking itself #1). Detected by fetching the URL."},
					"conflictOfInterest": map[string]any{
						"type":        "object",
						"description": "Present when the author has a detected financial stake in the recommended entity. Employment / funding / equity connections.",
						"properties": map[string]any{
							"detected":          map[string]any{"type": "boolean"},
							"authorAffiliation": map[string]any{"type": "string"},
							"conflictType":      map[string]any{"type": "string", "enum": []any{"employment", "funded_by", "owns_equity"}},
							"citedEntityName":   map[string]any{"type": "string"},
							"evidence":          map[string]any{"type": "string"},
							"confidence":        map[string]any{"type": "string", "enum": []any{"high", "medium", "low"}},
						},
					},
					"domainReputation": map[string]any{
						"type":        "object",
						"description": "Domain reputation when the URL host is in the known sources dataset. Omitted for unlisted hosts.",
					},
					"linkLive":   map[string]any{"type": "boolean", "description": "True when the URL resolves (2xx/3xx HTTP); false when dead."},
					"httpStatus": map[string]any{"type": "integer", "description": "Live HTTP status for the URL (0 = unreachable/timeout)."},
					"flags": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string", "enum": []any{"self_promotion", "conflict_of_interest", "dead_link", "unknown_reputation", "low_reputation"}},
						"description": "Audit flags. Empty = no issues detected. Treat as evidence, not verdicts.",
					},
					"reasons": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Human-readable explanations for any flags.",
					},
				},
			},
		},
		"trust": trustUntrustedExternal,
	},
}
