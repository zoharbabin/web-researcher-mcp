package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
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
	Title               string                       `json:"title"`
	URL                 string                       `json:"url,omitempty"`
	Author              string                       `json:"author,omitempty"`
	SelfPromotionSignal *content.SelfPromotionSignal `json:"selfPromotionSignal,omitempty"`
	// CorporateOwnershipSignal is present when lexical self-promotion was not
	// detected but a Wikidata P749 lookup found the domain brand is owned by a
	// distinct corporate parent (e.g. marketo.com → owner "Adobe Inc."). Evidence
	// only — the caller decides whether the corporate parent's recommendation is
	// self-interested.
	CorporateOwnershipSignal *content.SelfPromotionSignal      `json:"corporateOwnershipSignal,omitempty"`
	ConflictOfInterest       *content.ConflictOfInterestSignal `json:"conflictOfInterest,omitempty"`
	DomainReputation         *content.DomainReputation         `json:"domainReputation,omitempty"`
	LinkLive                 *bool                             `json:"linkLive,omitempty"`
	HTTPStatus               int                               `json:"httpStatus,omitempty"`
	CorroborationSearches    []corroborationResult             `json:"corroborationSearches,omitempty"`
	Flags                    []string                          `json:"flags"`
	Reasons                  []string                          `json:"reasons"`
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
		} else {
			// Lexical check found no self-promotion; try the Wikidata corporate
			// ownership fallback (#248). Only runs when the resolver is configured.
			if ownerSignal := detectCorporateOwnershipForURL(ctx, deps, rec.URL); ownerSignal != nil {
				result.CorporateOwnershipSignal = ownerSignal
				result.Flags = append(result.Flags, "corporate_ownership")
				result.Reasons = append(result.Reasons,
					"Domain brand (\""+ownerSignal.BrandToken+"\") is owned by \""+
						ownerSignal.CorporateOwner+"\" (Wikidata P749)")
			}
		}
	}

	// Corroboration search (#246): query independent news and tech lenses
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

// wikidataOwnershipCacheTTL is how long a Wikidata P749 lookup result — found
// or not-found — is cached. Ownership rarely changes, and negative caching
// avoids hammering Wikidata for domains with no corporate parent.
const wikidataOwnershipCacheTTL = 7 * 24 * time.Hour

// wikidataOwnershipCacheEntry is the cached shape for a corporate-ownership
// lookup, capturing both positive and negative (Found=false) results so a
// repeat call never re-invokes the resolver within the TTL.
type wikidataOwnershipCacheEntry struct {
	Found  bool                    `json:"found"`
	Result *search.OwnershipResult `json:"result,omitempty"`
}

// wikidataOwnershipCacheKey derives the cache key for a brand token's
// ownership lookup, namespaced from any other cache use of the same token.
func wikidataOwnershipCacheKey(brandToken string) string {
	h := sha256.Sum256([]byte("wikidata-ownership:" + brandToken))
	return fmt.Sprintf("%x", h)
}

// brandTokenFromHost extracts the registrable stem from a hostname.
// "marketo.com" → "marketo", "mail.marketo.com" → "marketo", "go.dev" → "go".
// Returns "" when the token is a single character (too ambiguous, e.g.
// "x.co" → ""). Mirrors DetectSelfPromotion's own brandToken derivation in
// classify.go.
func brandTokenFromHost(host string) string {
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return ""
	}
	token := strings.ToLower(parts[len(parts)-2])
	if len(token) < 2 {
		return ""
	}
	return token
}

// detectCorporateOwnershipForURL resolves the URL's domain brand token against
// Wikidata P749 (parent organization). Returns nil when: resolver is nil, no
// Wikidata entity found, no P749 parent, or any error (fail-open). Results are
// cached under "wikidata-ownership:{brandToken}" with a 7-day TTL, including
// negative (not-found) results.
func detectCorporateOwnershipForURL(ctx context.Context, deps Dependencies, rawURL string) *content.SelfPromotionSignal {
	if deps.WikidataOwnershipResolver == nil || rawURL == "" {
		return nil
	}
	host := hostForURL(rawURL)
	if host == "" {
		return nil
	}
	brandToken := brandTokenFromHost(host)
	if brandToken == "" {
		return nil
	}

	cacheKey := wikidataOwnershipCacheKey(brandToken)
	if deps.Cache != nil {
		if cached, ok := deps.Cache.Get(ctx, cacheKey); ok {
			var entry wikidataOwnershipCacheEntry
			if json.Unmarshal(cached, &entry) == nil {
				if !entry.Found || entry.Result == nil {
					return nil
				}
				return &content.SelfPromotionSignal{
					Detected:          true,
					BrandDomain:       host,
					BrandToken:        brandToken,
					CorporateOwner:    entry.Result.OwnerLabel,
					CorporateOwnerQID: entry.Result.OwnerQID,
					Confidence:        "medium",
				}
			}
		}
	}

	result, found, err := deps.WikidataOwnershipResolver.Resolve(ctx, brandToken)
	if deps.Cache != nil && err == nil {
		entry := wikidataOwnershipCacheEntry{Found: found, Result: result}
		if b, merr := json.Marshal(entry); merr == nil {
			deps.Cache.Set(ctx, cacheKey, b, wikidataOwnershipCacheTTL)
		}
	}
	if err != nil || !found || result == nil {
		return nil
	}

	return &content.SelfPromotionSignal{
		Detected:          true,
		BrandDomain:       host,
		BrandToken:        brandToken,
		CorporateOwner:    result.OwnerLabel,
		CorporateOwnerQID: result.OwnerQID,
		Confidence:        "medium",
	}
}

// genericCorroborationLenses are searched for every claim: news (Reuters, AP,
// BBC, NYT, The Guardian, ...) and tech (Ars Technica, TechCrunch, The Verge,
// Wired, ...) — both independent of the recommendation author's domain, making
// them resistant to brand-controlled or sponsored content.
var genericCorroborationLenses = []string{"news", "tech"}

// corporateGovLegalKeywords trigger routing to the investigative_records lens
// in addition to the generic set (see #434 Finding D). lenses/investigative_records.json
// (renamed from journalism in #535, since it was never about news media) is
// scoped to government/public-record/corporate-filing domains (sec.gov,
// courtlistener.com, opencorporates.com, data.gov, census.gov, federalregister.gov,
// opensecrets.org, foia.gov, congress.gov, gao.gov) — the right lens for claims
// about corporate, governmental, legal, or financial matters, not generic
// tech/product claims.
var corporateGovLegalKeywords = []string{
	"sec filing", "10-k", "10-q", "8-k", "proxy advisory", "proxy advisor",
	"shareholder", "securities", "lawsuit", "litigation", "court", "regulator",
	"regulatory", "compliance", "antitrust", "merger", "acquisition", "earnings",
	"financial statement", "audit", "fraud", "sanction", "congress", "legislation",
	"government contract", "federal", "sec.gov", "ftc", "doj", "esg",
}

// selectCorroborationLenses classifies claim+title text and returns the lens
// set to search (#434 Finding D): generic/tech/product claims use
// {news, tech}; claims about corporate/gov/legal/financial matters
// additionally route to investigative_records.
func selectCorroborationLenses(title, claim string) []string {
	lenses := append([]string{}, genericCorroborationLenses...)
	text := strings.ToLower(title + " " + claim)
	for _, kw := range corporateGovLegalKeywords {
		if strings.Contains(text, kw) {
			return append(lenses, "investigative_records")
		}
	}
	return lenses
}

// corroborateRecommendation issues one web search per lens selected by
// selectCorroborationLenses for the recommended item title within the
// caller's claim context. It counts how many results address the
// recommendation positively (agree), negatively
// (disagree), or neutrally/silently (silent). Each result's claimSignal is the
// single most claim-relevant snippet sentence (content.ExtractClaimEvidence),
// computed against the FULL "title + claim" text (not the bare title alone)
// so the more discriminating claim terms take part in matching (#600).
//
// A result only counts toward agreeCount when its snippet clears
// content.ClaimTermCoverageWindowed's claimAddressedThreshold ratio against
// "title + claim" — not merely >=1 term matched. A single coincidental token
// match (e.g. claim "React 19 introduced Actions" against a snippet
// mentioning "COVID-19" or "reacted") no longer counts as agreement; it falls
// to silentCount instead, matching the ratio-gated approach already shipped
// for audit_bibliography/verify_citation in claim_coverage.go (#600).
//
// The disagree check also scans the result's title, not just claimSignal:
// enrichResultsWithReputation only extracts claim evidence from the snippet
// (#66, documented in docs/TOOLS.md), so refutation language that lands in a
// headline rather than the snippet body — e.g. a title like "CDC website now
// falsely links vaccines and autism" backed by an unrelated snippet — would
// otherwise be missed and mistallied as silent/agree. This is scoped to the
// corroboration tally only; it does not change claimSignal's public,
// documented snippet-only contract used by web_search. Disagree takes
// priority over the coverage gate: a title-only refutation counts as
// disagreement even when the snippet itself shows no claim-term coverage.
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
	fullClaim := title + " " + claim
	var corroborations []corroborationResult
	for _, lensName := range selectCorroborationLenses(title, claim) {
		lensData, ok := registry.Get(lensName)
		if !ok {
			continue
		}
		query := registry.BuildSiteQuery(fullClaim, lensData)
		results, err := deps.Search.Web(ctx, search.WebSearchParams{
			Query:      query,
			NumResults: numResults,
		})
		if err != nil || len(results) == 0 {
			continue
		}
		enriched := enrichResultsWithReputation(results, fullClaim)
		cr := corroborationResult{
			Query:       query,
			Lens:        lensName,
			ResultCount: len(enriched),
		}
		for _, r := range enriched {
			signal, _ := r["claimSignal"].(string)
			resultTitle, _ := r["title"].(string)
			snippet, _ := r["snippet"].(string)
			matched, total := content.ClaimTermCoverageWindowed(snippet, fullClaim, 0)
			addressed := total > 0 && float64(matched)/float64(total) >= claimAddressedThreshold
			switch {
			case content.HasContrastCue([]string{signal, resultTitle}):
				// The claimSignal sentence or the result's own title carries a
				// negation/refutation cue — independent coverage that disputes
				// the recommendation (checking the title too catches refutation
				// language that lands in a headline but not the snippet).
				cr.DisagreeCount++
			case !addressed:
				// The snippet doesn't clear the claim-term coverage ratio — no
				// contrast cue either, so this is independent silence rather
				// than a coincidental token match counted as agreement.
				cr.SilentCount++
			default:
				// Coverage clears the threshold with no refutation cue —
				// independent agreement.
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
					"title":                    map[string]any{"type": "string", "description": "The recommended item (echoed)."},
					"url":                      map[string]any{"type": "string", "description": "URL of the recommendation (echoed when provided)."},
					"author":                   map[string]any{"type": "string", "description": "Author name (echoed when provided)."},
					"selfPromotionSignal":      map[string]any{"type": "object", "description": "Present when the linked page is a ranking list that places its own host's brand first (e.g. a brand blog ranking itself #1). Detected by fetching the URL."},
					"corporateOwnershipSignal": map[string]any{"type": "object", "description": "Present when lexical self-promotion was not detected but a Wikidata P749 lookup found the domain brand is owned by a distinct corporate parent (e.g. marketo.com → owner \"Adobe Inc.\"). Evidence only. Results are cached 7 days."},
					"conflictOfInterest": map[string]any{
						"type":        "object",
						"description": "Present when the author has a detected financial stake in the recommended entity. Employment / funding / equity connections." + languageHeuristicCaveat,
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
						"description": "Present when the `claim` field was supplied. One entry per corroboration lens, selected by classifying the claim/title text: generic/tech/product claims search {news, tech}; claims about corporate/government/legal/financial matters additionally search {investigative_records} (gov/public-record/filing sources — sec.gov, courtlistener.com, data.gov, ...). Shows whether independent sources agree, disagree, or are silent about this recommendation in the context of the claim.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"query":         map[string]any{"type": "string", "description": "The site-scoped query issued against this lens."},
								"lens":          map[string]any{"type": "string", "description": "Lens name used (e.g. 'news', 'tech', 'investigative_records')."},
								"resultCount":   map[string]any{"type": "integer", "description": "Total results returned by the search."},
								"agreeCount":    map[string]any{"type": "integer", "description": "Results whose snippet addresses the recommendation positively in context of the claim."},
								"disagreeCount": map[string]any{"type": "integer", "description": "Results whose snippet or title contradicts or does not address the recommendation." + languageHeuristicCaveat},
								"silentCount":   map[string]any{"type": "integer", "description": "Results that mention the item but neither agree nor disagree with the claim context." + languageHeuristicCaveat},
								"topResults":    map[string]any{"type": "array", "description": "Enriched search results including claimSignal and sourceReputation per result." + languageHeuristicCaveat},
							},
						},
					},
					"flags": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string", "enum": []any{"self_promotion", "corporate_ownership", "conflict_of_interest", "dead_link", "unknown_reputation", "low_reputation"}},
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
