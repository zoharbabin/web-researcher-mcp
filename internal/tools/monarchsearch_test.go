package tools

import (
	"context"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// emptyMonarchProvider implements search.MonarchProvider and always returns
// zero results, so monarch_search's zero-result hints branch (#537) can be
// exercised in a test.
type emptyMonarchProvider struct{}

func (m *emptyMonarchProvider) Name() string { return "monarch" }
func (m *emptyMonarchProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "empty monarch"}
}
func (m *emptyMonarchProvider) Search(_ context.Context, _ search.MonarchSearchParams) ([]search.MonarchResult, error) {
	return nil, nil
}

// TestMonarchSearchZeroResultHintsExcludeOperation guards #537: monarch_search's
// zero-result hints must never suggest removing the required `operation`
// param — removing it isn't a valid recovery action, since operation is
// mandatory for every call. entityId (not required) should still be
// suggested for removal.
func TestMonarchSearchZeroResultHintsExcludeOperation(t *testing.T) {
	deps := setupTestDeps()
	deps.MonarchProviders = map[string]search.MonarchProvider{"monarch": &emptyMonarchProvider{}}

	out, res := callTool(t, deps, "monarch_search", map[string]any{
		"operation": "entity",
		"entityId":  "MONDO:0007947",
	})
	if res.IsError {
		t.Fatalf("unexpected error result: %v", res.Content)
	}
	if out["resultCount"].(float64) != 0 {
		t.Fatalf("expected 0 results, got %v", out["resultCount"])
	}
	hints, ok := out["hints"].(map[string]any)
	if !ok {
		t.Fatalf("expected hints object on zero results, got %v", out["hints"])
	}
	filters, _ := hints["filtersApplied"].(map[string]any)
	if filters["operation"] != "entity" {
		t.Errorf("operation should still be surfaced in filtersApplied for context, got %v", filters)
	}
	actions, _ := hints["suggestedActions"].([]any)
	for _, a := range actions {
		action, _ := a.(map[string]any)
		if action["action"] == "remove_filter" && action["parameter"] == "operation" {
			t.Errorf("must never suggest removing the required operation param, got %v", actions)
		}
	}
	found := false
	for _, a := range actions {
		action, _ := a.(map[string]any)
		if action["action"] == "remove_filter" && action["parameter"] == "entityId" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected entityId suggested for removal, got %v", actions)
	}
}

func TestMonarchSearchSemsim(t *testing.T) {
	out, res := callTool(t, setupTestDeps(), "monarch_search", map[string]any{
		"operation":  "semsim",
		"phenotypes": []any{"HP:0001166", "HP:0001083"},
	})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	if out["provider"] != "monarch" || out["trust"] != "untrusted-external-content" {
		t.Errorf("provider/trust: %v / %v", out["provider"], out["trust"])
	}
	results, ok := out["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("want 1 result, got %v", out["results"])
	}
	r0, _ := results[0].(map[string]any)
	if r0["id"] != "MONDO:0007947" || r0["ancestorId"] != "HP:0001166" {
		t.Errorf("unexpected result: %v", r0)
	}
}

func TestMonarchSearchEntity(t *testing.T) {
	out, res := callTool(t, setupTestDeps(), "monarch_search", map[string]any{
		"operation": "entity",
		"query":     "Marfan syndrome",
	})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	results, ok := out["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("want 1 result, got %v", out["results"])
	}
	r0, _ := results[0].(map[string]any)
	if r0["description"] == nil || r0["crossReferences"] == nil {
		t.Errorf("expected entity fields populated: %v", r0)
	}
}

func TestMonarchSearchAssociations(t *testing.T) {
	out, res := callTool(t, setupTestDeps(), "monarch_search", map[string]any{
		"operation": "associations",
		"entityId":  "MONDO:0007947",
	})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	results, ok := out["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("want 1 result, got %v", out["results"])
	}
	r0, _ := results[0].(map[string]any)
	if r0["subjectId"] != "HGNC:3603" || r0["objectId"] != "MONDO:0007947" {
		t.Errorf("unexpected association: %v", r0)
	}
}

func TestMonarchSearchCompare(t *testing.T) {
	out, res := callTool(t, setupTestDeps(), "monarch_search", map[string]any{
		"operation":  "compare",
		"phenotypes": []any{"HP:0001166"},
		"compareTo":  []any{"HP:0001083"},
	})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	results, ok := out["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("want 1 result, got %v", out["results"])
	}
}

func TestMonarchSearchAnnotate(t *testing.T) {
	out, res := callTool(t, setupTestDeps(), "monarch_search", map[string]any{
		"operation": "annotate",
		"text":      "patient has long fingers",
	})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	results, ok := out["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("want 1 result, got %v", out["results"])
	}
	r0, _ := results[0].(map[string]any)
	if r0["text"] != "long fingers" {
		t.Errorf("unexpected annotate text: %v", r0["text"])
	}
}

func TestMonarchSearchRequiresOperation(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "monarch_search", map[string]any{})
	if !res.IsError {
		t.Error("missing operation should error")
	}
}

func TestMonarchSearchUnknownOperation(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "monarch_search", map[string]any{"operation": "bogus"})
	if !res.IsError {
		t.Error("unknown operation should error")
	}
}

func TestMonarchSearchSemsimRequiresPhenotypes(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "monarch_search", map[string]any{"operation": "semsim"})
	if !res.IsError {
		t.Error("semsim without phenotypes should error")
	}
}

func TestMonarchSearchSemsimPhenotypeCap(t *testing.T) {
	phenotypes := make([]any, 21)
	for i := range phenotypes {
		phenotypes[i] = "HP:0001166"
	}
	_, res := callTool(t, setupTestDeps(), "monarch_search", map[string]any{
		"operation":  "semsim",
		"phenotypes": phenotypes,
	})
	if !res.IsError {
		t.Error("more than 20 phenotypes should error")
	}
}

func TestMonarchSearchSemsimInvalidGroup(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "monarch_search", map[string]any{
		"operation":  "semsim",
		"phenotypes": []any{"HP:0001166"},
		"group":      "Worm Genes",
	})
	if !res.IsError {
		t.Error("invalid semsim group should error")
	}
}

func TestMonarchSearchEntityRequiresQueryOrID(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "monarch_search", map[string]any{"operation": "entity"})
	if !res.IsError {
		t.Error("entity without query or entityId should error")
	}
}

func TestMonarchSearchRejectsPathTraversal(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "monarch_search", map[string]any{
		"operation": "entity",
		"entityId":  "../etc/passwd",
	})
	if !res.IsError {
		t.Error("path-traversal entityId should be rejected pre-flight")
	}
}

func TestMonarchSearchAssociationsRejectsPathTraversal(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "monarch_search", map[string]any{
		"operation": "associations",
		"entityId":  "../../etc/passwd",
	})
	if !res.IsError {
		t.Error("path-traversal entityId should be rejected pre-flight")
	}
}

func TestMonarchSearchAssociationsRequiresFacet(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "monarch_search", map[string]any{"operation": "associations"})
	if !res.IsError {
		t.Error("associations without entityId/assocSubject/assocObject should error")
	}
}

func TestMonarchSearchCompareRequiresBothLists(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "monarch_search", map[string]any{
		"operation":  "compare",
		"phenotypes": []any{"HP:0001166"},
	})
	if !res.IsError {
		t.Error("compare without compareTo should error")
	}
}

func TestMonarchSearchAnnotateRequiresText(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "monarch_search", map[string]any{"operation": "annotate"})
	if !res.IsError {
		t.Error("annotate without text should error")
	}
}

func TestMonarchSearchAnnotateTextCap(t *testing.T) {
	longText := make([]byte, 2001)
	for i := range longText {
		longText[i] = 'a'
	}
	_, res := callTool(t, setupTestDeps(), "monarch_search", map[string]any{
		"operation": "annotate",
		"text":      string(longText),
	})
	if !res.IsError {
		t.Error("text over 2000 chars should error")
	}
}

func TestMonarchSearchUnregisteredWithoutProvider(t *testing.T) {
	deps := setupTestDeps()
	deps.MonarchProviders = nil
	tools := toolNamesFor(t, deps)
	if tools["monarch_search"] {
		t.Error("monarch_search must NOT register without its provider")
	}
}

func TestMonarchSearchExplicitProvider(t *testing.T) {
	out, res := callTool(t, setupTestDeps(), "monarch_search", map[string]any{
		"operation":  "semsim",
		"phenotypes": []any{"HP:0001166"},
		"provider":   "monarch",
	})
	if res.IsError {
		t.Fatalf("unexpected error result")
	}
	if out["provider"] != "monarch" {
		t.Errorf("provider: %v", out["provider"])
	}
}

func TestMonarchSearchUnknownProvider(t *testing.T) {
	_, res := callTool(t, setupTestDeps(), "monarch_search", map[string]any{
		"operation":  "semsim",
		"phenotypes": []any{"HP:0001166"},
		"provider":   "bloomberg",
	})
	if !res.IsError {
		t.Error("unknown provider should error")
	}
}
