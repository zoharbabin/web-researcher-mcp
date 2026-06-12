package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newDOIRegistry(t *testing.T, handler http.HandlerFunc) *HandleDOIRegistry {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	r := NewHandleDOIRegistry(Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	r.SetBaseURL(srv.URL)
	return r
}

func TestDOIRegistry_Registered(t *testing.T) {
	t.Parallel()
	r := newDOIRegistry(t, func(w http.ResponseWriter, req *http.Request) {
		// The DOI's own slash must survive into the request path.
		if !strings.Contains(req.URL.Path, "10.48550/") {
			t.Errorf("path lost the DOI slash: %q", req.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"responseCode":1,"handle":"10.48550/arXiv.1706.03762"}`))
	})
	reg, err := r.IsRegistered(context.Background(), "10.48550/arXiv.1706.03762")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !reg {
		t.Fatal("want registered=true for a responseCode:1 handle")
	}
}

func TestDOIRegistry_NotRegistered404(t *testing.T) {
	t.Parallel()
	// The handle API returns HTTP 404 for an unregistered DOI — an authoritative
	// negative, not a transport error.
	r := newDOIRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"responseCode":100}`))
	})
	reg, err := r.IsRegistered(context.Background(), "10.1038/s41586-021-99999999-x")
	if err != nil {
		t.Fatalf("a 404 must be a clean negative, got err=%v", err)
	}
	if reg {
		t.Fatal("want registered=false for a 404 handle")
	}
}

func TestDOIRegistry_NotRegisteredCode100Body(t *testing.T) {
	t.Parallel()
	// Defense in depth: a 200 carrying responseCode!=1 must NOT read as registered
	// (so a caching proxy that rewrites the status can't produce a false positive).
	r := newDOIRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"responseCode":100}`))
	})
	reg, err := r.IsRegistered(context.Background(), "10.1038/nonexistent")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if reg {
		t.Fatal("want registered=false when responseCode!=1")
	}
}

func TestDOIRegistry_EmptyDOI(t *testing.T) {
	t.Parallel()
	r := newDOIRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("must not call the API for an empty DOI")
	})
	reg, err := r.IsRegistered(context.Background(), "")
	if err != nil || reg {
		t.Fatalf("empty DOI: want (false,nil), got (%v,%v)", reg, err)
	}
}

func TestDOIRegistry_TransportErrorIsUnknown(t *testing.T) {
	t.Parallel()
	// A 5xx must surface as err!=nil (existence unknown) — never a silent false,
	// which would mislabel a real DOI as fabricated.
	r := newDOIRegistry(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	reg, err := r.IsRegistered(context.Background(), "10.1038/171737a0")
	if err == nil {
		t.Fatal("a 5xx must return a non-nil error (existence unknown)")
	}
	if reg {
		t.Fatal("registered must be false alongside the error")
	}
}

func TestDOIRegistry_PrefixNormalized(t *testing.T) {
	t.Parallel()
	// A doi.org-prefixed input must be normalized to the bare DOI before the call.
	r := newDOIRegistry(t, func(w http.ResponseWriter, req *http.Request) {
		if strings.Contains(req.URL.Path, "doi.org") {
			t.Errorf("prefix not stripped: %q", req.URL.Path)
		}
		_, _ = w.Write([]byte(`{"responseCode":1}`))
	})
	reg, err := r.IsRegistered(context.Background(), "https://doi.org/10.1038/171737a0")
	if err != nil || !reg {
		t.Fatalf("want (true,nil), got (%v,%v)", reg, err)
	}
}
