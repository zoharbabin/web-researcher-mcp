package search

import (
	"context"
	"sync"
)

// ResultMeta is a request-scoped, concurrency-safe side channel for
// provider-emitted result metadata that is operator/caller-facing but does NOT
// belong in the result body — currently Brave's `query.more_results_available`
// pagination signal (F8). It mirrors RoutingTrace exactly: the tool layer
// installs one via NewResultMeta before the provider call, then reads it
// afterward and merges the value into the MCP result `_meta`. Providers that
// emit nothing leave it empty, so non-emitting providers and tests need no
// special-casing. All methods are nil-safe.
//
// Why a side channel and not the result body: `_meta` is the documented place
// for sibling-to-content provenance (cache freshness, routing) that the client
// app can read but the model never sees. A pagination cursor is exactly that —
// operator/caller plumbing, not search content — so it rides the same lane as
// routingMeta rather than polluting the typed result slice.
type ResultMeta struct {
	mu                   sync.Mutex
	moreResultsAvailable *bool
}

type resultMetaKey struct{}

// NewResultMeta returns a child context carrying a fresh ResultMeta and the
// collector itself. Pass the returned context to a provider method; read the
// collector afterward with MoreResultsAvailable(). When the provider emits no
// metadata the collector simply stays empty.
func NewResultMeta(ctx context.Context) (context.Context, *ResultMeta) {
	m := &ResultMeta{}
	return context.WithValue(ctx, resultMetaKey{}, m), m
}

// resultMetaFromContext returns the installed collector, or nil when none is
// present (the common case for direct provider calls outside the tool layer).
func resultMetaFromContext(ctx context.Context) *ResultMeta {
	m, _ := ctx.Value(resultMetaKey{}).(*ResultMeta)
	return m
}

// setMoreResultsAvailable records Brave's query.more_results_available flag.
// Stored as a pointer so "not reported" (nil) is distinguishable from an
// explicit false — the tool layer omits the _meta key entirely when unreported.
func (m *ResultMeta) setMoreResultsAvailable(v bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.moreResultsAvailable = &v
}

// MoreResultsAvailable returns (value, true) when a provider reported the
// pagination flag, or (false, false) when nothing was reported. The tool layer
// surfaces the value in `_meta.more_results_available` only when ok is true.
func (m *ResultMeta) MoreResultsAvailable() (val bool, ok bool) {
	if m == nil {
		return false, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.moreResultsAvailable == nil {
		return false, false
	}
	return *m.moreResultsAvailable, true
}
