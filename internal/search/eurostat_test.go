package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newEurostatTestProvider(t *testing.T, handler http.HandlerFunc) *EurostatProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewEurostatProvider(Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	p.SetBaseURLs(srv.URL+"/data", srv.URL+"/toc")
	return p
}

func TestEurostatKeyless(t *testing.T) {
	if p := NewEconProviderByName("eurostat", EconProviderConfig{}, Deps{}); p == nil {
		t.Error("eurostat should construct without any key")
	}
}

// TestEurostatObservations decodes a real-shaped JSON-stat 2.0 cube: geo=DE,FR ×
// 3 months. The value map is sparse + row-major (time fastest), so the decoder
// must map each flat key back to its period via the time category index.
func TestEurostatObservations(t *testing.T) {
	p := newEurostatTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/data/une_rt_m") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("format") != "JSON" {
			t.Error("format=JSON must be sent")
		}
		if r.URL.Query().Get("geo") != "DE" {
			t.Errorf("geo = %q, want DE", r.URL.Query().Get("geo"))
		}
		// size=[1(unit),1(geo... actually 1 geo here),3(time)] → keys 0,1,2 are DE Jan-Mar.
		w.Write([]byte(`{
			"label":"Unemployment by sex and age - monthly data",
			"updated":"2026-06-09T23:00:00+0200",
			"id":["unit","geo","time"],
			"size":[1,1,3],
			"value":{"0":3.2,"1":3.3,"2":3.1},
			"status":{"2":"p"},
			"dimension":{
				"unit":{"category":{"label":{"PC_ACT":"Percentage of population in the labour force"}}},
				"geo":{"category":{"index":{"DE":0},"label":{"DE":"Germany"}}},
				"time":{"category":{"index":{"2024-01":0,"2024-02":1,"2024-03":2},"label":{"2024-01":"2024-01","2024-02":"2024-02","2024-03":"2024-03"}}}
			}
		}`))
	})
	res, err := p.Econ(context.Background(), EconSearchParams{SeriesID: "une_rt_m", Country: "DE", DateFrom: "2024-01", DateTo: "2024-03", NumResults: 50})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("want 3 observations, got %d", len(res))
	}
	// Sorted by period ascending; values mapped to the right months.
	if res[0].Date != "2024-01" || res[0].Value != 3.2 || !res[0].HasValue {
		t.Errorf("obs[0] = %+v, want 2024-01=3.2", res[0])
	}
	if res[2].Date != "2024-03" || res[2].Value != 3.1 {
		t.Errorf("obs[2] = %+v, want 2024-03=3.1", res[2])
	}
	if res[0].Units != "Percentage of population in the labour force" {
		t.Errorf("units not decoded: %q", res[0].Units)
	}
	// The provisional flag on the last obs is surfaced as provenance.
	if !strings.Contains(res[2].Notes, "p") {
		t.Errorf("status flag should be surfaced: %q", res[2].Notes)
	}
}

func TestEurostatMultiDimDecode(t *testing.T) {
	// geo=DE,FR × 3 months, time fastest: keys 0-2 = DE Jan-Mar, 3-5 = FR Jan-Mar.
	p := newEurostatTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{
			"label":"GDP",
			"id":["geo","time"],
			"size":[2,3],
			"value":{"0":1.0,"1":1.1,"2":1.2,"3":2.0,"4":2.1,"5":2.2},
			"dimension":{
				"geo":{"category":{"index":{"DE":0,"FR":1},"label":{"DE":"Germany","FR":"France"}}},
				"time":{"category":{"index":{"2024-01":0,"2024-02":1,"2024-03":2},"label":{}}}
			}
		}`))
	})
	res, err := p.Econ(context.Background(), EconSearchParams{SeriesID: "nama", NumResults: 50})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(res) != 6 {
		t.Fatalf("want 6 obs, got %d", len(res))
	}
	// Each (geo,time) cell must decode to the RIGHT value at the RIGHT period AND
	// carry its geo in the series label so DE and FR don't collapse — the bug this
	// guards against (a varying geo dimension must disambiguate the series).
	want := map[string]float64{ // "<geoLabel>|<period>" → value
		"Germany|2024-01": 1.0, "Germany|2024-02": 1.1, "Germany|2024-03": 1.2,
		"France|2024-01": 2.0, "France|2024-02": 2.1, "France|2024-03": 2.2,
	}
	for _, o := range res {
		// The series label is the dataset title + the varying-dimension labels.
		geo := "Germany"
		if strings.Contains(o.Title, "France") {
			geo = "France"
		}
		key := geo + "|" + o.Date
		if w, ok := want[key]; !ok || o.Value != w {
			t.Errorf("cell %q = %v (want %v); title=%q", key, o.Value, w, o.Title)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Errorf("missing/duplicated cells: %v", want)
	}
	// Series must be grouped + period-ascending: France rows contiguous, ascending.
	var franceDates []string
	for _, o := range res {
		if strings.Contains(o.Title, "France") {
			franceDates = append(franceDates, o.Date)
		}
	}
	if len(franceDates) != 3 || franceDates[0] != "2024-01" || franceDates[2] != "2024-03" {
		t.Errorf("France series not coherent/ascending: %v", franceDates)
	}
}

func TestEurostatSearch(t *testing.T) {
	p := newEurostatTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/toc") {
			t.Errorf("search should hit the TOC, got %s", r.URL.Path)
		}
		// TSV: header + one folder (skipped) + two datasets.
		w.Write([]byte("\"title\"\t\"code\"\t\"type\"\t\"last update\"\n" +
			"\"Population\"\t\"demo\"\t\"folder\"\t\"\"\n" +
			"\"Unemployment rate - monthly data\"\t\"une_rt_m\"\t\"dataset\"\t\"\"\n" +
			"\"Harmonised unemployment\"\t\"ei_lmhr_m\"\t\"dataset\"\t\"\"\n"))
	})
	res, err := p.Econ(context.Background(), EconSearchParams{Query: "unemployment", NumResults: 10})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 unemployment datasets, got %d (%+v)", len(res), res)
	}
	if res[0].SeriesID != "une_rt_m" {
		t.Errorf("first match = %q, want une_rt_m", res[0].SeriesID)
	}
}

// TestEurostatSearchMultiWord verifies that multi-word queries use AND-matching
// so "quarterly GDP" matches a dataset whose title contains both words even when
// they are not adjacent ("GDP and main components - quarterly").
func TestEurostatSearchMultiWord(t *testing.T) {
	t.Parallel()
	tocBody := "\"title\"\t\"code\"\t\"type\"\t\"last update\"\n" +
		"\"GDP and main components - quarterly\"\t\"namq_10_gdp\"\t\"dataset\"\t\"\"\n" +
		"\"Annual national accounts\"\t\"nama_10_gdp\"\t\"dataset\"\t\"\"\n" +
		"\"Consumer Price Index - monthly\"\t\"prc_hicp_midx\"\t\"dataset\"\t\"\"\n"

	p := newEurostatTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(tocBody))
	})

	// "quarterly GDP" → both words appear in the first title, but not contiguously.
	res, err := p.Econ(context.Background(), EconSearchParams{Query: "quarterly GDP", NumResults: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 match for 'quarterly GDP', got %d: %+v", len(res), res)
	}
	if res[0].SeriesID != "namq_10_gdp" {
		t.Errorf("matched wrong dataset: %q", res[0].SeriesID)
	}

	// A single-word query still uses exact substring matching.
	res2, err := p.Econ(context.Background(), EconSearchParams{Query: "annual", NumResults: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res2) != 1 || !strings.Contains(strings.ToLower(res2[0].Title), "annual") {
		t.Errorf("single-word match wrong: %+v", res2)
	}

	// A word absent from ALL titles should return no results.
	res3, err := p.Econ(context.Background(), EconSearchParams{Query: "quarterly nonexistent", NumResults: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res3) != 0 {
		t.Errorf("partial AND should yield 0 results when one word is absent, got %d", len(res3))
	}
}

func TestEurostatErrors(t *testing.T) {
	// 404 unknown dataset.
	p := newEurostatTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":[{"status":404,"label":"not available"}]}`))
	})
	if _, err := p.Econ(context.Background(), EconSearchParams{SeriesID: "nope"}); err == nil {
		t.Error("404 should surface an error")
	}

	// 413 too-large.
	p2 := newEurostatTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	})
	if _, err := p2.Econ(context.Background(), EconSearchParams{SeriesID: "huge"}); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("413 should surface a too-large error, got %v", err)
	}
}

// TestEurostatMalformedCube guards the divide-by-zero path: a non-empty value map
// paired with a zero-sized dimension must return no observations, never panic.
func TestEurostatMalformedCube(t *testing.T) {
	for _, body := range []string{
		// timeSize == 0 with a value present.
		`{"label":"x","id":["time"],"size":[0],"value":{"0":1.0},"dimension":{"time":{"category":{"index":{},"label":{}}}}}`,
		// timeStride == 0 (trailing dim size 0) with a value present.
		`{"label":"x","id":["time","geo"],"size":[3,0],"value":{"0":1.0},"dimension":{"time":{"category":{"index":{"2024":0},"label":{}}},"geo":{"category":{"index":{},"label":{}}}}}`,
	} {
		p := newEurostatTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(body))
		})
		res, err := p.Econ(context.Background(), EconSearchParams{SeriesID: "x", NumResults: 10})
		if err != nil {
			t.Fatalf("malformed cube should not error: %v", err)
		}
		if len(res) != 0 {
			t.Errorf("malformed cube should yield no observations, got %d", len(res))
		}
	}
}

func TestEurostatEmptyRange(t *testing.T) {
	// Valid query, no data → empty value map → no observations, no error.
	p := newEurostatTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"label":"x","id":["time"],"size":[0],"value":{},"dimension":{"time":{"category":{"index":{},"label":{}}}}}`))
	})
	res, err := p.Econ(context.Background(), EconSearchParams{SeriesID: "x", NumResults: 10})
	if err != nil {
		t.Fatalf("empty range should not error: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("empty range should yield no observations, got %d", len(res))
	}
}
