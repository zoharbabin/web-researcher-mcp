package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newCourtListenerTestProvider(t *testing.T, token string, handler http.HandlerFunc) *CourtListenerProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewCourtListenerProvider(token, Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	p.SetBaseURL(srv.URL)
	return p
}

func TestCourtListenerSearch(t *testing.T) {
	p := newCourtListenerTestProvider(t, "", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "o" {
			t.Errorf("must search opinions (type=o), got %s", r.URL.Query().Get("type"))
		}
		w.Write([]byte(`{"results":[{"caseName":"Miranda v. Arizona","citation":["384 U.S. 436"],"court":"Supreme Court","court_id":"scotus","dateFiled":"1966-06-13","docketNumber":"759","citeCount":25000,"absolute_url":"/opinion/107252/miranda-v-arizona/"}]}`))
	})
	res, err := p.Cases(context.Background(), CaseSearchParams{Query: "miranda", NumResults: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 case, got %d", len(res))
	}
	c := res[0]
	if c.CaseName != "Miranda v. Arizona" || c.Citation != "384 U.S. 436" || c.CourtID != "scotus" || c.CitationCount != 25000 {
		t.Errorf("unexpected mapping: %+v", c)
	}
	if c.URL != "https://www.courtlistener.com/opinion/107252/miranda-v-arizona/" {
		t.Errorf("URL not absolutized: %s", c.URL)
	}
}

func TestCourtListenerJurisdictionAndDates(t *testing.T) {
	var q url.Values
	p := newCourtListenerTestProvider(t, "", func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		w.Write([]byte(`{"results":[]}`))
	})
	_, _ = p.Cases(context.Background(), CaseSearchParams{Query: "x", Jurisdiction: "ca9", DateFrom: "2020-01-01", DateTo: "2024-12-31"})
	if q.Get("court") != "ca9" || q.Get("filed_after") != "2020-01-01" || q.Get("filed_before") != "2024-12-31" {
		t.Errorf("filters not mapped: %v", q)
	}
}

func TestCourtListenerSendsToken(t *testing.T) {
	var gotAuth string
	p := newCourtListenerTestProvider(t, "secret-token", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"results":[]}`))
	})
	_, _ = p.Cases(context.Background(), CaseSearchParams{Query: "x"})
	if gotAuth != "Token secret-token" {
		t.Errorf("token not sent as Authorization header, got %q", gotAuth)
	}
}

func TestCourtListenerKeylessConstructs(t *testing.T) {
	// Works without a token (always available).
	if p := NewCaseProviderByName("courtlistener", CaseProviderConfig{}, Deps{}); p == nil {
		t.Error("courtlistener should construct keyless")
	}
}

func TestCourtListenerRateLimit(t *testing.T) {
	p := newCourtListenerTestProvider(t, "", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) })
	_, err := p.Cases(context.Background(), CaseSearchParams{Query: "x"})
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("expected rate-limit error, got %v", err)
	}
}

func TestCourtListenerInterface(t *testing.T) {
	var _ CaseProvider = (*CourtListenerProvider)(nil)
}
