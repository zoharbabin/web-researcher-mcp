package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newOECDTestProvider(t *testing.T, handler http.HandlerFunc) *OECDProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewOECDProvider(Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	p.SetBaseURL(srv.URL)
	return p
}

func TestOECDKeyless(t *testing.T) {
	if p := NewEconProviderByName("oecd", EconProviderConfig{}, Deps{}); p == nil {
		t.Error("oecd should construct without any key")
	}
}

// TestOECDObservations decodes a real-shaped SDMX-JSON 2.0 cube. Observations are
// keyed by the TIME index; the time label comes from structures.dimensions.
// observation[0].values (by index, NOT date order — index 0 here is the MIDDLE
// quarter to prove index-not-order lookup).
func TestOECDObservations(t *testing.T) {
	p := newOECDTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/data/") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("dimensionAtObservation") != "TIME_PERIOD" {
			t.Error("dimensionAtObservation=TIME_PERIOD must be sent")
		}
		if r.URL.Query().Get("c[REF_AREA]") != "USA" {
			t.Errorf("REF_AREA filter = %q, want USA", r.URL.Query().Get("c[REF_AREA]"))
		}
		if !strings.Contains(r.Header.Get("Accept"), "vnd.sdmx.data+json") {
			t.Errorf("Accept = %q, want sdmx data json", r.Header.Get("Accept"))
		}
		w.Write([]byte(`{
			"data":{
				"dataSets":[{"series":{"0:0":{"observations":{"0":[100.5],"1":[99.0],"2":[101.2]}}}}],
				"structures":[{"dimensions":{
					"series":[
						{"id":"REF_AREA","name":"Reference area","values":[{"id":"USA","name":"United States"}]},
						{"id":"UNIT_MEASURE","name":"Unit","values":[{"id":"XDC","name":"National currency"}]}
					],
					"observation":[{"id":"TIME_PERIOD","name":"Time","values":[
						{"id":"2023-Q2","name":"2023-Q2"},
						{"id":"2023-Q1","name":"2023-Q1"},
						{"id":"2023-Q3","name":"2023-Q3"}
					]}]
				}}]
			}
		}`))
	})
	res, err := p.Econ(context.Background(), EconSearchParams{
		SeriesID: "OECD.SDD.NAD,DSD_NAMAIN1@DF_QNA,1.1", Country: "USA",
		DateFrom: "2023", DateTo: "2023", NumResults: 50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("want 3 observations, got %d", len(res))
	}
	// Index 0 → "2023-Q2" (NOT the chronologically-first) proves index-based lookup.
	// After sort by (title,date), the earliest period is first.
	byDate := map[string]float64{}
	for _, o := range res {
		byDate[o.Date] = o.Value
	}
	if byDate["2023-Q2"] != 100.5 || byDate["2023-Q1"] != 99.0 || byDate["2023-Q3"] != 101.2 {
		t.Errorf("observation index→period mapping wrong: %+v", byDate)
	}
	// Title composed from REF_AREA; units from UNIT_MEASURE.
	if !strings.Contains(res[0].Title, "United States") {
		t.Errorf("title should include REF_AREA label: %q", res[0].Title)
	}
	if res[0].Units != "National currency" {
		t.Errorf("units = %q, want National currency", res[0].Units)
	}
}

func TestOECDNullObservation(t *testing.T) {
	p := newOECDTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"data":{
			"dataSets":[{"series":{"0":{"observations":{"0":[null],"1":[5.5]}}}}],
			"structures":[{"dimensions":{
				"series":[{"id":"REF_AREA","values":[{"id":"USA","name":"USA"}]}],
				"observation":[{"id":"TIME_PERIOD","values":[{"id":"2020","name":"2020"},{"id":"2021","name":"2021"}]}]
			}}]
		}}`))
	})
	res, err := p.Econ(context.Background(), EconSearchParams{SeriesID: "A,B,1.0", NumResults: 10})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 obs, got %d", len(res))
	}
	for _, o := range res {
		if o.Date == "2020" && o.HasValue {
			t.Error("null observation must have HasValue=false")
		}
		if o.Date == "2021" && (!o.HasValue || o.Value != 5.5) {
			t.Errorf("2021 obs = %+v, want 5.5", o)
		}
	}
}

func TestOECDSearch(t *testing.T) {
	p := newOECDTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/dataflow/") {
			t.Errorf("search should hit /dataflow, got %s", r.URL.Path)
		}
		w.Write([]byte(`{"data":{"dataflows":[
			{"id":"DF_QNA","agencyID":"OECD.SDD.NAD","version":"1.1","name":"Quarterly National Accounts"},
			{"id":"DF_CPI","agencyID":"OECD.SDD.TPS","version":"1.0","name":"Consumer Price Index"}
		]}}`))
	})
	res, err := p.Econ(context.Background(), EconSearchParams{Query: "national accounts", NumResults: 10})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 match for 'national accounts', got %d", len(res))
	}
	if res[0].SeriesID != "OECD.SDD.NAD,DF_QNA,1.1" {
		t.Errorf("dataflow ref = %q, want OECD.SDD.NAD,DF_QNA,1.1", res[0].SeriesID)
	}
}

func TestOECDRefValidation(t *testing.T) {
	// A ref containing path/query metacharacters must be rejected BEFORE any
	// request is made (no URL reshaping).
	p := newOECDTestProvider(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("a malformed ref must not reach the API")
	})
	// Non-empty but malformed refs reach observations() and must be rejected before
	// any request. (An empty SeriesID is a dataflow SEARCH, not an observation ref.)
	for _, bad := range []string{
		"foo/all?key=evil",
		"a,b,1.0/../../etc",
		"a b,c,1",
		"agency,id,1#frag",
	} {
		if _, err := p.Econ(context.Background(), EconSearchParams{SeriesID: bad}); err == nil {
			t.Errorf("ref %q should be rejected", bad)
		}
	}
	// A well-formed ref (letters, digits, . _ - @ ,) passes validation.
	if !validOECDRef("OECD.SDD.NAD,DSD_NAMAIN1@DF_QNA,1.1") {
		t.Error("a real OECD dataflow ref should be valid")
	}
}

func TestOECDErrors(t *testing.T) {
	p := newOECDTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Could not find Dataflow"))
	})
	if _, err := p.Econ(context.Background(), EconSearchParams{SeriesID: "bad,flow,1.0"}); err == nil {
		t.Error("404 should surface an error")
	}
}
