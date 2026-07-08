package tools

import (
	"context"
	"strings"
	"testing"
)

// TestWebSearchLensDescriptionListsAwesomeLists asserts that the live,
// registered web_search tool's "lens" parameter description advertises the
// "awesome-lists" lens name, matching the pattern already used for every
// other lens (docs, academic, security, ...) — see issue #354.
func TestWebSearchLensDescriptionListsAwesomeLists(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	var lensDesc string
	var found bool
	for _, tool := range result.Tools {
		if tool.Name != "web_search" {
			continue
		}
		schemaMap, ok := tool.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("web_search InputSchema is not map[string]any, got %T", tool.InputSchema)
		}
		props, ok := schemaMap["properties"].(map[string]any)
		if !ok {
			t.Fatalf("web_search InputSchema has no properties map")
		}
		lensProp, ok := props["lens"].(map[string]any)
		if !ok {
			t.Fatalf("web_search InputSchema has no %q property", "lens")
		}
		lensDesc, ok = lensProp["description"].(string)
		if !ok {
			t.Fatalf("web_search lens property has no string description")
		}
		found = true
		break
	}

	if !found {
		t.Fatal("web_search tool not found in ListTools result")
	}

	if !strings.Contains(lensDesc, "awesome-lists") {
		t.Errorf("web_search lens description should list the %q lens, got: %q", "awesome-lists", lensDesc)
	}
}
