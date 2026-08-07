package tools

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// TestEconResultToMap_ObservationsEmitLabels guards the rank-4 live-test finding:
// multi-dimensional providers (OECD, Eurostat) compose a disambiguating label
// (sex/age/seasonal-adjustment) into Title — and Units — for each subgroup
// series. In observations mode those must be surfaced so interleaved rows sharing
// a period are tellable apart. Plain single series (FRED/World Bank) leave
// Title/Units empty, so the keys stay absent for them.
func TestEconResultToMap_ObservationsEmitLabels(t *testing.T) {
	t.Parallel()

	// A labeled subgroup observation (e.g. Eurostat une_rt_m, females 15-24).
	labeled := econResultToMap(search.EconResult{
		Source:   "eurostat",
		SeriesID: "une_rt_m",
		Date:     "2023-01",
		Value:    2.9,
		HasValue: true,
		Title:    "Unemployment rate — Females, From 15 to 74 years",
		Units:    "Percentage of population in the labour force",
	}, "observations")
	if labeled["title"] != "Unemployment rate — Females, From 15 to 74 years" {
		t.Errorf("observations row should surface the subgroup title, got %v", labeled["title"])
	}
	if labeled["units"] != "Percentage of population in the labour force" {
		t.Errorf("observations row should surface units, got %v", labeled["units"])
	}
	if labeled["value"] != 2.9 || labeled["date"] != "2023-01" {
		t.Errorf("value/date must still pass through exactly, got %v / %v", labeled["value"], labeled["date"])
	}

	// A plain single-series observation (FRED) carries no label → keys absent.
	plain := econResultToMap(search.EconResult{
		Source:   "fred",
		SeriesID: "UNRATE",
		Date:     "2023-01",
		Value:    3.4,
		HasValue: true,
	}, "observations")
	if _, present := plain["title"]; present {
		t.Errorf("a plain FRED observation must not carry a title key, got %v", plain["title"])
	}
	if _, present := plain["units"]; present {
		t.Errorf("a plain FRED observation must not carry a units key, got %v", plain["units"])
	}
}

// TestEconResultToMap_MissingValueIsExplicitNull is the regression guard for
// #505: a FRED "." sentinel (delayed/not-yet-released observation, e.g. the
// Oct 2025 government-shutdown gap in BLS releases) decodes to
// EconResult{HasValue: false}. The rendered map must carry `value: null`
// (present, explicit) plus `available: false` — never a silently absent
// `value` key, which is indistinguishable from a parse failure.
func TestEconResultToMap_MissingValueIsExplicitNull(t *testing.T) {
	t.Parallel()

	missing := econResultToMap(search.EconResult{
		Source:   "fred",
		SeriesID: "UNRATE",
		Date:     "2025-10-01",
		HasValue: false,
	}, "observations")

	value, present := missing["value"]
	if !present {
		t.Fatal("value key must be present (explicit null), not absent")
	}
	if value != nil {
		t.Errorf("value must be null for a missing observation, got %v", value)
	}
	available, ok := missing["available"]
	if !ok || available != false {
		t.Errorf("available must be false for a missing observation, got %v (present=%v)", available, ok)
	}

	// A real value still round-trips with available:true and the same value key.
	present2 := econResultToMap(search.EconResult{
		Source:   "fred",
		SeriesID: "UNRATE",
		Date:     "2025-09-01",
		Value:    4.4,
		HasValue: true,
	}, "observations")
	if present2["value"] != 4.4 {
		t.Errorf("a real value must still pass through exactly, got %v", present2["value"])
	}
	if present2["available"] != true {
		t.Errorf("available must be true for a real value, got %v", present2["available"])
	}
}

// TestEconSearchFREDMissingValueSentinel is an end-to-end regression test for
// #505: a realistic FRED /series/observations response containing the "."
// missing-value sentinel (e.g. the Oct 2025 government-shutdown delay to BLS
// releases) must round-trip through econ_search as an explicit `value: null`
// + `available: false` observation — never a dropped/absent `value` key.
func TestEconSearchFREDMissingValueSentinel(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A realistic FRED observations payload: two normal monthly readings
		// bracketing an observation withheld by the shutdown-delayed release.
		w.Write([]byte(`{
			"realtime_start": "2026-08-02",
			"realtime_end": "2026-08-02",
			"observation_start": "1600-01-01",
			"observation_end": "9999-12-31",
			"units": "lin",
			"output_type": 1,
			"file_type": "json",
			"order_by": "observation_date",
			"sort_order": "desc",
			"count": 3,
			"offset": 0,
			"limit": 3,
			"observations": [
				{"realtime_start": "2026-08-02", "realtime_end": "2026-08-02", "date": "2025-11-01", "value": "4.5"},
				{"realtime_start": "2026-08-02", "realtime_end": "2026-08-02", "date": "2025-10-01", "value": "."},
				{"realtime_start": "2026-08-02", "realtime_end": "2026-08-02", "date": "2025-09-01", "value": "4.4"}
			]
		}`))
	}))
	t.Cleanup(srv.Close)

	fred := search.NewFREDProvider("test-key", search.Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	fred.SetBaseURL(srv.URL)

	deps := setupTestDeps()
	deps.EconProviders = map[string]search.EconProvider{"fred": fred}

	out, res := callTool(t, deps, "econ_search", map[string]any{"series_id": "UNRATE", "provider": "fred"})
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	results, ok := out["results"].([]any)
	if !ok || len(results) != 3 {
		t.Fatalf("want 3 observations, got %v", out["results"])
	}

	shutdownRow, ok := results[1].(map[string]any)
	if !ok || shutdownRow["date"] != "2025-10-01" {
		t.Fatalf("expected the withheld observation at index 1, got %v", results[1])
	}
	value, present := shutdownRow["value"]
	if !present {
		t.Fatal("the withheld observation must carry an explicit `value` key (null), not omit it")
	}
	if value != nil {
		t.Errorf("the withheld observation's value must be null, got %v", value)
	}
	if avail, ok := shutdownRow["available"]; !ok || avail != false {
		t.Errorf("the withheld observation must carry available:false, got %v (present=%v)", avail, ok)
	}

	normalRow, ok := results[0].(map[string]any)
	if !ok || normalRow["date"] != "2025-11-01" || normalRow["value"] != 4.5 {
		t.Errorf("a normal observation must still pass its numeric value through exactly, got %v", results[0])
	}
	if avail, ok := normalRow["available"]; !ok || avail != true {
		t.Errorf("a normal observation must carry available:true, got %v (present=%v)", avail, ok)
	}
}

// TestEconSearchEurostatTruncationWarning is an end-to-end regression test for
// #536: a Eurostat observations() call against a 3-series (demographic
// breakdown) cube with num_results below the full row count must surface a
// top-level `truncationWarning` on econ_search's response — not silently keep
// only the alphabetically-first series with no signal.
func TestEconSearchEurostatTruncationWarning(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"label":"Unemployment rate",
			"id":["sex","time"],
			"size":[3,2],
			"value":{"0":3.2,"1":3.3,"2":2.9,"3":3.0,"4":3.1,"5":3.2},
			"dimension":{
				"sex":{"category":{"index":{"F":0,"M":1,"T":2},"label":{"F":"Females","M":"Males","T":"Total"}}},
				"time":{"category":{"index":{"2024-01":0,"2024-02":1},"label":{"2024-01":"2024-01","2024-02":"2024-02"}}}
			}
		}`))
	}))
	t.Cleanup(srv.Close)

	eurostat := search.NewEurostatProvider(search.Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	eurostat.SetBaseURLs(srv.URL+"/data", srv.URL+"/toc")

	deps := setupTestDeps()
	deps.EconProviders = map[string]search.EconProvider{"eurostat": eurostat}

	out, res := callTool(t, deps, "econ_search", map[string]any{"series_id": "une_rt_m", "provider": "eurostat", "num_results": 2})
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}

	warning, ok := out["truncationWarning"].(string)
	if !ok || warning == "" {
		t.Fatalf("expected a top-level truncationWarning, got %v", out["truncationWarning"])
	}
	if !strings.Contains(warning, "3 series") {
		t.Errorf("truncationWarning should name the total distinct-series count, got: %q", warning)
	}

	results, ok := out["results"].([]any)
	if !ok || len(results) != 2 {
		t.Fatalf("want 2 rows (respecting num_results), got %v", out["results"])
	}
}

// TestEconSearchNoTruncationWarningWhenSingleSeries guards the fail-open half
// of #536: a single-series Eurostat observation set truncated below its full
// row count (the ordinary "give me the latest N" case) must not carry
// truncationWarning at all.
func TestEconSearchNoTruncationWarningWhenSingleSeries(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"label":"Unemployment rate",
			"id":["geo","time"],
			"size":[1,5],
			"value":{"0":3.0,"1":3.1,"2":3.2,"3":3.3,"4":3.4},
			"dimension":{
				"geo":{"category":{"index":{"DE":0},"label":{"DE":"Germany"}}},
				"time":{"category":{"index":{"2024-01":0,"2024-02":1,"2024-03":2,"2024-04":3,"2024-05":4},"label":{}}}
			}
		}`))
	}))
	t.Cleanup(srv.Close)

	eurostat := search.NewEurostatProvider(search.Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	eurostat.SetBaseURLs(srv.URL+"/data", srv.URL+"/toc")

	deps := setupTestDeps()
	deps.EconProviders = map[string]search.EconProvider{"eurostat": eurostat}

	out, res := callTool(t, deps, "econ_search", map[string]any{"series_id": "une_rt_m", "provider": "eurostat", "country": "DE", "num_results": 2})
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}
	if _, present := out["truncationWarning"]; present {
		t.Errorf("single-series truncation must not carry truncationWarning, got %v", out["truncationWarning"])
	}
}
