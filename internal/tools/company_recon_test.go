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
