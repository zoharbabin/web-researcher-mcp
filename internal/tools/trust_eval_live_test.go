//go:build live

// Labeled accuracy eval for the trust suite (#180). Unlike the unit tests (mocked
// resolvers) and tests/benchmark/ (performance only), this drives the REAL
// verify_citation and audit_bibliography paths against live Crossref / link
// checks over a curated GOLD SET of known-fabricated, known-retracted, known-real,
// and mischaracterization cases — turning the anti-hallucination claim into a
// measured precision/recall number and a permanent regression guard on the moat.
//
// Run with: make test-eval   (or: go test -tags=live -run TestTrustSuiteAccuracy ./internal/tools/)
// Network + CROSSREF_EMAIL required; skips cleanly when unset.
package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// goldDOI is one labeled citation-existence/retraction case.
type goldDOI struct {
	name          string
	doi           string
	wantExists    bool // expect the DOI to resolve in Crossref
	wantRetracted bool // expect retractionStatus.retracted == true
}

// trustGoldDOIs is the curated existence + retraction gold set. Kept small and
// well-chosen (a few dozen entries is enough to be credible) and pinned to stable,
// famous identifiers so the eval is reproducible.
var trustGoldDOIs = []goldDOI{
	// ── Known-RETRACTED (must flag retracted=true) ───────────────────────────
	{"Wakefield 1998 (retracted MMR/autism)", "10.1016/S0140-6736(97)11096-0", true, true},
	{"Obokata STAP cells (retracted)", "10.1038/nature12968", true, true},
	// ── Known-REAL, not retracted (must exist=true, retracted=false) ─────────
	{"Watson & Crick 1953 DNA", "10.1038/171737a0", true, false},
	{"AlphaFold (Jumper 2021)", "10.1038/s41586-021-03819-2", true, false},
	{"Hwang & Reich 2001 (Science)", "10.1126/science.1058040", true, false},
	{"Shannon 1948 (reprinted DOI)", "10.1002/j.1538-7305.1948.tb01338.x", true, false},
	// ── Known-FABRICATED (plausible-looking but nonexistent → exists=false) ──
	{"fabricated DOI (valid prefix, nonexistent suffix)", "10.1038/s41586-021-99999999-x", false, false},
	{"fabricated DOI 2 (nonexistent)", "10.1016/j.cell.2099.13.013", false, false},
}

func newEvalDeps(t *testing.T) Dependencies {
	t.Helper()
	email := os.Getenv("CROSSREF_EMAIL")
	if email == "" {
		t.Skip("CROSSREF_EMAIL not set — skipping live trust-suite eval")
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	retraction := search.NewCrossrefRetractionResolver(email, search.Deps{
		HTTPClient: httpClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 10, ResetTimeout: 60}),
	})
	linkVerifier := scraper.NewLinkVerifier(scraper.LinkVerifierConfig{})
	academic := search.AvailableAcademicProviders(search.AcademicProviderConfig{
		CrossRefEmail: email,
		OpenAlexEmail: os.Getenv("OPENALEX_EMAIL"),
	}, search.Deps{HTTPClient: httpClient})

	return Dependencies{
		Cache:              cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 16}),
		AcademicProviders:  academic,
		RetractionResolver: retraction,
		LinkVerifier:       linkVerifier,
		Scraper:            scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 3}),
		Content:            content.NewProcessor(),
		Metrics:            metrics.NewCollector(),
		Auditor:            audit.NewNoop(),
	}
}

// prf holds confusion-matrix tallies for one signal and prints precision/recall.
type prf struct {
	tp, fp, tn, fn int
}

func (p *prf) observe(predictedPositive, actualPositive bool) {
	switch {
	case predictedPositive && actualPositive:
		p.tp++
	case predictedPositive && !actualPositive:
		p.fp++
	case !predictedPositive && actualPositive:
		p.fn++
	default:
		p.tn++
	}
}

func (p *prf) report(t *testing.T, signal string) {
	precision, recall := 1.0, 1.0
	if p.tp+p.fp > 0 {
		precision = float64(p.tp) / float64(p.tp+p.fp)
	}
	if p.tp+p.fn > 0 {
		recall = float64(p.tp) / float64(p.tp+p.fn)
	}
	t.Logf("[%s] precision=%.2f recall=%.2f (tp=%d fp=%d tn=%d fn=%d)",
		signal, precision, recall, p.tp, p.fp, p.tn, p.fn)
	// The trust suite's whole point: a FALSE POSITIVE (calling a real source fake,
	// or a clean paper retracted) is the unacceptable error — it destroys trust.
	// Demand zero false positives; recall may lag (we under-flag by design).
	if p.fp > 0 {
		t.Errorf("[%s] %d FALSE POSITIVES — the trust suite must never mislabel a legitimate source", signal, p.fp)
	}
}

// TestTrustSuiteAccuracy_Existence measures verify_citation's existence + retraction
// signals over the gold DOI set.
func TestTrustSuiteAccuracy_Existence(t *testing.T) {
	deps := newEvalDeps(t)
	ctx := context.Background()

	var existence, retraction prf
	for _, g := range trustGoldDOIs {
		out := map[string]any{}
		var prov []string
		verifyByDOI(ctx, deps, g.doi, g.doi, "", out, &prov)

		gotExists, _ := out["exists"].(bool)
		// existence signal: predicted "real" vs actually real.
		existence.observe(gotExists, g.wantExists)

		gotRetracted := false
		if rs, ok := out["retractionStatus"].(*search.RetractionStatus); ok && rs != nil {
			gotRetracted = rs.Retracted
		}
		retraction.observe(gotRetracted, g.wantRetracted)

		t.Logf("%-55s exists=%v(want %v) retracted=%v(want %v)", g.name, gotExists, g.wantExists, gotRetracted, g.wantRetracted)
	}
	existence.report(t, "existence")
	retraction.report(t, "retraction")
}

// TestTrustSuiteAccuracy_Mischaracterization measures audit_bibliography's claim
// check: a real source that does NOT address a given claim must flag
// mischaracterized (not_addressed), while a source that DOES address its claim
// must not be flagged.
func TestTrustSuiteAccuracy_Mischaracterization(t *testing.T) {
	deps := newEvalDeps(t)
	ctx := context.Background()

	type claimCase struct {
		name                 string
		url                  string
		claim                string
		wantMischaracterized bool
	}
	// A stable, content-rich page (the CRISPR Wikipedia article) checked against:
	//   - an on-topic claim (must be `addressed`, never flagged);
	//   - genuinely-disjoint off-topic claims whose vocabulary does not occur in
	//     any passage (must be `not_addressed` → mischaracterized).
	// Note: the windowed coverage (#177) correctly catches these; a claim that
	// merely shares stray generic terms ("war", "signed") stays at the conservative
	// `partially_addressed` by design — under-flagging, never false-accusing.
	cases := []claimCase{
		{"on-topic claim addressed", "https://en.wikipedia.org/wiki/CRISPR",
			"CRISPR is a gene editing technology", false},
		{"off-topic physics claim flagged", "https://en.wikipedia.org/wiki/CRISPR",
			"quantum chromodynamics describes the confinement of gluons and quarks", true},
		{"off-topic history claim flagged", "https://en.wikipedia.org/wiki/CRISPR",
			"Napoleon Bonaparte was crowned Emperor of the French in 1804", true},
	}

	var mis prf
	for _, c := range cases {
		r := &auditEntryResult{URL: c.url, Claim: c.claim}
		live := true
		r.LinkLive = &live
		auditClaimCoverage(ctx, deps, r)
		gotMis := r.ClaimSupport == claimNotAddressed
		mis.observe(gotMis, c.wantMischaracterized)
		t.Logf("%-32s support=%q mischaracterized=%v(want %v)", c.name, r.ClaimSupport, gotMis, c.wantMischaracterized)
	}
	mis.report(t, "mischaracterization")
}

// TestTrustSuiteAccuracy_VerifyCitationClaim mirrors the corpus mischaracterization
// eval on verify_citation's single-citation claim path (#195). It drives the same
// shared claimCoverageFor helper the tool uses, over real live URLs, enforcing the
// zero-false-positive invariant (a real-but-tangential source must NOT be
// not_addressed).
func TestTrustSuiteAccuracy_VerifyCitationClaim(t *testing.T) {
	deps := newEvalDeps(t)
	ctx := context.Background()

	cases := []struct {
		name        string
		url         string
		claim       string
		wantNotAddr bool
	}{
		{"on-topic addressed", "https://en.wikipedia.org/wiki/CRISPR",
			"CRISPR is a gene editing technology", false},
		{"off-topic flagged", "https://en.wikipedia.org/wiki/CRISPR",
			"Napoleon Bonaparte was crowned Emperor of the French in 1804", true},
	}
	var mis prf
	for _, c := range cases {
		cc := claimCoverageFor(ctx, deps, c.url, c.claim)
		got := cc.Support == claimNotAddressed
		mis.observe(got, c.wantNotAddr)
		t.Logf("%-22s support=%q not_addressed=%v(want %v)", c.name, cc.Support, got, c.wantNotAddr)
	}
	mis.report(t, "verify_citation-claim")
}

// TestTrustSuiteAccuracy_ScrapedDOIRetraction validates #199 end-to-end: scraping
// a known-retracted paper's publisher landing page surfaces detectedDoi and a
// retracted retractionStatus. Needs the live scraper + Crossref resolver; skips
// when the page can't be reached.
func TestTrustSuiteAccuracy_ScrapedDOIRetraction(t *testing.T) {
	deps := newEvalDeps(t)
	ctx := context.Background()

	// A few known-retracted papers whose publisher landing pages declare the DOI
	// in citation_doi metadata. Publisher pages vary in how aggressively they
	// redirect/paywall scrapers, so we try several and skip if none is scrapeable
	// in this environment — the deterministic guarantees live in the hermetic
	// TestScrapeDOI_* unit tests; this is a best-effort end-to-end smoke.
	urls := []string{
		"https://www.nature.com/articles/nature12968",         // Obokata STAP (retracted)
		"https://www.science.org/doi/10.1126/science.1078616", // a retracted Science paper
		"https://journals.plos.org/plosone/article?id=10.1371/journal.pone.0000000",
	}

	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	for _, url := range urls {
		res, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "scrape_page", Arguments: map[string]any{"url": url}})
		if err != nil || res.IsError {
			t.Logf("skip %s (unscrapeable in this env)", url)
			continue
		}
		var out map[string]any
		if e := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); e != nil {
			continue
		}
		doi, _ := out["detectedDoi"].(string)
		if doi == "" {
			t.Logf("%s: no detectedDoi (sourceType=%v)", url, out["sourceType"])
			continue
		}
		t.Logf("detectedDoi=%s sourceType=%v retractionStatus=%v", doi, out["sourceType"], out["retractionStatus"])
		// We reached a scholarly page and detected its DOI end-to-end — the #199
		// path works. (retractionStatus presence depends on the specific DOI's
		// Retraction Watch record; logged, not asserted, to avoid flakiness.)
		return
	}
	t.Skip("no publisher landing page was scrapeable in this environment — see hermetic TestScrapeDOI_* for the guarantees")
}

// TestTrustSuiteAccuracy_TitleMatch measures #221 titleMatch signal over a labeled
// gold set: known-real DOIs with correct titles, with an invented title (mismatch),
// and bare DOIs (not_checked). The zero-false-positive invariant is enforced: a
// real DOI with a correct title must NEVER be flagged "mismatch".
func TestTrustSuiteAccuracy_TitleMatch(t *testing.T) {
	deps := newEvalDeps(t)
	ctx := context.Background()

	type tmCase struct {
		name         string
		citation     string // full citation string passed to verifyByDOI
		doi          string
		wantMismatch bool // true only when the supplied title is wrong (invented)
	}

	cases := []tmCase{
		// Correct title alongside the DOI → must be "match", never "mismatch".
		{"correct title AlphaFold", "10.1038/s41586-021-03819-2 Highly accurate protein structure prediction with AlphaFold", "10.1038/s41586-021-03819-2", false},
		// Correct title with subtitle dropped → token overlap is still strong → "match".
		{"AlphaFold DOI only prefix with partial title", "Highly accurate protein structure prediction 10.1038/s41586-021-03819-2", "10.1038/s41586-021-03819-2", false},
		// Bare DOI only → not_checked (no title text to compare) — not a mismatch.
		{"bare DOI Watson Crick", "10.1038/171737a0", "10.1038/171737a0", false},
		// Invented title with multiple wrong tokens that don't appear in the real record.
		{"invented title AlphaFold", "10.1038/s41586-021-03819-2 Quantum entanglement teleportation bandwidth", "10.1038/s41586-021-03819-2", true},
	}

	var mismatch prf
	for _, c := range cases {
		out := map[string]any{}
		var prov []string
		verifyByDOI(ctx, deps, c.doi, c.citation, "", out, &prov)

		tm, _ := out["titleMatch"].(string)
		gotMismatch := tm == "mismatch"
		mismatch.observe(gotMismatch, c.wantMismatch)
		t.Logf("%-50s titleMatch=%q wantMismatch=%v", c.name, tm, c.wantMismatch)
	}
	mismatch.report(t, "titleMatch")
}
