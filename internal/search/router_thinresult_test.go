package search

import (
	"context"
	"errors"
	"testing"
)

// thinProvider simulates a provider returning thin results on the first call
// and full results on a simplified-query retry.
type thinProvider struct {
	name            string
	callCount       int
	firstResults    []SearchResult
	retryResults    []SearchResult
	capturedQueries []string
}

func (t *thinProvider) Web(_ context.Context, p WebSearchParams) ([]SearchResult, error) {
	t.callCount++
	t.capturedQueries = append(t.capturedQueries, p.Query)
	if t.callCount == 1 {
		return t.firstResults, nil
	}
	return t.retryResults, nil
}
func (t *thinProvider) Images(_ context.Context, _ ImageSearchParams) ([]ImageResult, error) {
	return nil, nil
}
func (t *thinProvider) News(_ context.Context, _ NewsSearchParams) ([]NewsResult, error) {
	return nil, nil
}
func (t *thinProvider) Name() string { return t.name }

// customProvider allows injecting an arbitrary webFn for error-path testing.
type customProvider struct {
	name  string
	webFn func(context.Context, WebSearchParams) ([]SearchResult, error)
}

func (c *customProvider) Web(ctx context.Context, p WebSearchParams) ([]SearchResult, error) {
	return c.webFn(ctx, p)
}
func (c *customProvider) Images(_ context.Context, _ ImageSearchParams) ([]ImageResult, error) {
	return nil, nil
}
func (c *customProvider) News(_ context.Context, _ NewsSearchParams) ([]NewsResult, error) {
	return nil, nil
}
func (c *customProvider) Name() string { return c.name }

// TestSimplifyQuery (#278): pure-function table test for stop-word stripping.
func TestSimplifyQuery(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"strips leading stop words", "what is the capital of france", "capital france"},
		{"no stop words returns original", "golang concurrency patterns", "golang concurrency patterns"},
		{"all stop words returns original", "what is the", "what is the"},
		{"empty string returns original", "", ""},
		{"single informative token survives", "how do I use kubernetes", "use kubernetes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := simplifyQuery(c.query)
			if got != c.want {
				t.Errorf("simplifyQuery(%q) = %q, want %q", c.query, got, c.want)
			}
		})
	}
}

// TestRouter_ThinResultRetry_SucceedsWithSimplifiedQuery (#278): a thin first
// result triggers a same-provider retry with the simplified query, and the
// richer retry result set is used.
func TestRouter_ThinResultRetry_SucceedsWithSimplifiedQuery(t *testing.T) {
	p := &thinProvider{
		name:         "thin",
		firstResults: []SearchResult{{Title: "one"}},
		retryResults: []SearchResult{{Title: "one"}, {Title: "two"}},
	}
	r := NewRouter(map[string]Provider{"thin": p}, RouterConfig{
		Routing:       RoutingConfig{Default: []string{"thin"}},
		ThinThreshold: 1,
	})

	results, err := r.Web(context.Background(), WebSearchParams{Query: "what is the capital of france"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results after retry, got %d", len(results))
	}
	if p.callCount != 2 {
		t.Errorf("expected 2 provider calls, got %d", p.callCount)
	}
	if len(p.capturedQueries) == 2 && p.capturedQueries[0] == p.capturedQueries[1] {
		t.Error("expected retry query to differ from original")
	}
}

// TestRouter_ThinResultRetry_NoRetryWhenThresholdZero (#278): ThinThreshold=0
// disables the feature entirely, preserving current behavior.
func TestRouter_ThinResultRetry_NoRetryWhenThresholdZero(t *testing.T) {
	p := &thinProvider{
		name:         "thin",
		firstResults: []SearchResult{{Title: "one"}},
		retryResults: []SearchResult{{Title: "one"}, {Title: "two"}},
	}
	r := NewRouter(map[string]Provider{"thin": p}, RouterConfig{
		Routing:       RoutingConfig{Default: []string{"thin"}},
		ThinThreshold: 0,
	})

	results, err := r.Web(context.Background(), WebSearchParams{Query: "what is the capital of france"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result (feature disabled), got %d", len(results))
	}
	if p.callCount != 1 {
		t.Errorf("expected 1 provider call (feature disabled), got %d", p.callCount)
	}
}

// TestRouter_ThinResultRetry_NoRetryWhenQueryUnchanged (#278): a query with no
// stop words has nothing to simplify, so no retry call is made.
func TestRouter_ThinResultRetry_NoRetryWhenQueryUnchanged(t *testing.T) {
	p := &thinProvider{
		name:         "thin",
		firstResults: []SearchResult{{Title: "one"}},
		retryResults: []SearchResult{{Title: "one"}, {Title: "two"}},
	}
	r := NewRouter(map[string]Provider{"thin": p}, RouterConfig{
		Routing:       RoutingConfig{Default: []string{"thin"}},
		ThinThreshold: 1,
	})

	results, err := r.Web(context.Background(), WebSearchParams{Query: "golang concurrency"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result (no simplification possible), got %d", len(results))
	}
	if p.callCount != 1 {
		t.Errorf("expected 1 provider call (query unchanged by simplifyQuery), got %d", p.callCount)
	}
}

// TestRouter_ThinResultRetry_StillServesOriginalOnRetryFail (#278): if the
// retry call errors, the original thin result set is still returned with a
// nil error — the thin-result path degrades gracefully, never propagates a
// retry error, and never triggers provider fallback.
func TestRouter_ThinResultRetry_StillServesOriginalOnRetryFail(t *testing.T) {
	callCount := 0
	p := &customProvider{
		name: "flaky",
		webFn: func(_ context.Context, _ WebSearchParams) ([]SearchResult, error) {
			callCount++
			if callCount == 1 {
				return []SearchResult{{Title: "one"}}, nil
			}
			return nil, errors.New("flaky: simulated retry failure")
		},
	}
	r := NewRouter(map[string]Provider{"flaky": p}, RouterConfig{
		Routing:       RoutingConfig{Default: []string{"flaky"}},
		ThinThreshold: 1,
	})

	results, err := r.Web(context.Background(), WebSearchParams{Query: "what is the capital of france"})
	if err != nil {
		t.Fatalf("expected graceful degradation (nil error), got %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected original thin result preserved, got %d results", len(results))
	}
	if callCount != 2 {
		t.Errorf("expected 2 provider calls (original + failed retry), got %d", callCount)
	}
}

// TestRouter_ThinResultRetry_NormalPathNotAffected (#278): a result count
// above the threshold makes exactly one provider call — zero overhead.
func TestRouter_ThinResultRetry_NormalPathNotAffected(t *testing.T) {
	p := &thinProvider{
		name:         "rich",
		firstResults: []SearchResult{{Title: "one"}, {Title: "two"}, {Title: "three"}},
	}
	r := NewRouter(map[string]Provider{"rich": p}, RouterConfig{
		Routing:       RoutingConfig{Default: []string{"rich"}},
		ThinThreshold: 1,
	})

	results, err := r.Web(context.Background(), WebSearchParams{Query: "what is the capital of france"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
	if p.callCount != 1 {
		t.Errorf("expected exactly 1 provider call (above threshold), got %d", p.callCount)
	}
}
