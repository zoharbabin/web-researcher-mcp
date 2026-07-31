package search

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// chaosProvider simulates a real provider whose health can be toggled at
// runtime, driving Router failover/recovery under concurrent load without a
// real HTTP server (#469).
type chaosProvider struct {
	name    string
	healthy atomic.Bool
	calls   atomic.Int64
}

func newChaosProvider(name string, healthy bool) *chaosProvider {
	p := &chaosProvider{name: name}
	p.healthy.Store(healthy)
	return p
}

func (p *chaosProvider) Web(_ context.Context, _ WebSearchParams) ([]SearchResult, error) {
	p.calls.Add(1)
	if !p.healthy.Load() {
		return nil, fmt.Errorf("%s: simulated outage", p.name)
	}
	return []SearchResult{{Title: "ok from " + p.name}}, nil
}

func (p *chaosProvider) Images(_ context.Context, _ ImageSearchParams) ([]ImageResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *chaosProvider) News(_ context.Context, _ NewsSearchParams) ([]NewsResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *chaosProvider) Name() string { return p.name }

// TestRouterChaosFailoverAndRecovery_ConcurrentFailoverNoErrorsLeakToCaller
// (#469): under a burst of concurrent requests against an always-failing
// primary provider, the Router must fail over to the healthy secondary
// within each request — no caller should ever observe an error, and the
// primary's breaker must open.
func TestRouterChaosFailoverAndRecovery_ConcurrentFailoverNoErrorsLeakToCaller(t *testing.T) {
	a := newChaosProvider("a", false) // always failing (simulated outage)
	b := newChaosProvider("b", true)  // healthy fallback

	providers := map[string]Provider{"a": a, "b": b}
	r := NewRouter(providers, RouterConfig{Routing: RoutingConfig{Default: []string{"a", "b"}}})

	const n = 50
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.Web(context.Background(), WebSearchParams{Query: "q"})
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Errorf("expected no error to leak to caller (Router should fail over to healthy 'b'), got %v", err)
		}
	}

	if got := b.calls.Load(); got != n {
		t.Errorf("expected all %d requests served by healthy fallback 'b', got %d calls", n, got)
	}
	if r.breakers["a"].State() != circuit.StateOpen {
		t.Errorf("expected breaker for 'a' to be Open after repeated concurrent failures, got %v", r.breakers["a"].State())
	}
}

// TestRouterChaosFailoverAndRecovery_HalfOpenRecoveryReturnsTrafficToProvider
// (#469): once a tripped provider's breaker passes its reset window and the
// provider itself has recovered, the Router must route back to it (half-open
// probe succeeds) rather than staying permanently failed-over.
func TestRouterChaosFailoverAndRecovery_HalfOpenRecoveryReturnsTrafficToProvider(t *testing.T) {
	a := newChaosProvider("a", false)
	providers := map[string]Provider{"a": a}
	r := NewRouter(providers, RouterConfig{Routing: RoutingConfig{Default: []string{"a"}}})

	// Same-package access, mirroring router_ratelimit_test.go: override with a
	// short reset window so the test doesn't wait out the real 30s default.
	r.breakers["a"] = circuit.New(circuit.Config{FailureThreshold: 3, ResetTimeout: 1, HalfOpenAttempts: 1})

	for i := 0; i < 3; i++ {
		if _, err := r.Web(context.Background(), WebSearchParams{Query: "q"}); err == nil {
			t.Fatalf("call %d: expected error while provider unhealthy", i)
		}
	}
	if r.breakers["a"].State() != circuit.StateOpen {
		t.Fatalf("expected breaker Open after 3 failures, got %v", r.breakers["a"].State())
	}

	callsWhileOpen := a.calls.Load()
	if _, err := r.Web(context.Background(), WebSearchParams{Query: "q"}); err == nil {
		t.Fatal("expected error while circuit is open")
	}
	if a.calls.Load() != callsWhileOpen {
		t.Error("provider must not be called while its circuit is open")
	}

	// Provider recovers; wait past ResetTimeout + max jitter (20%) for
	// half-open eligibility (#471).
	a.healthy.Store(true)
	time.Sleep(1300 * time.Millisecond)

	results, err := r.Web(context.Background(), WebSearchParams{Query: "q"})
	if err != nil {
		t.Fatalf("expected success once provider recovered, got %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results from recovered provider")
	}
	if r.breakers["a"].State() != circuit.StateClosed {
		t.Errorf("expected breaker Closed after successful half-open probe, got %v", r.breakers["a"].State())
	}
}
