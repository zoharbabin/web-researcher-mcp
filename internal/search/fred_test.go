package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newFREDTestProvider(t *testing.T, handler http.HandlerFunc) *FREDProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewFREDProvider("test-key", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	p.SetBaseURL(srv.URL)
	return p
}

func TestFREDRequiresKey(t *testing.T) {
	if p := NewEconProviderByName("fred", EconProviderConfig{}, Deps{}); p != nil {
		t.Error("fred must be nil without an API key")
	}
	if p := NewEconProviderByName("fred", EconProviderConfig{FREDAPIKey: "k"}, Deps{}); p == nil {
		t.Error("fred should construct with a key")
	}
}

func TestFREDSeriesSearch(t *testing.T) {
	p := newFREDTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/series/search") {
			t.Errorf("keyword query should hit series search, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("api_key") != "test-key" {
			t.Error("api_key must be sent")
		}
		w.Write([]byte(`{"seriess":[{"id":"UNRATE","title":"Unemployment Rate","units":"Percent","frequency":"Monthly","last_updated":"2024-01-01","notes":"n"}]}`))
	})
	res, err := p.Econ(context.Background(), EconSearchParams{Query: "unemployment", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 || res[0].SeriesID != "UNRATE" || res[0].Units != "Percent" {
		t.Fatalf("unexpected series mapping: %+v", res)
	}
}

func TestFREDObservations(t *testing.T) {
	p := newFREDTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/series/observations") {
			t.Errorf("series_id should hit observations, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("series_id") != "GDP" {
			t.Error("series_id must be passed")
		}
		// One real value, one missing (".") which must be dropped from value.
		w.Write([]byte(`{"observations":[{"date":"2024-01-01","value":"27000.5"},{"date":"2024-04-01","value":"."}]}`))
	})
	res, err := p.Econ(context.Background(), EconSearchParams{SeriesID: "GDP", NumResults: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 observations, got %d", len(res))
	}
	if !res[0].HasValue || res[0].Value != 27000.5 {
		t.Errorf("first observation should pass value through verbatim: %+v", res[0])
	}
	if res[1].HasValue {
		t.Errorf("missing observation (.) must have no value: %+v", res[1])
	}
}

func TestFREDZeroValuePreserved(t *testing.T) {
	p := newFREDTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"observations":[{"date":"2024-01-01","value":"0"}]}`))
	})
	res, _ := p.Econ(context.Background(), EconSearchParams{SeriesID: "X", NumResults: 1})
	if len(res) != 1 || !res[0].HasValue || res[0].Value != 0 {
		t.Errorf("a real 0.0 must be preserved (HasValue=true): %+v", res)
	}
}

func TestFREDKeyRejected(t *testing.T) {
	p := newFREDTestProvider(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400) })
	_, err := p.Econ(context.Background(), EconSearchParams{SeriesID: "GDP"})
	if err == nil || !strings.Contains(err.Error(), "FRED_API_KEY") {
		t.Errorf("400 should map to a key/param error, got %v", err)
	}
}

func TestFREDInterface(t *testing.T) {
	var _ EconProvider = (*FREDProvider)(nil)
}
