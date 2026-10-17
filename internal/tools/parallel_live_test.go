//go:build live

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/config"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// parallelLiveTransport observes real anonymous requests without changing their
// destination, headers, credentials, or response. Keep quota responses in the log.
type parallelLiveTransport struct {
	t     *testing.T
	mu    sync.Mutex
	quota bool
}

func (tr *parallelLiveTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.String() != "https://search.parallel.ai/mcp" || !strings.HasPrefix(r.UserAgent(), "web-researcher-mcp ") || r.Header.Get("Authorization") != "" {
		tr.t.Errorf("unexpected live request: %s %v", r.URL, r.Header)
	}
	res, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 5*1024*1024))
	res.Body.Close()
	if err != nil {
		return nil, err
	}
	res.Body = io.NopCloser(bytes.NewReader(body))
	tr.t.Logf("%s HTTP %d Retry-After=%q body=%s", time.Now().UTC().Format(time.RFC3339), res.StatusCode, res.Header.Get("Retry-After"), body)
	lower := strings.ToLower(string(body))
	tr.mu.Lock()
	tr.quota = tr.quota || res.StatusCode == 429 || (strings.Contains(lower, `"iserror":true`) && (strings.Contains(lower, "quota") || strings.Contains(lower, "rate limit")))
	tr.mu.Unlock()
	return res, nil
}

func TestParallelLiveWebSearchTool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	transport := &parallelLiveTransport{t: t}
	deps := setupTestDeps()
	// Empty provider config means no saved/environment credentials are loaded.
	deps.SearchProviders = search.AvailableProviders(config.SearchConfig{}, search.Deps{HTTPClient: &http.Client{Transport: transport, Timeout: 30 * time.Second}})
	client := connectTestClient(ctx, t, createTestServer(deps))
	defer client.Close()
	res, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "web_search", Arguments: map[string]any{"provider": "parallel", "query": "Go programming language official documentation", "num_results": 2}})
	transport.mu.Lock()
	quota := transport.quota
	transport.mu.Unlock()
	if quota {
		t.Skip("explicit anonymous Parallel quota response retained above; no successful live search claimed")
	}
	if err != nil || res.IsError {
		t.Fatalf("web_search: %+v, %v", res, err)
	}
	var output struct {
		Results []search.SearchResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Results) == 0 || len(output.Results) > 2 || output.Results[0].URL == "" || output.Results[0].Snippet == "" {
		t.Fatalf("no useful search results: %+v", output)
	}
	t.Logf("web_search returned %d sources; first: %s", len(output.Results), output.Results[0].URL)
}
