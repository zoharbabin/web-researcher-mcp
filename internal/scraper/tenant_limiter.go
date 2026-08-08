package scraper

import (
	"container/list"
	"context"
	"sync"
)

// defaultTenantBucket is the key used for every request that carries no
// explicit tenant ID (STDIO mode, or HTTP without OAuth configured). An empty
// tenant ID must NOT bypass the per-tenant cap and get unlimited slots — it is
// simply one more bucket, itself bounded like any other tenant's.
const defaultTenantBucket = "default"

// defaultMaxLimiterTenants bounds tenantLimiter.buckets growth (#463), mirroring
// internal/metrics/collector.go's tenantStats bound (#475): a shared
// multi-tenant deployment with many short-lived/one-off tenant IDs would
// otherwise grow this map by one entry per distinct tenant ID for the process
// lifetime, with no reclamation short of a restart.
const defaultMaxLimiterTenants = 10000

// tenantLimiter hands out a per-tenant bounded channel ("bucket") sized
// perTenantCap, created lazily on first use and evicted least-recently-used
// once the number of distinct tenants would exceed maxTenants. It holds no
// package-level state — each Pipeline instance owns its own tenantLimiter, so
// two Pipeline instances never share tenant fairness state (#463).
//
// A bucket only bounds how many of ITS tenant's scrapes are in flight
// concurrently; callers must still acquire the existing global semaphore
// afterward so the combined ceiling never exceeds the global cap.
type tenantLimiter struct {
	mu         sync.Mutex
	perTenant  int
	maxTenants int
	buckets    map[string]chan struct{}
	// lru tracks bucket recency for eviction: front = most recently used.
	lru      *list.List
	elements map[string]*list.Element
}

// newTenantLimiter constructs a tenantLimiter. perTenantCap <= 0 disables the
// per-tenant sub-limiter (acquire becomes a no-op) — used when the caller
// hasn't configured a per-tenant cap distinct from the global one. maxTenants
// <= 0 falls back to defaultMaxLimiterTenants.
func newTenantLimiter(perTenantCap, maxTenants int) *tenantLimiter {
	if maxTenants <= 0 {
		maxTenants = defaultMaxLimiterTenants
	}
	return &tenantLimiter{
		perTenant:  perTenantCap,
		maxTenants: maxTenants,
		buckets:    make(map[string]chan struct{}),
		lru:        list.New(),
		elements:   make(map[string]*list.Element),
	}
}

// bucketFor returns the channel for tenantID, creating it (and evicting the
// least-recently-used tenant if the map is full) if necessary, and marks it
// most-recently-used. tenantID must already be resolved (empty ⇒ caller
// passes defaultTenantBucket) — this function does no context lookups itself.
func (tl *tenantLimiter) bucketFor(tenantID string) chan struct{} {
	tl.mu.Lock()
	defer tl.mu.Unlock()

	if ch, ok := tl.buckets[tenantID]; ok {
		tl.lru.MoveToFront(tl.elements[tenantID])
		return ch
	}

	if len(tl.buckets) >= tl.maxTenants {
		tl.evictOldestLocked()
	}

	ch := make(chan struct{}, tl.perTenant)
	tl.buckets[tenantID] = ch
	tl.elements[tenantID] = tl.lru.PushFront(tenantID)
	return ch
}

// evictOldestLocked drops the least-recently-used tenant bucket. Called with
// tl.mu already held. A bucket with in-flight slots held by a still-running
// scrape is only evicted from the map — the channel itself, and any goroutine
// blocked acquiring/releasing it, is unaffected (release still sends to the
// channel it captured, and the channel is garbage-collected once unreferenced
// after that release). A new acquire for the same tenant ID after eviction
// simply gets a fresh, empty bucket, which very rarely lets that tenant
// briefly exceed perTenant by the number of slots still draining in the old,
// evicted bucket — acceptable slack for a bound whose purpose is memory
// growth, not perfect fairness accounting.
func (tl *tenantLimiter) evictOldestLocked() {
	oldest := tl.lru.Back()
	if oldest == nil {
		return
	}
	tenantID := oldest.Value.(string)
	tl.lru.Remove(oldest)
	delete(tl.elements, tenantID)
	delete(tl.buckets, tenantID)
}

// acquire blocks until a per-tenant slot is free for tenantID, or ctx is done.
// perTenant <= 0 (disabled) is a no-op that always succeeds immediately. The
// returned release func must be called exactly once.
func (tl *tenantLimiter) acquire(ctx context.Context, tenantID string) (func(), error) {
	if tl.perTenant <= 0 {
		return func() {}, nil
	}
	ch := tl.bucketFor(tenantID)
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
