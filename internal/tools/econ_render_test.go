package tools

import (
	"testing"

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
