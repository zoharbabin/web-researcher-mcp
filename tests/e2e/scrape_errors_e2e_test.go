//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScrapeErrors_E2E(t *testing.T) {
	// These subtests point the scraper at local httptest servers (127.0.0.1) to
	// exercise per-status error handling. Allow private IPs so the SSRF guard
	// doesn't block the loopback target before the scraper sees the response.
	h := newMCPTestHarness(t, "ALLOW_PRIVATE_IPS=true")

	// Initialize
	h.send(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "e2e-error-test", "version": "1.0.0"},
		},
	})
	resp := h.readResponse()
	if resp.Error != nil {
		t.Fatalf("init error: %s", resp.Error)
	}

	h.send(jsonRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"})

	t.Run("403_returns_blocked_error_with_hint", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer ts.Close()

		h.send(jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      10,
			Method:  "tools/call",
			Params: map[string]interface{}{
				"name":      "scrape_page",
				"arguments": map[string]interface{}{"url": ts.URL},
			},
		})

		resp := h.readResponse()
		if resp.ID != float64(10) {
			t.Fatalf("expected ID 10, got %v", resp.ID)
		}

		var result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("parse error: %v", err)
		}

		if !result.IsError {
			t.Fatal("expected isError=true for 403")
		}

		text := result.Content[0].Text
		if !strings.Contains(text, "bot detection") {
			t.Errorf("LLM should see 'bot detection' hint, got: %s", text)
		}
		// A 403 is the remote site refusing us, not a server bug — no issue link expected.
		if strings.Contains(text, "github.com/zoharbabin/web-researcher-mcp/issues") {
			t.Errorf("LLM should NOT see GitHub issue link for a bot-wall 403, got: %s", text)
		}
		t.Logf("LLM sees: %s", text)
	})

	t.Run("401_returns_auth_error_without_issue_link", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer ts.Close()

		h.send(jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      11,
			Method:  "tools/call",
			Params: map[string]interface{}{
				"name":      "scrape_page",
				"arguments": map[string]interface{}{"url": ts.URL},
			},
		})

		resp := h.readResponse()
		var result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		}
		json.Unmarshal(resp.Result, &result)

		if !result.IsError {
			t.Fatal("expected isError=true for 401")
		}

		text := result.Content[0].Text
		if !strings.Contains(text, "login wall") {
			t.Errorf("LLM should see auth guidance, got: %s", text)
		}
		if strings.Contains(text, "github.com/zoharbabin/web-researcher-mcp/issues") {
			t.Errorf("auth errors should NOT suggest filing issues, got: %s", text)
		}
		t.Logf("LLM sees: %s", text)
	})

	t.Run("429_returns_rate_limit_with_retry_hint", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer ts.Close()

		h.send(jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      12,
			Method:  "tools/call",
			Params: map[string]interface{}{
				"name":      "scrape_page",
				"arguments": map[string]interface{}{"url": ts.URL},
			},
		})

		resp := h.readResponse()
		var result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		}
		json.Unmarshal(resp.Result, &result)

		if !result.IsError {
			t.Fatal("expected isError=true for 429")
		}

		text := result.Content[0].Text
		if !strings.Contains(text, "rate limited") || !strings.Contains(text, "60 seconds") {
			t.Errorf("LLM should see rate limit with retry hint, got: %s", text)
		}
		t.Logf("LLM sees: %s", text)
	})

	t.Run("thin_content_returns_diagnostic_with_tier_details", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<html><body>tiny</body></html>`))
		}))
		defer ts.Close()

		h.send(jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      13,
			Method:  "tools/call",
			Params: map[string]interface{}{
				"name":      "scrape_page",
				"arguments": map[string]interface{}{"url": ts.URL},
			},
		})

		resp := h.readResponse()
		var result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		}
		json.Unmarshal(resp.Result, &result)

		if !result.IsError {
			t.Fatal("expected isError=true for thin content")
		}

		text := result.Content[0].Text
		// Should show per-tier diagnostics
		if !strings.Contains(text, "no content extracted") {
			t.Errorf("LLM should see diagnostic, got: %s", text)
		}
		if !strings.Contains(text, "github.com/zoharbabin/web-researcher-mcp/issues") {
			t.Errorf("content errors should suggest filing issue, got: %s", text)
		}
		t.Logf("LLM sees: %s", text)
	})

	t.Run("successful_scrape_unaffected", func(t *testing.T) {
		content := strings.Repeat("This is a full article with enough content to pass quality threshold. ", 20)
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<html><head><title>Good Page</title></head><body><article>` + content + `</article></body></html>`))
		}))
		defer ts.Close()

		h.send(jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      14,
			Method:  "tools/call",
			Params: map[string]interface{}{
				"name":      "scrape_page",
				"arguments": map[string]interface{}{"url": ts.URL},
			},
		})

		resp := h.readResponse()
		var result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		}
		json.Unmarshal(resp.Result, &result)

		if result.IsError {
			t.Fatalf("successful scrape should not error, got: %s", result.Content[0].Text)
		}

		text := result.Content[0].Text
		if !strings.Contains(text, "article") {
			t.Errorf("expected article content, got: %s", text[:200])
		}
	})

	t.Run("search_and_scrape_all_fail_shows_diagnostics", func(t *testing.T) {
		// This will search (and fail because fake API key) but tests the tool registration
		h.send(jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      15,
			Method:  "tools/call",
			Params: map[string]interface{}{
				"name":      "search_and_scrape",
				"arguments": map[string]interface{}{"query": "test"},
			},
		})

		resp := h.readResponse()
		// With fake keys, search itself fails — that's fine, we just verify no crash
		if resp.ID != float64(15) {
			t.Fatalf("expected ID 15, got %v", resp.ID)
		}
		var result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		}
		json.Unmarshal(resp.Result, &result)
		t.Logf("search_and_scrape response: isError=%v, text=%s", result.IsError, result.Content[0].Text[:min(200, len(result.Content[0].Text))])
	})

	t.Run("shutdown", func(t *testing.T) {
		h.shutdown()
	})
}
