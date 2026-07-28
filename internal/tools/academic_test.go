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
// fullTextAcademicProvider records whether Scholarly was called with
// FullText=true and returns a result carrying FullText when it was. Named
// "pubmed" so it's selected by resolveAcademicSearcher when provider=pubmed.
type fullTextAcademicProvider struct {
	gotFullText bool
}

func (p *fullTextAcademicProvider) Name() string { return "pubmed" }
func (p *fullTextAcademicProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "mock (fulltext)"}
}
func (p *fullTextAcademicProvider) Scholarly(_ context.Context, params search.AcademicSearchParams) ([]search.AcademicResult, error) {
	p.gotFullText = params.FullText
	r := search.AcademicResult{Title: "A PMC Paper", URL: "https://pubmed.ncbi.nlm.nih.gov/1/", Source: "pubmed"}
	if params.FullText {
		r.FullText = "The full extracted body text."
	}
	return []search.AcademicResult{r}, nil
}

// TestAcademicSearchFullText is the #268 integration guard: full_text=true
// propagates through to AcademicSearchParams.FullText and the resulting
// AcademicResult.FullText renders as papers[].fullText in tool output,
// matching academicSearchOutputSchema (guards TestOutputSchemaMatchesResponse).
func TestAcademicSearchFullText(t *testing.T) {
	deps := setupTestDeps()
	provider := &fullTextAcademicProvider{}
	deps.AcademicProviders = map[string]search.AcademicProvider{"pubmed": provider}

	out, res := callTool(t, deps, "academic_search", map[string]any{
		"query": "CRISPR", "provider": "pubmed", "full_text": true,
	})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	if !provider.gotFullText {
		t.Error("AcademicSearchParams.FullText was not propagated to the provider")
	}
	papers, _ := out["papers"].([]any)
	if len(papers) != 1 {
		t.Fatalf("expected 1 paper, got %d", len(papers))
	}
	paper := papers[0].(map[string]any)
	if paper["fullText"] != "The full extracted body text." {
		t.Errorf("fullText = %v, want the provider's full text", paper["fullText"])
	}
}

// scholarAPIStubProvider is a mock standing in for the real ScholarAPI
// provider (#266) — named "scholarapi" so resolveAcademicSearcher's
// isKnownAcademicName/explicit-only path picks it by name.
type scholarAPIStubProvider struct{}

func (p *scholarAPIStubProvider) Name() string { return "scholarapi" }
func (p *scholarAPIStubProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "metered", Description: "mock (scholarapi)"}
}
func (p *scholarAPIStubProvider) Scholarly(_ context.Context, _ search.AcademicSearchParams) ([]search.AcademicResult, error) {
	return []search.AcademicResult{
		{Title: "Full-Text Paper", DOI: "10.1/ft", Source: "scholarapi", HasText: true, HasPDF: false},
	}, nil
}

// TestAcademicSearchScholarAPIExplicitProvider is the #266 integration guard:
// a provider only listed in AcademicProvidersExplicitOnly (never
// SupportedAcademicProviders) must still resolve and serve results when
// requested by name via provider=scholarapi, and its hasText/hasPdf fields
// must reach the tool's JSON output.
func TestAcademicSearchScholarAPIExplicitProvider(t *testing.T) {
	deps := setupTestDeps()
	deps.AcademicProviders = map[string]search.AcademicProvider{"scholarapi": &scholarAPIStubProvider{}}

	out, res := callTool(t, deps, "academic_search", map[string]any{
		"query": "gene editing", "provider": "scholarapi",
	})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	papers, _ := out["papers"].([]any)
	if len(papers) != 1 {
		t.Fatalf("expected 1 paper, got %d", len(papers))
	}
	paper := papers[0].(map[string]any)
	if paper["hasText"] != true {
		t.Errorf("hasText = %v, want true", paper["hasText"])
	}
	if _, ok := paper["hasPdf"]; ok {
		t.Errorf("hasPdf should be omitted when false, got %v", paper["hasPdf"])
	}
}

// TestAcademicSearchScholarAPINotAutoSelected proves scholarapi is never
// chosen when no provider is requested — isKnownAcademicName gates it out of
// the default (empty-provider) strategies just as SupportedAcademicProviders
// gates auto-routing elsewhere.
func TestAcademicSearchScholarAPINotAutoSelected(t *testing.T) {
	deps := setupTestDeps()
	deps.AcademicProviders = map[string]search.AcademicProvider{"scholarapi": &scholarAPIStubProvider{}}

	out, res := callTool(t, deps, "academic_search", map[string]any{"query": "gene editing"})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	// No provider requested and no auto-routed provider configured: must not
	// silently reach scholarapi. Result should fall through to the web-search
	// strategy (empty papers here since setupTestDeps' default search stub
	// returns none), never scholarapi's stubbed paper.
	papers, _ := out["papers"].([]any)
	for _, p := range papers {
		if paper, ok := p.(map[string]any); ok && paper["source"] == "scholarapi" {
			t.Fatalf("scholarapi must not be auto-selected without an explicit provider request: %v", paper)
		}
	}
}

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
