package search

import (
	"context"
	"fmt"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

type rateLimitedProvider struct {
	name string
}

func (r *rateLimitedProvider) Web(_ context.Context, _ WebSearchParams) ([]SearchResult, error) {
	return nil, fmt.Errorf("test: rate limited: %w", circuit.ErrRateLimit)
}
func (r *rateLimitedProvider) Images(_ context.Context, _ ImageSearchParams) ([]ImageResult, error) {
	return nil, fmt.Errorf("test: rate limited: %w", circuit.ErrRateLimit)
}
func (r *rateLimitedProvider) News(_ context.Context, _ NewsSearchParams) ([]NewsResult, error) {
	return nil, fmt.Errorf("test: rate limited: %w", circuit.ErrRateLimit)
}
func (r *rateLimitedProvider) Name() string { return r.name }

// TestRouterRecordFailurePassesErrRateLimit (#276): the Router must forward the
// real provider error to the breaker so a wrapped circuit.ErrRateLimit opens
// the circuit immediately, without waiting for FailureThreshold generic
// failures — verifies the recordFailure(name, err) plumbing end-to-end.
func TestRouterRecordFailurePassesErrRateLimit(t *testing.T) {
	providers := map[string]Provider{
		"limited": &rateLimitedProvider{name: "limited"},
	}
	r := NewRouter(providers, RouterConfig{
		Routing: RoutingConfig{Default: []string{"limited"}},
	})

	_, err := r.Web(context.Background(), WebSearchParams{Query: "test"})
	if err == nil {
		t.Fatal("expected error from rate-limited provider")
	}

	breaker, ok := r.breakers["limited"]
	if !ok {
		t.Fatal("expected breaker registered for provider 'limited'")
	}
	if breaker.State() != circuit.StateOpen {
		t.Errorf("expected breaker Open after a single rate-limit failure (FailureThreshold=3 default), got %v", breaker.State())
	}
}
