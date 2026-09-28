package search

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newSearchAPITestProvider(t *testing.T, handler http.HandlerFunc) *SearchAPIProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	deps := Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5}),
	}
	p := NewSearchAPIProvider("test-key", deps)
	p.SetBaseURL(srv.URL)
	return p
}

func TestSearchAPIProvider_LimitsResults(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		requested int
		available int
		want      int
	}{
		{"excess results", 5, 100, 5},
		{"fewer results", 5, 3, 3},
		{"exact count", 5, 5, 5},
		{"empty results", 5, 0, 0},
		{"single result", 1, 3, 1},
		{"zero count uses minimum", 0, 100, 1},
		{"negative count uses minimum", -1, 100, 1},
		{"zero count and empty results", 0, 0, 0},
		{"negative count and empty results", -1, 0, 0},
		{"above former ceiling", 50, 100, 50},
		{"all available results", 150, 100, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			items := make([]string, tc.available)
			for i := range items {
				items[i] = fmt.Sprintf(`{"title":"Result %d"}`, i)
			}
			p := newSearchAPITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Has("num") {
					t.Error("unexpected num parameter")
				}
				field := "organic_results"
				if r.URL.Query().Get("engine") == "google_images" {
					field = "images"
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"%s":[%s]}`, field, strings.Join(items, ","))
			})

			web, err := p.Web(context.Background(), WebSearchParams{Query: "cats", NumResults: tc.requested})
			if err != nil {
				t.Fatal(err)
			}
			images, err := p.Images(context.Background(), ImageSearchParams{Query: "cats", NumResults: tc.requested})
			if err != nil {
				t.Fatal(err)
			}
			news, err := p.News(context.Background(), NewsSearchParams{Query: "cats", NumResults: tc.requested})
			if err != nil {
				t.Fatal(err)
			}
			if len(web) != tc.want || len(images) != tc.want || len(news) != tc.want {
				t.Fatalf("result counts: web=%d, images=%d, news=%d; want %d", len(web), len(images), len(news), tc.want)
			}
			for i := 0; i < tc.want; i++ {
				want := fmt.Sprintf("Result %d", i)
				if web[i].Title != want || images[i].Title != want || news[i].Title != want {
					t.Errorf("unexpected result at position %d: web=%q, images=%q, news=%q", i, web[i].Title, images[i].Title, news[i].Title)
				}
			}
		})
	}
}

// TestSearchAPIProvider_429SurfacesRetryAfter is the #666 regression test:
// a 429 response's retryAfterSeconds body field must be parsed and surfaced
// as a circuit.RateLimitError the breaker can honor, instead of being
// silently discarded in favor of the bare circuit.ErrRateLimit sentinel.
func TestSearchAPIProvider_429SurfacesRetryAfter(t *testing.T) {
	t.Parallel()
	p := newSearchAPITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"retryAfterSeconds": 60}`))
	})

	_, err := p.Web(context.Background(), WebSearchParams{Query: "test"})
	if err == nil {
		t.Fatal("expected an error on HTTP 429, got nil")
	}
	if !strings.Contains(err.Error(), "60") {
		t.Errorf("expected error message to mention the 60s retry-after, got %q", err.Error())
	}

	var rle *circuit.RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("expected error to unwrap to *circuit.RateLimitError, got %v", err)
	}
	if rle.After != 60*time.Second {
		t.Errorf("RateLimitError.After = %s, want 60s", rle.After)
	}
	if !errors.Is(err, circuit.ErrRateLimit) {
		t.Error("expected error to still match circuit.ErrRateLimit via errors.Is")
	}
}

// TestSearchAPIProvider_429FallsBackToRetryAfterHeader proves the standard
// HTTP Retry-After header is honored when the response body carries no
// retryAfterSeconds field.
func TestSearchAPIProvider_429FallsBackToRetryAfterHeader(t *testing.T) {
	t.Parallel()
	p := newSearchAPITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "45")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	_, err := p.Web(context.Background(), WebSearchParams{Query: "test"})
	if err == nil {
		t.Fatal("expected an error on HTTP 429, got nil")
	}

	var rle *circuit.RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("expected error to unwrap to *circuit.RateLimitError, got %v", err)
	}
	if rle.After != 45*time.Second {
		t.Errorf("RateLimitError.After = %s, want 45s", rle.After)
	}
}

// TestSearchAPIProvider_429NoSignalFallsBackToBareErrRateLimit proves that
// when the provider gives no retry-after signal at all, the bare
// circuit.ErrRateLimit sentinel is used (the breaker's configured
// ResetTimeout applies, unchanged from prior behavior).
func TestSearchAPIProvider_429NoSignalFallsBackToBareErrRateLimit(t *testing.T) {
	t.Parallel()
	p := newSearchAPITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})

	_, err := p.Web(context.Background(), WebSearchParams{Query: "test"})
	if err == nil {
		t.Fatal("expected an error on HTTP 429, got nil")
	}
	if !errors.Is(err, circuit.ErrRateLimit) {
		t.Error("expected error to match circuit.ErrRateLimit")
	}
	var rle *circuit.RateLimitError
	if errors.As(err, &rle) {
		t.Errorf("expected no RateLimitError when no retry-after signal was given, got %+v", rle)
	}
}
