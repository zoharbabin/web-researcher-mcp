package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

func callAudit(t *testing.T, deps Dependencies, args map[string]any) (map[string]any, bool) {
	t.Helper()
	ctx := context.Background()
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "audit_bibliography", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		return nil, true
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return out, false
}

func summaryOf(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	s, ok := out["summary"].(map[string]any)
	if !ok {
		t.Fatalf("missing summary: %v", out)
	}
	return s
}

func TestAuditBibliography_RetractedDOI(t *testing.T) {
	// Crossref stub: the DOI is retracted.
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

	out, isErr := callAudit(t, deps, map[string]any{
		"entries": []any{map[string]any{"doi": "10.1234/example", "title": "A Retracted Paper"}},
	})
	if isErr {
		t.Fatal("unexpected tool error")
	}
	if out["trust"] != "untrusted-external-content" {
		t.Errorf("trust marker missing: %v", out["trust"])
	}
	s := summaryOf(t, out)
	if s["retracted"].(float64) != 1 {
		t.Errorf("expected 1 retracted, got %v", s["retracted"])
	}
	entries := out["entries"].([]any)
	e0 := entries[0].(map[string]any)
	flags, _ := e0["flags"].([]any)
	found := false
	for _, f := range flags {
		if f == "retracted" {
			found = true
		}
	}
	if !found {
		t.Errorf("entry should be flagged retracted: %v", e0)
	}
	if e0["exists"] != true {
		t.Errorf("a resolved DOI should exist=true: %v", e0)
	}
}

func TestAuditBibliography_DeadLinkWithArchive(t *testing.T) {
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

	out, isErr := callAudit(t, deps, map[string]any{
		"entries": []any{map[string]any{"url": origin.URL + "/missing", "title": "Dead Source"}},
	})
	if isErr {
		t.Fatal("unexpected tool error")
	}
	s := summaryOf(t, out)
	if s["deadLink"].(float64) != 1 {
		t.Errorf("expected 1 dead link, got %v", s["deadLink"])
	}
	e0 := out["entries"].([]any)[0].(map[string]any)
	if e0["linkLive"] != false {
		t.Errorf("dead link should be linkLive=false: %v", e0)
	}
	if e0["archivedUrl"] != "http://web.archive.org/snap" {
		t.Errorf("expected a Wayback archivedUrl: %v", e0["archivedUrl"])
	}
}

func TestAuditBibliography_BibTeXDocument(t *testing.T) {
	// A live origin so the link check passes (exists via live link).
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer origin.Close()
	deps := setupTestDeps()
	lv := scraper.NewLinkVerifier(scraper.LinkVerifierConfig{AllowPrivateIPs: true})
	deps.LinkVerifier = lv

	doc := "@misc{k, title = {Live Source}, url = {" + origin.URL + "}, year = {2024}}"
	out, isErr := callAudit(t, deps, map[string]any{"bibliography": doc, "format": "auto"})
	if isErr {
		t.Fatal("unexpected tool error")
	}
	if out["source"] != "bibliography:bibtex" {
		t.Errorf("source = %v, want bibliography:bibtex", out["source"])
	}
	s := summaryOf(t, out)
	if s["total"].(float64) != 1 {
		t.Errorf("expected 1 entry parsed from bibtex, got %v", s["total"])
	}
	e0 := out["entries"].([]any)[0].(map[string]any)
	if e0["linkLive"] != true {
		t.Errorf("live URL should be linkLive=true: %v", e0)
	}
	// Live link → not unverifiable, not dead.
	if len(e0["flags"].([]any)) != 0 {
		t.Errorf("a live-linked entry should be clean: %v", e0["flags"])
	}
}

func TestAuditBibliography_Unchecked(t *testing.T) {
	// No DOI, no URL, no title → nothing to corroborate → unchecked (NOT not_found:
	// absence of evidence, not an authoritative absence). A reason must explain it.
	deps := setupTestDeps()
	deps.LinkVerifier = scraper.NewLinkVerifier(scraper.LinkVerifierConfig{AllowPrivateIPs: true})
	out, isErr := callAudit(t, deps, map[string]any{
		"entries": []any{map[string]any{"author": "Anonymous"}},
	})
	if isErr {
		t.Fatal("unexpected tool error")
	}
	s := summaryOf(t, out)
	if s["unchecked"].(float64) != 1 {
		t.Errorf("an entry with no doi/url/title should be unchecked, got %v", s)
	}
	if s["notFound"].(float64) != 0 {
		t.Errorf("an uncheckable entry must NOT be counted as not_found (it isn't a fabrication): %v", s)
	}
	e0 := out["entries"].([]any)[0].(map[string]any)
	if e0["reason"] == nil || e0["reason"] == "" {
		t.Error("an unchecked entry must carry a reason so it isn't read as fake")
	}
}

func TestAuditBibliography_NotFoundDOI(t *testing.T) {
	// A DOI authoritatively absent from Crossref (404 → found=false) is not_found
	// (a possible fabrication), distinct from unchecked.
	crossref := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	}))
	defer crossref.Close()
	deps := setupTestDeps()
	rr := search.NewCrossrefRetractionResolver("t@e.com", search.Deps{
		HTTPClient: crossref.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	rr.SetBaseURL(crossref.URL)
	deps.RetractionResolver = rr

	out, isErr := callAudit(t, deps, map[string]any{
		"entries": []any{map[string]any{"doi": "10.9999/fabricated.does.not.exist"}},
	})
	if isErr {
		t.Fatal("unexpected tool error")
	}
	s := summaryOf(t, out)
	if s["notFound"].(float64) != 1 {
		t.Errorf("a DOI Crossref returns 404 for should be not_found, got %v", s)
	}
	if s["unchecked"].(float64) != 0 {
		t.Errorf("a checked-and-absent DOI must not be counted unchecked: %v", s)
	}
	e0 := out["entries"].([]any)[0].(map[string]any)
	if e0["exists"] != false {
		t.Errorf("a 404 DOI should report exists=false: %v", e0)
	}
}

func TestAuditBibliography_CapEnforced(t *testing.T) {
	// More than auditMaxEntries entries → only the cap is audited; the overflow is
	// reported in skipped (never silently dropped).
	deps := setupTestDeps()
	deps.LinkVerifier = scraper.NewLinkVerifier(scraper.LinkVerifierConfig{AllowPrivateIPs: true})
	n := auditMaxEntries + 25
	entries := make([]any, n)
	for i := range entries {
		entries[i] = map[string]any{"title": "Paper", "doi": "10.1/x"}
	}
	out, isErr := callAudit(t, deps, map[string]any{"entries": entries})
	if isErr {
		t.Fatal("unexpected tool error")
	}
	s := summaryOf(t, out)
	if int(s["total"].(float64)) != auditMaxEntries {
		t.Errorf("audited %v, want cap %d", s["total"], auditMaxEntries)
	}
	if out["entryCount"].(float64) != float64(auditMaxEntries) {
		t.Errorf("entryCount = %v, want %d", out["entryCount"], auditMaxEntries)
	}
	if out["skipped"] != float64(25) {
		t.Errorf("skipped = %v, want 25", out["skipped"])
	}
	if _, ok := out["skippedNote"]; !ok {
		t.Error("skippedNote must be present when truncated")
	}
}

func TestAuditBibliography_SessionMode(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.LinkVerifier = scraper.NewLinkVerifier(scraper.LinkVerifierConfig{AllowPrivateIPs: true})

	// Seed a session with two recorded sources under the harness's identity
	// (anonymous client → tenant "default", user "anonymous").
	tenantID := auth.TenantIDFromContext(ctx)
	userID := auth.UserIDFromContext(ctx)
	idx, err := deps.Sessions.Create(tenantID, userID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := deps.Sessions.AddSources(tenantID, userID, idx.ID, []session.ResearchSource{
		{URL: "https://example.com/a", Title: "Source A"},
		{URL: "https://example.com/b", Title: "Source B"},
	}); err != nil {
		t.Fatalf("add sources: %v", err)
	}

	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "audit_bibliography", Arguments: map[string]any{"sessionId": idx.ID}})
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
	if out["source"] != "session" {
		t.Errorf("source = %v, want session", out["source"])
	}
	if summaryOf(t, out)["total"].(float64) != 2 {
		t.Errorf("expected 2 session sources audited, got %v", out["summary"])
	}
}

func TestAuditBibliography_SessionNotFound(t *testing.T) {
	_, isErr := callAudit(t, setupTestDeps(), map[string]any{"sessionId": "does-not-exist"})
	if !isErr {
		t.Error("an unknown sessionId should be a tool error")
	}
}

func TestAuditBibliography_NothingToAudit(t *testing.T) {
	_, isErr := callAudit(t, setupTestDeps(), map[string]any{"bibliography": "not a bibliography"})
	if !isErr {
		t.Error("an unparseable document should be a tool error (nothing to audit)")
	}
	_, isErr = callAudit(t, setupTestDeps(), map[string]any{})
	if !isErr {
		t.Error("no input should be a tool error")
	}
}
