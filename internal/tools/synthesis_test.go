package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

func callTool(t *testing.T, deps Dependencies, name string, args map[string]any) (map[string]any, *mcp.CallToolResult) {
	t.Helper()
	ctx := context.Background()
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s) failed: %v", name, err)
	}
	var out map[string]any
	if !res.IsError {
		if e := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); e != nil {
			t.Fatalf("parse(%s): %v", name, e)
		}
	}
	return out, res
}

func TestAnswerTool(t *testing.T) {
	out, res := callTool(t, setupTestDeps(), "answer", map[string]any{"query": "what is the meaning of life"})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	if out["answer"] != "A test answer." {
		t.Errorf("answer mismatch: %v", out["answer"])
	}
	if out["provider"] != "mocksynth" {
		t.Errorf("result should name the provider, got %v", out["provider"])
	}
	if out["trust"] != "untrusted-external-content" {
		t.Errorf("missing trust marker: %v", out["trust"])
	}
}

func TestAnswerToolRequiresQuery(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "answer", map[string]any{})
	if !res.IsError {
		t.Error("empty query should be an error")
	}
}

func TestStructuredSearchTool(t *testing.T) {
	out, res := callTool(t, setupTestDeps(), "structured_search", map[string]any{"query": "Anthropic", "category": "company"})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	if out["provider"] != "mocksynth" {
		t.Errorf("result should name the provider, got %v", out["provider"])
	}
	if out["trust"] != "untrusted-external-content" {
		t.Errorf("missing trust marker: %v", out["trust"])
	}
	if out["resultCount"] == nil {
		t.Error("resultCount should be present")
	}
}

// TestStructuredSearchToolIsVendorNeutral confirms the tool does NOT hard-code
// Exa's category vocabulary: with the mock provider (which accepts anything),
// a category that Exa would reject still succeeds — category validation is the
// provider's job, not the tool's.
func TestStructuredSearchToolIsVendorNeutral(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "structured_search", map[string]any{"query": "x", "category": "anything-goes"})
	if res.IsError {
		t.Error("tool must not enforce a vendor's category list; the provider validates")
	}
}

// TestSynthesisToolsUnregisteredWithoutProvider: with no synthesis providers,
// neither tool is registered (parity with academic/patent capability gating).
func TestSynthesisToolsUnregisteredWithoutProvider(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.AnswerProviders = nil
	deps.StructuredProviders = nil
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	list, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	for _, tool := range list.Tools {
		if tool.Name == "answer" || tool.Name == "structured_search" {
			t.Errorf("%s must NOT be registered when no synthesis provider is configured", tool.Name)
		}
	}
}

// TestAnswerProviderSelection: an explicit unknown provider is rejected with the
// supported list; a known-but-unconfigured provider gets a config error.
func TestAnswerProviderSelection(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "answer", map[string]any{"query": "x", "provider": "perplexity"})
	if !res.IsError {
		t.Error("unknown provider should be rejected")
	}
	// "exa" is a supported name but not wired in this test's deps → config error.
	_, res2 := callTool(t, setupTestDeps(), "answer", map[string]any{"query": "x", "provider": "exa"})
	if !res2.IsError {
		t.Error("known-but-unconfigured provider should return a config error")
	}
}

// capturingSynth records the StructuredParams it received, to assert num_results
// clamping happens in the tool layer.
type capturingSynth struct{ mockSynthProvider }

func (m *capturingSynth) StructuredSearch(_ context.Context, p search.StructuredParams) (*search.StructuredResult, error) {
	gotStructuredNum = p.NumResults
	return &search.StructuredResult{Results: []search.StructuredItem{{URL: "https://x"}}, Provider: "mocksynth"}, nil
}

var gotStructuredNum int

func TestStructuredSearchClampsNumResults(t *testing.T) {
	cap := &capturingSynth{}
	deps := setupTestDeps()
	deps.StructuredProviders = map[string]search.StructuredProvider{cap.Name(): cap}

	gotStructuredNum = 0
	callTool(t, deps, "structured_search", map[string]any{"query": "x", "num_results": 999})
	if gotStructuredNum != maxNumResults {
		t.Errorf("num_results should clamp to %d, provider saw %d", maxNumResults, gotStructuredNum)
	}

	gotStructuredNum = 0
	callTool(t, deps, "structured_search", map[string]any{"query": "x"})
	if gotStructuredNum != 5 {
		t.Errorf("default num_results should be 5, provider saw %d", gotStructuredNum)
	}
}
