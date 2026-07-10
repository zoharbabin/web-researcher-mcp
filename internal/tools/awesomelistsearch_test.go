package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func callAwesomeList(t *testing.T, deps Dependencies, args map[string]any) (map[string]any, bool) {
	t.Helper()
	ctx := context.Background()
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "awesome_list_search", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		return nil, true
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return out, false
}

func TestAwesomeListSearchHappyPath(t *testing.T) {
	out, isErr := callAwesomeList(t, setupTestDeps(), map[string]any{"topic": "osint", "num_results": 5})
	if isErr {
		t.Fatal("unexpected tool error")
	}
	if out["trust"] != "untrusted-external-content" {
		t.Errorf("trust marker missing: %v", out["trust"])
	}
	if out["provider"] != "ecosystems" {
		t.Errorf("provider = %v, want ecosystems", out["provider"])
	}
	lists, ok := out["lists"].([]any)
	if !ok || len(lists) != 1 {
		t.Fatalf("expected 1 list, got %v", out["lists"])
	}
	l, _ := lists[0].(map[string]any)
	if l["name"] != "awesome-osint" {
		t.Errorf("name = %v", l["name"])
	}
	if l["fullName"] != "jivoi/awesome-osint" {
		t.Errorf("fullName = %v", l["fullName"])
	}
	if l["url"] != "https://github.com/jivoi/awesome-osint" {
		t.Errorf("url = %v", l["url"])
	}
	if l["stars"] != float64(27176) {
		t.Errorf("stars = %v, want 27176", l["stars"])
	}
	if l["projectsCount"] != float64(1431) {
		t.Errorf("projectsCount = %v, want 1431", l["projectsCount"])
	}
}

func TestAwesomeListSearchRequiresTopicOrQuery(t *testing.T) {
	_, isErr := callAwesomeList(t, setupTestDeps(), map[string]any{"num_results": 5})
	if !isErr {
		t.Error("a call with no topic/query should be a tool error")
	}
}

func TestAwesomeListSearchUnknownProvider(t *testing.T) {
	out, isErr := callAwesomeList(t, setupTestDeps(), map[string]any{"topic": "osint", "provider": "nope"})
	if !isErr {
		t.Errorf("unknown provider should error, got %v", out)
	}
}

func TestAwesomeListSearchQueryAlone(t *testing.T) {
	out, isErr := callAwesomeList(t, setupTestDeps(), map[string]any{"query": "osint"})
	if isErr {
		t.Fatal("query alone should be accepted (no topic required)")
	}
	if out["resultCount"] != float64(1) {
		t.Errorf("resultCount = %v, want 1", out["resultCount"])
	}
}

// TestAwesomeListSearchZeroResultHintsSuggestRephrase verifies the zero-result
// hint tells the calling model to retry with a different word/phrase — since
// ecosyste.ms topic matching is exact-string with no stemming (verified live:
// "parenting" 404s while "parent" matches), the model needs to be told to
// rephrase rather than conclude no list exists.
func TestAwesomeListSearchZeroResultHintsSuggestRephrase(t *testing.T) {
	out, isErr := callAwesomeList(t, setupTestDeps(), map[string]any{"topic": "nomatch"})
	if isErr {
		t.Fatal("zero-result should not be a tool error")
	}
	hints, ok := out["hints"].(map[string]any)
	if !ok {
		t.Fatalf("expected hints on zero result, got %v", out)
	}
	actions, ok := hints["suggestedActions"].([]any)
	if !ok || len(actions) == 0 {
		t.Fatalf("expected suggestedActions, got %v", hints["suggestedActions"])
	}
	var found bool
	for _, a := range actions {
		action, _ := a.(map[string]any)
		if action["action"] == "rephrase_query" {
			found = true
			if detail, _ := action["detail"].(string); detail == "" {
				t.Error("rephrase_query hint should have a non-empty detail")
			}
		}
	}
	if !found {
		t.Errorf("expected a rephrase_query suggested action, got %v", actions)
	}
}
