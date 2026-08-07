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

// =============================================================================
// #529: search_type=landscape clusters by assignee instead of being an alias
// for prior_art that only changes the echoed searchType string.
// =============================================================================

// TestClusterPatentsByAssignee proves the core #529 behavior: patents are
// grouped by assignee, clusters are ordered by size descending (the most
// prolific assignee first — the "competitive overview" landscape promises),
// each cluster's own patents stay contiguous, and the result is truncated to
// the caller's limit without splitting a cluster's start away from its
// continuation.
func TestClusterPatentsByAssignee(t *testing.T) {
	patents := []scraper.PatentResult{
		{Number: "US1", Assignee: "Acme Corp"},
		{Number: "US2", Assignee: "Widget Inc"},
		{Number: "US3", Assignee: "Acme Corp"},
		{Number: "US4", Assignee: "Beta LLC"},
		{Number: "US5", Assignee: "Acme Corp"},
	}

	got, summary := clusterPatentsByAssignee(patents, 10)

	if len(got) != 5 {
		t.Fatalf("expected all 5 patents preserved, got %d", len(got))
	}
	// Acme Corp (3 patents) must lead, since it's the largest cluster.
	for i := 0; i < 3; i++ {
		if got[i].Assignee != "Acme Corp" {
			t.Errorf("position %d: expected Acme Corp (largest cluster first), got %q", i, got[i].Assignee)
		}
	}
	if len(summary) != 3 {
		t.Fatalf("expected 3 assignee clusters in summary, got %d: %+v", len(summary), summary)
	}
	if summary[0]["assignee"] != "Acme Corp" || summary[0]["count"] != 3 {
		t.Errorf("expected summary[0] = {Acme Corp, 3}, got %+v", summary[0])
	}
}

// TestClusterPatentsByAssignee_TruncatesWithoutSplittingCluster proves that
// when limit falls in the middle of what would be a cluster's contiguous
// block, the truncation still respects size-descending cluster order (it
// does not reach past a smaller earlier cluster to finish a later one), and
// never exceeds the requested limit.
func TestClusterPatentsByAssignee_TruncatesWithoutSplittingCluster(t *testing.T) {
	patents := []scraper.PatentResult{
		{Number: "US1", Assignee: "BigCo"},
		{Number: "US2", Assignee: "BigCo"},
		{Number: "US3", Assignee: "BigCo"},
		{Number: "US4", Assignee: "SmallCo"},
	}

	got, _ := clusterPatentsByAssignee(patents, 2)

	if len(got) != 2 {
		t.Fatalf("expected exactly 2 patents (limit), got %d", len(got))
	}
	for _, p := range got {
		if p.Assignee != "BigCo" {
			t.Errorf("expected only BigCo patents within the truncated limit, got %q", p.Assignee)
		}
	}
}

// TestClusterPatentsByAssignee_UnassigneedPatentsKept proves patents with no
// assignee are not silently dropped — they form singleton clusters and are
// backfilled after named-assignee clusters if there's room under limit.
func TestClusterPatentsByAssignee_UnassigneedPatentsKept(t *testing.T) {
	patents := []scraper.PatentResult{
		{Number: "US1", Assignee: "Acme Corp"},
		{Number: "US2", Assignee: ""},
	}

	got, summary := clusterPatentsByAssignee(patents, 10)

	if len(got) != 2 {
		t.Fatalf("expected both patents kept, got %d", len(got))
	}
	// Only the named-assignee cluster appears in the summary.
	if len(summary) != 1 || summary[0]["assignee"] != "Acme Corp" {
		t.Errorf("expected summary to contain only Acme Corp, got %+v", summary)
	}
}

// TestPatentSearchLandscapeDiffersFromPriorArt proves the #529 fix end to
// end: the same query with search_type=prior_art vs search_type=landscape
// must NOT return byte-identical patents/order — landscape's assignee
// clustering changes the composition, and its response carries a non-empty
// assigneeClusters summary that prior_art's response omits entirely.
func TestPatentSearchLandscapeDiffersFromPriorArt(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.Search = &mockMultiPatentProvider{
		name: "mock-main",
		results: []search.PatentResult{
			{Title: "P1", Number: "US1", Assignee: "Widget Inc", URL: "https://patents.google.com/patent/US1"},
			{Title: "P2", Number: "US2", Assignee: "Acme Corp", URL: "https://patents.google.com/patent/US2"},
			{Title: "P3", Number: "US3", Assignee: "Acme Corp", URL: "https://patents.google.com/patent/US3"},
			{Title: "P4", Number: "US4", Assignee: "Beta LLC", URL: "https://patents.google.com/patent/US4"},
		},
	}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	callAndParse := func(searchType string) map[string]any {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "patent_search",
			Arguments: map[string]any{
				"query":       "widget",
				"search_type": searchType,
				"num_results": 3,
			},
		})
		if err != nil {
			t.Fatalf("CallTool(%s) failed: %v", searchType, err)
		}
		if res.IsError {
			t.Fatalf("CallTool(%s) returned error: %v", searchType, res.Content[0].(*mcp.TextContent).Text)
		}
		var output map[string]any
		if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &output); err != nil {
			t.Fatalf("failed to parse output for %s: %v", searchType, err)
		}
		return output
	}

	priorArt := callAndParse("prior_art")
	landscape := callAndParse("landscape")

	priorArtJSON, _ := json.Marshal(priorArt["patents"])
	landscapeJSON, _ := json.Marshal(landscape["patents"])
	if string(priorArtJSON) == string(landscapeJSON) {
		t.Fatalf("expected prior_art and landscape to differ in patent composition/order for the same query, both got: %s", priorArtJSON)
	}

	if _, ok := priorArt["assigneeClusters"]; ok {
		t.Errorf("prior_art response must not carry assigneeClusters, got %v", priorArt["assigneeClusters"])
	}
	clusters, ok := landscape["assigneeClusters"].([]any)
	if !ok || len(clusters) == 0 {
		t.Fatalf("expected landscape response to carry a non-empty assigneeClusters summary, got %v", landscape["assigneeClusters"])
	}
	first := clusters[0].(map[string]any)
	if first["assignee"] != "Acme Corp" || first["count"].(float64) != 2 {
		t.Errorf("expected landscape's leading cluster to be Acme Corp with count 2, got %+v", first)
	}
}

// mockMultiPatentProvider embeds mockProviderWithURL (to satisfy
// search.Provider for deps.Search) and returns a caller-supplied multi-
// result slice from Patents, letting tests exercise landscape's assignee
// clustering (which needs several results across distinct assignees) rather
// than the single-canned-result mockPatentProvider used elsewhere.
type mockMultiPatentProvider struct {
	mockProviderWithURL
	name    string
	results []search.PatentResult
}

func (m *mockMultiPatentProvider) Name() string { return m.name }
func (m *mockMultiPatentProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "mock " + m.name}
}
func (m *mockMultiPatentProvider) Patents(_ context.Context, _ search.PatentSearchParams) ([]search.PatentResult, error) {
	return m.results, nil
}

// mockCapturingPatentProvider records the search.PatentSearchParams it
// receives so a test can assert on fields (like CPCCode) that the tool layer
// is responsible for threading through, without needing a real provider or
// network access (#530).
type mockCapturingPatentProvider struct {
	mockProviderWithURL
	name     string
	received *search.PatentSearchParams
	result   search.PatentResult
}

func (m *mockCapturingPatentProvider) Name() string { return m.name }
func (m *mockCapturingPatentProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "mock " + m.name}
}
func (m *mockCapturingPatentProvider) Patents(_ context.Context, params search.PatentSearchParams) ([]search.PatentResult, error) {
	m.received = &params
	return []search.PatentResult{m.result}, nil
}

// TestPatentSearchThreadsCPCCodeToProvider proves the #530 fix: cpc_code
// reaches the provider-facing search.PatentSearchParams (via Strategy 2's
// router path, deps.Search) rather than being silently dropped — before the
// fix, PatentSearchParams had no CPCCode field at all, so no provider ever
// saw the caller's cpc_code regardless of which strategy answered.
func TestPatentSearchThreadsCPCCodeToProvider(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	mock := &mockCapturingPatentProvider{
		name:   "mock-main",
		result: search.PatentResult{Title: "Battery Cell", Number: "US10000009", URL: "https://patents.google.com/patent/US10000009"},
	}
	deps.Search = mock
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "patent_search",
		Arguments: map[string]any{
			"query":    "battery",
			"cpc_code": "H01M10/00",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("CallTool returned error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	if mock.received == nil {
		t.Fatal("expected provider to receive PatentSearchParams, got none")
	}
	if mock.received.CPCCode != "H01M10/00" {
		t.Errorf("expected CPCCode %q to reach the provider, got %q", "H01M10/00", mock.received.CPCCode)
	}
}

// =============================================================================
// #527: explicit web-search-fallback provider is honored, not silently
// rewritten to whichever dedicated patent provider answers first.
// =============================================================================

// TestPatentSearchExplicitWebProviderHonored proves the #527 fix:
// provider="google" (a documented web-search-fallback name, not one of
// search.SupportedPatentProviders) must route through Strategy 4's
// web-discovery using that pinned provider — never fall through to Strategy 2
// (deps.Search, here wired as a healthy PatentSearcher) or Strategy 3 (a
// healthy dedicated "epo" provider), both of which would otherwise answer
// first and silently substitute themselves for the caller's explicit choice.
func TestPatentSearchExplicitWebProviderHonored(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	// deps.Search implements PatentSearcher (Strategy 2) AND is registered
	// under provider name "google" (Strategy 4's pinned-provider path) so a
	// bug that fell through to Strategy 2 instead of honoring the pin would
	// still produce a result — just from the wrong strategy — while the
	// result's own patent number differs from Strategy 3/2's canned patent,
	// making a fallthrough to either detectable.
	googleProvider := &mockProviderWithURL{url: "https://patents.google.com/patent/US20200099999A1/en"}
	deps.Search = &mockPatentSearcherProvider{
		mockProviderWithURL: *googleProvider,
		name:                "mock-main",
	}
	deps.SearchProviders = map[string]search.Provider{"google": googleProvider}
	deps.PatentProviders = map[string]search.PatentProvider{
		"epo": &mockPatentProvider{name: "epo"},
	}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "patent_search",
		Arguments: map[string]any{
			"query":    "solid-state battery electrolyte",
			"provider": "google",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if output["source"] != "google" {
		t.Fatalf("expected source 'google' (the caller's explicit choice, honored via web-discovery), got %v — provider=\"google\" must never be silently rewritten to epo or the main provider", output["source"])
	}
	patents, ok := output["patents"].([]any)
	if !ok || len(patents) == 0 {
		t.Fatalf("expected at least one patent discovered via the pinned google provider, got %v", output["patents"])
	}
	first := patents[0].(map[string]any)
	if first["number"] != "US20200099999A1" {
		t.Fatalf("expected the patent discovered from the pinned google provider's web result, got %v (Strategy 2/3 must not have answered instead)", first["number"])
	}
}

// mockPatentSearcherProvider wires mockProviderWithURL (a search.Provider) up
// as a search.PatentSearcher too, so it satisfies Strategy 2's type-assertion
// `deps.Search.(search.PatentSearcher)` — letting TestPatentSearchExplicitWebProviderHonored
// prove that an explicit web-provider pin bypasses Strategy 2 even when the
// main provider is patent-capable.
type mockPatentSearcherProvider struct {
	mockProviderWithURL
	name string
}

func (m *mockPatentSearcherProvider) Name() string { return m.name }
func (m *mockPatentSearcherProvider) Patents(_ context.Context, _ search.PatentSearchParams) ([]search.PatentResult, error) {
	return []search.PatentResult{{Title: "Should not be picked", Number: "US99999999", URL: "https://patents.google.com/patent/US99999999"}}, nil
}

// TestPatentSearchUnknownProviderStillErrors proves #527 doesn't loosen
// resolvePatentSearcher's existing "unknown name" validation: a provider
// string that is neither a dedicated patent provider nor a recognized web
// provider must still return a structured error naming the supported list,
// not silently fall through to any strategy.
func TestPatentSearchUnknownProviderStillErrors(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.PatentProviders = map[string]search.PatentProvider{
		"epo": &mockPatentProvider{name: "epo"},
	}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "patent_search",
		Arguments: map[string]any{
			"query":    "solid-state battery electrolyte",
			"provider": "not-a-real-provider",
		},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected a structured error for an unknown provider, got success: %v", res.Content[0].(*mcp.TextContent).Text)
	}
}
