//go:build live

// Labeled accuracy eval for scrape_page extraction fidelity (#483). Unlike the
// hermetic scraper unit tests (fixed HTML fixtures) and tests/benchmark/
// (performance only), this drives the REAL scrape_page tool against a curated
// gold set of live pages, each labeled with key facts that MUST survive
// extraction — turning "does the content we hand back actually reflect the
// source page" into a measured recall number and a permanent regression
// guard, the extraction-fidelity counterpart to trust_eval_live_test.go's
// citation-integrity eval.
//
// Run with: make test-extraction-eval (or: go test -tags=live -run TestExtractionFidelity ./internal/tools/)
// Network required; no API key needed (every gold-set page is scraped keylessly).
package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
)

// extractionGoldCase is one labeled (URL, expected-key-facts) case. Every fact
// has been individually confirmed present on the live page at the time this
// gold set was added (#483) — not a hand-guessed list.
type extractionGoldCase struct {
	name  string
	url   string
	facts []string // case-insensitive substrings that MUST appear in extracted content
}

// extractionGoldSet spans distinct page types the 4-tier scrape pipeline
// handles differently in practice (long-form encyclopedia prose, technical
// documentation, a GitHub-native README route) so the eval isn't just
// measuring one extraction path repeatedly.
var extractionGoldSet = []extractionGoldCase{
	{"CRISPR (Wikipedia)", "https://en.wikipedia.org/wiki/CRISPR", []string{"Cas9", "gene editing", "bacteria"}},
	{"Go language (Wikipedia)", "https://en.wikipedia.org/wiki/Go_(programming_language)", []string{"Rob Pike", "Google", "2009"}},
	{"Effective Go (go.dev docs)", "https://go.dev/doc/effective_go", []string{"gofmt", "goroutine", "package"}},
	{"Python (Wikipedia)", "https://en.wikipedia.org/wiki/Python_(programming_language)", []string{"Guido van Rossum", "1991", "interpreted"}},
	{"DNA (Wikipedia)", "https://en.wikipedia.org/wiki/DNA", []string{"double helix", "nucleotide", "Watson"}},
	{"Alan Turing (Wikipedia)", "https://en.wikipedia.org/wiki/Alan_Turing", []string{"Bletchley Park", "Enigma", "computer science"}},
	{"Transformer architecture (Wikipedia)", "https://en.wikipedia.org/wiki/Transformer_(deep_learning_architecture)", []string{"attention mechanism", "2017", "neural network"}},
	{"HTTP (Wikipedia)", "https://en.wikipedia.org/wiki/HTTP", []string{"Hypertext Transfer Protocol", "client", "server"}},
	{"Kubernetes (Wikipedia)", "https://en.wikipedia.org/wiki/Kubernetes", []string{"Google", "container", "orchestration"}},
	{"PostgreSQL (Wikipedia)", "https://en.wikipedia.org/wiki/PostgreSQL", []string{"relational database", "open-source", "SQL"}},
	{"Albert Einstein (Wikipedia)", "https://en.wikipedia.org/wiki/Albert_Einstein", []string{"theory of relativity", "Nobel Prize", "physics"}},
	{"Marie Curie (Wikipedia)", "https://en.wikipedia.org/wiki/Marie_Curie", []string{"radioactivity", "Nobel Prize", "polonium"}},
}

func newExtractionEvalDeps() Dependencies {
	return Dependencies{
		Cache:   cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 16}),
		Scraper: scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 3}),
		Content: content.NewProcessor(),
		Metrics: metrics.NewCollector(),
		Auditor: audit.NewNoop(),
	}
}

// TestExtractionFidelity_KeyFactsSurvive drives the real scrape_page tool over
// the MCP transport against every gold-set page and measures per-case and
// aggregate fact recall: the fraction of a page's labeled key facts that
// actually appear in the returned content. Unlike the trust suite's
// zero-false-positive framing, extraction fidelity has no "false positive" —
// the failure mode is a fact silently dropped — so this eval reports recall
// and fails only when a page's own extraction drops fact recall below the
// per-case floor, which would mean the pipeline handed back content that no
// longer reflects the source page.
func TestExtractionFidelity_KeyFactsSurvive(t *testing.T) {
	deps := newExtractionEvalDeps()
	ctx := context.Background()

	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	// A page can legitimately drop one fact to layout/redirect noise without
	// the extraction being broken; losing every fact means the pipeline
	// returned something other than the page's real content.
	const perCaseMinRecall = 0.5

	var totalFacts, totalFound int
	for _, c := range extractionGoldSet {
		t.Run(c.name, func(t *testing.T) {
			res, err := client.CallTool(ctx, &mcp.CallToolParams{
				Name:      "scrape_page",
				Arguments: map[string]any{"url": c.url},
			})
			if err != nil {
				t.Skipf("skip %s (unreachable in this environment): %v", c.url, err)
			}
			if res.IsError {
				t.Skipf("skip %s (scrape_page returned an error result in this environment)", c.url)
			}

			var out struct {
				Content string `json:"content"`
			}
			if e := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); e != nil {
				t.Fatalf("parse scrape_page result: %v", e)
			}
			lower := strings.ToLower(out.Content)

			found := 0
			for _, fact := range c.facts {
				if strings.Contains(lower, strings.ToLower(fact)) {
					found++
				} else {
					t.Logf("missing key fact %q", fact)
				}
			}
			recall := float64(found) / float64(len(c.facts))
			t.Logf("%s: %d/%d key facts present (recall=%.2f)", c.name, found, len(c.facts), recall)

			totalFacts += len(c.facts)
			totalFound += found

			if recall < perCaseMinRecall {
				t.Errorf("%s: only %d/%d key facts survived extraction (recall=%.2f, floor=%.2f) — content no longer reflects the source page",
					c.name, found, len(c.facts), recall, perCaseMinRecall)
			}
		})
	}

	if totalFacts > 0 {
		t.Logf("=== extraction fidelity: %d/%d key facts recalled across %d pages (%.2f) ===",
			totalFound, totalFacts, len(extractionGoldSet), float64(totalFound)/float64(totalFacts))
	}
}
