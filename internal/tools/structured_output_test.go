package tools

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

func TestStructuredContentPresent(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html><html><head><title>Test</title></head>
<body><article><h1>Heading</h1>
<p>Enough content to pass the minimum threshold for extraction. This paragraph needs to be sufficiently long to avoid being filtered out by the content quality checks in the pipeline.</p>
<p>Second paragraph with additional detail about the topic.</p>
</article></body></html>`))
	}))
	defer ts.Close()

	ctx := context.Background()
	deps := Dependencies{
		Cache:    cache.NewNoop(),
		Search:   &mockProviderWithURL{url: ts.URL},
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true}),
		Content:  content.NewProcessor(),
		Sessions: session.NewManager(session.Config{MaxSessions: 100}),
		Metrics:  metrics.NewCollector(),
		Auditor:  audit.NewNoop(),
		Logger:   slog.Default(),
	}
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	toolCalls := []struct {
		name string
		args map[string]any
	}{
		{"web_search", map[string]any{"query": "test"}},
		{"image_search", map[string]any{"query": "test"}},
		{"news_search", map[string]any{"query": "test"}},
		{"academic_search", map[string]any{"query": "test"}},
		{"patent_search", map[string]any{"query": "test"}},
		{"scrape_page", map[string]any{"url": ts.URL}},
		{"search_and_scrape", map[string]any{"query": "test"}},
		{"sequential_search", map[string]any{"searchStep": "step one", "stepNumber": float64(1), "nextStepNeeded": false}},
	}

	for _, tc := range toolCalls {
		t.Run(tc.name, func(t *testing.T) {
			res, err := client.CallTool(ctx, &mcp.CallToolParams{
				Name:      tc.name,
				Arguments: tc.args,
			})
			if err != nil {
				t.Fatalf("CallTool failed: %v", err)
			}
			if res.IsError {
				t.Skipf("tool returned error (likely no mock for this path): %s", res.Content[0].(*mcp.TextContent).Text)
			}

			if res.StructuredContent == nil {
				t.Fatal("StructuredContent is nil — MCP clients that enforce output schema will reject this response")
			}

			// StructuredContent must be valid JSON that deserializes to a map
			scBytes, err := json.Marshal(res.StructuredContent)
			if err != nil {
				t.Fatalf("StructuredContent cannot be marshaled: %v", err)
			}
			var scMap map[string]any
			if err := json.Unmarshal(scBytes, &scMap); err != nil {
				t.Fatalf("StructuredContent is not a JSON object: %v", err)
			}

			// Content[0].Text must also be valid JSON and match StructuredContent
			if len(res.Content) == 0 {
				t.Fatal("Content is empty")
			}
			textContent, ok := res.Content[0].(*mcp.TextContent)
			if !ok {
				t.Fatalf("Content[0] is not TextContent, got %T", res.Content[0])
			}
			var textMap map[string]any
			if err := json.Unmarshal([]byte(textContent.Text), &textMap); err != nil {
				t.Fatalf("Content text is not valid JSON: %v", err)
			}

			// Both should have the same keys
			for key := range scMap {
				if _, ok := textMap[key]; !ok {
					t.Errorf("StructuredContent has key %q not present in Content text", key)
				}
			}
			for key := range textMap {
				if _, ok := scMap[key]; !ok {
					t.Errorf("Content text has key %q not present in StructuredContent", key)
				}
			}
		})
	}
}

func TestStructuredContentMatchesOutputSchema(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	tools := listTools(t)
	schemas := make(map[string]map[string]any)
	for _, tool := range tools {
		if s, ok := tool.OutputSchema.(map[string]any); ok {
			schemas[tool.Name] = s
		}
	}

	toolCalls := map[string]map[string]any{
		"web_search":        {"query": "test"},
		"image_search":      {"query": "test"},
		"news_search":       {"query": "test"},
		"academic_search":   {"query": "test"},
		"patent_search":     {"query": "test"},
		"sequential_search": {"searchStep": "research", "stepNumber": float64(1), "nextStepNeeded": false},
	}

	for name, args := range toolCalls {
		t.Run(name, func(t *testing.T) {
			res, err := client.CallTool(ctx, &mcp.CallToolParams{
				Name:      name,
				Arguments: args,
			})
			if err != nil {
				t.Fatalf("CallTool failed: %v", err)
			}
			if res.IsError {
				t.Skip("tool returned error")
			}

			schema, ok := schemas[name]
			if !ok {
				t.Fatal("no output schema for tool")
			}
			props, _ := schema["properties"].(map[string]any)

			scBytes, err := json.Marshal(res.StructuredContent)
			if err != nil {
				t.Fatalf("failed to marshal StructuredContent: %v", err)
			}
			var scMap map[string]any
			if err := json.Unmarshal(scBytes, &scMap); err != nil {
				t.Fatalf("StructuredContent not a JSON object: %v", err)
			}

			// Every key in StructuredContent must be declared in the schema
			for key := range scMap {
				if _, declared := props[key]; !declared {
					t.Errorf("StructuredContent field %q not declared in OutputSchema", key)
				}
			}
		})
	}
}

func TestStructuredContentAbsentOnError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	errorCases := []struct {
		name string
		args map[string]any
	}{
		{"web_search", map[string]any{"query": ""}},
		{"image_search", map[string]any{"query": ""}},
		{"news_search", map[string]any{"query": ""}},
		{"academic_search", map[string]any{"query": ""}},
		{"patent_search", map[string]any{"query": ""}},
		{"scrape_page", map[string]any{"url": ""}},
		{"search_and_scrape", map[string]any{"query": ""}},
		{"sequential_search", map[string]any{"searchStep": "", "stepNumber": float64(1), "nextStepNeeded": true}},
	}

	for _, tc := range errorCases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := client.CallTool(ctx, &mcp.CallToolParams{
				Name:      tc.name,
				Arguments: tc.args,
			})
			if err != nil {
				t.Fatalf("CallTool failed: %v", err)
			}
			if !res.IsError {
				t.Fatal("expected error response")
			}
			if res.StructuredContent != nil {
				t.Fatal("StructuredContent should be nil on error responses")
			}
		})
	}
}

func TestStructuredContentOnCacheHit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	memCache := cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 1})
	deps := Dependencies{
		Cache:    memCache,
		Search:   &mockProvider{},
		Scraper:  scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2}),
		Content:  content.NewProcessor(),
		Sessions: session.NewManager(session.Config{MaxSessions: 100}),
		Metrics:  metrics.NewCollector(),
		Auditor:  audit.NewNoop(),
		Logger:   slog.Default(),
	}
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	toolCalls := []struct {
		name string
		args map[string]any
	}{
		{"web_search", map[string]any{"query": "cache-structured-test"}},
		{"image_search", map[string]any{"query": "cache-structured-test"}},
		{"news_search", map[string]any{"query": "cache-structured-test"}},
		{"academic_search", map[string]any{"query": "cache-structured-test"}},
		{"patent_search", map[string]any{"query": "cache-structured-test"}},
	}

	for _, tc := range toolCalls {
		t.Run(tc.name, func(t *testing.T) {
			// First call: populate cache
			res1, err := client.CallTool(ctx, &mcp.CallToolParams{
				Name:      tc.name,
				Arguments: tc.args,
			})
			if err != nil {
				t.Fatalf("first call failed: %v", err)
			}
			if res1.IsError {
				t.Skip("tool returned error")
			}
			if res1.StructuredContent == nil {
				t.Fatal("first call: StructuredContent is nil")
			}

			// Second call: should hit cache and still have StructuredContent
			res2, err := client.CallTool(ctx, &mcp.CallToolParams{
				Name:      tc.name,
				Arguments: tc.args,
			})
			if err != nil {
				t.Fatalf("second call failed: %v", err)
			}
			if res2.IsError {
				t.Fatal("second call unexpectedly errored")
			}
			if res2.StructuredContent == nil {
				t.Fatal("cache hit: StructuredContent is nil — cached responses must also include structured content")
			}

			// Verify cache hit returns same data
			sc1, _ := json.Marshal(res1.StructuredContent)
			sc2, _ := json.Marshal(res2.StructuredContent)
			if string(sc1) != string(sc2) {
				t.Error("StructuredContent differs between fresh and cached responses")
			}
		})
	}
}

func TestStructuredResultHelper(t *testing.T) {
	t.Parallel()

	data := []byte(`{"query":"test","resultCount":1}`)
	result := structuredResult(data)

	if result.IsError {
		t.Fatal("structuredResult should not set IsError")
	}
	if result.StructuredContent == nil {
		t.Fatal("structuredResult must set StructuredContent")
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(result.Content))
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatal("expected *TextContent")
	}
	if tc.Text != string(data) {
		t.Fatalf("Content text mismatch: got %q", tc.Text)
	}

	// StructuredContent must be json.RawMessage
	raw, ok := result.StructuredContent.(json.RawMessage)
	if !ok {
		t.Fatalf("StructuredContent should be json.RawMessage, got %T", result.StructuredContent)
	}
	if string(raw) != string(data) {
		t.Fatalf("StructuredContent data mismatch: got %q", string(raw))
	}
}

func TestTextResultHasNoStructuredContent(t *testing.T) {
	t.Parallel()

	result := textResult("some text")

	if result.StructuredContent != nil {
		t.Fatal("textResult must NOT set StructuredContent")
	}
}

func TestStructuredContentIsRawJSON(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "json-raw-test"},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if res.IsError {
		t.Fatal("unexpected error")
	}

	// StructuredContent should be json.RawMessage ([]byte) that can be used directly
	switch sc := res.StructuredContent.(type) {
	case json.RawMessage:
		var m map[string]any
		if err := json.Unmarshal(sc, &m); err != nil {
			t.Fatalf("RawMessage is not valid JSON: %v", err)
		}
	case map[string]any:
		// Also acceptable after deserialization
	default:
		// After JSON round-trip, could be map[string]any — check it's serializable
		b, err := json.Marshal(sc)
		if err != nil {
			t.Fatalf("StructuredContent not serializable: %v (type: %T)", err, sc)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("serialized StructuredContent is not a JSON object: %v", err)
		}
	}
}
