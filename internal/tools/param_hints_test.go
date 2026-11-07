package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestParamAliasHints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		tool     string
		args     map[string]any
		wantHint string // substring expected in the error text; empty means no hint
	}{
		{"web_search max_results", "web_search", map[string]any{"query": "go", "max_results": "5"}, `Use "num_results"`},
		{"web_search limit", "web_search", map[string]any{"query": "go", "limit": 5}, `Use "num_results"`},
		{"news_search max_results", "news_search", map[string]any{"query": "go", "max_results": 5}, `Use "num_results"`},
		{"scrape_page start_index", "scrape_page", map[string]any{"url": "https://example.com", "start_index": 100}, `Use "offset"`},
		{"unknown property gets no hint", "web_search", map[string]any{"query": "go", "bogus": 1}, ""},
		{"other tool gets no hint", "scrape_page", map[string]any{"url": "https://example.com", "max_results": 1}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			sess := connectTestClient(ctx, t, createTestServer(setupTestDeps()))
			defer sess.Close()
			res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: tt.tool, Arguments: tt.args})
			if err != nil {
				t.Fatalf("CallTool: %v", err)
			}
			if !res.IsError {
				t.Fatalf("expected a validation error, got success")
			}
			text := res.Content[0].(*mcp.TextContent).Text
			if !strings.Contains(text, "unexpected additional properties") {
				t.Fatalf("lost the SDK message: %s", text)
			}
			if tt.wantHint == "" {
				if strings.Contains(text, "is not a parameter of") {
					t.Fatalf("unexpected hint: %s", text)
				}
				return
			}
			if !strings.Contains(text, tt.wantHint) {
				t.Fatalf("missing hint %q in: %s", tt.wantHint, text)
			}
		})
	}
}
