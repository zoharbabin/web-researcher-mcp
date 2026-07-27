package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// newWikidataTestResolver wires a resolver against two independent httptest
// servers (search API + SPARQL endpoint), mirroring the two real Wikidata
// hosts. Either handler may be nil to assert it's never called.
func newWikidataTestResolver(t *testing.T, searchHandler, sparqlHandler http.HandlerFunc) *WikidataOwnershipResolver {
	t.Helper()
	r := NewWikidataOwnershipResolver(Deps{
		HTTPClient: http.DefaultClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	var searchURL, sparqlURL string
	if searchHandler != nil {
		srv := httptest.NewServer(searchHandler)
		t.Cleanup(srv.Close)
		searchURL = srv.URL
	} else {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			t.Fatalf("unexpected call to search API: %s", req.URL.String())
		}))
		t.Cleanup(srv.Close)
		searchURL = srv.URL
	}
	if sparqlHandler != nil {
		srv := httptest.NewServer(sparqlHandler)
		t.Cleanup(srv.Close)
		sparqlURL = srv.URL
	} else {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			t.Fatalf("unexpected call to SPARQL endpoint: %s", req.URL.String())
		}))
		t.Cleanup(srv.Close)
		sparqlURL = srv.URL
	}
	r.SetBaseURLs(searchURL, sparqlURL)
	return r
}

func TestWikidataOwnershipResolver_EntityFound(t *testing.T) {
	r := newWikidataTestResolver(t,
		func(w http.ResponseWriter, req *http.Request) {
			_, _ = w.Write([]byte(`{"search":[{"id":"Q123"}]}`))
		},
		func(w http.ResponseWriter, req *http.Request) {
			_, _ = w.Write([]byte(`{"results":{"bindings":[{"owner":{"value":"http://www.wikidata.org/entity/Q8"},"ownerLabel":{"value":"Adobe Inc."}}]}}`))
		},
	)
	result, found, err := r.Resolve(context.Background(), "marketo")
	if err != nil || !found {
		t.Fatalf("expected found, no error; got found=%v err=%v", found, err)
	}
	if result.OwnerLabel != "Adobe Inc." || result.OwnerQID != "Q8" || result.EntityQID != "Q123" {
		t.Errorf("unexpected result: %+v", result)
	}
}

func TestWikidataOwnershipResolver_NoEntity(t *testing.T) {
	r := newWikidataTestResolver(t,
		func(w http.ResponseWriter, req *http.Request) {
			_, _ = w.Write([]byte(`{"search":[]}`))
		},
		nil, // SPARQL must never be called
	)
	result, found, err := r.Resolve(context.Background(), "notarealbrand")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if found || result != nil {
		t.Errorf("expected found=false, nil result; got found=%v result=%+v", found, result)
	}
}

func TestWikidataOwnershipResolver_NoParent(t *testing.T) {
	r := newWikidataTestResolver(t,
		func(w http.ResponseWriter, req *http.Request) {
			_, _ = w.Write([]byte(`{"search":[{"id":"Q123"}]}`))
		},
		func(w http.ResponseWriter, req *http.Request) {
			_, _ = w.Write([]byte(`{"results":{"bindings":[]}}`))
		},
	)
	result, found, err := r.Resolve(context.Background(), "independentco")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if found || result != nil {
		t.Errorf("expected found=false, nil result; got found=%v result=%+v", found, result)
	}
}

func TestWikidataOwnershipResolver_SearchAPIError(t *testing.T) {
	r := newWikidataTestResolver(t,
		func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(500)
		},
		nil,
	)
	_, found, err := r.Resolve(context.Background(), "somebrand")
	if err == nil {
		t.Error("expected an error on search API 500")
	}
	if found {
		t.Error("expected found=false on error")
	}
}

func TestWikidataOwnershipResolver_SPARQLRateLimit(t *testing.T) {
	r := newWikidataTestResolver(t,
		func(w http.ResponseWriter, req *http.Request) {
			_, _ = w.Write([]byte(`{"search":[{"id":"Q123"}]}`))
		},
		func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(429)
		},
	)
	_, found, err := r.Resolve(context.Background(), "somebrand")
	if err == nil {
		t.Error("expected an error on SPARQL 429")
	}
	if found {
		t.Error("expected found=false on error")
	}
}

func TestWikidataOwnershipResolver_BlankToken(t *testing.T) {
	r := newWikidataTestResolver(t, nil, nil)
	result, found, err := r.Resolve(context.Background(), "")
	if err != nil || found || result != nil {
		t.Errorf("expected no-op for blank token; got result=%+v found=%v err=%v", result, found, err)
	}
	r2 := newWikidataTestResolver(t, nil, nil)
	result2, found2, err2 := r2.Resolve(context.Background(), "   ")
	if err2 != nil || found2 || result2 != nil {
		t.Errorf("expected no-op for whitespace-only token; got result=%+v found=%v err=%v", result2, found2, err2)
	}
}

func TestWikidataOwnershipResolverName(t *testing.T) {
	r := NewWikidataOwnershipResolver(Deps{HTTPClient: http.DefaultClient, Breaker: circuit.New(circuit.Config{})})
	if r.Name() != "wikidata-ownership" {
		t.Errorf("unexpected name: %s", r.Name())
	}
}
