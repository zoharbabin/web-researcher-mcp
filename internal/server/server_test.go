package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/config"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/ratelimit"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

func TestNew(t *testing.T) {
	s := New(Config{Name: "test-server", Version: "1.0.0"})
	if s == nil {
		t.Fatal("expected non-nil server")
	}
	if s.MCP() == nil {
		t.Fatal("expected non-nil MCP server")
	}
}

func TestSecurityHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := securityHeaders(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	headers := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Cache-Control":             "no-store",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	}

	for key, want := range headers {
		got := rec.Header().Get(key)
		if got != want {
			t.Errorf("header %s = %q, want %q", key, got, want)
		}
	}
}

func TestCORSMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("allowed origin", func(t *testing.T) {
		handler := corsMiddleware([]string{"https://app.example.com"}, inner)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://app.example.com")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
			t.Fatalf("expected origin header, got %q", got)
		}
	})

	t.Run("disallowed origin", func(t *testing.T) {
		handler := corsMiddleware([]string{"https://app.example.com"}, inner)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://evil.com")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("expected no origin header, got %q", got)
		}
	})

	t.Run("wildcard origin", func(t *testing.T) {
		handler := corsMiddleware([]string{"*"}, inner)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://anything.com")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://anything.com" {
			t.Fatalf("expected origin, got %q", got)
		}
	})

	t.Run("preflight OPTIONS", func(t *testing.T) {
		handler := corsMiddleware([]string{"*"}, inner)
		req := httptest.NewRequest(http.MethodOptions, "/", nil)
		req.Header.Set("Origin", "https://app.example.com")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", rec.Code)
		}
	})

	t.Run("no origin allowed", func(t *testing.T) {
		handler := corsMiddleware(nil, inner)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://anything.com")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://anything.com" {
			t.Fatalf("expected origin when no restrictions, got %q", got)
		}
	})
}

func TestAdminFlushCache(t *testing.T) {
	c := cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 1})
	c.Set(nil, "key1", []byte("val1"), time.Hour)

	handler := handleAdminFlushCache(c)
	req := httptest.NewRequest(http.MethodDelete, "/admin/cache", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if _, ok := c.Get(nil, "key1"); ok {
		t.Fatal("expected cache to be flushed")
	}
}

func TestAdminFlushCacheNil(t *testing.T) {
	handler := handleAdminFlushCache(nil)
	req := httptest.NewRequest(http.MethodDelete, "/admin/cache", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 even with nil cache, got %d", rec.Code)
	}
}

func TestAdminFlushSessions(t *testing.T) {
	mgr := session.NewManager(session.Config{MaxSessions: 10, SessionTTL: time.Hour})
	defer mgr.Close()

	_, _ = mgr.Create("tenant-1")
	_, _ = mgr.Create("tenant-1")

	if mgr.ActiveCount() != 2 {
		t.Fatalf("expected 2 sessions, got %d", mgr.ActiveCount())
	}

	handler := handleAdminFlushSessions(mgr)
	req := httptest.NewRequest(http.MethodDelete, "/admin/sessions", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if mgr.ActiveCount() != 0 {
		t.Fatalf("expected 0 sessions after flush, got %d", mgr.ActiveCount())
	}
}

func TestAdminFlushSessionsNil(t *testing.T) {
	handler := handleAdminFlushSessions(nil)
	req := httptest.NewRequest(http.MethodDelete, "/admin/sessions", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 even with nil manager, got %d", rec.Code)
	}
}

func TestAdminAuth(t *testing.T) {
	handler := adminAuth("secret-key", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("valid key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/admin/cache", nil)
		req.Header.Set("X-Admin-Key", "secret-key")
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("invalid key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/admin/cache", nil)
		req.Header.Set("X-Admin-Key", "wrong-key")
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("missing key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/admin/cache", nil)
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})
}

// =============================================================================
// Full HTTP Server Integration Tests
// =============================================================================

func buildTestHTTPServer(t *testing.T) *httptest.Server {
	t.Helper()

	metricsCollector := metrics.NewCollector()
	c := cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 1})
	mgr := session.NewManager(session.Config{MaxSessions: 10, SessionTTL: time.Hour})

	mux := http.NewServeMux()

	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ready")
	})
	mux.Handle("GET /metrics", metricsCollector.HTTPHandler())
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"issuer":"web-researcher-mcp","token_endpoint":"n/a"}`)
	})
	mux.HandleFunc("DELETE /admin/cache", adminAuth("test-admin-key", handleAdminFlushCache(c)))
	mux.HandleFunc("DELETE /admin/sessions", adminAuth("test-admin-key", handleAdminFlushSessions(mgr)))

	handler := securityHeaders(corsMiddleware([]string{"https://allowed.example.com"}, mux))
	return httptest.NewServer(handler)
}

func TestHealthLiveEndpoint(t *testing.T) {
	ts := buildTestHTTPServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health/live")
	if err != nil {
		t.Fatalf("failed to GET /health/live: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("expected body 'ok', got %q", body)
	}
}

func TestHealthReadyEndpoint(t *testing.T) {
	ts := buildTestHTTPServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health/ready")
	if err != nil {
		t.Fatalf("failed to GET /health/ready: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ready" {
		t.Fatalf("expected body 'ready', got %q", body)
	}
}

func TestHealthEndpointSecurityHeaders(t *testing.T) {
	ts := buildTestHTTPServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health/live")
	if err != nil {
		t.Fatalf("failed to GET /health/live: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("expected X-Content-Type-Options: nosniff, got %q", got)
	}
	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("expected X-Frame-Options: DENY, got %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("expected Cache-Control: no-store, got %q", got)
	}
}

func TestOAuthWellKnownEndpoint(t *testing.T) {
	ts := buildTestHTTPServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatalf("failed to GET /.well-known/oauth-authorization-server: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type: application/json, got %q", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	var oauthResp map[string]any
	if err := json.Unmarshal(body, &oauthResp); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	if oauthResp["issuer"] != "web-researcher-mcp" {
		t.Errorf("expected issuer 'web-researcher-mcp', got %v", oauthResp["issuer"])
	}
	if oauthResp["token_endpoint"] != "n/a" {
		t.Errorf("expected token_endpoint 'n/a', got %v", oauthResp["token_endpoint"])
	}
}

func TestCORSNoOriginHeader(t *testing.T) {
	ts := buildTestHTTPServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/health/live", nil)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no CORS headers without Origin, got %q", got)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestCORSWithAllowedOrigin(t *testing.T) {
	ts := buildTestHTTPServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/health/live", nil)
	req.Header.Set("Origin", "https://allowed.example.com")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://allowed.example.com" {
		t.Fatalf("expected CORS origin header, got %q", got)
	}
}

func TestCORSWithDisallowedOrigin(t *testing.T) {
	ts := buildTestHTTPServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/health/live", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no CORS header for disallowed origin, got %q", got)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	ts := buildTestHTTPServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("failed to GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Fatal("expected non-empty metrics body")
	}
}

func TestAdminCacheFlushIntegration(t *testing.T) {
	ts := buildTestHTTPServer(t)
	defer ts.Close()

	client := &http.Client{Timeout: 5 * time.Second}

	t.Run("with valid key", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/admin/cache", nil)
		req.Header.Set("X-Admin-Key", "test-admin-key")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})

	t.Run("with invalid key", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/admin/cache", nil)
		req.Header.Set("X-Admin-Key", "wrong-key")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
	})
}

func TestServeHTTP_ContextCancellation(t *testing.T) {
	s := New(Config{Name: "test-server", Version: "1.0.0"})
	metricsCollector := metrics.NewCollector()
	authMw := auth.NewMiddleware(config.OAuthConfig{})
	limiter := ratelimit.New(config.RateLimitConfig{
		Global:     100,
		PerTenant:  50,
		DailyQuota: 10000,
	})

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.ServeHTTP(ctx, HTTPConfig{
			Port:           0,
			Auth:           authMw,
			RateLimiter:    limiter,
			AllowedOrigins: []string{"*"},
			Metrics:        metricsCollector,
			AdminKey:       "key",
			Cache:          cache.NewNoop(),
			Sessions:       session.NewManager(session.Config{MaxSessions: 10, SessionTTL: time.Hour}),
		})
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Logf("server returned error (acceptable): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after context cancellation")
	}
}
