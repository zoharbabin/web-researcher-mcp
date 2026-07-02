package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// callToolJSON invokes a tool and decodes its (non-error) JSON body.
func callToolJSON(t *testing.T, deps Dependencies, name string, args map[string]any) map[string]any {
	t.Helper()
	ctx := context.Background()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s) failed: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("CallTool(%s) returned IsError: %s", name, res.Content[0].(*mcp.TextContent).Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse(%s): %v", name, err)
	}
	return out
}

// TestWebSearchZeroResultHints verifies web_search attaches a ZeroResultHints
// object (issue #100) only when the provider returns nothing (parity with
// academic/patent), and never on a non-empty result set.
func TestWebSearchZeroResultHints(t *testing.T) {
	t.Run("emits hints on zero results", func(t *testing.T) {
		deps := setupTestDeps()
		deps.Search = &emptyWebProvider{}
		out := callToolJSON(t, deps, "web_search", map[string]any{"query": "asdkjhqweh", "site": "example.com"})

		if out["resultCount"].(float64) != 0 {
			t.Fatalf("expected 0 results, got %v", out["resultCount"])
		}
		hints, ok := out["hints"].(map[string]any)
		if !ok {
			t.Fatalf("expected hints object on zero results, got %v", out["hints"])
		}
		// site filter should surface as filters_too_restrictive
		if hints["reason"] != "filters_too_restrictive" {
			t.Errorf("expected reason filters_too_restrictive (site filter present), got %v", hints["reason"])
		}
		if _, ok := hints["filtersApplied"].(map[string]any)["site"]; !ok {
			t.Errorf("expected site in filtersApplied, got %v", hints["filtersApplied"])
		}
		// #357: every zero-result hint must carry the fixed epistemic warning so
		// callers never assert non-existence from an empty result set.
		if hints["epistemicWarning"] != epistemicZeroResultWarning {
			t.Errorf("expected epistemicWarning %q, got %v", epistemicZeroResultWarning, hints["epistemicWarning"])
		}
	})

	t.Run("no hints when results present", func(t *testing.T) {
		deps := setupTestDeps() // default mockProvider returns one result
		out := callToolJSON(t, deps, "web_search", map[string]any{"query": "test"})
		if out["resultCount"].(float64) == 0 {
			t.Fatal("precondition: expected non-zero results from mockProvider")
		}
		if _, present := out["hints"]; present {
			t.Errorf("hints must be absent on non-empty results, got %v", out["hints"])
		}
	})
}

// TestNewsSearchZeroResultHints mirrors the web case for news_search, including
// that the default freshness window is surfaced as a filter (issue #100).
func TestNewsSearchZeroResultHints(t *testing.T) {
	t.Run("emits hints with freshness on zero results", func(t *testing.T) {
		deps := setupTestDeps()
		deps.Search = &emptyWebProvider{}
		out := callToolJSON(t, deps, "news_search", map[string]any{"query": "asdkjhqweh"})

		if out["resultCount"].(float64) != 0 {
			t.Fatalf("expected 0 results, got %v", out["resultCount"])
		}
		hints, ok := out["hints"].(map[string]any)
		if !ok {
			t.Fatalf("expected hints object on zero results, got %v", out["hints"])
		}
		filters, _ := hints["filtersApplied"].(map[string]any)
		if filters["freshness"] != "week" {
			t.Errorf("expected default freshness=week surfaced as a filter, got %v", filters)
		}
		// #357: news_search must carry the same fixed epistemic warning as web_search.
		if hints["epistemicWarning"] != epistemicZeroResultWarning {
			t.Errorf("expected epistemicWarning %q, got %v", epistemicZeroResultWarning, hints["epistemicWarning"])
		}
	})

	t.Run("no hints when results present", func(t *testing.T) {
		deps := setupTestDeps() // default mockProvider returns one news result
		out := callToolJSON(t, deps, "news_search", map[string]any{"query": "test"})
		if _, present := out["hints"]; present {
			t.Errorf("hints must be absent on non-empty results, got %v", out["hints"])
		}
	})
}

// TestImageSearchZeroResultHints (#357): image_search's zero-result branch
// (`if len(results) == 0 { output["hints"] = buildZeroResultHints(...) }`)
// had no covering test — no test drove image_search with a provider
// returning zero results, so a regression dropping the hints field or its
// epistemicWarning would not be caught.
func TestImageSearchZeroResultHints(t *testing.T) {
	t.Run("emits hints on zero results", func(t *testing.T) {
		deps := setupTestDeps()
		deps.Search = &emptyWebProvider{}
		out := callToolJSON(t, deps, "image_search", map[string]any{"query": "asdkjhqweh"})

		if out["resultCount"].(float64) != 0 {
			t.Fatalf("expected 0 results, got %v", out["resultCount"])
		}
		hints, ok := out["hints"].(map[string]any)
		if !ok {
			t.Fatalf("expected hints object on zero results, got %v", out["hints"])
		}
		if hints["epistemicWarning"] != epistemicZeroResultWarning {
			t.Errorf("expected epistemicWarning %q, got %v", epistemicZeroResultWarning, hints["epistemicWarning"])
		}
	})

	t.Run("no hints when results present", func(t *testing.T) {
		deps := setupTestDeps() // default mockProvider returns one image result
		out := callToolJSON(t, deps, "image_search", map[string]any{"query": "cats"})
		if out["resultCount"].(float64) == 0 {
			t.Fatal("precondition: expected non-zero results from mockProvider")
		}
		if _, present := out["hints"]; present {
			t.Errorf("hints must be absent on non-empty results, got %v", out["hints"])
		}
	})
}

// TestHealthyAlternativesExcludesUsed checks the alternatives helper omits the
// provider that was just used and returns a deterministic order (issue #100).
func TestHealthyAlternativesExcludesUsed(t *testing.T) {
	deps := Dependencies{
		SearchProviders: map[string]search.Provider{
			"brave":      &mockProvider{},
			"serper":     &mockProvider{},
			"duckduckgo": &mockProvider{},
		},
	}
	alts := healthyAlternatives(deps, "brave")
	if len(alts) != 2 {
		t.Fatalf("expected 2 alternatives (brave excluded), got %v", alts)
	}
	if alts[0] != "duckduckgo" || alts[1] != "serper" {
		t.Errorf("expected sorted [duckduckgo serper], got %v", alts)
	}
	for _, a := range alts {
		if a == "brave" {
			t.Errorf("used provider must be excluded, got %v", alts)
		}
	}
}

// routerNamedProvider is an empty provider that reports Name()=="router", to
// exercise the hint-name mapping for the multi-provider Router case.
type routerNamedProvider struct{ *emptyWebProvider }

func (routerNamedProvider) Name() string { return "router" }

// TestWebSearchHints_RouterNameNotLeaked verifies that when the resolved
// provider is the Router (Name=="router"), the zero-result hints do NOT surface
// the unusable "router" string in providersAttempted (issue #100 routing case).
func TestWebSearchHints_RouterNameNotLeaked(t *testing.T) {
	deps := setupTestDeps()
	deps.Search = routerNamedProvider{&emptyWebProvider{}}
	out := callToolJSON(t, deps, "web_search", map[string]any{"query": "asdkjhqweh"})

	hints, ok := out["hints"].(map[string]any)
	if !ok {
		t.Fatalf("expected hints on zero results, got %v", out["hints"])
	}
	if pa, present := hints["providersAttempted"]; present {
		for _, p := range pa.([]any) {
			if p == "router" {
				t.Errorf("hints leaked unusable internal name 'router': %v", pa)
			}
		}
	}
}

// TestHintProviderName maps the router sentinel to "" and passes concrete names through.
func TestHintProviderName(t *testing.T) {
	if got := hintProviderName(routerNamedProvider{&emptyWebProvider{}}); got != "" {
		t.Errorf("router name should map to empty, got %q", got)
	}
	if got := hintProviderName(&mockProvider{}); got != "mock" {
		t.Errorf("concrete provider name should pass through, got %q", got)
	}
	if got := hintProviderName(nil); got != "" {
		t.Errorf("nil provider should map to empty, got %q", got)
	}
}

// TestBuildZeroResultHints_EpistemicWarning verifies the shared constructor
// (issue #357) always populates EpistemicWarning with the fixed, documented
// literal — regardless of provider/filters/alternatives — since every one of
// the 11 zero-result-hint call sites (web/news/academic/patent/image/
// search_and_scrape/filing/case/econ/trial/local search) funnels through this
// single function and must stay byte-identical.
func TestBuildZeroResultHints_EpistemicWarning(t *testing.T) {
	cases := []struct {
		name         string
		provider     string
		params       map[string]string
		alternatives []string
	}{
		{name: "no provider, no filters, no alternatives"},
		{name: "provider only", provider: "brave"},
		{name: "filters present", provider: "brave", params: map[string]string{"site": "example.com"}},
		{name: "alternatives present", provider: "brave", alternatives: []string{"serper"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hints := buildZeroResultHints(tc.provider, tc.params, tc.alternatives)
			if hints.EpistemicWarning != epistemicZeroResultWarning {
				t.Errorf("expected EpistemicWarning %q, got %q", epistemicZeroResultWarning, hints.EpistemicWarning)
			}
		})
	}
}

// emptyEconProvider implements search.EconProvider and always returns zero
// results, so econ_search's zero-result hints branch (buildZeroResultHints +
// econFilterMap, issue #357) can be exercised in a test.
type emptyEconProvider struct{}

func (m *emptyEconProvider) Name() string { return "fred" }
func (m *emptyEconProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"US"}, RateClass: "free", Description: "empty fred"}
}
func (m *emptyEconProvider) Econ(_ context.Context, _ search.EconSearchParams) ([]search.EconResult, error) {
	return []search.EconResult{}, nil
}

// TestEconSearchZeroResultHints guards issue #357's explicit requirement that
// econ_search stop passing buildZeroResultHints(providerName, nil, nil) and
// instead surface the filters that were actually set (via econFilterMap) so a
// caller can see which one to remove. Without this test, econFilterMap
// regressing to omit a set filter (or the call reverting to nil, nil) would
// not be caught by any test.
func TestEconSearchZeroResultHints(t *testing.T) {
	t.Run("emits hints with applied filters on zero results", func(t *testing.T) {
		deps := setupTestDeps()
		deps.EconProviders = map[string]search.EconProvider{"fred": &emptyEconProvider{}}
		out := callToolJSON(t, deps, "econ_search", map[string]any{
			"query":   "asdkjhqweh",
			"country": "US",
			"units":   "pch",
		})

		if out["resultCount"].(float64) != 0 {
			t.Fatalf("expected 0 results, got %v", out["resultCount"])
		}
		hints, ok := out["hints"].(map[string]any)
		if !ok {
			t.Fatalf("expected hints object on zero results, got %v", out["hints"])
		}
		if hints["reason"] != "filters_too_restrictive" {
			t.Errorf("expected reason filters_too_restrictive (country/units set), got %v", hints["reason"])
		}
		filters, ok := hints["filtersApplied"].(map[string]any)
		if !ok {
			t.Fatalf("expected filtersApplied object, got %v", hints["filtersApplied"])
		}
		if filters["country"] != "US" {
			t.Errorf("expected country in filtersApplied, got %v", filters)
		}
		if filters["units"] != "pch" {
			t.Errorf("expected units in filtersApplied, got %v", filters)
		}
		if hints["epistemicWarning"] != epistemicZeroResultWarning {
			t.Errorf("expected epistemicWarning %q, got %v", epistemicZeroResultWarning, hints["epistemicWarning"])
		}
	})

	t.Run("no hints when results present", func(t *testing.T) {
		deps := setupTestDeps() // default mockEconProvider returns one series
		out := callToolJSON(t, deps, "econ_search", map[string]any{"query": "gdp"})
		if out["resultCount"].(float64) == 0 {
			t.Fatal("precondition: expected non-zero results from mockEconProvider")
		}
		if _, present := out["hints"]; present {
			t.Errorf("hints must be absent on non-empty results, got %v", out["hints"])
		}
	})
}

// TestEconFilterMap verifies econFilterMap surfaces exactly the filterable
// econ_search params that were set (issue #357), so a regression that silently
// drops a field (e.g. Country) would fail this test directly.
func TestEconFilterMap(t *testing.T) {
	t.Run("no filters set", func(t *testing.T) {
		m := econFilterMap(econSearchInput{Query: "gdp"})
		if len(m) != 0 {
			t.Errorf("expected empty map, got %v", m)
		}
	})

	t.Run("all filters set", func(t *testing.T) {
		m := econFilterMap(econSearchInput{
			Query:     "gdp",
			Country:   "US",
			DateFrom:  "2020-01-01",
			DateTo:    "2021-01-01",
			Frequency: "q",
			Units:     "pch",
		})
		want := map[string]string{
			"country":   "US",
			"date_from": "2020-01-01",
			"date_to":   "2021-01-01",
			"frequency": "q",
			"units":     "pch",
		}
		if len(m) != len(want) {
			t.Fatalf("expected %d filters, got %v", len(want), m)
		}
		for k, v := range want {
			if m[k] != v {
				t.Errorf("expected %s=%q, got %q", k, v, m[k])
			}
		}
	})
}

// TestHealthyAlternativesEmpty returns nil when no other provider is configured.
func TestHealthyAlternativesEmpty(t *testing.T) {
	deps := Dependencies{SearchProviders: map[string]search.Provider{"brave": &mockProvider{}}}
	if alts := healthyAlternatives(deps, "brave"); alts != nil {
		t.Errorf("expected nil when only the used provider is configured, got %v", alts)
	}
	if alts := healthyAlternatives(Dependencies{}, "brave"); alts != nil {
		t.Errorf("expected nil when no providers configured, got %v", alts)
	}
}
