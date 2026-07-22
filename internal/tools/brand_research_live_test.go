//go:build live

// Live external-API integration test — excluded from the default suite
// (non-deterministic external dependency, real network calls to
// api.brandfetch.io). Run with `make test-live`.
//
// Proves searchBrandFetchDomain's request shape (path-segment query + "c="
// client ID, no Bearer header) actually works against BrandFetch's real
// Brand Search API — the endpoint is reachable even without a registered
// client ID, so this runs unconditionally rather than skipping on a missing
// key (unlike BRANDFETCH_API_KEY-gated Brand API tests would).
package tools

import (
	"context"
	"os"
	"sync"
	"testing"
)

func TestSearchBrandFetchDomainLive(t *testing.T) {
	clientID := os.Getenv("BRANDFETCH_CLIENT_ID")
	domain := searchBrandFetchDomain(context.Background(), clientID, "Kaltura")
	if domain != "kaltura.com" {
		t.Fatalf("searchBrandFetchDomain(%q) = %q, want %q", "Kaltura", domain, "kaltura.com")
	}
	t.Logf("resolved domain: %s (clientID set: %v)", domain, clientID != "")
}

// TestFetchBrandFetchLive proves the restored Tier 1 Brand API + Context API
// (Bearer auth, /v2/brands/{domain} + /v2/context/{domain}) work against the
// real BrandFetch API. Skipped when BRANDFETCH_API_KEY isn't set — unlike the
// Search API, the Brand API requires a valid key to return real data.
func TestFetchBrandFetchLive(t *testing.T) {
	apiKey := os.Getenv("BRANDFETCH_API_KEY")
	if apiKey == "" {
		t.Skip("BRANDFETCH_API_KEY not set; skipping live Brand API test")
	}

	result := &brandResearchResult{Identity: brandIdentity{Name: "Kaltura", Domain: "kaltura.com"}}
	var mu sync.Mutex
	src := fetchBrandFetch(context.Background(), apiKey, "kaltura.com", "standard", result, &mu)
	if src == nil {
		t.Fatal("fetchBrandFetch returned nil against the real API — expected a populated brandSource for kaltura.com")
	}
	if result.Identity.Description == "" {
		t.Error("Identity.Description empty — expected the real Brand API to return a description for kaltura.com")
	}
	colorPrimary := ""
	if result.Colors == nil || result.Colors.Primary == "" {
		t.Error("Colors.Primary empty — expected the real Brand API to return brand colors for kaltura.com")
	} else {
		colorPrimary = result.Colors.Primary
	}
	t.Logf("fetched: name=%s description=%q colors.primary=%s tagline=%q", result.Identity.Name, result.Identity.Description, colorPrimary, result.Identity.Tagline)
}
