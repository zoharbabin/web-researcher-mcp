package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
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
	"monarch_search",
	"get_my_analytics",
	"memory_save",
	"memory_recall",
	"workspace_contribute",
	"workspace_read",
	"brand_research",
	"paper_fulltext",
	"company_recon",
	"research_panel",
	"monitor_query_save",
	"monitor_query_check",
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
			writeTools := map[string]bool{"memory_save": true, "workspace_contribute": true, "archive_source": true, "monitor_query_save": true}
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
			case "company_recon":
				// Not idempotent: crt.sh/Wayback CDX results grow over time (new
				// certs logged, new captures archived) between two identical calls.
				if tool.Annotations.IdempotentHint {
					t.Error("company_recon should NOT be idempotent")
				}
				if !*tool.Annotations.OpenWorldHint {
					t.Error("company_recon should be open-world")
				}
			case "monitor_query_save":
				// A write (seeds/updates a monitor baseline); not idempotent
				// (re-running with the same query re-seeds the baseline from a
				// fresh live search), not open-world (writeAnnotations forces false).
				if tool.Annotations.IdempotentHint {
					t.Error("monitor_query_save should NOT be idempotent")
				}
				if *tool.Annotations.OpenWorldHint {
					t.Error("monitor_query_save should NOT be open-world")
				}
			case "monitor_query_check":
				// Read-only but mutates the stored baseline on every call (marks
				// found URLs as seen), so a second identical call returns zero new
				// results: NOT idempotent. Open-world (live upstream search).
				if tool.Annotations.IdempotentHint {
					t.Error("monitor_query_check should NOT be idempotent")
				}
				if !*tool.Annotations.OpenWorldHint {
					t.Error("monitor_query_check should be open-world")
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
		"monarch_search":      {"operation": "entity", "query": "Marfan syndrome"},
		"brand_research":      {"url": "example.com"},
		"paper_fulltext":      {"identifier": "https://example.com/paper.pdf"},
		"company_recon":       {"target": "example.com"},
		"research_panel":      {"query": "test"},
		// Anonymous test client has no auth context, so both return status:
		// "unavailable" rather than exercising the content path (covered by
		// monitor_test.go's authenticated-context tests).
		"monitor_query_save":  {"query": "test"},
		"monitor_query_check": {"query": "test"},
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
		"filing_search":       "untrusted-external-content",
		"legal_search":        "untrusted-external-content",
		"econ_search":         "untrusted-external-content",
		"clinical_search":     "untrusted-external-content",
		"awesome_list_search": "untrusted-external-content",
		"local_search":        "untrusted-external-content",
		"paper_fulltext":      "untrusted-external-content",
		"monarch_search":      "untrusted-external-content",
		"research_panel":      "untrusted-external-content",
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
		"filing_search":       {"query": "AAPL"},
		"legal_search":        {"query": "miranda"},
		"econ_search":         {"series_id": "GDP"},
		"clinical_search":     {"condition": "covid-19"},
		"awesome_list_search": {"topic": "osint"},
		"local_search":        {"query": "coffee near me"},
		"paper_fulltext":      {"identifier": "https://example.com/paper.pdf"},
		"monarch_search":      {"operation": "entity", "query": "Marfan syndrome"},
		"research_panel":      {"query": "test"},
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

// structuredDomainDocTools is the canonical set of tools that docs/PROVIDERS.md's
// "Structured-Domain Providers" table must document — each backed by its own
// dedicated, always/keyless-registered provider (structured_domains.go's
// Filing/Case/Econ/Trial capabilities, plus AwesomeListProvider, plus the two
// single-purpose tools archive_source and brand_research). Mirrors expectedTools
// in spirit: adding a tool here (e.g. a new capability under structured_domains.go)
// means adding its row to docs/PROVIDERS.md in the same change, exactly like
// adding to expectedTools means adding a docs/TOOLS.md section.
var structuredDomainDocTools = map[string]bool{
	"filing_search":       true,
	"legal_search":        true,
	"econ_search":         true,
	"clinical_search":     true,
	"awesome_list_search": true,
	"monarch_search":      true,
	"archive_source":      true,
	"brand_research":      true,
	"company_recon":       true,
}

// TestProvidersDocStructuredDomainTable is a doc-drift guard for docs/PROVIDERS.md's
// "Structured-Domain Providers" table: it fails CI if that table's tool column
// diverges from structuredDomainDocTools, or if it lists a tool that isn't
// actually registered (stale doc). This is what would have caught
// awesome_list_search shipping (#375) without a matching PROVIDERS.md row.
func TestProvidersDocStructuredDomainTable(t *testing.T) {
	docPath := filepath.Join(repoRoot(t), "docs", "PROVIDERS.md")
	data, err := os.ReadFile(docPath) // #nosec G304 -- fixed in-repo doc path, not user input
	if err != nil {
		t.Fatalf("read PROVIDERS.md: %v", err)
	}

	const marker = "## Structured-Domain Providers"
	start := strings.Index(string(data), marker)
	if start == -1 {
		t.Fatal("docs/PROVIDERS.md missing '## Structured-Domain Providers' section")
	}
	section := string(data)[start:]
	if end := strings.Index(section, "\n## "); end != -1 {
		section = section[:end]
	}

	re := regexp.MustCompile("(?m)^\\|\\s*`([a-z_]+)`\\s*\\|")
	matches := re.FindAllStringSubmatch(section, -1)
	documented := make(map[string]bool, len(matches))
	for _, m := range matches {
		documented[m[1]] = true
	}
	if len(documented) == 0 {
		t.Fatal("no tool rows found in docs/PROVIDERS.md's Structured-Domain Providers table")
	}

	registered := make(map[string]bool)
	for _, tool := range listTools(t) {
		registered[tool.Name] = true
	}

	for name := range structuredDomainDocTools {
		if !documented[name] {
			t.Errorf("tool %q belongs in docs/PROVIDERS.md's Structured-Domain Providers table but is missing", name)
		}
		if !registered[name] {
			t.Errorf("structuredDomainDocTools lists %q but it is NOT registered (stale test expectation)", name)
		}
	}
	for name := range documented {
		if !structuredDomainDocTools[name] {
			t.Errorf("docs/PROVIDERS.md documents tool %q not in structuredDomainDocTools — update the test's expected set if this is intentional", name)
		}
	}
}

// TestDeploymentDocK8sExampleHardened is a doc-content guard for issues
// #473 (K8s example memory limits undersized for Chromium) and #487 (missing
// K8s SecurityContext reference): it fails if docs/DEPLOYMENT.md's
// Kubernetes example regresses to a memory limit too small for the
// browser-tier RSS footprint, or drops the securityContext hardening block.
func TestDeploymentDocK8sExampleHardened(t *testing.T) {
	docPath := filepath.Join(repoRoot(t), "docs", "DEPLOYMENT.md")
	data, err := os.ReadFile(docPath) // #nosec G304 -- fixed in-repo doc path, not user input
	if err != nil {
		t.Fatalf("read DEPLOYMENT.md: %v", err)
	}

	const marker = "## Kubernetes"
	start := strings.Index(string(data), marker)
	if start == -1 {
		t.Fatal("docs/DEPLOYMENT.md missing '## Kubernetes' section")
	}
	section := string(data)[start:]
	if end := strings.Index(section, "\n## "); end != -1 {
		section = section[:end]
	}

	// Extract the container-level `limits: memory: <N>Mi|Gi` value and require
	// it to be at least 1Gi — the browser-tier RSS footprint (300-500Mi) plus
	// Go heap/cache means anything smaller repeats the #473 OOMKill gap.
	memRe := regexp.MustCompile(`limits:\s*\n\s*cpu:\s*\S+\s*\n\s*memory:\s*([\d.]+)(Mi|Gi)`)
	m := memRe.FindStringSubmatch(section)
	if m == nil {
		t.Fatal("docs/DEPLOYMENT.md's Kubernetes example is missing a container `limits.memory` value")
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("parse memory limit %q: %v", m[1], err)
	}
	mi := val
	if m[2] == "Gi" {
		mi = val * 1024
	}
	const minMi = 1024 // 1Gi
	if mi < minMi {
		t.Errorf("docs/DEPLOYMENT.md's Kubernetes example limits.memory is %s%s (%.0fMi) — must be >= 1Gi to cover Chromium RSS + Go heap + cache (issue #473)", m[1], m[2], mi)
	}

	for _, want := range []string{"securityContext", "runAsNonRoot", "allowPrivilegeEscalation", "readOnlyRootFilesystem", "drop"} {
		if !strings.Contains(section, want) {
			t.Errorf("docs/DEPLOYMENT.md's Kubernetes example is missing %q in its securityContext block (issue #487)", want)
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

// --- #548: JSON Schema enum drift gates --------------------------------
//
// The four checks below guard the closed-vocabulary → real-enum conversion
// (issue #548): a described-but-unenumerated smell test, an enum-value/
// tool-name collision guard, a tautological-description gate, and a
// byte-for-byte parity check between a resolver's "unknown provider" error
// list and the same field's schema Enum.

// schemaEnumStrings extracts a property schema's enum values (checking both
// a direct `enum` and an array field's `items.enum`) as []string. Returns nil
// when neither is present or elements aren't strings.
func schemaEnumStrings(pschema map[string]any) []string {
	raw := pschema["enum"]
	if raw == nil {
		if items, ok := pschema["items"].(map[string]any); ok {
			raw = items["enum"]
		}
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// toStringSlice converts an enums.go helper's []any return value (always
// string elements in practice) to []string.
func toStringSlice(vals []any) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// sameStringSet reports whether a and b contain the same strings, ignoring
// order and counting duplicates (so [a a b] != [a b]).
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, s := range a {
		counts[s]++
	}
	for _, s := range b {
		counts[s]--
	}
	for _, c := range counts {
		if c != 0 {
			return false
		}
	}
	return true
}

// describedButNotEnumeratedSmellRe flags a description that reads like a
// closed vocabulary ("one of X, Y, Z", "choose X, Y, Z", or "field: X, Y, Z")
// — a candidate for a missing real enum.
var describedButNotEnumeratedSmellRe = regexp.MustCompile(`(?i)(one of|choose|:)\s*[a-z0-9_.'-]+(\s*,\s*[a-z0-9_.'-]+){2,}`)

// describedButNotEnumeratedExceptions lists tool.field pairs whose
// description matches the smell regex above but are deliberately prose-only
// (#548). Each entry names why enumerating it would be wrong, not merely
// incomplete:
//   - web_search.lens: runtime-extensible via CUSTOM_LENSES_PATH — the valid
//     set isn't knowable at compile time.
//   - clinical_search.status, filing_search.form_type, legal_search.jurisdiction,
//     econ_search.frequency, econ_search.units, monarch_search.category: each
//     names an externally-governed, open-ended vocabulary (ClinicalTrials.gov
//     status codes, SEC form types, court IDs, FRED resample/units codes,
//     Biolink association categories) that this server does not — and should
//     not — hardcode a closed list for; see each tool's own file for the
//     field's jsonschema tag and reasoning.
//   - audit_bibliography.bibliography: the description names the DOCUMENT
//     FORMATS a free-text bibliography blob may be written in (CSL-JSON, RIS,
//     BibTeX) — it constrains the field's FORMAT, not the field's own value
//     set (which is arbitrary bibliography content). A false positive for
//     this regex, not a missed conversion.
var describedButNotEnumeratedExceptions = map[string]bool{
	"web_search.lens":                 true,
	"clinical_search.status":          true,
	"filing_search.form_type":         true,
	"legal_search.jurisdiction":       true,
	"econ_search.frequency":           true,
	"econ_search.units":               true,
	"monarch_search.category":         true,
	"audit_bibliography.bibliography": true,
}

// TestDescribedButNotEnumerated (#548) fails when a parameter's description
// reads like a closed vocabulary but the schema carries no real enum — the
// exact prose-only-enum smell issue #548 set out to eliminate. A new tool or
// field that reintroduces this pattern must either get a real Enum (see
// internal/tools/enums.go) or an explicit, reasoned entry in
// describedButNotEnumeratedExceptions above.
func TestDescribedButNotEnumerated(t *testing.T) {
	for _, tool := range listTools(t) {
		schemaMap, ok := tool.InputSchema.(map[string]any)
		if !ok {
			continue
		}
		props, _ := schemaMap["properties"].(map[string]any)
		for propName, raw := range props {
			pschema, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			desc, _ := pschema["description"].(string)
			if desc == "" || !describedButNotEnumeratedSmellRe.MatchString(desc) {
				continue
			}
			key := tool.Name + "." + propName
			if describedButNotEnumeratedExceptions[key] {
				continue
			}
			if len(schemaEnumStrings(pschema)) == 0 {
				t.Errorf("%s: description reads like a closed vocabulary (%q) but has no enum/items.enum — add a real Enum in enums.go, or if this field is genuinely open-ended, add %q to describedButNotEnumeratedExceptions with a reason", key, desc, key)
			}
		}
	}
}

// TestEnumToolNameCollision (#548) guards against the exact ambiguity that
// motivated issue #548: a provider value (or any other enum value) that is
// itself the name of a different registered tool, which an LLM client could
// plausibly confuse for a tool-selection instruction (e.g. a "provider":
// "news" value colliding with the news_search tool). A value shared across
// two or more of the SAME tool's own enum fields (a legitimate case of one
// tool reusing a token across semantically distinct fields) is not a
// collision; a value that happens to equal another tool's name and appears
// in only one field of one tool is.
func TestEnumToolNameCollision(t *testing.T) {
	tools := listTools(t)
	toolNames := make(map[string]bool, len(tools))
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}

	type fieldEnum struct {
		field  string
		values []string
	}

	for _, tool := range tools {
		schemaMap, ok := tool.InputSchema.(map[string]any)
		if !ok {
			continue
		}
		props, _ := schemaMap["properties"].(map[string]any)
		var fields []fieldEnum
		for propName, raw := range props {
			pschema, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if vals := schemaEnumStrings(pschema); len(vals) > 0 {
				fields = append(fields, fieldEnum{field: propName, values: vals})
			}
		}
		for _, fe := range fields {
			for _, v := range fe.values {
				if v == tool.Name || !toolNames[v] {
					continue
				}
				occurrences := 0
				for _, other := range fields {
					for _, ov := range other.values {
						if ov == v {
							occurrences++
						}
					}
				}
				if occurrences >= 2 {
					continue // legitimately shared across this tool's own fields
				}
				t.Errorf("%s.%s enum value %q collides with registered tool name %q", tool.Name, fe.field, v, v)
			}
		}
	}
}

// TestNoTautologicalDescriptions (#548) fails when a tool or parameter
// description is a verbatim (case-insensitive) restatement of its own name
// with underscores swapped for spaces — a description that names nothing
// beyond the identifier the caller already has.
func TestNoTautologicalDescriptions(t *testing.T) {
	normalize := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

	for _, tool := range listTools(t) {
		toolDesc := normalize(tool.Description)
		if toolDesc == normalize(tool.Name) || toolDesc == normalize(strings.ReplaceAll(tool.Name, "_", " ")) {
			t.Errorf("%s: description is just the tool's own name", tool.Name)
		}

		schemaMap, ok := tool.InputSchema.(map[string]any)
		if !ok {
			continue
		}
		props, _ := schemaMap["properties"].(map[string]any)
		for propName, raw := range props {
			pschema, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			desc, _ := pschema["description"].(string)
			if desc == "" {
				continue
			}
			d := normalize(desc)
			if d == normalize(propName) || d == normalize(strings.ReplaceAll(propName, "_", " ")) {
				t.Errorf("%s.%s: description %q is just a verbatim restatement of the field name", tool.Name, propName, desc)
			}
		}
	}
}

// extractToolError decodes the ToolError JSON block that structuredError
// appends after "msg\n\n" in an error result's text content.
func extractToolError(t *testing.T, res *mcp.CallToolResult) ToolError {
	t.Helper()
	if res == nil || !res.IsError {
		t.Fatal("expected an error result")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	idx := strings.Index(text, "{")
	if idx == -1 {
		t.Fatalf("error result has no embedded JSON: %q", text)
	}
	var wrapper struct {
		Error ToolError `json:"error"`
	}
	if err := json.Unmarshal([]byte(text[idx:]), &wrapper); err != nil {
		t.Fatalf("failed to parse embedded ToolError JSON: %v", err)
	}
	return wrapper.Error
}

// TestEnumErrorMessageParity (#548) asserts that a resolver's "unknown
// provider" error — specifically its ToolError.Alternatives list — is the
// same set of values as the corresponding tool field's schema Enum, so the
// error message a caller sees on rejection never drifts from what the
// schema itself now enforces up front.
//
// Scoped to the eight resolvers whose Alternatives list and enums.go helper
// both trace to the identical underlying slice (confirmed by direct read of
// each resolver): resolveFilingSearcher/filingProviderEnum,
// resolveCaseSearcher/caseProviderEnum, resolveEconSearcher/econProviderEnum,
// resolveTrialSearcher/trialProviderEnum, resolveLocalSearcher/localProviderEnum,
// resolveMonarchSearcher/monarchProviderEnum,
// resolveAwesomeListSearcher/awesomeListProviderEnum, and
// resolveCitationSearcher/citationProviderEnum (both hardcoded to
// ["semanticscholar","openalex"]).
//
// Deliberately excludes resolveProvider (web_search/image_search/news_search/
// search_and_scrape/monitor_query_save/monitor_query_check) and
// resolvePatentSearcher (patent_search): both build their Alternatives list
// from allSupportedProviders(), a cross-family union of every provider list
// (web + patent + academic + local + context) that is intentionally WIDER
// than either tool's own field-level Enum (webProviderEnum() /
// patentProviderEnum()) — a pre-existing, out-of-scope design property of the
// generic multi-domain resolver, not a drift this test should flag.
func TestEnumErrorMessageParity(t *testing.T) {
	deps := setupTestDeps()
	const unknownProvider = "definitely-not-a-real-provider-xyz123"

	cases := []struct {
		tool    string
		resolve func(Dependencies, string) *mcp.CallToolResult
		enum    []any
	}{
		{"filing_search", func(d Dependencies, p string) *mcp.CallToolResult { _, _, r := resolveFilingSearcher(d, p); return r }, filingProviderEnum()},
		{"legal_search", func(d Dependencies, p string) *mcp.CallToolResult { _, _, r := resolveCaseSearcher(d, p); return r }, caseProviderEnum()},
		{"econ_search", func(d Dependencies, p string) *mcp.CallToolResult { _, _, r := resolveEconSearcher(d, p); return r }, econProviderEnum()},
		{"clinical_search", func(d Dependencies, p string) *mcp.CallToolResult { _, _, r := resolveTrialSearcher(d, p); return r }, trialProviderEnum()},
		{"local_search", func(d Dependencies, p string) *mcp.CallToolResult { _, _, r := resolveLocalSearcher(d, p); return r }, localProviderEnum()},
		{"monarch_search", func(d Dependencies, p string) *mcp.CallToolResult { _, _, r := resolveMonarchSearcher(d, p); return r }, monarchProviderEnum()},
		{"awesome_list_search", func(d Dependencies, p string) *mcp.CallToolResult {
			_, _, r := resolveAwesomeListSearcher(d, p)
			return r
		}, awesomeListProviderEnum()},
		{"citation_graph", func(d Dependencies, p string) *mcp.CallToolResult { _, _, r := resolveCitationSearcher(d, p); return r }, citationProviderEnum()},
	}

	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			res := c.resolve(deps, unknownProvider)
			te := extractToolError(t, res)
			if len(te.Alternatives) == 0 {
				t.Fatalf("%s: unknown-provider error carries no Alternatives list", c.tool)
			}
			enumVals := toStringSlice(c.enum)
			if !sameStringSet(te.Alternatives, enumVals) {
				t.Errorf("%s: error Alternatives %v does not match the field's schema Enum %v", c.tool, te.Alternatives, enumVals)
			}
		})
	}
}
