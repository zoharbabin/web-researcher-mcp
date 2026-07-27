//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestMonitorQuery_E2E drives monitor_query_save → monitor_query_check over
// the real MCP transport and proves research monitoring (#273) end-to-end:
// registered only when MONITORING_ENABLED, consent-gated (denied without a
// grant), and the save→check diff actually narrows to just-new URLs against a
// deterministic local SearXNG-shaped httptest server (real DuckDuckGo/Google
// results aren't stable enough to assert "these exact URLs are new").
func TestMonitorQuery_E2E(t *testing.T) {
	// Two fixed responses: monitor_query_save baselines against the first
	// (both URLs "seen"); monitor_query_check re-runs against the second,
	// which adds one new URL — that's the only one that should come back new.
	first := `{"results":[{"title":"A","url":"https://example.com/a","content":"a"},{"title":"B","url":"https://example.com/b","content":"b"}]}`
	second := `{"results":[{"title":"A","url":"https://example.com/a","content":"a"},{"title":"B","url":"https://example.com/b","content":"b"},{"title":"C","url":"https://example.com/c","content":"c"}]}`
	call := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		w.Header().Set("Content-Type", "application/json")
		if call == 1 {
			_, _ = w.Write([]byte(first))
			return
		}
		_, _ = w.Write([]byte(second))
	}))
	defer ts.Close()

	h := newMCPTestHarness(t,
		"ALLOW_PRIVATE_IPS=true",
		"MONITORING_ENABLED=true",
		"STDIO_USER_ID=monitor-e2e-tester",
		"SEARCH_PROVIDER=searxng",
		"SEARXNG_URL="+ts.URL,
	)

	h.send(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "e2e-monitor-test", "version": "1.0.0"},
		},
	})
	if resp := h.readResponse(); resp.Error != nil {
		t.Fatalf("initialize returned error: %s", resp.Error)
	}

	h.send(jsonRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"})

	parseTool := func(resp jsonRPCResponse) map[string]any {
		t.Helper()
		var res struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(resp.Result, &res); err != nil || len(res.Content) == 0 {
			t.Fatalf("bad tool result: %v raw=%s", err, resp.Result)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
			t.Fatalf("tool output not JSON: %v raw=%s", err, res.Content[0].Text)
		}
		return out
	}

	t.Run("ListedInTools", func(t *testing.T) {
		h.send(jsonRPCRequest{JSONRPC: "2.0", ID: 2, Method: "tools/list"})
		resp := h.readResponse()
		if resp.Error != nil {
			t.Fatalf("tools/list returned error: %s", resp.Error)
		}
		var result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("failed to parse tools result: %v", err)
		}
		var haveSave, haveCheck bool
		for _, tool := range result.Tools {
			if tool.Name == "monitor_query_save" {
				haveSave = true
			}
			if tool.Name == "monitor_query_check" {
				haveCheck = true
			}
		}
		if !haveSave || !haveCheck {
			t.Errorf("monitor_query_save/monitor_query_check should be registered when MONITORING_ENABLED=true, got save=%v check=%v", haveSave, haveCheck)
		}
	})

	t.Run("SaveBaselinesThenCheckReturnsOnlyNewURL", func(t *testing.T) {
		h.send(jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      3,
			Method:  "tools/call",
			Params: map[string]interface{}{
				"name":      "monitor_query_save",
				"arguments": map[string]interface{}{"query": "test monitor query"},
			},
		})
		resp := h.readResponse()
		if resp.Error != nil {
			t.Fatalf("monitor_query_save transport error: %s", resp.Error)
		}
		save := parseTool(resp)
		if save["status"] != "ok" {
			t.Fatalf("monitor_query_save should succeed with STDIO_USER_ID + consent auto-grant, got status=%v reason=%v", save["status"], save["reason"])
		}
		if seenCount, _ := save["seenCount"].(float64); seenCount != 2 {
			t.Fatalf("expected seenCount=2 from the baseline fixture, got %v", save["seenCount"])
		}

		h.send(jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      4,
			Method:  "tools/call",
			Params: map[string]interface{}{
				"name":      "monitor_query_check",
				"arguments": map[string]interface{}{"query": "test monitor query"},
			},
		})
		resp = h.readResponse()
		if resp.Error != nil {
			t.Fatalf("monitor_query_check transport error: %s", resp.Error)
		}
		check := parseTool(resp)
		if check["status"] != "ok" {
			t.Fatalf("monitor_query_check should succeed, got status=%v reason=%v", check["status"], check["reason"])
		}
		if newCount, _ := check["newCount"].(float64); newCount != 1 {
			t.Fatalf("expected exactly 1 new result (the fixture added one URL), got %v", check["newCount"])
		}
		if check["trust"] != "untrusted-external-content" {
			t.Errorf("monitor_query_check trust = %v, want untrusted-external-content", check["trust"])
		}
	})

	t.Run("CheckIsIdempotentOnSecondCallWithNoUpstreamChange", func(t *testing.T) {
		h.send(jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      5,
			Method:  "tools/call",
			Params: map[string]interface{}{
				"name":      "monitor_query_check",
				"arguments": map[string]interface{}{"query": "test monitor query"},
			},
		})
		resp := h.readResponse()
		if resp.Error != nil {
			t.Fatalf("monitor_query_check transport error: %s", resp.Error)
		}
		check := parseTool(resp)
		if newCount, _ := check["newCount"].(float64); newCount != 0 {
			t.Fatalf("second consecutive check against the same fixture should report zero new results, got %v", check["newCount"])
		}
	})

	t.Run("Shutdown", func(t *testing.T) {
		h.shutdown()
	})
}

// TestMonitorQuery_DeniedWithoutConsent proves monitor_query_save fails
// closed when monitoring is enabled but the caller has no recorded consent
// for the 'monitoring' purpose — the same STDIO auto-grant path as memory
// (TestSecurity_STDIO_UserIdentity) but exercising monitor's own gate, since
// #273 is the first sub-issue to introduce this Purpose end-to-end.
func TestMonitorQuery_DeniedWithoutConsent(t *testing.T) {
	h := newMCPTestHarness(t,
		"MONITORING_ENABLED=true",
		// no STDIO_USER_ID → anonymous → unauthenticated, not just unconsented
	)

	h.send(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "e2e-monitor-denied-test", "version": "1.0.0"},
		},
	})
	if resp := h.readResponse(); resp.Error != nil {
		t.Fatalf("initialize returned error: %s", resp.Error)
	}
	h.send(jsonRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"})

	h.send(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name":      "monitor_query_save",
			"arguments": map[string]interface{}{"query": "test monitor query"},
		},
	})
	resp := h.readResponse()
	if resp.Error != nil {
		t.Fatalf("monitor_query_save transport error: %s", resp.Error)
	}
	var res struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp.Result, &res); err != nil || len(res.Content) == 0 {
		t.Fatalf("bad tool result: %v raw=%s", err, resp.Result)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("tool output not JSON: %v raw=%s", err, res.Content[0].Text)
	}
	if out["status"] != "unavailable" {
		t.Fatalf("monitor_query_save must deny an anonymous caller even with MONITORING_ENABLED=true, got status=%v", out["status"])
	}

	h.shutdown()
}
