package tools

import (
	"context"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

func TestIsPlaceholderDOI(t *testing.T) {
	t.Parallel()
	cases := []struct {
		doi  string
		want bool
	}{
		{"10.5555/12345678", true},
		{"doi:10.5555/more.testing.qwerty", true},
		{"https://doi.org/10.5555/test", true},
		{"10.1038/nature14539", false},
		{"10.1016/j.cell.2020.01.001", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isPlaceholderDOI(c.doi); got != c.want {
			t.Errorf("isPlaceholderDOI(%q)=%v want %v", c.doi, got, c.want)
		}
	}
}

func TestFilterPlaceholderResults(t *testing.T) {
	t.Parallel()
	in := []search.AcademicResult{
		{Title: "Real Paper", DOI: "10.1038/x"},
		{Title: "more testing qwerty", DOI: "10.5555/abc"},
		{Title: "Another Real", DOI: "10.1016/y"},
	}
	out := filterPlaceholderResults(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 real results after filtering, got %d", len(out))
	}
	for _, r := range out {
		if isPlaceholderDOI(r.DOI) {
			t.Errorf("placeholder %q survived the filter", r.DOI)
		}
	}
}

// placeholderAcademicProvider returns only a Crossref test-prefix record — the
// noise a nonsense query yields. Named "openalex" so it's the academic provider
// the default strategies pick.
type placeholderAcademicProvider struct{}

func (p *placeholderAcademicProvider) Name() string { return "openalex" }
func (p *placeholderAcademicProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "mock (placeholder)"}
}
func (p *placeholderAcademicProvider) Scholarly(_ context.Context, _ search.AcademicSearchParams) ([]search.AcademicResult, error) {
	return []search.AcademicResult{
		{Title: "more testing qwerty", URL: "https://doi.org/10.5555/12345678", DOI: "10.5555/12345678", Source: "openalex"},
	}, nil
}
func (p *placeholderAcademicProvider) Citations(_ context.Context, _ string, _ int) ([]search.AcademicResult, error) {
	return nil, nil
}
func (p *placeholderAcademicProvider) References(_ context.Context, _ string, _ int) ([]search.AcademicResult, error) {
	return nil, nil
}

// TestAcademicSearchPlaceholderTriggersHints is the #229 regression guard: when a
// provider returns only Crossref test-prefix noise, academic_search must drop it
// and surface the empty-result hints object rather than passing junk through.
func TestAcademicSearchPlaceholderTriggersHints(t *testing.T) {
	deps := setupTestDeps()
	deps.AcademicProviders = map[string]search.AcademicProvider{"openalex": &placeholderAcademicProvider{}}

	out, res := callTool(t, deps, "academic_search", map[string]any{"query": "asdkjfh qwerty nonsense xyz123 paper"})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	if tr, _ := out["totalResults"].(float64); tr != 0 {
		t.Errorf("totalResults=%v, want 0 (placeholder noise must be filtered)", out["totalResults"])
	}
	if _, ok := out["hints"]; !ok {
		t.Error("expected a hints object on a low-signal result")
	}
	if papers, _ := out["papers"].([]any); len(papers) != 0 {
		t.Errorf("papers must be empty, got %d", len(papers))
	}
}
