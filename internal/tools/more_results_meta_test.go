package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// braveRewriteTransport redirects api.search.brave.com to a local httptest
// server while preserving path + query + headers, so a real search.BraveProvider
// can be driven end-to-end through the tool layer without network access. It
// mirrors the rewriteTransport used in the search package's own tests.
type braveRewriteTransport struct {
	baseURL string
	inner   http.RoundTripper
}

func (t *braveRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base, _ := url.Parse(t.baseURL)
	req.URL.Scheme = base.Scheme
	req.URL.Host = base.Host
	return t.inner.RoundTrip(req)
}

// braveDepsWithMoreResults wires a real Brave web provider (backed by ts) as the
// sole search provider, so web_search exercises the genuine F8 side channel:
// provider sets query.more_results_available → ResultMeta → tool merges _meta.
func braveDepsWithMoreResults(ts *httptest.Server) Dependencies {
	deps := setupTestDeps()
	client := &http.Client{Transport: &braveRewriteTransport{baseURL: ts.URL, inner: http.DefaultTransport}}
	bp := search.NewBraveProvider("brave-key", search.BraveConfig{}, search.Deps{
		HTTPClient: client,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	deps.Search = bp
	deps.SearchProviders = map[string]search.Provider{bp.Name(): bp}
	return deps
}

// TestMoreResultsMeta_SurfacedAndNotInContent is the F8 end-to-end drift guard:
// Brave's query.more_results_available MUST appear in the result `_meta`
// (operator/client pagination channel) and MUST NOT leak into the LLM-facing
// content body. It rides the same lane as routing/cache _meta.
func TestMoreResultsMeta_SurfacedAndNotInContent(t *testing.T) {
	ctx := context.Background()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"query":{"more_results_available":true},"web":{"results":[{"title":"R","url":"https://e.test","description":"d"}]}}`))
	}))
	defer ts.Close()

	srv := createTestServer(braveDepsWithMoreResults(ts))
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "deep research"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content[0].(*mcp.TextContent).Text)
	}

	// --- _meta channel: the pagination flag is present and true. ---
	got, ok := res.Meta["more_results_available"]
	if !ok {
		t.Fatalf("_meta.more_results_available missing; _meta = %#v", res.Meta)
	}
	if got != true {
		t.Errorf("_meta.more_results_available = %v, want true", got)
	}

	// --- content channel: the flag must NOT leak into the model-facing body. ---
	var body map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &body); err != nil {
		t.Fatalf("parse content: %v", err)
	}
	for _, banned := range []string{"more_results_available", "moreResultsAvailable"} {
		if _, present := body[banned]; present {
			t.Errorf("LLM-facing content leaked pagination field %q", banned)
		}
	}
	if _, present := body["results"]; !present {
		t.Error("content body missing results")
	}
}

// TestMoreResultsMeta_OmittedWhenProviderSilent asserts the key is absent when
// the provider reports nothing (false, false) — "no cursor" must not be
// misreported as "no more results". The default mockProvider emits no flag.
func TestMoreResultsMeta_OmittedWhenProviderSilent(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps() // Search = &mockProvider{}, never touches the side channel
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "quiet"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error")
	}
	if v, present := res.Meta["more_results_available"]; present {
		t.Errorf("silent provider must omit _meta.more_results_available, got %v", v)
	}
}

// TestMoreResultsMeta_FalseIsSurfaced asserts an explicit false is distinct from
// silence: when Brave reports more_results_available:false, the key is present
// and false (a deep-research caller stops paging), not omitted.
func TestMoreResultsMeta_FalseIsSurfaced(t *testing.T) {
	ctx := context.Background()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"query":{"more_results_available":false},"web":{"results":[]}}`))
	}))
	defer ts.Close()

	srv := createTestServer(braveDepsWithMoreResults(ts))
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "last page"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	got, present := res.Meta["more_results_available"]
	if !present {
		t.Fatalf("explicit false must be surfaced, _meta = %#v", res.Meta)
	}
	if got != false {
		t.Errorf("_meta.more_results_available = %v, want false", got)
	}
	// Sanity: not accidentally tripping a "leak into body" via the empty-results path.
	if strings.Contains(res.Content[0].(*mcp.TextContent).Text, "more_results_available") {
		t.Error("pagination flag leaked into content body")
	}
}
