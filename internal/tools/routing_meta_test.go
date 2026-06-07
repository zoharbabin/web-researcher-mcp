package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// routingMockProvider is a minimal search.Provider for routing tests: it either
// succeeds (returning one result) or fails on every operation, so a Router can
// be driven through its fallback ladder deterministically.
type routingMockProvider struct {
	name string
	fail bool
}

func (m *routingMockProvider) Name() string { return m.name }
func (m *routingMockProvider) Web(_ context.Context, _ search.WebSearchParams) ([]search.SearchResult, error) {
	if m.fail {
		return nil, fmt.Errorf("%s: web unavailable", m.name)
	}
	return []search.SearchResult{{Title: m.name + " result", URL: "https://" + m.name + ".test"}}, nil
}
func (m *routingMockProvider) Images(_ context.Context, _ search.ImageSearchParams) ([]search.ImageResult, error) {
	if m.fail {
		return nil, fmt.Errorf("%s: images unavailable", m.name)
	}
	return []search.ImageResult{{Title: m.name + " img", Link: "https://" + m.name + ".test/i.png"}}, nil
}
func (m *routingMockProvider) News(_ context.Context, _ search.NewsSearchParams) ([]search.NewsResult, error) {
	if m.fail {
		return nil, fmt.Errorf("%s: news unavailable", m.name)
	}
	return []search.NewsResult{{Title: m.name + " news", URL: "https://" + m.name + ".test/n"}}, nil
}

// routedDeps wires a Router (primary fails → secondary serves) into otherwise
// standard test deps, so a web_search exercises the real routing trace path.
func routedDeps() Dependencies {
	deps := setupTestDeps()
	providers := map[string]search.Provider{
		"primary":   &routingMockProvider{name: "primary", fail: true},
		"secondary": &routingMockProvider{name: "secondary", fail: false},
	}
	router := search.NewRouter(providers, search.RouterConfig{
		Routing: search.RoutingConfig{Default: []string{"primary", "secondary"}},
	})
	deps.Search = router
	deps.SearchProviders = providers
	return deps
}

// TestRoutingMeta_PresentOnResultAndAbsentFromContent is the #58 drift guard:
// routing observability MUST appear in the result `_meta` (operator/client
// channel) and MUST NOT leak into the LLM-facing content body. A regression that
// puts provider/fallback/breaker data into content fails here.
func TestRoutingMeta_PresentOnResultAndAbsentFromContent(t *testing.T) {
	ctx := context.Background()
	deps := routedDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "observability"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content[0].(*mcp.TextContent).Text)
	}

	// --- _meta channel: routing must be present and describe the fallback. ---
	routing, ok := res.Meta["routing"].(map[string]any)
	if !ok {
		t.Fatalf("_meta.routing missing or wrong type: %#v", res.Meta["routing"])
	}
	if routing["provider_used"] != "secondary" {
		t.Errorf("provider_used = %v, want secondary", routing["provider_used"])
	}
	if routing["fallback"] != true {
		t.Errorf("fallback = %v, want true", routing["fallback"])
	}
	if routing["fallback_reason"] != search.FallbackReasonPrimaryUnavailable {
		t.Errorf("fallback_reason = %v, want %q", routing["fallback_reason"], search.FallbackReasonPrimaryUnavailable)
	}

	// --- content channel: NO routing/provider/breaker fields may appear. ---
	var body map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &body); err != nil {
		t.Fatalf("parse content: %v", err)
	}
	for _, banned := range []string{"routing", "provider_used", "providers_attempted", "fallback", "fallback_reason", "breaker", "provider"} {
		if _, present := body[banned]; present {
			t.Errorf("LLM-facing content leaked routing field %q: %v", banned, body[banned])
		}
	}
	// The body still carries its normal content.
	if _, present := body["results"]; !present {
		t.Error("content body missing results")
	}
}

// TestRoutingMeta_SingleProviderOmitted: a non-Router (single) provider yields
// no routing observation, so no routing block is attached (nothing to observe).
func TestRoutingMeta_SingleProviderOmitted(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps() // Search = &mockProvider{} (not a Router)
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "single"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error")
	}
	if _, present := res.Meta["routing"]; present {
		t.Errorf("single-provider result should carry no routing _meta, got %v", res.Meta["routing"])
	}
}

// TestRoutingMeta_UnitHelper exercises the routingMeta helper directly for the
// cache-hit and empty-decision branches that are awkward to reach end-to-end.
func TestRoutingMeta_UnitHelper(t *testing.T) {
	// Cache hit: provider attribution stripped, cache_hit:true present.
	got := routingMeta(search.RoutingDecision{ProviderUsed: "x", Attempted: []string{"x"}}, 0, true)
	if got["cache_hit"] != true {
		t.Errorf("cache hit: cache_hit = %v, want true", got["cache_hit"])
	}
	if _, present := got["provider_used"]; present {
		t.Errorf("cache hit must not attribute a provider, got %v", got["provider_used"])
	}

	// Empty decision (non-routed): nil block → no _meta emitted.
	if got := routingMeta(search.RoutingDecision{}, 0, false); got != nil {
		t.Errorf("empty decision routingMeta = %v, want nil", got)
	}

	// Direct success, no fallback: provider named, fallback omitted.
	got = routingMeta(search.RoutingDecision{ProviderUsed: "brave", Attempted: []string{"brave"}}, 0, false)
	if got["provider_used"] != "brave" {
		t.Errorf("provider_used = %v, want brave", got["provider_used"])
	}
	if _, present := got["fallback"]; present {
		t.Errorf("no-fallback result must omit fallback, got %v", got["fallback"])
	}
}
