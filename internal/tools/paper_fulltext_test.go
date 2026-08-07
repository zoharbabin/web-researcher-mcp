package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// mockOAResolver is a fixed-response search.OAResolver stub for #533 tests —
// it never hits the network, mirroring the other mock*Resolver types in
// tools_test.go.
type mockOAResolver struct {
	oa     bool
	pdfURL string
	found  bool
	err    error
}

func (m *mockOAResolver) Name() string { return "mock-unpaywall" }
func (m *mockOAResolver) Resolve(_ context.Context, _ string) (bool, string, bool, error) {
	return m.oa, m.pdfURL, m.found, m.err
}

// failingPaperFetcher implements search.AcademicProvider + search.PaperFetcher,
// with FetchPaper always returning a non-nil, non-not-found error — modeling a
// genuine upstream failure (rate limit, network, 5xx) as opposed to "no record".
type failingPaperFetcher struct {
	err error
}

func (f *failingPaperFetcher) Name() string { return "semanticscholar" }
func (f *failingPaperFetcher) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "mock failing S2"}
}
func (f *failingPaperFetcher) Scholarly(_ context.Context, _ search.AcademicSearchParams) ([]search.AcademicResult, error) {
	return nil, f.err
}
func (f *failingPaperFetcher) FetchPaper(_ context.Context, _ string) (*search.AcademicResult, error) {
	return nil, f.err
}

// callPaperFulltext calls paper_fulltext through the in-memory MCP client and
// returns the parsed result (or fails the test on a tool-level error).
func callPaperFulltext(t *testing.T, deps Dependencies, args map[string]any) map[string]any {
	t.Helper()
	ctx := context.Background()
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()
	res, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "paper_fulltext", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return out
}

// TestPaperFulltextURLPath: a direct URL scrapes as-is with no metadata lookup —
// source:"direct-url", no authors/doi/abstract fields.
func TestPaperFulltextURLPath(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>A Paper</title></head><body><article><p>` +
			"Some paper content that is long enough to extract cleanly for the test." +
			`</p></article></body></html>`))
	}))
	defer page.Close()

	deps := scrapeTestDeps()
	out := callPaperFulltext(t, deps, map[string]any{"identifier": page.URL})

	if out["source"] != "direct-url" {
		t.Errorf("source = %v, want direct-url", out["source"])
	}
	if out["resolvedUrl"] != page.URL {
		t.Errorf("resolvedUrl = %v, want %v", out["resolvedUrl"], page.URL)
	}
	if _, present := out["doi"]; present {
		t.Error("direct-url path must not carry metadata fields")
	}
	if out["trust"] != "untrusted-external-content" {
		t.Errorf("trust marker missing: %v", out["trust"])
	}
}

// TestPaperFulltextEmptyIdentifier: an empty/whitespace identifier is a
// validation error, not a panic or an upstream call.
func TestPaperFulltextEmptyIdentifier(t *testing.T) {
	ctx := context.Background()
	deps := scrapeTestDeps()
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "paper_fulltext", Arguments: map[string]any{"identifier": "   "}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a validation error for an empty identifier")
	}
}

// TestPaperFulltextDOIFallback exercises the graceful-degradation path: the
// only AcademicProvider wired into setupTestDeps (mockAcademicProvider, keyed
// "openalex") does NOT implement PaperFetcher, so a DOI identifier must fall
// back to the doi.org redirect rather than error.
func TestPaperFulltextDOIFallback(t *testing.T) {
	deps := setupTestDeps()
	if _, ok := deps.AcademicProviders["openalex"].(search.PaperFetcher); ok {
		t.Fatal("precondition: mockAcademicProvider must NOT implement PaperFetcher")
	}

	url, _, errResult, fetchErr := resolvePaperURL(context.Background(), deps, "10.1038/nature12373")
	if errResult != nil {
		t.Fatalf("unexpected error result: %+v", errResult)
	}
	if fetchErr != nil {
		t.Fatalf("unexpected fetch error: %v", fetchErr)
	}
	if url != "https://doi.org/10.1038/nature12373" {
		t.Errorf("resolved URL = %q, want the doi.org redirect", url)
	}
}

// TestPaperFulltextNoFetcherNoDOI: a bare (non-URL, non-DOI) identifier with no
// PaperFetcher configured has no URL to fall back to — a validation error, not
// a panic.
func TestPaperFulltextNoFetcherNoDOI(t *testing.T) {
	deps := setupTestDeps()
	ctx := context.Background()
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "paper_fulltext", Arguments: map[string]any{"identifier": "abc123"}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a validation error when no PaperFetcher is configured and the identifier has no URL fallback")
	}
}

// TestPaperFulltextWithMockS2 is the full happy path: a DOI resolves via a
// stubbed Semantic Scholar provider to an open-access PDF URL, which is then
// scraped, and the returned metadata is overlaid onto the output.
func TestPaperFulltextWithMockS2(t *testing.T) {
	pdf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Original PDF Title</title></head><body><article><p>` +
			"Full text of the open access paper, long enough to extract cleanly here." +
			`</p></article></body></html>`))
	}))
	defer pdf.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"paperId":"abc","externalIds":{"DOI":"10.1038/nature12373"},"title":"A Study","venue":"Nature","year":2013,"citationCount":100,"isOpenAccess":true,"openAccessPdf":{"url":"` + pdf.URL + `"},"tldr":{"text":"Summary."},"authors":[{"name":"A. Smith"}],"abstract":"Long abstract."}`))
	}))
	defer s2.Close()

	s2Provider := search.NewSemanticScholarProvider("", search.Deps{
		HTTPClient: http.DefaultClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	s2Provider.SetBaseURL(s2.URL)

	deps := scrapeTestDeps()
	deps.AcademicProviders = map[string]search.AcademicProvider{"semanticscholar": s2Provider}

	out := callPaperFulltext(t, deps, map[string]any{"identifier": "10.1038/nature12373"})

	if out["source"] != "semanticscholar" {
		t.Errorf("source = %v, want semanticscholar", out["source"])
	}
	if out["resolvedUrl"] != pdf.URL {
		t.Errorf("resolvedUrl = %v, want the OA PDF URL %v", out["resolvedUrl"], pdf.URL)
	}
	if out["doi"] != "10.1038/nature12373" {
		t.Errorf("doi = %v", out["doi"])
	}
	if out["title"] != "A Study" {
		t.Errorf("title should come from Semantic Scholar metadata, got %v", out["title"])
	}
	if out["tldr"] != "Summary." {
		t.Errorf("tldr = %v", out["tldr"])
	}
	if out["citationCount"] != float64(100) {
		t.Errorf("citationCount = %v", out["citationCount"])
	}
	if out["trust"] != "untrusted-external-content" {
		t.Errorf("trust marker missing: %v", out["trust"])
	}
}

// TestPaperFulltextUnpaywallFallback proves the #533 fix: when the Semantic
// Scholar fetcher resolves a DOI but has no PDF URL of its own, paper_fulltext
// must fall back to deps.OAResolver (Unpaywall) instead of degrading straight
// to the S2 landing page — mirroring the enrichment academic_search already
// performs via search.EnrichOpenAccess.
func TestPaperFulltextUnpaywallFallback(t *testing.T) {
	pdf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>OA Copy</title></head><body><article><p>` +
			"Full text served from the Unpaywall-resolved open access location." +
			`</p></article></body></html>`))
	}))
	defer pdf.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Semantic Scholar knows the paper but has no openAccessPdf of its own.
		_, _ = w.Write([]byte(`{"paperId":"abc","externalIds":{"DOI":"10.1038/nature12373"},"title":"A Study","year":2013,"isOpenAccess":false}`))
	}))
	defer s2.Close()

	s2Provider := search.NewSemanticScholarProvider("", search.Deps{
		HTTPClient: http.DefaultClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	s2Provider.SetBaseURL(s2.URL)

	deps := scrapeTestDeps()
	deps.AcademicProviders = map[string]search.AcademicProvider{"semanticscholar": s2Provider}
	deps.OAResolver = &mockOAResolver{oa: true, pdfURL: pdf.URL, found: true}

	out := callPaperFulltext(t, deps, map[string]any{"identifier": "10.1038/nature12373"})

	if out["resolvedUrl"] != pdf.URL {
		t.Errorf("resolvedUrl = %v, want the Unpaywall-resolved PDF URL %v", out["resolvedUrl"], pdf.URL)
	}
	if out["openAccess"] != true {
		t.Errorf("openAccess = %v, want true (Unpaywall found an OA copy)", out["openAccess"])
	}
	if out["source"] != "semanticscholar" {
		t.Errorf("source = %v, want semanticscholar", out["source"])
	}
}

// TestPaperFulltextUnpaywallNeverOverwritesExistingPDFUrl proves the fallback
// never overwrites a fetcher-supplied PDFUrl — Unpaywall is consulted only
// when the fetcher's own metadata has none.
func TestPaperFulltextUnpaywallNeverOverwritesExistingPDFUrl(t *testing.T) {
	s2PDF := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>S2 Copy</title></head><body><article><p>` +
			"Full text served from the Semantic Scholar PDF URL, not Unpaywall's." +
			`</p></article></body></html>`))
	}))
	defer s2PDF.Close()

	unpaywallPDF := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer unpaywallPDF.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"paperId":"abc","externalIds":{"DOI":"10.1038/nature12373"},"title":"A Study","year":2013,"isOpenAccess":true,"openAccessPdf":{"url":"` + s2PDF.URL + `"}}`))
	}))
	defer s2.Close()

	s2Provider := search.NewSemanticScholarProvider("", search.Deps{
		HTTPClient: http.DefaultClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	s2Provider.SetBaseURL(s2.URL)

	deps := scrapeTestDeps()
	deps.AcademicProviders = map[string]search.AcademicProvider{"semanticscholar": s2Provider}
	deps.OAResolver = &mockOAResolver{oa: true, pdfURL: unpaywallPDF.URL, found: true}

	out := callPaperFulltext(t, deps, map[string]any{"identifier": "10.1038/nature12373"})

	if out["resolvedUrl"] != s2PDF.URL {
		t.Errorf("resolvedUrl = %v, want the Semantic Scholar PDF URL %v (must not be overwritten by Unpaywall)", out["resolvedUrl"], s2PDF.URL)
	}
}

// TestPaperFulltextUpstreamErrorNotMisclassifiedAsConfigError: a bare (non-DOI)
// identifier whose PaperFetcher call fails with a genuine upstream error (rate
// limit) must surface as an upstream/rate-limited error, not the generic
// "no provider configured" config_error — the two are not the same failure.
func TestPaperFulltextUpstreamErrorNotMisclassifiedAsConfigError(t *testing.T) {
	deps := setupTestDeps()
	deps.AcademicProviders = map[string]search.AcademicProvider{
		"semanticscholar": &failingPaperFetcher{err: errors.New("rate limited: 429")},
	}

	ctx := context.Background()
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "paper_fulltext", Arguments: map[string]any{"identifier": "abc123"}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error result for an upstream FetchPaper failure")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(strings.ToLower(text), "rate limit") {
		t.Errorf("expected a rate-limit error message, got: %s", text)
	}
	if strings.Contains(strings.ToLower(text), "no semantic scholar provider is configured") {
		t.Errorf("upstream failure must not be reported as a config gap, got: %s", text)
	}
}

// TestPaperFulltextDOIUpstreamErrorStillDegradesButErrIsNotLost verifies the DOI
// path still falls back to the doi.org redirect on an upstream FetchPaper
// failure (existing graceful-degradation contract), while resolvePaperURL
// itself surfaces the error to the caller for audit/metrics visibility.
func TestPaperFulltextDOIUpstreamErrorStillDegradesButErrIsNotLost(t *testing.T) {
	deps := setupTestDeps()
	wantErr := errors.New("upstream 500")
	deps.AcademicProviders = map[string]search.AcademicProvider{
		"semanticscholar": &failingPaperFetcher{err: wantErr},
	}

	url, meta, errResult, fetchErr := resolvePaperURL(context.Background(), deps, "10.1038/nature12373")
	if errResult != nil {
		t.Fatalf("unexpected error result: %+v", errResult)
	}
	if url != "https://doi.org/10.1038/nature12373" {
		t.Errorf("resolved URL = %q, want the doi.org redirect", url)
	}
	if meta != nil {
		t.Errorf("meta = %+v, want nil (fetch failed)", meta)
	}
	if fetchErr == nil {
		t.Fatal("expected fetchErr to be surfaced instead of silently dropped")
	}
}

// TestPaperFulltextIdentifierTooLarge: an identifier beyond the size cap is a
// validation error, not a call into any provider.
func TestPaperFulltextIdentifierTooLarge(t *testing.T) {
	ctx := context.Background()
	deps := scrapeTestDeps()
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	huge := strings.Repeat("a", maxPaperFulltextIdentifierBytes+1)
	res, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "paper_fulltext", Arguments: map[string]any{"identifier": huge}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a validation error for an oversized identifier")
	}
}
