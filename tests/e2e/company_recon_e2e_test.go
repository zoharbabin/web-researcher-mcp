//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"
)

// TestCompanyRecon_E2E drives company_recon over the real MCP transport.
// It stays hermetic (no live crt.sh/Wayback CDX calls) by asserting the
// in-handler private-host guard, the same pattern TestMCPLifecycle uses for
// archive_source: proving the tool is registered, reachable, and validates
// input end-to-end without depending on external network availability.
func TestCompanyRecon_E2E(t *testing.T) {
	h := newMCPTestHarness(t)

	h.send(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "e2e-company-recon-test", "version": "1.0.0"},
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
			if tool.Name == "company_recon" {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("company_recon should be registered in tools/list")
		}
	})

	t.Run("RejectsPrivateHost", func(t *testing.T) {
		h.send(jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      3,
			Method:  "tools/call",
			Params: map[string]interface{}{
				"name":      "company_recon",
				"arguments": map[string]interface{}{"target": "site.internal"},
			},
		})
		resp := h.readResponse()
		if resp.Error != nil {
			t.Fatalf("company_recon transport error: %s", resp.Error)
		}
		var result struct {
			IsError bool `json:"isError"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("parse company_recon result: %v", err)
		}
		if !result.IsError {
			t.Error("company_recon should refuse a private/internal host with a tool error")
		}
	})

	t.Run("Shutdown", func(t *testing.T) {
		h.shutdown()
	})
}
