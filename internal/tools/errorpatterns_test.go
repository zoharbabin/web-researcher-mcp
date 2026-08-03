package tools

import (
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

func TestGetSessionSurfacesErrorPatterns(t *testing.T) {
	deps := setupTestDeps()
	// Create a session via the tool, then record outcomes directly on the
	// shared manager (mirrors what scrape_page/academic_search do internally).
	sid, _ := startSession(t, deps, "")
	if sid == "" {
		t.Fatal("no session created")
	}
	for i := 0; i < 3; i++ {
		_ = deps.Sessions.RecordOutcome("default", "anonymous", sid, session.OutcomeEvent{
			Success:   false,
			ErrorKind: "auth_required",
			URL:       "https://paywall.example.com/" + string(rune('a'+i)),
			Timestamp: time.Now().Format(time.RFC3339),
		})
	}

	out, res := callTool(t, deps, "get_research_session", map[string]any{"sessionId": sid})
	if res.IsError {
		t.Fatalf("get_research_session failed")
	}
	patterns, ok := out["errorPatterns"].([]any)
	if !ok || len(patterns) != 1 {
		t.Fatalf("expected 1 errorPattern, got %v", out["errorPatterns"])
	}
	p, _ := patterns[0].(map[string]any)
	if p["kind"] != "auth_required" {
		t.Errorf("pattern kind = %v", p["kind"])
	}
	if p["suggestion"] == nil || p["suggestion"] == "" {
		t.Error("error pattern should carry a session-level suggestion")
	}
}

func TestGetSessionNoPatternBelowThreshold(t *testing.T) {
	deps := setupTestDeps()
	sid, _ := startSession(t, deps, "")
	for i := 0; i < 2; i++ {
		_ = deps.Sessions.RecordOutcome("default", "anonymous", sid, session.OutcomeEvent{Success: false, ErrorKind: "blocked"})
	}
	out, res := callTool(t, deps, "get_research_session", map[string]any{"sessionId": sid})
	if res.IsError {
		t.Fatalf("get_research_session failed")
	}
	if _, ok := out["errorPatterns"]; ok {
		t.Error("2 errors must not surface a pattern (threshold is 3)")
	}
}

// TestGetSessionOverviewSupersededBy (#512): a session's overview
// (stepIndex/lastSteps) must surface supersededBy on a step revised by a
// later one, so a caller recovering the session after context loss can see a
// step is stale without re-deriving it from isRevision/revisesStep itself.
func TestGetSessionOverviewSupersededBy(t *testing.T) {
	deps := setupTestDeps()
	sid := makeSessionWithRevision(t, deps)

	out, res := callTool(t, deps, "get_research_session", map[string]any{"sessionId": sid})
	if res.IsError {
		t.Fatalf("get_research_session failed")
	}

	stepIndex, ok := out["stepIndex"].([]any)
	if !ok || len(stepIndex) != 2 {
		t.Fatalf("expected 2 stepIndex entries, got %v", out["stepIndex"])
	}
	e1, _ := stepIndex[0].(map[string]any)
	if sb, _ := e1["supersededBy"].(float64); sb != 2 {
		t.Errorf("stepIndex[0].supersededBy = %v, want 2", e1["supersededBy"])
	}

	lastSteps, ok := out["lastSteps"].([]any)
	if !ok || len(lastSteps) == 0 {
		t.Fatalf("expected lastSteps, got %v", out["lastSteps"])
	}
	var found bool
	for _, s := range lastSteps {
		step, _ := s.(map[string]any)
		if step["stepNumber"] == float64(1) {
			found = true
			if sb, _ := step["supersededBy"].(float64); sb != 2 {
				t.Errorf("lastSteps step 1 supersededBy = %v, want 2", step["supersededBy"])
			}
		}
	}
	if !found {
		t.Fatal("step 1 not present in lastSteps window (only 2 steps total)")
	}
}

// TestGetSessionSingleStepSupersededBy (#512): fetching a specific revised
// step by stepId must also carry supersededBy, matching the overview view.
func TestGetSessionSingleStepSupersededBy(t *testing.T) {
	deps := setupTestDeps()
	sid := makeSessionWithRevision(t, deps)

	out, res := callTool(t, deps, "get_research_session", map[string]any{"sessionId": sid, "stepId": 1})
	if res.IsError {
		t.Fatalf("get_research_session (stepId=1) failed")
	}
	step, ok := out["step"].(map[string]any)
	if !ok {
		t.Fatalf("expected step, got %v", out["step"])
	}
	if sb, _ := step["supersededBy"].(float64); sb != 2 {
		t.Errorf("step 1 supersededBy = %v, want 2", step["supersededBy"])
	}

	// The revising step itself must not carry supersededBy.
	out2, res2 := callTool(t, deps, "get_research_session", map[string]any{"sessionId": sid, "stepId": 2})
	if res2.IsError {
		t.Fatalf("get_research_session (stepId=2) failed")
	}
	step2, _ := out2["step"].(map[string]any)
	if _, ok := step2["supersededBy"]; ok {
		t.Errorf("step 2 (the revising step) must not itself carry supersededBy, got %v", step2["supersededBy"])
	}
}

func TestGetSessionProviderStats(t *testing.T) {
	deps := setupTestDeps()
	sid, _ := startSession(t, deps, "")
	_ = deps.Sessions.RecordOutcome("default", "anonymous", sid, session.OutcomeEvent{Provider: "brave", Success: true})
	_ = deps.Sessions.RecordOutcome("default", "anonymous", sid, session.OutcomeEvent{Provider: "brave", Success: true})
	out, res := callTool(t, deps, "get_research_session", map[string]any{"sessionId": sid})
	if res.IsError {
		t.Fatalf("get_research_session failed")
	}
	stats, ok := out["providerStats"].(map[string]any)
	if !ok {
		t.Fatalf("expected providerStats, got %v", out["providerStats"])
	}
	brave, _ := stats["brave"].(map[string]any)
	if a, _ := brave["attempts"].(float64); a != 2 {
		t.Errorf("brave attempts = %v, want 2", brave["attempts"])
	}
}
