package tools

import (
	"context"
	"testing"
)

func TestFilingSearchTool(t *testing.T) {
	out, res := callTool(t, setupTestDeps(), "filing_search", map[string]any{"query": "AAPL"})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	if out["provider"] != "edgar" || out["trust"] != "untrusted-external-content" {
		t.Errorf("provider/trust: %v / %v", out["provider"], out["trust"])
	}
	filings, ok := out["filings"].([]any)
	if !ok || len(filings) != 1 {
		t.Fatalf("want 1 filing, got %v", out["filings"])
	}
	f0, _ := filings[0].(map[string]any)
	if f0["formType"] != "10-K" || f0["source"] != "edgar" {
		t.Errorf("unexpected filing: %v", f0)
	}
}

func TestFilingSearchRequiresQuery(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "filing_search", map[string]any{})
	if !res.IsError {
		t.Error("missing query+ticker should error")
	}
}

func TestFilingSearchUnknownProvider(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "filing_search", map[string]any{"query": "x", "provider": "bloomberg"})
	if !res.IsError {
		t.Error("unknown provider should error")
	}
}

func TestLegalSearchTool(t *testing.T) {
	out, res := callTool(t, setupTestDeps(), "legal_search", map[string]any{"query": "miranda"})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	if out["provider"] != "courtlistener" || out["trust"] != "untrusted-external-content" {
		t.Errorf("provider/trust: %v / %v", out["provider"], out["trust"])
	}
	cases, ok := out["cases"].([]any)
	if !ok || len(cases) != 1 {
		t.Fatalf("want 1 case, got %v", out["cases"])
	}
	c0, _ := cases[0].(map[string]any)
	if c0["courtId"] != "scotus" || c0["citation"] != "1 U.S. 1" {
		t.Errorf("unexpected case: %v", c0)
	}
}

func TestLegalSearchRequiresQuery(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "legal_search", map[string]any{})
	if !res.IsError {
		t.Error("missing query should error")
	}
}

func TestEconSearchSeriesMode(t *testing.T) {
	out, res := callTool(t, setupTestDeps(), "econ_search", map[string]any{"query": "gdp"})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	if out["mode"] != "series" || out["provider"] != "fred" {
		t.Errorf("mode/provider: %v / %v", out["mode"], out["provider"])
	}
	results, ok := out["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("want 1 series, got %v", out["results"])
	}
}

func TestEconSearchObservationsMode(t *testing.T) {
	out, res := callTool(t, setupTestDeps(), "econ_search", map[string]any{"series_id": "GDP"})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	if out["mode"] != "observations" || out["seriesId"] != "GDP" {
		t.Errorf("mode/seriesId: %v / %v", out["mode"], out["seriesId"])
	}
}

func TestEconSearchRequiresInput(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "econ_search", map[string]any{})
	if !res.IsError {
		t.Error("missing query+series_id should error")
	}
}

// Each structured-domain tool must NOT register when its provider map is empty.
func TestStructuredToolsUnregisteredWithoutProvider(t *testing.T) {
	deps := setupTestDeps()
	deps.FilingProviders = nil
	deps.CaseProviders = nil
	deps.EconProviders = nil
	tools := toolNamesFor(t, deps)
	for _, name := range []string{"filing_search", "legal_search", "econ_search"} {
		if tools[name] {
			t.Errorf("%s must NOT register without its provider", name)
		}
	}
}

// toolNamesFor lists registered tool names for a given deps set.
func toolNamesFor(t *testing.T, deps Dependencies) map[string]bool {
	t.Helper()
	ctx := context.Background()
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()
	list, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make(map[string]bool, len(list.Tools))
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	return names
}
