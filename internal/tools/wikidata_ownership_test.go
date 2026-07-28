package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// mockOwnershipResolver is a stub search.OwnershipResolver (#248) that counts
// calls so tests can assert caching behavior, and returns a fixed
// result/found/err triple.
type mockOwnershipResolver struct {
	result *search.OwnershipResult
	found  bool
	err    error
	calls  int
}

func (m *mockOwnershipResolver) Resolve(_ context.Context, _ string) (*search.OwnershipResult, bool, error) {
	m.calls++
	return m.result, m.found, m.err
}
func (m *mockOwnershipResolver) Name() string { return "mock-wikidata-ownership" }

// ownershipTestDeps builds Dependencies with a nil Scraper, so
// detectSelfPromotionForURL always no-ops (returns nil) and the else-branch
// Wikidata ownership fallback always runs — no live scrape or DNS-resolvable
// hostname is needed since detectCorporateOwnershipForURL only parses the URL
// string and calls the resolver.
func ownershipTestDeps(resolver search.OwnershipResolver) Dependencies {
	deps := setupTestDeps()
	deps.Scraper = nil
	deps.WikidataOwnershipResolver = resolver
	return deps
}

func TestBrandTokenFromHost(t *testing.T) {
	cases := []struct {
		host string
		want string
	}{
		{"shopify.com", "shopify"},
		{"mail.marketo.com", "marketo"},
		{"go.dev", "go"},
		{"x.co", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := brandTokenFromHost(c.host); got != c.want {
			t.Errorf("brandTokenFromHost(%q) = %q, want %q", c.host, got, c.want)
		}
	}
}

func TestCorporateOwnershipSkippedWhenResolverNil(t *testing.T) {
	deps := ownershipTestDeps(nil)

	rec := verifyOneRecommendation(context.Background(), deps, recommendationItem{Title: "Marketo", URL: "https://marketo.com/best-tools"}, "", 0)
	if rec.CorporateOwnershipSignal != nil {
		t.Errorf("corporateOwnershipSignal must be nil when resolver is nil, got %+v", rec.CorporateOwnershipSignal)
	}
	for _, f := range rec.Flags {
		if f == "corporate_ownership" {
			t.Errorf("flags must not contain corporate_ownership when resolver is nil, got %v", rec.Flags)
		}
	}
}

func TestCorporateOwnershipDetected(t *testing.T) {
	resolver := &mockOwnershipResolver{
		result: &search.OwnershipResult{OwnerLabel: "Adobe Inc.", OwnerQID: "Q8", EntityQID: "Q123"},
		found:  true,
	}
	deps := ownershipTestDeps(resolver)

	rec := verifyOneRecommendation(context.Background(), deps, recommendationItem{Title: "Marketo", URL: "https://marketo.com/best-tools"}, "", 0)
	if rec.CorporateOwnershipSignal == nil {
		t.Fatalf("expected corporateOwnershipSignal to be set")
	}
	if rec.CorporateOwnershipSignal.CorporateOwner != "Adobe Inc." {
		t.Errorf("corporateOwner = %q, want Adobe Inc.", rec.CorporateOwnershipSignal.CorporateOwner)
	}
	if rec.CorporateOwnershipSignal.CorporateOwnerQID != "Q8" {
		t.Errorf("corporateOwnerQID = %q, want Q8", rec.CorporateOwnershipSignal.CorporateOwnerQID)
	}
	if !containsString(rec.Flags, "corporate_ownership") {
		t.Errorf("flags missing corporate_ownership, got %v", rec.Flags)
	}
	found := false
	for _, r := range rec.Reasons {
		if strings.Contains(r, "Adobe Inc.") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a reason mentioning Adobe Inc., got %v", rec.Reasons)
	}
}

func TestCorporateOwnershipCacheHit(t *testing.T) {
	resolver := &mockOwnershipResolver{
		result: &search.OwnershipResult{OwnerLabel: "Adobe Inc.", OwnerQID: "Q8", EntityQID: "Q123"},
		found:  true,
	}
	deps := ownershipTestDeps(resolver)
	deps.Cache = cache.NewMemory(cache.MemoryConfig{})

	verifyOneRecommendation(context.Background(), deps, recommendationItem{Title: "Marketo", URL: "https://marketo.com/best-tools"}, "", 0)
	verifyOneRecommendation(context.Background(), deps, recommendationItem{Title: "Marketo", URL: "https://marketo.com/best-tools"}, "", 0)

	if resolver.calls != 1 {
		t.Errorf("expected resolver to be invoked exactly once across 2 calls, got %d", resolver.calls)
	}
}

func TestCorporateOwnershipNegativeCached(t *testing.T) {
	resolver := &mockOwnershipResolver{found: false}
	deps := ownershipTestDeps(resolver)
	deps.Cache = cache.NewMemory(cache.MemoryConfig{})

	for i := 0; i < 2; i++ {
		rec := verifyOneRecommendation(context.Background(), deps, recommendationItem{Title: "Marketo", URL: "https://marketo.com/best-tools"}, "", 0)
		if rec.CorporateOwnershipSignal != nil {
			t.Errorf("call %d: corporateOwnershipSignal should be nil on a negative result", i)
		}
	}
	if resolver.calls != 1 {
		t.Errorf("expected resolver to be invoked exactly once, got %d", resolver.calls)
	}
}

// TestSelfPromotionTakesPrecedenceOverOwnership uses a real scrape (unlike the
// other tests here) because detectSelfPromotionForURL must actually detect
// self-promotion to prove the else-branch (the Wikidata fallback) never runs.
// httptest servers bind to 127.0.0.1, so DetectSelfPromotion's own brandToken
// derivation (host's first label) is "127" — the page ranks "127" at #1 to
// match it.
func TestSelfPromotionTakesPrecedenceOverOwnership(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body><p>1. 127 is the top pick.</p></body></html>`))
	}))
	defer page.Close()

	resolver := &mockOwnershipResolver{
		result: &search.OwnershipResult{OwnerLabel: "Adobe Inc.", OwnerQID: "Q8", EntityQID: "Q123"},
		found:  true,
	}
	deps := setupTestDeps()
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	deps.WikidataOwnershipResolver = resolver

	rec := verifyOneRecommendation(context.Background(), deps, recommendationItem{Title: "Marketo", URL: page.URL}, "", 0)
	if rec.SelfPromotionSignal == nil {
		t.Fatalf("expected selfPromotionSignal to be set (host stem matches rank-1 item); got %+v", rec)
	}
	if rec.CorporateOwnershipSignal != nil {
		t.Errorf("corporateOwnershipSignal must be nil when self-promotion already fired, got %+v", rec.CorporateOwnershipSignal)
	}
	if resolver.calls != 0 {
		t.Errorf("ownership resolver must not be invoked when self-promotion already fired, calls=%d", resolver.calls)
	}
}

// TestSelfPromotionSignalCorporateOwnerFields (#248) confirms the new
// CorporateOwner/CorporateOwnerQID fields round-trip through JSON and are
// omitted when empty.
func TestSelfPromotionSignalCorporateOwnerFields(t *testing.T) {
	sig := content.SelfPromotionSignal{
		Detected:          true,
		BrandDomain:       "marketo.com",
		BrandToken:        "marketo",
		CorporateOwner:    "Adobe Inc.",
		CorporateOwnerQID: "Q8",
		Confidence:        "medium",
	}
	b, err := json.Marshal(sig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out content.SelfPromotionSignal
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.CorporateOwner != "Adobe Inc." || out.CorporateOwnerQID != "Q8" {
		t.Errorf("round-trip mismatch: %+v", out)
	}

	empty := content.SelfPromotionSignal{Detected: true, BrandDomain: "x.com", BrandToken: "x"}
	b2, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if strings.Contains(string(b2), "corporateOwner") {
		t.Errorf("empty CorporateOwner/CorporateOwnerQID must be omitted, got %s", b2)
	}
}
