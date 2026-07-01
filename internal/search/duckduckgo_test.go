package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

const ddgSampleHTML = `<!DOCTYPE html>
<html>
<body>
<div id="links">
  <div class="result">
    <h2 class="result__title">
      <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2F&amp;rut=abc123">The Go Programming Language</a>
    </h2>
    <a class="result__snippet">Go is an open source programming language supported by Google.</a>
  </div>
  <div class="result">
    <h2 class="result__title">
      <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fen.wikipedia.org%2Fwiki%2FGo_(programming_language)&amp;rut=def456">Go (programming language) - Wikipedia</a>
    </h2>
    <a class="result__snippet">Go is a statically typed, compiled high-level general-purpose programming language.</a>
  </div>
  <div class="result">
    <h2 class="result__title">
      <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgithub.com%2Fgolang%2Fgo&amp;rut=ghi789">GitHub - golang/go</a>
    </h2>
    <a class="result__snippet">The Go programming language repository on GitHub.</a>
  </div>
</div>
</body>
</html>`

func TestDDGProvider_ParseResults(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") == "" {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(ddgSampleHTML))
	}))
	defer srv.Close()

	provider := NewDuckDuckGoProvider(Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)

	results, err := provider.Web(context.Background(), WebSearchParams{
		Query:      "golang",
		NumResults: 3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	if results[0].Title != "The Go Programming Language" {
		t.Errorf("unexpected title: %s", results[0].Title)
	}
	if results[0].URL != "https://go.dev/" {
		t.Errorf("unexpected URL: %s", results[0].URL)
	}
	if results[1].URL != "https://en.wikipedia.org/wiki/Go_(programming_language)" {
		t.Errorf("unexpected URL[1]: %s", results[1].URL)
	}
	if results[2].URL != "https://github.com/golang/go" {
		t.Errorf("unexpected URL[2]: %s", results[2].URL)
	}
	// #356: DuckDuckGo provider does not populate PublishedAt
	if results[0].PublishedAt != "" {
		t.Errorf("expected empty PublishedAt, got %q", results[0].PublishedAt)
	}
}

func TestDDGProvider_MaxResults(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(ddgSampleHTML))
	}))
	defer srv.Close()

	provider := NewDuckDuckGoProvider(Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)

	results, err := provider.Web(context.Background(), WebSearchParams{
		Query:      "test",
		NumResults: 2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (capped), got %d", len(results))
	}
}

func TestDDGProvider_RateLimit202(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(202)
	}))
	defer srv.Close()

	provider := NewDuckDuckGoProvider(Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)

	_, err := provider.Web(context.Background(), WebSearchParams{Query: "test"})
	if err == nil {
		t.Fatal("expected error for 202 rate limit")
	}
	if !isRateLimitError(err) {
		t.Errorf("expected rate limit detection, got: %v", err)
	}
}

func TestDDGProvider_RateLimit429(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
	}))
	defer srv.Close()

	provider := NewDuckDuckGoProvider(Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)

	_, err := provider.Web(context.Background(), WebSearchParams{Query: "test"})
	if err == nil {
		t.Fatal("expected error for 429")
	}
	if !isRateLimitError(err) {
		t.Errorf("expected rate limit detection, got: %v", err)
	}
}

func TestDDGProvider_ExtractURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2F&rut=abc", "https://go.dev/"},
		{"//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpath%3Fq%3Dtest", "https://example.com/path?q=test"},
		{"https://direct-link.com/page", "https://direct-link.com/page"},
		{"", ""},
		{"/relative/path", ""},
	}

	for _, tt := range tests {
		got := extractDDGURL(tt.input)
		if got != tt.want {
			t.Errorf("extractDDGURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDDGProvider_EmptyQuery(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("<html><body></body></html>"))
	}))
	defer srv.Close()

	provider := NewDuckDuckGoProvider(Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)

	results, err := provider.Web(context.Background(), WebSearchParams{Query: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty query, got %d", len(results))
	}
}

func TestDDGProvider_Name(t *testing.T) {
	t.Parallel()
	p := NewDuckDuckGoProvider(Deps{})
	if p.Name() != "duckduckgo" {
		t.Errorf("expected 'duckduckgo', got %q", p.Name())
	}
}

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return contains(s, "rate limited") || contains(s, "429")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
