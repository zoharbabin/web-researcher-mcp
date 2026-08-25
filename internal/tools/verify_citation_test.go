package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

func callVerify(t *testing.T, deps Dependencies, citation string) map[string]any {
	t.Helper()
	ctx := context.Background()
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "verify_citation",
		Arguments: map[string]any{"citation": citation},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return out
}

// callVerifyClaim drives verify_citation with an optional claim through the
// in-memory MCP client (end-to-end: tool + schema), returning the parsed result.
func callVerifyClaim(t *testing.T, deps Dependencies, citation, claim string) map[string]any {
	t.Helper()
	ctx := context.Background()
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()
	args := map[string]any{"citation": citation}
	if claim != "" {
		args["claim"] = claim
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "verify_citation", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return out
}

// verifyClaimDeps returns deps whose scraper + link verifier can reach httptest
// servers (private IPs allowed) — the default setupTestDeps() scraper cannot.
func verifyClaimDeps(t *testing.T) Dependencies {
	t.Helper()
	deps := setupTestDeps()
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	deps.LinkVerifier = scraper.NewLinkVerifier(scraper.LinkVerifierConfig{AllowPrivateIPs: true})
	return deps
}

// TestVerifyCitation_ClaimAddressed: a URL whose page addresses the claim → addressed + evidence.
func TestVerifyCitation_ClaimAddressed(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><article><p>The randomized trial showed that the vaccine significantly reduced infection rates. Efficacy was 95% in the treatment group.</p></article></body></html>`))
	}))
	defer page.Close()

	out := callVerifyClaim(t, verifyClaimDeps(t), page.URL, "vaccine efficacy reduced infection")
	if out["claimSupport"] != "addressed" {
		t.Errorf("claimSupport = %v, want addressed", out["claimSupport"])
	}
	if ev, _ := out["claimEvidence"].([]any); len(ev) == 0 {
		t.Error("expected claimEvidence when addressed")
	}
	if out["claimSourceUrl"] != page.URL {
		t.Errorf("claimSourceUrl = %v, want %s", out["claimSourceUrl"], page.URL)
	}
	if out["claim"] != "vaccine efficacy reduced infection" {
		t.Errorf("claim not echoed: %v", out["claim"])
	}
}

// TestVerifyCitation_ClaimNotAddressed: a real, live page about something else → not_addressed (mischaracterization signal).
func TestVerifyCitation_ClaimNotAddressed(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><article><p>This article discusses medieval architecture and the construction of cathedrals in twelfth-century France.</p></article></body></html>`))
	}))
	defer page.Close()

	out := callVerifyClaim(t, verifyClaimDeps(t), page.URL, "quantum entanglement teleportation bandwidth")
	if out["claimSupport"] != "not_addressed" {
		t.Errorf("claimSupport = %v, want not_addressed", out["claimSupport"])
	}
}

// TestVerifyCitation_ClaimContrastSignal: a page that shares the claim's terms while negating it → contrastSignal heads-up.
func TestVerifyCitation_ClaimContrastSignal(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><article><p>The randomized trial found that the vaccine did not significantly reduce infection rates; there was no significant difference between groups.</p></article></body></html>`))
	}))
	defer page.Close()

	out := callVerifyClaim(t, verifyClaimDeps(t), page.URL, "vaccine significantly reduced infection rates")
	if out["contrastSignal"] != true {
		t.Errorf("contrastSignal = %v, want true (negation cue present)", out["contrastSignal"])
	}
}

// TestVerifyCitationSparseClaim (#358): a thin paywall/bot-wall stub (< 150
// words) still clears the pipeline's >100-byte admission gate and produces a
// claimSupport verdict, but must be annotated with sparsityNote + contentWords
// so a caller knows the check ran against a stub, not the full document.
func TestVerifyCitationSparseClaim(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><article><p>This page contains only a short note about vaccine efficacy research, with no additional detail provided here.</p></article></body></html>`))
	}))
	defer page.Close()

	out := callVerifyClaim(t, verifyClaimDeps(t), page.URL, "vaccine efficacy reduced infection")
	note, _ := out["sparsityNote"].(string)
	if note == "" {
		t.Fatal("expected non-empty sparsityNote for thin source content")
	}
	words, ok := out["contentWords"].(float64)
	if !ok || words >= 150 {
		t.Fatalf("expected contentWords < 150, got %v", out["contentWords"])
	}
}

// TestVerifyCitation_ClaimWaybackFallback: dead origin + Wayback snapshot → claim checked against the snapshot URL.
func TestVerifyCitation_ClaimWaybackFallback(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) }))
	defer origin.Close()
	snap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><article><p>The randomized trial showed the vaccine reduced infection rates significantly.</p></article></body></html>`))
	}))
	defer snap.Close()
	wb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"archived_snapshots":{"closest":{"available":true,"url":"` + snap.URL + `","status":"200"}}}`))
	}))
	defer wb.Close()

	deps := verifyClaimDeps(t)
	lv := scraper.NewLinkVerifier(scraper.LinkVerifierConfig{AllowPrivateIPs: true})
	lv.SetWaybackBase(wb.URL)
	deps.LinkVerifier = lv

	out := callVerifyClaim(t, deps, origin.URL+"/gone", "vaccine reduced infection rates")
	if out["claimSourceUrl"] != snap.URL {
		t.Errorf("claimSourceUrl = %v, want the Wayback snapshot %s", out["claimSourceUrl"], snap.URL)
	}
	if out["claimSupport"] != "addressed" {
		t.Errorf("claimSupport = %v, want addressed (checked against snapshot)", out["claimSupport"])
	}
}

// TestVerifyCitation_ClaimNoURL: a DOI/reference whose record has no URL → source_unavailable, claim echoed, never dropped.
func TestVerifyCitation_ClaimNoURL(t *testing.T) {
	// A free-text reference that matches no record → rec==nil path must still report the claim.
	deps := verifyClaimDeps(t)
	deps.AcademicProviders = nil // force no match
	deps.Search = nil
	out := callVerifyClaim(t, deps, "Nonexistent fabricated reference zzqq 1899", "some asserted claim about widgets")
	if out["claimSupport"] != "source_unavailable" {
		t.Errorf("claimSupport = %v, want source_unavailable (no record/URL)", out["claimSupport"])
	}
	if out["claim"] != "some asserted claim about widgets" {
		t.Errorf("claim should be echoed even on a reference miss: %v", out["claim"])
	}
}

// TestVerifyCitation_NoClaimRegression: without a claim, none of the claim keys appear.
func TestVerifyCitation_NoClaimRegression(t *testing.T) {
	out := callVerify(t, setupTestDeps(), "Mock Paper, 2024")
	for _, k := range []string{"claim", "claimSupport", "claimEvidence", "claimSourceUrl", "contrastSignal"} {
		if _, present := out[k]; present {
			t.Errorf("no-claim call should not emit %q, got %v", k, out[k])
		}
	}
}

// TestVerifyCitation_ClaimSchemaDeclared: every key in a claim-bearing response is
// declared in verifyCitationOutputSchema. The metadata drift gate
// (TestOutputSchemaMatchesResponse) does NOT include verify_citation in toolInputs,
// so this dedicated assertion is the only guard against an undeclared field.
func TestVerifyCitation_ClaimSchemaDeclared(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><article><p>The randomized trial found the vaccine did not significantly reduce infection rates.</p></article></body></html>`))
	}))
	defer page.Close()

	out := callVerifyClaim(t, verifyClaimDeps(t), page.URL, "vaccine significantly reduced infection rates")
	props, _ := verifyCitationOutputSchema["properties"].(map[string]any)
	if props == nil {
		t.Fatal("verifyCitationOutputSchema has no properties")
	}
	for k := range out {
		if _, declared := props[k]; !declared {
			t.Errorf("response key %q is not declared in verifyCitationOutputSchema", k)
		}
	}
}

// TestVerifyCitation_DOIExactMatch: a DOI the resolver knows attaches the EXACT
// record (matched by DOI), with matchConfidence high.
func TestVerifyCitation_DOIExactMatch(t *testing.T) {
	out := callVerify(t, setupTestDeps(), "10.1234/x")
	if out["inputType"] != "doi" {
		t.Fatalf("inputType = %v, want doi", out["inputType"])
	}
	rec, ok := out["matchedRecord"].(map[string]any)
	if !ok {
		t.Fatalf("expected a matchedRecord for the known DOI, got %v", out["matchedRecord"])
	}
	if rec["doi"] != "10.1234/x" {
		t.Errorf("matchedRecord.doi = %v, want the input DOI 10.1234/x", rec["doi"])
	}
	if out["matchConfidence"] != "high" {
		t.Errorf("matchConfidence = %v, want high", out["matchConfidence"])
	}
}

// TestVerifyCitation_DOINoFabricatedRecord is the CRITICAL anti-fabrication guard:
// a DOI the resolver has NO exact record for must NOT carry a matchedRecord or a
// matchConfidence — recording a near-neighbor as this DOI's record would fabricate
// exactly what the tool exists to catch. (The mock's Scholarly() returns a record
// with DOI 10.1/x for any query, so this also proves the fuzzy fallback never
// attaches a non-matching DOI.)
func TestVerifyCitation_DOINoFabricatedRecord(t *testing.T) {
	out := callVerify(t, setupTestDeps(), "10.9999/does-not-exist")
	if _, present := out["matchedRecord"]; present {
		t.Errorf("a DOI with no exact record must NOT have a matchedRecord, got %v", out["matchedRecord"])
	}
	if _, present := out["matchConfidence"]; present {
		t.Errorf("no matchConfidence without a matched record, got %v", out["matchConfidence"])
	}
}

// TestReferenceMatchConfidence_SingleTokenIsLow guards the noisy-match finding:
// a junk reference that coincidentally shares ONE substantive word with a record
// title (the live "garbage" → book titled "Garbage" case) must not read as a
// confident match — a single-token overlap stays "low" regardless of ratio.
func TestReferenceMatchConfidence_SingleTokenIsLow(t *testing.T) {
	t.Parallel()
	// One-word title fully contained in the reference: hit=1, total=1, 100% —
	// but a single coincidental token must still be "low".
	if got := referenceMatchConfidence("@#$ garbage !!!", &search.AcademicResult{Title: "Garbage"}); got != "low" {
		t.Errorf("single-token junk match = %q, want low", got)
	}
	// Two genuine matched tokens still earns a real confidence.
	if got := referenceMatchConfidence("Highly accurate protein structure prediction", &search.AcademicResult{Title: "Highly accurate protein structure prediction with AlphaFold"}); got == "low" {
		t.Errorf("multi-token title match = %q, want >= medium", got)
	}
}

func TestSameDOI(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want bool
	}{
		{"10.1/x", "10.1/X", true},                                          // case-insensitive
		{"https://doi.org/10.1038/abc", "10.1038/abc", true},                // URL-prefixed vs bare
		{"http://dx.doi.org/10.1/Y", "doi:10.1/y", true},                    // mixed prefixes
		{"10.1/x", "10.1/y", false},                                         // different
		{"", "10.1/x", false},                                               // empty never matches
		{"10.1038/s41586-021-03819-2", "10.1038/s41586-021-03828-1", false}, // the real neighbor case
	}
	for _, c := range cases {
		if got := sameDOI(c.a, c.b); got != c.want {
			t.Errorf("sameDOI(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestVerifyCitation_Reference(t *testing.T) {
	// setupTestDeps wires a mock academic provider that returns a record with a DOI.
	out := callVerify(t, setupTestDeps(), "Mock Paper, 2024")
	if out["inputType"] != "reference" {
		t.Errorf("inputType = %v, want reference", out["inputType"])
	}
	if out["exists"] != true {
		t.Errorf("exists = %v, want true (mock provider matched)", out["exists"])
	}
	if out["matchedRecord"] == nil {
		t.Error("expected a matchedRecord")
	}
	if out["trust"] != "untrusted-external-content" {
		t.Errorf("trust marker missing: %v", out["trust"])
	}
}

func TestVerifyCitation_DOIWithRetraction(t *testing.T) {
	// A Crossref stub that reports the DOI as retracted.
	crossref := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"updated-by":[{"DOI":"10.1/retr","type":"retraction","source":"retraction-watch","updated":{"date-time":"2020-05-05T00:00:00Z"}}]}}`))
	}))
	defer crossref.Close()

	deps := setupTestDeps()
	rr := search.NewCrossrefRetractionResolver("t@e.com", search.Deps{
		HTTPClient: crossref.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	rr.SetBaseURL(crossref.URL)
	deps.RetractionResolver = rr

	out := callVerify(t, deps, "10.1234/example.doi")
	if out["inputType"] != "doi" {
		t.Errorf("inputType = %v, want doi", out["inputType"])
	}
	if out["exists"] != true {
		t.Errorf("exists = %v, want true", out["exists"])
	}
	rs, ok := out["retractionStatus"].(map[string]any)
	if !ok || rs["retracted"] != true {
		t.Fatalf("expected retracted status, got %v", out["retractionStatus"])
	}
}

func TestVerifyCitation_URLDeadWithArchive(t *testing.T) {
	// Origin returns 404; Wayback stub has a snapshot.
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) }))
	defer origin.Close()
	wb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"archived_snapshots":{"closest":{"available":true,"url":"http://web.archive.org/snap","status":"200"}}}`))
	}))
	defer wb.Close()

	deps := setupTestDeps()
	lv := scraper.NewLinkVerifier(scraper.LinkVerifierConfig{AllowPrivateIPs: true})
	lv.SetWaybackBase(wb.URL)
	deps.LinkVerifier = lv

	out := callVerify(t, deps, origin.URL+"/missing")
	if out["inputType"] != "url" {
		t.Errorf("inputType = %v, want url", out["inputType"])
	}
	if out["exists"] != false {
		t.Errorf("dead URL exists = %v, want false", out["exists"])
	}
	if out["archivedUrl"] != "http://web.archive.org/snap" {
		t.Errorf("archivedUrl = %v, want the snapshot", out["archivedUrl"])
	}
}

// TestBestClaimURL verifies the OA-URL preference logic: PDFUrl beats a doi.org
// URL, a non-doi.org rec.URL beats a doi.org rec.URL, and we always fall back to
// at least a doi.org URL rather than returning empty.
func TestBestClaimURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		rec  search.AcademicResult
		doi  string
		want string
	}{
		{
			name: "PDFUrl preferred over doi.org URL",
			rec:  search.AcademicResult{URL: "https://doi.org/10.1/x", PDFUrl: "https://pmc.ncbi.nlm.nih.gov/articles/PMC123/"},
			doi:  "10.1/x",
			want: "https://pmc.ncbi.nlm.nih.gov/articles/PMC123/",
		},
		{
			name: "direct URL preferred over doi.org URL when no PDFUrl",
			rec:  search.AcademicResult{URL: "https://arxiv.org/abs/2301.00001"},
			doi:  "10.1/x",
			want: "https://arxiv.org/abs/2301.00001",
		},
		{
			name: "doi.org fallback when rec.URL is a doi.org redirect and no PDFUrl",
			rec:  search.AcademicResult{URL: "https://doi.org/10.1/x"},
			doi:  "10.1/x",
			want: "https://doi.org/10.1/x",
		},
		{
			name: "doi.org fallback constructed from doi when URL is empty",
			rec:  search.AcademicResult{DOI: "10.1/x"},
			doi:  "10.1/x",
			want: "https://doi.org/10.1/x",
		},
		{
			name: "dx.doi.org URL is also treated as a redirect",
			rec:  search.AcademicResult{URL: "https://dx.doi.org/10.1/x", PDFUrl: "https://europepmc.org/article/10.1/x"},
			doi:  "10.1/x",
			want: "https://europepmc.org/article/10.1/x",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := bestClaimURL(&c.rec, c.doi)
			if got != c.want {
				t.Errorf("bestClaimURL = %q, want %q", got, c.want)
			}
		})
	}
}

// TestVerifyCitation_DOIClaimPrefersOAURL: when a DOI record carries a PDFUrl
// (open-access URL), the claim check fetches that URL, not the doi.org redirect.
func TestVerifyCitation_DOIClaimPrefersOAURL(t *testing.T) {
	// Serve OA content at a local httptest URL that the claim check can scrape.
	oaPage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><article><p>The vaccine trial demonstrated significant efficacy in reducing infection rates across all age groups.</p></article></body></html>`))
	}))
	defer oaPage.Close()

	deps := verifyClaimDeps(t)
	// Inject a mock DOIResolver that returns a record whose PDFUrl is the local OA
	// page and whose URL is a doi.org redirect — so we can confirm PDFUrl wins.
	deps.AcademicProviders = map[string]search.AcademicProvider{
		"openalex": &mockOAURLProvider{oaURL: oaPage.URL},
	}

	out := callVerifyClaim(t, deps, "10.1234/oa-test", "vaccine efficacy reduced infection")
	if out["claimSourceUrl"] != oaPage.URL {
		t.Errorf("claimSourceUrl = %v, want the OA page URL %s (PDFUrl must win over doi.org URL)", out["claimSourceUrl"], oaPage.URL)
	}
	if out["claimSupport"] != "addressed" {
		t.Errorf("claimSupport = %v, want addressed (OA page addresses the claim)", out["claimSupport"])
	}
}

// TestVerifyCitation_PDFUrlServingHTML_StillAddressed reproduces #631
// end-to-end: a DOI record's PDFUrl ends in .pdf (routing the claim-check
// fetch through the document tier), but the server actually serves the full
// HTML article (nature.com's OA "grover" auth-handshake redirect does
// exactly this for 10.1038/nature12373.pdf). The claim check must still
// extract the HTML and address the claim, not collapse to
// source_unavailable.
func TestVerifyCitation_PDFUrlServingHTML_StillAddressed(t *testing.T) {
	oaPage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><article><p>The vaccine trial demonstrated significant efficacy in reducing infection rates across all age groups.</p></article></body></html>`))
	}))
	defer oaPage.Close()

	deps := verifyClaimDeps(t)
	deps.AcademicProviders = map[string]search.AcademicProvider{
		"openalex": &mockOAURLProvider{oaURL: oaPage.URL + "/nature12373.pdf"},
	}

	out := callVerifyClaim(t, deps, "10.1234/oa-test", "vaccine efficacy reduced infection")
	if out["claimSupport"] != "addressed" {
		t.Errorf("claimSupport = %v, want addressed (HTML served at a .pdf-suffixed URL must still be read)", out["claimSupport"])
	}
	if _, present := out["claimFetchError"]; present {
		t.Errorf("claimFetchError should be absent on a successful fetch, got %v", out["claimFetchError"])
	}
}

// TestVerifyCitation_ClaimFetchError_Surfaced verifies the #631
// claimFetchError field: when the claim-check fetch genuinely fails, the
// caller sees why instead of an unattributed source_unavailable. Uses the
// DOI path (mockOAURLProvider's PDFUrl points at a closed server) because
// verifyByURL's own link-liveness pre-check short-circuits fetchURL to ""
// for a dead URL — a distinct, already-correct "no fetch attempted" case
// that must NOT populate claimFetchError.
func TestVerifyCitation_ClaimFetchError_Surfaced(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	deadURL := dead.URL
	dead.Close() // closed before use: connection refused, not a 404

	deps := verifyClaimDeps(t)
	deps.AcademicProviders = map[string]search.AcademicProvider{
		"openalex": &mockOAURLProvider{oaURL: deadURL},
	}

	out := callVerifyClaim(t, deps, "10.1234/oa-test", "vaccine efficacy reduced infection")
	if out["claimSupport"] != "source_unavailable" {
		t.Fatalf("claimSupport = %v, want source_unavailable", out["claimSupport"])
	}
	fetchErr, _ := out["claimFetchError"].(string)
	if fetchErr == "" {
		t.Error("expected claimFetchError to be populated when the fetch itself failed (connection refused)")
	}
	if out["claimSourceUrl"] != deadURL {
		t.Errorf("claimSourceUrl = %v, want %q (the attempted URL, so the failure is attributable)", out["claimSourceUrl"], deadURL)
	}
}

// mockOAURLProvider returns a record whose PDFUrl is a given OA URL and whose
// rec.URL is a doi.org redirect — used to verify bestClaimURL's OA preference.
type mockOAURLProvider struct {
	oaURL string
}

func (m *mockOAURLProvider) Name() string { return "openalex" }
func (m *mockOAURLProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "mock oa"}
}
func (m *mockOAURLProvider) Scholarly(_ context.Context, _ search.AcademicSearchParams) ([]search.AcademicResult, error) {
	return []search.AcademicResult{{Title: "OA Paper", URL: "https://doi.org/10.1234/oa-test", DOI: "10.1234/oa-test", PDFUrl: m.oaURL, Year: 2024, Source: "openalex"}}, nil
}
func (m *mockOAURLProvider) ResolveByDOI(_ context.Context, doi string) (*search.AcademicResult, error) {
	if doi == "10.1234/oa-test" {
		return &search.AcademicResult{Title: "OA Paper", URL: "https://doi.org/10.1234/oa-test", DOI: "10.1234/oa-test", PDFUrl: m.oaURL, Year: 2024, Source: "openalex"}, nil
	}
	return nil, nil
}

// TestVerifyCitation_TitleMatch_Match: DOI + correct title → titleMatch "match".
// The mock ResolveByDOI returns the record for "10.1234/x" with title "Mock Paper".
func TestVerifyCitation_TitleMatch_Match(t *testing.T) {
	out := callVerify(t, setupTestDeps(), "10.1234/x Mock Paper")
	if out["titleMatch"] != "match" {
		t.Errorf("titleMatch = %v, want match (correct title supplied)", out["titleMatch"])
	}
}

// TestVerifyCitation_TitleMatch_Mismatch: DOI + clearly wrong title → titleMatch "mismatch".
func TestVerifyCitation_TitleMatch_Mismatch(t *testing.T) {
	out := callVerify(t, setupTestDeps(), "10.1234/x Quantum entanglement teleportation bandwidth")
	if out["titleMatch"] != "mismatch" {
		t.Errorf("titleMatch = %v, want mismatch (invented title supplied)", out["titleMatch"])
	}
}

// TestVerifyCitation_TitleMatch_NotChecked: bare DOI only → titleMatch "not_checked".
func TestVerifyCitation_TitleMatch_NotChecked(t *testing.T) {
	out := callVerify(t, setupTestDeps(), "10.1234/x")
	if out["titleMatch"] != "not_checked" {
		t.Errorf("titleMatch = %v, want not_checked (bare DOI, no title text)", out["titleMatch"])
	}
}

// TestVerifyCitation_BareDOIConfirmedCarriesCaveat (#599): a bare DOI resolves
// verificationStatus:"confirmed" from existence + non-retraction alone, with
// titleMatch:"not_checked" — no authenticity/title comparison ever ran. That
// combination must surface an explicit authenticityCaveat so a caller checking
// only the headline verificationStatus field isn't misled into full confidence.
func TestVerifyCitation_BareDOIConfirmedCarriesCaveat(t *testing.T) {
	out := callVerify(t, setupTestDeps(), "10.1234/x")
	if out["verificationStatus"] != verificationConfirmed {
		t.Fatalf("verificationStatus = %v, want confirmed (precondition for this test)", out["verificationStatus"])
	}
	if out["titleMatch"] != "not_checked" {
		t.Fatalf("titleMatch = %v, want not_checked (precondition for this test)", out["titleMatch"])
	}
	caveat, ok := out["authenticityCaveat"].(string)
	if !ok || caveat == "" {
		t.Errorf("authenticityCaveat missing/empty for a confirmed bare-DOI result with titleMatch:not_checked, got %v", out["authenticityCaveat"])
	}
}

// TestVerifyCitation_TitledDOINoCaveat: a DOI supplied WITH comparison title text
// runs titleMatch for real ("match"), so no authenticityCaveat should appear.
func TestVerifyCitation_TitledDOINoCaveat(t *testing.T) {
	out := callVerify(t, setupTestDeps(), "10.1234/x Mock Paper")
	if out["titleMatch"] != "match" {
		t.Fatalf("titleMatch = %v, want match (precondition for this test)", out["titleMatch"])
	}
	if _, present := out["authenticityCaveat"]; present {
		t.Errorf("authenticityCaveat must not be present when titleMatch actually ran, got %v", out["authenticityCaveat"])
	}
}

// TestVerifyCitation_TitleMatch_InSchema: titleMatch is declared in verifyCitationOutputSchema.
func TestVerifyCitation_TitleMatch_InSchema(t *testing.T) {
	out := callVerify(t, setupTestDeps(), "10.1234/x Mock Paper")
	props, _ := verifyCitationOutputSchema["properties"].(map[string]any)
	if props == nil {
		t.Fatal("verifyCitationOutputSchema has no properties")
	}
	if _, ok := props["titleMatch"]; !ok {
		t.Error("titleMatch is not declared in verifyCitationOutputSchema")
	}
	if _, ok := out["titleMatch"]; !ok {
		t.Error("titleMatch not emitted in response for a DOI input with title text")
	}
}

// TestVerifyCitation_TitleMatch_URLInputNoTitleMatch: non-DOI inputs must NOT emit titleMatch.
func TestVerifyCitation_TitleMatch_URLInputNoTitleMatch(t *testing.T) {
	out := callVerify(t, setupTestDeps(), "Mock Paper, 2024")
	if _, present := out["titleMatch"]; present {
		t.Errorf("titleMatch must not be emitted for reference inputs, got %v", out["titleMatch"])
	}
}

// scholarlyURLPage returns an HTML page that classifies peer_reviewed (citation_doi
// meta) with the given title and a long body so the HTML tier wins and populates
// StructuredData. Used by the #232 URL-input enrichment tests.
func scholarlyURLPage(doi, title string) string {
	return `<html><head><title>` + title + `</title>` +
		`<meta name="citation_doi" content="` + doi + `">` +
		`<meta name="citation_title" content="` + title + `">` +
		`</head><body><article><p>` +
		strings.Repeat("This randomized study reports its methods and results in detail. ", 6) +
		`</p></article></body></html>`
}

// TestVerifyCitation_URLScholarlyDOIDetected (#232): a URL pointing at a scholarly
// article (citation_doi meta) must extract the DOI and run the DOI enrichment —
// surfacing detectedDoi + matchedRecord + titleMatch — instead of liveness-only.
// The mock ResolveByDOI knows 10.1234/x → "Mock Paper", and the page title matches.
func TestVerifyCitation_URLScholarlyDOIDetected(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(scholarlyURLPage("10.1234/x", "Mock Paper")))
	}))
	defer page.Close()

	out := callVerify(t, verifyClaimDeps(t), page.URL)
	if out["inputType"] != "url" {
		t.Fatalf("inputType = %v, want url", out["inputType"])
	}
	if out["detectedDoi"] != "10.1234/x" {
		t.Errorf("detectedDoi = %v, want 10.1234/x", out["detectedDoi"])
	}
	if _, ok := out["matchedRecord"].(map[string]any); !ok {
		t.Errorf("expected matchedRecord for a scholarly URL, got %v", out["matchedRecord"])
	}
	if out["matchConfidence"] != "high" {
		t.Errorf("matchConfidence = %v, want high (exact DOI)", out["matchConfidence"])
	}
	if out["titleMatch"] != "match" {
		t.Errorf("titleMatch = %v, want match (page title equals record title)", out["titleMatch"])
	}
}

// TestVerifyCitation_URLScholarlyTitleMismatch (#232): a scholarly URL whose page
// title does NOT match the record the DOI resolves to → titleMatch "mismatch"
// (a misattributed link the caller should not trust). Page DOI 10.1234/x resolves
// to "Mock Paper", but the page title is an unrelated string.
func TestVerifyCitation_URLScholarlyTitleMismatch(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(scholarlyURLPage("10.1234/x", "Quantum entanglement teleportation bandwidth")))
	}))
	defer page.Close()

	out := callVerify(t, verifyClaimDeps(t), page.URL)
	if out["detectedDoi"] != "10.1234/x" {
		t.Fatalf("detectedDoi = %v, want 10.1234/x", out["detectedDoi"])
	}
	if out["titleMatch"] != "mismatch" {
		t.Errorf("titleMatch = %v, want mismatch (page title differs from record)", out["titleMatch"])
	}
}

// TestVerifyCitation_URLScholarlyRetracted (#232): a URL to a scholarly article
// whose DOI Crossref reports retracted → retractionStatus.retracted, surfaced from
// a URL input exactly as a DOI input would.
func TestVerifyCitation_URLScholarlyRetracted(t *testing.T) {
	crossref := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"updated-by":[{"DOI":"10.1/r","type":"retraction","source":"retraction-watch","updated":{"date-time":"2020-05-05T00:00:00Z"}}]}}`))
	}))
	defer crossref.Close()
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(scholarlyURLPage("10.1234/x", "Mock Paper")))
	}))
	defer page.Close()

	deps := verifyClaimDeps(t)
	rr := search.NewCrossrefRetractionResolver("t@e.com", search.Deps{
		HTTPClient: crossref.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	rr.SetBaseURL(crossref.URL)
	deps.RetractionResolver = rr

	out := callVerify(t, deps, page.URL)
	rs, ok := out["retractionStatus"].(map[string]any)
	if !ok || rs["retracted"] != true {
		t.Errorf("expected retractionStatus.retracted=true for a retracted scholarly URL, got %v", out["retractionStatus"])
	}
}

// TestVerifyCitation_URLNonScholarlyNoFalseDOI (#232): a plain (non-scholarly) page
// that happens to contain a DOI-shaped string in prose must NOT trigger DOI
// enrichment — no detectedDoi, no matchedRecord, no titleMatch. This protects the
// liveness-only contract for ordinary web pages.
func TestVerifyCitation_URLNonScholarlyNoFalseDOI(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Blog</title></head><body><article><p>` +
			strings.Repeat("Just an ordinary blog post about gardening tips and seasonal planting. ", 6) +
			`A doi-shaped string 10.1234/x appears in prose.</p></article></body></html>`))
	}))
	defer page.Close()

	out := callVerify(t, verifyClaimDeps(t), page.URL)
	if _, present := out["detectedDoi"]; present {
		t.Errorf("non-scholarly page must not surface detectedDoi, got %v", out["detectedDoi"])
	}
	if _, present := out["matchedRecord"]; present {
		t.Errorf("non-scholarly page must not surface matchedRecord, got %v", out["matchedRecord"])
	}
	if _, present := out["titleMatch"]; present {
		t.Errorf("non-scholarly page must not surface titleMatch, got %v", out["titleMatch"])
	}
}

// TestVerifyCitation_URLScholarlyClaim (#232): a scholarly URL given with a claim
// surfaces BOTH the DOI enrichment (detectedDoi) and the claim coverage — the body
// fetched for DOI detection is reused for the claim check (a code-level guarantee;
// see enrichURLWithScholarlyDOI → emitClaimCoverageFromContent).
func TestVerifyCitation_URLScholarlyClaim(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Mock Paper</title>` +
			`<meta name="citation_doi" content="10.1234/x">` +
			`<meta name="citation_title" content="Mock Paper">` +
			`</head><body><article><p>` +
			strings.Repeat("The randomized trial showed the vaccine significantly reduced infection rates across all groups. ", 4) +
			`</p></article></body></html>`))
	}))
	defer page.Close()

	out := callVerifyClaim(t, verifyClaimDeps(t), page.URL, "vaccine reduced infection rates")
	if out["detectedDoi"] != "10.1234/x" {
		t.Errorf("detectedDoi = %v, want 10.1234/x", out["detectedDoi"])
	}
	if out["claimSupport"] != "addressed" {
		t.Errorf("claimSupport = %v, want addressed", out["claimSupport"])
	}
	if out["claimSourceUrl"] != page.URL {
		t.Errorf("claimSourceUrl = %v, want %s", out["claimSourceUrl"], page.URL)
	}
}

// TestVerifyCitationClaimMismatch (#359 Test A): a DOI that resolves to a real
// academic record, whose open-access URL serves content addressing NONE of the
// claim's terms → claimSupport must be not_addressed (the mischaracterization
// signal). verify_citation has no generic `flags` array (that's
// audit_bibliography's shape) — claimSupport IS the mischaracterization signal
// here, so that is what this test asserts.
func TestVerifyCitationClaimMismatch(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><article><p>This paper discusses medieval architecture and cathedral construction in twelfth-century France.</p></article></body></html>`))
	}))
	defer page.Close()

	deps := verifyClaimDeps(t)
	deps.AcademicProviders = map[string]search.AcademicProvider{
		"openalex": &mockOAURLProvider{oaURL: page.URL},
	}

	out := callVerifyClaim(t, deps, "10.1234/oa-test", "vaccine efficacy reduced infection rates")
	if out["exists"] != true {
		t.Errorf("exists = %v, want true (DOI resolves to a real record)", out["exists"])
	}
	if out["claimSupport"] != "not_addressed" {
		t.Errorf("claimSupport = %v, want not_addressed", out["claimSupport"])
	}
}

// TestVerifyCitationNoClaimSkipSignal (#359 Test B): calling without a claim
// must surface claimCheckSkipped:true and must NOT emit claimSupport.
func TestVerifyCitationNoClaimSkipSignal(t *testing.T) {
	out := callVerify(t, setupTestDeps(), "10.1234/x")
	if out["claimCheckSkipped"] != true {
		t.Errorf("claimCheckSkipped = %v, want true", out["claimCheckSkipped"])
	}
	if _, present := out["claimSupport"]; present {
		t.Errorf("claimSupport must not be present when the claim check was skipped, got %v", out["claimSupport"])
	}
	if _, present := out["claimCheckSkippedReason"]; !present {
		t.Error("claimCheckSkippedReason should accompany claimCheckSkipped")
	}
}

// TestVerifyCitationFabricatedFreeTextRef (#359 Test E): a free-text reference
// with a plausible-looking title that resolves to NO academic record. Unlike the
// DOI paths, verify_citation's reference path reports this as exists:false +
// matchConfidence:"none" — there is no `flags` field on verify_citation (that
// shape belongs to audit_bibliography), so those are the fields asserted here.
func TestVerifyCitationFabricatedFreeTextRef(t *testing.T) {
	deps := setupTestDeps()
	deps.AcademicProviders = nil // force no academic match
	deps.Search = nil
	out := callVerify(t, deps, "A Totally Fabricated Study of Nonexistent Phenomena, Smith J, 2023")
	if out["exists"] != false {
		t.Errorf("exists = %v, want false (no academic match for a fabricated reference)", out["exists"])
	}
	if out["matchConfidence"] != "none" {
		t.Errorf("matchConfidence = %v, want none", out["matchConfidence"])
	}
}

// mockUnrelatedMediumMatchProvider (#510) models the exact bug reported in the
// issue: a free-text query returns a real but UNRELATED academic record whose
// title shares just enough substantive tokens with the query to score "medium"
// confidence (never "high", never "low"/coincidental-single-token). It has no
// DOIResolver capability, so verify_citation's DOI-exact-lookup path is never
// exercised — this mock exists solely to drive verifyByReference's fuzzy path.
type mockUnrelatedMediumMatchProvider struct{}

func (m *mockUnrelatedMediumMatchProvider) Name() string { return "openalex" }
func (m *mockUnrelatedMediumMatchProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "mock unrelated medium match"}
}
func (m *mockUnrelatedMediumMatchProvider) Scholarly(_ context.Context, _ search.AcademicSearchParams) ([]search.AcademicResult, error) {
	// Query "Smith 2021 time travel psychology" shares "time"/"travel"/"psychology"
	// with this title, but the paper itself is about an unrelated subject (physics
	// education, not the fabricated psychology study the citation claims).
	return []search.AcademicResult{{
		Title:   "Time Travel Concepts And Psychology Of Physics Education",
		URL:     "https://doi.org/10.5555/unrelated",
		DOI:     "10.5555/unrelated",
		Year:    2015,
		Authors: []string{"Jones, A."},
		Source:  "openalex",
	}}, nil
}

// TestVerifyCitationFreeTextFabricated_MediumMatchNotExists is the direct
// regression test for #510: a fabricated free-text citation whose ONLY academic
// hit is a real-but-unrelated paper at medium confidence must NOT read as
// exists:true. Before the fix, verifyByReference attached any non-nil match as
// matchedRecord with exists:true regardless of confidence — the exact false
// positive the issue reported (a fabricated "Smith 2021 time travel psychology"
// citation resolving to an unrelated real paper). After the fix, exists must
// stay false, verificationStatus must be "uncertain" (not "confirmed" or
// "not_found"), and the candidate must be surfaced as possibleMatch — never as
// matchedRecord, which is reserved for confirmed matches.
func TestVerifyCitationFreeTextFabricated_MediumMatchNotExists(t *testing.T) {
	deps := setupTestDeps()
	deps.AcademicProviders = map[string]search.AcademicProvider{
		"openalex": &mockUnrelatedMediumMatchProvider{},
	}
	deps.Search = nil

	out := callVerify(t, deps, "Smith 2021 time travel psychology")

	if out["exists"] != false {
		t.Errorf("exists = %v, want false — a medium-confidence match to an unrelated paper must never confirm a fabricated citation", out["exists"])
	}
	if out["verificationStatus"] != verificationUncertain {
		t.Errorf("verificationStatus = %v, want %q", out["verificationStatus"], verificationUncertain)
	}
	if out["matchConfidence"] != "medium" {
		t.Errorf("matchConfidence = %v, want medium (this test's mock is calibrated to that band)", out["matchConfidence"])
	}
	if _, present := out["matchedRecord"]; present {
		t.Errorf("matchedRecord must NOT be set for an unconfirmed (uncertain) match, got %v", out["matchedRecord"])
	}
	pm, ok := out["possibleMatch"].(map[string]any)
	if !ok {
		t.Fatalf("expected possibleMatch to carry the medium-confidence candidate, got %v", out["possibleMatch"])
	}
	if pm["title"] != "Time Travel Concepts And Psychology Of Physics Education" {
		t.Errorf("possibleMatch.title = %v, want the candidate's title", pm["title"])
	}
}

// TestVerifyCitationFreeTextHighConfidence_StillExists proves the fix is
// confidence-gated, not a blanket demotion: a genuine high-confidence free-text
// match (the mock academic provider's title, "Mock Paper", overlapping strongly
// with the query text) must still return exists:true, verificationStatus
// "confirmed", and matchedRecord populated — exactly the pre-fix behavior for a
// real match, unaffected by #510's fix.
func TestVerifyCitationFreeTextHighConfidence_StillExists(t *testing.T) {
	out := callVerify(t, setupTestDeps(), "Mock Paper, 2024")
	if out["exists"] != true {
		t.Errorf("exists = %v, want true (high-confidence match)", out["exists"])
	}
	if out["verificationStatus"] != verificationConfirmed {
		t.Errorf("verificationStatus = %v, want %q", out["verificationStatus"], verificationConfirmed)
	}
	if out["matchConfidence"] != "high" {
		t.Errorf("matchConfidence = %v, want high", out["matchConfidence"])
	}
	if _, present := out["matchedRecord"]; !present {
		t.Error("expected matchedRecord for a high-confidence match")
	}
	if _, present := out["possibleMatch"]; present {
		t.Errorf("possibleMatch must not be set alongside a confirmed matchedRecord, got %v", out["possibleMatch"])
	}
}

// TestVerifyCitationDOIPath_VerificationStatusUnaffected (#510): the DOI path's
// existence signal is already authoritative (exact-DOI entity lookup / Crossref /
// the doi.org handle registry), so it must be entirely unaffected by the
// free-text confidence gate — exists:true still maps to verificationStatus
// "confirmed", and a fabricated DOI with no record anywhere still maps to
// exists:false / verificationStatus "not_found".
func TestVerifyCitationDOIPath_VerificationStatusUnaffected(t *testing.T) {
	real := callVerify(t, setupTestDeps(), "10.1234/x")
	if real["exists"] != true {
		t.Fatalf("exists = %v, want true for the known-good DOI", real["exists"])
	}
	if real["verificationStatus"] != verificationConfirmed {
		t.Errorf("verificationStatus = %v, want %q for a resolved DOI", real["verificationStatus"], verificationConfirmed)
	}

	fake := callVerify(t, setupTestDeps(), "10.9999/fake.made.up.2099")
	if fake["exists"] != false {
		t.Fatalf("exists = %v, want false for a fabricated DOI", fake["exists"])
	}
	if fake["verificationStatus"] != verificationNotFound {
		t.Errorf("verificationStatus = %v, want %q for a fabricated DOI", fake["verificationStatus"], verificationNotFound)
	}
	if _, present := fake["possibleMatch"]; present {
		t.Errorf("the DOI path must never populate possibleMatch, got %v", fake["possibleMatch"])
	}
}

// TestDetectDOIStripsFrontiersViewerSuffix verifies #526: a Frontiers article
// URL's trailing "/full" (or similar publisher viewer-type path segment) must
// not be captured as part of the DOI — doiPattern's path character class
// allows "/" and would otherwise greedily include it.
func TestDetectDOIStripsFrontiersViewerSuffix(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "frontiers full",
			in:   "https://www.frontiersin.org/articles/10.3389/fpsyg.2020.01234/full",
			want: "10.3389/fpsyg.2020.01234",
		},
		{
			name: "abstract suffix",
			in:   "https://example.org/10.1234/abcd.5678/abstract",
			want: "10.1234/abcd.5678",
		},
		{
			name: "pdf suffix",
			in:   "https://example.org/10.1234/abcd.5678/pdf",
			want: "10.1234/abcd.5678",
		},
		{
			name: "no suffix unaffected",
			in:   "10.1038/nature12373",
			want: "10.1038/nature12373",
		},
		{
			name: "real doi ending in a word that happens to look like a suffix stays untouched",
			in:   "10.1234/some.pdf-format.study",
			want: "10.1234/some.pdf-format.study",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectDOI(tc.in); got != strings.ToLower(tc.want) {
				t.Errorf("detectDOI(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestDetectDOI_SICIFormat verifies #490: legacy Wiley "SICI"-format DOIs
// contain literal "<" and ">" (e.g. Haddon & Lewis, J. Comp. Neurol. 1996,
// a real Crossref-registered DOI). doiPattern's character class previously
// excluded both, so FindString truncated the match at the first "<",
// silently dropping the rest of a real identifier.
func TestDetectDOI_SICIFormat(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bare SICI DOI",
			in:   "10.1002/(SICI)1096-9861(19960129)365:1<113::AID-CNE9>3.0.CO;2-6",
			want: "10.1002/(sici)1096-9861(19960129)365:1<113::aid-cne9>3.0.co;2-6",
		},
		{
			name: "SICI DOI embedded in a doi.org URL",
			in:   "https://doi.org/10.1002/(SICI)1096-9861(19960129)365:1<113::AID-CNE9>3.0.CO;2-6",
			want: "10.1002/(sici)1096-9861(19960129)365:1<113::aid-cne9>3.0.co;2-6",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectDOI(tc.in); got != tc.want {
				t.Errorf("detectDOI(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestDetectDOI_MarkdownAutolinkDelimiterNotCaptured verifies the fix for
// #490's own regression: permitting "<"/">" in doiPattern for SICI DOIs must
// not cause a Markdown/HTML autolink's closing ">" delimiter to be captured
// as part of an ordinary (non-SICI) DOI.
func TestDetectDOI_MarkdownAutolinkDelimiterNotCaptured(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "markdown autolink around a doi.org URL",
			in:   "See <https://doi.org/10.1038/nature12373> for details.",
			want: "10.1038/nature12373",
		},
		{
			name: "bare DOI wrapped in angle brackets",
			in:   "<10.1038/nature12373>",
			want: "10.1038/nature12373",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectDOI(tc.in); got != tc.want {
				t.Errorf("detectDOI(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestVerifyCitation_EmptyInput(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "verify_citation", Arguments: map[string]any{"citation": "  "}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Error("empty citation should return a tool error")
	}
}
