package tools

import (
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// Eval 4 of the GEO-defense eval suite (see the suite-level header comment in
// geo_eval_reputation_test.go). Targets the paper's coverage-gap risk: when a
// query has genuinely narrow or zero coverage — the exact situation a curated
// or lens-scoped corpus produces more often than open web search — the tool
// must never paper over the gap with a synthesized citation. It must return
// the structured ZeroResultHints object, carrying the fixed epistemic warning
// that zero results do not confirm absence.
//
// Hermetic: uses emptyWebProvider (always returns zero results) so this is
// deterministic and network-free, unlike Eval 1/5 which need a live provider
// to prove lens containment against a real index.

// TestGeoEval_NeverFabricateOnZeroResults drives web_search end-to-end (real
// MCP tool surface) with a lens set and a provider that always returns zero
// results. It asserts: resultCount is 0, results is empty (never a fabricated
// entry), hints is present, and hints carries the fixed epistemic warning.
func TestGeoEval_NeverFabricateOnZeroResults(t *testing.T) {
	if err := search.GetLensRegistry().LoadEmbedded(); err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	deps := setupTestDeps()
	deps.Search = &emptyWebProvider{}

	out := callToolJSON(t, deps, "web_search", map[string]any{
		"query": "an extremely narrow claim with no coverage in this lens",
		"lens":  "clinical",
	})

	if rc, _ := out["resultCount"].(float64); rc != 0 {
		t.Fatalf("expected resultCount 0, got %v", out["resultCount"])
	}
	results, ok := out["results"].([]any)
	if !ok {
		t.Fatalf("expected results to be an array, got %v", out["results"])
	}
	if len(results) != 0 {
		t.Fatalf("expected zero results on a coverage gap — got %d fabricated-looking entries: %v", len(results), results)
	}

	hints, ok := out["hints"].(map[string]any)
	if !ok {
		t.Fatalf("expected a structured hints object on zero results, got %v", out["hints"])
	}
	if hints["epistemicWarning"] != epistemicZeroResultWarning {
		t.Errorf("expected the fixed epistemic warning %q, got %v", epistemicZeroResultWarning, hints["epistemicWarning"])
	}
	t.Logf("zero-coverage lensed query correctly returned no fabricated results, with hints.reason=%v", hints["reason"])
}

// TestGeoEval_NeverFabricateOnZeroResults_SearchAndScrape mirrors the above for
// search_and_scrape, the other tool with its own zero-result success path
// (issue #100 parity), so both of the repo's two most-used research surfaces
// are covered by this eval, not just web_search.
func TestGeoEval_NeverFabricateOnZeroResults_SearchAndScrape(t *testing.T) {
	if err := search.GetLensRegistry().LoadEmbedded(); err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	deps := setupTestDeps()
	deps.Search = &emptyWebProvider{}

	out := callToolJSON(t, deps, "search_and_scrape", map[string]any{
		"query": "an extremely narrow claim with no coverage in this lens",
	})

	sources, ok := out["sources"].([]any)
	if !ok {
		t.Fatalf("expected sources to be an array, got %v", out["sources"])
	}
	if len(sources) != 0 {
		t.Fatalf("expected zero sources on a coverage gap — got %d fabricated-looking entries: %v", len(sources), sources)
	}
	if combined, _ := out["combinedContent"].(string); combined != "" {
		t.Fatalf("expected empty combinedContent on a coverage gap, got %q", combined)
	}
	hints, ok := out["hints"].(map[string]any)
	if !ok {
		t.Fatalf("expected a structured hints object on zero results, got %v", out["hints"])
	}
	if hints["epistemicWarning"] != epistemicZeroResultWarning {
		t.Errorf("expected the fixed epistemic warning %q, got %v", epistemicZeroResultWarning, hints["epistemicWarning"])
	}
}
