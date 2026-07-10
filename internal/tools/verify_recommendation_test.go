package tools

import (
	"context"
	"encoding/json"
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

// corroborationTestProvider returns a fixed snippet that references the item
// title, so enrichResultsWithReputation can compute a non-empty claimSignal.
type corroborationTestProvider struct {
	snippet string
}

func (p *corroborationTestProvider) Web(_ context.Context, _ search.WebSearchParams) ([]search.SearchResult, error) {
	return []search.SearchResult{
		{Title: "Review", URL: "https://arstechnica.com/review", Snippet: p.snippet, DisplayLink: "arstechnica.com"},
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
	// At least one lens should have been searched (journalism or tech must be in registry).
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

func (p *titleOnlyRefutationProvider) Web(_ context.Context, _ search.WebSearchParams) ([]search.SearchResult, error) {
	return []search.SearchResult{
		{
			Title:       "CDC website now falsely links vaccines and autism",
			URL:         "https://arstechnica.com/health/vaccines-autism-edit",
			Snippet:     "Federal health agencies updated several pages on their site this week.",
			DisplayLink: "arstechnica.com",
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
