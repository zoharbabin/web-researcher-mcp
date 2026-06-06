package tools

import (
	"context"
	"testing"
)

func TestCitationGraphBoth(t *testing.T) {
	out, res := callTool(t, setupTestDeps(), "citation_graph", map[string]any{"paper": "10.1/x"})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	if out["seed"] != "10.1/x" || out["direction"] != "both" {
		t.Errorf("seed/direction: %v / %v", out["seed"], out["direction"])
	}
	if out["provider"] != "openalex" {
		t.Errorf("provider should be the configured academic provider, got %v", out["provider"])
	}
	if out["trust"] != "untrusted-external-content" {
		t.Errorf("missing trust marker")
	}
	// both directions present
	if out["citedBy"] == nil || out["references"] == nil {
		t.Errorf("both citedBy and references expected for direction=both")
	}
	if cb, _ := out["citedByCount"].(float64); cb != 1 {
		t.Errorf("citedByCount=%v want 1", out["citedByCount"])
	}
}

func TestCitationGraphDirectionFilter(t *testing.T) {
	out, res := callTool(t, setupTestDeps(), "citation_graph", map[string]any{"paper": "10.1/x", "direction": "cited_by"})
	if res.IsError {
		t.Fatalf("unexpected error")
	}
	if _, ok := out["references"]; ok {
		t.Error("references must be absent when direction=cited_by")
	}
	if out["citedBy"] == nil {
		t.Error("citedBy must be present")
	}
}

func TestCitationGraphRequiresPaper(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "citation_graph", map[string]any{})
	if !res.IsError {
		t.Error("empty paper should error")
	}
}

func TestCitationGraphInvalidDirection(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "citation_graph", map[string]any{"paper": "x", "direction": "sideways"})
	if !res.IsError {
		t.Error("invalid direction should error")
	}
}

func TestCitationGraphInfluentialOnly(t *testing.T) {
	// mock: citedBy has IsInfluential=true (kept), references has false (dropped).
	out, res := callTool(t, setupTestDeps(), "citation_graph", map[string]any{"paper": "10.1/x", "influential_only": true})
	if res.IsError {
		t.Fatalf("unexpected error")
	}
	if cb, _ := out["citedByCount"].(float64); cb != 1 {
		t.Errorf("influential citedBy kept: got %v", out["citedByCount"])
	}
	if rc, _ := out["referencesCount"].(float64); rc != 0 {
		t.Errorf("non-influential references should be filtered out, got %v", out["referencesCount"])
	}
}

func TestCitationGraphUnknownProvider(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "citation_graph", map[string]any{"paper": "x", "provider": "perplexity"})
	if !res.IsError {
		t.Error("unknown citation provider should be rejected")
	}
}

func TestCitationGraphUnregisteredWithoutProvider(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.AcademicProviders = nil // no citation-capable provider
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()
	list, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	for _, tool := range list.Tools {
		if tool.Name == "citation_graph" {
			t.Error("citation_graph must NOT register without a citation provider")
		}
	}
}
