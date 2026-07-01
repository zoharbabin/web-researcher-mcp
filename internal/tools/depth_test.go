package tools

import (
	"fmt"
	"testing"
)

// startSession creates a session and records a knowledge gap + a couple of
// same-domain sources so coverage analysis has something to chew on.
func startSession(t *testing.T, deps Dependencies, depth string) (string, map[string]any) {
	t.Helper()
	out, res := callTool(t, deps, "sequential_search", map[string]any{
		"searchStep":     "Looked at transformer scaling",
		"stepNumber":     1,
		"nextStepNeeded": true,
		"researchGoal":   "transformer scaling laws",
		"knowledgeGap":   "no data past 2023",
		"depth":          depth,
	})
	if res.IsError {
		t.Fatalf("sequential_search failed")
	}
	sid, _ := out["sessionId"].(string)
	return sid, out
}

func TestDepthQuickIsDefault(t *testing.T) {
	_, out := startSession(t, setupTestDeps(), "")
	if _, ok := out["coverage"]; ok {
		t.Error("quick depth must not emit coverage")
	}
	if _, ok := out["depth"]; ok {
		t.Error("quick depth must not echo depth field")
	}
}

func TestDepthStandardAddsCoverage(t *testing.T) {
	_, out := startSession(t, setupTestDeps(), "standard")
	if out["depth"] != "standard" {
		t.Errorf("depth = %v", out["depth"])
	}
	if _, ok := out["coverage"]; !ok {
		t.Error("standard depth should emit coverage")
	}
	// A research goal + a knowledge gap should produce refinement queries.
	rq, ok := out["refinementQueries"].([]any)
	if !ok || len(rq) == 0 {
		t.Errorf("standard depth should suggest refinement queries, got %v", out["refinementQueries"])
	}
	// standard must NOT auto-execute.
	if _, ok := out["refinementResults"]; ok {
		t.Error("standard depth must NOT auto-execute searches")
	}
}

func TestDepthThoroughAutoExecutes(t *testing.T) {
	deps := setupTestDeps()
	sid, out := startSession(t, deps, "thorough")
	if out["depth"] != "thorough" {
		t.Errorf("depth = %v", out["depth"])
	}
	rr, ok := out["refinementResults"].([]any)
	if !ok || len(rr) == 0 {
		t.Fatalf("thorough depth should auto-run refinement searches, got %v", out["refinementResults"])
	}
	// Bounded to maxRefinementRounds.
	if len(rr) > maxRefinementRounds {
		t.Errorf("thorough exceeded %d rounds: %d", maxRefinementRounds, len(rr))
	}
	first, _ := rr[0].(map[string]any)
	if first["query"] == nil {
		t.Error("each refinement result must carry its query for provenance")
	}

	// Regression (IRL bug): on the session-creating call input.SessionID is empty,
	// so auto-discovered sources must be tracked via idx.ID, not input.SessionID.
	// Verify they actually landed on the session.
	exp, eres := callTool(t, deps, "research_export", map[string]any{"sessionId": sid, "format": "json"})
	if eres.IsError {
		t.Fatalf("research_export failed")
	}
	if sc, _ := exp["sourceCount"].(float64); sc == 0 {
		t.Error("thorough refinement sources were not persisted to the session (idx.ID tracking regression)")
	}
}

// TestSequentialSearchMapsPublishedAt (#356): searchResultsToMaps must not
// silently drop a SearchResult's PublishedAt field.
func TestSequentialSearchMapsPublishedAt(t *testing.T) {
	deps := setupTestDeps()
	_, out := startSession(t, deps, "thorough")
	rr, ok := out["refinementResults"].([]any)
	if !ok || len(rr) == 0 {
		t.Fatalf("thorough depth should auto-run refinement searches, got %v", out["refinementResults"])
	}
	first, _ := rr[0].(map[string]any)
	results, _ := first["results"].([]any)
	if len(results) == 0 {
		t.Fatalf("expected mapped results, got %v", first["results"])
	}
	r0, _ := results[0].(map[string]any)
	if r0["publishedAt"] != "2026-05-01T12:00:00Z" {
		t.Errorf("expected publishedAt to be mapped, got %v", r0["publishedAt"])
	}

	// Regression guard: sequentialSearchOutputSchema's nested
	// refinementResults[].results[] item schema must declare every key the
	// response actually emits. TestOutputSchemaMatchesResponse only checks
	// top-level response keys and never drives depth=thorough, so it cannot
	// catch drift in this nested schema on its own.
	props, _ := sequentialSearchOutputSchema["properties"].(map[string]any)
	rrSchema, _ := props["refinementResults"].(map[string]any)
	rrItems, _ := rrSchema["items"].(map[string]any)
	rrProps, _ := rrItems["properties"].(map[string]any)
	resultsSchema, _ := rrProps["results"].(map[string]any)
	resultsItems, _ := resultsSchema["items"].(map[string]any)
	resultsProps, _ := resultsItems["properties"].(map[string]any)
	for key := range r0 {
		if _, declared := resultsProps[key]; !declared {
			t.Errorf("refinementResults[].results[] field %q not declared in sequentialSearchOutputSchema", key)
		}
	}
}

// TestDepthThoroughWarnsOnZeroResults (#357): when a thorough-depth
// refinement round returns zero results, the response must surface a
// refinementWarning naming exactly how many of the rounds came back empty.
// No existing test drives zeroCount > 0 because the default mockProvider
// always returns a non-empty result, so this exercises that path directly.
func TestDepthThoroughWarnsOnZeroResults(t *testing.T) {
	deps := setupTestDeps()
	deps.Search = &emptyWebProvider{}
	_, out := startSession(t, deps, "thorough")

	rr, ok := out["refinementResults"].([]any)
	if !ok || len(rr) == 0 {
		t.Fatalf("thorough depth should auto-run refinement searches, got %v", out["refinementResults"])
	}

	warning, ok := out["refinementWarning"].(string)
	if !ok || warning == "" {
		t.Fatalf("expected refinementWarning when all refinement rounds return zero results, got %v", out["refinementWarning"])
	}
	want := fmt.Sprintf("%d of %d refinement searches returned no results; gaps in coverage may persist and do not confirm absence", len(rr), len(rr))
	if warning != want {
		t.Errorf("refinementWarning = %q, want %q", warning, want)
	}
}

// TestDepthThoroughWarnsOnRefinementSearchError (#357): a refinement round
// that errors (not just returns zero results) must also count toward
// zeroCount/refinementWarning, and the entry must carry the "search failed"
// marker. No existing test drives this branch since emptyWebProvider returns
// nil error with an empty slice, never a non-nil error.
func TestDepthThoroughWarnsOnRefinementSearchError(t *testing.T) {
	deps := setupTestDeps()
	deps.Search = &genericErrorProvider{}
	_, out := startSession(t, deps, "thorough")

	rr, ok := out["refinementResults"].([]any)
	if !ok || len(rr) == 0 {
		t.Fatalf("thorough depth should auto-run refinement searches, got %v", out["refinementResults"])
	}
	first, _ := rr[0].(map[string]any)
	if first["error"] != "search failed" {
		t.Errorf("expected refinementResults[0].error = %q, got %v", "search failed", first["error"])
	}

	warning, ok := out["refinementWarning"].(string)
	if !ok || warning == "" {
		t.Fatalf("expected refinementWarning when refinement rounds error out, got %v", out["refinementWarning"])
	}
	want := fmt.Sprintf("%d of %d refinement searches returned no results; gaps in coverage may persist and do not confirm absence", len(rr), len(rr))
	if warning != want {
		t.Errorf("refinementWarning = %q, want %q", warning, want)
	}
}

func TestDepthUnknownTreatedAsQuick(t *testing.T) {
	_, out := startSession(t, setupTestDeps(), "ludicrous")
	if _, ok := out["coverage"]; ok {
		t.Error("unknown depth should behave as quick (no coverage)")
	}
}
