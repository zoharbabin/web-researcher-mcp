//go:build e2e

package e2e

import (
	"os"
	"testing"
)

func TestProviders_Context_Live(t *testing.T) {
	if os.Getenv("CONTEXT_DEV_API_KEY") == "" {
		t.Skip("CONTEXT_DEV_API_KEY not set")
	}
	h := newProviderHarness(t, []string{"SEARCH_PROVIDER=context"})
	defer h.shutdown()
	h.initialize(t)

	result := h.callTool(t, "web_search", map[string]interface{}{
		"query":       "site:go.dev/doc effective go concurrency",
		"num_results": 3,
		"provider":    "context",
	})
	assertToolSuccess(t, result)
	assertHasURLs(t, result)
}
