package search

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newSerpBaseTestDeps() Deps {
	return Deps{
		HTTPClient: http.DefaultClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	}
}

// newSerpBaseTestServer returns a SerpBase provider wired to a test server
// that asserts the X-API-Key header and returns the given status+body.
func newSerpBaseTestServer(t *testing.T, handler http.HandlerFunc) (*SerpBaseProvider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewSerpBaseProvider("test-key", newSerpBaseTestDeps())
	p.SetBaseURL(srv.URL)
	return p, srv
}

func TestSerpBaseWebSearch(t *testing.T) {
	var gotBody map[string]any
	p, _ := newSerpBaseTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("want POST, got %s", r.Method)
		}
		if r.Header.Get("X-API-Key") != "test-key" {
			t.Errorf("missing/wrong X-API-Key header: %q", r.Header.Get("X-API-Key"))
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{
			"status": 0,
			"request_id": "req_abc",
			"organic": [
				{"rank": 1, "title": "First", "link": "https://a.example/x", "display_link": "a.example", "snippet": "first snippet"},
				{"rank": 2, "title": "Second", "link": "https://b.example/y", "display_link": "b.example", "snippet": "second snippet"}
			]
		}`))
	})

	results, err := p.Web(context.Background(), WebSearchParams{Query: "golang", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if gotBody["q"] != "golang" {
		t.Errorf("request body q = %v, want golang", gotBody["q"])
	}
	if gotBody["num"] != float64(5) {
		t.Errorf("request body num = %v, want 5", gotBody["num"])
	}
	if results[0].Title != "First" || results[0].URL != "https://a.example/x" || results[0].Snippet != "first snippet" {
		t.Errorf("unexpected first result: %+v", results[0])
	}
	if results[1].DisplayLink != "b.example" {
		t.Errorf("unexpected displayLink: %q", results[1].DisplayLink)
	}
}

func TestSerpBaseWebSearchCountryLanguage(t *testing.T) {
	var gotBody map[string]any
	p, _ := newSerpBaseTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{"status": 0, "organic": []}`))
	})

	_, err := p.Web(context.Background(), WebSearchParams{Query: "x", NumResults: 3, Country: "DE", Language: "de"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["gl"] != "DE" || gotBody["hl"] != "de" {
		t.Errorf("expected gl=DE hl=de, got gl=%v hl=%v", gotBody["gl"], gotBody["hl"])
	}
}

func TestSerpBaseWebSearchErrorEnvelope(t *testing.T) {
	// SerpBase returns HTTP 200 with a non-zero status for business errors.
	p, _ := newSerpBaseTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status": 1, "message": "invalid query"}`))
	})

	_, err := p.Web(context.Background(), WebSearchParams{Query: "x", NumResults: 5})
	if err == nil {
		t.Fatal("want error for non-zero status envelope, got nil")
	}
	if !strings.Contains(err.Error(), "status=1") {
		t.Errorf("error should surface envelope status, got %v", err)
	}
}

func TestSerpBaseRateLimit(t *testing.T) {
	p, _ := newSerpBaseTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})

	_, err := p.Web(context.Background(), WebSearchParams{Query: "x", NumResults: 5})
	if !errors.Is(err, circuit.ErrRateLimit) {
		t.Errorf("want circuit.ErrRateLimit, got %v", err)
	}
}

func TestSerpBaseImagesNewsNoop(t *testing.T) {
	p, _ := newSerpBaseTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("noop capability must not hit the network")
	})

	imgs, err := p.Images(context.Background(), ImageSearchParams{Query: "x"})
	if err != nil || imgs != nil {
		t.Errorf("Images should return (nil, nil), got (%v, %v)", imgs, err)
	}
	news, err := p.News(context.Background(), NewsSearchParams{Query: "x"})
	if err != nil || news != nil {
		t.Errorf("News should return (nil, nil), got (%v, %v)", news, err)
	}
}
