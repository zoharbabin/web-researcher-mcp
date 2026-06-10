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
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// callScrapeDOI scrapes url (optionally a mode) through the in-memory MCP client
// and returns the parsed result.
func callScrapeDOI(t *testing.T, deps Dependencies, url, mode string) map[string]any {
	t.Helper()
	ctx := context.Background()
	srv := createTestServer(deps)
	client := connectTestClient(ctx, t, srv)
	defer client.Close()
	args := map[string]any{"url": url}
	if mode != "" {
		args["mode"] = mode
	}
	res, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "scrape_page", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return out
}

// crossrefRetractionDeps wires a Crossref retraction stub into scrape test deps.
func crossrefRetractionDeps(t *testing.T, handler http.HandlerFunc) (Dependencies, func()) {
	t.Helper()
	crossref := httptest.NewServer(handler)
	deps := scrapeTestDeps()
	rr := search.NewCrossrefRetractionResolver("t@e.com", search.Deps{
		HTTPClient: crossref.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	rr.SetBaseURL(crossref.URL)
	deps.RetractionResolver = rr
	return deps, crossref.Close
}

// scholarlyPage returns an HTML page that classifies as peer_reviewed (citation_doi
// meta) with a long body so the HTML tier wins.
func scholarlyPage(doi string) string {
	return `<html><head><title>A Study</title>` +
		`<meta name="citation_doi" content="` + doi + `">` +
		`<meta name="citation_title" content="A Study">` +
		`</head><body><article><p>` +
		strings.Repeat("This randomized study reports its methods and results in detail. ", 6) +
		`</p></article></body></html>`
}

// TestScrapeDOI_DetectedAndRetracted: a scholarly page with citation_doi whose DOI
// Crossref reports retracted → detectedDoi + retractionStatus.retracted.
func TestScrapeDOI_DetectedAndRetracted(t *testing.T) {
	deps, closeCR := crossrefRetractionDeps(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"updated-by":[{"DOI":"10.1/r","type":"retraction","source":"retraction-watch","updated":{"date-time":"2020-05-05T00:00:00Z"}}]}}`))
	})
	defer closeCR()
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(scholarlyPage("10.1234/abc")))
	}))
	defer page.Close()

	out := callScrapeDOI(t, deps, page.URL, "")
	if out["sourceType"] != "peer_reviewed" {
		t.Fatalf("sourceType = %v, want peer_reviewed", out["sourceType"])
	}
	if out["detectedDoi"] != "10.1234/abc" {
		t.Errorf("detectedDoi = %v, want 10.1234/abc", out["detectedDoi"])
	}
	rs, ok := out["retractionStatus"].(map[string]any)
	if !ok || rs["retracted"] != true {
		t.Errorf("expected retractionStatus.retracted=true, got %v", out["retractionStatus"])
	}
	if out["trust"] != "untrusted-external-content" {
		t.Errorf("trust marker missing: %v", out["trust"])
	}
}

// TestScrapeDOI_ReferencesOnlyNoFalsePositive (the crux): a DOI only in a
// references block far below the front matter, no citation_doi meta → NOT detected.
func TestScrapeDOI_ReferencesOnlyNoFalsePositive(t *testing.T) {
	deps, closeCR := crossrefRetractionDeps(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("resolver must not be called when no DOI is detected")
	})
	defer closeCR()
	// citation_meta present (citation_title) so it classifies scholarly, but NO
	// citation_doi; a references DOI sits well past detectScholarlyDOITopBytes.
	frontMatter := strings.Repeat("Front matter discussion of the topic without any identifier here. ", 120) // > 4000 bytes
	page := `<html><head><title>No DOI Meta</title><meta name="citation_title" content="No DOI Meta"></head><body><article><p>` +
		frontMatter + `</p><h2>References</h2><p>Smith et al. 10.9999/refonly.doi</p></article></body></html>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer ts.Close()

	out := callScrapeDOI(t, deps, ts.URL, "")
	if _, present := out["detectedDoi"]; present {
		t.Errorf("a references-list DOI must NOT be detected, got %v", out["detectedDoi"])
	}
	if _, present := out["retractionStatus"]; present {
		t.Error("no retractionStatus when no DOI detected")
	}
}

// TestScrapeDOI_NonScholarlyNoCheck: a plain prose page → no DOI scan, resolver never hit.
func TestScrapeDOI_NonScholarlyNoCheck(t *testing.T) {
	deps, closeCR := crossrefRetractionDeps(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("resolver must not be called for a non-scholarly page")
	})
	defer closeCR()
	page := `<html><head><title>Blog</title></head><body><article><p>` +
		strings.Repeat("Just an ordinary blog post about gardening tips and seasonal planting. ", 6) +
		`A doi-shaped string 10.1234/should-not-matter appears in prose.</p></article></body></html>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer ts.Close()

	out := callScrapeDOI(t, deps, ts.URL, "")
	if out["sourceType"] == "peer_reviewed" {
		t.Fatalf("precondition: page should not classify peer_reviewed, got %v", out["sourceType"])
	}
	if _, present := out["detectedDoi"]; present {
		t.Errorf("non-scholarly page must not surface detectedDoi, got %v", out["detectedDoi"])
	}
}

// TestScrapeDOI_CleanDOINoRetraction: scholarly DOI, Crossref reports nothing → detectedDoi present, no retractionStatus.
func TestScrapeDOI_CleanDOINoRetraction(t *testing.T) {
	deps, closeCR := crossrefRetractionDeps(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":{}}`))
	})
	defer closeCR()
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(scholarlyPage("10.1234/clean")))
	}))
	defer page.Close()

	out := callScrapeDOI(t, deps, page.URL, "")
	if out["detectedDoi"] != "10.1234/clean" {
		t.Errorf("detectedDoi = %v, want 10.1234/clean", out["detectedDoi"])
	}
	if _, present := out["retractionStatus"]; present {
		t.Error("clean DOI must not carry retractionStatus")
	}
}

// TestScrapeDOI_NilResolver: scholarly page, no resolver → detectedDoi present (offline detection), no retractionStatus, no error.
func TestScrapeDOI_NilResolver(t *testing.T) {
	deps := scrapeTestDeps() // RetractionResolver is nil
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(scholarlyPage("10.1234/noresolver")))
	}))
	defer page.Close()

	out := callScrapeDOI(t, deps, page.URL, "")
	if out["detectedDoi"] != "10.1234/noresolver" {
		t.Errorf("detectedDoi = %v, want 10.1234/noresolver (detection is offline)", out["detectedDoi"])
	}
	if _, present := out["retractionStatus"]; present {
		t.Error("nil resolver must not produce retractionStatus")
	}
}

// TestScrapeDOI_RawModeNoFields: raw mode never carries the DOI fields.
func TestScrapeDOI_RawModeNoFields(t *testing.T) {
	deps := scrapeTestDeps()
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(scholarlyPage("10.1234/raw")))
	}))
	defer page.Close()

	out := callScrapeDOI(t, deps, page.URL, "raw")
	if _, present := out["detectedDoi"]; present {
		t.Errorf("raw mode must not surface detectedDoi, got %v", out["detectedDoi"])
	}
}

// TestScrapePageSchemaDeclaresDOIFields is the MANDATORY schema guard: scrape_page
// is NOT in TestOutputSchemaMatchesResponse's toolInputs, so that gate never
// exercises scrape_page — this is the only test that catches a missing declaration.
func TestScrapePageSchemaDeclaresDOIFields(t *testing.T) {
	props, _ := scrapePageOutputSchema["properties"].(map[string]any)
	if props == nil {
		t.Fatal("scrapePageOutputSchema has no properties")
	}
	for _, k := range []string{"detectedDoi", "retractionStatus"} {
		if _, declared := props[k]; !declared {
			t.Errorf("scrapePageOutputSchema must declare %q", k)
		}
	}
}
