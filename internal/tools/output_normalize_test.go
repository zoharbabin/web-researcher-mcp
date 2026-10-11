package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFillNullArrays(t *testing.T) {
	t.Parallel()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"list":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"nullable": map[string]any{"type": []any{"array", "null"}},
			"name":     map[string]any{"type": "string"},
			"nested": map[string]any{
				"type":       "object",
				"properties": map[string]any{"inner": map[string]any{"type": "array"}},
			},
			"rows": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":       "object",
					"properties": map[string]any{"tags": map[string]any{"type": "array"}},
				},
			},
			"byKey": map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "array"},
			},
		},
	}
	in := `{"list":null,"nullable":null,"name":null,"nested":{"inner":null},"rows":[{"tags":null},{"tags":["a"]}],"byKey":{"x":null},"undeclared":null}`
	var doc any
	if err := json.Unmarshal([]byte(in), &doc); err != nil {
		t.Fatal(err)
	}
	got, changed := fillNullArrays(doc, schema)
	if !changed {
		t.Fatal("expected changes")
	}
	b, _ := json.Marshal(got)
	want := `{"byKey":{"x":[]},"list":[],"name":null,"nested":{"inner":[]},"nullable":null,"rows":[{"tags":[]},{"tags":["a"]}],"undeclared":null}`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}

func TestFillNullArraysNoChange(t *testing.T) {
	t.Parallel()
	schema := map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "array"}}}
	var doc any
	_ = json.Unmarshal([]byte(`{"a":[1]}`), &doc)
	if _, changed := fillNullArrays(doc, schema); changed {
		t.Error("expected no change for a populated array")
	}
}

// addTool must rewrite both the structured content and the text block, and
// leave error results alone.
func TestAddToolNormalizesNullArrays(t *testing.T) {
	ctx := context.Background()
	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"items": map[string]any{"type": "array"}},
	}
	addTool(srv, &mcp.Tool{Name: "nil_list", Description: "d", InputSchema: map[string]any{"type": "object"}, OutputSchema: schema},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			return structuredResult([]byte(`{"items":null}`)), nil, nil
		})
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "nil_list", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if got := res.Content[0].(*mcp.TextContent).Text; got != `{"items":[]}` {
		t.Errorf("text = %s", got)
	}
	sc, _ := json.Marshal(res.StructuredContent)
	if string(sc) != `{"items":[]}` {
		t.Errorf("structured = %s", sc)
	}
}
