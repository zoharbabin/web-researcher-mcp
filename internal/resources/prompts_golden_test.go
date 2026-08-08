package resources

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

// updateGolden regenerates every golden file in TestPromptGolden instead of
// diffing against it. Usage: go test ./internal/resources/... -run
// TestPromptGolden -update
var updateGolden = flag.Bool("update", false, "update golden files")

// promptGoldenCases pairs each of the 10 registered prompts with a fixed,
// representative argument set. Arguments are reused verbatim from the
// canonical GetPrompt calls already exercised in resources_test.go, so the
// golden output stays consistent with the behavioral assertions there.
var promptGoldenCases = []struct {
	name string
	args map[string]string
}{
	// TestComprehensiveResearchPrompt
	{"comprehensive-research", map[string]string{"topic": "quantum computing", "depth": "deep"}},
	// TestFactCheckPrompt
	{"fact-check", map[string]string{"claim": "The earth is flat", "context": "social media debate"}},
	// TestCompetitiveAnalysisPrompt
	{"competitive-analysis", map[string]string{"company": "Acme Corp", "market": "cloud computing"}},
	// TestLiteratureReviewPrompt
	{"literature-review", map[string]string{"topic": "CRISPR gene editing", "year_from": "2020", "year_to": "2025"}},
	// TestBrandGuidelinesPromptDefaultUseCase
	{"brand-guidelines", map[string]string{"company": "Kaltura"}},
	// TestCompanyReconPromptDefaultDepth
	{"company-recon", map[string]string{"target": "Acme Corp acme.com"}},
	// No existing resources_test.go coverage for curriculum-research; "Marx" is
	// docs/TOOLS.md's own example subject.
	{"curriculum-research", map[string]string{"subject": "Marx"}},
	// TestRareDiseaseResearchPrompt
	{"rare-disease-research", map[string]string{"topic": "Marfan syndrome"}},
	// TestResearchPanelFactcheckPrompt
	{"research-panel-factcheck", map[string]string{"claim": "vaccines cause autism"}},
	// TestResearchPanelSynthesisPrompt
	{"research-panel-synthesis", map[string]string{"question": "what causes inflation"}},
}

// TestPromptGolden renders each of the 10 registered prompts with a fixed
// argument set and diffs the result against a committed golden file under
// testdata/prompts/<name>.golden. A wording edit to a prompt template (e.g.
// accidentally dropping a tool-name reference or an instruction) now shows up
// as a reviewable diff instead of shipping silently.
//
// Regenerate after an intentional prompt-wording change:
//
//	go test ./internal/resources/... -run TestPromptGolden -update
func TestPromptGolden(t *testing.T) {
	for _, tc := range promptGoldenCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			m := metrics.NewCollector()
			s, _ := session.NewManager(session.Config{MaxSessions: 10})
			srv := createTestServer(m, s)
			cs := connectTestClient(ctx, t, srv)
			defer cs.Close()

			result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
				Name:      tc.name,
				Arguments: tc.args,
			})
			if err != nil {
				t.Fatalf("GetPrompt(%q) failed: %v", tc.name, err)
			}
			if len(result.Messages) == 0 {
				t.Fatalf("GetPrompt(%q) returned no messages", tc.name)
			}
			tcontent, ok := result.Messages[0].Content.(*mcp.TextContent)
			if !ok {
				t.Fatalf("GetPrompt(%q) message content is not TextContent", tc.name)
			}
			got := "Description: " + result.Description + "\n---\n" + tcontent.Text

			goldenPath := filepath.Join("testdata", "prompts", tc.name+".golden")
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
					t.Fatalf("mkdir testdata/prompts: %v", err)
				}
				if err := os.WriteFile(goldenPath, []byte(got), 0o600); err != nil {
					t.Fatalf("write golden file %s: %v", goldenPath, err)
				}
				return
			}

			want, err := os.ReadFile(goldenPath) // #nosec G304 -- fixed in-repo testdata path, not user input
			if err != nil {
				t.Fatalf("read golden file %s (run with -update to create it): %v", goldenPath, err)
			}
			if got != string(want) {
				t.Fatalf("rendered prompt %q does not match golden file %s.\nRun with -update to regenerate if this change is intentional.\n\n--- got ---\n%s\n--- want ---\n%s", tc.name, goldenPath, got, string(want))
			}
		})
	}
}
