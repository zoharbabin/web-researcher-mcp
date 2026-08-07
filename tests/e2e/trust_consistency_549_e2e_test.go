//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"
)

// TestTrustConsistency549_E2E proves the tools touched by milestone #23
// "v1.47.2 Trust & Consistency Fixes" (issue #549, consolidating #522-#539)
// are registered and reachable end-to-end over the real MCP transport.
// research_panel (#538) and monarch_search (#537/#539) already have
// dedicated E2E coverage (TestResearchPanel_E2E, exercised live via
// monarch_search's own unit/eval suite), so this test covers the remaining
// tools: news_search (#524), patent_search (#527-#530), sequential_search
// (#525), verify_citation (#522/#523), audit_bibliography (#526/#531),
// paper_fulltext (#533), and econ_search (#536). All eight register
// unconditionally (World Bank keeps econ_search on with no API key — see
// internal/tools/registry.go), so no extra env is needed. Stays hermetic (no
// live provider calls) by asserting each tool's in-handler input-validation
// path, the same pattern TestFindings434_E2E uses.
func TestTrustConsistency549_E2E(t *testing.T) {
	h := newMCPTestHarness(t)

	h.send(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "e2e-trust-consistency-549-test", "version": "1.0.0"},
		},
	})
	if resp := h.readResponse(); resp.Error != nil {
		t.Fatalf("initialize returned error: %s", resp.Error)
	}

	h.send(jsonRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"})

	toolNames := []string{
		"news_search", "patent_search", "sequential_search", "verify_citation",
		"audit_bibliography", "paper_fulltext", "econ_search",
	}

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
		{"news_search", map[string]interface{}{}},
		{"patent_search", map[string]interface{}{}},
		{"sequential_search", map[string]interface{}{}},
		{"verify_citation", map[string]interface{}{}},
		{"audit_bibliography", map[string]interface{}{}},
		{"paper_fulltext", map[string]interface{}{}},
		{"econ_search", map[string]interface{}{}},
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
