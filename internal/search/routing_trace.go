package search

import (
	"context"
	"sync"
)

// RoutingDecision is the operator-facing summary of what the Router actually did
// for a single request: which provider served it, which were attempted (in
// priority order), whether a fallback fired, and a COARSE reason. It is debug/
// operator data — never content — surfaced by the tool layer via result _meta
// (issue #58). It deliberately carries no breaker counts, upstream URLs, or
// error bodies; the provider NAME is the disclosure boundary.
type RoutingDecision struct {
	// ProviderUsed is the provider whose result was returned. Empty when no
	// provider served (all failed) or when no Router was in play.
	ProviderUsed string
	// Attempted lists providers tried (invoked or skipped for an open breaker)
	// in priority order, up to and including the one that served.
	Attempted []string
	// Fallback is true when the served provider was not the first attempted.
	Fallback bool
	// FallbackReason is a coarse enum describing why the ladder advanced:
	// "circuit_open" (prior provider's breaker was open) or "primary_unavailable"
	// (prior provider returned an error). Empty when no fallback occurred.
	FallbackReason string
}

// Coarse fallback-reason enum values. Kept intentionally small (no raw breaker
// dump/counts) per the operator-observability design.
const (
	FallbackReasonCircuitOpen        = "circuit_open"
	FallbackReasonPrimaryUnavailable = "primary_unavailable"
)

// coarsenFallbackReason maps the Router's internal reason string (an error
// message or the literal "circuit_open") to the coarse public enum, so raw
// upstream error text never reaches an operator-facing _meta field.
func coarsenFallbackReason(reason string) string {
	if reason == FallbackReasonCircuitOpen {
		return FallbackReasonCircuitOpen
	}
	return FallbackReasonPrimaryUnavailable
}

// RoutingTrace is a request-scoped, concurrency-safe collector the Router writes
// to while iterating providers. The tool layer installs one via NewRoutingTrace
// before calling a Router method, then reads Decision() afterward. All mutating
// methods are nil-safe so non-Router providers and tests need no special-casing.
type RoutingTrace struct {
	mu        sync.Mutex
	attempted []string
	used      string
	reason    string
}

type routingTraceKey struct{}

// NewRoutingTrace returns a child context carrying a fresh RoutingTrace and the
// trace itself. Pass the returned context to a Router method; read the trace
// afterward with Decision(). When the underlying provider is not a Router the
// trace simply stays empty (Decision reports nothing observed).
func NewRoutingTrace(ctx context.Context) (context.Context, *RoutingTrace) {
	t := &RoutingTrace{}
	return context.WithValue(ctx, routingTraceKey{}, t), t
}

// routingTraceFromContext returns the installed trace, or nil when none is
// present (the common case for direct, non-routed provider calls).
func routingTraceFromContext(ctx context.Context) *RoutingTrace {
	t, _ := ctx.Value(routingTraceKey{}).(*RoutingTrace)
	return t
}

// attempt records that a provider was tried (or skipped for an open breaker), in
// priority order. Duplicate consecutive names are ignored so a provider examined
// once is listed once.
func (t *RoutingTrace) attempt(name string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if n := len(t.attempted); n > 0 && t.attempted[n-1] == name {
		return
	}
	t.attempted = append(t.attempted, name)
}

// served records the provider that returned a successful result.
func (t *RoutingTrace) served(name string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.used = name
}

// fellBack records the coarse reason the ladder advanced past a provider. The
// most recent reason before success is the one surfaced.
func (t *RoutingTrace) fellBack(reason string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reason = coarsenFallbackReason(reason)
}

// Decision returns an immutable snapshot. Fallback is derived as "the served
// provider was not the first attempted". Returns the zero value for a nil trace
// (nothing observed → the tool layer emits no routing _meta).
func (t *RoutingTrace) Decision() RoutingDecision {
	if t == nil {
		return RoutingDecision{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	d := RoutingDecision{ProviderUsed: t.used}
	if len(t.attempted) > 0 {
		d.Attempted = append([]string(nil), t.attempted...)
		if t.used != "" && t.attempted[0] != t.used {
			d.Fallback = true
			d.FallbackReason = t.reason
		}
	}
	return d
}
