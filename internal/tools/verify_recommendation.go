package tools

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// verify_recommendation audits an AI recommendation (e.g. a product list or
// listicle) against anti-sloptimization signals. Given a list of recommendations
// with optional URLs and authors, it returns per-recommendation evidence:
// self-promotion patterns, conflicts of interest, source reputation, and dead
// links — helping you decide whether the recommendation is trustworthy or suspect.
//
// Read-only, openWorld (queries external sources for link liveness, domain
// reputation, and author conflicts).

// defaultCorroborationResults is the number of results to fetch per lens when
// corroboration is requested but NumCorroborationResults is unset (0).
const defaultCorroborationResults = 5

type verifyRecommendationInput struct {
	Recommendations         []recommendationItem `json:"recommendations" jsonschema:"Array of recommendations to audit. Each has: title (the recommendation), url (optional), author (optional), authorBio (optional). At least 1 required."`
	Claim                   string               `json:"claim,omitempty" jsonschema:"Optional claim or context describing what the recommendation list is about (e.g. 'best e-commerce platforms for small businesses'). When set, triggers corroboration searches across independent journalism and tech sources to surface agreement/disagreement with each recommendation."`
	NumCorroborationResults int                  `json:"numCorroborationResults,omitempty" jsonschema:"Number of search results to fetch per lens per recommendation when claim is set. Default 5, max 10."`
}

type recommendationItem struct {
	Title     string `json:"title"`
	URL       string `json:"url,omitempty"`
	Author    string `json:"author,omitempty"`
	AuthorBio string `json:"authorBio,omitempty"`
}

// corroborationResult holds the outcome of one corroboration search — a query
// issued against a specific lens to find independent coverage of a recommendation.
type corroborationResult struct {
	Query         string           `json:"query"`
	Lens          string           `json:"lens"`
	ResultCount   int              `json:"resultCount"`
	AgreeCount    int              `json:"agreeCount"`
	DisagreeCount int              `json:"disagreeCount"`
	SilentCount   int              `json:"silentCount"`
	TopResults    []map[string]any `json:"topResults,omitempty"`
}

type recommendationResult struct {
	Title                 string                            `json:"title"`
	URL                   string                            `json:"url,omitempty"`
	Author                string                            `json:"author,omitempty"`
	SelfPromotionSignal   *content.SelfPromotionSignal      `json:"selfPromotionSignal,omitempty"`
	ConflictOfInterest    *content.ConflictOfInterestSignal `json:"conflictOfInterest,omitempty"`
	DomainReputation      *content.DomainReputation         `json:"domainReputation,omitempty"`
	LinkLive              *bool                             `json:"linkLive,omitempty"`
	HTTPStatus            int                               `json:"httpStatus,omitempty"`
	CorroborationSearches []corroborationResult             `json:"corroborationSearches,omitempty"`
	Flags                 []string                          `json:"flags"`
	Reasons               []string                          `json:"reasons"`
}

func registerVerifyRecommendation(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "verify_recommendation",
		Description:  "Audit an AI recommendation list against anti-sloptimization signals. Given a list of recommended items (products, services, articles), returns per-item evidence: self-promotion patterns (a brand ranking itself first), conflicts of interest (author employed by the recommended company), domain reputation (is this a known trustworthy source), link liveness, and — when a claim is provided — corroboration searches across independent journalism and tech sources that show how widely each recommendation is independently endorsed or contested. Flags suspect recommendations so you can decide whether the list is gaming you or genuinely helpful. Built for catching GEO (Generative Engine Optimization) and brand-favoring listicles. Use alongside web_search + verify_citation to audit sources and claims.",
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

		numCorroboration := input.NumCorroborationResults
		if numCorroboration <= 0 {
			numCorroboration = defaultCorroborationResults
		}
		if numCorroboration > 10 {
			numCorroboration = 10
		}

		results := []recommendationResult{}
		for _, rec := range input.Recommendations {
			result := verifyOneRecommendation(ctx, deps, rec, input.Claim, numCorroboration)
			results = append(results, result)
		}

		// Aggregate flag: fired when a claim was given but NOT ONE recommendation
		// received any independent agreement across all corroboration lenses.
		aggregateFlags := []string{}
		if input.Claim != "" {
			totalAgree := 0
			for _, r := range results {
				for _, cs := range r.CorroborationSearches {
					totalAgree += cs.AgreeCount
				}
			}
			if totalAgree == 0 {
				aggregateFlags = append(aggregateFlags, "no_independent_corroboration")
			}
		}

		out := map[string]any{
			"itemCount":       len(results),
			"recommendations": results,
			"trust":           untrustedContentTrust,
		}
		if len(aggregateFlags) > 0 {
			out["aggregateFlags"] = aggregateFlags
		}

		jsonBytes, _ := json.Marshal(out)
		recordToolCall(deps, "verify_recommendation", time.Since(start), nil, "", false)
		auditToolCallQuery(ctx, deps, "verify_recommendation", time.Since(start), nil, "", "", nil)
		return structuredResult(jsonBytes), nil, nil
	})
}

func verifyOneRecommendation(ctx context.Context, deps Dependencies, rec recommendationItem, claim string, numResults int) recommendationResult {
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

	// Corroboration search (#246): query independent journalism and tech lenses
	// to find sources that agree, disagree, or are silent about this recommendation
	// in the context of the caller's claim. Skipped when no claim is provided or
	// when the item has no title to search for. Fail-open — a provider error or
	// missing lens leaves CorroborationSearches nil rather than failing the audit.
	if claim != "" && rec.Title != "" {
		result.CorroborationSearches = corroborateRecommendation(ctx, deps, rec.Title, claim, numResults)
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

// corroborationLenses are the lens names searched in order. journalism covers
// government/public-record/filing sources (sec.gov, courtlistener.com,
// data.gov, ...); tech covers independent tech press. Both are independent of
// the recommendation author's domain, making them resistant to brand-controlled
// or sponsored content.
var corroborationLenses = []string{"journalism", "tech"}

// corroborateRecommendation issues one web search per corroborationLens for
// the recommended item title within the caller's claim context. It counts how
// many results address the recommendation positively (agree), negatively
// (disagree), or neutrally/silently (silent). Each result's claimSignal is the
// single most claim-relevant snippet sentence (content.ExtractClaimEvidence),
// not a fixed enum: an empty signal means no sentence mentioned the title
// (silentCount); a non-empty signal carrying a negation/refutation cue
// (content.HasContrastCue) means independent coverage disputes it
// (disagreeCount); any other non-empty signal means independent agreement
// (agreeCount).
//
// The function is fail-open: a nil provider, missing lens, or network error
// produces an empty slice rather than propagating an error — the audit's
// reputation/liveness signals are unaffected. deps.Search (the default provider
// or router) is used; provider-agnostic, no hardcoded preference.
func corroborateRecommendation(ctx context.Context, deps Dependencies, title, claim string, numResults int) []corroborationResult {
	if deps.Search == nil {
		return nil
	}
	registry := search.GetLensRegistry()
	var corroborations []corroborationResult
	for _, lensName := range corroborationLenses {
		lensData, ok := registry.Get(lensName)
		if !ok {
			continue
		}
		query := registry.BuildSiteQuery(title+" "+claim, lensData)
		results, err := deps.Search.Web(ctx, search.WebSearchParams{
			Query:      query,
			NumResults: numResults,
		})
		if err != nil || len(results) == 0 {
			continue
		}
		enriched := enrichResultsWithReputation(results, title)
		cr := corroborationResult{
			Query:       query,
			Lens:        lensName,
			ResultCount: len(enriched),
		}
		for _, r := range enriched {
			signal, _ := r["claimSignal"].(string)
			switch {
			case signal == "":
				// No snippet sentence mentioned the title at all — independent silence.
				cr.SilentCount++
			case content.HasContrastCue([]string{signal}):
				// The most relevant sentence carries a negation/refutation cue —
				// independent coverage that disputes the recommendation.
				cr.DisagreeCount++
			default:
				// A relevant sentence with no refutation cue — independent agreement.
				cr.AgreeCount++
			}
		}
		cr.TopResults = enriched
		corroborations = append(corroborations, cr)
	}
	return corroborations
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
					"corroborationSearches": map[string]any{
						"type":        "array",
						"description": "Present when the `claim` field was supplied. One entry per corroboration lens (journalism, tech). Shows whether independent sources agree, disagree, or are silent about this recommendation in the context of the claim.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"query":         map[string]any{"type": "string", "description": "The site-scoped query issued against this lens."},
								"lens":          map[string]any{"type": "string", "description": "Lens name used (e.g. 'journalism', 'tech')."},
								"resultCount":   map[string]any{"type": "integer", "description": "Total results returned by the search."},
								"agreeCount":    map[string]any{"type": "integer", "description": "Results whose snippet addresses the recommendation positively in context of the claim."},
								"disagreeCount": map[string]any{"type": "integer", "description": "Results whose snippet contradicts or does not address the recommendation."},
								"silentCount":   map[string]any{"type": "integer", "description": "Results that mention the item but neither agree nor disagree with the claim context."},
								"topResults":    map[string]any{"type": "array", "description": "Enriched search results including claimSignal and sourceReputation per result."},
							},
						},
					},
					"flags": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string", "enum": []any{"self_promotion", "conflict_of_interest", "dead_link", "unknown_reputation", "low_reputation"}},
						"description": "Per-item audit flags. Empty = no issues detected. Treat as evidence, not verdicts.",
					},
					"reasons": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Human-readable explanations for any flags.",
					},
				},
			},
		},
		"aggregateFlags": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string", "enum": []any{"no_independent_corroboration"}},
			"description": "Aggregate flags across all recommendations (present only when `claim` was given). 'no_independent_corroboration' fires when zero results across all lenses agreed with any recommendation — a strong signal the list may be AI-generated or sponsored without independent validation.",
		},
		"trust": trustUntrustedExternal,
	},
}
