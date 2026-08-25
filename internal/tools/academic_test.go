package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
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

// TestNormalizeAcademicTitle proves titles that differ only by casing,
// punctuation, or whitespace normalize to the same comparison key, while
// genuinely different titles do not.
func TestNormalizeAcademicTitle(t *testing.T) {
	t.Parallel()
	if got := normalizeAcademicTitle("Attention Is All You Need"); got != normalizeAcademicTitle("attention is all you need.") {
		t.Errorf("expected casing/punctuation-insensitive match, got %q vs %q", got, normalizeAcademicTitle("attention is all you need."))
	}
	if got := normalizeAcademicTitle("Attention Is All You Need"); got == normalizeAcademicTitle("BERT: Pre-training") {
		t.Errorf("different titles must not collide: %q", got)
	}
	if normalizeAcademicTitle("   ") != "" {
		t.Error("all-whitespace title should normalize to empty")
	}
	if normalizeAcademicTitle("") != "" {
		t.Error("empty title should normalize to empty")
	}
}

// TestFlagLowConfidenceDomains_SpamMirror is the #509 regression guard: a
// spam/mirror record title-matching a famous paper, hosted off a recognized
// publisher/preprint domain with an implausibly low citation count next to
// the genuine highly-cited peer in the same response, must be flagged.
func TestFlagLowConfidenceDomains_SpamMirror(t *testing.T) {
	t.Parallel()
	in := []search.AcademicResult{
		{
			Title:         "Attention Is All You Need",
			URL:           "https://doi.org/10.48550/arXiv.1706.03762",
			PDFUrl:        "https://arxiv.org/pdf/1706.03762",
			CitationCount: 95000,
			Source:        "openalex",
		},
		{
			// Spam/mirror record: same title, low-quality unrecognized host, tiny
			// citation count relative to the genuine paper above.
			Title:         "Attention is all you need",
			URL:           "https://doi.org/10.9999/mirror.spam.1234",
			PDFUrl:        "https://sketchy-paper-mirror.example/attention.pdf",
			CitationCount: 2,
			Source:        "openalex",
		},
	}
	out := flagLowConfidenceDomains(in)
	if out[0].LowConfidenceDomain {
		t.Error("the well-cited genuine paper must never be flagged")
	}
	if !out[1].LowConfidenceDomain {
		t.Error("the low-cited spam/mirror record on an unrecognized domain must be flagged")
	}
}

// TestFlagLowConfidenceDomains_RecognizedDomainNeverFlagged proves a result
// hosted on a recognized publisher/preprint domain (content.IsAcademicHost)
// is never flagged, even if its citation count trails a same-titled peer —
// e.g. two independently-indexed copies of the same paper on arxiv.org and
// nature.com, where a citation-count lag alone is not spam.
func TestFlagLowConfidenceDomains_RecognizedDomainNeverFlagged(t *testing.T) {
	t.Parallel()
	in := []search.AcademicResult{
		{Title: "Attention Is All You Need", PDFUrl: "https://arxiv.org/pdf/1706.03762", CitationCount: 95000},
		{Title: "Attention Is All You Need", PDFUrl: "https://www.nature.com/articles/attention", CitationCount: 5},
	}
	out := flagLowConfidenceDomains(in)
	for i, r := range out {
		if r.LowConfidenceDomain {
			t.Errorf("result %d on a recognized publisher domain must never be flagged", i)
		}
	}
}

// TestFlagLowConfidenceDomains_NoWellCitedPeerLeftUnflagged proves the
// heuristic requires a genuinely well-cited peer to compare against — two
// lightly-cited results sharing a title, none of them above
// lowConfidenceMinPeerCitations, must not be flagged (nothing to compare to).
func TestFlagLowConfidenceDomains_NoWellCitedPeerLeftUnflagged(t *testing.T) {
	t.Parallel()
	in := []search.AcademicResult{
		{Title: "An Obscure Paper", PDFUrl: "https://unrecognized-host.example/a.pdf", CitationCount: 1},
		{Title: "An Obscure Paper", PDFUrl: "https://another-unrecognized-host.example/b.pdf", CitationCount: 3},
	}
	out := flagLowConfidenceDomains(in)
	for i, r := range out {
		if r.LowConfidenceDomain {
			t.Errorf("result %d must not be flagged with no well-cited peer to compare against", i)
		}
	}
}

// TestFlagLowConfidenceDomains_SingleResultUntouched proves the fast path
// (fewer than 2 results — nothing to compare) leaves the slice unflagged.
func TestFlagLowConfidenceDomains_SingleResultUntouched(t *testing.T) {
	t.Parallel()
	in := []search.AcademicResult{
		{Title: "Solo Paper", PDFUrl: "https://unrecognized-host.example/a.pdf", CitationCount: 0},
	}
	out := flagLowConfidenceDomains(in)
	if out[0].LowConfidenceDomain {
		t.Error("a lone result has no peer to compare against and must not be flagged")
	}
}

// spamMirrorOpenAlexResponse is a fixture OpenAlex /works response modeling
// the #509 report: the genuine, highly-cited "Attention Is All You Need"
// alongside a spam/mirror record that title-matches it but is hosted on an
// unrecognized domain with an implausible (near-zero) citation count and an
// implausible DOI.
const spamMirrorOpenAlexResponse = `{
  "results": [
    {
      "display_name": "Attention Is All You Need",
      "doi": "https://doi.org/10.48550/arXiv.1706.03762",
      "publication_year": 2017,
      "cited_by_count": 95000,
      "primary_location": {"source": {"display_name": "NeurIPS"}},
      "open_access": {"is_oa": true, "oa_url": "https://arxiv.org/pdf/1706.03762"}
    },
    {
      "display_name": "Attention Is All You Need",
      "doi": "https://doi.org/10.9999/sketchy.mirror.99887766",
      "publication_year": 2023,
      "cited_by_count": 1,
      "primary_location": {"source": {"display_name": "Sketchy Paper Mirror"}},
      "open_access": {"is_oa": true, "oa_url": "https://sketchy-paper-mirror.example/attention-is-all-you-need.pdf"}
    }
  ]
}`

// recognizedDomainOpenAlexResponse is a fixture from a recognized publisher
// domain only — the no-flag control case.
const recognizedDomainOpenAlexResponse = `{
  "results": [
    {
      "display_name": "Attention Is All You Need",
      "doi": "https://doi.org/10.48550/arXiv.1706.03762",
      "publication_year": 2017,
      "cited_by_count": 95000,
      "primary_location": {"source": {"display_name": "NeurIPS"}},
      "open_access": {"is_oa": true, "oa_url": "https://arxiv.org/pdf/1706.03762"}
    }
  ]
}`

// fixtureOpenAlexProvider replays a canned OpenAlex-shaped response through
// the same parser the real provider uses (search.parseOpenAlexResponse via an
// httptest server), so these tests exercise the tool's actual end-to-end
// pipeline (parse → filter → enrich → flagLowConfidenceDomains → render)
// against a realistic fixture rather than hand-built AcademicResult structs.
func newFixtureOpenAlexProvider(t *testing.T, body string) search.AcademicProvider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	p := search.NewOpenAlexProvider("test@example.com", search.Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	p.SetBaseURL(srv.URL)
	return p
}

// TestAcademicSearchFlagsSpamMirrorFromUnrecognizedDomain is the #509
// end-to-end regression guard: academic_search's output for a query that
// OpenAlex answers with a genuine highly-cited paper PLUS a spam/mirror
// record (unrecognized host, near-zero citations) must flag only the
// mirror's lowConfidenceDomain, leaving the genuine paper unflagged.
func TestAcademicSearchFlagsSpamMirrorFromUnrecognizedDomain(t *testing.T) {
	deps := setupTestDeps()
	deps.AcademicProviders = map[string]search.AcademicProvider{
		"openalex": newFixtureOpenAlexProvider(t, spamMirrorOpenAlexResponse),
	}

	out, res := callTool(t, deps, "academic_search", map[string]any{
		"query": "transformer attention mechanism", "provider": "openalex",
	})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	papers, _ := out["papers"].([]any)
	if len(papers) != 2 {
		t.Fatalf("expected 2 papers, got %d", len(papers))
	}

	var genuineFlagged, mirrorFlagged bool
	for _, p := range papers {
		paper := p.(map[string]any)
		flagged, _ := paper["lowConfidenceDomain"].(bool)
		if paper["citationCount"] == float64(95000) {
			genuineFlagged = flagged
		} else {
			mirrorFlagged = flagged
		}
	}
	if genuineFlagged {
		t.Error("the genuine, highly-cited arxiv.org paper must never be flagged")
	}
	if !mirrorFlagged {
		t.Error("the spam/mirror record on an unrecognized domain must be flagged lowConfidenceDomain")
	}
}

// TestAcademicSearchNoFlagFromRecognizedDomainOnly proves a response from a
// recognized publisher/preprint domain alone never carries the
// lowConfidenceDomain field — the field must be entirely omitted, matching
// the schema's omitempty contract.
func TestAcademicSearchNoFlagFromRecognizedDomainOnly(t *testing.T) {
	deps := setupTestDeps()
	deps.AcademicProviders = map[string]search.AcademicProvider{
		"openalex": newFixtureOpenAlexProvider(t, recognizedDomainOpenAlexResponse),
	}

	out, res := callTool(t, deps, "academic_search", map[string]any{
		"query": "transformer attention mechanism", "provider": "openalex",
	})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	papers, _ := out["papers"].([]any)
	if len(papers) != 1 {
		t.Fatalf("expected 1 paper, got %d", len(papers))
	}
	paper := papers[0].(map[string]any)
	if _, present := paper["lowConfidenceDomain"]; present {
		t.Errorf("lowConfidenceDomain must be omitted for a recognized-domain-only response, got %v", paper["lowConfidenceDomain"])
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

// laddderStepAcademicProvider is a mock academic provider that returns a
// fixed name, a fixed error (nil for a healthy provider), and results.
type ladderStepAcademicProvider struct {
	name    string
	err     error
	results []search.AcademicResult
}

func (p *ladderStepAcademicProvider) Name() string { return p.name }
func (p *ladderStepAcademicProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "mock (ladder step)"}
}
func (p *ladderStepAcademicProvider) Scholarly(_ context.Context, _ search.AcademicSearchParams) ([]search.AcademicResult, error) {
	return p.results, p.err
}
func (p *ladderStepAcademicProvider) Citations(_ context.Context, _ string, _ int) ([]search.AcademicResult, error) {
	return nil, nil
}
func (p *ladderStepAcademicProvider) References(_ context.Context, _ string, _ int) ([]search.AcademicResult, error) {
	return nil, nil
}

// TestAcademicSearchSkipsCircuitOpenProviderInLadder is the #697 regression
// guard for the #503 anti-pattern reintroduced via isRateLimitError's #664
// widening to match circuit.ErrCircuitOpen: Strategy 3's provider ladder
// (search.SupportedAcademicProviders order: openalex, crossref, pubmed,
// semanticscholar, core, exa) must skip a mid-ladder provider whose circuit
// breaker is open rather than aborting the whole ladder — a healthy provider
// listed later (core) must still be reached.
func TestAcademicSearchSkipsCircuitOpenProviderInLadder(t *testing.T) {
	deps := setupTestDeps()
	deps.AcademicProviders = map[string]search.AcademicProvider{
		"openalex":        &ladderStepAcademicProvider{name: "openalex"}, // no results, no error
		"semanticscholar": &ladderStepAcademicProvider{name: "semanticscholar", err: circuit.ErrCircuitOpen},
		"core": &ladderStepAcademicProvider{name: "core", results: []search.AcademicResult{
			{Title: "Healthy Result", DOI: "10.1/healthy", Source: "core"},
		}},
	}

	out, res := callTool(t, deps, "academic_search", map[string]any{"query": "gene editing"})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	if out["source"] != "core" {
		t.Fatalf("source = %v, want %q (a circuit-open provider must not abort the ladder for healthy providers after it)", out["source"], "core")
	}
	papers, _ := out["papers"].([]any)
	if len(papers) != 1 {
		t.Fatalf("expected 1 paper from core, got %d", len(papers))
	}
}
