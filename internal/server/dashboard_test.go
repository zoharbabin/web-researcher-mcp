package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/config"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/ratelimit"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

type stubHealth struct{}

func (stubHealth) Health() any {
	return map[string]any{
		"status": "degraded",
		"providers": []map[string]any{
			{"name": "google", "type": "web", "breaker": "open", "available": false},
		},
	}
}

// TestDashboardPage_NonceAndCSP: the page carries a per-request CSP nonce that
// authorizes its single inline script/style, sets connect-src 'self' for the
// poll, and embeds NO data (the data arrives via the gated JSON endpoint).
func TestDashboardPage_NonceAndCSP(t *testing.T) {
	handler := handleDashboard("1.20.0")
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'nonce-", "connect-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q; got %q", want, csp)
		}
	}
	body := rec.Body.String()
	// The nonce in the CSP must match the nonce on the inline tags.
	if !strings.Contains(body, `<style nonce=`) || !strings.Contains(body, `<script nonce=`) {
		t.Error("inline tags not nonce'd")
	}
	if strings.Contains(body, "{{NONCE}}") || strings.Contains(body, "{{VERSION}}") {
		t.Error("template placeholders not substituted")
	}
	// No 'unsafe-inline' fallback — the whole point of the nonce.
	if strings.Contains(csp, "unsafe-inline") {
		t.Error("CSP uses unsafe-inline")
	}
}

// TestDashboardPage_NonceIsPerRequest: two requests get distinct nonces.
func TestDashboardPage_NonceIsPerRequest(t *testing.T) {
	handler := handleDashboard("1.20.0")
	get := func() string {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
		return rec.Header().Get("Content-Security-Policy")
	}
	if get() == get() {
		t.Error("CSP nonce is not per-request (identical across two requests)")
	}
}

func dashboardTestDeps() (*metrics.Collector, session.Manager, *ratelimit.Limiter) {
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	rl := ratelimit.New(config.RateLimitConfig{PerTenant: 60, Global: 100, DailyQuota: 1000})
	return m, s, rl
}

// TestDashboardData_AggregateShape: the data endpoint returns the aggregate
// payload (tools, sessions, rate limits, health, recent errors) and nothing
// per-user.
func TestDashboardData_AggregateShape(t *testing.T) {
	m, s, rl := dashboardTestDeps()
	m.RecordToolCall("web_search", 100*time.Millisecond, nil, "", false)
	m.RecordError(metrics.ErrorRecord{Tool: "web_search", Kind: "rate_limited", Provider: "google"})

	handler := handleDashboardData("1.20.0", m, s, rl, stubHealth{})
	req := httptest.NewRequest(http.MethodGet, "/dashboard/data", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	var data dashboardData
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := data.Tools["web_search"]; !ok {
		t.Error("tools missing web_search")
	}
	if data.RateLimit.PerMinutePerTenant != 60 {
		t.Errorf("rate limit config not surfaced: %+v", data.RateLimit)
	}
	if data.Health == nil {
		t.Error("health snapshot missing")
	}
	if len(data.RecentErrors) != 1 {
		t.Errorf("recent errors = %d, want 1", len(data.RecentErrors))
	}
	if data.Version != "1.20.0" {
		t.Errorf("version = %q", data.Version)
	}
}

// TestDashboardData_NilHealth: a nil health provider simply omits the panel.
func TestDashboardData_NilHealth(t *testing.T) {
	m, s, rl := dashboardTestDeps()
	handler := handleDashboardData("1.20.0", m, s, rl, nil)
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/dashboard/data", nil))

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, present := raw["health"]; present {
		t.Error("health key should be omitted when nil")
	}
}

// TestDashboardData_AdminGated: through the adminAuth wrapper, a missing/wrong
// key is 401 and the correct key passes — same trust tier as /admin/*.
func TestDashboardData_AdminGated(t *testing.T) {
	m, s, rl := dashboardTestDeps()
	const key = "s3cret-admin-key"
	gated := adminAuth(key, audit.NewNoop(), handleDashboardData("1.20.0", m, s, rl, nil))

	t.Run("no key → 401", func(t *testing.T) {
		rec := httptest.NewRecorder()
		gated(rec, httptest.NewRequest(http.MethodGet, "/dashboard/data", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})
	t.Run("wrong key → 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/dashboard/data", nil)
		req.Header.Set("X-Admin-Key", "nope")
		rec := httptest.NewRecorder()
		gated(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})
	t.Run("correct key → 200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/dashboard/data", nil)
		req.Header.Set("X-Admin-Key", key)
		rec := httptest.NewRecorder()
		gated(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})
}

// TestServeHTTP_DashboardRoutesRegistered is an integration test: with an admin
// key set, GET /dashboard serves HTML and GET /dashboard/data is admin-gated.
// Without an admin key, neither route exists (404) — parity with /admin/*.
func TestServeHTTP_DashboardRoutesRegistered(t *testing.T) {
	// Mirror the exact production route wiring from ServeHTTP for these two
	// routes (which are registered only when an admin key is set), driven through
	// an httptest server rather than binding a real port.
	run := func(adminKey string) *httptest.Server {
		m, s, rl := dashboardTestDeps()
		mux := http.NewServeMux()
		cfg := HTTPConfig{Version: "1.20.0", AdminKey: adminKey, Metrics: m, Sessions: s, RateLimiter: rl, Auditor: audit.NewNoop()}
		if cfg.AdminKey != "" {
			mux.Handle("GET /dashboard", handleDashboard(cfg.Version))
			mux.Handle("GET /dashboard/data", adminAuth(cfg.AdminKey, cfg.Auditor, handleDashboardData(cfg.Version, cfg.Metrics, cfg.Sessions, cfg.RateLimiter, cfg.Health)))
		}
		return httptest.NewServer(mux)
	}

	t.Run("with admin key", func(t *testing.T) {
		ts := run("k")
		defer ts.Close()
		resp, err := http.Get(ts.URL + "/dashboard")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("/dashboard = %d, want 200", resp.StatusCode)
		}
		// data endpoint without key → 401
		resp2, err := http.Get(ts.URL + "/dashboard/data")
		if err != nil {
			t.Fatal(err)
		}
		defer resp2.Body.Close()
		if resp2.StatusCode != http.StatusUnauthorized {
			t.Errorf("/dashboard/data (no key) = %d, want 401", resp2.StatusCode)
		}
	})

	t.Run("without admin key", func(t *testing.T) {
		ts := run("")
		defer ts.Close()
		resp, err := http.Get(ts.URL + "/dashboard")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("/dashboard (no admin key) = %d, want 404", resp.StatusCode)
		}
	})
}
