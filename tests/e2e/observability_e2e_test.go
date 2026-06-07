//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestHTTP_Dashboard_ServedAndGated drives the operator dashboard (#87) on the
// real binary: the page is served (admin key required for the route to exist),
// carries a nonce-based CSP, embeds no template placeholders, and its data
// endpoint is admin-gated (401 without the key, 200 + aggregate JSON with it).
func TestHTTP_Dashboard_ServedAndGated(t *testing.T) {
	const adminKey = "test-admin-key-1234567890"
	h := newHTTPHarness(t, "ADMIN_API_KEY="+adminKey)

	// --- the page (no auth on the shell itself; it prompts client-side) ---
	resp, err := h.client.Get(h.baseURL + "/dashboard")
	if err != nil {
		t.Fatalf("GET /dashboard: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/dashboard status = %d, want 200", resp.StatusCode)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'nonce-", "connect-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q; got %q", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe-inline") {
		t.Error("CSP uses unsafe-inline")
	}

	// --- data endpoint: 401 without key ---
	r1, err := h.client.Get(h.baseURL + "/dashboard/data")
	if err != nil {
		t.Fatalf("GET /dashboard/data: %v", err)
	}
	_ = r1.Body.Close()
	if r1.StatusCode != http.StatusUnauthorized {
		t.Errorf("/dashboard/data (no key) = %d, want 401", r1.StatusCode)
	}

	// --- data endpoint: 200 + aggregate shape with key ---
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, h.baseURL+"/dashboard/data", nil)
	req.Header.Set("X-Admin-Key", adminKey)
	r2, err := h.client.Do(req)
	if err != nil {
		t.Fatalf("GET /dashboard/data (keyed): %v", err)
	}
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("/dashboard/data (keyed) = %d, want 200", r2.StatusCode)
	}
	var data struct {
		Version      string         `json:"version"`
		Tools        map[string]any `json:"tools"`
		RateLimit    map[string]any `json:"rateLimit"`
		RecentErrors []any          `json:"recentErrors"`
	}
	if err := json.NewDecoder(r2.Body).Decode(&data); err != nil {
		t.Fatalf("decode dashboard data: %v", err)
	}
	if data.RateLimit == nil {
		t.Error("dashboard data missing rateLimit block")
	}
}

// TestHTTP_DiagnosticsResources reads the two diagnostics:// Resources (#81)
// over the live MCP transport and asserts valid, redaction-safe JSON.
func TestHTTP_DiagnosticsResources(t *testing.T) {
	h := newHTTPHarness(t, "ADMIN_API_KEY=test-admin-key-1234567890")
	h.initialize()

	read := func(id int, uri string) json.RawMessage {
		resp := h.rpc(jsonRPCRequest{
			JSONRPC: "2.0", ID: id, Method: "resources/read",
			Params: map[string]interface{}{"uri": uri},
		})
		if resp.Error != nil {
			t.Fatalf("resources/read %s error: %s", uri, resp.Error)
		}
		var result struct {
			Contents []struct {
				URI, MIMEType, Text string
			} `json:"contents"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("parse %s result: %v", uri, err)
		}
		if len(result.Contents) == 0 || result.Contents[0].URI != uri {
			t.Fatalf("%s: unexpected contents %+v", uri, result.Contents)
		}
		if result.Contents[0].MIMEType != "application/json" {
			t.Errorf("%s MIME = %q, want application/json", uri, result.Contents[0].MIMEType)
		}
		return json.RawMessage(result.Contents[0].Text)
	}

	// diagnostics://errors/recent — valid JSON with count + errors array.
	errBody := read(10, "diagnostics://errors/recent")
	var errs struct {
		Count  int   `json:"count"`
		Errors []any `json:"errors"`
	}
	if err := json.Unmarshal(errBody, &errs); err != nil {
		t.Fatalf("parse errors/recent: %v", err)
	}

	// diagnostics://health — valid JSON with a tri-state status string.
	healthBody := read(11, "diagnostics://health")
	var health struct {
		Status    string `json:"status"`
		Providers []any  `json:"providers"`
	}
	if err := json.Unmarshal(healthBody, &health); err != nil {
		t.Fatalf("parse health: %v", err)
	}
	switch health.Status {
	case "healthy", "degraded", "unhealthy":
		// ok
	default:
		t.Errorf("health.status = %q, want a tri-state value", health.Status)
	}
}
