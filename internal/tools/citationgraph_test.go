package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
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
func (f *failingS2Provider) SupportsInfluenceSignal() bool { return true }

// circuitOpenS2Provider implements AcademicProvider + CitationSearcher and
// models Semantic Scholar's circuit breaker already OPEN (#664) — every
// Citations/References call returns the bare circuit.ErrCircuitOpen sentinel,
// exactly what search.Deps.Breaker.Execute returns once a breaker trips,
// without any "semanticscholar:" wrapping. Named "semanticscholar" so the
// auto-select path picks it first.
type circuitOpenS2Provider struct{}

func (f *circuitOpenS2Provider) Name() string { return "semanticscholar" }
func (f *circuitOpenS2Provider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "mock S2 (circuit open)"}
}
func (f *circuitOpenS2Provider) Scholarly(_ context.Context, _ search.AcademicSearchParams) ([]search.AcademicResult, error) {
	return nil, circuit.ErrCircuitOpen
}
func (f *circuitOpenS2Provider) Citations(_ context.Context, _ string, _ int) ([]search.AcademicResult, error) {
	return nil, circuit.ErrCircuitOpen
}
func (f *circuitOpenS2Provider) References(_ context.Context, _ string, _ int) ([]search.AcademicResult, error) {
	return nil, circuit.ErrCircuitOpen
}
func (f *circuitOpenS2Provider) SupportsInfluenceSignal() bool { return true }

// rateLimitedS2Provider is circuitOpenS2Provider's companion (#664): models a
// Semantic Scholar call that returns the wrapped 429 sentinel directly (the
// breaker hasn't tripped open yet, but this call is itself rate-limited),
// exercising the same widened fallback condition via isRateLimitError rather
// than errors.Is(err, circuit.ErrCircuitOpen).
type rateLimitedS2Provider struct{}

func (f *rateLimitedS2Provider) Name() string { return "semanticscholar" }
func (f *rateLimitedS2Provider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "mock S2 (rate limited)"}
}
func (f *rateLimitedS2Provider) Scholarly(_ context.Context, _ search.AcademicSearchParams) ([]search.AcademicResult, error) {
	return nil, fmt.Errorf("semanticscholar: rate limited: %w", circuit.ErrRateLimit)
}
func (f *rateLimitedS2Provider) Citations(_ context.Context, _ string, _ int) ([]search.AcademicResult, error) {
	return nil, fmt.Errorf("semanticscholar: rate limited: %w", circuit.ErrRateLimit)
}
func (f *rateLimitedS2Provider) References(_ context.Context, _ string, _ int) ([]search.AcademicResult, error) {
	return nil, fmt.Errorf("semanticscholar: rate limited: %w", circuit.ErrRateLimit)
}
func (f *rateLimitedS2Provider) SupportsInfluenceSignal() bool { return true }

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

// TestCitationGraphInfluentialOnly is the #655 regression guard: influential_only
// must filter when the active provider supplies the influence signal (Semantic
// Scholar) but pass results through UNFILTERED when it doesn't (OpenAlex) —
// per the documented contract, not silently discard everything.
func TestCitationGraphInfluentialOnly(t *testing.T) {
	t.Run("semanticscholar filters to influential edges", func(t *testing.T) {
		// influentialAndFlatS2Provider (below) has citedBy IsInfluential=true
		// (kept) and references IsInfluential=false (dropped) — same fixture
		// shape the old (pre-#655) test used, now against a provider that
		// actually claims to support the signal.
		deps := setupTestDeps()
		deps.AcademicProviders = map[string]search.AcademicProvider{
			"semanticscholar": &influentialAndFlatS2Provider{},
		}
		out, res := callTool(t, deps, "citation_graph", map[string]any{"paper": "10.1/x", "influential_only": true})
		if res.IsError {
			t.Fatalf("unexpected error")
		}
		if out["provider"] != "semanticscholar" {
			t.Fatalf("provider=%v, want semanticscholar", out["provider"])
		}
		if cb, _ := out["citedByCount"].(float64); cb != 1 {
			t.Errorf("influential citedBy kept: got %v", out["citedByCount"])
		}
		if rc, _ := out["referencesCount"].(float64); rc != 0 {
			t.Errorf("non-influential references should be filtered out, got %v", out["referencesCount"])
		}
	})

	t.Run("openalex passes through unfiltered (no influence signal)", func(t *testing.T) {
		// mockAcademicProvider is named "openalex" and reports
		// SupportsInfluenceSignal()==false, matching real OpenAlex (counts-only
		// edges). influential_only must be a no-op: both citedBy and references
		// come back at full count, not zeroed out.
		out, res := callTool(t, setupTestDeps(), "citation_graph", map[string]any{"paper": "10.1/x", "influential_only": true})
		if res.IsError {
			t.Fatalf("unexpected error")
		}
		if out["provider"] != "openalex" {
			t.Fatalf("provider=%v, want openalex", out["provider"])
		}
		if cb, _ := out["citedByCount"].(float64); cb != 1 {
			t.Errorf("pass-through citedBy: got %v, want 1 (unfiltered)", out["citedByCount"])
		}
		if rc, _ := out["referencesCount"].(float64); rc != 1 {
			t.Errorf("pass-through references: got %v, want 1 (unfiltered — no influence signal, no-op)", out["referencesCount"])
		}
	})
}

// influentialAndFlatS2Provider implements AcademicProvider + CitationSearcher,
// named "semanticscholar", SupportsInfluenceSignal()==true. Citations returns
// one influential edge, References returns one non-influential edge — used to
// exercise the influential_only FILTERING branch (#655).
type influentialAndFlatS2Provider struct{}

func (f *influentialAndFlatS2Provider) Name() string { return "semanticscholar" }
func (f *influentialAndFlatS2Provider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "mock S2 (influential filtering)"}
}
func (f *influentialAndFlatS2Provider) Scholarly(_ context.Context, _ search.AcademicSearchParams) ([]search.AcademicResult, error) {
	return nil, nil
}
func (f *influentialAndFlatS2Provider) Citations(_ context.Context, _ string, _ int) ([]search.AcademicResult, error) {
	return []search.AcademicResult{{Title: "Cites It", URL: "https://doi.org/10.2/y", DOI: "10.2/y", Year: 2025, Source: "semanticscholar", IsInfluential: true}}, nil
}
func (f *influentialAndFlatS2Provider) References(_ context.Context, _ string, _ int) ([]search.AcademicResult, error) {
	return []search.AcademicResult{{Title: "Foundational", URL: "https://doi.org/10.0/z", DOI: "10.0/z", Year: 2017, Source: "semanticscholar"}}, nil
}
func (f *influentialAndFlatS2Provider) SupportsInfluenceSignal() bool { return true }

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

// TestCitationGraphAutoFallbackOnCircuitOpen is the #664 regression guard: when
// no provider is pinned and Semantic Scholar's circuit breaker is already OPEN
// (returning the bare circuit.ErrCircuitOpen sentinel, not a "paper not found"
// 404), the traversal must still transparently fall back to a healthy OpenAlex
// and succeed — not surface the breaker-open error to the caller.
func TestCitationGraphAutoFallbackOnCircuitOpen(t *testing.T) {
	deps := setupTestDeps()
	deps.AcademicProviders = map[string]search.AcademicProvider{
		"semanticscholar": &circuitOpenS2Provider{},
		"openalex":        &mockAcademicProvider{},
	}
	out, res := callTool(t, deps, "citation_graph", map[string]any{"paper": "10.1038/nature14539"})
	if res.IsError {
		t.Fatalf("auto-select should fall back to OpenAlex on circuit-open, got error result")
	}
	if out["provider"] != "openalex" {
		t.Errorf("provider=%v, want openalex (fallback must have fired on ErrCircuitOpen)", out["provider"])
	}
	if cb, _ := out["citedByCount"].(float64); cb != 1 {
		t.Errorf("citedByCount=%v want 1 (OpenAlex mock result)", out["citedByCount"])
	}
}

// TestCitationGraphAutoFallbackOnRateLimit is TestCitationGraphAutoFallbackOnCircuitOpen's
// companion (#664): the same widened fallback condition must also cover a
// wrapped circuit.ErrRateLimit (a 429 that hasn't tripped the breaker open
// yet), via isRateLimitError rather than a literal ErrCircuitOpen match.
func TestCitationGraphAutoFallbackOnRateLimit(t *testing.T) {
	deps := setupTestDeps()
	deps.AcademicProviders = map[string]search.AcademicProvider{
		"semanticscholar": &rateLimitedS2Provider{},
		"openalex":        &mockAcademicProvider{},
	}
	out, res := callTool(t, deps, "citation_graph", map[string]any{"paper": "10.1038/nature14539"})
	if res.IsError {
		t.Fatalf("auto-select should fall back to OpenAlex on rate-limit, got error result")
	}
	if out["provider"] != "openalex" {
		t.Errorf("provider=%v, want openalex (fallback must have fired on rate limit)", out["provider"])
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

// failingOpenAlexProvider implements AcademicProvider + CitationSearcher and
// always 404s, modeling OpenAlex's own DOI-entity lookup also failing after the
// #228 fallback fires. Named "openalex" so fallbackCitationSearcher picks it.
type failingOpenAlexProvider struct{}

func (f *failingOpenAlexProvider) Name() string { return "openalex" }
func (f *failingOpenAlexProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "mock OpenAlex (always 404)"}
}
func (f *failingOpenAlexProvider) Scholarly(_ context.Context, _ search.AcademicSearchParams) ([]search.AcademicResult, error) {
	return nil, fmt.Errorf("openalex: not found")
}
func (f *failingOpenAlexProvider) Citations(_ context.Context, _ string, _ int) ([]search.AcademicResult, error) {
	return nil, fmt.Errorf("openalex: not found")
}
func (f *failingOpenAlexProvider) References(_ context.Context, _ string, _ int) ([]search.AcademicResult, error) {
	return nil, fmt.Errorf("openalex: not found")
}
func (f *failingOpenAlexProvider) SupportsInfluenceSignal() bool { return false }

// TestCitationGraphBothProvidersFailErrorNamesBoth (#434, Clean-code Rule 2):
// when the auto-select fallback ALSO fails, the surfaced error must name both
// providers and both underlying errors — never silently drop the fallback's own
// diagnostically distinct error in favor of the stale primary error.
func TestCitationGraphBothProvidersFailErrorNamesBoth(t *testing.T) {
	deps := setupTestDeps()
	deps.AcademicProviders = map[string]search.AcademicProvider{
		"semanticscholar": &failingS2Provider{},
		"openalex":        &failingOpenAlexProvider{},
	}
	_, res := callTool(t, deps, "citation_graph", map[string]any{"paper": "10.1038/nature14539"})
	if !res.IsError {
		t.Fatal("expected an error result when both providers fail")
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(text, "semanticscholar") || !strings.Contains(text, "openalex") {
		t.Errorf("error text must name both providers, got: %q", text)
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
