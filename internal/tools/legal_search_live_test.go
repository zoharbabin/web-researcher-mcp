//go:build live

// Live integration test for legal_search tool via MCP connection with real CourtListener API.
// Run with: go test -tags=live -run TestLegalSearchLive ./internal/tools/
package tools

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

func newLiveTestDeps(t *testing.T) Dependencies {
	t.Helper()
	httpClient := &http.Client{Timeout: 30 * time.Second}

	// Create a real CourtListener provider (works keyless at lower rate)
	caseProvider := search.NewCourtListenerProvider(os.Getenv("COURTLISTENER_API_TOKEN"), search.Deps{
		HTTPClient: httpClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	return Dependencies{
		Cache:         cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 16}),
		CaseProviders: map[string]search.CaseProvider{caseProvider.Name(): caseProvider},
		Metrics:       metrics.NewCollector(),
		Auditor:       audit.NewNoop(),
	}
}

// TestLegalSearchLiveBrownVBoard: Test legal_search tool with real CourtListener API.
// This test verifies that legal_search returns real court opinion records when queried
// for "Brown v. Board of Education" via the live MCP tool interface.
func TestLegalSearchLiveBrownVBoard(t *testing.T) {
	out, res := callTool(t, newLiveTestDeps(t), "legal_search", map[string]any{
		"query":       "Brown v. Board of Education",
		"num_results": 3,
	})

	// Verify no error
	if res.IsError {
		t.Fatalf("legal_search returned error: expected success")
	}

	// Verify provider is courtlistener
	if out["provider"] != "courtlistener" {
		t.Errorf("expected provider=courtlistener, got %v", out["provider"])
	}

	// Verify trust marker
	if out["trust"] != "untrusted-external-content" {
		t.Errorf("expected trust=untrusted-external-content, got %v", out["trust"])
	}

	// Verify cases array exists and has results
	cases, ok := out["cases"].([]any)
	if !ok {
		t.Fatalf("cases field missing or not an array: %v", out["cases"])
	}

	if len(cases) == 0 {
		t.Fatal("expected at least 1 case result, got 0")
	}

	t.Logf("Result count: %d", len(cases))

	// Verify first case has required fields
	c0, ok := cases[0].(map[string]any)
	if !ok {
		t.Fatalf("first case not a map: %v", cases[0])
	}

	caseName, ok := c0["caseName"].(string)
	if !ok || caseName == "" {
		t.Errorf("caseName missing or empty: %v", c0["caseName"])
	}

	citation, ok := c0["citation"].(string)
	if !ok || citation == "" {
		t.Errorf("citation missing or empty: %v", c0["citation"])
	}

	url, ok := c0["url"].(string)
	if !ok || url == "" {
		t.Errorf("url missing or empty: %v", c0["url"])
	}

	source, ok := c0["source"].(string)
	if !ok || source != "courtlistener" {
		t.Errorf("source should be courtlistener, got %v", c0["source"])
	}

	t.Logf("First case: %q [%s] (URL: %s)", caseName, citation, url)
}

// TestLegalSearchLiveRequiresQuery: Verify legal_search rejects empty query
func TestLegalSearchLiveRequiresQuery(t *testing.T) {
	_, res := callTool(t, newLiveTestDeps(t), "legal_search", map[string]any{})
	if !res.IsError {
		t.Error("legal_search should reject missing query parameter")
	}
}
