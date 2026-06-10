package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
)

func artifactTestDeps() Dependencies {
	return Dependencies{Cache: cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 8})}
}

// TestLargeResultOrInline_SmallInlines: payloads under the threshold inline
// exactly as structuredResult — a single TextContent + StructuredContent, no link.
func TestLargeResultOrInline_SmallInlines(t *testing.T) {
	deps := artifactTestDeps()
	small := []byte(`{"hello":"world"}`)
	res := largeResultOrInline(context.Background(), deps, small, "small")
	if len(res.Content) != 1 {
		t.Fatalf("small payload should be a single inline content, got %d items", len(res.Content))
	}
	if _, ok := res.Content[0].(*mcp.TextContent); !ok {
		t.Fatalf("small payload content should be TextContent, got %T", res.Content[0])
	}
	if res.StructuredContent == nil {
		t.Error("small payload should keep StructuredContent")
	}
}

// TestLargeResultOrInline_LargeLinks: payloads at/above the threshold return a
// small summary + a resource_link to the stored artifact, NOT the full body.
func TestLargeResultOrInline_LargeLinks(t *testing.T) {
	deps := artifactTestDeps()
	big := []byte(`{"content":"` + strings.Repeat("x", linkThresholdBytes) + `"}`)
	res := largeResultOrInline(context.Background(), deps, big, "a big page")

	if len(res.Content) != 2 {
		t.Fatalf("large payload should be summary + link (2 items), got %d", len(res.Content))
	}
	// First item: small inline summary (must NOT contain the full body).
	txt, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("first content should be the summary TextContent, got %T", res.Content[0])
	}
	if len(txt.Text) >= len(big) {
		t.Errorf("summary (%d bytes) should be far smaller than the payload (%d bytes)", len(txt.Text), len(big))
	}
	var sum linkSummary
	if err := json.Unmarshal([]byte(txt.Text), &sum); err != nil {
		t.Fatalf("summary should be valid linkSummary JSON: %v", err)
	}
	if !sum.Linked || sum.Bytes != len(big) || sum.Summary != "a big page" || sum.ExpiresAt == "" {
		t.Errorf("summary fields wrong: %+v", sum)
	}
	// Second item: the resource_link.
	link, ok := res.Content[1].(*mcp.ResourceLink)
	if !ok {
		t.Fatalf("second content should be a ResourceLink, got %T", res.Content[1])
	}
	if !strings.HasPrefix(link.URI, artifactURIPrefix) {
		t.Errorf("link URI = %q, want %s prefix", link.URI, artifactURIPrefix)
	}
	if sum.Resource != link.URI {
		t.Errorf("summary.resource %q != link.URI %q", sum.Resource, link.URI)
	}
	if link.Size == nil || *link.Size != int64(len(big)) {
		t.Errorf("link Size should equal payload size")
	}
}

// TestArtifact_RoundTrip: a linked artifact is fetchable through the registered
// resource template and returns the exact stored bytes.
func TestArtifact_RoundTrip(t *testing.T) {
	ctx := context.Background()
	deps := artifactTestDeps()
	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	registerArtifactResource(srv, deps)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	big := []byte(`{"content":"` + strings.Repeat("y", linkThresholdBytes) + `","n":1}`)
	res := largeResultOrInline(ctx, deps, big, "round trip")
	link := res.Content[1].(*mcp.ResourceLink)

	got, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: link.URI})
	if err != nil {
		t.Fatalf("ReadResource(%s) failed: %v", link.URI, err)
	}
	if len(got.Contents) != 1 || got.Contents[0].Text != string(big) {
		t.Fatalf("artifact round-trip body mismatch")
	}
	if got.Contents[0].MIMEType != artifactMIMEType {
		t.Errorf("artifact MIME = %q, want %s", got.Contents[0].MIMEType, artifactMIMEType)
	}
}

// TestArtifact_Idempotent: the same payload always yields the same content-
// addressed URI (de-dup), different payloads differ.
func TestArtifact_Idempotent(t *testing.T) {
	ctx := context.Background()
	deps := artifactTestDeps()
	a := []byte(strings.Repeat("a", 100))
	b := []byte(strings.Repeat("b", 100))
	u1, _, _ := storeArtifact(ctx, deps, a)
	u2, _, _ := storeArtifact(ctx, deps, a)
	u3, _, _ := storeArtifact(ctx, deps, b)
	if u1 != u2 {
		t.Errorf("same payload should yield same URI: %q vs %q", u1, u2)
	}
	if u1 == u3 {
		t.Error("different payloads should yield different URIs")
	}
}

// TestArtifact_MissReturnsError: an unknown/expired id is a not-found error, never
// another caller's data.
func TestArtifact_MissReturnsError(t *testing.T) {
	ctx := context.Background()
	deps := artifactTestDeps()
	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	registerArtifactResource(srv, deps)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	_, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: artifactURIPrefix + "deadbeef"})
	if err == nil {
		t.Fatal("reading an unknown artifact should error")
	}
}

// TestLargeResultOrInline_NoCacheInlines: with no backing cache, large payloads
// inline (correctness over size) rather than producing a dangling link.
func TestLargeResultOrInline_NoCacheInlines(t *testing.T) {
	deps := Dependencies{Cache: nil}
	big := []byte(strings.Repeat("z", linkThresholdBytes+10))
	res := largeResultOrInline(context.Background(), deps, big, "no cache")
	if len(res.Content) != 1 {
		t.Fatalf("no-cache large payload should inline (1 item), got %d", len(res.Content))
	}
}
