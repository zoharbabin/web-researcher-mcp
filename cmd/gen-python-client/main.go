// gen-python-client dumps the full tools/list schema (inputSchema + outputSchema
// for every registered tool) to stdout as a JSON array.
//
// The Python generator (scripts/gen_python_client.py) reads this output and
// regenerates python/web_researcher_mcp/client.py and models.py from it.
//
// Run via:
//
//	go run ./cmd/gen-python-client > /tmp/schema.json
//
// or via:
//
//	make gen-python-client
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/consent"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/memory"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/persist"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
	"github.com/zoharbabin/web-researcher-mcp/internal/tools"
	"github.com/zoharbabin/web-researcher-mcp/internal/useranalytics"
	"github.com/zoharbabin/web-researcher-mcp/internal/workspace"
)

func main() {
	ctx := context.Background()

	srv := mcp.NewServer(&mcp.Implementation{Name: "gen-schema-server", Version: "1.0.0"}, nil)
	tools.RegisterAll(srv, buildDeps())

	// Wire server and client via in-process transports — no ports, no processes.
	ct, st := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		fmt.Fprintf(os.Stderr, "server connect: %v\n", err)
		os.Exit(1)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "gen-schema-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "client connect: %v\n", err)
		os.Exit(1)
	}

	// Collect all tools using the paginated iterator.
	var allTools []*mcp.Tool
	for t, err := range session.Tools(ctx, nil) {
		if err != nil {
			fmt.Fprintf(os.Stderr, "list tools: %v\n", err)
			os.Exit(1)
		}
		allTools = append(allTools, t)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(allTools); err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
}

// buildDeps wires the full superset of optional providers so every
// conditionally-registered tool is included in the schema dump.
// This mirrors setupTestDeps() in internal/tools/tools_test.go.
func buildDeps() tools.Dependencies {
	synth := &mockSynthProvider{}
	academic := &mockAcademicProvider{}
	filing := &mockFilingProvider{}
	caseProv := &mockCaseProvider{}
	econ := &mockEconProvider{}
	trial := &mockTrialProvider{}

	mgr, _ := session.NewManager(session.Config{MaxSessions: 100})

	return tools.Dependencies{
		Cache:               cache.NewNoop(),
		Search:              &mockProvider{},
		SearchProviders:     map[string]search.Provider{"mock": &mockProvider{}},
		PatentProviders:     map[string]search.PatentProvider{},
		AcademicProviders:   map[string]search.AcademicProvider{academic.Name(): academic},
		FilingProviders:     map[string]search.FilingProvider{filing.Name(): filing},
		CaseProviders:       map[string]search.CaseProvider{caseProv.Name(): caseProv},
		EconProviders:       map[string]search.EconProvider{econ.Name(): econ},
		TrialProviders:      map[string]search.TrialProvider{trial.Name(): trial},
		AnswerProviders:     map[string]search.AnswerProvider{synth.Name(): synth},
		StructuredProviders: map[string]search.StructuredProvider{synth.Name(): synth},
		Scraper:             scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2}),
		Content:             content.NewProcessor(),
		Sessions:            mgr,
		Metrics:             metrics.NewCollector(),
		Auditor:             audit.NewNoop(),
		Logger:              slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Consent:             consent.NewStoreManager(persist.NewMemoryStore()),
		UserAnalytics:       useranalytics.NewStoreRecorder(persist.NewMemoryStore()),
		Memory:              memory.NewStore(persist.NewMemoryStore(), 0),
		Workspaces:          workspace.NewStore(persist.NewMemoryStore(), 0),
	}
}

// ---------------------------------------------------------------------------
// Minimal mock providers — only Name() + required interface methods.
// ---------------------------------------------------------------------------

type mockProvider struct{}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) Web(_ context.Context, _ search.WebSearchParams) ([]search.SearchResult, error) {
	return []search.SearchResult{{Title: "t", URL: "https://example.com", Snippet: "s"}}, nil
}
func (m *mockProvider) Images(_ context.Context, _ search.ImageSearchParams) ([]search.ImageResult, error) {
	return nil, nil
}
func (m *mockProvider) News(_ context.Context, _ search.NewsSearchParams) ([]search.NewsResult, error) {
	return nil, nil
}

type mockSynthProvider struct{}

func (m *mockSynthProvider) Name() string { return "mocksynth" }
func (m *mockSynthProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free"}
}
func (m *mockSynthProvider) Answer(_ context.Context, _ search.AnswerParams) (*search.AnswerResult, error) {
	return &search.AnswerResult{Answer: "a", Provider: "mocksynth"}, nil
}
func (m *mockSynthProvider) StructuredSearch(_ context.Context, _ search.StructuredParams) (*search.StructuredResult, error) {
	return &search.StructuredResult{Provider: "mocksynth"}, nil
}

type mockAcademicProvider struct{}

func (m *mockAcademicProvider) Name() string { return "openalex" }
func (m *mockAcademicProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free"}
}
func (m *mockAcademicProvider) Scholarly(_ context.Context, _ search.AcademicSearchParams) ([]search.AcademicResult, error) {
	return []search.AcademicResult{{Title: "t", DOI: "10.1/x", Year: 2024, Source: "openalex"}}, nil
}
func (m *mockAcademicProvider) Citations(_ context.Context, _ string, _ int) ([]search.AcademicResult, error) {
	return nil, nil
}
func (m *mockAcademicProvider) References(_ context.Context, _ string, _ int) ([]search.AcademicResult, error) {
	return nil, nil
}
func (m *mockAcademicProvider) ResolveByDOI(_ context.Context, doi string) (*search.AcademicResult, error) {
	if doi == "10.1234/x" {
		return &search.AcademicResult{Title: "t", DOI: doi, Year: 2024, Source: "openalex"}, nil
	}
	return nil, nil
}

type mockFilingProvider struct{}

func (m *mockFilingProvider) Name() string { return "edgar" }
func (m *mockFilingProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"US"}, RateClass: "free"}
}
func (m *mockFilingProvider) Filings(_ context.Context, _ search.FilingSearchParams) ([]search.FilingResult, error) {
	return []search.FilingResult{{Company: "Mock Corp", FormType: "10-K", Source: "edgar"}}, nil
}

type mockCaseProvider struct{}

func (m *mockCaseProvider) Name() string { return "courtlistener" }
func (m *mockCaseProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"US"}, RateClass: "free"}
}
func (m *mockCaseProvider) Cases(_ context.Context, _ search.CaseSearchParams) ([]search.CaseResult, error) {
	return []search.CaseResult{{CaseName: "Mock v. Test", Court: "Supreme Court", Source: "courtlistener"}}, nil
}

type mockEconProvider struct{}

func (m *mockEconProvider) Name() string { return "fred" }
func (m *mockEconProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"US"}, RateClass: "free"}
}
func (m *mockEconProvider) Econ(_ context.Context, _ search.EconSearchParams) ([]search.EconResult, error) {
	return []search.EconResult{{SeriesID: "GDP", Title: "GDP", Source: "fred"}}, nil
}

type mockTrialProvider struct{}

func (m *mockTrialProvider) Name() string { return "clinicaltrials" }
func (m *mockTrialProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free"}
}
func (m *mockTrialProvider) Trials(_ context.Context, _ search.TrialSearchParams) ([]search.TrialResult, error) {
	return []search.TrialResult{{NCTID: "NCT00000000", Title: "Mock Trial", Source: "clinicaltrials"}}, nil
}
