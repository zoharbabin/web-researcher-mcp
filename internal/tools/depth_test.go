package tools

import (
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

func TestDepthUnknownTreatedAsQuick(t *testing.T) {
	_, out := startSession(t, setupTestDeps(), "ludicrous")
	if _, ok := out["coverage"]; ok {
		t.Error("unknown depth should behave as quick (no coverage)")
	}
}
