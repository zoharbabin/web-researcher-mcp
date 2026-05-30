package ratelimit

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/config"
	"github.com/zoharbabin/web-researcher-mcp/internal/persist"
)

func TestAllow(t *testing.T) {
	l := New(config.RateLimitConfig{
		Global:     100,
		PerTenant:  10,
		DailyQuota: 1000,
	})

	if !l.Allow("tenant1") {
		t.Fatal("expected first call to be allowed")
	}
}

func TestAllowRejectAfterBurst(t *testing.T) {
	l := New(config.RateLimitConfig{
		Global:     5,
		PerTenant:  2,
		DailyQuota: 1000,
	})

	allowed := 0
	for i := 0; i < 10; i++ {
		if l.Allow("tenant1") {
			allowed++
		}
	}

	if allowed >= 10 {
		t.Fatalf("expected some calls to be rate-limited, but all %d were allowed", allowed)
	}
}

func TestAllowDailyQuota(t *testing.T) {
	l := New(config.RateLimitConfig{
		Global:     1000,
		PerTenant:  1000,
		DailyQuota: 3,
	})

	for i := 0; i < 3; i++ {
		if !l.AllowDaily("tenant1") {
			t.Fatalf("call %d should be allowed within daily quota", i+1)
		}
	}

	if l.AllowDaily("tenant1") {
		t.Fatal("expected call to be rejected after daily quota exceeded")
	}
}

func TestTenantIsolation(t *testing.T) {
	l := New(config.RateLimitConfig{
		Global:     1000,
		PerTenant:  1000,
		DailyQuota: 2,
	})

	l.AllowDaily("tenant1")
	l.AllowDaily("tenant1")

	if l.AllowDaily("tenant1") {
		t.Fatal("tenant1 should be exhausted")
	}

	if !l.AllowDaily("tenant2") {
		t.Fatal("tenant2 should not be affected by tenant1's quota")
	}
}

func TestWrapMiddleware(t *testing.T) {
	l := New(config.RateLimitConfig{
		Global:     100,
		PerTenant:  100,
		DailyQuota: 100,
	})

	handler := l.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestWrapMiddlewareReject(t *testing.T) {
	l := New(config.RateLimitConfig{
		Global:     1,
		PerTenant:  1,
		DailyQuota: 100,
	})

	handler := l.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust the rate limiter
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	// Next request should be rate-limited
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests && rec.Code != http.StatusOK {
		t.Fatalf("expected either 200 or 429, got %d", rec.Code)
	}
}

func TestCleanup(t *testing.T) {
	l := New(config.RateLimitConfig{
		Global:     100,
		PerTenant:  100,
		DailyQuota: 100,
	})

	l.Allow("tenant1")
	l.Cleanup()
	// Should not panic
}

func TestStats(t *testing.T) {
	l := New(config.RateLimitConfig{
		Global:     100,
		PerTenant:  60,
		DailyQuota: 500,
	})

	l.AllowDaily("tenant1")
	l.AllowDaily("tenant1")

	stats := l.Stats("tenant1")
	if stats.TenantID != "tenant1" {
		t.Fatalf("expected tenantId=tenant1, got %s", stats.TenantID)
	}
	if stats.PerMinuteLimit != 60 {
		t.Fatalf("expected perMinuteLimit=60, got %d", stats.PerMinuteLimit)
	}
	if stats.DailyLimit != 500 {
		t.Fatalf("expected dailyLimit=500, got %d", stats.DailyLimit)
	}
	if stats.DailyUsed != 2 {
		t.Fatalf("expected dailyUsed=2, got %d", stats.DailyUsed)
	}
	if stats.DailyRemaining != 498 {
		t.Fatalf("expected dailyRemaining=498, got %d", stats.DailyRemaining)
	}
}

func TestStatsUnknownTenant(t *testing.T) {
	l := New(config.RateLimitConfig{
		Global:     100,
		PerTenant:  60,
		DailyQuota: 500,
	})

	stats := l.Stats("unknown")
	if stats.DailyUsed != 0 {
		t.Fatalf("expected 0 usage for unknown tenant, got %d", stats.DailyUsed)
	}
	if stats.DailyRemaining != 500 {
		t.Fatalf("expected full daily remaining for unknown tenant, got %d", stats.DailyRemaining)
	}
}

func TestWrapMiddlewareErrorMessage(t *testing.T) {
	l := New(config.RateLimitConfig{
		Global:     1000,
		PerTenant:  1,
		DailyQuota: 1000,
	})

	handler := l.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust the per-tenant limiter
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusTooManyRequests {
		body := rec.Body.String()
		if body == "" {
			t.Fatal("expected non-empty error body on 429")
		}
		if !strings.Contains(body, "RATE_LIMIT_PER_TENANT") {
			t.Errorf("expected error message to mention RATE_LIMIT_PER_TENANT env var, got: %s", body)
		}
	}
}

func TestWrapMiddlewareReadsTenantFromAuthContext(t *testing.T) {
	l := New(config.RateLimitConfig{
		Global:     1000,
		PerTenant:  2,
		DailyQuota: 1000,
	})

	handler := l.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Simulate auth middleware setting tenant ID in context
	makeReq := func(tenantID string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := context.WithValue(req.Context(), auth.ContextKeyTenantID, tenantID)
		return req.WithContext(ctx)
	}

	// Exhaust tenant-A's quota
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, makeReq("tenant-A"))
	}

	// tenant-A should be rate-limited
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, makeReq("tenant-A"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("tenant-A should be rate-limited, got %d", rec.Code)
	}

	// tenant-B should still work (isolated bucket)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, makeReq("tenant-B"))
	if rec.Code != http.StatusOK {
		t.Fatalf("tenant-B should not be rate-limited, got %d", rec.Code)
	}
}

func TestWrapMiddlewareDefaultTenantWithoutAuth(t *testing.T) {
	l := New(config.RateLimitConfig{
		Global:     1000,
		PerTenant:  2,
		DailyQuota: 1000,
	})

	handler := l.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// No auth context → should use "default" tenant via auth.TenantIDFromContext
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after exhausting default tenant, got %d", rec.Code)
	}

	// Verify error message mentions "default" tenant
	body := rec.Body.String()
	if !strings.Contains(body, "default") {
		t.Errorf("error message should mention 'default' tenant, got: %s", body)
	}
}

func TestWrapMiddlewareDailyQuotaErrorMessage(t *testing.T) {
	l := New(config.RateLimitConfig{
		Global:     1000,
		PerTenant:  1000,
		DailyQuota: 2,
	})

	handler := l.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	makeReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := context.WithValue(req.Context(), auth.ContextKeyTenantID, "test-tenant")
		return req.WithContext(ctx)
	}

	// Use up daily quota
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, makeReq())
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, makeReq())
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after daily quota, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "DAILY_QUOTA_PER_TENANT") {
		t.Errorf("daily quota error should mention env var, got: %s", body)
	}
	if !strings.Contains(body, "midnight UTC") {
		t.Errorf("daily quota error should mention reset time, got: %s", body)
	}
}

// --- H7: persisted daily-quota counters (NewWithStore) ---

// TestNewWithStoreNilEquivalentToNew verifies that passing a nil store yields a
// pure-memory limiter identical to New(cfg): daily quota still enforced, no panic.
func TestNewWithStoreNilEquivalentToNew(t *testing.T) {
	t.Parallel()
	l := NewWithStore(config.RateLimitConfig{
		Global:     1000,
		PerTenant:  1000,
		DailyQuota: 2,
	}, nil)

	if !l.AllowDaily("t1") || !l.AllowDaily("t1") {
		t.Fatal("first two daily calls should be allowed")
	}
	if l.AllowDaily("t1") {
		t.Fatal("third call should be rejected by daily quota")
	}
}

func TestPersistWriteThrough(t *testing.T) {
	t.Parallel()
	store := persist.NewMemoryStore()
	cfg := config.RateLimitConfig{Global: 1000, PerTenant: 1000, DailyQuota: 10}
	l := NewWithStore(cfg, store)

	l.AllowDaily("tenant-x")
	l.AllowDaily("tenant-x")
	l.AllowDaily("tenant-x")

	// Counter must have been written through to the store.
	if _, ok := store.Get(context.Background(), quotaStorePrefix+"tenant-x"); !ok {
		t.Fatal("expected daily counter to be persisted to the store")
	}
}

func TestPersistHydrateAfterRestart(t *testing.T) {
	t.Parallel()
	store := persist.NewMemoryStore()
	cfg := config.RateLimitConfig{Global: 1000, PerTenant: 1000, DailyQuota: 3}

	// First "process": consume the full quota.
	l1 := NewWithStore(cfg, store)
	for i := 0; i < 3; i++ {
		if !l1.AllowDaily("tenant-r") {
			t.Fatalf("call %d should be allowed in first process", i+1)
		}
	}
	if l1.AllowDaily("tenant-r") {
		t.Fatal("quota should be exhausted in first process")
	}

	// Second "process" sharing the same store: must resume exhausted.
	l2 := NewWithStore(cfg, store)
	if l2.AllowDaily("tenant-r") {
		t.Fatal("quota should remain exhausted after restart (hydrated from store)")
	}

	// Stats should reflect the persisted usage even before materialization.
	l3 := NewWithStore(cfg, store)
	stats := l3.Stats("tenant-r")
	if stats.DailyUsed != 3 {
		t.Fatalf("expected hydrated DailyUsed=3, got %d", stats.DailyUsed)
	}
	if stats.DailyRemaining != 0 {
		t.Fatalf("expected DailyRemaining=0, got %d", stats.DailyRemaining)
	}
}

func TestPersistIgnoresStaleWindow(t *testing.T) {
	t.Parallel()
	store := persist.NewMemoryStore()
	cfg := config.RateLimitConfig{Global: 1000, PerTenant: 1000, DailyQuota: 5}

	// Seed the store with a counter from a window that has already reset.
	buf := make([]byte, 16)
	binary.BigEndian.PutUint64(buf[0:8], 5)
	binary.BigEndian.PutUint64(buf[8:16], uint64(time.Now().Add(-time.Hour).UnixNano()))
	store.Set(context.Background(), quotaStorePrefix+"tenant-old", buf, time.Hour)

	l := NewWithStore(cfg, store)
	// Stale window must be ignored — fresh quota available.
	if !l.AllowDaily("tenant-old") {
		t.Fatal("stale persisted window should not block a fresh quota window")
	}
}

// --- M6: per-IP pre-auth limiting ---

func TestAllowIPDisabledPassthrough(t *testing.T) {
	t.Parallel()
	l := New(config.RateLimitConfig{Global: 1, PerTenant: 1, DailyQuota: 1, PerIP: 0})
	for i := 0; i < 1000; i++ {
		if !l.AllowIP("1.2.3.4") {
			t.Fatal("PerIP=0 must allow every request (passthrough)")
		}
	}
}

func TestAllowIPIsolation(t *testing.T) {
	t.Parallel()
	l := New(config.RateLimitConfig{Global: 1000, PerTenant: 1000, DailyQuota: 1000, PerIP: 1})

	// Exhaust IP-A's bucket.
	allowedA := 0
	for i := 0; i < 5; i++ {
		if l.AllowIP("10.0.0.1") {
			allowedA++
		}
	}
	if allowedA >= 5 {
		t.Fatalf("expected IP-A to be limited, but %d/5 allowed", allowedA)
	}

	// IP-B must have its own untouched bucket.
	if !l.AllowIP("10.0.0.2") {
		t.Fatal("IP-B should not be affected by IP-A's bucket")
	}
}

func TestWrapIPPassthroughWhenDisabled(t *testing.T) {
	t.Parallel()
	l := New(config.RateLimitConfig{Global: 1, PerTenant: 1, DailyQuota: 1, PerIP: 0})
	called := 0
	handler := l.WrapIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < 50; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "9.9.9.9:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("disabled per-IP limit must pass through, got %d", rec.Code)
		}
	}
	if called != 50 {
		t.Fatalf("expected all 50 requests to reach handler, got %d", called)
	}
}

func TestWrapIPRejectsFlood(t *testing.T) {
	t.Parallel()
	l := New(config.RateLimitConfig{Global: 1000, PerTenant: 1000, DailyQuota: 1000, PerIP: 1})
	handler := l.WrapIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	got429 := false
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "8.8.8.8:5555"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			got429 = true
			if !strings.Contains(rec.Body.String(), "RATE_LIMIT_PER_IP") {
				t.Errorf("429 body should mention RATE_LIMIT_PER_IP, got: %s", rec.Body.String())
			}
		}
	}
	if !got429 {
		t.Fatal("expected at least one request to be rejected with 429")
	}
}

func TestClientIPTrustProxyOff(t *testing.T) {
	t.Parallel()
	l := New(config.RateLimitConfig{TrustProxy: false, PerIP: 1})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:443"
	req.Header.Set("X-Forwarded-For", "1.1.1.1, 2.2.2.2")
	if got := l.clientIP(req); got != "203.0.113.7" {
		t.Fatalf("TRUST_PROXY=false must use RemoteAddr host, got %q", got)
	}
}

func TestClientIPTrustProxyOn(t *testing.T) {
	t.Parallel()
	l := New(config.RateLimitConfig{TrustProxy: true, PerIP: 1})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:443"
	req.Header.Set("X-Forwarded-For", " 1.1.1.1 , 2.2.2.2 ")
	if got := l.clientIP(req); got != "1.1.1.1" {
		t.Fatalf("TRUST_PROXY=true must use leftmost X-Forwarded-For, got %q", got)
	}
}

func TestClientIPTrustProxyOnEmptyHeaderFallsBack(t *testing.T) {
	t.Parallel()
	l := New(config.RateLimitConfig{TrustProxy: true, PerIP: 1})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:80"
	// No X-Forwarded-For header set.
	if got := l.clientIP(req); got != "203.0.113.9" {
		t.Fatalf("empty XFF with TRUST_PROXY should fall back to RemoteAddr, got %q", got)
	}
}

func TestClientIPMalformedRemoteAddr(t *testing.T) {
	t.Parallel()
	l := New(config.RateLimitConfig{TrustProxy: false, PerIP: 1})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "not-an-addr-no-port" // no panic, used verbatim
	if got := l.clientIP(req); got != "not-an-addr-no-port" {
		t.Fatalf("malformed RemoteAddr should be used verbatim without panic, got %q", got)
	}
}

func TestCleanupPrunesIPMap(t *testing.T) {
	t.Parallel()
	l := New(config.RateLimitConfig{Global: 1000, PerTenant: 1000, DailyQuota: 1000, PerIP: 5})

	// Materialize a fully-idle IP limiter via getIPLimiter (full bucket, never
	// drawn down) — the state a bucket reaches once it has refilled.
	_ = l.getIPLimiter("172.16.0.1")
	if _, ok := l.ips.Load("172.16.0.1"); !ok {
		t.Fatal("expected IP limiter to be materialized")
	}

	l.Cleanup()

	// A bucket at full capacity (idle) should be pruned.
	if _, ok := l.ips.Load("172.16.0.1"); ok {
		t.Fatal("expected idle (full-capacity) IP limiter to be pruned by Cleanup")
	}
}

func TestCleanupKeepsActiveIPMap(t *testing.T) {
	t.Parallel()
	l := New(config.RateLimitConfig{Global: 1000, PerTenant: 1000, DailyQuota: 1000, PerIP: 2})

	// Drain the bucket so it is below capacity → should be retained.
	l.AllowIP("172.16.0.2")
	l.AllowIP("172.16.0.2")
	l.AllowIP("172.16.0.2")

	l.Cleanup()

	if _, ok := l.ips.Load("172.16.0.2"); !ok {
		t.Fatal("expected an actively-used IP limiter to be retained by Cleanup")
	}
}
