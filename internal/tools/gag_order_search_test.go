package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
)

// swapGagOrderDeps points the Airtable client/base vars at an httptest
// server for the duration of the test, restoring the originals on cleanup —
// matching the established package-level-var-swap convention (see
// brand_research_test.go).
func swapGagOrderDeps(t *testing.T, client *http.Client, baseURL string) {
	t.Helper()
	origClient, origBaseID, origAPIBase := gagOrderHTTPClient, gagOrderAirtableBaseID, gagOrderAirtableAPIBase
	gagOrderHTTPClient = client
	gagOrderAirtableAPIBase = baseURL
	t.Cleanup(func() {
		gagOrderHTTPClient, gagOrderAirtableBaseID, gagOrderAirtableAPIBase = origClient, origBaseID, origAPIBase
	})
}

// newGagOrderAirtableServer builds a fake Airtable API: metadata endpoint
// returns the given tables, and the records endpoint returns one page of
// records for the discovered table.
func newGagOrderAirtableServer(t *testing.T, tables []airtableTable, records []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/meta/bases/"):
			json.NewEncoder(w).Encode(map[string]any{"tables": tables}) //nolint:errcheck
		default:
			recs := make([]map[string]any, 0, len(records))
			for _, f := range records {
				recs = append(recs, map[string]any{"fields": f})
			}
			json.NewEncoder(w).Encode(map[string]any{"records": recs, "offset": ""}) //nolint:errcheck
		}
	}))
}

func TestGagOrderSearchTableDiscoveryPrefersBillTable(t *testing.T) {
	srv := newGagOrderAirtableServer(t,
		[]airtableTable{{ID: "tblOther", Name: "Notes"}, {ID: "tblBills", Name: "Bill Tracker"}},
		[]map[string]any{{"State": "FL", "Bill Name": "HB 999", "Status": "enacted", "Targets": "higher_education", "Year": float64(2023)}},
	)
	defer srv.Close()
	swapGagOrderDeps(t, srv.Client(), srv.URL)

	deps := setupTestDeps()
	deps.PENAmericaAirtableToken = "test-token"

	out, res := callTool(t, deps, "gag_order_search", map[string]any{})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	if out["provider"] != "pen_america" {
		t.Errorf("provider = %v, want pen_america", out["provider"])
	}
	results, ok := out["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("want 1 result, got %v", out["results"])
	}
	r0, _ := results[0].(map[string]any)
	if r0["state"] != "FL" || r0["billName"] != "HB 999" || r0["year"] != float64(2023) {
		t.Errorf("unexpected result: %v", r0)
	}
}

func TestGagOrderSearchTableDiscoveryFallsBackToFirstTable(t *testing.T) {
	srv := newGagOrderAirtableServer(t,
		[]airtableTable{{ID: "tblFirst", Name: "Sheet1"}},
		[]map[string]any{{"Jurisdiction": "TX", "Title": "SB 1", "Bill Status": "pending"}},
	)
	defer srv.Close()
	swapGagOrderDeps(t, srv.Client(), srv.URL)

	deps := setupTestDeps()
	deps.PENAmericaAirtableToken = "test-token"

	out, res := callTool(t, deps, "gag_order_search", map[string]any{})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	results, _ := out["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("want 1 result via fallback table, got %v", out["results"])
	}
	r0, _ := results[0].(map[string]any)
	if r0["state"] != "TX" || r0["billName"] != "SB 1" {
		t.Errorf("unexpected result via fuzzy field match: %v", r0)
	}
}

func TestGagOrderSearchFiltersByState(t *testing.T) {
	srv := newGagOrderAirtableServer(t,
		[]airtableTable{{ID: "tblBills", Name: "Bill Tracker"}},
		[]map[string]any{
			{"State": "FL", "Bill Name": "HB 1", "Status": "enacted", "Year": float64(2022)},
			{"State": "TX", "Bill Name": "SB 2", "Status": "enacted", "Year": float64(2022)},
		},
	)
	defer srv.Close()
	swapGagOrderDeps(t, srv.Client(), srv.URL)

	deps := setupTestDeps()
	deps.PENAmericaAirtableToken = "test-token"

	out, res := callTool(t, deps, "gag_order_search", map[string]any{"state": "FL"})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	results, _ := out["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("want 1 filtered result, got %v", out["results"])
	}
	r0, _ := results[0].(map[string]any)
	if r0["state"] != "FL" {
		t.Errorf("filter did not exclude non-matching state: %v", r0)
	}
}

func TestGagOrderSearchYearRangeFilter(t *testing.T) {
	srv := newGagOrderAirtableServer(t,
		[]airtableTable{{ID: "tblBills", Name: "Bill Tracker"}},
		[]map[string]any{
			{"State": "FL", "Bill Name": "Old Bill", "Year": float64(2010)},
			{"State": "FL", "Bill Name": "New Bill", "Year": float64(2024)},
		},
	)
	defer srv.Close()
	swapGagOrderDeps(t, srv.Client(), srv.URL)

	deps := setupTestDeps()
	deps.PENAmericaAirtableToken = "test-token"

	out, res := callTool(t, deps, "gag_order_search", map[string]any{"year_from": 2020, "year_to": 2025})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	results, _ := out["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("want 1 result in year range, got %v", out["results"])
	}
	r0, _ := results[0].(map[string]any)
	if r0["billName"] != "New Bill" {
		t.Errorf("year range filter kept wrong record: %v", r0)
	}
}

func TestGagOrderSearchZeroResultsHints(t *testing.T) {
	srv := newGagOrderAirtableServer(t,
		[]airtableTable{{ID: "tblBills", Name: "Bill Tracker"}},
		[]map[string]any{{"State": "FL", "Bill Name": "HB 1"}},
	)
	defer srv.Close()
	swapGagOrderDeps(t, srv.Client(), srv.URL)

	deps := setupTestDeps()
	deps.PENAmericaAirtableToken = "test-token"

	out, res := callTool(t, deps, "gag_order_search", map[string]any{"state": "ZZ"})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	if out["resultCount"] != float64(0) {
		t.Errorf("resultCount = %v, want 0", out["resultCount"])
	}
	if out["hints"] == nil {
		t.Error("expected zero-result hints to be present")
	}
}

func TestGagOrderSearchMaxResultsCap(t *testing.T) {
	records := make([]map[string]any, 0, 10)
	for i := 0; i < 10; i++ {
		records = append(records, map[string]any{"State": "FL", "Bill Name": "Bill"})
	}
	srv := newGagOrderAirtableServer(t, []airtableTable{{ID: "tblBills", Name: "Bill Tracker"}}, records)
	defer srv.Close()
	swapGagOrderDeps(t, srv.Client(), srv.URL)

	deps := setupTestDeps()
	deps.PENAmericaAirtableToken = "test-token"

	out, res := callTool(t, deps, "gag_order_search", map[string]any{"max_results": 500})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	if out["resultCount"] != float64(10) {
		t.Errorf("resultCount = %v, want 10 (all available, request capped to 200)", out["resultCount"])
	}
}

func TestGagOrderSearchRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	swapGagOrderDeps(t, srv.Client(), srv.URL)

	deps := setupTestDeps()
	deps.PENAmericaAirtableToken = "test-token"

	_, res := callTool(t, deps, "gag_order_search", map[string]any{})
	if !res.IsError {
		t.Error("429 upstream should produce a tool error")
	}
}

func TestGagOrderSearchUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	swapGagOrderDeps(t, srv.Client(), srv.URL)

	deps := setupTestDeps()
	deps.PENAmericaAirtableToken = "test-token"

	_, res := callTool(t, deps, "gag_order_search", map[string]any{})
	if !res.IsError {
		t.Error("500 upstream should produce a tool error")
	}
}

func TestGagOrderSearchCaches(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/meta/bases/") {
			json.NewEncoder(w).Encode(map[string]any{"tables": []airtableTable{{ID: "tblBills", Name: "Bill Tracker"}}}) //nolint:errcheck
			return
		}
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"records": []map[string]any{{"fields": map[string]any{"State": "FL", "Bill Name": "HB 1"}}},
			"offset":  "",
		})
	}))
	defer srv.Close()
	swapGagOrderDeps(t, srv.Client(), srv.URL)

	deps := setupTestDeps()
	deps.Cache = cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 1})
	deps.PENAmericaAirtableToken = "test-token"

	for i := 0; i < 2; i++ {
		_, res := callTool(t, deps, "gag_order_search", map[string]any{})
		if res.IsError {
			t.Fatalf("unexpected error on call %d", i)
		}
	}
	if calls != 2 {
		t.Errorf("expected 2 upstream calls total (1 discovery + 1 records) on first call, second served from cache; got %d", calls)
	}
}

func TestFuzzyFieldMatchesCaseInsensitive(t *testing.T) {
	fields := map[string]any{"STATE": "FL", "count": float64(3)}
	if got := fuzzyField(fields, "State"); got != "FL" {
		t.Errorf("fuzzyField(State) = %q, want FL", got)
	}
	if got := fuzzyField(fields, "Count"); got != "3" {
		t.Errorf("fuzzyField(Count) = %q, want 3", got)
	}
	if got := fuzzyField(fields, "Missing"); got != "" {
		t.Errorf("fuzzyField(Missing) = %q, want empty", got)
	}
}

func TestFuzzyFieldContainsMatchesSubstring(t *testing.T) {
	fields := map[string]any{"Bill URL": "https://example.com/bill"}
	if got := fuzzyFieldContains(fields, "url"); got != "https://example.com/bill" {
		t.Errorf("fuzzyFieldContains(url) = %q, want the URL field value", got)
	}
	if got := fuzzyFieldContains(fields, "nonexistent"); got != "" {
		t.Errorf("fuzzyFieldContains(nonexistent) = %q, want empty", got)
	}
}
