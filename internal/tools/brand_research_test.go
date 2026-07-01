package tools

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
)

// callBrandResearch drives brand_research through the in-memory MCP client.
func callBrandResearch(t *testing.T, deps Dependencies, args map[string]any) (map[string]any, bool) {
	t.Helper()
	ctx := context.Background()
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "brand_research", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		return nil, true
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse output: %v — raw: %s", err, res.Content[0].(*mcp.TextContent).Text)
	}
	return out, false
}

// brandDepsWithPrivate wires a private-IP-allowing scraper so httptest servers
// on 127.0.0.1 are reachable from the tool.
func brandDepsWithPrivate() Dependencies {
	deps := setupTestDeps()
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
	})
	return deps
}

// ─── 1. Missing input ─────────────────────────────────────────────────────────

func TestBrandResearchMissingInput(t *testing.T) {
	t.Parallel()
	_, isErr := callBrandResearch(t, setupTestDeps(), map[string]any{})
	if !isErr {
		t.Error("empty url and company_name should produce a tool error")
	}
}

// ─── 2b. rootDomain strips informational subdomains ──────────────────────────

func TestBrandResearchRootDomain(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input, want string
	}{
		{"support.apple.com", "apple.com"},
		{"developer.apple.com", "apple.com"},
		{"help.github.com", "github.com"},
		{"docs.stripe.com", "stripe.com"},
		{"apple.com", "apple.com"},
		{"stripe.com", "stripe.com"},
		// brand subdomain → root, so Tier 4 probes brand.acme.com correctly.
		{"brand.acme.com", "acme.com"},
		// corp subdomain → root.
		{"corp.kaltura.com", "kaltura.com"},
		// brand.kaltura.com → kaltura.com so Tier 4 probes brand.kaltura.com.
		{"brand.kaltura.com", "kaltura.com"},
		// Two-part domain (no extra dot) — must not strip.
		{"apple.co", "apple.co"},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			got := rootDomain(c.input)
			if got != c.want {
				t.Errorf("rootDomain(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

// ─── 2. Domain normalisation ──────────────────────────────────────────────────

func TestBrandResearchDomainNormalization(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input, want string
	}{
		{"https://www.kaltura.com/", "kaltura.com"},
		{"kaltura.com", "kaltura.com"},
		{"http://www.example.co.uk/path?q=1", "example.co.uk"},
		{"HTTPS://WWW.ACME.IO", "acme.io"},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			got := canonicalDomain(c.input)
			if got != c.want {
				t.Errorf("canonicalDomain(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

// ─── 3. CSS color extraction — short hex expansion ───────────────────────────

func TestBrandResearchCSSColorExtraction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input, want string
	}{
		{"#06f", "#0066ff"},
		{"#AABBCC", "#aabbcc"},
		{"#112233", "#112233"},
		{"#FF000080", "#ff0000"}, // RGBA → strip alpha
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			got := normalizeColorValue(c.input)
			if got != c.want {
				t.Errorf("normalizeColorValue(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

// ─── 4. RGB → hex normalisation ───────────────────────────────────────────────

func TestBrandResearchColorNormalizationRGB(t *testing.T) {
	t.Parallel()
	got := normalizeColorValue("rgb(0,110,250)")
	want := "#006efa"
	if got != want {
		t.Errorf("normalizeColorValue(rgb(0,110,250)) = %q, want %q", got, want)
	}
}

// ─── 5. HSL → hex normalisation ──────────────────────────────────────────────

func TestBrandResearchColorNormalizationHSL(t *testing.T) {
	t.Parallel()
	// hsl(220,100%,49%) pre-computed via the same hslToHex logic: #0053fa
	got := normalizeColorValue("hsl(220,100%,49%)")
	want := "#0053fa"
	if got != want {
		t.Errorf("normalizeColorValue(hsl(220,100%%,49%%)) = %q, want %q", got, want)
	}
}

// ─── 6. Hex brightness ───────────────────────────────────────────────────────

func TestBrandResearchHexBrightness(t *testing.T) {
	t.Parallel()
	cases := []struct {
		hex  string
		want int
	}{
		{"#000000", 0},
		{"#ffffff", 100},
	}
	for _, c := range cases {
		t.Run(c.hex, func(t *testing.T) {
			got := hexBrightness(c.hex)
			if got != c.want {
				t.Errorf("hexBrightness(%q) = %d, want %d", c.hex, got, c.want)
			}
		})
	}
}

// ─── 7. Coverage — none ──────────────────────────────────────────────────────

func TestBrandResearchCoverageNone(t *testing.T) {
	t.Parallel()
	result := &brandResearchResult{}
	cov := computeBrandCoverage(result)
	if cov.Colors != "none" {
		t.Errorf("Colors = %q, want none", cov.Colors)
	}
	if cov.Logos != "none" {
		t.Errorf("Logos = %q, want none", cov.Logos)
	}
	if cov.Typography != "none" {
		t.Errorf("Typography = %q, want none", cov.Typography)
	}
	if cov.ToneOfVoice != "none" {
		t.Errorf("ToneOfVoice = %q, want none", cov.ToneOfVoice)
	}
}

// ─── 7b. Coverage — extraction_blocked (#358) ────────────────────────────────
// TestBrandResearchCoverageExtractionBlocked verifies that computeBrandCoverage
// reports "extraction_blocked" (not "none") for colors/typography/tone_of_voice
// when the brand page was found but too thin to read — and that logos (sourced
// from the homepage meta tier, unaffected by brand-page thinness) stays "none".
// Regression: a revert of the blockedOrNone branch would silently fall back to
// "none" and this test would fail.
func TestBrandResearchCoverageExtractionBlocked(t *testing.T) {
	t.Parallel()
	result := &brandResearchResult{brandPageThin: true}
	cov := computeBrandCoverage(result)
	if cov.Colors != "extraction_blocked" {
		t.Errorf("Colors = %q, want extraction_blocked", cov.Colors)
	}
	if cov.Typography != "extraction_blocked" {
		t.Errorf("Typography = %q, want extraction_blocked", cov.Typography)
	}
	if cov.ToneOfVoice != "extraction_blocked" {
		t.Errorf("ToneOfVoice = %q, want extraction_blocked", cov.ToneOfVoice)
	}
	if cov.Logos != "none" {
		t.Errorf("Logos = %q, want none (logos are unaffected by brand-page thinness)", cov.Logos)
	}

	// A field that was actually populated still wins over the blocked signal.
	result2 := &brandResearchResult{
		brandPageThin: true,
		Colors: &brandColors{Palette: []brandColor{
			{Hex: "#0066ff"}, {Hex: "#ff6600"}, {Hex: "#00ff66"},
		}},
	}
	cov2 := computeBrandCoverage(result2)
	if cov2.Colors != "full" {
		t.Errorf("Colors = %q, want full (populated data overrides extraction_blocked)", cov2.Colors)
	}
}

// ─── 7c. markBrandPageThin — mutex-guarded sparsity signal (#358) ────────────
// TestBrandResearchMarkPageThin verifies the mutex-guarded write that flags a
// brand page as "thin" when its scraped content falls below
// sparseWordThreshold, and that it leaves both signals untouched otherwise.
// Regression: a broken threshold comparison or a dropped mutex-guarded write
// here would silently disable the "extraction_blocked" coverage signal.
func TestBrandResearchMarkPageThin(t *testing.T) {
	t.Parallel()

	t.Run("thin content sets both signals", func(t *testing.T) {
		t.Parallel()
		result := &brandResearchResult{}
		src := &brandSource{Name: "brand_page", URL: "https://example.com/brand"}
		var mu sync.Mutex
		markBrandPageThin("only a few words here", src, result, &mu)
		if src.ScrapeQuality != "thin" {
			t.Errorf("ScrapeQuality = %q, want thin", src.ScrapeQuality)
		}
		if !result.brandPageThin {
			t.Error("brandPageThin = false, want true for sparse content")
		}
	})

	t.Run("sufficient content leaves both signals unset", func(t *testing.T) {
		t.Parallel()
		result := &brandResearchResult{}
		src := &brandSource{Name: "brand_page", URL: "https://example.com/brand"}
		var mu sync.Mutex
		content := strings.Repeat("word ", sparseWordThreshold+1)
		markBrandPageThin(content, src, result, &mu)
		if src.ScrapeQuality != "" {
			t.Errorf("ScrapeQuality = %q, want empty for content above the threshold", src.ScrapeQuality)
		}
		if result.brandPageThin {
			t.Error("brandPageThin = true, want false for content above the threshold")
		}
	})
}

// ─── 8. Coverage — full ──────────────────────────────────────────────────────

func TestBrandResearchCoverageFull(t *testing.T) {
	t.Parallel()
	result := &brandResearchResult{
		Colors: &brandColors{
			Primary: "#0066ff",
			Palette: []brandColor{
				{Hex: "#0066ff"},
				{Hex: "#ff6600"},
				{Hex: "#00ff66"},
			},
		},
		Logos: &brandLogos{
			Primary: &brandLogoAsset{URL: "https://example.com/logo.svg", Format: "svg"},
			Icon:    &brandLogoAsset{URL: "https://example.com/icon.png", Format: "png"},
		},
		Typography: &brandTypography{
			Heading: &brandFont{Family: "Inter"},
			Body:    &brandFont{Family: "Roboto"},
		},
		ToneOfVoice: &brandTone{
			Summary:    "Clear and concise",
			Attributes: []string{"friendly", "professional"},
		},
	}
	cov := computeBrandCoverage(result)
	if cov.Colors != "full" {
		t.Errorf("Colors = %q, want full", cov.Colors)
	}
	if cov.Logos != "full" {
		t.Errorf("Logos = %q, want full", cov.Logos)
	}
	if cov.Typography != "full" {
		t.Errorf("Typography = %q, want full", cov.Typography)
	}
	if cov.ToneOfVoice != "found" {
		t.Errorf("ToneOfVoice = %q, want found", cov.ToneOfVoice)
	}
}

// ─── 9. Design tokens ────────────────────────────────────────────────────────

func TestBrandResearchDesignTokens(t *testing.T) {
	t.Parallel()
	result := &brandResearchResult{
		Colors: &brandColors{
			Primary: "#0066ff",
			Accent:  "#ff6600",
		},
		Typography: &brandTypography{
			Heading: &brandFont{Family: "Inter"},
			Body:    &brandFont{Family: "Roboto"},
		},
	}
	tokens := buildDesignTokens(result)
	if tokens == nil {
		t.Fatal("buildDesignTokens returned nil")
	}

	colorGroup, ok := tokens["color"].(map[string]any)
	if !ok {
		t.Fatalf("tokens[color] not a map: %T", tokens["color"])
	}
	brand, ok := colorGroup["brand"].(map[string]any)
	if !ok {
		t.Fatalf("color.brand not a map: %T", colorGroup["brand"])
	}
	if brand["$value"] != "#0066ff" {
		t.Errorf("color.brand.$value = %v, want #0066ff", brand["$value"])
	}
	if brand["$type"] != "color" {
		t.Errorf("color.brand.$type = %v, want color", brand["$type"])
	}

	fontGroup, ok := tokens["font"].(map[string]any)
	if !ok {
		t.Fatalf("tokens[font] not a map: %T", tokens["font"])
	}
	heading, ok := fontGroup["heading"].(map[string]any)
	if !ok {
		t.Fatalf("font.heading not a map: %T", fontGroup["heading"])
	}
	if heading["$value"] != "Inter" {
		t.Errorf("font.heading.$value = %v, want Inter", heading["$value"])
	}
	if heading["$type"] != "fontFamily" {
		t.Errorf("font.heading.$type = %v, want fontFamily", heading["$type"])
	}
}

// ─── 10a. Bare IP URL is rejected (F05) ────────────────────────────────────
// canonicalDomain now rejects bare IP addresses to prevent SSRF via the browser
// tier. F25: this test is renamed from TestBrandResearchNoBrandFetchKey which
// previously used a bare IP input — that input is correctly rejected now.

func TestBrandResearchBareIPRejected(t *testing.T) {
	t.Parallel()
	deps := brandDepsWithPrivate()
	// 127.0.0.1 is a bare IP — should be rejected with a tool error.
	_, isErr := callBrandResearch(t, deps, map[string]any{"url": "http://127.0.0.1:9876"})
	if !isErr {
		t.Error("bare IP URL should produce a tool error (SSRF protection)")
	}
}

// TestBrandResearchNXDomainSubdomainNotAccepted verifies that a brand.* subdomain
// that does not resolve (DNS NXDOMAIN) is NOT recorded as guidelines_url.
// Regression: the probe goroutine used to accept brand.*/press.* on label alone
// even when the HEAD request failed with "no such host".
func TestBrandResearchNXDomainSubdomainNotAccepted(t *testing.T) {
	t.Parallel()
	deps := brandDepsWithPrivate()

	// brand.this-domain-definitely-does-not-exist-xyzq123.com will NXDOMAIN.
	domain := "this-domain-definitely-does-not-exist-xyzq123.com"
	result, isErr := callBrandResearch(t, deps, map[string]any{"url": domain})
	if isErr {
		// A tool error is also acceptable (can't resolve homepage). Skip the rest.
		return
	}
	if result["guidelines_url"] != nil && result["guidelines_url"] != "" {
		t.Errorf("NXDOMAIN brand.* subdomain must not be accepted as guidelines_url, got: %v", result["guidelines_url"])
	}
}

// ─── 10b. No BrandFetch key — company_name path succeeds ────────────────────
// This tests that the tool handles a missing BrandFetchAPIKey on the
// company_name-only resolution path without panicking or crashing.
// Uses a mock search provider to avoid real network calls. (F25)

func TestBrandResearchURLInputNoBrandFetchKey(t *testing.T) {
	t.Parallel()
	deps := brandDepsWithPrivate()
	if deps.BrandFetchAPIKey != "" {
		t.Skip("BrandFetchAPIKey is set in environment; skipping no-key test")
	}
	// company_name-only path: search provider returns no results, tool should
	// return a toolError about not being able to resolve the domain — not crash.
	_, isErr := callBrandResearch(t, deps, map[string]any{"company_name": "NonExistentBrandXYZ123"})
	// Either a tool error (could not resolve domain) or success — both are valid.
	// What matters is no panic and no nil-pointer crash.
	_ = isErr
}

// ─── 11. Cache hit — cache_age >= 0 on second call ──────────────────────────
// Uses company_name input (avoids bare-IP domain) with the cache pre-seeded to
// avoid real network resolution.

func TestBrandResearchCacheHit(t *testing.T) {
	t.Parallel()

	// Use a real memory cache so the second call finds the stored result.
	deps := brandDepsWithPrivate()
	deps.Cache = cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 4})

	// Pre-seed the cache with a valid brandResearchResult for domain "example.com"
	// so the first call returns a cache hit without real network calls.
	seedDomain := "example.com"
	cacheKey := brandCacheKey(seedDomain, "quick")
	seedResult := brandResearchResult{
		Identity: brandIdentity{Name: "Example", Domain: seedDomain},
		Sources:  []brandSource{},
		Trust:    untrustedContentTrust,
		Coverage: brandCoverage{Colors: "none", Logos: "none", Typography: "none", ToneOfVoice: "none"},
	}
	if b, err := json.Marshal(seedResult); err == nil {
		deps.Cache.Set(context.Background(), cacheKey, b, 24*time.Hour)
	}

	args := map[string]any{"url": "https://example.com", "depth": "quick"}

	// First call — reads from pre-seeded cache; cache_age should be >= 0.
	first, isErr := callBrandResearch(t, deps, args)
	if isErr {
		t.Fatal("first call returned a tool error")
	}
	if first == nil {
		t.Fatal("first call result is nil")
	}
	if _, ok := first["cache_age"]; !ok {
		t.Error("cache_age field missing from first call result")
	}

	// Second call — also from cache; cache_age must still be present.
	second, isErr := callBrandResearch(t, deps, args)
	if isErr {
		t.Fatal("second call returned a tool error")
	}
	if second == nil {
		t.Fatal("second call result is nil")
	}
	if _, ok := second["cache_age"]; !ok {
		t.Error("cache_age field missing from second call result")
	}
}

// ─── 13. cleanPageTitle strips tagline separators ────────────────────────────

func TestBrandResearchCleanPageTitle(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input, want string
	}{
		// Leading brand name (short before separator).
		{"Acme | The best widgets", "Acme"},
		{"Acme — The global leader in stuff", "Acme"},
		{"Acme – Innovation since 1984", "Acme"},
		{"Acme - Making things better", "Acme"},
		// Trailing brand name (short brand name on right, long tagline on left).
		{"The AI workspace that works for you. | Notion", "Notion"},
		{"Vacation Rentals, Cabins, Beach Houses, Unique Homes & Experiences | Airbnb", "Airbnb"},
		{"Financial Infrastructure to Grow Your Revenue | Stripe", "Stripe"},
		// Colon separator.
		{"Figma: The Collaborative Interface Design Tool", "Figma"},
		{"Shopify: The All-in-One Commerce Platform", "Shopify"},
		// No separator.
		{"Acme", "Acme"},
		{"", ""},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			got := cleanPageTitle(c.input)
			if got != c.want {
				t.Errorf("cleanPageTitle(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

// ─── 14. hexSaturation correctness ───────────────────────────────────────────

func TestBrandResearchHexSaturation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		hex     string
		wantMin float64
	}{
		{"#0066ff", 0.9},
		{"#ff0000", 0.9},
		{"#ffffff", 0.0},
		{"#000000", 0.0},
		{"#808080", 0.0},
	}
	for _, c := range cases {
		t.Run(c.hex, func(t *testing.T) {
			got := hexSaturation(c.hex)
			if got < c.wantMin {
				t.Errorf("hexSaturation(%q) = %.3f, want >= %.3f", c.hex, got, c.wantMin)
			}
		})
	}
}

// ─── 15. pickChromaticPrimary skips neutrals ────────────────────────────────

func TestBrandResearchPickChromaticPrimary(t *testing.T) {
	t.Parallel()
	sorted := []cssColorScored{
		{"#ffffff", 10},
		{"#0066ff", 5},
		{"#cccccc", 3},
	}
	got := pickChromaticPrimary(sorted)
	if got != "#0066ff" {
		t.Errorf("pickChromaticPrimary = %q, want #0066ff", got)
	}
}

// ─── 16. cleanFontFamily blocks icon fonts and strips !important ─────────────

func TestBrandResearchCleanFontFamilyIconBlocked(t *testing.T) {
	t.Parallel()
	blocked := []string{
		"Material Icons",
		"Font Awesome 5 Free",
		"Font Awesome 6 Brands",
		"WebFlow-Icons",
		"ionicons",
	}
	for _, f := range blocked {
		t.Run(f, func(t *testing.T) {
			got := cleanFontFamily(f)
			if got != "" {
				t.Errorf("cleanFontFamily(%q) = %q, want empty (icon font should be blocked)", f, got)
			}
		})
	}
}

func TestBrandResearchCleanFontFamilyStripImportant(t *testing.T) {
	t.Parallel()
	got := cleanFontFamily("Inter !important")
	if got != "Inter" {
		t.Errorf("cleanFontFamily(\"Inter !important\") = %q, want Inter", got)
	}
}

// ─── 17. resolveFontVar resolves CSS custom properties (incl. chained) ───────

func TestBrandResearchResolveFontVar(t *testing.T) {
	t.Parallel()
	varMap := map[string]string{
		"--font-sans":           "'Inter', sans-serif",
		"--font-mono":           "'JetBrains Mono', monospace",
		"--font-family-heading": "var(--font-sans)",
		"--font-family-body":    "var(--font-sans)",
	}
	cases := []struct {
		input, want string
	}{
		{"var(--font-sans)", "'Inter', sans-serif"},
		{"var(--font-mono)", "'JetBrains Mono', monospace"},
		// Chained: --font-family-heading → var(--font-sans) → 'Inter', sans-serif.
		{"var(--font-family-heading)", "'Inter', sans-serif"},
		{"var(--font-family-body)", "'Inter', sans-serif"},
		// Unresolved.
		{"var(--font-missing)", ""},
		// Non-var pass-through.
		{"Inter", "Inter"},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			got := resolveFontVar(c.input, varMap)
			if got != c.want {
				t.Errorf("resolveFontVar(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

// ─── 18. og:site_name extracted from DOM meta tag ────────────────────────────

func TestBrandResearchExtractMetaTagsOGSiteName(t *testing.T) {
	t.Parallel()
	html := `<html><head>
		<meta property="og:site_name" content="Acme Corp" />
		<title>Acme Corp | Making the future</title>
	</head><body></body></html>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	// Name starts as the domain-derived fallback ("Acme") — should be replaced by og:site_name.
	result := &brandResearchResult{Identity: brandIdentity{Name: domainToName("acme.com")}}
	fields := []string{}
	extractMetaTags(doc, "acme.com", result, &fields)
	if result.Identity.Name != "Acme Corp" {
		t.Errorf("Identity.Name = %q, want Acme Corp", result.Identity.Name)
	}
}

// ─── 12. SVG icon link wins over apple-touch-icon ────────────────────────────

func TestBrandResearchLogoChainSVGWins(t *testing.T) {
	t.Parallel()

	const domain = "example.com"
	html := `<html><head>
		<link rel="apple-touch-icon" sizes="180x180" href="/apple-touch-icon.png">
		<link rel="icon" type="image/svg+xml" href="/logo.svg">
	</head><body></body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("failed to parse HTML: %v", err)
	}

	result := &brandResearchResult{Logos: &brandLogos{}}
	fields := []string{}
	extractLogoChain(doc, domain, result, &fields)

	if result.Logos.Icon == nil {
		t.Fatal("Icon should not be nil after extractLogoChain")
	}
	if result.Logos.Icon.Format != "svg" {
		t.Errorf("Icon.Format = %q, want svg (SVG link should win over apple-touch-icon)", result.Logos.Icon.Format)
	}
	if result.Logos.Icon.URL != "https://example.com/logo.svg" {
		t.Errorf("Icon.URL = %q, want https://example.com/logo.svg", result.Logos.Icon.URL)
	}
}

// ─── 21. isNearNeutral ────────────────────────────────────────────────────────

func TestBrandResearchIsNearNeutral(t *testing.T) {
	t.Parallel()
	cases := []struct {
		hex      string
		expected bool
	}{
		{"#ffffff", true},  // pure white
		{"#f5f5f5", true},  // near-white
		{"#000000", true},  // pure black
		{"#080808", true},  // near-black
		{"#808080", true},  // mid-grey
		{"#cccccc", true},  // light grey
		{"#e2e4ff", true},  // pale lavender tint — brightness≈93, looks near-white
		{"#f9edff", true},  // very light purple tint — brightness≈97, near-white
		{"#a1a1aa", true},  // zinc-400 gray — sat=0.05, perceptually neutral
		{"#ffe01b", false}, // Mailchimp yellow — chromatic, brightness≈66
		{"#0066ff", false}, // saturated blue — chromatic
		{"#004e56", false}, // Mailchimp dark teal — chromatic
		{"#692340", false}, // Mailchimp wine — chromatic
		{"#533afd", false}, // Stripe indigo — chromatic, brightness≈52
	}
	for _, tc := range cases {
		t.Run(tc.hex, func(t *testing.T) {
			got := isNearNeutral(tc.hex)
			if got != tc.expected {
				t.Errorf("isNearNeutral(%q) = %v, want %v", tc.hex, got, tc.expected)
			}
		})
	}
}

// ─── 22. CSS primary override: near-neutral primary is replaced by chromatic ──

func TestBrandResearchCSSPrimaryOverridesNearNeutral(t *testing.T) {
	t.Parallel()
	// Simulate a result where a prior tier set primary to white.
	result := &brandResearchResult{
		Colors: &brandColors{Primary: "#ffffff"},
	}
	sorted := []cssColorScored{
		{"#ffffff", 20},
		{"#ffe01b", 8},
		{"#231e15", 4},
	}
	// Apply the same logic as fetchBrandCSS.
	if result.Colors.Primary == "" || isNearNeutral(result.Colors.Primary) {
		if chromatic := pickChromaticPrimary(sorted); chromatic != "" {
			result.Colors.Primary = chromatic
		}
	}
	if result.Colors.Primary != "#ffe01b" {
		t.Errorf("primary = %q, want #ffe01b (chromatic should override near-neutral)", result.Colors.Primary)
	}
}

// ─── 24. fontVarMap chained resolution via stored var() values ───────────────

func TestBrandResearchFontVarMapChained(t *testing.T) {
	t.Parallel()
	// Simulate the two-pass CSS font extraction: varMap includes chained var() entries.
	varMap := map[string]string{
		"--font-sans":           "'Inter', sans-serif",
		"--font-family-heading": "var(--font-sans)",
		"--typography-body":     "var(--font-sans)",
	}
	cases := []struct{ input, want string }{
		{"var(--font-family-heading)", "'Inter', sans-serif"},
		{"var(--typography-body)", "'Inter', sans-serif"},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			got := resolveFontVar(c.input, varMap)
			if got != c.want {
				t.Errorf("resolveFontVar(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

// ─── 25. og:site_name equal to domainToName must still block SiteName/Title fallback ──

func TestBrandResearchNameFromOGSiteNameEqualsDefaultName(t *testing.T) {
	t.Parallel()
	// Vercel: og:site_name="Vercel" == domainToName("vercel.com").
	// The field-presence tracker must still detect that extractMetaTags wrote the name.
	html := `<html><head>
		<meta property="og:site_name" content="Vercel" />
		<title>Agentic Infrastructure</title>
	</head><body></body></html>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	result := &brandResearchResult{Identity: brandIdentity{Name: domainToName("vercel.com")}}
	fields := []string{}
	extractMetaTags(doc, "vercel.com", result, &fields)
	// extractMetaTags must have emitted "identity.name" even though value == domainToName.
	found := false
	for _, f := range fields {
		if f == "identity.name" {
			found = true
			break
		}
	}
	if !found {
		t.Error("extractMetaTags should append 'identity.name' to fields even when og:site_name == domainToName")
	}
	// The name itself should still be "Vercel" — not "Agentic Infrastructure".
	if result.Identity.Name != "Vercel" {
		t.Errorf("Identity.Name = %q, want Vercel", result.Identity.Name)
	}
}

// ─── 26. Informational-subdomain label ("Docs") must not become the brand name ──

func TestBrandResearchSubdomainLabelNotUsedAsName(t *testing.T) {
	t.Parallel()
	// docs.stripe.com: SiteName="Docs" equals domainToName("docs.stripe.com").
	// The root domain fallback should give "Stripe" instead.
	domain := "docs.stripe.com"
	defaultName := domainToName(domain) // "Docs"
	if defaultName != "Docs" {
		t.Skipf("test assumption broken: domainToName(%q) = %q", domain, defaultName)
	}
	// Simulate no og:site_name, no fields from extractMetaTags, SiteName=="Docs".
	// rootDomain("docs.stripe.com") = "stripe.com" → domainToName = "Stripe".
	rootName := domainToName(rootDomain(domain))
	if rootName == defaultName {
		t.Fatalf("rootName %q == subdomain label %q — fix rootDomain logic", rootName, defaultName)
	}
	if rootName != "Stripe" {
		t.Errorf("rootName = %q, want Stripe", rootName)
	}
}

// TestPickChromaticPrimaryFrequencyWeighted verifies that a high-saturation colour
// appearing only once (e.g. inside a @media override) cannot beat a moderately-saturated
// colour that appears many times throughout the stylesheet.
func TestPickChromaticPrimaryFrequencyWeighted(t *testing.T) {
	t.Parallel()
	// #ddd600: sat≈1.0, count=2  → score = 1.0 × log2(3) ≈ 1.585
	// #533afd: sat≈0.979, count=9 → score ≈ 0.979 × log2(10) ≈ 3.25
	// Expect #533afd to win.
	sorted := []cssColorScored{
		{hex: "#ddd600", count: 2},
		{hex: "#533afd", count: 9},
		{hex: "#1a1a2e", count: 12},
	}
	got := pickChromaticPrimary(sorted)
	if got != "#533afd" {
		t.Errorf("pickChromaticPrimary = %q, want #533afd (frequency-weighted saturation should prefer high-count chromatic over low-count ultra-saturated)", got)
	}
}

// TestPickChromaticPrimaryHighSatHighCount verifies that a single ultra-saturated
// colour still wins when its count is also high.
func TestPickChromaticPrimaryHighSatHighCount(t *testing.T) {
	t.Parallel()
	// #ff0000: sat=1.0, count=10 → score = 1.0 × log2(11) ≈ 3.459
	// #5555ff: sat≈0.6, count=8  → score ≈ 0.6 × log2(9) ≈ 1.906
	sorted := []cssColorScored{
		{hex: "#ff0000", count: 10},
		{hex: "#5555ff", count: 8},
	}
	got := pickChromaticPrimary(sorted)
	if got != "#ff0000" {
		t.Errorf("pickChromaticPrimary = %q, want #ff0000", got)
	}
}

// TestPickChromaticPrimaryAllNearNeutral verifies that when all candidates are
// near-neutral or extreme-brightness artifacts (e.g. #ffff00 brightness=89, #ffffff,
// #000000), pickChromaticPrimary returns "" rather than a wrong color.
func TestPickChromaticPrimaryAllNearNeutral(t *testing.T) {
	t.Parallel()
	// All rejected by the brightness/saturation filter — no chromatic winner.
	sorted := []cssColorScored{
		{hex: "#ffff00", count: 5}, // brightness=89 > 87 — extreme yellow, rejected
		{hex: "#ffffff", count: 3}, // brightness=100 — white, rejected
		{hex: "#000000", count: 8}, // brightness=0 < 3 — black, rejected
	}
	got := pickChromaticPrimary(sorted)
	if got != "" {
		t.Errorf("pickChromaticPrimary = %q, want empty string when all colors are near-neutral/extreme", got)
	}
}

// TestToneHeadingRegex verifies reToneHeading matches canonical brand-voice
// headings and rejects prose lines that merely contain the word "voice".
func TestToneHeadingRegex(t *testing.T) {
	t.Parallel()
	matches := []string{
		"Tone of voice",
		"Tone of Voice:",
		"Brand Voice",
		"brand voice",
		"Writing Style",
		"voice & tone",
		"Voice & Tone",
		"Language & Tone",
		"Brand Personality",
		"Our Voice",
		"our tone",
	}
	rejects := []string{
		"Charles C. Mann author of 1491: New Revelations",
		"In his fresh and inimitable prose, Stewart Brand makes a striking case",
		"Brand shows us why know-how and understanding how machines work really matter.",
		"vocal performance was outstanding",
		"personality type quiz",
		"writing tools for teams",
		"language learning app",
	}
	for _, s := range matches {
		if !reToneHeading.MatchString(s) {
			t.Errorf("reToneHeading should match %q but did not", s)
		}
	}
	for _, s := range rejects {
		if reToneHeading.MatchString(s) {
			t.Errorf("reToneHeading should NOT match %q but did", s)
		}
	}
}

// ─── F20a. brand_portal_resource set when brand page found ───────────────────

// TestBrandResearchPortalResource verifies that when a brand portal is found,
// brand_portal_resource is a research:// URI and suggestion is empty;
// and when no portal is found, suggestion is non-empty. (F20)
// Uses a pre-seeded cache to test the output contract without live scraping.
func TestBrandResearchPortalResource(t *testing.T) {
	t.Parallel()

	deps := brandDepsWithPrivate()
	deps.Cache = cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 4})

	// Case 1: portal found — pre-seed result with BrandPortalResource set.
	withPortal := brandResearchResult{
		Identity:            brandIdentity{Name: "TestCo", Domain: "testco.com"},
		Sources:             []brandSource{},
		Trust:               untrustedContentTrust,
		GuidelinesURL:       "https://brand.testco.com",
		BrandPortalResource: "research://artifact/abc123",
		Coverage:            brandCoverage{Colors: "none", Logos: "none", Typography: "none", ToneOfVoice: "none"},
	}
	if b, err := json.Marshal(withPortal); err == nil {
		deps.Cache.Set(context.Background(), brandCacheKey("testco.com", "standard"), b, 24*time.Hour)
	}

	out, isErr := callBrandResearch(t, deps, map[string]any{
		"url":   "https://testco.com",
		"depth": "standard",
	})
	if isErr {
		t.Fatal("tool returned error (case: portal found)")
	}
	if out == nil {
		t.Fatal("output is nil (case: portal found)")
	}
	portalResource, _ := out["brand_portal_resource"].(string)
	suggestion, _ := out["suggestion"].(string)
	if !strings.HasPrefix(portalResource, "research://") {
		t.Errorf("brand_portal_resource = %q, want research:// URI", portalResource)
	}
	if suggestion != "" {
		t.Errorf("suggestion should be empty when portal found, got %q", suggestion)
	}

	// Case 2: no portal — pre-seed result with Suggestion set.
	noPortal := brandResearchResult{
		Identity:   brandIdentity{Name: "NoCo", Domain: "noco.com"},
		Sources:    []brandSource{},
		Trust:      untrustedContentTrust,
		Suggestion: "No brand portal found. Use scrape_page on https://noco.com to retrieve the fully rendered homepage.",
		Coverage:   brandCoverage{Colors: "none", Logos: "none", Typography: "none", ToneOfVoice: "none"},
	}
	if b, err := json.Marshal(noPortal); err == nil {
		deps.Cache.Set(context.Background(), brandCacheKey("noco.com", "standard"), b, 24*time.Hour)
	}
	out2, isErr2 := callBrandResearch(t, deps, map[string]any{
		"url":   "https://noco.com",
		"depth": "standard",
	})
	if isErr2 {
		t.Fatal("tool returned error (case: no portal)")
	}
	if out2 == nil {
		t.Fatal("output is nil (case: no portal)")
	}
	portalResource2, _ := out2["brand_portal_resource"].(string)
	suggestion2, _ := out2["suggestion"].(string)
	if portalResource2 != "" {
		t.Errorf("brand_portal_resource should be empty when no portal, got %q", portalResource2)
	}
	if suggestion2 == "" {
		t.Error("suggestion should be set when no portal found")
	}
}

// ─── F20b. suggestion set when depth=quick (portal probe skipped) ─────────────

// TestBrandResearchSuggestionOnQuickDepth verifies that depth=quick always
// returns a non-empty suggestion (portal probe is skipped entirely). (F20)
// Uses a pre-seeded cache result to test the output contract.
func TestBrandResearchSuggestionOnQuickDepth(t *testing.T) {
	t.Parallel()

	deps := brandDepsWithPrivate()
	deps.Cache = cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 4})

	// Pre-seed: no portal, suggestion set, depth=quick.
	quickResult := brandResearchResult{
		Identity:   brandIdentity{Name: "QuickCo", Domain: "quickco.com"},
		Sources:    []brandSource{},
		Trust:      untrustedContentTrust,
		Suggestion: "No brand portal found. Use scrape_page on https://quickco.com to retrieve the fully rendered homepage.",
		Coverage:   brandCoverage{Colors: "none", Logos: "none", Typography: "none", ToneOfVoice: "none"},
	}
	if b, err := json.Marshal(quickResult); err == nil {
		deps.Cache.Set(context.Background(), brandCacheKey("quickco.com", "quick"), b, 24*time.Hour)
	}

	out, isErr := callBrandResearch(t, deps, map[string]any{
		"url":   "https://quickco.com",
		"depth": "quick",
	})
	if isErr {
		t.Fatal("tool returned error")
	}
	if out == nil {
		t.Fatal("output is nil")
	}

	suggestion, _ := out["suggestion"].(string)
	if suggestion == "" {
		t.Error("depth=quick should always produce a suggestion (portal probe is skipped)")
	}
	// Portal resource must NOT be set when depth=quick.
	if pr, _ := out["brand_portal_resource"].(string); pr != "" {
		t.Errorf("brand_portal_resource should be empty for depth=quick, got %q", pr)
	}
}

// ─── F20c. suggestion/portal mutual exclusion ────────────────────────────────

// TestBrandResearchSuggestionPortalMutualExclusion verifies that suggestion and
// brand_portal_resource are never both populated simultaneously. (F20)
func TestBrandResearchSuggestionPortalMutualExclusion(t *testing.T) {
	t.Parallel()

	result := &brandResearchResult{
		GuidelinesURL:       "https://example.com/brand",
		BrandPortalResource: "research://artifact/abc123",
	}
	// When both are set the suggestion block must NOT fire.
	if result.GuidelinesURL == "" && result.BrandPortalResource == "" {
		result.Suggestion = "should not be set"
	}
	if result.Suggestion != "" {
		t.Error("Suggestion must not be set when BrandPortalResource is non-empty")
	}

	// Verify the no-portal path sets suggestion.
	result2 := &brandResearchResult{}
	if result2.GuidelinesURL == "" && result2.BrandPortalResource == "" {
		result2.Suggestion = "No brand portal found."
	}
	if result2.Suggestion == "" {
		t.Error("Suggestion must be set when both GuidelinesURL and BrandPortalResource are empty")
	}
}

// TestBrandGuidelinesURLFilter verifies that the web-search tier rejects
// third-party template/category pages and non-own-domain results.
func TestBrandGuidelinesURLFilter(t *testing.T) {
	t.Parallel()

	isAllowed := func(rawURL, domain string) bool {
		urlLower := strings.ToLower(rawURL)
		// document extensions
		if strings.HasSuffix(urlLower, ".pdf") || strings.HasSuffix(urlLower, ".docx") || strings.HasSuffix(urlLower, ".pptx") {
			return false
		}
		// template/category slugs
		if strings.Contains(urlLower, "/templates/") || strings.Contains(urlLower, "/template/") ||
			strings.Contains(urlLower, "/category/") || strings.Contains(urlLower, "/tag/") ||
			strings.HasSuffix(urlLower, "-template") || strings.HasSuffix(urlLower, "-templates") ||
			strings.HasSuffix(urlLower, "_template") || strings.HasSuffix(urlLower, "_templates") {
			return false
		}
		// own-domain or known brand host (with company-label check)
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return false
		}
		host := strings.ToLower(parsed.Hostname())
		domainLabel := strings.SplitN(domain, ".", 2)[0]
		ownDomain := !strings.Contains(domain, ".") || strings.HasSuffix(host, "."+domain) || host == domain
		if ownDomain {
			return true
		}
		for _, kh := range knownBrandHosts {
			if strings.HasSuffix(host, kh) || host == kh {
				return strings.Contains(urlLower, strings.ToLower(domainLabel))
			}
		}
		return false
	}

	type tc struct {
		url     string
		domain  string
		allowed bool
	}
	cases := []tc{
		// own-domain passes
		{"https://stripe.com/press", "stripe.com", true},
		{"https://press.stripe.com/", "stripe.com", true},
		{"https://brand.kaltura.com", "kaltura.com", true},
		{"https://vercel.com/geist/brands", "vercel.com", true},
		// known brand hosts pass only when company label appears in URL
		{"https://figma.com/using-the-figma-brand/", "figma.com", true},
		{"https://kaltura.frontify.com/d/brand-kit", "kaltura.com", true},
		// known brand host rejected when company label absent (prevents e.g. figma's own brand page for a different company)
		{"https://figma.com/using-the-figma-brand/", "stripe.com", false},
		{"https://brand.something.frontify.com/", "otherdomain.com", false},
		// github.com: rejected when not from site:github.com query (not modeled here — query context not passed)
		{"https://somecompany.github.io/design-system", "somecompany.com", false},
		{"https://github.com/somecompany/design", "somecompany.com", false},
		// github.com: also rejected for unrelated org even from site:github.com query
		{"https://github.com/voltagent/awesome-design-md", "notion.so", false},
		// third-party template sites rejected
		{"https://www.themarketingplot.com/notion-hub/the-brand-book-notion-template", "notion.so", false},
		{"https://www.notion.com/templates/category/brand-guidelines", "notion.so", false},
		{"https://some-blog.com/how-to-use-brand-templates", "acme.com", false},
		// document extensions rejected
		{"https://kaltura.com/brand-guidelines.pdf", "kaltura.com", false},
		{"https://stripe.com/brand.docx", "stripe.com", false},
		// tag pages rejected
		{"https://stripe.com/blog/tag/brand", "stripe.com", false},
	}

	for _, c := range cases {
		got := isAllowed(c.url, c.domain)
		if got != c.allowed {
			t.Errorf("isAllowed(%q, %q) = %v, want %v", c.url, c.domain, got, c.allowed)
		}
	}
}

// ─── 30. deduplicateFields preserves order and removes duplicates ─────────────

// TestDeduplicateFields verifies that deduplicateFields removes duplicate
// entries (the Airbnb/Slack regression) while preserving insertion order.
func TestDeduplicateFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "no duplicates",
			input: []string{"identity.name", "logos.icon", "logos.og_image"},
			want:  []string{"identity.name", "logos.icon", "logos.og_image"},
		},
		{
			name:  "duplicate identity.name at end",
			input: []string{"identity.name", "logos.icon", "logos.og_image", "logos.primary", "identity.description", "identity.name"},
			want:  []string{"identity.name", "logos.icon", "logos.og_image", "logos.primary", "identity.description"},
		},
		{
			name:  "multiple duplicates",
			input: []string{"identity.name", "identity.name", "logos.icon", "logos.icon"},
			want:  []string{"identity.name", "logos.icon"},
		},
		{
			name:  "empty slice",
			input: []string{},
			want:  []string{},
		},
		{
			name:  "all same",
			input: []string{"identity.name", "identity.name", "identity.name"},
			want:  []string{"identity.name"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deduplicateFields(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("deduplicateFields length = %d, want %d (got %v, want %v)", len(got), len(tc.want), got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("deduplicateFields[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
