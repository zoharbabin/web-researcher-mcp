package tools

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
)

// cachedDeps wires a REAL memory cache into otherwise standard test deps so a
// repeated identical query takes the cache-hit path (the default setupTestDeps
// uses a Noop cache that never stores).
func cachedDeps() Dependencies {
	deps := setupTestDeps()
	deps.Cache = cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 4})
	return deps
}

// TestWebSearchCacheMeta_PresentOnFreshAndCacheHit is the #227 drift guard.
//
// The cache-freshness provenance the tool docs promise (`cached`, `ageSeconds`,
// `maxAgeSeconds`, `freshness`) lives on the MCP result `_meta` envelope —
// SIBLING to the content body, NEVER inside it. A reported "no _meta" came from
// parsing the content JSON, which by design carries no _meta. This asserts the
// envelope over a full client↔server roundtrip:
//   - first call → fresh: cached=false, freshness="fresh"
//   - identical repeat → cache hit: cached=true, ageSeconds/maxAgeSeconds/freshness present
//
// A regression that stops emitting _meta (or moves it into the body) fails here.
func TestWebSearchCacheMeta_PresentOnFreshAndCacheHit(t *testing.T) {
	ctx := context.Background()
	deps := cachedDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	args := map[string]any{"query": "Model Context Protocol specification"}

	// --- First call: freshly fetched. ---
	first, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "web_search", Arguments: args})
	if err != nil {
		t.Fatalf("first CallTool: %v", err)
	}
	if first.IsError {
		t.Fatalf("first call errored: %s", first.Content[0].(*mcp.TextContent).Text)
	}
	if first.Meta == nil {
		t.Fatal("fresh result carries no _meta envelope (#227: cache provenance must be present)")
	}
	if got := first.Meta["cached"]; got != false {
		t.Errorf("fresh result _meta.cached = %v, want false", got)
	}
	if got := first.Meta["freshness"]; got != "fresh" {
		t.Errorf("fresh result _meta.freshness = %v, want \"fresh\"", got)
	}
	if _, ok := first.Meta["maxAgeSeconds"]; !ok {
		t.Error("fresh result _meta missing maxAgeSeconds")
	}

	// --- Identical repeat: served from cache. ---
	second, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "web_search", Arguments: args})
	if err != nil {
		t.Fatalf("second CallTool: %v", err)
	}
	if second.IsError {
		t.Fatalf("second call errored: %s", second.Content[0].(*mcp.TextContent).Text)
	}
	if second.Meta == nil {
		t.Fatal("cache-hit result carries no _meta envelope")
	}
	if got := second.Meta["cached"]; got != true {
		t.Errorf("cache-hit _meta.cached = %v, want true", got)
	}
	for _, key := range []string{"ageSeconds", "maxAgeSeconds", "freshness"} {
		if _, ok := second.Meta[key]; !ok {
			t.Errorf("cache-hit _meta missing %q", key)
		}
	}
}
