package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
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
