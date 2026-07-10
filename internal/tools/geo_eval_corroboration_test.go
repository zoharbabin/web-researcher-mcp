package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// Eval 3 of the GEO-defense eval suite (see the suite-level header comment in
// geo_eval_reputation_test.go). Targets the paper's seeded-fabrication finding:
// a claim with no independent backing got repeated by AI assistants within a
// day because nothing checked whether independent sources actually endorsed
// it. verify_recommendation's claim-corroboration path is exactly that check.
//
// This is a HERMETIC companion to the fix applied to corroborateRecommendation
// (which previously compared claimSignal against enum literals it never
// produces, so AgreeCount could never increment and
// no_independent_corroboration fired unconditionally on every claim-bearing
// call). These tests pin the corrected agree/disagree/silent tallying with a
// fixed, deterministic provider — no network required.

// geoEvalCorroborationProvider returns one fixed web result per Web() call,
// whose snippet is fully controlled by the test.
type geoEvalCorroborationProvider struct{ snippet string }

func (p *geoEvalCorroborationProvider) Web(_ context.Context, _ search.WebSearchParams) ([]search.SearchResult, error) {
	return []search.SearchResult{
		{Title: "Coverage", URL: "https://arstechnica.com/coverage", Snippet: p.snippet, DisplayLink: "arstechnica.com"},
	}, nil
}
func (p *geoEvalCorroborationProvider) Images(_ context.Context, _ search.ImageSearchParams) ([]search.ImageResult, error) {
	return nil, nil
}
func (p *geoEvalCorroborationProvider) News(_ context.Context, _ search.NewsSearchParams) ([]search.NewsResult, error) {
	return nil, nil
}
func (p *geoEvalCorroborationProvider) Name() string { return "geo-eval-corroboration" }

// runCorroborationEval drives verify_recommendation end-to-end over the real
// MCP tool surface (not the internal helper directly) with the given provider,
// title, and claim, returning the parsed aggregateFlags and the first
// recommendation's corroborationSearches tallies summed across lenses.
func runCorroborationEval(t *testing.T, provider search.Provider, title, claim string) (aggregateFlags []string, agree, disagree, silent int) {
	t.Helper()
	if err := search.GetLensRegistry().LoadEmbedded(); err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
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
			"recommendations": []any{map[string]any{"title": title}},
			"claim":           claim,
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

	for _, f := range asStringSlice(out["aggregateFlags"]) {
		aggregateFlags = append(aggregateFlags, f)
	}
	recs, _ := out["recommendations"].([]any)
	if len(recs) != 1 {
		t.Fatalf("expected 1 recommendation, got %d", len(recs))
	}
	rec := recs[0].(map[string]any)
	searches, _ := rec["corroborationSearches"].([]any)
	for _, s := range searches {
		m := s.(map[string]any)
		agree += int(m["agreeCount"].(float64))
		disagree += int(m["disagreeCount"].(float64))
		silent += int(m["silentCount"].(float64))
	}
	return aggregateFlags, agree, disagree, silent
}

func asStringSlice(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, a := range arr {
		if s, ok := a.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// TestGeoEval_CorroborationAgreement: an independent snippet that names the
// recommendation without a refutation cue counts as agreement, and the
// no_independent_corroboration flag must NOT fire.
func TestGeoEval_CorroborationAgreement(t *testing.T) {
	provider := &geoEvalCorroborationProvider{
		snippet: "Shopify is widely considered the best e-commerce platform for small businesses.",
	}
	flags, agree, disagree, silent := runCorroborationEval(t, provider, "Shopify", "best e-commerce platform for small businesses")
	t.Logf("agree=%d disagree=%d silent=%d flags=%v", agree, disagree, silent, flags)
	if agree == 0 {
		t.Errorf("expected agreeCount > 0 for a snippet that names the title without a refutation cue, got 0")
	}
	for _, f := range flags {
		if f == "no_independent_corroboration" {
			t.Errorf("no_independent_corroboration must not fire when at least one independent source agrees")
		}
	}
}

// TestGeoEval_CorroborationDisagreement: an independent snippet that names the
// recommendation WITH a refutation/negation cue counts as disagreement, not
// silence, and NOT agreement — proving the fixed switch distinguishes disputed
// coverage from mere silence rather than lumping both into "no signal".
func TestGeoEval_CorroborationDisagreement(t *testing.T) {
	provider := &geoEvalCorroborationProvider{
		snippet: "Independent reviewers found Shopify does not offer the best e-commerce platform for small businesses; users reported the opposite.",
	}
	flags, agree, disagree, silent := runCorroborationEval(t, provider, "Shopify", "best e-commerce platform for small businesses")
	t.Logf("agree=%d disagree=%d silent=%d flags=%v", agree, disagree, silent, flags)
	if disagree == 0 {
		t.Errorf("expected disagreeCount > 0 for a snippet naming the title with an explicit refutation cue, got 0")
	}
	if agree != 0 {
		t.Errorf("expected agreeCount == 0 when the only signal is a refutation, got %d", agree)
	}
	// Nothing independently agreed, so the aggregate flag correctly fires — this
	// is the eval's honest scope: the flag detects "nothing endorses this," not
	// "something specifically disputes this." A caller reading disagreeCount
	// alongside the flag gets the fuller picture.
	found := false
	for _, f := range flags {
		if f == "no_independent_corroboration" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected no_independent_corroboration to fire when zero independent sources agreed (even though disagreeCount > 0)")
	}
}

// TestGeoEval_CorroborationSilenceTriggersFlag mirrors the paper's
// seeded-fabrication scenario: a recommendation with a claim that no
// independent source even mentions must surface silentCount > 0,
// agreeCount == 0, and the no_independent_corroboration flag firing — the
// signal a caller needs to avoid repeating an unverified claim.
func TestGeoEval_CorroborationSilenceTriggersFlag(t *testing.T) {
	provider := &geoEvalCorroborationProvider{
		snippet: "This page discusses unrelated topics in general software engineering practice.",
	}
	flags, agree, disagree, silent := runCorroborationEval(t, provider, "Acme Widget Pro", "the fastest widget management tool ever built")
	t.Logf("agree=%d disagree=%d silent=%d flags=%v", agree, disagree, silent, flags)
	if silent == 0 {
		t.Errorf("expected silentCount > 0 when no snippet mentions the recommendation title, got 0")
	}
	if agree != 0 {
		t.Errorf("expected agreeCount == 0 when no independent source mentions the title, got %d", agree)
	}
	found := false
	for _, f := range flags {
		if f == "no_independent_corroboration" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected no_independent_corroboration to fire when every corroboration search came back silent")
	}
}
