package tools

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var expectedTools = []string{
	"web_search",
	"image_search",
	"news_search",
	"academic_search",
	"patent_search",
	"scrape_page",
	"search_and_scrape",
	"sequential_search",
	"get_research_session",
}

func listTools(t *testing.T) []*mcp.Tool {
	t.Helper()
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	return result.Tools
}

func TestAllToolsRegistered(t *testing.T) {
	tools := listTools(t)
	registered := make(map[string]bool)
	for _, tool := range tools {
		registered[tool.Name] = true
	}
	for _, name := range expectedTools {
		if !registered[name] {
			t.Errorf("expected tool %q not registered", name)
		}
	}
}

func TestAllToolsHaveAnnotations(t *testing.T) {
	tools := listTools(t)
	for _, tool := range tools {
		t.Run(tool.Name, func(t *testing.T) {
			if tool.Annotations == nil {
				t.Fatal("annotations is nil")
			}
			if !tool.Annotations.ReadOnlyHint {
				t.Error("ReadOnlyHint should be true")
			}
			if tool.Annotations.DestructiveHint == nil {
				t.Error("DestructiveHint should be set")
			} else if *tool.Annotations.DestructiveHint {
				t.Error("DestructiveHint should be false")
			}
			if tool.Annotations.OpenWorldHint == nil {
				t.Error("OpenWorldHint should be set")
			}
			switch tool.Name {
			case "sequential_search":
				if tool.Annotations.IdempotentHint {
					t.Error("sequential_search should NOT be idempotent")
				}
				if *tool.Annotations.OpenWorldHint {
					t.Error("sequential_search should NOT be open-world")
				}
			case "get_research_session":
				if !tool.Annotations.IdempotentHint {
					t.Error("get_research_session should be idempotent")
				}
				if *tool.Annotations.OpenWorldHint {
					t.Error("get_research_session should NOT be open-world")
				}
			default:
				if !tool.Annotations.IdempotentHint {
					t.Errorf("%s should be idempotent", tool.Name)
				}
				if !*tool.Annotations.OpenWorldHint {
					t.Errorf("%s should be open-world", tool.Name)
				}
			}
		})
	}
}

func TestAllToolsHaveOutputSchema(t *testing.T) {
	tools := listTools(t)
	for _, tool := range tools {
		t.Run(tool.Name, func(t *testing.T) {
			if tool.OutputSchema == nil {
				t.Fatal("OutputSchema is nil")
			}
			schemaMap, ok := tool.OutputSchema.(map[string]any)
			if !ok {
				t.Fatalf("OutputSchema is not map[string]any, got %T", tool.OutputSchema)
			}
			if schemaMap["type"] != "object" {
				t.Errorf("OutputSchema type should be 'object', got %v", schemaMap["type"])
			}
			props, ok := schemaMap["properties"].(map[string]any)
			if !ok || len(props) == 0 {
				t.Error("OutputSchema should have non-empty properties")
			}
		})
	}
}

func TestToolDescriptionQuality(t *testing.T) {
	tools := listTools(t)
	for _, tool := range tools {
		t.Run(tool.Name, func(t *testing.T) {
			desc := tool.Description
			if len(desc) < 100 {
				t.Errorf("description too short (%d chars), minimum 100", len(desc))
			}
			hasAlternative := false
			for _, alt := range expectedTools {
				if alt != tool.Name && strings.Contains(desc, alt) {
					hasAlternative = true
					break
				}
			}
			if !hasAlternative {
				t.Error("description should mention at least one alternative tool")
			}
		})
	}
}

func TestOutputSchemaMatchesResponse(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	toolInputs := map[string]map[string]any{
		"web_search":        {"query": "test"},
		"image_search":      {"query": "test"},
		"news_search":       {"query": "test"},
		"academic_search":   {"query": "test"},
		"patent_search":     {"query": "test"},
		"sequential_search": {"searchStep": "initial research", "stepNumber": 1, "nextStepNeeded": false},
	}

	tools := listTools(t)
	schemaMap := make(map[string]map[string]any)
	for _, tool := range tools {
		if s, ok := tool.OutputSchema.(map[string]any); ok {
			schemaMap[tool.Name] = s
		}
	}

	for name, args := range toolInputs {
		t.Run(name, func(t *testing.T) {
			res, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name:      name,
				Arguments: args,
			})
			if err != nil {
				t.Fatalf("CallTool failed: %v", err)
			}
			if res.IsError {
				return
			}
			text := res.Content[0].(*mcp.TextContent).Text
			var output map[string]any
			if err := json.Unmarshal([]byte(text), &output); err != nil {
				t.Fatalf("response is not valid JSON: %v", err)
			}

			schema, ok := schemaMap[name]
			if !ok {
				t.Fatal("no schema found for tool")
			}
			props, _ := schema["properties"].(map[string]any)
			for key := range output {
				if _, declared := props[key]; !declared {
					t.Errorf("response field %q not declared in OutputSchema", key)
				}
			}
		})
	}
}

func TestAnnotationsStableUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	baseline, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan string, 50)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := session.ListTools(ctx, nil)
			if err != nil {
				errs <- err.Error()
				return
			}
			if len(result.Tools) != len(baseline.Tools) {
				errs <- "tool count mismatch"
				return
			}
			for j, tool := range result.Tools {
				if tool.Name != baseline.Tools[j].Name {
					errs <- "tool order changed"
					return
				}
				if tool.Annotations == nil {
					errs <- tool.Name + ": annotations nil"
					return
				}
				if tool.Annotations.ReadOnlyHint != baseline.Tools[j].Annotations.ReadOnlyHint {
					errs <- tool.Name + ": ReadOnlyHint changed"
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
