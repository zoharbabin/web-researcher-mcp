package search

import (
	"context"
	"errors"
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
