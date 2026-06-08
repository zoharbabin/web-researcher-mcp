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
