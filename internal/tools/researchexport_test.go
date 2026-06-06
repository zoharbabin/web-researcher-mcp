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
