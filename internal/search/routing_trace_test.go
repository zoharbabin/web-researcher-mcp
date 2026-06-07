package search

import (
	"context"
	"testing"
)

// TestRoutingTrace_DirectSuccess: the first provider serves; no fallback.
func TestRoutingTrace_DirectSuccess(t *testing.T) {
	r := NewRouter(map[string]Provider{
		"primary":   &successProvider{name: "primary"},
		"secondary": &successProvider{name: "secondary"},
	}, RouterConfig{Routing: RoutingConfig{Default: []string{"primary", "secondary"}}})

	ctx, trace := NewRoutingTrace(context.Background())
	if _, err := r.Web(ctx, WebSearchParams{Query: "q"}); err != nil {
		t.Fatalf("Web: %v", err)
	}
	d := trace.Decision()
	if d.ProviderUsed != "primary" {
		t.Errorf("ProviderUsed = %q, want primary", d.ProviderUsed)
	}
	if d.Fallback {
		t.Errorf("Fallback = true, want false")
	}
	if len(d.Attempted) != 1 || d.Attempted[0] != "primary" {
		t.Errorf("Attempted = %v, want [primary]", d.Attempted)
	}
	if d.FallbackReason != "" {
		t.Errorf("FallbackReason = %q, want empty", d.FallbackReason)
	}
}

// TestRoutingTrace_FallbackOnError: primary errors, secondary serves; the trace
// records the fallback with the coarse "primary_unavailable" reason.
func TestRoutingTrace_FallbackOnError(t *testing.T) {
	r := NewRouter(map[string]Provider{
		"failing":   &failingProvider{name: "failing"},
		"secondary": &successProvider{name: "secondary"},
	}, RouterConfig{Routing: RoutingConfig{Default: []string{"failing", "secondary"}}})

	ctx, trace := NewRoutingTrace(context.Background())
	if _, err := r.Web(ctx, WebSearchParams{Query: "q"}); err != nil {
		t.Fatalf("Web: %v", err)
	}
	d := trace.Decision()
	if d.ProviderUsed != "secondary" {
		t.Errorf("ProviderUsed = %q, want secondary", d.ProviderUsed)
	}
	if !d.Fallback {
		t.Errorf("Fallback = false, want true")
	}
	if d.FallbackReason != FallbackReasonPrimaryUnavailable {
		t.Errorf("FallbackReason = %q, want %q", d.FallbackReason, FallbackReasonPrimaryUnavailable)
	}
	want := []string{"failing", "secondary"}
	if len(d.Attempted) != 2 || d.Attempted[0] != want[0] || d.Attempted[1] != want[1] {
		t.Errorf("Attempted = %v, want %v", d.Attempted, want)
	}
}

// TestRoutingTrace_CircuitOpenFallback: when the primary's breaker is open, the
// reason is the coarse "circuit_open" and the open provider is still recorded as
// attempted (it was considered, then skipped).
func TestRoutingTrace_CircuitOpenFallback(t *testing.T) {
	r := NewRouter(map[string]Provider{
		"failing":   &failingProvider{name: "failing"},
		"secondary": &successProvider{name: "secondary"},
	}, RouterConfig{Routing: RoutingConfig{Default: []string{"failing", "secondary"}}})

	// Trip the failing provider's breaker (threshold 3).
	for i := 0; i < 3; i++ {
		_, _ = r.Web(context.Background(), WebSearchParams{Query: "warm"})
	}

	ctx, trace := NewRoutingTrace(context.Background())
	if _, err := r.Web(ctx, WebSearchParams{Query: "q"}); err != nil {
		t.Fatalf("Web: %v", err)
	}
	d := trace.Decision()
	if d.ProviderUsed != "secondary" {
		t.Errorf("ProviderUsed = %q, want secondary", d.ProviderUsed)
	}
	if !d.Fallback {
		t.Errorf("Fallback = false, want true")
	}
	if d.FallbackReason != FallbackReasonCircuitOpen {
		t.Errorf("FallbackReason = %q, want %q", d.FallbackReason, FallbackReasonCircuitOpen)
	}
}

// TestRoutingTrace_NilSafe: every mutating method is a no-op on a nil trace, and
// a Router call with no trace installed in the context must not panic.
func TestRoutingTrace_NilSafe(t *testing.T) {
	var nilTrace *RoutingTrace
	nilTrace.attempt("x")
	nilTrace.served("x")
	nilTrace.fellBack("x")
	if d := nilTrace.Decision(); d.ProviderUsed != "" || len(d.Attempted) != 0 {
		t.Errorf("nil trace Decision = %+v, want zero", d)
	}

	r := NewRouter(map[string]Provider{"a": &successProvider{name: "a"}},
		RouterConfig{Routing: RoutingConfig{Default: []string{"a"}}})
	if _, err := r.Web(context.Background(), WebSearchParams{Query: "q"}); err != nil {
		t.Fatalf("Web without trace: %v", err)
	}
}

// TestRoutingTrace_NonRouterEmpty: a trace installed around a direct (non-Router)
// provider stays empty — nothing observed, so the tool layer emits no routing meta.
func TestRoutingTrace_NonRouterEmpty(t *testing.T) {
	p := &successProvider{name: "solo"}
	ctx, trace := NewRoutingTrace(context.Background())
	if _, err := p.Web(ctx, WebSearchParams{Query: "q"}); err != nil {
		t.Fatalf("Web: %v", err)
	}
	d := trace.Decision()
	if d.ProviderUsed != "" || len(d.Attempted) != 0 {
		t.Errorf("non-router Decision = %+v, want zero", d)
	}
}

func TestCoarsenFallbackReason(t *testing.T) {
	if got := coarsenFallbackReason(FallbackReasonCircuitOpen); got != FallbackReasonCircuitOpen {
		t.Errorf("circuit_open coarsened to %q", got)
	}
	// Any raw upstream error string collapses to the coarse enum (never leaked).
	if got := coarsenFallbackReason("brave: 500 internal error key=AIza..."); got != FallbackReasonPrimaryUnavailable {
		t.Errorf("raw error coarsened to %q, want %q", got, FallbackReasonPrimaryUnavailable)
	}
}
