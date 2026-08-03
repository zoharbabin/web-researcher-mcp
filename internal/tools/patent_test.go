package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// =============================================================================
// #502: specific-lookup short-circuit
// =============================================================================

// TestLooksLikePatentNumber proves the query-shape classifier that gates the
// #502 short-circuit: it must accept bare patent numbers (any case) and
// reject free-text queries, including a query that merely mentions a number.
func TestLooksLikePatentNumber(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  bool
	}{
		{"bare US number", "US10000000", true},
		{"bare US number with kind code", "US10000000B2", true},
		{"bare EP number with kind code", "EP1234567A1", true},
		{"lowercase office prefix", "us10000000", true},
		{"leading/trailing whitespace", "  US10000000  ", true},
		{"number followed by words", "US10000000 improvements", false},
		{"free-text phrase", "machine learning video encoding", false},
		{"empty query", "", false},
		{"only digits, no office prefix", "10000000", false},
		{"office prefix, no digits", "USABCDEFG", false},
		{"single-letter prefix", "U10000000", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikePatentNumber(tc.query); got != tc.want {
				t.Errorf("looksLikePatentNumber(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

// TestIsPatentSpecificLookup proves all three gates that must hold together
// for the #502 short-circuit to engage: no explicit provider pinned (that's
// Strategy 1's job), search_type=specific, and a number-shaped query.
func TestIsPatentSpecificLookup(t *testing.T) {
	cases := []struct {
		name       string
		provider   string
		searchType string
		query      string
		want       bool
	}{
		{"all three gates satisfied", "", "specific", "US10000000", true},
		{"wrong search_type (prior_art)", "", "prior_art", "US10000000", false},
		{"wrong search_type (landscape)", "", "landscape", "US10000000", false},
		{"explicit provider pinned", "epo", "specific", "US10000000", false},
		{"free-text query", "", "specific", "lithium battery thermal management", false},
		{"empty query", "", "specific", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPatentSpecificLookup(tc.provider, tc.searchType, tc.query); got != tc.want {
				t.Errorf("isPatentSpecificLookup(%q, %q, %q) = %v, want %v", tc.provider, tc.searchType, tc.query, got, tc.want)
			}
		})
	}
}

// fakePatentDetailScraper is a mock patentDetailScraper (the narrow interface
// shared by the #502 short-circuit and enrichPatents) that returns a
// pre-programmed result or error without any network access.
type fakePatentDetailScraper struct {
	result *scraper.PatentResult
	err    error
}

func (f *fakePatentDetailScraper) ScrapePatentDetail(_ context.Context, _ string) (*scraper.PatentResult, error) {
	return f.result, f.err
}

// TestLookupPatentByNumber proves lookupPatentByNumber (the #502
// short-circuit's fetch) treats every "not found" shape — a fetch error, a
// nil detail, or a detail with an empty title (Google Patents' own "not
// found" page shape) — as a miss (nil), and only a detail with a non-empty
// title as a hit.
func TestLookupPatentByNumber(t *testing.T) {
	cases := []struct {
		name    string
		scraper patentDetailScraper
		wantNil bool
	}{
		{
			name:    "successful fetch with title",
			scraper: &fakePatentDetailScraper{result: &scraper.PatentResult{Number: "US10000000", Title: "A Widget"}},
			wantNil: false,
		},
		{
			name:    "fetch error",
			scraper: &fakePatentDetailScraper{err: fmt.Errorf("network error")},
			wantNil: true,
		},
		{
			name:    "nil detail, no error",
			scraper: &fakePatentDetailScraper{result: nil, err: nil},
			wantNil: true,
		},
		{
			name:    "detail with empty title (not-found page shape)",
			scraper: &fakePatentDetailScraper{result: &scraper.PatentResult{Number: "US99999999"}},
			wantNil: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := lookupPatentByNumber(context.Background(), "US10000000", tc.scraper)
			if tc.wantNil && got != nil {
				t.Errorf("lookupPatentByNumber() = %+v, want nil", got)
			}
			if !tc.wantNil && got == nil {
				t.Error("lookupPatentByNumber() = nil, want non-nil")
			}
		})
	}
}

// =============================================================================
// #503: Strategy 3 deterministic-order, skip-not-abort provider routing
// =============================================================================

// mockPatentProvider is a minimal search.PatentProvider (PatentSearcher +
// Name + Metadata) that either succeeds with one canned result or fails with
// a caller-supplied error (e.g. circuit.ErrRateLimit), letting tests wire up
// a mixed healthy/failing provider ladder without any network access.
type mockPatentProvider struct {
	name string
	err  error
}

func (m *mockPatentProvider) Name() string { return m.name }
func (m *mockPatentProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "mock " + m.name}
}
func (m *mockPatentProvider) Patents(_ context.Context, _ search.PatentSearchParams) ([]search.PatentResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return []search.PatentResult{{Title: "Mock Patent from " + m.name, Number: "US10000001", URL: "https://patents.google.com/patent/US10000001"}}, nil
}

// TestPatentSearchStrategy3SkipsRateLimitedProvider proves the #503 fix:
// Strategy 3 must skip a rate-limited provider and continue to the next
// provider in search.SupportedPatentProviders order, rather than aborting the
// whole ladder (the old `break`-on-rate-limit bug) and falling through to
// Strategy 4's web-discovery. "searchapi" sorts before "epo" in
// SupportedPatentProviders, so wiring a rate-limited searchapi + a healthy epo
// deterministically exercises the "skip past the earlier failing provider"
// path on every run — unlike the old Go-map-iteration-order bug, this must
// pass every single time, not intermittently.
func TestPatentSearchStrategy3SkipsRateLimitedProvider(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	// deps.Search must NOT implement PatentSearcher, or Strategy 2 would
	// short-circuit before Strategy 3 ever runs.
	deps.Search = &mockProvider{}
	deps.PatentProviders = map[string]search.PatentProvider{
		"searchapi": &mockPatentProvider{name: "searchapi", err: fmt.Errorf("rate limited: %w", circuit.ErrRateLimit)},
		"epo":       &mockPatentProvider{name: "epo"},
	}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	// Run multiple times with distinct queries (cache key includes query) to
	// rule out any residual non-determinism — the fix's whole point is that
	// this must be reliable every time, not a coin flip.
	for i := 0; i < 10; i++ {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "patent_search",
			Arguments: map[string]any{
				"query":       fmt.Sprintf("lithium battery thermal management %d", i),
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
		if output["source"] != "epo" {
			t.Fatalf("run %d: expected source 'epo' (Strategy 3 must skip the rate-limited searchapi and continue to epo), got %v", i, output["source"])
		}
		if output["resultCount"].(float64) != 1 {
			t.Fatalf("run %d: expected 1 result from epo, got %v", i, output["resultCount"])
		}
	}
}

// TestPatentSearchStrategy3AllProvidersFail proves that when every
// patent-only provider fails (not just one), Strategy 3 still falls through
// cleanly to Strategy 4's web-discovery fallback rather than erroring out —
// the #503 fix removes the abort-on-rate-limit `break`, but a provider that
// returns a non-rate-limit error must still be skipped the same way.
func TestPatentSearchStrategy3AllProvidersFail(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &mockProviderWithURL{url: "https://patents.google.com/patent/US20200012345A1/en"}
	deps.PatentProviders = map[string]search.PatentProvider{
		"searchapi": &mockPatentProvider{name: "searchapi", err: fmt.Errorf("rate limited: %w", circuit.ErrRateLimit)},
		"epo":       &mockPatentProvider{name: "epo", err: fmt.Errorf("upstream error")},
		"uspto":     &mockPatentProvider{name: "uspto", err: fmt.Errorf("upstream error")},
	}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "patent_search",
		Arguments: map[string]any{
			"query":       "graphene oxide filtration membrane",
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
	if output["source"] != "web_discovery" {
		t.Fatalf("expected fallback to 'web_discovery' when all patent providers fail, got %v", output["source"])
	}
}
