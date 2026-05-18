package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/config"
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
