package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newWorldBankTestProvider(t *testing.T, handler http.HandlerFunc) *WorldBankProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewWorldBankProvider(Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	p.SetBaseURL(srv.URL)
	return p
}

func TestWorldBankKeyless(t *testing.T) {
	// World Bank needs no key — it must always construct.
	if p := NewEconProviderByName("worldbank", EconProviderConfig{}, Deps{}); p == nil {
		t.Error("worldbank should construct without any key")
	}
}

func TestWorldBankObservations(t *testing.T) {
	p := newWorldBankTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/country/US/indicator/NY.GDP.MKTP.CD") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("format") != "json" {
			t.Error("format=json must be sent (XML is the API default)")
		}
		if r.URL.Query().Get("date") != "2018:2020" {
			t.Errorf("date range = %q, want 2018:2020", r.URL.Query().Get("date"))
		}
		// [0]=metadata, [1]=observations (newest-first), one real + one null value.
		w.Write([]byte(`[{"page":1,"pages":1,"per_page":10,"total":3},[` +
			`{"indicator":{"id":"NY.GDP.MKTP.CD","value":"GDP (current US$)"},"country":{"id":"US","value":"United States"},"countryiso3code":"USA","date":"2020","value":21060473613000,"unit":""},` +
			`{"indicator":{"id":"NY.GDP.MKTP.CD","value":"GDP (current US$)"},"country":{"id":"US","value":"United States"},"countryiso3code":"USA","date":"2019","value":null,"unit":""}` +
			`]]`))
	})
	res, err := p.Econ(context.Background(), EconSearchParams{SeriesID: "NY.GDP.MKTP.CD", Country: "US", DateFrom: "2018", DateTo: "2020", NumResults: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 observations, got %d", len(res))
	}
	if !res[0].HasValue || res[0].Value != 21060473613000 {
		t.Errorf("first observation value should pass through verbatim: %+v", res[0])
	}
	if res[1].HasValue {
		t.Errorf("null observation must have no value: %+v", res[1])
	}
	if res[0].Title != "GDP (current US$)" || res[0].Source != "worldbank" {
		t.Errorf("unexpected mapping: %+v", res[0])
	}
}

func TestWorldBankDefaultsToWorld(t *testing.T) {
	var gotPath string
	p := newWorldBankTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`[{"page":1,"pages":1,"per_page":10,"total":1},[{"indicator":{"value":"X"},"date":"2022","value":1.5}]]`))
	})
	_, err := p.Econ(context.Background(), EconSearchParams{SeriesID: "SP.POP.TOTL"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "/country/WLD/indicator/SP.POP.TOTL") {
		t.Errorf("no country should default to WLD (World): %s", gotPath)
	}
}

func TestWorldBankZeroValuePreserved(t *testing.T) {
	p := newWorldBankTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"page":1,"pages":1,"per_page":1,"total":1},[{"indicator":{"value":"X"},"date":"2022","value":0}]]`))
	})
	res, _ := p.Econ(context.Background(), EconSearchParams{SeriesID: "X", Country: "US", NumResults: 1})
	if len(res) != 1 || !res[0].HasValue || res[0].Value != 0 {
		t.Errorf("a real 0.0 must be preserved (HasValue=true): %+v", res)
	}
}

func TestWorldBankIndicatorSearch(t *testing.T) {
	p := newWorldBankTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/indicator") {
			t.Errorf("keyword query should hit /indicator, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("source") != "2" {
			t.Error("indicator list should filter to source=2 (WDI)")
		}
		w.Write([]byte(`[{"page":1,"pages":1,"per_page":2,"total":2},[` +
			`{"id":"NY.GDP.MKTP.CD","name":"GDP (current US$)","unit":"","sourceNote":"Gross domestic product...","source":{"value":"World Development Indicators"}},` +
			`{"id":"SP.POP.TOTL","name":"Population, total","unit":"","sourceNote":"Total population...","source":{"value":"World Development Indicators"}}` +
			`]]`))
	})
	res, err := p.Econ(context.Background(), EconSearchParams{Query: "gdp", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Client-side name filter: only "GDP (current US$)" matches "gdp".
	if len(res) != 1 || res[0].SeriesID != "NY.GDP.MKTP.CD" {
		t.Fatalf("client-side name filter failed: %+v", res)
	}
}

func TestWorldBankAPIErrorEnvelope(t *testing.T) {
	// Bad indicator → HTTP 200 with a {"message":[…]} envelope in element [0].
	p := newWorldBankTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"message":[{"id":"120","key":"Invalid value","value":"The provided parameter value is not valid"}]}]`))
	})
	_, err := p.Econ(context.Background(), EconSearchParams{SeriesID: "BOGUS", Country: "US"})
	if err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Errorf("HTTP-200 error envelope should surface as an error, got %v", err)
	}
}

func TestWorldBankNoDataIsEmpty(t *testing.T) {
	// Valid query, no rows for the range → [1] is null. Must be empty, not an error.
	p := newWorldBankTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"page":1,"pages":0,"per_page":10,"total":0},null]`))
	})
	res, err := p.Econ(context.Background(), EconSearchParams{SeriesID: "X", Country: "US"})
	if err != nil {
		t.Errorf("no-data should not error: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("no-data should be empty, got %+v", res)
	}
}

func TestWorldBankInterface(t *testing.T) {
	var _ EconProvider = (*WorldBankProvider)(nil)
}

func TestWorldBankDateRange(t *testing.T) {
	cases := []struct{ from, to, want string }{
		{"2018", "2020", "2018:2020"},
		{"2018-01-01", "2020-12-31", "2018:2020"},
		{"2018", "", "2018:2018"},
		{"", "2020", "2020:2020"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := worldBankDateRange(c.from, c.to); got != c.want {
			t.Errorf("worldBankDateRange(%q,%q) = %q, want %q", c.from, c.to, got, c.want)
		}
	}
}
