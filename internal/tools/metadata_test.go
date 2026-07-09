package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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
	"research_export",
	"format_bibliography",
	"verify_citation",
	"audit_bibliography",
	"archive_source",
	"verify_recommendation",
	"citation_graph",
	"filing_search",
	"legal_search",
	"econ_search",
	"clinical_search",
	"awesome_list_search",
	"local_search",
	"answer",
	"structured_search",
	"get_my_analytics",
	"memory_save",
	"memory_recall",
	"workspace_contribute",
	"workspace_read",
	"brand_research",
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
			// memory_save is the one WRITE tool (it persists a memory). Every
			// other tool is read-only. No tool is ever destructive — deletion is
			// the separate #85 erasure endpoint, never a tool flag.
			writeTools := map[string]bool{"memory_save": true, "workspace_contribute": true, "archive_source": true}
			if writeTools[tool.Name] {
				if tool.Annotations.ReadOnlyHint {
					t.Errorf("%s writes state; ReadOnlyHint should be false", tool.Name)
				}
			} else if !tool.Annotations.ReadOnlyHint {
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
			case "research_export":
				// Renders existing session state: idempotent, local (not open-world).
				if !tool.Annotations.IdempotentHint {
					t.Error("research_export should be idempotent")
				}
				if *tool.Annotations.OpenWorldHint {
					t.Error("research_export should NOT be open-world")
				}
			case "format_bibliography":
				if !tool.Annotations.IdempotentHint {
					t.Error("format_bibliography should be idempotent")
				}
				if *tool.Annotations.OpenWorldHint {
					t.Error("format_bibliography should NOT be open-world")
				}
			case "get_my_analytics":
				// Reads internal per-user state: idempotent, not open-world.
				if !tool.Annotations.IdempotentHint {
					t.Error("get_my_analytics should be idempotent")
				}
				if *tool.Annotations.OpenWorldHint {
					t.Error("get_my_analytics should NOT be open-world")
				}
			case "memory_recall":
				if !tool.Annotations.IdempotentHint {
					t.Error("memory_recall should be idempotent")
				}
				if *tool.Annotations.OpenWorldHint {
					t.Error("memory_recall should NOT be open-world")
				}
			case "memory_save":
				// A write; not idempotent (each save appends a new entry), not open-world.
				if tool.Annotations.IdempotentHint {
					t.Error("memory_save should NOT be idempotent")
				}
				if *tool.Annotations.OpenWorldHint {
					t.Error("memory_save should NOT be open-world")
				}
			case "workspace_contribute":
				// A write; not idempotent (appends a contribution), not open-world.
				if tool.Annotations.IdempotentHint {
					t.Error("workspace_contribute should NOT be idempotent")
				}
				if *tool.Annotations.OpenWorldHint {
					t.Error("workspace_contribute should NOT be open-world")
				}
			case "archive_source":
				// A write (creates a public IA snapshot); idempotent (SPN dedups within
				// its rate window). writeAnnotations forces OpenWorldHint:false.
				if !tool.Annotations.IdempotentHint {
					t.Error("archive_source should be idempotent")
				}
				if *tool.Annotations.OpenWorldHint {
					t.Error("archive_source should NOT be open-world (writeAnnotations forces false)")
				}
			case "workspace_read":
				if !tool.Annotations.IdempotentHint {
					t.Error("workspace_read should be idempotent")
				}
				if *tool.Annotations.OpenWorldHint {
					t.Error("workspace_read should NOT be open-world")
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
		"web_search":          {"query": "test"},
		"image_search":        {"query": "test"},
		"news_search":         {"query": "test"},
		"academic_search":     {"query": "test"},
		"patent_search":       {"query": "test"},
		"sequential_search":   {"searchStep": "initial research", "stepNumber": 1, "nextStepNeeded": false},
		"citation_graph":      {"paper": "10.1/x"},
		"answer":              {"query": "test"},
		"structured_search":   {"query": "test"},
		"format_bibliography": {"sources": []any{map[string]any{"url": "https://example.com/a", "title": "A", "author": "Smith, J.", "date": "2024"}}},
		"audit_bibliography":  {"entries": []any{map[string]any{"url": "https://example.com/a", "title": "A", "doi": "10.1/x"}}},
		// setupTestDeps has a nil LinkVerifier → archive_source returns status:"unavailable",
		// locking the unavailable-path keys (requestedUrl/status/reason/source/trust)
		// against archiveSourceOutputSchema. The content-path keys are covered by the
		// stub-driven handler tests in archive_source_test.go.
		"archive_source":      {"url": "https://example.com"},
		"filing_search":       {"query": "AAPL"},
		"legal_search":        {"query": "miranda"},
		"econ_search":         {"series_id": "GDP"},
		"clinical_search":     {"condition": "covid-19"},
		"awesome_list_search": {"topic": "osint"},
		"local_search":        {"query": "coffee near me"},
		"brand_research":      {"url": "example.com"},
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

// TestExternalContentToolsCarryTrustMarker is a drift guard for the tools that
// return external content on an UNAUTHENTICATED call (the search family +
// scrape + sequential_search): each MUST stamp a top-level "trust" boundary
// marker (OWASP LLM01 / Agentic ASI05). A new such tool shipping without a
// marker (or with the wrong value) fails here.
//
// Scope note: the consent-gated tools that also carry markers —
// memory_recall ("user-asserted-content") and workspace_read /
// get_research_session ("untrusted-external-content") — return a denial (no
// content) for the anonymous client this harness uses, so they cannot be
// exercised here. Their markers are asserted in their own tests
// (TestMemoryRecall*, TestWorkspace*, getsession tests). Tools that return no
// model-facing content (memory_save, workspace_contribute, get_my_analytics)
// carry no marker by design.
func TestExternalContentToolsCarryTrustMarker(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	// tool -> required trust value. Search/scrape/session/workspace echo external
	// content; memory_recall echoes user-asserted content.
	want := map[string]string{
		"web_search":          "untrusted-external-content",
		"image_search":        "untrusted-external-content",
		"news_search":         "untrusted-external-content",
		"academic_search":     "untrusted-external-content",
		"patent_search":       "untrusted-external-content",
		"scrape_page":         "untrusted-external-content",
		"search_and_scrape":   "untrusted-external-content",
		"sequential_search":   "untrusted-external-content",
		"citation_graph":      "untrusted-external-content",
		"verify_citation":     "untrusted-external-content",
		"audit_bibliography":  "untrusted-external-content",
		"answer":              "untrusted-external-content",
		"structured_search":   "untrusted-external-content",
		"filing_search":       "untrusted-external-content",
		"legal_search":        "untrusted-external-content",
		"econ_search":         "untrusted-external-content",
		"clinical_search":     "untrusted-external-content",
		"awesome_list_search": "untrusted-external-content",
		"local_search":        "untrusted-external-content",
	}
	args := map[string]map[string]any{
		"web_search":          {"query": "test"},
		"image_search":        {"query": "test"},
		"news_search":         {"query": "test"},
		"academic_search":     {"query": "test"},
		"patent_search":       {"query": "test"},
		"scrape_page":         {"url": "https://example.com"},
		"search_and_scrape":   {"query": "test"},
		"sequential_search":   {"searchStep": "initial research", "stepNumber": 1, "nextStepNeeded": false},
		"citation_graph":      {"paper": "10.1/x"},
		"verify_citation":     {"citation": "https://example.com/paper"},
		"audit_bibliography":  {"entries": []any{map[string]any{"url": "https://example.com/paper", "title": "A"}}},
		"answer":              {"query": "test"},
		"structured_search":   {"query": "test"},
		"filing_search":       {"query": "AAPL"},
		"legal_search":        {"query": "miranda"},
		"econ_search":         {"series_id": "GDP"},
		"clinical_search":     {"condition": "covid-19"},
		"awesome_list_search": {"topic": "osint"},
		"local_search":        {"query": "coffee near me"},
	}

	for name, wantTrust := range want {
		t.Run(name, func(t *testing.T) {
			res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args[name]})
			if err != nil {
				t.Fatalf("CallTool(%s) failed: %v", name, err)
			}
			if res.IsError {
				return // upstream/network unavailable in unit env — schema gate covers shape
			}
			var out map[string]any
			if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
				t.Fatalf("parse(%s): %v", name, err)
			}
			if out["trust"] != wantTrust {
				t.Errorf("%s: trust = %v, want %q", name, out["trust"], wantTrust)
			}
		})
	}
}

// repoRoot returns the repository root resolved relative to this test file,
// independent of the working directory the test is invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// this file is internal/tools/metadata_test.go -> up two dirs to repo root.
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

// TestToolsDocMatchesRegistry is the doc-drift guard: it fails CI if docs/TOOLS.md
// documents a different set of tools than the server actually registers at
// runtime. Adding, removing, or renaming a tool without updating TOOLS.md (or
// vice versa) breaks the build, keeping the doc honest by construction.
//
// It parses the "## Tool N: `name`" headers from TOOLS.md and compares that set
// to the live ListTools result — the runtime is the source of truth.
func TestToolsDocMatchesRegistry(t *testing.T) {
	docPath := filepath.Join(repoRoot(t), "docs", "TOOLS.md")
	data, err := os.ReadFile(docPath) // #nosec G304 -- fixed in-repo doc path, not user input
	if err != nil {
		t.Fatalf("read TOOLS.md: %v", err)
	}

	// Match headers like:  ## Tool 1: `web_search`
	re := regexp.MustCompile("(?m)^##+\\s+Tool\\s+\\d+:\\s+`([a-z_]+)`")
	matches := re.FindAllStringSubmatch(string(data), -1)
	documented := make(map[string]bool, len(matches))
	for _, m := range matches {
		documented[m[1]] = true
	}
	if len(documented) == 0 {
		t.Fatal("no tool headers found in docs/TOOLS.md — expected '## Tool N: `name`' format")
	}

	registered := make(map[string]bool)
	for _, tool := range listTools(t) {
		registered[tool.Name] = true
	}

	for name := range registered {
		if !documented[name] {
			t.Errorf("tool %q is registered but NOT documented in docs/TOOLS.md", name)
		}
	}
	for name := range documented {
		if !registered[name] {
			t.Errorf("docs/TOOLS.md documents tool %q that is NOT registered (stale doc)", name)
		}
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
