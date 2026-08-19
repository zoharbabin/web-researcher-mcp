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
	category      string // gold-set category, for per-category precision/recall (#481)
	doi           string
	wantExists    bool // expect the DOI to resolve in Crossref
	wantRetracted bool // expect retractionStatus.retracted == true
}

// Gold-set categories (#481). "canonical" is the original famous/easy set;
// the rest were added to cover harder, more realistic cases: recent
// retractions, non-English/regional publishers, preprint-then-retracted
// chains, structurally unusual (but real) DOIs, and a larger fabricated set
// spanning multiple real publisher prefixes.
const (
	catCanonical     = "canonical"
	catRecentRetract = "recent_retraction"
	catRegionalPub   = "regional_publisher"
	catPreprintChain = "preprint_retraction_chain"
	catMalformedEdge = "malformed_edge_case"
	catFabricated    = "fabricated"
)

// trustGoldDOIs is the curated existence + retraction gold set, categorized so
// TestTrustSuiteAccuracy_Existence can report precision/recall per category as
// well as in aggregate. Every entry has been individually verified against the
// live doi.org handle registry / Crossref / Retraction Watch at the time it was
// added (see #481) — this is not a hand-guessed list.
var trustGoldDOIs = []goldDOI{
	// ── canonical: famous, stable identifiers (original set) ────────────────
	{"Wakefield 1998 (retracted MMR/autism)", catCanonical, "10.1016/S0140-6736(97)11096-0", true, true},
	{"Obokata STAP cells (retracted)", catCanonical, "10.1038/nature12968", true, true},
	{"Watson & Crick 1953 DNA", catCanonical, "10.1038/171737a0", true, false},
	{"AlphaFold (Jumper 2021)", catCanonical, "10.1038/s41586-021-03819-2", true, false},
	{"Hwang & Reich 2001 (Science)", catCanonical, "10.1126/science.1058040", true, false},
	{"Shannon 1948 (reprinted DOI)", catCanonical, "10.1002/j.1538-7305.1948.tb01338.x", true, false},
	// arXiv DOI: registered through DataCite, 404 in Crossref, and NOT carried under
	// this DOI by OpenAlex (which re-mints its own) — must still resolve exists=true
	// via the authoritative doi.org handle registry (#226).
	{"Transformer/arXiv DOI (DataCite-registered, Crossref-absent)", catCanonical, "10.48550/arXiv.1706.03762", true, false},

	// ── recent_retraction: retracted within roughly the last 12-18 months ───
	{"Arsenic-life bacterium (Wolfe-Simon 2010, Science; retracted 2025)", catRecentRetract, "10.1126/science.1197258", true, true},
	{"Duckworth et al. 2011 test-motivation/IQ (PNAS; retracted 2025)", catRecentRetract, "10.1073/pnas.1018601108", true, true},
	{"Luo et al. 2022 fetal cerebellar atlas (Nature; retracted 2025)", catRecentRetract, "10.1038/s41586-022-05487-2", true, true},
	{"Kotz/Levermann/Wenz 2024 climate econ commitment (Nature; retracted 2025)", catRecentRetract, "10.1038/s41586-024-07219-0", true, true},
	{"Liang et al. 2024 CRISPR RNA-targeting screens (Cell; retracted 2025)", catRecentRetract, "10.1016/j.cell.2024.10.021", true, true},

	// ── regional_publisher: real, non-retracted, non-US/UK-publisher DOIs ───
	{"Lim et al. 2020 COVID-19 index patient (J Korean Med Sci)", catRegionalPub, "10.3346/jkms.2020.35.e79", true, false},
	{"Kutsumi et al. 2012 puppy training (J Vet Med Sci, Japan)", catRegionalPub, "10.1292/jvms.12-0008", true, false},
	{"China NHC 2022 diabetes primary-care guideline", catRegionalPub, "10.3760/cma.j.cn112138-20220120-000063", true, false},
	{"Croda et al. 2020 COVID-19 in Brazil (Rev Soc Bras Med Trop, SciELO)", catRegionalPub, "10.1590/0037-8682-0167-2020", true, false},
	{"Pe'er 2016 eyelid tumor pathology (Indian J Ophthalmology)", catRegionalPub, "10.4103/0301-4738.181752", true, false},

	// ── preprint_retraction_chain: preprint + its published version, both
	// withdrawn/retracted, or a preprint formally withdrawn on its own ──────
	{"Elgazzar et al. ivermectin COVID-19 preprint (Research Square, withdrawn)", catPreprintChain, "10.21203/rs.3.rs-100956/v1", true, true},
	{"Pradhan et al. spike-protein/HIV-1 preprint (bioRxiv, self-withdrawn 2020)", catPreprintChain, "10.1101/2020.01.30.927871", true, true},
	{"Vaghefi et al. additive-manufacturing RL — published version (retracted 2024)", catPreprintChain, "10.1016/j.addma.2024.104121", true, true},
	{"Aneh/Ngwasiri et al. Dacryodes extraction — published version (Heliyon, retracted 2026)", catPreprintChain, "10.1016/j.heliyon.2023.e16443", true, true},

	// ── malformed_edge_case: real DOIs with structurally unusual suffixes
	// (non-Crossref registrars, dotted sub-component suffixes) — tests
	// resolver robustness, not typo-tolerance ────────────────────────────────
	{"Zenodo software release (DataCite registrar)", catMalformedEdge, "10.5281/zenodo.1212303", true, false},
	{"Figshare teaching dataset (DataCite registrar)", catMalformedEdge, "10.6084/m9.figshare.1314459", true, false},
	{"PANGAEA dataset (non-publisher registrant prefix)", catMalformedEdge, "10.1594/PANGAEA.873570", true, false},
	{"Dryad dataset (short alphanumeric suffix)", catMalformedEdge, "10.5061/dryad.052q5", true, false},
	{"PLOS supplementary file (dotted sub-component suffix)", catMalformedEdge, "10.1371/journal.pone.0184843.s001", true, false},

	// ── fabricated: plausible-looking but nonexistent, spanning multiple
	// real publisher prefixes (must all resolve exists=false) ──────────────
	{"fabricated (Nature prefix)", catFabricated, "10.1038/s41586-021-99999999-x", false, false},
	{"fabricated (Cell/Elsevier prefix)", catFabricated, "10.1016/j.cell.2099.13.013", false, false},
	{"fabricated (Nature prefix 2)", catFabricated, "10.1038/s41586-023-88888-1", false, false},
	{"fabricated (Wiley Angewandte Chemie prefix)", catFabricated, "10.1002/anie.202299999", false, false},
	{"fabricated (Springer neurology journal prefix)", catFabricated, "10.1007/s00415-022-77777-x", false, false},
	{"fabricated (Science/AAAS prefix)", catFabricated, "10.1126/science.abz9999", false, false},
	{"fabricated (Elsevier Cell prefix 2)", catFabricated, "10.1016/j.cell.2023.09.999", false, false},
	{"fabricated (PNAS prefix)", catFabricated, "10.1073/pnas.2299999120", false, false},
	{"fabricated (PLOS Biology prefix)", catFabricated, "10.1371/journal.pbio.4009999", false, false},
	{"fabricated (Wiley Advanced Materials prefix)", catFabricated, "10.1002/adma.202399999", false, false},
	{"fabricated (Nature Communications prefix)", catFabricated, "10.1038/s41467-024-99999-9", false, false},
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
	doiRegistry := search.NewHandleDOIRegistry(search.Deps{
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
		DOIRegistry:        doiRegistry,
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
	p.reportBudgeted(t, signal, 0)
}

// reportBudgeted is report with an explicit false-positive tolerance (#482).
// The trust suite's whole point is that a FALSE POSITIVE (calling a real
// source fake, or a clean paper retracted) is the unacceptable error — it
// destroys trust — so maxFP must stay 0 for every category except the small
// set of genuinely ambiguous ones calibrated in reportRetraction below.
// Recall may lag (we under-flag by design) and is never gated here.
func (p *prf) reportBudgeted(t *testing.T, signal string, maxFP int) {
	precision, recall := 1.0, 1.0
	if p.tp+p.fp > 0 {
		precision = float64(p.tp) / float64(p.tp+p.fp)
	}
	if p.tp+p.fn > 0 {
		recall = float64(p.tp) / float64(p.tp+p.fn)
	}
	t.Logf("[%s] precision=%.2f recall=%.2f (tp=%d fp=%d tn=%d fn=%d)",
		signal, precision, recall, p.tp, p.fp, p.tn, p.fn)
	if p.fp > maxFP {
		t.Errorf("[%s] %d FALSE POSITIVES (budget %d) — the trust suite must never mislabel a legitimate source beyond its calibrated tolerance", signal, p.fp, maxFP)
	} else if p.fp > 0 {
		t.Logf("[%s] %d false positive(s) within calibrated budget (%d) — see #482", signal, p.fp, maxFP)
	}
}

// recentRetractionFPBudget (#482) is the only nonzero false-positive
// tolerance in the trust eval. It applies solely to the recent_retraction
// category's retraction signal: a retraction registered in the last
// 12-18 months may genuinely lag Crossref/Retraction Watch indexing on the
// day this test runs, which is a real-world timing gap, not a resolver bug.
// Every other category (including recent_retraction's own existence signal)
// stays at strict zero-tolerance.
const recentRetractionFPBudget = 1

// TestTrustSuiteAccuracy_Existence measures verify_citation's existence + retraction
// signals over the gold DOI set, in aggregate and per category (#481) — the
// aggregate number alone can hide a category (e.g. regional publishers) that
// is doing much worse than the "canonical famous cases" average suggests.
func TestTrustSuiteAccuracy_Existence(t *testing.T) {
	deps := newEvalDeps(t)
	ctx := context.Background()

	var existence, retraction prf
	byCategory := map[string]*struct{ existence, retraction prf }{}
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

		cat := byCategory[g.category]
		if cat == nil {
			cat = &struct{ existence, retraction prf }{}
			byCategory[g.category] = cat
		}
		cat.existence.observe(gotExists, g.wantExists)
		cat.retraction.observe(gotRetracted, g.wantRetracted)

		t.Logf("[%s] %-55s exists=%v(want %v) retracted=%v(want %v)", g.category, g.name, gotExists, g.wantExists, gotRetracted, g.wantRetracted)
	}

	for _, cat := range []string{catCanonical, catRecentRetract, catRegionalPub, catPreprintChain, catMalformedEdge, catFabricated} {
		c, ok := byCategory[cat]
		if !ok {
			continue
		}
		c.existence.report(t, "existence:"+cat)
		// recent_retraction (#482) is the sole category with a nonzero budget:
		// a retraction registered in the last 12-18 months can lag indexing on
		// the day this runs, so the retraction signal alone gets calibrated
		// slack here. Every other category, and this category's own existence
		// signal, stays at report()'s strict zero-tolerance.
		if cat == catRecentRetract {
			c.retraction.reportBudgeted(t, "retraction:"+cat, recentRetractionFPBudget)
		} else {
			c.retraction.report(t, "retraction:"+cat)
		}
	}
	existence.report(t, "existence:aggregate")
	retraction.report(t, "retraction:aggregate")
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

// TestTrustSuiteAccuracy_UncheckedClassification validates #225 live: an entry with
// neither a DOI nor a URL — only a title — can at most be matched by a fuzzy
// title search, which is NOT authoritative existence. Such an entry must classify
// `unchecked` (absence of evidence), NEVER `ok` (falsely verified) and NEVER
// `not_found` (a title-search miss is not a Crossref absence). The zero-false-
// positive invariant here is: no uncheckable citation is ever presented as verified.
func TestTrustSuiteAccuracy_UncheckedClassification(t *testing.T) {
	deps := newEvalDeps(t)
	ctx := context.Background()

	// Title-only entries with no resolvable identifier. The gibberish title cannot
	// match any real work; the plausible-but-nonexistent title may coincidentally
	// fuzzy-match a near-neighbor — either way, with no DOI/URL it must read as
	// unchecked, not verified.
	items := []auditItem{
		{entry: content.BibEntry{Title: "Zxqvb Wgklm Frnst Plurdnik Theory Of Nonexistent Quasiparticles"}},
		{entry: content.BibEntry{Title: "A Comprehensive Survey Of Imaginary Citation Fabrication Patterns 2099"}},
	}
	results := auditEntries(ctx, deps, items)
	if len(results) != len(items) {
		t.Fatalf("expected %d results, got %d", len(items), len(results))
	}
	for _, r := range results {
		hasUnchecked, hasNotFound := false, false
		for _, f := range r.Flags {
			switch f {
			case auditFlagUnchecked:
				hasUnchecked = true
			case auditFlagNotFound:
				hasNotFound = true
			}
		}
		t.Logf("%-60s exists=%v flags=%v", r.Title, r.Exists, r.Flags)
		// FALSE POSITIVE 1: an uncheckable entry presented as verified (clean/ok).
		if r.clean() {
			t.Errorf("[unchecked] FALSE POSITIVE: title-only entry %q classified ok (verified) — it has no authoritative identifier", r.Title)
		}
		// FALSE POSITIVE 2: a title-search miss mislabeled as a fabricated DOI.
		if hasNotFound {
			t.Errorf("[unchecked] FALSE POSITIVE: title-only entry %q flagged not_found — a title miss is absence of evidence, not a Crossref absence", r.Title)
		}
		if !hasUnchecked {
			t.Errorf("[unchecked] title-only entry %q should be flagged unchecked, got %v", r.Title, r.Flags)
		}
	}
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

// TestTrustSuiteAccuracy_ClinicalSearchPhase is the regression test for #437
// fix 3: a free-text query carrying an implied phase (the shape an LLM caller
// that hasn't split it into structured fields would produce) must surface
// majority phase-matching results against the live ClinicalTrials.gov v2 API
// — proving the aggFilters=phase:N fix (verified against the live API,
// 2026-08-18) actually changes result composition, not just that the request
// doesn't error.
func TestTrustSuiteAccuracy_ClinicalSearchPhase(t *testing.T) {
	httpClient := &http.Client{Timeout: 30 * time.Second}
	breakerCfg := circuit.Config{FailureThreshold: 10, ResetTimeout: 60}
	trial := search.NewClinicalTrialsProvider(search.Deps{HTTPClient: httpClient, Breaker: circuit.New(breakerCfg)})
	deps := Dependencies{
		Cache:          cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 16}),
		Metrics:        metrics.NewCollector(),
		Auditor:        audit.NewNoop(),
		TrialProviders: map[string]search.TrialProvider{trial.Name(): trial},
	}

	// The exact repro from #437: before the fix, 1 of 5 results plausibly
	// matched phase 3; the rest were off-phase or off-condition.
	out, res := callTool(t, deps, "clinical_search", map[string]any{
		"query":       "type 2 diabetes phase 3 trial",
		"num_results": 10,
	})
	if res.IsError {
		t.Fatalf("clinical_search returned a tool error: %v", res.Content)
	}
	trials, _ := out["trials"].([]any)
	if len(trials) == 0 {
		t.Fatal("expected at least one trial result")
	}

	matching := 0
	for _, tr := range trials {
		m, _ := tr.(map[string]any)
		phases, _ := m["phases"].([]any)
		for _, p := range phases {
			if p == "PHASE3" {
				matching++
				break
			}
		}
		t.Logf("nctId=%v phases=%v conditions=%v", m["nctId"], m["phases"], m["conditions"])
	}
	if matching*2 < len(trials) { // majority (>=50%) phase-3 matching
		t.Errorf("phase-3-matching = %d/%d, want majority (>=50%%)", matching, len(trials))
	}
}

// TestTrustSuiteAccuracy_CompanyRecon is the live regression case for #438:
// company_recon's ct_logs (crt.sh) and archives (Wayback CDX) phases against
// known real, high-traffic domains must return real signal — not a silent
// zero-result that (before this fix) was indistinguishable from a swallowed
// upstream error. Named with the TestTrustSuiteAccuracy prefix so `make
// test-eval`'s `-run TestTrustSuiteAccuracy` picks it up in the weekly
// trust-eval CI job (.github/workflows/ci.yml) alongside the rest of the
// suite, with no workflow change needed. Its own providers (crt.sh, Wayback
// CDX) are keyless and need no newEvalDeps, but it still gates on
// CROSSREF_EMAIL like every other case in this file — otherwise `make
// test-eval` would make live network calls from this one test even in an
// environment that unset CROSSREF_EMAIL specifically to skip the whole suite.
func TestTrustSuiteAccuracy_CompanyRecon(t *testing.T) {
	if os.Getenv("CROSSREF_EMAIL") == "" {
		t.Skip("CROSSREF_EMAIL not set — skipping live trust-suite eval")
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	breakerCfg := circuit.Config{FailureThreshold: 10, ResetTimeout: 60}
	deps := Dependencies{
		Cache:           cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 16}),
		Metrics:         metrics.NewCollector(),
		Auditor:         audit.NewNoop(),
		CTLogResolver:   search.NewCrtShResolver(search.Deps{HTTPClient: httpClient, Breaker: circuit.New(breakerCfg)}),
		ArchiveResolver: search.NewWaybackCDXResolver(search.Deps{HTTPClient: httpClient, Breaker: circuit.New(breakerCfg)}),
	}

	// Long-established, high-traffic domains with a large public certificate
	// and web-archive history — a near-zero result here reliably indicates a
	// resolver/parsing regression, not the domain genuinely lacking history.
	targets := []string{"stripe.com", "github.com"}
	const minSubdomains = 2
	const minArchiveEntries = 5

	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			out, res := callTool(t, deps, "company_recon", map[string]any{
				"target": target,
				"phases": []any{"ct_logs", "archives"},
			})
			if res.IsError {
				t.Fatalf("company_recon(%s) returned a tool error: %v", target, res.Content)
			}
			if phaseErrors, ok := out["phase_errors"].([]any); ok && len(phaseErrors) > 0 {
				t.Errorf("company_recon(%s) phase_errors: %v", target, phaseErrors)
			}
			subdomains, _ := out["subdomains"].([]any)
			archiveURLs, _ := out["archive_urls"].([]any)
			t.Logf("company_recon(%s): %d subdomains, %d archive entries", target, len(subdomains), len(archiveURLs))
			if len(subdomains) < minSubdomains {
				t.Errorf("company_recon(%s) subdomains = %d, want >= %d", target, len(subdomains), minSubdomains)
			}
			if len(archiveURLs) < minArchiveEntries {
				t.Errorf("company_recon(%s) archive_urls = %d, want >= %d", target, len(archiveURLs), minArchiveEntries)
			}
		})
	}
}
