package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
