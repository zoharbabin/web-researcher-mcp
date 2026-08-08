package scraper

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
)

// TestPerTenantLimiter_GlobalCeilingRespected proves #463's core safety
// property: even with many distinct tenants each queuing concurrently, total
// in-flight scrapes across ALL tenants never exceeds the existing global
// MAX_SCRAPE_CONCURRENCY ceiling. Ten tenants each request one slot
// concurrently against a global cap of 3 and a per-tenant cap of 1 (so no
// single tenant's own bucket is ever the bottleneck) — only the global
// semaphore can be the limiting factor here.
func TestPerTenantLimiter_GlobalCeilingRespected(t *testing.T) {
	t.Parallel()

	const globalCap = 3
	const numTenants = 10
	p := NewPipeline(PipelineConfig{MaxConcurrency: globalCap, MaxConcurrencyPerTenant: 1, AllowPrivateIPs: true})

	var inFlight atomic.Int32
	var maxSeen atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < numTenants; i++ {
		tenantID := fmt.Sprintf("tenant-%d", i)
		wg.Add(1)
		go func(tid string) {
			defer wg.Done()
			<-start
			ctx := auth.WithIdentity(context.Background(), tid, "user")
			release, err := p.acquireTier(ctx, "")
			if err != nil {
				t.Errorf("acquireTier for tenant %s failed: %v", tid, err)
				return
			}
			cur := inFlight.Add(1)
			for {
				m := maxSeen.Load()
				if cur <= m || maxSeen.CompareAndSwap(m, cur) {
					break
				}
			}
			time.Sleep(30 * time.Millisecond)
			inFlight.Add(-1)
			release()
		}(tenantID)
	}
	close(start)
	wg.Wait()

	if got := maxSeen.Load(); got > int32(globalCap) {
		t.Errorf("observed %d concurrent in-flight scrapes across %d tenants, want <= global cap %d", got, numTenants, globalCap)
	}
}

// TestPerTenantLimiter_SingleTenantUsesFullCapacity proves the per-tenant
// sub-limiter doesn't artificially restrict a lone tenant below the global
// cap — the common single-tenant deployment must see no behavior change and
// no deadlock/under-utilization. MaxConcurrencyPerTenant is left unset, so it
// defaults to MaxConcurrency (NewPipeline's documented default), meaning "no
// per-tenant restriction beyond the global cap".
func TestPerTenantLimiter_SingleTenantUsesFullCapacity(t *testing.T) {
	t.Parallel()

	const globalCap = 5
	p := NewPipeline(PipelineConfig{MaxConcurrency: globalCap, AllowPrivateIPs: true})

	ctx := auth.WithIdentity(context.Background(), "solo-tenant", "user")

	var inFlight atomic.Int32
	var maxSeen atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < globalCap; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release, err := p.acquireTier(ctx, "")
			if err != nil {
				t.Errorf("acquireTier failed: %v", err)
				return
			}
			cur := inFlight.Add(1)
			for {
				m := maxSeen.Load()
				if cur <= m || maxSeen.CompareAndSwap(m, cur) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
			inFlight.Add(-1)
			release()
		}()
	}
	close(start)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("single tenant's acquireTier calls deadlocked instead of all reaching the global cap concurrently")
	}

	if got := maxSeen.Load(); got != int32(globalCap) {
		t.Errorf("solo tenant reached only %d concurrent in-flight scrapes, want the full global cap %d (no artificial per-tenant restriction below it)", got, globalCap)
	}
}

// spoofedTenantContextKey simulates an attacker-controlled context value that
// is NOT the authenticated tenant identity — e.g. if some future call site
// mistakenly threaded a tool-input-derived value into the context under a
// different key. tenantBucketKey must ignore it entirely.
type spoofedTenantContextKey struct{}

// TestScrapeFairness_TenantIDFromContextOnly proves the CRITICAL security
// constraint from #463: the limiter's tenant identity comes ONLY from
// auth.TenantIDFromContext(ctx) — an already-authenticated context value —
// and never from any other context value or tool-call-supplied field. A
// request must not be able to spoof another tenant's fairness bucket (or
// claim an unbounded bucket of its own) by any other means.
func TestScrapeFairness_TenantIDFromContextOnly(t *testing.T) {
	t.Parallel()

	// A context carrying a legitimate authenticated tenant ID plus an
	// unrelated context value that looks like a tenant override. The
	// resolved bucket key must be the authenticated ID, ignoring the
	// impostor value.
	ctx := auth.WithIdentity(context.Background(), "tenant-real", "user-1")
	ctx = context.WithValue(ctx, spoofedTenantContextKey{}, "tenant-attacker")

	if got := tenantBucketKey(ctx); got != "tenant-real" {
		t.Errorf("tenantBucketKey = %q, want %q — must come only from auth.TenantIDFromContext, never any other context value", got, "tenant-real")
	}

	// A context with ONLY the impostor value and no authenticated tenant ID
	// must fall back to the shared default bucket — an unauthenticated
	// caller must never be able to adopt an arbitrary value as its own
	// private, unbounded fairness lane.
	bare := context.WithValue(context.Background(), spoofedTenantContextKey{}, "tenant-attacker")
	if got := tenantBucketKey(bare); got != defaultTenantBucket {
		t.Errorf("tenantBucketKey(bare) = %q, want %q — an unauthenticated context must not adopt an arbitrary context value as its tenant bucket", got, defaultTenantBucket)
	}

	// Two different auth-context tenant IDs must resolve to two different
	// buckets, proving the key is actually derived from the authenticated
	// value and not a constant.
	ctxA := auth.WithIdentity(context.Background(), "tenant-a", "user")
	ctxB := auth.WithIdentity(context.Background(), "tenant-b", "user")
	if tenantBucketKey(ctxA) == tenantBucketKey(ctxB) {
		t.Error("tenantBucketKey resolved two distinct authenticated tenant IDs to the same bucket")
	}

	// acquireTier's own signature takes only (ctx, tier) — there is no
	// tool-input parameter through which a tenant ID could flow even if a
	// caller wanted to pass one. This guards against a future edit
	// reintroducing a tenant-like parameter on the acquire path.
	p := NewPipeline(PipelineConfig{AllowPrivateIPs: true})
	rt := reflect.TypeOf(p.acquireTier)
	if rt.NumIn() != 2 {
		t.Fatalf("acquireTier signature changed: got %d params, want 2 (ctx, tier) — a tenant ID must never be added as a caller-supplied parameter", rt.NumIn())
	}
}

// TestPerTenantLimiter_NoisyNeighborIsolation proves the core noisy-neighbor
// fix: one tenant saturating its own per-tenant slots must NOT block another
// tenant's scrape. Tenant A exhausts its single per-tenant slot; tenant B,
// sharing the same generous global pool, must still acquire promptly.
func TestPerTenantLimiter_NoisyNeighborIsolation(t *testing.T) {
	t.Parallel()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 5, MaxConcurrencyPerTenant: 1, AllowPrivateIPs: true})

	ctxA := auth.WithIdentity(context.Background(), "tenant-a", "user-a")
	releaseA, err := p.acquireTier(ctxA, "")
	if err != nil {
		t.Fatalf("tenant A's first acquireTier failed: %v", err)
	}
	defer releaseA()

	// Sanity check: tenant A really is saturated at its own cap (1) — a
	// second acquire for tenant A must block until released.
	ctxA2 := auth.WithIdentity(context.Background(), "tenant-a", "user-a2")
	blockedCtx, cancel := context.WithTimeout(ctxA2, 150*time.Millisecond)
	defer cancel()
	if _, err := p.acquireTier(blockedCtx, ""); err == nil {
		t.Fatal("expected tenant A's second acquireTier to block on its own saturated per-tenant bucket")
	}

	// Tenant B must NOT be blocked by tenant A's saturation.
	ctxB := auth.WithIdentity(context.Background(), "tenant-b", "user-b")
	done := make(chan struct{})
	go func() {
		release, err := p.acquireTier(ctxB, "")
		if err != nil {
			t.Errorf("tenant B's acquireTier failed: %v", err)
			return
		}
		release()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("tenant B's acquireTier blocked on tenant A's saturated per-tenant bucket — noisy-neighbor isolation failed")
	}
}

// TestMultiInstancePipelineTenantLimiterIsolation proves two Pipeline
// instances constructed independently in the same process do not share any
// tenant-limiter state — the per-tenant map lives on the Pipeline struct
// itself (constructed per-instance in NewPipeline), never as package-level
// state. The SAME tenant ID saturates its slot on p1; the same tenant ID must
// still acquire immediately on the independent p2.
func TestMultiInstancePipelineTenantLimiterIsolation(t *testing.T) {
	t.Parallel()

	p1 := NewPipeline(PipelineConfig{MaxConcurrency: 5, MaxConcurrencyPerTenant: 1, AllowPrivateIPs: true})
	p2 := NewPipeline(PipelineConfig{MaxConcurrency: 5, MaxConcurrencyPerTenant: 1, AllowPrivateIPs: true})

	ctx := auth.WithIdentity(context.Background(), "shared-tenant-id", "user")

	release1, err := p1.acquireTier(ctx, "")
	if err != nil {
		t.Fatalf("p1.acquireTier failed: %v", err)
	}
	defer release1()

	// p1's bucket for this tenant is now full; a second acquire on p1 for the
	// same tenant must block.
	blockedCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if _, err := p1.acquireTier(blockedCtx, ""); err == nil {
		t.Fatal("expected p1's second acquire for the same tenant to block")
	}

	// p2 is a completely independent Pipeline — the same tenant ID must
	// acquire immediately on p2, proving no shared package-level state.
	done := make(chan struct{})
	go func() {
		release2, err := p2.acquireTier(ctx, "")
		if err != nil {
			t.Errorf("p2.acquireTier failed: %v", err)
			return
		}
		release2()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("p2's acquireTier for the same tenant ID blocked on p1's saturated bucket — tenant-limiter state leaked across Pipeline instances")
	}
}
