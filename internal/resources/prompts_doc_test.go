package resources

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

// listPrompts connects an in-memory client/server pair and returns every
// prompt the server advertises via prompts/list — the runtime source of
// truth for TestPromptsDocMatchesRegistry.
func listPrompts(t *testing.T) []*mcp.Prompt {
	t.Helper()
	ctx := context.Background()
	m := metrics.NewCollector()
	s, _ := session.NewManager(session.Config{MaxSessions: 10})
	srv := createTestServer(m, s)
	cs := connectTestClient(ctx, t, srv)
	defer cs.Close()

	result, err := cs.ListPrompts(ctx, &mcp.ListPromptsParams{})
	if err != nil {
		t.Fatalf("ListPrompts failed: %v", err)
	}
	return result.Prompts
}

// repoRoot resolves the repository root from this test file's location,
// independent of the working directory the test is invoked from. Mirrors
// internal/tools/metadata_test.go's helper of the same name.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// this file is internal/resources/prompts_doc_test.go -> up two dirs to repo root.
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

// TestPromptsDocMatchesRegistry is the doc-drift guard for MCP Prompts: it
// fails CI if docs/TOOLS.md's "## MCP Prompts" section documents a different
// set of prompts than the server actually registers at runtime. Mirrors
// internal/tools/metadata_test.go's TestToolsDocMatchesRegistry pattern.
//
// It parses "### `name`" headers from the "## MCP Prompts" section and
// compares that set to the live ListPrompts result — the runtime is the
// source of truth, in both directions (undocumented registered prompt, or
// documented-but-unregistered stale entry).
func TestPromptsDocMatchesRegistry(t *testing.T) {
	docPath := filepath.Join(repoRoot(t), "docs", "TOOLS.md")
	data, err := os.ReadFile(docPath) // #nosec G304 -- fixed in-repo doc path, not user input
	if err != nil {
		t.Fatalf("read TOOLS.md: %v", err)
	}

	const marker = "## MCP Prompts"
	content := string(data)
	start := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(marker) + `\s*$`).FindStringIndex(content)
	if start == nil {
		t.Fatal("docs/TOOLS.md missing '## MCP Prompts' section")
	}
	section := content[start[1]:]
	if end := regexp.MustCompile(`(?m)^## `).FindStringIndex(section); end != nil {
		section = section[:end[0]]
	}

	// Match headers like:  ### `comprehensive-research`
	re := regexp.MustCompile("(?m)^###\\s+`([a-z0-9-]+)`")
	matches := re.FindAllStringSubmatch(section, -1)
	documented := make(map[string]bool, len(matches))
	for _, m := range matches {
		documented[m[1]] = true
	}
	if len(documented) == 0 {
		t.Fatal("no prompt headers found in docs/TOOLS.md's '## MCP Prompts' section — expected '### `name`' format")
	}

	registered := make(map[string]bool)
	for _, p := range listPrompts(t) {
		registered[p.Name] = true
	}

	for name := range registered {
		if !documented[name] {
			t.Errorf("prompt %q is registered but NOT documented in docs/TOOLS.md's '## MCP Prompts' section", name)
		}
	}
	for name := range documented {
		if !registered[name] {
			t.Errorf("docs/TOOLS.md documents prompt %q that is NOT registered (stale doc)", name)
		}
	}
}
