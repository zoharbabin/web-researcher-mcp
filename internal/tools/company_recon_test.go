package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

func TestCompanyReconMissingTarget(t *testing.T) {
	t.Parallel()
	_, res := callTool(t, setupTestDeps(), "company_recon", map[string]any{})
	if !res.IsError {
		t.Error("empty target should produce a tool error")
	}
}

func TestCompanyReconPrivateHostRejected(t *testing.T) {
	t.Parallel()
	_, res := callTool(t, setupTestDeps(), "company_recon", map[string]any{"target": "site.internal"})
	if !res.IsError {
		t.Error("a private/internal host target should produce a tool error")
	}
}

func TestCompanyReconAllPhases(t *testing.T) {
	t.Parallel()
	out, res := callTool(t, setupTestDeps(), "company_recon", map[string]any{"target": "acme.com"})
	if res.IsError {
		t.Fatalf("unexpected tool error: %v", res.Content)
	}
	if out["domain"] != "acme.com" {
		t.Errorf("domain = %v, want acme.com", out["domain"])
	}
	if out["trust"] != untrustedContentTrust {
		t.Errorf("trust = %v, want %q", out["trust"], untrustedContentTrust)
	}
	certSANs, ok := out["cert_sans"].([]any)
	if !ok || len(certSANs) == 0 {
		t.Fatalf("cert_sans missing or empty: %v", out["cert_sans"])
	}
	archiveURLs, ok := out["archive_urls"].([]any)
	if !ok || len(archiveURLs) == 0 {
		t.Fatalf("archive_urls missing or empty: %v", out["archive_urls"])
	}
	profile, ok := out["profile"].(map[string]any)
	if !ok || profile["summary"] == "" {
		t.Fatalf("profile.summary missing: %v", out["profile"])
	}
	subdomains, ok := out["subdomains"].([]any)
	if !ok || len(subdomains) == 0 {
		t.Fatalf("subdomains should be derived from cert_sans + archive_urls: %v", out["subdomains"])
	}
	sources, ok := out["sources"].([]any)
	if !ok || len(sources) != 3 { // ct_logs, archives, profiling
		t.Fatalf("sources = %v, want 3 entries", out["sources"])
	}
}

func TestCompanyReconPhaseSelection(t *testing.T) {
	t.Parallel()
	out, res := callTool(t, setupTestDeps(), "company_recon", map[string]any{
		"target": "acme.com",
		"phases": []any{"ct_logs"},
	})
	if res.IsError {
		t.Fatalf("unexpected tool error: %v", res.Content)
	}
	if _, ok := out["cert_sans"]; !ok {
		t.Error("ct_logs phase should have populated cert_sans")
	}
	if _, ok := out["archive_urls"]; ok {
		t.Error("archives phase was not selected; archive_urls should be absent")
	}
	if _, ok := out["profile"]; ok {
		t.Error("profiling phase was not selected; profile should be absent")
	}
}

// mockErroringCTLogResolver always fails, exercising company_recon's soft-fail
// contract: one phase erroring must never fail the whole call.
type mockErroringCTLogResolver struct{}

func (m *mockErroringCTLogResolver) Name() string { return "crt.sh" }
func (m *mockErroringCTLogResolver) Lookup(_ context.Context, _ string, _ int) ([]search.CertEntry, error) {
	return nil, errors.New("upstream unavailable")
}

func TestCompanyReconPhaseErrorDegradesSoft(t *testing.T) {
	t.Parallel()
	deps := setupTestDeps()
	deps.CTLogResolver = &mockErroringCTLogResolver{}
	out, res := callTool(t, deps, "company_recon", map[string]any{"target": "acme.com"})
	if res.IsError {
		t.Fatalf("a single phase erroring must not fail the whole call: %v", res.Content)
	}
	if _, ok := out["cert_sans"]; ok {
		t.Error("cert_sans should be absent when ct_logs errored")
	}
	if _, ok := out["archive_urls"]; !ok {
		t.Error("archives phase should still have run and populated archive_urls")
	}
	// Regression for #438: a genuine zero-result and a swallowed resolver error
	// used to look identical (both just "cert_sans absent"). phase_errors makes
	// them distinguishable.
	phaseErrors, ok := out["phase_errors"].([]any)
	if !ok || len(phaseErrors) != 1 {
		t.Fatalf("phase_errors = %v, want exactly 1 entry for the errored ct_logs phase", out["phase_errors"])
	}
	pe := phaseErrors[0].(map[string]any)
	if pe["phase"] != "ct_logs" {
		t.Errorf("phase_errors[0].phase = %v, want ct_logs", pe["phase"])
	}
	if pe["error"] != "upstream unavailable" {
		t.Errorf("phase_errors[0].error = %v, want %q", pe["error"], "upstream unavailable")
	}
}

// mockErroringArchiveResolver mirrors mockErroringCTLogResolver for the
// archives phase.
type mockErroringArchiveResolver struct{}

func (m *mockErroringArchiveResolver) Name() string { return "wayback-cdx" }
func (m *mockErroringArchiveResolver) Lookup(_ context.Context, _ string, _ int) ([]search.ArchiveEntry, error) {
	return nil, errors.New("wayback: server error 503")
}

func TestCompanyReconArchivesPhaseErrorDegradesSoft(t *testing.T) {
	t.Parallel()
	deps := setupTestDeps()
	deps.ArchiveResolver = &mockErroringArchiveResolver{}
	out, res := callTool(t, deps, "company_recon", map[string]any{"target": "acme.com"})
	if res.IsError {
		t.Fatalf("a single phase erroring must not fail the whole call: %v", res.Content)
	}
	if _, ok := out["archive_urls"]; ok {
		t.Error("archive_urls should be absent when archives errored")
	}
	if _, ok := out["cert_sans"]; !ok {
		t.Error("ct_logs phase should still have run and populated cert_sans")
	}
	phaseErrors, ok := out["phase_errors"].([]any)
	if !ok || len(phaseErrors) != 1 {
		t.Fatalf("phase_errors = %v, want exactly 1 entry for the errored archives phase", out["phase_errors"])
	}
	pe := phaseErrors[0].(map[string]any)
	if pe["phase"] != "archives" {
		t.Errorf("phase_errors[0].phase = %v, want archives", pe["phase"])
	}
}

// recordingCTLogResolver captures the maxResults value it was called with, so
// tests can assert on company_recon's per-phase clamp without a live crt.sh.
type recordingCTLogResolver struct {
	gotMaxResults int
}

func (m *recordingCTLogResolver) Name() string { return "crt.sh" }
func (m *recordingCTLogResolver) Lookup(_ context.Context, _ string, maxResults int) ([]search.CertEntry, error) {
	m.gotMaxResults = maxResults
	return nil, nil
}

// recordingArchiveResolver mirrors recordingCTLogResolver for the archives phase.
type recordingArchiveResolver struct {
	gotMaxResults int
}

func (m *recordingArchiveResolver) Name() string { return "wayback-cdx" }
func (m *recordingArchiveResolver) Lookup(_ context.Context, _ string, maxResults int) ([]search.ArchiveEntry, error) {
	m.gotMaxResults = maxResults
	return nil, nil
}

func TestCompanyReconNumResultsClampedPerPhase(t *testing.T) {
	t.Parallel()
	deps := setupTestDeps()
	ctLog := &recordingCTLogResolver{}
	archive := &recordingArchiveResolver{}
	deps.CTLogResolver = ctLog
	deps.ArchiveResolver = archive

	_, res := callTool(t, deps, "company_recon", map[string]any{"target": "acme.com", "num_results": 1000})
	if res.IsError {
		t.Fatalf("unexpected tool error: %v", res.Content)
	}
	if ctLog.gotMaxResults != 25 {
		t.Errorf("ct_logs maxResults = %d, want 25 (per the tool's documented cap)", ctLog.gotMaxResults)
	}
	if archive.gotMaxResults != 1000 {
		t.Errorf("archives maxResults = %d, want 1000", archive.gotMaxResults)
	}
}

func TestCompanyReconNilResolverSkipsPhase(t *testing.T) {
	t.Parallel()
	deps := setupTestDeps()
	deps.CTLogResolver = nil
	deps.ArchiveResolver = nil
	out, res := callTool(t, deps, "company_recon", map[string]any{"target": "acme.com"})
	if res.IsError {
		t.Fatalf("nil resolvers must degrade to a soft skip, not a tool error: %v", res.Content)
	}
	if _, ok := out["cert_sans"]; ok {
		t.Error("cert_sans should be absent when CTLogResolver is nil")
	}
	if _, ok := out["archive_urls"]; ok {
		t.Error("archive_urls should be absent when ArchiveResolver is nil")
	}
	if _, ok := out["profile"]; !ok {
		t.Error("profiling phase should still have run")
	}
}

func TestCompanyReconCacheHit(t *testing.T) {
	t.Parallel()
	deps := setupTestDeps()
	args := map[string]any{"target": "acme.com"}

	first, res := callTool(t, deps, "company_recon", args)
	if res.IsError {
		t.Fatalf("unexpected tool error: %v", res.Content)
	}
	if first["cache_age"] != float64(0) {
		t.Errorf("first call cache_age = %v, want 0 (live fetch)", first["cache_age"])
	}

	second, res := callTool(t, deps, "company_recon", args)
	if res.IsError {
		t.Fatalf("unexpected tool error on cached call: %v", res.Content)
	}
	if second["domain"] != "acme.com" {
		t.Errorf("cached result domain = %v, want acme.com", second["domain"])
	}
}

func TestCompanyReconSessionTracking(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	deps := setupTestDeps()
	tenantID := auth.TenantIDFromContext(ctx)
	userID := auth.UserIDFromContext(ctx)
	idx, err := deps.Sessions.Create(tenantID, userID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, res := callTool(t, deps, "company_recon", map[string]any{"target": "acme.com", "sessionId": idx.ID})
	if res.IsError {
		t.Fatalf("unexpected tool error: %v", res.Content)
	}

	got, err := deps.Sessions.GetFull(tenantID, userID, idx.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if len(got.Sources) == 0 {
		t.Error("company_recon should have recorded sources on the session")
	}
}

// TestCompanyReconWebPhaseAloneProducesSummary is the regression test for
// issue #432: the tool description promises phases "profiling|ct_logs|
// archives|web" are independently selectable, but "web" alone used to be a
// no-op placeholder. selecting "web" without "profiling" must produce the
// same web-search company summary "profiling" alone would.
func TestCompanyReconWebPhaseAloneProducesSummary(t *testing.T) {
	t.Parallel()
	out, res := callTool(t, setupTestDeps(), "company_recon", map[string]any{
		"target": "acme.com",
		"phases": []any{"web"},
	})
	if res.IsError {
		t.Fatalf("unexpected tool error: %v", res.Content)
	}
	profile, ok := out["profile"].(map[string]any)
	if !ok || profile["summary"] == "" {
		t.Fatalf("phases:[web] alone should populate profile.summary, got: %v", out["profile"])
	}
	if _, ok := out["cert_sans"]; ok {
		t.Error("ct_logs phase was not selected; cert_sans should be absent")
	}
	if _, ok := out["archive_urls"]; ok {
		t.Error("archives phase was not selected; archive_urls should be absent")
	}
	sources, ok := out["sources"].([]any)
	if !ok || len(sources) != 1 {
		t.Fatalf("sources = %v, want exactly 1 entry (the web-search summary)", out["sources"])
	}
	s0 := sources[0].(map[string]any)
	if s0["phase"] != "web" {
		t.Errorf("sole source phase = %v, want %q", s0["phase"], "web")
	}
	if s0["name"] != "web_search" {
		t.Errorf("sole source name = %v, want web_search (not a placeholder note)", s0["name"])
	}
}

// TestCompanyReconWebAndProfilingNoDuplicateSource proves selecting both
// "web" and "profiling" together records the source once, not twice — the
// two phase names share one underlying web-search call.
func TestCompanyReconWebAndProfilingNoDuplicateSource(t *testing.T) {
	t.Parallel()
	out, res := callTool(t, setupTestDeps(), "company_recon", map[string]any{
		"target": "acme.com",
		"phases": []any{"web", "profiling"},
	})
	if res.IsError {
		t.Fatalf("unexpected tool error: %v", res.Content)
	}
	sources, ok := out["sources"].([]any)
	if !ok || len(sources) != 1 {
		t.Fatalf("sources = %v, want exactly 1 entry (no duplicate web-search source)", out["sources"])
	}
}

func TestCompanyReconCompanyNameFallsBackToWebSearch(t *testing.T) {
	t.Parallel()
	out, res := callTool(t, setupTestDeps(), "company_recon", map[string]any{"target": "Acme Corp"})
	if res.IsError {
		t.Fatalf("unexpected tool error: %v", res.Content)
	}
	if out["domain"] != "example.com" { // mockProvider.Web always returns example.com
		t.Errorf("domain = %v, want example.com (resolved via web search)", out["domain"])
	}
}
