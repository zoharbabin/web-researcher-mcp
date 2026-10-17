package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/config"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

type parallelToolTransport struct{ t *testing.T }

func (tr parallelToolTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.String() != "https://search.parallel.ai/mcp" || !strings.HasPrefix(r.UserAgent(), "web-researcher-mcp ") || r.Header.Get("Authorization") != "" {
		tr.t.Errorf("unexpected Parallel request: %s %v", r.URL, r.Header)
	}
	var call struct {
		ID     any            `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
		return nil, err
	}
	var result any
	switch call.Method {
	case "server/discover":
		body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": call.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, err
	case "initialize":
		result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "fixture", "version": "1"}}
	case "notifications/initialized":
		return &http.Response{StatusCode: 202, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	case "tools/call":
		args := call.Params["arguments"].(map[string]any)
		queries := args["search_queries"].([]any)
		if call.Params["name"] != "web_search" || queries[0] != "Go documentation site:go.dev" {
			tr.t.Errorf("tool parameters were lost: %+v", call.Params)
		}
		result = map[string]any{"content": []any{}, "structuredContent": map[string]any{"results": []any{map[string]any{"title": "Go documentation", "url": "https://go.dev/doc/", "publish_date": "2026-01-02", "excerpts": []string{"Go is an open source programming language."}}}}}
	default:
		tr.t.Errorf("unexpected method %s", call.Method)
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": result})
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, err
}

func TestParallelWebSearchTool(t *testing.T) {
	t.Parallel()
	deps := setupTestDeps()
	deps.SearchProviders = search.AvailableProviders(config.SearchConfig{}, search.Deps{HTTPClient: &http.Client{Transport: parallelToolTransport{t: t}}})
	client := connectTestClient(context.Background(), t, createTestServer(deps))
	defer client.Close()
	res, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "web_search", Arguments: map[string]any{
		"provider": "parallel", "query": "Go documentation", "site": "go.dev", "num_results": 1,
	}})
	if err != nil || res.IsError {
		t.Fatalf("web_search: %+v, %v", res, err)
	}
	var output struct {
		Results []search.SearchResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Results) != 1 || output.Results[0].URL != "https://go.dev/doc/" || output.Results[0].Snippet == "" {
		t.Fatalf("provider output not consumed by web_search: %+v", output)
	}
}
