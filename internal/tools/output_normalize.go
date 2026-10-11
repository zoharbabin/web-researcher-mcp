package tools

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// addTool is mcp.AddTool plus a guard for a bug class that bit three tools at
// once: a nil Go slice marshals as JSON `null`, but the published
// OutputSchema says `array`, so MCP clients reject the whole tool result.
// Handlers should still return empty slices, but this makes the schema the
// last line of defense: any `null` where the schema demands an array becomes
// `[]` before the result leaves the server.
func addTool[In, Out any](srv *mcp.Server, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	schema, _ := t.OutputSchema.(map[string]any)
	if schema == nil {
		mcp.AddTool(srv, t, h)
		return
	}
	mcp.AddTool(srv, t, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		res, out, err := h(ctx, req, in)
		normalizeNullArrays(res, schema)
		return res, out, err
	})
}

// normalizeNullArrays rewrites res in place. Only successful results with
// JSON structured content are touched, and only when a null was replaced.
func normalizeNullArrays(res *mcp.CallToolResult, schema map[string]any) {
	if res == nil || res.IsError {
		return
	}
	raw, ok := res.StructuredContent.(json.RawMessage)
	if !ok || len(raw) == 0 {
		return
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return
	}
	fixed, changed := fillNullArrays(doc, schema)
	if !changed {
		return
	}
	out, err := json.Marshal(fixed)
	if err != nil {
		return
	}
	res.StructuredContent = json.RawMessage(out)
	// structuredResult mirrors the same bytes into the text block; keep them equal.
	for i, c := range res.Content {
		if tc, isText := c.(*mcp.TextContent); isText && tc.Text == string(raw) {
			res.Content[i] = &mcp.TextContent{Text: string(out)}
		}
	}
}

// fillNullArrays walks v alongside its schema and replaces a null with an
// empty array wherever the schema type is exactly "array". Absent keys and
// nullable types (e.g. ["array","null"]) are left alone.
func fillNullArrays(v any, schema map[string]any) (any, bool) {
	switch node := v.(type) {
	case nil:
		if t, _ := schema["type"].(string); t == "array" {
			return []any{}, true
		}
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		extra, _ := schema["additionalProperties"].(map[string]any)
		changed := false
		for k, child := range node {
			sub, _ := props[k].(map[string]any)
			if sub == nil {
				sub = extra
			}
			if sub == nil {
				continue
			}
			if fixed, c := fillNullArrays(child, sub); c {
				node[k] = fixed
				changed = true
			}
		}
		return node, changed
	case []any:
		items, _ := schema["items"].(map[string]any)
		if items == nil {
			return node, false
		}
		changed := false
		for i, child := range node {
			if fixed, c := fillNullArrays(child, items); c {
				node[i] = fixed
				changed = true
			}
		}
		return node, changed
	}
	return v, false
}

// nonNil returns s, or an empty slice when s is nil, so it marshals as `[]`.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
