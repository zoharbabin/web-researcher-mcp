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
