//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"
)

// TestFindings434_E2E proves that all 4 tools touched by issue #434
// (citation_graph, filing_search, econ_search, verify_recommendation) are
// registered and reachable end-to-end over the real MCP transport. Stays
// hermetic (no live OpenAlex/EDGAR/FRED/search-provider calls) by asserting
// each tool's in-handler input-validation path, the same pattern
// TestCompanyRecon_E2E uses for company_recon: proving the tool is wired all
// the way from transport to handler without depending on external network
// availability.
//
// citation_graph and filing_search only register when their provider deps
// are configured (OPENALEX_EMAIL / EDGAR_CONTACT_EMAIL respectively — see
// internal/tools/registry.go's hasCitationProvider and FilingProviders
// checks), so this harness sets both.
func TestFindings434_E2E(t *testing.T) {
	h := newMCPTestHarness(t, "OPENALEX_EMAIL=e2e-test@example.com", "EDGAR_CONTACT_EMAIL=e2e-test@example.com")

	h.send(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "e2e-findings-434-test", "version": "1.0.0"},
		},
	})
	if resp := h.readResponse(); resp.Error != nil {
		t.Fatalf("initialize returned error: %s", resp.Error)
	}

	h.send(jsonRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"})

	toolNames := []string{"citation_graph", "filing_search", "econ_search", "verify_recommendation"}

	t.Run("AllListedInTools", func(t *testing.T) {
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
		found := map[string]bool{}
		for _, tool := range result.Tools {
			found[tool.Name] = true
		}
		for _, name := range toolNames {
			if !found[name] {
				t.Errorf("%s should be registered in tools/list", name)
			}
		}
	})

	cases := []struct {
		tool string
		args map[string]interface{}
	}{
		{"citation_graph", map[string]interface{}{}},
		{"filing_search", map[string]interface{}{}},
		{"econ_search", map[string]interface{}{}},
		{"verify_recommendation", map[string]interface{}{"recommendations": []interface{}{}}},
	}
	for i, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			h.send(jsonRPCRequest{
				JSONRPC: "2.0",
				ID:      3 + i,
				Method:  "tools/call",
				Params: map[string]interface{}{
					"name":      c.tool,
					"arguments": c.args,
				},
			})
			resp := h.readResponse()
			if resp.Error != nil {
				t.Fatalf("%s transport error: %s", c.tool, resp.Error)
			}
			var result struct {
				IsError bool `json:"isError"`
			}
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				t.Fatalf("parse %s result: %v", c.tool, err)
			}
			if !result.IsError {
				t.Errorf("%s should reject an empty/missing-required-field call with a tool error", c.tool)
			}
		})
	}

	t.Run("Shutdown", func(t *testing.T) {
		h.shutdown()
	})
}
