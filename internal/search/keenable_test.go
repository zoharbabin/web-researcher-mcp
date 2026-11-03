package search

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newKeenableTestProvider(t *testing.T, handler http.HandlerFunc) *KeenableProvider {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	client := &http.Client{Transport: &rewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	deps := Deps{HTTPClient: client, Breaker: circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60})}
	return NewKeenableProvider("kn-key", deps)
}

func TestKeenableWeb_RequestAndParsing(t *testing.T) {
	var gotKey, gotMethod, gotPath string
	var gotBody map[string]any
	p := newKeenableTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotMethod, gotPath = r.Header.Get("X-API-Key"), r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"results":[
			{"title":"A","url":"https://a.example/x","description":"desc","snippet":"s"},
			{"title":"B","url":"https://b.example/","snippet":"` + strings.Repeat("x", 900) + `"},
			{"title":"no url","description":"dropped"}]}`))
	})

	results, err := p.Web(context.Background(), WebSearchParams{Query: "golang", NumResults: 5})
	if err != nil {
		t.Fatalf("Web: %v", err)
	}
	if gotKey != "kn-key" || gotMethod != "POST" || gotPath != "/v1/search" {
		t.Fatalf("request = %s %s key=%q", gotMethod, gotPath, gotKey)
	}
	if gotBody["query"] != "golang" || gotBody["max_results"] != float64(5) {
		t.Fatalf("body = %v", gotBody)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if results[0].Snippet != "desc" || results[0].DisplayLink == "" {
		t.Errorf("first result = %+v", results[0])
	}
	if len([]rune(results[1].Snippet)) != keenableSnippetMaxRunes {
		t.Errorf("fallback snippet len = %d", len([]rune(results[1].Snippet)))
	}
}

func TestKeenable_RateLimitAndUnsupportedOps(t *testing.T) {
	p := newKeenableTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	if _, err := p.Web(context.Background(), WebSearchParams{Query: "q", NumResults: 3}); !errors.Is(err, circuit.ErrRateLimit) {
		t.Fatalf("want ErrRateLimit, got %v", err)
	}
	if r, err := p.Images(context.Background(), ImageSearchParams{}); r != nil || err != nil {
		t.Errorf("Images should be (nil, nil)")
	}
	if r, err := p.News(context.Background(), NewsSearchParams{}); r != nil || err != nil {
		t.Errorf("News should be (nil, nil)")
	}
}
