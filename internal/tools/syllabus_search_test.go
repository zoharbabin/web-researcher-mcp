package tools

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
)

// swapSyllabusHTTPClient points syllabusHTTPClient at an httptest server for
// the duration of the test, restoring the original on cleanup — matching the
// established package-level-var-swap convention (see brand_research_test.go).
func swapSyllabusHTTPClient(t *testing.T, client *http.Client) {
	t.Helper()
	orig := syllabusHTTPClient
	syllabusHTTPClient = client
	t.Cleanup(func() { syllabusHTTPClient = orig })
}

func TestSyllabusSearchMissingQuery(t *testing.T) {
	deps := setupTestDeps()
	_, res := callTool(t, deps, "syllabus_search", map[string]any{})
	if !res.IsError {
		t.Error("missing query should produce a tool error")
	}
}

func TestSyllabusSearchRequestShape(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[{"title":"Das Kapital","author":"Marx","institution":"Yale","country":"US","field":"economics","year":2020,"frequency":42,"institution_count":10}]}`))
	}))
	defer srv.Close()
	swapSyllabusHTTPClient(t, srv.Client())

	deps := setupTestDeps()
	deps.OpenSyllabusAPIKey = "test-key"
	deps.OpenSyllabusAPIURL = srv.URL

	out, res := callTool(t, deps, "syllabus_search", map[string]any{
		"query":       "Marx",
		"institution": "Yale",
		"country":     "US",
		"field":       "economics",
		"year_from":   2015,
		"year_to":     2024,
		"sort_by":     "recency",
		"max_results": 5,
	})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	if gotPath != "/v1/syllabi" {
		t.Errorf("request path = %q, want /v1/syllabi", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want Bearer test-key", gotAuth)
	}
	for _, want := range []string{"query=Marx", "institution=Yale", "country=US", "field=economics", "year_from=2015", "year_to=2024", "sort_by=recency", "max_results=5"} {
		if !containsQueryParam(gotQuery, want) {
			t.Errorf("query %q missing param %q", gotQuery, want)
		}
	}

	if out["provider"] != "opensyllabus" {
		t.Errorf("provider = %v, want opensyllabus", out["provider"])
	}
	if out["trust"] != "untrusted-external-content" {
		t.Errorf("missing trust marker: %v", out["trust"])
	}
	if out["sortBy"] != "recency" {
		t.Errorf("sortBy = %v, want recency", out["sortBy"])
	}
	results, ok := out["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("want 1 result, got %v", out["results"])
	}
	r0, _ := results[0].(map[string]any)
	if r0["title"] != "Das Kapital" || r0["frequency"] != float64(42) {
		t.Errorf("unexpected result: %v", r0)
	}
	if out["corpusNote"] == nil {
		t.Error("expected corpusNote to be present")
	}
}

func TestSyllabusSearchDefaults(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()
	swapSyllabusHTTPClient(t, srv.Client())

	deps := setupTestDeps()
	deps.OpenSyllabusAPIKey = "test-key"
	deps.OpenSyllabusAPIURL = srv.URL

	out, res := callTool(t, deps, "syllabus_search", map[string]any{"query": "Marx"})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	if !containsQueryParam(gotQuery, "sort_by=frequency") {
		t.Errorf("default sort_by missing: %q", gotQuery)
	}
	if !containsQueryParam(gotQuery, "max_results=10") {
		t.Errorf("default max_results missing: %q", gotQuery)
	}
	if out["resultCount"] != float64(0) {
		t.Errorf("resultCount = %v, want 0", out["resultCount"])
	}
	if out["hints"] == nil {
		t.Error("expected zero-result hints to be present")
	}
}

func TestSyllabusSearchMaxResultsCap(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()
	swapSyllabusHTTPClient(t, srv.Client())

	deps := setupTestDeps()
	deps.OpenSyllabusAPIKey = "test-key"
	deps.OpenSyllabusAPIURL = srv.URL

	_, res := callTool(t, deps, "syllabus_search", map[string]any{"query": "Marx", "max_results": 500})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	if !containsQueryParam(gotQuery, "max_results=50") {
		t.Errorf("max_results should be capped to 50, got query %q", gotQuery)
	}
}

func TestSyllabusSearchRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	swapSyllabusHTTPClient(t, srv.Client())

	deps := setupTestDeps()
	deps.OpenSyllabusAPIKey = "test-key"
	deps.OpenSyllabusAPIURL = srv.URL

	_, res := callTool(t, deps, "syllabus_search", map[string]any{"query": "Marx"})
	if !res.IsError {
		t.Error("429 upstream should produce a tool error")
	}
}

func TestSyllabusSearchUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	swapSyllabusHTTPClient(t, srv.Client())

	deps := setupTestDeps()
	deps.OpenSyllabusAPIKey = "test-key"
	deps.OpenSyllabusAPIURL = srv.URL

	_, res := callTool(t, deps, "syllabus_search", map[string]any{"query": "Marx"})
	if !res.IsError {
		t.Error("500 upstream should produce a tool error")
	}
}

func TestSyllabusSearchCaches(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[{"title":"Das Kapital","author":"Marx"}]}`))
	}))
	defer srv.Close()
	swapSyllabusHTTPClient(t, srv.Client())

	deps := setupTestDeps()
	deps.Cache = cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 1})
	deps.OpenSyllabusAPIKey = "test-key"
	deps.OpenSyllabusAPIURL = srv.URL

	for i := 0; i < 2; i++ {
		_, res := callTool(t, deps, "syllabus_search", map[string]any{"query": "Marx"})
		if res.IsError {
			t.Fatalf("unexpected error on call %d", i)
		}
	}
	if calls != 1 {
		t.Errorf("expected 1 upstream call (second served from cache), got %d", calls)
	}
}

func containsQueryParam(rawQuery, param string) bool {
	for _, p := range splitQuery(rawQuery) {
		if p == param {
			return true
		}
	}
	return false
}

func splitQuery(rawQuery string) []string {
	var parts []string
	start := 0
	for i := 0; i <= len(rawQuery); i++ {
		if i == len(rawQuery) || rawQuery[i] == '&' {
			parts = append(parts, rawQuery[start:i])
			start = i + 1
		}
	}
	return parts
}
