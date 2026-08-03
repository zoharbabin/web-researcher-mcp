package tools

import (
	"net/http"
	"net/http/httptest"
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
