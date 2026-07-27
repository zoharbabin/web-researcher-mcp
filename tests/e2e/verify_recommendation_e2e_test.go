//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestVerifyRecommendation_E2E drives verify_recommendation over the real MCP
// transport and proves the Wikidata corporate-ownership fallback (#248) is
// wired end-to-end: registered, reachable, and its outputSchema advertises
// corporateOwnershipSignal + the corporate_ownership flag. Stays hermetic (no
// live Wikidata calls) the same way TestCompanyRecon_E2E and
// TestFindings434_E2E do — asserting the schema contract and the in-handler
// validation path rather than a live network round-trip, since the resolver
// itself (search.WikidataOwnershipResolver) already has full live-vs-mock
// coverage in internal/search/wikidata_test.go and the fail-open branch logic
// has full coverage in internal/tools/wikidata_ownership_test.go.
func TestVerifyRecommendation_E2E(t *testing.T) {
	h := newMCPTestHarness(t)

	h.send(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "e2e-verify-recommendation-test", "version": "1.0.0"},
		},
	})
	if resp := h.readResponse(); resp.Error != nil {
		t.Fatalf("initialize returned error: %s", resp.Error)
	}

	h.send(jsonRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"})

	t.Run("ListedWithOwnershipSchema", func(t *testing.T) {
		h.send(jsonRPCRequest{JSONRPC: "2.0", ID: 2, Method: "tools/list"})
		resp := h.readResponse()
		if resp.Error != nil {
			t.Fatalf("tools/list returned error: %s", resp.Error)
		}
		var result struct {
			Tools []struct {
				Name         string         `json:"name"`
				OutputSchema map[string]any `json:"outputSchema"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("failed to parse tools result: %v", err)
		}
		var tool *struct {
			Name         string         `json:"name"`
			OutputSchema map[string]any `json:"outputSchema"`
		}
		for i := range result.Tools {
			if result.Tools[i].Name == "verify_recommendation" {
				tool = &result.Tools[i]
				break
			}
		}
		if tool == nil {
			t.Fatal("verify_recommendation should be registered in tools/list")
		}

		itemSchemaJSON, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatalf("marshal outputSchema: %v", err)
		}
		schemaStr := string(itemSchemaJSON)
		if !strings.Contains(schemaStr, "corporateOwnershipSignal") || !strings.Contains(schemaStr, "corporate_ownership") {
			t.Errorf("outputSchema should advertise the #248 corporate ownership fallback (corporateOwnershipSignal property + corporate_ownership flag), got: %s", schemaStr)
		}
	})

	t.Run("RejectsEmptyRecommendations", func(t *testing.T) {
		h.send(jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      3,
			Method:  "tools/call",
			Params: map[string]interface{}{
				"name":      "verify_recommendation",
				"arguments": map[string]interface{}{"recommendations": []interface{}{}},
			},
		})
		resp := h.readResponse()
		if resp.Error != nil {
			t.Fatalf("verify_recommendation transport error: %s", resp.Error)
		}
		var result struct {
			IsError bool `json:"isError"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("parse verify_recommendation result: %v", err)
		}
		if !result.IsError {
			t.Error("verify_recommendation should reject an empty recommendations list with a tool error")
		}
	})

	t.Run("AuditsRecommendationWithoutURL", func(t *testing.T) {
		h.send(jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      4,
			Method:  "tools/call",
			Params: map[string]interface{}{
				"name": "verify_recommendation",
				"arguments": map[string]interface{}{
					"recommendations": []interface{}{
						map[string]interface{}{"title": "Example Tool"},
					},
				},
			},
		})
		resp := h.readResponse()
		if resp.Error != nil {
			t.Fatalf("verify_recommendation transport error: %s", resp.Error)
		}
		var result struct {
			IsError bool `json:"isError"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("parse verify_recommendation result: %v", err)
		}
		if result.IsError {
			t.Error("verify_recommendation should succeed for a valid recommendation with no url (ownership branch skipped, not errored)")
		}
	})

	t.Run("Shutdown", func(t *testing.T) {
		h.shutdown()
	})
}
