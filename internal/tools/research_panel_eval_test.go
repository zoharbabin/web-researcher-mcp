//go:build live

// Labeled accuracy eval for research_panel (#302). Unlike the unit tests
// (mockModelProvider, canned text), this drives the REAL auto-detected panel —
// whatever LLM credentials are configured — end-to-end through the actual
// MCP tool, over a small gold set of questions with known expected divergence
// shape: an uncontroversial factual question should yield high confidence with
// no contradictions, while a genuinely contested/opinion question should
// surface at least one contradiction or drop confidence below "high".
//
// Run with: go test -tags=live -run TestResearchPanelEvalAccuracy ./internal/tools/
// Needs at least 2 configured LLM credentials (OPENROUTER_API_KEY,
// OPENAI_API_KEY, ANTHROPIC_API_KEY, or GOOGLE_AI_API_KEY); skips cleanly when
// fewer than 2 resolve, since divergence analysis needs at least 2 panel
// members to compare.
package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/config"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
)

// newResearchPanelEvalDeps builds Dependencies with the real auto-detected
// panel (internal/tools/research_panel_providers.go), from whatever LLM
// credentials are actually present in the environment. Skips cleanly when
// fewer than 2 members resolve — AnalyzeDivergence needs at least 2 responses
// to say anything about consensus or contradiction.
func newResearchPanelEvalDeps(t *testing.T) Dependencies {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	panel := AvailableModelProviders(cfg.ResearchPanel, cfg.AllowPrivateIPs)
	if len(panel) < 2 {
		t.Skip("fewer than 2 research_panel model providers configured — skipping live eval")
	}

	return Dependencies{
		Cache:                  cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 16}),
		Content:                content.NewProcessor(),
		Metrics:                metrics.NewCollector(),
		Auditor:                audit.NewNoop(),
		ResearchPanelProviders: panel,
	}
}

// researchPanelGoldCase is one labeled research_panel question with the
// expected divergence shape.
type researchPanelGoldCase struct {
	name             string
	question         string
	wantContradicted bool // true: expect >=1 contradiction OR confidence below "high"
}

// researchPanelGoldSet is intentionally small: a settled factual question
// (models should converge, high confidence, no contradictions) and a
// genuinely contested/opinion question (models are expected to diverge).
var researchPanelGoldSet = []researchPanelGoldCase{
	{
		name:             "settled fact — capital of France",
		question:         "What is the capital of France? Answer in one short sentence.",
		wantContradicted: false,
	},
	{
		name:             "contested opinion — best programming language",
		question:         "What is the single best programming language for all purposes, and why? Answer in one short sentence, taking a definitive position.",
		wantContradicted: true,
	},
}

// TestResearchPanelEvalAccuracy drives the real research_panel tool end-to-end
// over the gold set and asserts on the resulting divergence shape. Contested
// questions are logged rather than hard-failed on mismatch — model behavior on
// opinion questions is not perfectly predictable — but the settled-fact case
// enforces the zero-false-contradiction invariant: a question with one
// unambiguous correct answer must not be reported as a contradiction.
func TestResearchPanelEvalAccuracy(t *testing.T) {
	deps := newResearchPanelEvalDeps(t)
	ctx := context.Background()

	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	for _, g := range researchPanelGoldSet {
		res, err := client.CallTool(ctx, &mcp.CallToolParams{
			Name:      "research_panel",
			Arguments: map[string]any{"query": g.question, "use_cache": false},
		})
		if err != nil {
			t.Fatalf("%s: CallTool error: %v", g.name, err)
		}
		if res.IsError {
			t.Fatalf("%s: tool returned error: %v", g.name, res.Content)
		}

		var out map[string]any
		if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
			t.Fatalf("%s: unmarshal: %v", g.name, err)
		}

		meta, _ := out["_meta"].(map[string]any)
		succeeded, _ := meta["models_succeeded"].(float64)
		if succeeded < 2 {
			// Fewer than 2 members answered — panelConfidence reports "low" purely
			// because there's nothing to compare, not because of disagreement.
			// That's a panel-availability signal, not a divergence-accuracy one.
			t.Logf("%s: skip — only %.0f panel member(s) succeeded this run (meta=%v)", g.name, succeeded, meta)
			continue
		}

		divergence, _ := out["divergence"].(map[string]any)
		contradictions, _ := divergence["contradictions"].([]any)
		confidence, _ := divergence["confidence"].(string)
		got := len(contradictions) > 0 || confidence != "high"

		t.Logf("%-45s confidence=%s contradictions=%d (want_contradicted=%v got=%v)",
			g.name, confidence, len(contradictions), g.wantContradicted, got)

		if !g.wantContradicted && got {
			t.Errorf("%s: FALSE POSITIVE — settled-fact question flagged as contradicted/uncertain (confidence=%s, contradictions=%d)",
				g.name, confidence, len(contradictions))
		}
	}
}
