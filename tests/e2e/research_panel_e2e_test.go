//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"
)

// TestResearchPanel_E2E proves research_panel (#302) is registered and
// reachable end-to-end over the real MCP transport. research_panel only
// registers when at least one panel member resolves (registerResearchPanel
// returns early on an empty deps.ResearchPanelProviders — see
// internal/tools/research_panel.go), so this harness sets ALLOW_PRIVATE_IPS=1,
// which makes AvailableModelProviders auto-add a local Ollama provider
// (internal/tools/research_panel_providers.go) without requiring any real LLM
// credentials. Stays hermetic (no live model call) by asserting only the
// in-handler input-validation path, the same pattern TestFindings434_E2E uses.
func TestResearchPanel_E2E(t *testing.T) {
	h := newMCPTestHarness(t, "ALLOW_PRIVATE_IPS=1")

	h.send(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "e2e-research-panel-test", "version": "1.0.0"},
		},
	})
	if resp := h.readResponse(); resp.Error != nil {
		t.Fatalf("initialize returned error: %s", resp.Error)
	}

	h.send(jsonRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"})

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
		found := false
		for _, tool := range result.Tools {
			if tool.Name == "research_panel" {
				found = true
			}
		}
		if !found {
			t.Errorf("research_panel should be registered in tools/list when a panel member resolves")
		}
	})

	t.Run("RejectsEmptyQuery", func(t *testing.T) {
		h.send(jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      3,
			Method:  "tools/call",
			Params: map[string]interface{}{
				"name":      "research_panel",
				"arguments": map[string]interface{}{"query": "  "},
			},
		})
		resp := h.readResponse()
		if resp.Error != nil {
			t.Fatalf("transport error: %s", resp.Error)
		}
		var result struct {
			IsError bool `json:"isError"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("parse result: %v", err)
		}
		if !result.IsError {
			t.Errorf("research_panel should reject a blank query with a tool error")
		}
	})

	t.Run("Shutdown", func(t *testing.T) {
		h.shutdown()
	})
}
