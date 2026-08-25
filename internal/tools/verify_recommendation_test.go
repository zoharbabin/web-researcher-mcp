package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

func TestVerifyRecommendationConflictOfInterest(t *testing.T) {
	// Test: Shopify employee recommending Shopify should detect conflict
	coi := content.DetectConflictOfInterest(
		"Jane is a senior developer at Shopify with 5 years experience",
		"Shopify — Top-rated e-commerce platform with excellent API support",
	)

	if coi == nil || !coi.Detected {
		t.Fatalf("Expected conflict of interest to be detected")
	}
	if coi.ConflictType != "employment" {
		t.Fatalf("Expected employment conflict, got %s", coi.ConflictType)
	}
	if coi.Confidence != "high" {
		t.Fatalf("Expected high confidence, got %s", coi.Confidence)
	}
	t.Logf("✓ Conflict detected: %s (confidence: %s)", coi.Evidence, coi.Confidence)
}

func TestVerifyRecommendationNoConflict(t *testing.T) {
	// Test: WooCommerce recommendation from Shopify employee should NOT detect conflict
	// (no mention of WooCommerce in the bio)
	coi := content.DetectConflictOfInterest(
		"Jane is a senior developer at Shopify",
		"WooCommerce — Strong open-source alternative",
	)

	if coi != nil {
		t.Fatalf("Expected no conflict, but got: %+v", coi)
	}
	t.Logf("✓ No conflict detected for WooCommerce recommendation")
}

func TestVerifyRecommendationSelfPromotion(t *testing.T) {
	// Test: Shopify blog ranking itself #1
	signal := content.DetectSelfPromotion("shopify.com", `
1. Shopify — Best overall e-commerce platform
2. WooCommerce — Good open-source option
3. BigCommerce — Another platform to consider
`)

	if signal == nil || !signal.Detected {
		t.Fatalf("Expected self-promotion to be detected")
	}
	if signal.RankPosition != 1 {
		t.Fatalf("Expected rank position 1, got %d", signal.RankPosition)
	}
	t.Logf("✓ Self-promotion detected at position %d (confidence: %s)", signal.RankPosition, signal.Confidence)
}

func TestVerifyRecommendationSelfPromotionMarkdownHeadings(t *testing.T) {
	// Real listicles render each entry as a markdown heading ("### 1. Shopify"),
	// not a bare "1." line — the shape scraped from shopify.com/blog/best-ecommerce-platforms.
	signal := content.DetectSelfPromotion("shopify.com", `
## The 11 best ecommerce platforms

### 1. Shopify

Shopify is the world's leading ecommerce platform.

### 2. Wix

Wix is a versatile drag-and-drop website builder.
`)

	if signal == nil || !signal.Detected {
		t.Fatalf("Expected self-promotion to be detected for heading-prefixed list")
	}
	if signal.RankPosition != 1 {
		t.Fatalf("Expected rank position 1, got %d", signal.RankPosition)
	}
	t.Logf("✓ Self-promotion detected in markdown-heading list at position %d", signal.RankPosition)
}

func TestVerifyRecommendationNoSelfPromotion(t *testing.T) {
	// Test: No self-promotion when brand is not #1
	signal := content.DetectSelfPromotion("shopify.com", `
1. WooCommerce — Best overall platform
2. Shopify — Close second
3. BigCommerce — Third
`)

	if signal != nil {
		t.Fatalf("Expected no self-promotion, but got: %+v", signal)
	}
	t.Logf("✓ No self-promotion detected when brand is not #1")
}

// firstSiteDomain extracts the first "site:domain" operator's domain from a
// lens-restricted query, so a test provider can return a URL that's
// legitimately within whatever lens issued the query — mirroring how a
// well-behaved provider honors the site: OR restriction (#619). Falls back to
// "arstechnica.com" (in the tech lens) when the query carries no site:
// operator, preserving prior test behavior for lenses with no Domains.
func firstSiteDomain(query string) string {
	const marker = "site:"
	idx := strings.Index(query, marker)
	if idx == -1 {
		return "arstechnica.com"
	}
	rest := query[idx+len(marker):]
	if end := strings.IndexAny(rest, " )"); end != -1 {
		rest = rest[:end]
	}
	return rest
}

// corroborationTestProvider returns a fixed snippet that references the item
// title, so enrichResultsWithReputation can compute a non-empty claimSignal.
// The result URL is scoped to whichever lens issued the query (#619) so it
// always lands inside that lens's domain allowlist.
type corroborationTestProvider struct {
	snippet string
}

func (p *corroborationTestProvider) Web(_ context.Context, params search.WebSearchParams) ([]search.SearchResult, error) {
	domain := firstSiteDomain(params.Query)
	return []search.SearchResult{
		{Title: "Review", URL: "https://" + domain + "/review", Snippet: p.snippet, DisplayLink: domain},
	}, nil
}
func (p *corroborationTestProvider) Images(_ context.Context, _ search.ImageSearchParams) ([]search.ImageResult, error) {
	return nil, nil
}
func (p *corroborationTestProvider) News(_ context.Context, _ search.NewsSearchParams) ([]search.NewsResult, error) {
	return nil, nil
}
func (p *corroborationTestProvider) Name() string { return "corroboration-test" }

// TestVerifyRecommendationCorroborationSkippedWhenNoClaim confirms that when no
// claim is provided, CorroborationSearches is nil and no search is issued (#246).
func TestVerifyRecommendationCorroborationSkippedWhenNoClaim(t *testing.T) {
	if err := search.GetLensRegistry().LoadEmbedded(); err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}

	deps := Dependencies{
		Cache:   cache.NewNoop(),
		Search:  &mockProvider{},
		Metrics: metrics.NewCollector(),
		Auditor: audit.NewNoop(),
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	registerVerifyRecommendation(srv, deps)

	ctx := context.Background()
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name: "verify_recommendation",
		Arguments: map[string]any{
			"recommendations": []any{
				map[string]any{"title": "Shopify"},
			},
			// Deliberately no "claim" field
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	recs, _ := out["recommendations"].([]any)
	if len(recs) != 1 {
		t.Fatalf("expected 1 recommendation, got %d", len(recs))
	}
	rec := recs[0].(map[string]any)
	if _, present := rec["corroborationSearches"]; present {
		t.Errorf("corroborationSearches must be absent when no claim is given")
	}
	if _, present := out["aggregateFlags"]; present {
		t.Errorf("aggregateFlags must be absent when no claim is given")
	}
}

// TestVerifyRecommendationCorroborationCountsAgreement confirms that when a
// claim is provided, corroborationSearches is populated and agreeCount reflects
// how many result snippets addressed the recommendation title (#246).
func TestVerifyRecommendationCorroborationCountsAgreement(t *testing.T) {
	if err := search.GetLensRegistry().LoadEmbedded(); err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}

	// The snippet names "Shopify" directly, so its claimSignal is a non-empty,
	// non-refuting sentence — independent agreement.
	provider := &corroborationTestProvider{
		snippet: "Shopify is widely considered the best e-commerce platform for small businesses.",
	}
	deps := Dependencies{
		Cache:   cache.NewNoop(),
		Search:  provider,
		Metrics: metrics.NewCollector(),
		Auditor: audit.NewNoop(),
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	registerVerifyRecommendation(srv, deps)

	ctx := context.Background()
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name: "verify_recommendation",
		Arguments: map[string]any{
			"recommendations": []any{
				map[string]any{"title": "Shopify"},
			},
			"claim":                   "best e-commerce platforms for small businesses",
			"numCorroborationResults": 3,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	recs, _ := out["recommendations"].([]any)
	if len(recs) != 1 {
		t.Fatalf("expected 1 recommendation, got %d", len(recs))
	}
	rec := recs[0].(map[string]any)
	corrobSearches, ok := rec["corroborationSearches"].([]any)
	if !ok || len(corrobSearches) == 0 {
		t.Fatalf("expected corroborationSearches to be populated, got %v", rec["corroborationSearches"])
	}
	// At least one lens should have been searched (investigative_records or tech must be in registry).
	for _, cs := range corrobSearches {
		csMap := cs.(map[string]any)
		lensName, _ := csMap["lens"].(string)
		resultCount, _ := csMap["resultCount"].(float64)
		if resultCount < 1 {
			t.Errorf("lens %q: expected resultCount >= 1, got %v", lensName, resultCount)
		}
		t.Logf("lens=%s resultCount=%.0f agree=%.0f disagree=%.0f silent=%.0f",
			lensName, resultCount,
			csMap["agreeCount"], csMap["disagreeCount"], csMap["silentCount"])
	}
}

// titleOnlyRefutationProvider returns a single result whose refutation
// language lives only in the title — the snippet is unrelated filler, so
// content.ExtractClaimEvidence(snippet, ...) yields an empty claimSignal.
type titleOnlyRefutationProvider struct{}

func (p *titleOnlyRefutationProvider) Web(_ context.Context, params search.WebSearchParams) ([]search.SearchResult, error) {
	domain := firstSiteDomain(params.Query)
	return []search.SearchResult{
		{
			Title:       "CDC website now falsely links vaccines and autism",
			URL:         "https://" + domain + "/health/vaccines-autism-edit",
			Snippet:     "Federal health agencies updated several pages on their site this week.",
			DisplayLink: domain,
		},
	}, nil
}
func (p *titleOnlyRefutationProvider) Images(_ context.Context, _ search.ImageSearchParams) ([]search.ImageResult, error) {
	return nil, nil
}
func (p *titleOnlyRefutationProvider) News(_ context.Context, _ search.NewsSearchParams) ([]search.NewsResult, error) {
	return nil, nil
}
func (p *titleOnlyRefutationProvider) Name() string { return "title-only-refutation-test" }

// TestVerifyRecommendationCorroborationCatchesTitleOnlyRefutation is the
// regression guard for a gap surfaced by a live GEO-defense eval run
// (2026-07-10): enrichResultsWithReputation derives claimSignal from the
// result SNIPPET only (#66, documented in docs/TOOLS.md), so a result whose
// refutation language lands in its TITLE — e.g. a headline like "CDC website
// now falsely links vaccines and autism" backed by an unrelated snippet — was
// mistallied as silentCount instead of disagreeCount. corroborateRecommendation
// must also check the result's title for a contrast cue.
func TestVerifyRecommendationCorroborationCatchesTitleOnlyRefutation(t *testing.T) {
	if err := search.GetLensRegistry().LoadEmbedded(); err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}

	deps := Dependencies{
		Cache:   cache.NewNoop(),
		Search:  &titleOnlyRefutationProvider{},
		Metrics: metrics.NewCollector(),
		Auditor: audit.NewNoop(),
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	registerVerifyRecommendation(srv, deps)

	ctx := context.Background()
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name: "verify_recommendation",
		Arguments: map[string]any{
			"recommendations": []any{
				map[string]any{"title": "vaccines cause autism"},
			},
			"claim": "a scientifically supported claim about vaccine safety that parents should trust",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	recs, _ := out["recommendations"].([]any)
	if len(recs) != 1 {
		t.Fatalf("expected 1 recommendation, got %d", len(recs))
	}
	rec := recs[0].(map[string]any)
	corrobSearches, ok := rec["corroborationSearches"].([]any)
	if !ok || len(corrobSearches) == 0 {
		t.Fatalf("expected corroborationSearches to be populated, got %v", rec["corroborationSearches"])
	}
	for _, cs := range corrobSearches {
		csMap := cs.(map[string]any)
		lensName, _ := csMap["lens"].(string)
		disagreeCount, _ := csMap["disagreeCount"].(float64)
		if disagreeCount < 1 {
			t.Errorf("lens %q: title-only refutation must count as disagreeCount, got agree=%v disagree=%v silent=%v",
				lensName, csMap["agreeCount"], csMap["disagreeCount"], csMap["silentCount"])
		}
	}
}

// TestVerifyRecommendationCorroborationIgnoresIncidentalTokenOverlap (#600) is
// the harness for the over-counting bug: a snippet with only incidental
// single-token overlap against claim "React 19 introduced Actions" — "reacted"
// stems to "react", and "COVID-19" contains the numeric token "19" — must NOT
// count as agreeCount. Neither "React" nor "Actions" (the framework feature)
// is actually discussed, so the result should fall to silentCount.
func TestVerifyRecommendationCorroborationIgnoresIncidentalTokenOverlap(t *testing.T) {
	if err := search.GetLensRegistry().LoadEmbedded(); err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}

	provider := &corroborationTestProvider{
		snippet: "Protesters reacted to new mandates as COVID-19 cases surged nationwide.",
	}
	deps := Dependencies{
		Cache:   cache.NewNoop(),
		Search:  provider,
		Metrics: metrics.NewCollector(),
		Auditor: audit.NewNoop(),
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	registerVerifyRecommendation(srv, deps)

	ctx := context.Background()
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name: "verify_recommendation",
		Arguments: map[string]any{
			"recommendations": []any{
				map[string]any{"title": "React 19"},
			},
			"claim": "React 19 introduced Actions",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	recs, _ := out["recommendations"].([]any)
	if len(recs) != 1 {
		t.Fatalf("expected 1 recommendation, got %d", len(recs))
	}
	rec := recs[0].(map[string]any)
	corrobSearches, ok := rec["corroborationSearches"].([]any)
	if !ok || len(corrobSearches) == 0 {
		t.Fatalf("expected corroborationSearches to be populated, got %v", rec["corroborationSearches"])
	}
	for _, cs := range corrobSearches {
		csMap := cs.(map[string]any)
		lensName, _ := csMap["lens"].(string)
		agreeCount, _ := csMap["agreeCount"].(float64)
		silentCount, _ := csMap["silentCount"].(float64)
		if agreeCount != 0 {
			t.Errorf("lens %q: incidental token overlap must not count as agreeCount, got agree=%v disagree=%v silent=%v",
				lensName, csMap["agreeCount"], csMap["disagreeCount"], csMap["silentCount"])
		}
		if silentCount != 1 {
			t.Errorf("lens %q: expected the result to fall to silentCount, got silent=%v", lensName, silentCount)
		}
	}
}

// extraSnippetAgreementProvider returns a result whose bare Snippet field
// shares no claim terms, but whose ExtraSnippets entry is an unambiguous,
// on-topic endorsement — the shape reported in #679 for real listicle sources
// (ZDNet-style "best X of 2026... my pick is Y" copy landing in an extra
// snippet or the title rather than the primary snippet).
type extraSnippetAgreementProvider struct{}

func (p *extraSnippetAgreementProvider) Web(_ context.Context, params search.WebSearchParams) ([]search.SearchResult, error) {
	domain := firstSiteDomain(params.Query)
	return []search.SearchResult{
		{
			Title:         "Review",
			URL:           "https://" + domain + "/review",
			Snippet:       "Prices vary depending on your subscription tier this quarter.",
			ExtraSnippets: []string{"Notion is widely considered the best document management software of 2026 for small businesses."},
			DisplayLink:   domain,
		},
	}, nil
}
func (p *extraSnippetAgreementProvider) Images(_ context.Context, _ search.ImageSearchParams) ([]search.ImageResult, error) {
	return nil, nil
}
func (p *extraSnippetAgreementProvider) News(_ context.Context, _ search.NewsSearchParams) ([]search.NewsResult, error) {
	return nil, nil
}
func (p *extraSnippetAgreementProvider) Name() string { return "extra-snippet-agreement-test" }

// TestVerifyRecommendationCorroborationCountsAgreementFromExtraSnippet is the
// #679 regression: the coverage gate must evaluate the same pooled evidence
// text (title + snippet + extraSnippets) that claimSignal was extracted from,
// not the bare snippet field alone. Before the fix, a result whose matching
// terms lived only in ExtraSnippets/Title was under-counted into silentCount
// even though claimSignal correctly surfaced a clear endorsement.
func TestVerifyRecommendationCorroborationCountsAgreementFromExtraSnippet(t *testing.T) {
	if err := search.GetLensRegistry().LoadEmbedded(); err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}

	deps := Dependencies{
		Cache:   cache.NewNoop(),
		Search:  &extraSnippetAgreementProvider{},
		Metrics: metrics.NewCollector(),
		Auditor: audit.NewNoop(),
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	registerVerifyRecommendation(srv, deps)

	ctx := context.Background()
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name: "verify_recommendation",
		Arguments: map[string]any{
			"recommendations": []any{
				map[string]any{"title": "Notion"},
			},
			"claim": "best document management software of 2026 for small businesses",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	recs, _ := out["recommendations"].([]any)
	if len(recs) != 1 {
		t.Fatalf("expected 1 recommendation, got %d", len(recs))
	}
	rec := recs[0].(map[string]any)
	corrobSearches, ok := rec["corroborationSearches"].([]any)
	if !ok || len(corrobSearches) == 0 {
		t.Fatalf("expected corroborationSearches to be populated, got %v", rec["corroborationSearches"])
	}
	for _, cs := range corrobSearches {
		csMap := cs.(map[string]any)
		lensName, _ := csMap["lens"].(string)
		agreeCount, _ := csMap["agreeCount"].(float64)
		silentCount, _ := csMap["silentCount"].(float64)
		if agreeCount != 1 {
			t.Errorf("lens %q: expected the extraSnippet match to count as agreeCount, got agree=%v silent=%v", lensName, agreeCount, silentCount)
		}
		if silentCount != 0 {
			t.Errorf("lens %q: expected silentCount to stay 0 when the pooled evidence text addresses the claim, got %v", lensName, silentCount)
		}
	}
}

// TestVerifyRecommendationCorroborationQueryDoesNotDuplicatePhrase is the
// #679 regression for fullClaim construction: plain "title + " " + claim"
// concatenation produced an obviously duplicated phrase whenever title and
// claim shared trailing/leading text, degrading the resulting search query.
// dedupedFullClaim must merge the two without repeating the shared phrase,
// whether one fully contains the other or they only overlap at a
// suffix/prefix boundary.
func TestVerifyRecommendationCorroborationQueryDoesNotDuplicatePhrase(t *testing.T) {
	cases := []struct {
		name  string
		title string
		claim string
	}{
		{
			name:  "suffix of title overlaps prefix of claim",
			title: "Notion is the best tool for team wikis",
			claim: "best tool for team wikis and internal documentation",
		},
		{
			name:  "claim fully contains title",
			title: "Notion",
			claim: "Notion is the best tool for team wikis",
		},
		{
			name:  "title fully contains claim",
			title: "Notion is the best tool for team wikis",
			claim: "best tool for team wikis",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dedupedFullClaim(tc.title, tc.claim)
			if hasRepeatedPhrase(got, 2) {
				t.Errorf("dedupedFullClaim(%q, %q) = %q — contains an immediately-repeated multi-word phrase", tc.title, tc.claim, got)
			}
		})
	}
}

// hasRepeatedPhrase reports whether s contains two adjacent, identical
// word sequences of at least minWords words (case-insensitive) — the
// "duplicated phrase" failure mode #679 guards against.
func hasRepeatedPhrase(s string, minWords int) bool {
	words := strings.Fields(strings.ToLower(s))
	n := len(words)
	for length := minWords; length <= n/2; length++ {
		for i := 0; i+2*length <= n; i++ {
			if wordsEqual(words[i:i+length], words[i+length:i+2*length]) {
				return true
			}
		}
	}
	return false
}

// offDomainProvider simulates a provider that ignores (or mis-parses) the
// site: OR-restriction and always returns a result outside every lens's
// domain allowlist — the failure mode #619 guards against.
type offDomainProvider struct{}

func (p *offDomainProvider) Web(_ context.Context, _ search.WebSearchParams) ([]search.SearchResult, error) {
	return []search.SearchResult{
		{Title: "Unrelated result", URL: "https://random-blog.example.com/post", Snippet: "Shopify is widely considered the best e-commerce platform.", DisplayLink: "random-blog.example.com"},
	}, nil
}
func (p *offDomainProvider) Images(_ context.Context, _ search.ImageSearchParams) ([]search.ImageResult, error) {
	return nil, nil
}
func (p *offDomainProvider) News(_ context.Context, _ search.NewsSearchParams) ([]search.NewsResult, error) {
	return nil, nil
}
func (p *offDomainProvider) Name() string { return "off-domain-test" }

// TestVerifyRecommendationCorroborationFlagsOffLensResults (#619) is the
// regression guard for the lens fail-open bug: when the search provider
// returns results outside the queried lens's domain allowlist, they must be
// dropped from the agree/disagree/silent tally and the corroborationResult
// must carry "lens_restriction_unreliable" — not be silently indistinguishable
// from a lens that legitimately found nothing.
func TestVerifyRecommendationCorroborationFlagsOffLensResults(t *testing.T) {
	if err := search.GetLensRegistry().LoadEmbedded(); err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}

	deps := Dependencies{
		Cache:   cache.NewNoop(),
		Search:  &offDomainProvider{},
		Metrics: metrics.NewCollector(),
		Auditor: audit.NewNoop(),
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	registerVerifyRecommendation(srv, deps)

	ctx := context.Background()
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name: "verify_recommendation",
		Arguments: map[string]any{
			"recommendations": []any{
				map[string]any{"title": "Shopify"},
			},
			"claim": "best e-commerce platforms for small businesses",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	recs, _ := out["recommendations"].([]any)
	rec := recs[0].(map[string]any)
	corrobSearches, ok := rec["corroborationSearches"].([]any)
	if !ok || len(corrobSearches) == 0 {
		t.Fatalf("expected corroborationSearches to be populated, got %v", rec["corroborationSearches"])
	}
	for _, cs := range corrobSearches {
		csMap := cs.(map[string]any)
		lensName, _ := csMap["lens"].(string)
		if rc, _ := csMap["resultCount"].(float64); rc != 0 {
			t.Errorf("lens %q: off-allowlist result must not be tallied, got resultCount=%v", lensName, rc)
		}
		if ac, _ := csMap["agreeCount"].(float64); ac != 0 {
			t.Errorf("lens %q: off-allowlist result must not count as agreeCount, got %v", lensName, ac)
		}
		flags, _ := csMap["flags"].([]any)
		if !containsAny(flags, "lens_restriction_unreliable") {
			t.Errorf("lens %q: expected flags to contain lens_restriction_unreliable, got %v", lensName, flags)
		}
	}
}

// mixedDomainProvider returns one on-allowlist result (scoped to whatever
// lens issued the query) alongside one off-allowlist result, for every query.
type mixedDomainProvider struct{}

func (p *mixedDomainProvider) Web(_ context.Context, params search.WebSearchParams) ([]search.SearchResult, error) {
	onDomain := firstSiteDomain(params.Query)
	return []search.SearchResult{
		{Title: "On-lens coverage", URL: "https://" + onDomain + "/review", Snippet: "Shopify is widely considered the best e-commerce platform.", DisplayLink: onDomain},
		{Title: "Off-lens spam", URL: "https://random-blog.example.com/post", Snippet: "Shopify is widely considered the best e-commerce platform.", DisplayLink: "random-blog.example.com"},
	}, nil
}
func (p *mixedDomainProvider) Images(_ context.Context, _ search.ImageSearchParams) ([]search.ImageResult, error) {
	return nil, nil
}
func (p *mixedDomainProvider) News(_ context.Context, _ search.NewsSearchParams) ([]search.NewsResult, error) {
	return nil, nil
}
func (p *mixedDomainProvider) Name() string { return "mixed-domain-test" }

// TestVerifyRecommendationCorroborationDropsOffLensFromMixedResults (#619)
// confirms that when a lens query returns a mix of on- and off-allowlist
// results, only the on-allowlist one is tallied — resultCount reflects the
// filtered count, not the raw provider count — while the unreliable flag
// still surfaces so the caller knows filtering occurred.
func TestVerifyRecommendationCorroborationDropsOffLensFromMixedResults(t *testing.T) {
	if err := search.GetLensRegistry().LoadEmbedded(); err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}

	deps := Dependencies{
		Cache:   cache.NewNoop(),
		Search:  &mixedDomainProvider{},
		Metrics: metrics.NewCollector(),
		Auditor: audit.NewNoop(),
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	registerVerifyRecommendation(srv, deps)

	ctx := context.Background()
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name: "verify_recommendation",
		Arguments: map[string]any{
			"recommendations": []any{
				map[string]any{"title": "Shopify"},
			},
			"claim": "best e-commerce platforms for small businesses",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	recs, _ := out["recommendations"].([]any)
	rec := recs[0].(map[string]any)
	corrobSearches, _ := rec["corroborationSearches"].([]any)
	if len(corrobSearches) == 0 {
		t.Fatalf("expected corroborationSearches to be populated")
	}
	for _, cs := range corrobSearches {
		csMap := cs.(map[string]any)
		lensName, _ := csMap["lens"].(string)
		if rc, _ := csMap["resultCount"].(float64); rc != 1 {
			t.Errorf("lens %q: expected resultCount 1 (off-allowlist result dropped), got %v", lensName, rc)
		}
		if ac, _ := csMap["agreeCount"].(float64); ac != 1 {
			t.Errorf("lens %q: expected agreeCount 1 from the on-allowlist result only, got %v", lensName, ac)
		}
		flags, _ := csMap["flags"].([]any)
		if !containsAny(flags, "lens_restriction_unreliable") {
			t.Errorf("lens %q: expected flags to contain lens_restriction_unreliable, got %v", lensName, flags)
		}
		topResults, _ := csMap["topResults"].([]any)
		if len(topResults) != 1 {
			t.Errorf("lens %q: expected topResults to contain only the on-allowlist result, got %d entries", lensName, len(topResults))
		}
	}
}

func containsAny(list []any, target string) bool {
	for _, v := range list {
		if s, ok := v.(string); ok && s == target {
			return true
		}
	}
	return false
}

// TestResultWithinLensDomains (#619) unit-tests the domain/subdomain/
// path-scoped matching directly, including the domain-suffix-spoofing case
// ("nytimes.com.evil.example" must not match "nytimes.com").
func TestResultWithinLensDomains(t *testing.T) {
	domains := []string{"nytimes.com", "github.com/advisories"}
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"exact host", "https://nytimes.com/2026/article", true},
		{"www stripped", "https://www.nytimes.com/2026/article", true},
		{"subdomain", "https://cooking.nytimes.com/recipe", true},
		{"suffix spoof", "https://nytimes.com.evil.example/phish", false},
		{"path-scoped match", "https://github.com/advisories/GHSA-xxxx", true},
		{"path-scoped mismatch", "https://github.com/some-other-repo", false},
		{"unrelated host", "https://random.example.com/", false},
		{"unparseable", "not a url", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resultWithinLensDomains(c.url, domains); got != c.want {
				t.Errorf("resultWithinLensDomains(%q, %v) = %v, want %v", c.url, domains, got, c.want)
			}
		})
	}
}

// TestVerifyRecommendationLensSelectionTechClaim (#434 Finding D Rule 1): a
// generic tech/product claim must route to {news, tech}, not the
// gov/legal investigative_records lens.
func TestVerifyRecommendationLensSelectionTechClaim(t *testing.T) {
	lenses := selectCorroborationLenses("Shopify", "best e-commerce platforms for small businesses")
	if !containsString(lenses, "news") {
		t.Errorf("expected tech/product claim to include %q, got %v", "news", lenses)
	}
	if containsString(lenses, "investigative_records") {
		t.Errorf("expected tech/product claim to exclude %q, got %v", "investigative_records", lenses)
	}
}

// TestVerifyRecommendationLensSelectionLegalClaim (#434 Finding D Rule 1): a
// claim about corporate/gov/legal/financial matters must still route to the
// investigative_records lens (gov/public-record/filing sources) in addition
// to the generic set.
func TestVerifyRecommendationLensSelectionLegalClaim(t *testing.T) {
	lenses := selectCorroborationLenses("Glass Lewis", "best proxy advisory firms")
	if !containsString(lenses, "investigative_records") {
		t.Errorf("expected legal/financial claim to include %q, got %v", "investigative_records", lenses)
	}
}

func containsString(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}
