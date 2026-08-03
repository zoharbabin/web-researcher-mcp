package tools

import (
	"strings"
	"testing"
)

// makeSessionWithSources creates a session via sequential_search and records a
// source on it, returning the sessionId. Shared deps so the session persists
// across the follow-up export/bibliography call.
func makeSessionWithSources(t *testing.T, deps Dependencies) string {
	t.Helper()
	out, res := callTool(t, deps, "sequential_search", map[string]any{
		"searchStep":     "Investigated transformer attention mechanisms",
		"stepNumber":     1,
		"nextStepNeeded": true,
		"researchGoal":   "How do transformers work?",
		"reasoning":      "Start from the seminal paper",
		"confidence":     "high",
		"knowledgeGap":   "Need scaling-law data",
	})
	if res.IsError {
		t.Fatalf("sequential_search step 1 failed")
	}
	sid, _ := out["sessionId"].(string)
	if sid == "" {
		t.Fatal("no sessionId returned")
	}
	// academic_search with the session links a source.
	_, res = callTool(t, deps, "academic_search", map[string]any{"query": "attention is all you need", "sessionId": sid})
	if res.IsError {
		t.Fatalf("academic_search failed")
	}
	return sid
}

// makeSessionWithRevision creates a 2-step session where step 2 revises step
// 1, returning the sessionId. Used by both #512 supersededBy tests.
func makeSessionWithRevision(t *testing.T, deps Dependencies) string {
	t.Helper()
	out, res := callTool(t, deps, "sequential_search", map[string]any{
		"searchStep":     "Initial finding: X causes Y",
		"stepNumber":     1,
		"nextStepNeeded": true,
		"researchGoal":   "Does X cause Y?",
	})
	if res.IsError {
		t.Fatalf("sequential_search step 1 failed")
	}
	sid, _ := out["sessionId"].(string)
	if sid == "" {
		t.Fatal("no sessionId returned")
	}
	_, res = callTool(t, deps, "sequential_search", map[string]any{
		"searchStep":     "Correction: X does not cause Y",
		"stepNumber":     2,
		"nextStepNeeded": false,
		"sessionId":      sid,
		"isRevision":     true,
		"revisesStep":    1,
	})
	if res.IsError {
		t.Fatalf("sequential_search step 2 (revision) failed")
	}
	return sid
}

// TestResearchExportMarkdownSupersededBy (#512): the markdown report must
// annotate a revised step's heading with "(superseded by step N)" so a reader
// scanning the report doesn't act on stale step 1 findings.
func TestResearchExportMarkdownSupersededBy(t *testing.T) {
	deps := setupTestDeps()
	sid := makeSessionWithRevision(t, deps)

	out, res := callTool(t, deps, "research_export", map[string]any{"sessionId": sid})
	if res.IsError {
		t.Fatalf("research_export failed")
	}
	doc, _ := out["document"].(string)
	if !strings.Contains(doc, "### Step 1 (superseded by step 2)") {
		t.Errorf("markdown missing supersededBy annotation on step 1:\n%s", doc)
	}
	if !strings.Contains(doc, "### Step 2 (revises step 1)") {
		t.Errorf("markdown missing revises annotation on step 2:\n%s", doc)
	}
}

// TestResearchExportJSONSupersededBy (#512): the json document's steps must
// carry supersededBy on the revised step, without mutating the underlying
// isRevision/revisesStep audit trail on either step.
func TestResearchExportJSONSupersededBy(t *testing.T) {
	deps := setupTestDeps()
	sid := makeSessionWithRevision(t, deps)

	out, res := callTool(t, deps, "research_export", map[string]any{"sessionId": sid, "format": "json"})
	if res.IsError {
		t.Fatalf("research_export json failed")
	}
	doc, ok := out["document"].(map[string]any)
	if !ok {
		t.Fatalf("document should be a structured object for format=json, got %T", out["document"])
	}
	steps, ok := doc["steps"].([]any)
	if !ok || len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %v", doc["steps"])
	}
	step1, _ := steps[0].(map[string]any)
	if sb, _ := step1["supersededBy"].(float64); sb != 2 {
		t.Errorf("step 1 supersededBy = %v, want 2", step1["supersededBy"])
	}
	step2, _ := steps[1].(map[string]any)
	if _, ok := step2["supersededBy"]; ok {
		t.Errorf("step 2 (the revising step) must not itself carry supersededBy, got %v", step2["supersededBy"])
	}
	// Audit trail stays intact — revision is additive, never mutates step 1.
	if step2["isRevision"] != true || step2["revisesStep"] != float64(1) {
		t.Errorf("step 2 revision fields = isRevision:%v revisesStep:%v", step2["isRevision"], step2["revisesStep"])
	}
}

func TestResearchExportMarkdown(t *testing.T) {
	deps := setupTestDeps()
	sid := makeSessionWithSources(t, deps)

	out, res := callTool(t, deps, "research_export", map[string]any{"sessionId": sid})
	if res.IsError {
		t.Fatalf("research_export failed")
	}
	if out["format"] != "markdown" {
		t.Errorf("format = %v, want markdown", out["format"])
	}
	if out["trust"] != "untrusted-external-content" {
		t.Error("missing trust marker")
	}
	doc, _ := out["document"].(string)
	if !strings.Contains(doc, "# How do transformers work?") {
		t.Errorf("markdown missing research goal heading:\n%s", doc)
	}
	if !strings.Contains(doc, "### Step 1") {
		t.Error("markdown missing step section")
	}
	if !strings.Contains(doc, "**Confidence:** high") {
		t.Error("markdown missing confidence provenance")
	}
	if !strings.Contains(doc, "## Sources") {
		t.Error("markdown missing sources section")
	}
	if !strings.Contains(doc, "## Open Questions") {
		t.Error("markdown missing knowledge-gap section")
	}
}

func TestResearchExportJSON(t *testing.T) {
	deps := setupTestDeps()
	sid := makeSessionWithSources(t, deps)

	out, res := callTool(t, deps, "research_export", map[string]any{"sessionId": sid, "format": "json"})
	if res.IsError {
		t.Fatalf("research_export json failed")
	}
	if out["format"] != "json" {
		t.Errorf("format = %v, want json", out["format"])
	}
	doc, ok := out["document"].(map[string]any)
	if !ok {
		t.Fatalf("document should be a structured object for format=json, got %T", out["document"])
	}
	if doc["id"] != sid {
		t.Errorf("document.id = %v, want %v", doc["id"], sid)
	}
}

func TestResearchExportInvalidFormat(t *testing.T) {
	deps := setupTestDeps()
	sid := makeSessionWithSources(t, deps)
	_, res := callTool(t, deps, "research_export", map[string]any{"sessionId": sid, "format": "pdf"})
	if !res.IsError {
		t.Error("invalid format should error")
	}
}

func TestResearchExportRequiresSession(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "research_export", map[string]any{})
	if !res.IsError {
		t.Error("missing sessionId should error")
	}
}

func TestResearchExportUnknownSession(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "research_export", map[string]any{"sessionId": "does-not-exist"})
	if !res.IsError {
		t.Error("unknown session should error")
	}
}
