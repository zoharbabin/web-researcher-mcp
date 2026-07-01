package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/config"
	"github.com/zoharbabin/web-researcher-mcp/internal/consent"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/memory"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/persist"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
	"github.com/zoharbabin/web-researcher-mcp/internal/useranalytics"
	"github.com/zoharbabin/web-researcher-mcp/internal/workspace"
)

type mockProvider struct{}

func (m *mockProvider) Web(_ context.Context, params search.WebSearchParams) ([]search.SearchResult, error) {
	return []search.SearchResult{
		{Title: "Test Result", URL: "https://example.com", Snippet: "A test snippet", PublishedAt: "2026-05-01T12:00:00Z"},
	}, nil
}

func (m *mockProvider) Images(_ context.Context, params search.ImageSearchParams) ([]search.ImageResult, error) {
	return []search.ImageResult{
		{Title: "Test Image", Link: "https://example.com/img.png", DisplayLink: "example.com"},
	}, nil
}

func (m *mockProvider) News(_ context.Context, params search.NewsSearchParams) ([]search.NewsResult, error) {
	return []search.NewsResult{
		{Title: "Test News", URL: "https://news.example.com/story", Source: "Example News", Snippet: "News snippet"},
	}, nil
}

func (m *mockProvider) Name() string { return "mock" }

// captureProvider records the WebSearchParams of the last Web call so a test can
// assert how the tool layer transformed the input (e.g. that a lens cleared the
// site filter and injected its domains into the query).
type captureProvider struct{ last search.WebSearchParams }

func (m *captureProvider) Web(_ context.Context, params search.WebSearchParams) ([]search.SearchResult, error) {
	m.last = params
	return []search.SearchResult{{Title: "Test Result", URL: "https://example.com", Snippet: "snip"}}, nil
}
func (m *captureProvider) Images(_ context.Context, _ search.ImageSearchParams) ([]search.ImageResult, error) {
	return nil, nil
}
func (m *captureProvider) News(_ context.Context, _ search.NewsSearchParams) ([]search.NewsResult, error) {
	return nil, nil
}
func (m *captureProvider) Name() string { return "capture" }

// emptyWebProvider returns no web results, to exercise the search_and_scrape
// zero-results success branch.
type emptyWebProvider struct{}

func (m *emptyWebProvider) Web(_ context.Context, _ search.WebSearchParams) ([]search.SearchResult, error) {
	return []search.SearchResult{}, nil
}

func (m *emptyWebProvider) Images(_ context.Context, _ search.ImageSearchParams) ([]search.ImageResult, error) {
	return []search.ImageResult{}, nil
}

func (m *emptyWebProvider) News(_ context.Context, _ search.NewsSearchParams) ([]search.NewsResult, error) {
	return []search.NewsResult{}, nil
}

func (m *emptyWebProvider) Name() string { return "empty" }

type mockProviderWithURL struct {
	url string
}

func (m *mockProviderWithURL) Web(_ context.Context, params search.WebSearchParams) ([]search.SearchResult, error) {
	return []search.SearchResult{
		{Title: "Test Result", URL: m.url, Snippet: "A test snippet"},
	}, nil
}

func (m *mockProviderWithURL) Images(_ context.Context, params search.ImageSearchParams) ([]search.ImageResult, error) {
	return []search.ImageResult{}, nil
}

func (m *mockProviderWithURL) News(_ context.Context, params search.NewsSearchParams) ([]search.NewsResult, error) {
	return []search.NewsResult{}, nil
}

func (m *mockProviderWithURL) Name() string { return "mock-with-url" }

func newTestBreaker() *circuit.Breaker {
	return circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60})
}

// mockSynthProvider implements the provider-independent AnswerProvider and
// StructuredProvider interfaces, so wiring it into AnswerProviders/
// StructuredProviders makes the conditionally-registered `answer` and
// `structured_search` tools visible to the CI drift tests. It is vendor-neutral
// (Name "mocksynth") — the tools must work for any provider, not just Exa.
type mockSynthProvider struct{}

func (m *mockSynthProvider) Name() string { return "mocksynth" }

func (m *mockSynthProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "mock synthesis provider"}
}

func (m *mockSynthProvider) Answer(_ context.Context, _ search.AnswerParams) (*search.AnswerResult, error) {
	return &search.AnswerResult{
		Answer:    "A test answer.",
		Citations: []search.Citation{{Title: "Source", URL: "https://example.com", PublishedDate: "2026-01-01"}},
		Provider:  "mocksynth",
		CostUSD:   0.005,
	}, nil
}

func (m *mockSynthProvider) StructuredSearch(_ context.Context, p search.StructuredParams) (*search.StructuredResult, error) {
	return &search.StructuredResult{
		Results:  []search.StructuredItem{{Title: "Item", URL: "https://example.com"}},
		Provider: "mocksynth",
		CostUSD:  0.007,
	}, nil
}

// mockAcademicProvider implements AcademicProvider + CitationSearcher, so wiring
// it into AcademicProviders makes citation_graph register and exercises the
// academic_search academic path in tests.
type mockAcademicProvider struct{}

func (m *mockAcademicProvider) Name() string { return "openalex" }
func (m *mockAcademicProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "mock academic"}
}
func (m *mockAcademicProvider) Scholarly(_ context.Context, _ search.AcademicSearchParams) ([]search.AcademicResult, error) {
	return []search.AcademicResult{{Title: "Mock Paper", URL: "https://doi.org/10.1/x", DOI: "10.1/x", Year: 2024, Source: "openalex"}}, nil
}
func (m *mockAcademicProvider) Citations(_ context.Context, _ string, _ int) ([]search.AcademicResult, error) {
	return []search.AcademicResult{{Title: "Cites It", URL: "https://doi.org/10.2/y", DOI: "10.2/y", Year: 2025, Source: "openalex", IsInfluential: true}}, nil
}
func (m *mockAcademicProvider) References(_ context.Context, _ string, _ int) ([]search.AcademicResult, error) {
	return []search.AcademicResult{{Title: "Foundational", URL: "https://doi.org/10.0/z", DOI: "10.0/z", Year: 2017, Source: "openalex"}}, nil
}

// ResolveByDOI implements the DOIResolver capability: it returns the exact record
// ONLY for the DOI it knows (10.1234/x, a valid-shaped DOI the doiPattern accepts),
// and nil for anything else — modeling a real entity lookup that has no record for
// a fabricated/unknown DOI.
func (m *mockAcademicProvider) ResolveByDOI(_ context.Context, doi string) (*search.AcademicResult, error) {
	if doi == "10.1234/x" {
		return &search.AcademicResult{Title: "Mock Paper", URL: "https://doi.org/10.1234/x", DOI: "10.1234/x", Year: 2024, Source: "openalex"}, nil
	}
	return nil, nil
}

// mockFilingProvider implements FilingProvider, so wiring it makes filing_search
// register and exercises the EDGAR path in the drift tests.
type mockFilingProvider struct{}

func (m *mockFilingProvider) Name() string { return "edgar" }
func (m *mockFilingProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"US"}, RateClass: "free", Description: "mock edgar"}
}
func (m *mockFilingProvider) Filings(_ context.Context, _ search.FilingSearchParams) ([]search.FilingResult, error) {
	return []search.FilingResult{{Company: "Mock Corp", CIK: "0000320193", FormType: "10-K", FilingDate: "2024-01-01", Accession: "0000320193-24-000001", URL: "https://www.sec.gov/Archives/edgar/data/320193/x.htm", Source: "edgar"}}, nil
}

// mockCaseProvider implements CaseProvider for legal_search.
type mockCaseProvider struct{}

func (m *mockCaseProvider) Name() string { return "courtlistener" }
func (m *mockCaseProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"US"}, RateClass: "free", Description: "mock courtlistener"}
}
func (m *mockCaseProvider) Cases(_ context.Context, _ search.CaseSearchParams) ([]search.CaseResult, error) {
	return []search.CaseResult{{CaseName: "Mock v. Test", Citation: "1 U.S. 1", Court: "Supreme Court", CourtID: "scotus", DateFiled: "2024-01-01", CitationCount: 3, URL: "https://www.courtlistener.com/opinion/1/mock/", Source: "courtlistener"}}, nil
}

// mockEconProvider implements EconProvider for econ_search.
type mockEconProvider struct{}

func (m *mockEconProvider) Name() string { return "fred" }
func (m *mockEconProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"US"}, RateClass: "free", Description: "mock fred"}
}
func (m *mockEconProvider) Econ(_ context.Context, _ search.EconSearchParams) ([]search.EconResult, error) {
	return []search.EconResult{{SeriesID: "GDP", Title: "Gross Domestic Product", Units: "Billions", Frequency: "Quarterly", Source: "fred"}}, nil
}

// mockTrialProvider implements TrialProvider for clinical_search.
type mockTrialProvider struct{}

func (m *mockTrialProvider) Name() string { return "clinicaltrials" }
func (m *mockTrialProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "mock clinicaltrials"}
}
func (m *mockTrialProvider) Trials(_ context.Context, _ search.TrialSearchParams) ([]search.TrialResult, error) {
	return []search.TrialResult{{NCTID: "NCT00000000", Title: "Mock Trial", Status: "COMPLETED", Phases: []string{"PHASE1"}, Conditions: []string{"Covid19"}, Interventions: []string{"MockDrug"}, Sponsor: "Mock Sponsor", StartDate: "2024-01-01", HasResults: true, URL: "https://clinicaltrials.gov/study/NCT00000000", Source: "clinicaltrials"}}, nil
}

// mockContextSearcherProvider wraps mockProvider and additionally implements
// ContextSearcher so the search_and_scrape tool exercises the fast-path branch.
type mockContextSearcherProvider struct {
	mockProvider
	ctxResult *search.ContextResult
	ctxErr    error
}

func (m *mockContextSearcherProvider) Context(_ context.Context, _ search.ContextParams) (*search.ContextResult, error) {
	return m.ctxResult, m.ctxErr
}

// mockContextSearcherProviderWithURL extends mockProviderWithURL with a
// ContextSearcher implementation that always returns an error, exercising the
// fallback path in search_and_scrape.
type mockContextSearcherProviderWithURL struct {
	mockProviderWithURL
	ctxErr error
}

func (m *mockContextSearcherProviderWithURL) Context(_ context.Context, _ search.ContextParams) (*search.ContextResult, error) {
	return nil, m.ctxErr
}

// mockLocalProvider implements LocalProvider for local_search.
type mockLocalProvider struct{}

func (m *mockLocalProvider) Name() string { return "brave" }
func (m *mockLocalProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "paid", Description: "mock brave local"}
}
func (m *mockLocalProvider) Local(_ context.Context, _ search.LocalSearchParams) ([]search.LocalResult, error) {
	return []search.LocalResult{{
		ID:          "local-mock-001",
		Name:        "Mock Coffee Shop",
		Address:     "123 Main St, Seattle, WA 98101",
		Lat:         47.6062,
		Lon:         -122.3321,
		Phone:       "+1-206-555-0100",
		Website:     "https://mockcoffee.example.com",
		Categories:  []string{"coffee shop", "cafe"},
		Rating:      4.5,
		RatingCount: 120,
		Hours:       []string{"Monday: 07:00-19:00"},
		Description: "A cozy mock coffee shop in downtown Seattle.",
		Source:      "brave",
	}}, nil
}

func setupTestDeps() Dependencies {
	synth := &mockSynthProvider{}
	academic := &mockAcademicProvider{}
	filing := &mockFilingProvider{}
	caseProv := &mockCaseProvider{}
	econ := &mockEconProvider{}
	trial := &mockTrialProvider{}
	local := &mockLocalProvider{}
	return Dependencies{
		Cache:               cache.NewNoop(),
		Search:              &mockProvider{},
		AnswerProviders:     map[string]search.AnswerProvider{synth.Name(): synth},
		StructuredProviders: map[string]search.StructuredProvider{synth.Name(): synth},
		AcademicProviders:   map[string]search.AcademicProvider{academic.Name(): academic},
		FilingProviders:     map[string]search.FilingProvider{filing.Name(): filing},
		CaseProviders:       map[string]search.CaseProvider{caseProv.Name(): caseProv},
		EconProviders:       map[string]search.EconProvider{econ.Name(): econ},
		TrialProviders:      map[string]search.TrialProvider{trial.Name(): trial},
		LocalProviders:      map[string]search.LocalProvider{local.Name(): local},
		Scraper:             scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2}),
		Content:             content.NewProcessor(),
		Sessions:            func() session.Manager { m, _ := session.NewManager(session.Config{MaxSessions: 100}); return m }(),
		Metrics:             metrics.NewCollector(),
		Auditor:             audit.NewNoop(),
		Logger:              slog.Default(),
		// Wire the full superset of optional regulated deps so every
		// conditionally-registered tool is visible to the CI drift tests
		// (TestToolsDocMatchesRegistry / TestAllToolsHaveAnnotations /
		// TestOutputSchemaMatchesResponse). Production gates these by feature flag.
		Consent:       consent.NewStoreManager(persist.NewMemoryStore()),
		UserAnalytics: useranalytics.NewStoreRecorder(persist.NewMemoryStore()),
		Memory:        memory.NewStore(persist.NewMemoryStore(), 0),
		Workspaces:    workspace.NewStore(persist.NewMemoryStore(), 0),
	}
}

func createTestServer(deps Dependencies) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	RegisterAll(srv, deps)
	return srv
}

func connectTestClient(ctx context.Context, t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect failed: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}
	return session
}

func TestRegisterAllDoesNotPanic(t *testing.T) {
	deps := setupTestDeps()
	createTestServer(deps)
}

func TestWebSearchTool(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "golang testing"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if output["query"] != "golang testing" {
		t.Fatalf("expected query 'golang testing', got %v", output["query"])
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result, got %v", output["resultCount"])
	}
}

func TestWebSearchEmptyQuery(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": ""},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty query")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if text != "query is required" {
		t.Fatalf("expected 'query is required', got %q", text)
	}
}

// TestWebSearchLensOverridesSite guards the lens+site contract finding: the
// schema says a lens "overrides the site parameter", but the non-CX lens path
// left params.Site set, so a caller passing both got site AND the lens domains
// AND-ed together → an over-constrained empty result. A lens must clear site and
// scope via its own domains.
func TestWebSearchLensOverridesSite(t *testing.T) {
	ctx := context.Background()
	// The lens registry is an empty lazily-created singleton in tests; load the
	// embedded lenses so "security" resolves (same source the binary uses).
	if err := search.GetLensRegistry().LoadEmbedded(); err != nil {
		t.Fatalf("load embedded lenses: %v", err)
	}
	cap := &captureProvider{}
	deps := setupTestDeps()
	deps.Search = cap
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "ransomware", "lens": "security", "site": "example.com"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	if cap.last.Site != "" {
		t.Errorf("lens must clear params.Site, got %q", cap.last.Site)
	}
	if !strings.Contains(cap.last.Query, "site:") {
		t.Errorf("lens should inject its domains into the query, got %q", cap.last.Query)
	}
	if strings.Contains(cap.last.Query, "example.com") {
		t.Errorf("the overridden site filter must not appear, got %q", cap.last.Query)
	}
}

func TestWebSearchLongQuery(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	longQuery := strings.Repeat("x", 501)
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": longQuery},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for query exceeding 500 chars")
	}
}

func TestImageSearchTool(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "image_search",
		Arguments: map[string]any{"query": "cats"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result, got %v", output["resultCount"])
	}
}

func TestNewsSearchTool(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "news_search",
		Arguments: map[string]any{"query": "technology"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result, got %v", output["resultCount"])
	}
}

func TestAcademicSearchTool(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "academic_search",
		Arguments: map[string]any{"query": "machine learning"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["query"] != "machine learning" {
		t.Fatalf("expected query 'machine learning', got %v", output["query"])
	}
}

func TestSequentialSearchTool(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	// Step 1: Create session
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "sequential_search",
		Arguments: map[string]any{
			"searchStep":     "Initial search for topic X",
			"stepNumber":     float64(1),
			"nextStepNeeded": true,
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	sessionID, ok := output["sessionId"].(string)
	if !ok || sessionID == "" {
		t.Fatal("expected sessionId in response")
	}

	// Step 2: Continue session
	res, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "sequential_search",
		Arguments: map[string]any{
			"searchStep":     "Found relevant paper on topic X",
			"stepNumber":     float64(2),
			"nextStepNeeded": false,
			"sessionId":      sessionID,
		},
	})
	if err != nil {
		t.Fatalf("CallTool step 2 failed: %v", err)
	}

	text = res.Content[0].(*mcp.TextContent).Text
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse step 2 output: %v", err)
	}
	if output["isComplete"] != true {
		t.Fatal("expected isComplete=true on final step")
	}
}

// TestSequentialSearchMissingSessionOnStep2 verifies that a step > 1 with no
// sessionId is rejected with guidance rather than silently forking a new,
// orphaned session (which would abandon the real research trail after a caller
// loses its sessionId mid-research).
func TestSequentialSearchMissingSessionOnStep2(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "sequential_search",
		Arguments: map[string]any{
			"searchStep":     "Step two with no session id",
			"stepNumber":     float64(2),
			"nextStepNeeded": true,
			// sessionId deliberately omitted
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true for step 2 without a sessionId")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "missing sessionId") {
		t.Errorf("expected missing-sessionId guidance, got: %s", text)
	}
}

func TestPatentSearchTool(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &mockProviderWithURL{url: "https://patents.google.com/patent/US20200012345A1/en"}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "patent_search",
		Arguments: map[string]any{
			"query":       "neural network acceleration",
			"search_type": "prior_art",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if output["query"] != "neural network acceleration" {
		t.Fatalf("expected query 'neural network acceleration', got %v", output["query"])
	}
	if output["searchType"] != "prior_art" {
		t.Fatalf("expected searchType 'prior_art', got %v", output["searchType"])
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result, got %v", output["resultCount"])
	}
}

func TestPatentSearchEmptyQuery(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "patent_search",
		Arguments: map[string]any{"query": ""},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty query")
	}
}

func TestPatentSearchWithFilters(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &mockProviderWithURL{url: "https://patents.google.com/patent/US20200012345A1/en"}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "patent_search",
		Arguments: map[string]any{
			"query":         "machine learning",
			"assignee":      "Google Inc",
			"patent_office": "US",
			"year_from":     float64(2020),
			"year_to":       float64(2024),
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result, got %v", output["resultCount"])
	}
}

func TestPatentSearchFilterRejectsNonMatching(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &mockProviderWithURL{url: "https://patents.google.com/patent/EP1234567B1/en"}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "patent_search",
		Arguments: map[string]any{
			"query":         "machine learning",
			"patent_office": "US",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["resultCount"].(float64) != 0 {
		t.Fatalf("expected 0 results (EP patent filtered by US office), got %v", output["resultCount"])
	}
}

func TestPatentSearchFilterAllowsAll(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &mockProviderWithURL{url: "https://patents.google.com/patent/EP1234567B1/en"}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "patent_search",
		Arguments: map[string]any{
			"query":         "machine learning",
			"patent_office": "all",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result with 'all' office, got %v", output["resultCount"])
	}
}

func TestPatentSearchFilterNoOffice(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &mockProviderWithURL{url: "https://patents.google.com/patent/WO2021123456A1/en"}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "patent_search",
		Arguments: map[string]any{
			"query": "machine learning",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result with no office filter, got %v", output["resultCount"])
	}
}

func TestPatentSearchFilterMultipleOffices(t *testing.T) {
	offices := []struct {
		office string
		url    string
		expect float64
	}{
		{"US", "https://patents.google.com/patent/US10123456B2/en", 1},
		{"EP", "https://patents.google.com/patent/EP3456789A1/en", 1},
		{"WO", "https://patents.google.com/patent/WO2022000001A1/en", 1},
		{"JP", "https://patents.google.com/patent/JP6789012B2/en", 1},
		{"CN", "https://patents.google.com/patent/CN112345678A/en", 1},
		{"KR", "https://patents.google.com/patent/KR20200012345A/en", 1},
		{"US", "https://patents.google.com/patent/CN112345678A/en", 0},
		{"EP", "https://patents.google.com/patent/US10123456B2/en", 0},
	}

	for _, tt := range offices {
		t.Run(tt.office+"_"+tt.url, func(t *testing.T) {
			ctx := context.Background()
			deps := setupTestDeps()
			deps.Search = &mockProviderWithURL{url: tt.url}
			srv := createTestServer(deps)
			session := connectTestClient(ctx, t, srv)
			defer session.Close()

			res, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name: "patent_search",
				Arguments: map[string]any{
					"query":         "test",
					"patent_office": tt.office,
				},
			})
			if err != nil {
				t.Fatalf("CallTool failed: %v", err)
			}

			text := res.Content[0].(*mcp.TextContent).Text
			var output map[string]any
			if err := json.Unmarshal([]byte(text), &output); err != nil {
				t.Fatalf("failed to parse output: %v", err)
			}
			if output["resultCount"].(float64) != tt.expect {
				t.Fatalf("office=%s url=%s: expected %v results, got %v", tt.office, tt.url, tt.expect, output["resultCount"])
			}
		})
	}
}

func TestScrapePageTool(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html><head><title>Test Page</title><meta property="og:title" content="Test Title"/></head>
<body><article>
<h1>Main Heading</h1>
<p>This is test content from the httptest server. It contains enough text to be extracted properly by the scraping pipeline and should pass the minimum content length threshold of 100 characters for successful extraction.</p>
<p>Additional paragraph with more relevant information for the scraper to pick up during testing.</p>
</article></body></html>`))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := setupTestDeps()
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": ts.URL},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if output["url"] != ts.URL {
		t.Fatalf("expected url %q, got %v", ts.URL, output["url"])
	}
	if output["contentType"] != "html" {
		t.Fatalf("expected contentType 'html', got %v", output["contentType"])
	}
	if output["trust"] != "untrusted-external-content" {
		t.Fatalf("expected trust boundary marker 'untrusted-external-content', got %v", output["trust"])
	}
	contentStr, _ := output["content"].(string)
	if !strings.Contains(contentStr, "Main Heading") {
		t.Fatal("expected content to contain 'Main Heading'")
	}
}

func TestScrapePageEmptyURL(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": ""},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty url")
	}
}

func TestScrapePageHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := setupTestDeps()
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": ts.URL},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestScrapePageRawMode(t *testing.T) {
	const rawBody = `<html><head><title>Raw</title></head><body><script>alert('xss')</script><style>.x{}</style><p>visible</p></body></html>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(rawBody))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := setupTestDeps()
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": ts.URL, "mode": "raw"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	// Raw mode must NOT sanitize: active <script>/<style> stay verbatim.
	contentStr, _ := output["content"].(string)
	if contentStr != rawBody {
		t.Fatalf("raw content should be byte-for-byte; got %q", contentStr)
	}
	if !strings.Contains(contentStr, "<script>") || !strings.Contains(contentStr, "<style>") {
		t.Fatal("raw mode must preserve <script>/<style> tags unsanitized")
	}
	// Real MIME from Content-Type header, not the normalized "html" classifier.
	if ct, _ := output["contentType"].(string); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("expected real MIME contentType, got %v", output["contentType"])
	}
	if raw, _ := output["raw"].(bool); !raw {
		t.Fatal("expected raw flag true")
	}
	// Even (especially) in raw mode, unsanitized bytes must carry the untrusted
	// boundary marker so the host treats them as data, not instructions.
	if output["trust"] != "untrusted-external-content" {
		t.Fatalf("raw mode must carry trust marker, got %v", output["trust"])
	}
}

func TestScrapePageRawVsFullDistinctCache(t *testing.T) {
	const body = `<!DOCTYPE html><html><head><title>Cache Test</title></head><body><article>
<h1>Heading</h1><p>This is the main article body with sufficient length to be extracted by the cleaning pipeline so that full mode returns sanitized readable text rather than the raw markup.</p>
<p>A second paragraph adds more extractable prose for the content processor.</p></article></body></html>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(body))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := setupTestDeps()
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	call := func(mode string) map[string]any {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "scrape_page",
			Arguments: map[string]any{"url": ts.URL, "mode": mode},
		})
		if err != nil {
			t.Fatalf("CallTool(%s) failed: %v", mode, err)
		}
		if res.IsError {
			t.Fatalf("CallTool(%s) error: %v", mode, res.Content)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
			t.Fatalf("parse(%s): %v", mode, err)
		}
		return out
	}

	full := call("full")
	raw := call("raw")
	preview := call("preview")

	// All three modes must carry the untrusted-content boundary marker.
	for mode, out := range map[string]map[string]any{"full": full, "raw": raw, "preview": preview} {
		if out["trust"] != "untrusted-external-content" {
			t.Fatalf("mode %s missing trust marker, got %v", mode, out["trust"])
		}
	}

	// Distinct cache entries: full is sanitized (no <h1> markup), raw is verbatim.
	fullContent, _ := full["content"].(string)
	rawContent, _ := raw["content"].(string)
	if fullContent == rawContent {
		t.Fatal("raw and full must produce distinct content (separate cache keys)")
	}
	if strings.Contains(fullContent, "<h1>") {
		t.Fatal("full mode should be sanitized, not contain raw <h1> markup")
	}
	if !strings.Contains(rawContent, "<h1>") {
		t.Fatal("raw mode should contain verbatim <h1> markup")
	}
}

// TestScrapePageSparsityWarning (#358): a page whose extracted content clears
// the pipeline's >100-byte admission gate but stays under the 150-word
// sparsity floor must surface wordCount + sparsityWarning — the content-volume
// signal is orthogonal to extractionQuality (still "complete" here: no tier
// was exhausted, the page is just genuinely short).
func TestScrapePageSparsityWarning(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html><html><head><title>Thin</title></head><body><article>
<p>Please subscribe to continue reading this article. Access is limited to subscribers only at this time.</p>
</article></body></html>`))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := setupTestDeps()
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": ts.URL},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content[0].(*mcp.TextContent).Text)
	}

	var output map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	wc, ok := output["wordCount"].(float64)
	if !ok || wc >= 150 {
		t.Fatalf("expected wordCount < 150, got %v", output["wordCount"])
	}
	warning, _ := output["sparsityWarning"].(string)
	if warning == "" {
		t.Fatal("expected non-empty sparsityWarning for thin content")
	}
	// The sparsity signal must be orthogonal to extractionQuality: this page's
	// content is genuinely short, not a tier-exhaustion partial extraction, so
	// extractionQuality must stay "complete" even though sparsityWarning fires.
	if quality, _ := output["extractionQuality"].(string); quality != "complete" {
		t.Fatalf("expected extractionQuality \"complete\" for a short-but-complete page, got %v", output["extractionQuality"])
	}
}

func TestSearchAndScrapeTool(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html><head><title>Scraped Page</title></head>
<body><article>
<h1>Search Result Content</h1>
<p>This page was found by search and then scraped. It has enough content to pass the minimum threshold for content extraction and should be included in the combined output of search_and_scrape.</p>
<p>Second paragraph with additional detail about the topic being researched via the combined pipeline.</p>
</article></body></html>`))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &mockProviderWithURL{url: ts.URL}
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_and_scrape",
		Arguments: map[string]any{"query": "test topic"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if output["query"] != "test topic" {
		t.Fatalf("expected query 'test topic', got %v", output["query"])
	}

	combined, _ := output["combinedContent"].(string)
	if combined == "" {
		t.Fatal("expected non-empty combinedContent")
	}
	if output["trust"] != "untrusted-external-content" {
		t.Fatalf("expected trust boundary marker on combinedContent, got %v", output["trust"])
	}
	if !strings.Contains(combined, "Search Result Content") {
		t.Fatal("expected combinedContent to include scraped content")
	}
	// Every source must also carry the per-source trust marker.
	sources, _ := output["sources"].([]any)
	if len(sources) == 0 {
		t.Fatal("expected at least one source")
	}
	for i, s := range sources {
		src, _ := s.(map[string]any)
		if src["trust"] != "untrusted-external-content" {
			t.Fatalf("source %d missing trust marker, got %v", i, src["trust"])
		}
	}
}

// TestSearchAndScrapeSparseSources (#358): every scraped source is a thin
// (< 150 word) paywall/bot-wall stub — each source must carry its own
// wordCount and the summary must count them via sparseSources.
func TestSearchAndScrapeSparseSources(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html><html><head><title>Thin</title></head><body><article>
<p>Please subscribe to continue reading this article. Access is limited to subscribers only at this time.</p>
</article></body></html>`))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &mockProviderWithURL{url: ts.URL}
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_and_scrape",
		Arguments: map[string]any{"query": "test topic"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content[0].(*mcp.TextContent).Text)
	}

	var output map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	sources, _ := output["sources"].([]any)
	if len(sources) == 0 {
		t.Fatal("expected at least one source")
	}
	for i, s := range sources {
		src, _ := s.(map[string]any)
		wc, ok := src["wordCount"].(float64)
		if !ok || wc >= 150 {
			t.Fatalf("source %d expected wordCount < 150, got %v", i, src["wordCount"])
		}
	}
	summary := output["summary"].(map[string]any)
	sparse, ok := summary["sparseSources"].(float64)
	if !ok || sparse == 0 {
		t.Fatalf("expected summary.sparseSources > 0, got %v", summary["sparseSources"])
	}
}

// TestSearchAndScrapeZeroResults exercises the len(searchResults)==0 success
// branch: it must still carry the contract fields (status, trust) and mirror
// the normal success shape (summary + sizeMetadata).
func TestSearchAndScrapeZeroResults(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &emptyWebProvider{}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_and_scrape",
		Arguments: map[string]any{"query": "nothing matches this"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	var output map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if output["status"] != "complete" {
		t.Fatalf("zero-results status should be 'complete', got %v", output["status"])
	}
	if output["trust"] != "untrusted-external-content" {
		t.Fatalf("zero-results must carry trust marker, got %v", output["trust"])
	}
	if output["combinedContent"] != "" {
		t.Fatalf("zero-results combinedContent should be empty, got %v", output["combinedContent"])
	}
	if _, ok := output["summary"].(map[string]any); !ok {
		t.Fatalf("zero-results must include summary block, got %v", output["summary"])
	}
	if _, ok := output["sizeMetadata"].(map[string]any); !ok {
		t.Fatalf("zero-results must include sizeMetadata block, got %v", output["sizeMetadata"])
	}
}

func TestSearchAndScrapeEmptyQuery(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_and_scrape",
		Arguments: map[string]any{"query": ""},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty query")
	}
}

// TestSearchAndScrape_ContextFastPath verifies that when the resolved search
// provider implements ContextSearcher, search_and_scrape uses the server-side
// context assembly path and returns a properly shaped output with
// _contextSource, combinedContent, sources, and the trust marker.
func TestSearchAndScrape_ContextFastPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	provider := &mockContextSearcherProvider{
		ctxResult: &search.ContextResult{
			Context: "Paris is the capital of France.",
			Snippets: []search.ContextSnippet{
				{Title: "France Wiki", URL: "https://en.wikipedia.org/wiki/France", Text: "Paris is the capital of France.", Source: "brave"},
			},
			Source: "brave",
		},
	}

	deps := setupTestDeps()
	deps.Search = provider
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_and_scrape",
		Arguments: map[string]any{"query": "capital of France"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}

	var output map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["status"] != "complete" {
		t.Fatalf("status = %v, want complete", output["status"])
	}
	if output["_contextSource"] != "brave" {
		t.Fatalf("_contextSource = %v, want brave", output["_contextSource"])
	}
	if output["trust"] != "untrusted-external-content" {
		t.Fatalf("trust = %v, want untrusted-external-content", output["trust"])
	}
	combined, _ := output["combinedContent"].(string)
	if combined == "" {
		t.Fatal("expected non-empty combinedContent from LLM context path")
	}
	if !strings.Contains(combined, "Paris") {
		t.Fatalf("combinedContent does not contain expected text: %q", combined)
	}
	sources, _ := output["sources"].([]any)
	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
}

// TestSearchAndScrape_ContextFastPath_FallsBackOnError verifies that when the
// ContextSearcher returns an error, search_and_scrape falls through to the
// normal search + scrape path (i.e. still returns a valid result).
func TestSearchAndScrape_ContextFastPath_FallsBackOnError(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html><html><head><title>Fallback Page</title></head>` +
			`<body><article><h1>Fallback Content</h1>` +
			`<p>This content was fetched via the normal scrape path after the context endpoint failed.</p>` +
			`<p>It confirms the graceful fallback behaviour works correctly.</p>` +
			`</article></body></html>`))
	}))
	defer ts.Close()

	ctx := context.Background()
	providerWithURL := &mockContextSearcherProviderWithURL{
		mockProviderWithURL: mockProviderWithURL{url: ts.URL},
		ctxErr:              fmt.Errorf("403: plan does not include LLM context"),
	}

	deps := setupTestDeps()
	deps.Search = providerWithURL
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_and_scrape",
		Arguments: map[string]any{"query": "fallback test"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error (should have fallen back): %v", res.Content)
	}
	var output map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	// When the context fast-path falls back, _contextSource is NOT present.
	if _, has := output["_contextSource"]; has {
		t.Fatal("_contextSource should be absent on the fallback path")
	}
	combined, _ := output["combinedContent"].(string)
	if !strings.Contains(combined, "Fallback Content") {
		t.Fatalf("expected fallback scraped content, got: %q", combined)
	}
}

func TestWebSearchCaching(t *testing.T) {
	ctx := context.Background()
	metricsCollector := metrics.NewCollector()
	deps := Dependencies{
		Cache:    cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 1}),
		Search:   &mockProvider{},
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2}),
		Content:  content.NewProcessor(),
		Sessions: func() session.Manager { m, _ := session.NewManager(session.Config{MaxSessions: 100}); return m }(),
		Metrics:  metricsCollector,
		Auditor:  audit.NewNoop(),
		Logger:   slog.Default(),
	}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	// First call: should not be from cache
	_, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "cache test"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	stats := metricsCollector.GetToolStats()
	s := stats["web_search"]
	if s.CacheHits != 0 {
		t.Fatalf("expected 0 cache hits after first call, got %d", s.CacheHits)
	}

	// Second call with same query: should hit cache
	_, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "cache test"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	stats = metricsCollector.GetToolStats()
	s = stats["web_search"]
	if s.CacheHits != 1 {
		t.Fatalf("expected 1 cache hit after second call, got %d", s.CacheHits)
	}
}

// TestWebSearchSourceReputation guards #198: web_search results from known-tier
// hosts (e.g. sec.gov) must include a sourceReputation field; unknown hosts
// must omit it. The test uses enrichResultsWithReputation directly since it owns
// the output-shaping logic. The embedded reputation dataset is loaded at package
// init time so no explicit load is needed here.
func TestWebSearchSourceReputation(t *testing.T) {
	t.Parallel()
	results := []search.SearchResult{
		{Title: "SEC filing", URL: "https://www.sec.gov/Archives/edgar/data/x.htm", Snippet: "annual report"},
		{Title: "Random blog", URL: "https://unknown-blog.example/post", Snippet: "just a post"},
	}

	enriched := enrichResultsWithReputation(results, "")
	if len(enriched) != 2 {
		t.Fatalf("want 2 results, got %d", len(enriched))
	}

	// sec.gov is tier:high in the dataset — must surface sourceReputation.
	if enriched[0]["sourceReputation"] == nil {
		t.Errorf("sec.gov result must have sourceReputation field")
	}
	// unknown-blog.example is not in the dataset — must omit the field.
	if enriched[1]["sourceReputation"] != nil {
		t.Errorf("unknown host must omit sourceReputation, got %v", enriched[1]["sourceReputation"])
	}
}

// TestWebSearchSourceReputationWithClaim guards that claim signal + reputation are
// both present in the same result when both apply.
func TestWebSearchSourceReputationWithClaim(t *testing.T) {
	t.Parallel()
	results := []search.SearchResult{
		{
			Title:   "WHO report",
			URL:     "https://www.who.int/news/item/test",
			Snippet: "the pandemic caused significant mortality worldwide",
		},
	}
	enriched := enrichResultsWithReputation(results, "pandemic mortality")
	if len(enriched) != 1 {
		t.Fatalf("want 1 result")
	}
	if enriched[0]["sourceReputation"] == nil {
		t.Errorf("who.int must have sourceReputation")
	}
	if enriched[0]["claimSignal"] == nil {
		t.Errorf("claim signal should be present for matching snippet")
	}
}

// TestWebSearchClaimSignalUniform guards #235 item 2: when a claim is supplied,
// EVERY result carries a claimSignal key (the empty string when no snippet
// sentence is relevant), never a sometimes-absent field — so downstream
// null-checking sees a uniform shape.
func TestWebSearchClaimSignalUniform(t *testing.T) {
	t.Parallel()
	results := []search.SearchResult{
		{Title: "Relevant", URL: "https://example.org/a", Snippet: "the pandemic caused significant mortality worldwide"},
		{Title: "Irrelevant", URL: "https://example.org/b", Snippet: "a recipe for chocolate chip cookies"},
	}
	enriched := enrichResultsWithReputation(results, "pandemic mortality")
	if len(enriched) != 2 {
		t.Fatalf("want 2 results, got %d", len(enriched))
	}
	for i, e := range enriched {
		if _, ok := e["claimSignal"]; !ok {
			t.Errorf("result %d: claimSignal key must be present when a claim is supplied", i)
		}
	}
	// The non-matching result must carry an empty signal, not be missing the key.
	if got := enriched[1]["claimSignal"]; got != "" {
		t.Errorf("non-matching result claimSignal = %q, want empty string", got)
	}
}

// TestWebSearchEnrichPreservesPublishedAt (#356): enrichResultsWithReputation
// rebuilds every web_search result as a fresh map — it must carry PublishedAt
// through rather than silently dropping it, and must omit the key entirely
// when the provider left it empty (never emit an empty publishedAt string).
func TestWebSearchEnrichPreservesPublishedAt(t *testing.T) {
	t.Parallel()
	results := []search.SearchResult{
		{Title: "Dated", URL: "https://example.org/a", Snippet: "has a date", PublishedAt: "2026-05-01T12:00:00Z"},
		{Title: "Undated", URL: "https://example.org/b", Snippet: "no date"},
	}
	enriched := enrichResultsWithReputation(results, "")
	if len(enriched) != 2 {
		t.Fatalf("want 2 results, got %d", len(enriched))
	}
	if enriched[0]["publishedAt"] != "2026-05-01T12:00:00Z" {
		t.Errorf("publishedAt = %v, want 2026-05-01T12:00:00Z", enriched[0]["publishedAt"])
	}
	if _, ok := enriched[1]["publishedAt"]; ok {
		t.Errorf("publishedAt key must be omitted when the provider left it empty, got %v", enriched[1]["publishedAt"])
	}
}

func TestImageSearchEmptyQuery(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "image_search",
		Arguments: map[string]any{"query": ""},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty query")
	}
}

func TestNewsSearchEmptyQuery(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "news_search",
		Arguments: map[string]any{"query": ""},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty query")
	}
}

func TestAcademicSearchEmptyQuery(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "academic_search",
		Arguments: map[string]any{"query": ""},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty query")
	}
}

// TestSearchToolsRejectWhitespaceQuery is the regression guard for the
// whitespace-trim consistency gap found in the v1.27.1 live-test pass: a
// whitespace-only query (e.g. "   ") must be trimmed and rejected as "query is
// required" — identical to an empty string — across every search tool, so it can
// never reach a provider and bill a junk search. (answer/structured_search
// already trimmed; web/news/image/academic/search_and_scrape/legal did not.)
func TestSearchToolsRejectWhitespaceQuery(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	for _, tool := range []string{"web_search", "news_search", "image_search", "academic_search", "search_and_scrape", "legal_search"} {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      tool,
			Arguments: map[string]any{"query": "   "},
		})
		if err != nil {
			t.Fatalf("%s CallTool failed: %v", tool, err)
		}
		if !res.IsError {
			t.Errorf("%s: whitespace-only query should be rejected, got success", tool)
		}
	}
}

// TestMemorySaveRejectsWhitespaceNote: a whitespace-only note must be rejected
// the same as an empty one, so a blank memory can't be persisted to the
// per-user store and then pollute recall.
func TestMemorySaveRejectsWhitespaceNote(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "memory_save",
		Arguments: map[string]any{"note": "   "},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Error("whitespace-only note should be rejected")
	}
}

func TestSequentialSearchEmptyStep(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "sequential_search",
		Arguments: map[string]any{
			"searchStep":     "",
			"stepNumber":     float64(1),
			"nextStepNeeded": true,
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty searchStep")
	}
}

func TestToolError(t *testing.T) {
	result := toolError("something went wrong")
	if !result.IsError {
		t.Fatal("expected IsError=true")
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(result.Content))
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatal("expected *TextContent")
	}
	if tc.Text != "something went wrong" {
		t.Fatalf("expected error text, got %q", tc.Text)
	}
}

// capturingAuditor records every logged event and reports a configurable
// IncludeRequestBody value, for asserting the query-gating + request-id wiring.
type capturingAuditor struct {
	mu             sync.Mutex
	events         []audit.AuditEvent
	includeReqBody bool
}

func (c *capturingAuditor) Log(ev audit.AuditEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
}
func (c *capturingAuditor) IncludeRequestBody() bool { return c.includeReqBody }
func (c *capturingAuditor) Close()                   {}
func (c *capturingAuditor) last() audit.AuditEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.events[len(c.events)-1]
}

func TestAuditQueryGatingOmitsRawQuery(t *testing.T) {
	t.Parallel()
	cap := &capturingAuditor{includeReqBody: false}
	deps := setupTestDeps()
	deps.Auditor = cap

	auditToolCallQuery(context.Background(), deps, "web_search", time.Millisecond, nil, "", "secret research topic", nil)

	ev := cap.last()
	if _, ok := ev.Metadata["query"]; ok {
		t.Error("raw query must NOT be present when IncludeRequestBody=false")
	}
	ql, ok := ev.Metadata["query_length"]
	if !ok {
		t.Fatal("expected query_length in metadata when IncludeRequestBody=false")
	}
	if ql.(int) != len("secret research topic") {
		t.Errorf("query_length = %v, want %d", ql, len("secret research topic"))
	}
}

func TestAuditQueryGatingIncludesRawQuery(t *testing.T) {
	t.Parallel()
	cap := &capturingAuditor{includeReqBody: true}
	deps := setupTestDeps()
	deps.Auditor = cap

	auditToolCallQuery(context.Background(), deps, "web_search", time.Millisecond, nil, "", "open research topic", nil)

	ev := cap.last()
	q, ok := ev.Metadata["query"]
	if !ok {
		t.Fatal("expected raw query in metadata when IncludeRequestBody=true")
	}
	if q.(string) != "open research topic" {
		t.Errorf("query = %v, want 'open research topic'", q)
	}
	if _, ok := ev.Metadata["query_length"]; ok {
		t.Error("query_length must not be set when raw query is included")
	}
}

func TestAuditMasksQueryAndError(t *testing.T) {
	t.Parallel()
	cap := &capturingAuditor{includeReqBody: true}
	deps := setupTestDeps()
	deps.Auditor = cap

	// Synthetic key-shaped values assembled at runtime so no contiguous
	// credential literal lands in source (keeps secret scanners quiet); the
	// google-key and token= query-param rules still fire on the joined string.
	googleKey := "AIza" + "0123456789abcdefghijklmnopqrstuv012"
	tokenVal := "val-" + "0123456789abcdef"
	secretQuery := "lookup key=" + googleKey
	err := errorString("provider failed: token=" + tokenVal)
	auditToolCallQuery(context.Background(), deps, "web_search", time.Millisecond, err, "upstream_error", secretQuery, nil)

	ev := cap.last()
	if q := ev.Metadata["query"].(string); strings.Contains(q, googleKey) {
		t.Errorf("query metadata leaked a secret: %q", q)
	}
	if e := ev.Metadata["error"].(string); strings.Contains(e, tokenVal) {
		t.Errorf("error metadata leaked a secret: %q", e)
	}
}

func TestAuditSetsRequestIDFromContext(t *testing.T) {
	t.Parallel()
	cap := &capturingAuditor{}
	deps := setupTestDeps()
	deps.Auditor = cap

	ctx := context.WithValue(context.Background(), auth.ContextKeyRequestID, "req-correlate-123")
	auditToolCall(ctx, deps, "scrape_page", time.Millisecond, nil, "")

	if got := cap.last().RequestID; got != "req-correlate-123" {
		t.Errorf("RequestID = %q, want correlated value from context", got)
	}
}

func TestAuditToolCallNoQueryHasNoQueryMeta(t *testing.T) {
	t.Parallel()
	cap := &capturingAuditor{includeReqBody: false}
	deps := setupTestDeps()
	deps.Auditor = cap

	auditToolCall(context.Background(), deps, "image_search", time.Millisecond, nil, "")

	ev := cap.last()
	if _, ok := ev.Metadata["query"]; ok {
		t.Error("no query metadata expected for query-less call")
	}
	if _, ok := ev.Metadata["query_length"]; ok {
		t.Error("no query_length metadata expected for query-less call")
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

func TestToolsWorkWithRouter(t *testing.T) {
	// Verify that tools work correctly when the Provider is a Router
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"organic_results": []map[string]any{
				{"position": 1, "title": "Router Result", "link": "https://router.example.com", "snippet": "Via router", "displayed_link": "router.example.com"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	cfg := config.SearchConfig{
		SearchAPIKey: "test-key",
	}
	searchDeps := search.Deps{
		HTTPClient: ts.Client(),
		Breaker:    nil, // AvailableProviders creates its own breakers via the router
	}

	// Manually create a searchapi provider pointed at our test server
	provider := search.NewSearchAPIProvider(cfg.SearchAPIKey, search.Deps{
		HTTPClient: ts.Client(),
		Breaker:    newTestBreaker(),
	})
	provider.SetBaseURL(ts.URL)
	_ = searchDeps // used only for the pattern illustration

	providers := map[string]search.Provider{
		"searchapi": provider,
	}
	router := search.NewRouter(providers, search.RouterConfig{
		Routing: search.RoutingConfig{Default: []string{"searchapi"}},
	})

	deps := Dependencies{
		Cache:    cache.NewNoop(),
		Search:   router,
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2}),
		Content:  content.NewProcessor(),
		Sessions: func() session.Manager { m, _ := session.NewManager(session.Config{MaxSessions: 100}); return m }(),
		Metrics:  metrics.NewCollector(),
		Auditor:  audit.NewNoop(),
		Logger:   slog.Default(),
	}

	ctx := context.Background()
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "test via router"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content[0].(*mcp.TextContent).Text)
	}

	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["resultCount"].(float64) != 1 {
		t.Fatalf("expected 1 result, got %v", output["resultCount"])
	}
}

// TestRegulatedToolDenialsAreAuditedAndMetered verifies metrics+audit PARITY on
// the regulated tools' refusal paths: an unauthenticated (anonymous/STDIO)
// caller is denied, and each denial emits exactly one audit event (Success=false,
// errCode=unauthenticated, no tool input) AND increments the per-tool metric.
func TestRegulatedToolDenialsAreAuditedAndMetered(t *testing.T) {
	for _, tool := range []string{"memory_save", "memory_recall", "workspace_contribute", "workspace_read", "get_my_analytics"} {
		t.Run(tool, func(t *testing.T) {
			cap := &capturingAuditor{}
			mc := metrics.NewCollector()
			deps := setupTestDeps()
			deps.Auditor = cap
			deps.Metrics = mc

			ctx := context.Background()
			srv := createTestServer(deps)
			sess := connectTestClient(ctx, t, srv)
			defer sess.Close()

			args := map[string]any{}
			switch tool {
			case "memory_save":
				args["note"] = "x"
			case "workspace_contribute":
				args["workspace_id"] = "w1"
				args["note"] = "x"
			case "workspace_read":
				args["workspace_id"] = "w1"
			}
			if _, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args}); err != nil {
				t.Fatalf("CallTool(%s) failed: %v", tool, err)
			}

			// Exactly one audit event for this denied call, Success=false, no PII.
			evs := cap.events
			if len(evs) != 1 {
				t.Fatalf("%s: expected 1 audit event on denial, got %d", tool, len(evs))
			}
			ev := evs[0]
			if ev.Success {
				t.Errorf("%s: denial event must have Success=false", tool)
			}
			if ev.ErrorCode != "unauthenticated" {
				t.Errorf("%s: errCode = %q, want unauthenticated", tool, ev.ErrorCode)
			}
			if ev.ToolName != tool {
				t.Errorf("%s: tool name = %q", tool, ev.ToolName)
			}
			// Metric parity: the denied call is counted for this tool.
			if got := mc.GetToolStats()[tool].TotalCalls; got != 1 {
				t.Errorf("%s: metric TotalCalls = %d, want 1", tool, got)
			}
		})
	}
}

// numCapturingProvider records the NumResults it was asked for, to assert the
// tool-boundary clamp.
type numCapturingProvider struct {
	mu   sync.Mutex
	seen int
}

func (p *numCapturingProvider) Web(_ context.Context, params search.WebSearchParams) ([]search.SearchResult, error) {
	p.mu.Lock()
	p.seen = params.NumResults
	p.mu.Unlock()
	return []search.SearchResult{{Title: "t", URL: "https://e.com", Snippet: "s"}}, nil
}
func (p *numCapturingProvider) Images(_ context.Context, _ search.ImageSearchParams) ([]search.ImageResult, error) {
	return nil, nil
}
func (p *numCapturingProvider) News(_ context.Context, _ search.NewsSearchParams) ([]search.NewsResult, error) {
	return nil, nil
}
func (p *numCapturingProvider) Name() string { return "numcap" }

// TestNumResultsClampedAtBoundary verifies web_search clamps an over-limit
// num_results to the documented ceiling before it reaches the provider (ASI06
// fan-out / billing bound, defense-in-depth).
func TestNumResultsClampedAtBoundary(t *testing.T) {
	cap := &numCapturingProvider{}
	deps := setupTestDeps()
	deps.Search = cap
	ctx := context.Background()
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	if _, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "x", "num_results": 50},
	}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	cap.mu.Lock()
	got := cap.seen
	cap.mu.Unlock()
	if got != maxNumResults {
		t.Errorf("num_results=50 should clamp to %d at the boundary, provider saw %d", maxNumResults, got)
	}
}

func TestGetMyAnalyticsResponse(t *testing.T) {
	t.Parallel()

	deps := setupTestDeps()
	ctx := context.Background()
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	// Unauthenticated user (anonymous by default in test)
	result, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_my_analytics",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if len(result.Content) == 0 {
		t.Fatal("no content in result")
	}

	textContent, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content type: %T", result.Content[0])
	}

	// Parse JSON response
	var response map[string]interface{}
	if err := json.Unmarshal([]byte(textContent.Text), &response); err != nil {
		t.Fatalf("failed to parse response JSON: %v\nResponse: %s", err, textContent.Text)
	}

	// Verify response structure
	status, ok := response["status"].(string)
	if !ok {
		t.Fatal("missing or invalid 'status' field in response")
	}

	// Unauthenticated users should get "unavailable"
	if status != "unavailable" {
		t.Errorf("expected status=unavailable, got %s; full response: %v", status, response)
	}

	// Should have a reason field
	if reason, ok := response["reason"].(string); !ok || reason == "" {
		t.Error("missing reason field in response")
	}

	// Verify the response text contains no errors
	if result.IsError {
		t.Error("result should not be marked as error")
	}
}
