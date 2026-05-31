package tools

import "testing"

// TestSearchCacheKey_Deterministic verifies the key is a pure function of its
// inputs: identical parts → identical key (so legitimate cache hits work).
func TestSearchCacheKey_Deterministic(t *testing.T) {
	t.Parallel()
	a := searchCacheKey("web", "query", 5, "", "", "google")
	b := searchCacheKey("web", "query", 5, "", "", "google")
	if a != b {
		t.Fatalf("same inputs must yield same key: %s != %s", a, b)
	}
}

// TestSearchCacheKey_VariesByPart guarantees that changing ANY result-affecting
// part changes the key. This is the regression guard for the provider
// cache-key collision: two providers queried with the same terms must NOT share
// a cache entry (idempotency + consistency across calls). It also covers the
// other params each search tool feeds into its key.
func TestSearchCacheKey_VariesByPart(t *testing.T) {
	t.Parallel()
	base := searchCacheKey("web", "query", 5, "month", "medium", "en", "site.com", "lens", "google", "exact", "exclude", "US")

	cases := []struct {
		name string
		key  string
	}{
		{"provider", searchCacheKey("web", "query", 5, "month", "medium", "en", "site.com", "lens", "brave", "exact", "exclude", "US")},
		{"query", searchCacheKey("web", "other", 5, "month", "medium", "en", "site.com", "lens", "google", "exact", "exclude", "US")},
		{"numResults", searchCacheKey("web", "query", 10, "month", "medium", "en", "site.com", "lens", "google", "exact", "exclude", "US")},
		{"timeRange", searchCacheKey("web", "query", 5, "week", "medium", "en", "site.com", "lens", "google", "exact", "exclude", "US")},
		{"safe", searchCacheKey("web", "query", 5, "month", "high", "en", "site.com", "lens", "google", "exact", "exclude", "US")},
		{"language", searchCacheKey("web", "query", 5, "month", "medium", "fr", "site.com", "lens", "google", "exact", "exclude", "US")},
		{"site", searchCacheKey("web", "query", 5, "month", "medium", "en", "other.com", "lens", "google", "exact", "exclude", "US")},
		{"lens", searchCacheKey("web", "query", 5, "month", "medium", "en", "site.com", "security", "google", "exact", "exclude", "US")},
		{"exactTerms", searchCacheKey("web", "query", 5, "month", "medium", "en", "site.com", "lens", "google", "different", "exclude", "US")},
		{"excludeTerms", searchCacheKey("web", "query", 5, "month", "medium", "en", "site.com", "lens", "google", "exact", "different", "US")},
		{"country", searchCacheKey("web", "query", 5, "month", "medium", "en", "site.com", "lens", "google", "exact", "exclude", "GB")},
	}
	seen := map[string]string{base: "base"}
	for _, c := range cases {
		if c.key == base {
			t.Errorf("changing %q must change the cache key, but it matched base", c.name)
		}
		if prev, dup := seen[c.key]; dup {
			t.Errorf("changing %q collided with %q (key %s)", c.name, prev, c.key)
		}
		seen[c.key] = c.name
	}
}

// TestScrapeCacheKey_VariesByModeAndMaxLength guards the scrape cache key:
// content is truncated to max_length before caching, so the key must vary by
// URL, mode, AND max_length — otherwise a small-max_length request could serve
// a later larger request a truncated body.
func TestScrapeCacheKey_VariesByModeAndMaxLength(t *testing.T) {
	t.Parallel()
	base := scrapeCacheKey("https://example.com", "full", 50000)
	variants := map[string]string{
		"url":       scrapeCacheKey("https://other.com", "full", 50000),
		"mode":      scrapeCacheKey("https://example.com", "preview", 50000),
		"maxLength": scrapeCacheKey("https://example.com", "full", 2000),
	}
	for name, key := range variants {
		if key == base {
			t.Errorf("changing %q must change the scrape cache key", name)
		}
	}
	// Determinism: identical inputs → identical key.
	if scrapeCacheKey("https://example.com", "full", 50000) != base {
		t.Error("scrape cache key must be deterministic for identical inputs")
	}
}

// TestSearchCacheKey_NamespacedByTool ensures different tools with otherwise
// identical args do not share cache entries.
func TestSearchCacheKey_NamespacedByTool(t *testing.T) {
	t.Parallel()
	web := searchCacheKey("web", "query", 5)
	news := searchCacheKey("news", "query", 5)
	img := searchCacheKey("image", "query", 5)
	if web == news || web == img || news == img {
		t.Fatalf("different tools must yield different keys: web=%s news=%s image=%s", web, news, img)
	}
}
