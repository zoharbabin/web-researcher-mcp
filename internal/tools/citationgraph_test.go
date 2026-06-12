package tools

import (
	"context"
	"fmt"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// failingS2Provider implements AcademicProvider + CitationSearcher but always
// 404s its citation lookups with Semantic Scholar's "paper not found" wording —
// modeling a heavily-cited paper that is simply absent from SS's keyless graph.
// Named "semanticscholar" so the auto-select path picks it first.
type failingS2Provider struct{}

func (f *failingS2Provider) Name() string { return "semanticscholar" }
func (f *failingS2Provider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "mock S2 (always 404)"}
}
func (f *failingS2Provider) Scholarly(_ context.Context, _ search.AcademicSearchParams) ([]search.AcademicResult, error) {
	return nil, fmt.Errorf("semanticscholar: paper not found")
}
func (f *failingS2Provider) Citations(_ context.Context, _ string, _ int) ([]search.AcademicResult, error) {
	return nil, fmt.Errorf("semanticscholar: paper not found")
}
func (f *failingS2Provider) References(_ context.Context, _ string, _ int) ([]search.AcademicResult, error) {
	return nil, fmt.Errorf("semanticscholar: paper not found")
}

// depsWithS2AndOpenAlex wires BOTH a failing semanticscholar (preferred on
// auto-select) and the working openalex mock, so the #228 auto-fallback can be
// exercised end-to-end.
func depsWithS2AndOpenAlex() Dependencies {
	deps := setupTestDeps()
	deps.AcademicProviders = map[string]search.AcademicProvider{
		"semanticscholar": &failingS2Provider{},
		"openalex":        &mockAcademicProvider{},
	}
	return deps
}

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

// TestCitationGraphAutoFallbackToOpenAlex is the #228 regression guard: when no
// provider is pinned and Semantic Scholar 404s the seed ("paper not found"), the
// traversal must transparently retry on OpenAlex and succeed, reporting
// provider=openalex (proof the fallback fired).
func TestCitationGraphAutoFallbackToOpenAlex(t *testing.T) {
	out, res := callTool(t, depsWithS2AndOpenAlex(), "citation_graph", map[string]any{"paper": "10.1038/nature14539"})
	if res.IsError {
		t.Fatalf("auto-select should fall back to OpenAlex, got error result")
	}
	if out["provider"] != "openalex" {
		t.Errorf("provider=%v, want openalex (fallback must have fired)", out["provider"])
	}
	if cb, _ := out["citedByCount"].(float64); cb != 1 {
		t.Errorf("citedByCount=%v want 1 (OpenAlex mock result)", out["citedByCount"])
	}
}

// TestCitationGraphExplicitProviderNoFallback enforces Design Rule 7: an EXPLICIT
// provider is honored exclusively. When the caller pins semanticscholar and it
// 404s, the tool must surface the error — never silently substitute OpenAlex.
func TestCitationGraphExplicitProviderNoFallback(t *testing.T) {
	_, res := callTool(t, depsWithS2AndOpenAlex(), "citation_graph", map[string]any{
		"paper":    "10.1038/nature14539",
		"provider": "semanticscholar",
	})
	if !res.IsError {
		t.Error("explicit semanticscholar must surface its error, not silently fall back to OpenAlex")
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
