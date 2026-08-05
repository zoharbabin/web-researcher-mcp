//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"
)

// TestGitHubTrustSignals546_E2E proves, against the real compiled subprocess
// binary, that scrape_page's advertised outputSchema carries the #546
// githubTrustSignals shape end-to-end (transport -> registry -> schema JSON).
//
// It deliberately does NOT assert a populated githubTrustSignals value from a
// live scrape: the e2e harness drives the real binary over stdio, and
// production wires no environment variable for scraper.PipelineConfig's
// GitHubRawBase/GitHubAPIBase (only GITHUB_TOKEN is wired — see
// cmd/web-researcher-mcp/main.go), so there is no way to redirect the
// subprocess's internal GitHub API calls to a local mock server. A populated
// value is instead covered at the tool layer, against an injected
// scraper.PipelineConfig{GitHubRawBase, GitHubAPIBase}, in
// TestScrapePageGitHubTrustSignalsPresent (internal/tools/scrape_github_trust_test.go).
func TestGitHubTrustSignals546_E2E(t *testing.T) {
	h := newMCPTestHarness(t)

	h.send(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "e2e-github-trust-signals-546-test", "version": "1.0.0"},
		},
	})
	if resp := h.readResponse(); resp.Error != nil {
		t.Fatalf("initialize returned error: %s", resp.Error)
	}

	h.send(jsonRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized"})

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

	var scrapePageSchema map[string]any
	for _, tool := range result.Tools {
		if tool.Name == "scrape_page" {
			scrapePageSchema = tool.OutputSchema
			break
		}
	}
	if scrapePageSchema == nil {
		t.Fatal("scrape_page should be registered in tools/list with an outputSchema")
	}

	props, ok := scrapePageSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("scrape_page outputSchema has no properties object: %v", scrapePageSchema)
	}
	trust, ok := props["githubTrustSignals"].(map[string]any)
	if !ok {
		t.Fatalf("scrape_page outputSchema missing githubTrustSignals property: %v", props)
	}
	trustProps, ok := trust["properties"].(map[string]any)
	if !ok {
		t.Fatalf("githubTrustSignals has no nested properties: %v", trust)
	}
	for _, field := range []string{"repo", "owner", "contributorCount", "community", "releaseCount"} {
		if _, present := trustProps[field]; !present {
			t.Errorf("githubTrustSignals.properties missing %q: %v", field, trustProps)
		}
	}

	h.shutdown()
}
