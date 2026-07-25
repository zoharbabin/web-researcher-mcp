package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newCrtShTestServer(t *testing.T, handler http.HandlerFunc) *CrtShResolver {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	r := NewCrtShResolver(Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	r.SetBaseURL(srv.URL)
	return r
}

func TestCrtSh_ParsesDedupesAndSorts(t *testing.T) {
	var gotQuery string
	r := newCrtShTestServer(t, func(w http.ResponseWriter, req *http.Request) {
		gotQuery = req.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"issuer_name":"Let's Encrypt","name_value":"acme.com\nwww.acme.com","not_before":"2026-01-01","not_after":"2027-01-01","entry_timestamp":"2026-01-01T00:00:00"},
			{"issuer_name":"Let's Encrypt","name_value":"WWW.ACME.COM","not_before":"2026-01-01","not_after":"2027-01-01","entry_timestamp":"2026-01-01T00:00:00"},
			{"issuer_name":"DigiCert","name_value":"api.acme.com","not_before":"2025-06-01","not_after":"2026-06-01","entry_timestamp":"2025-06-01T00:00:00"}
		]`))
	})
	entries, err := r.Lookup(context.Background(), "acme.com", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// www.acme.com from row 1 and row 2 share the same (name, not_before) key, so
	// the duplicate is dropped -> 3 distinct entries, not 4.
	if len(entries) != 3 {
		t.Fatalf("want 3 deduplicated entries, got %d: %+v", len(entries), entries)
	}
	// Sorted by LoggedAt descending — the 2026 entries come before the 2025 one.
	if entries[len(entries)-1].Domain != "api.acme.com" {
		t.Errorf("oldest entry should sort last, got %+v", entries[len(entries)-1])
	}
	if !strings.Contains(gotQuery, "q=%25.acme.com") {
		t.Errorf("query should search for %%.<domain>, got %q", gotQuery)
	}
}

func TestCrtSh_MaxResultsTruncates(t *testing.T) {
	r := newCrtShTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"name_value":"a.acme.com","not_before":"1","entry_timestamp":"3"},
			{"name_value":"b.acme.com","not_before":"2","entry_timestamp":"2"},
			{"name_value":"c.acme.com","not_before":"3","entry_timestamp":"1"}
		]`))
	})
	entries, err := r.Lookup(context.Background(), "acme.com", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("maxResults=2 should cap output, got %d", len(entries))
	}
}

func TestCrtSh_EmptyBodyIsZeroEntriesNotError(t *testing.T) {
	r := newCrtShTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		// crt.sh returns a genuinely empty body for a domain with no logged certs.
	})
	entries, err := r.Lookup(context.Background(), "nocert.example", 0)
	if err != nil {
		t.Fatalf("empty body must not be an error, got %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("want zero entries, got %d", len(entries))
	}
}

func TestCrtSh_RateLimitClassified(t *testing.T) {
	r := newCrtShTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
	})
	_, err := r.Lookup(context.Background(), "acme.com", 0)
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("429 must produce an error containing 'rate limited', got %v", err)
	}
}

func TestCrtSh_ServerErrorPropagates(t *testing.T) {
	r := newCrtShTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte("bad gateway"))
	})
	_, err := r.Lookup(context.Background(), "acme.com", 0)
	if err == nil {
		t.Error("5xx should propagate an error")
	}
}

func TestCrtSh_InvalidDomainRejectedWithoutRequest(t *testing.T) {
	called := false
	r := newCrtShTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
	})
	_, err := r.Lookup(context.Background(), "not a domain; drop table", 0)
	if err == nil {
		t.Fatal("invalid domain should be rejected")
	}
	if called {
		t.Error("invalid domain must never reach the HTTP client")
	}
}

func TestCrtSh_Interface(t *testing.T) {
	var _ CTLogResolver = (*CrtShResolver)(nil)
}
