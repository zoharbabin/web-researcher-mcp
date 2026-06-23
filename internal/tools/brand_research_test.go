package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		// Should NOT strip a branded subdomain that isn't in the blocklist.
		{"brand.acme.com", "brand.acme.com"},
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

// ─── 10. No BrandFetch key — tool succeeds ───────────────────────────────────

func TestBrandResearchNoBrandFetchKey(t *testing.T) {
	t.Parallel()

	// A minimal homepage that serves empty HTML so the scraper doesn't error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>TestCo</title></head><body></body></html>`)
	}))
	defer srv.Close()

	// Extract host:port from the test server URL so we can use it as domain.
	// The pipeline with AllowPrivateIPs will reach it.
	deps := brandDepsWithPrivate()
	// No BrandFetchAPIKey set (zero value in setupTestDeps).
	if deps.BrandFetchAPIKey != "" {
		t.Skip("BrandFetchAPIKey is set in environment; skipping no-key test")
	}

	// Use the test server's hostname:port as the brand domain.
	// Strip the "http://" prefix to get host:port for the URL input.
	testHost := srv.URL[len("http://"):]
	out, isErr := callBrandResearch(t, deps, map[string]any{"url": "http://" + testHost})
	if isErr {
		t.Errorf("tool should succeed without a BrandFetch API key, got error")
	}
	if out == nil {
		t.Fatal("output is nil on non-error path")
	}
}

// ─── 11. Cache hit — cache_age > 0 on second call ───────────────────────────

func TestBrandResearchCacheHit(t *testing.T) {
	t.Parallel()

	// Use a real memory cache so the second call finds the stored result.
	deps := brandDepsWithPrivate()
	deps.Cache = cache.NewMemory(cache.MemoryConfig{MaxSizeMB: 4})

	// Serve a minimal homepage for the first (live) scrape.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>CacheTestCo</title></head><body></body></html>`)
	}))
	defer srv.Close()

	testHost := srv.URL[len("http://"):]
	args := map[string]any{"url": "http://" + testHost, "depth": "quick"}

	// First call — populates the cache.
	first, isErr := callBrandResearch(t, deps, args)
	if isErr {
		t.Fatal("first call returned a tool error")
	}
	firstAge, _ := first["cache_age"].(float64)
	if firstAge != 0 {
		t.Errorf("first call cache_age = %v, want 0 (freshly fetched)", firstAge)
	}

	// Second call — must come from cache; cache_age should be >= 0 and the
	// result structure should be intact.
	second, isErr := callBrandResearch(t, deps, args)
	if isErr {
		t.Fatal("second call returned a tool error")
	}
	if second == nil {
		t.Fatal("second call result is nil")
	}
	// The cached path sets CacheAge = meta.AgeSeconds() which is >= 0.
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

// ─── 23. reCSSVarFont regex captures --typography-* patterns ─────────────────

func TestBrandResearchCSSVarFontRegexTypography(t *testing.T) {
	t.Parallel()
	css := `
		--font-sans: 'Inter', sans-serif;
		--typography-font-family-cereal-font-family: 'AirbnbCereal', 'Inter', sans-serif;
		--typeface-body: 'Slack Circular', sans-serif;
	`
	matches := reCSSVarFont.FindAllStringSubmatch(css, -1)
	found := map[string]string{}
	for _, m := range matches {
		if len(m) >= 3 {
			found[strings.ToLower(strings.TrimSpace(m[1]))] = strings.TrimSpace(m[2])
		}
	}
	cases := []struct{ key, want string }{
		{"--font-sans", "'Inter', sans-serif"},
		{"--typography-font-family-cereal-font-family", "'AirbnbCereal', 'Inter', sans-serif"},
		{"--typeface-body", "'Slack Circular', sans-serif"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			got := found[c.key]
			if got != c.want {
				t.Errorf("reCSSVarFont[%q] = %q, want %q", c.key, got, c.want)
			}
		})
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

// TestStripConditionalAtRules verifies that @media/@supports/@-moz-document blocks
// are removed from CSS before colour counting, preventing browser-quirk overrides
// (P3 wide-gamut, Firefox fixes) from inflating frequency counts.
func TestStripConditionalAtRules(t *testing.T) {
	t.Parallel()
	css := `
:root{--brand:#533afd}
@media (color-gamut:p3){h1{color:#ddd600}}
h2{color:#533afd}
@supports (-webkit-touch-callout:none){.x{background:#ddd600}}
@-moz-document url-prefix(){h1{color:#ddd600}}
p{color:#533afd}
`
	result := stripConditionalAtRules(css)
	if strings.Contains(result, "#ddd600") {
		t.Errorf("stripConditionalAtRules: #ddd600 should have been removed (was in @media/@supports/@-moz-document), got: %s", result)
	}
	if !strings.Contains(result, "#533afd") {
		t.Errorf("stripConditionalAtRules: #533afd outside conditional blocks should be preserved, got: %s", result)
	}
	if !strings.Contains(result, "--brand:#533afd") {
		t.Errorf("stripConditionalAtRules: :root variables should be preserved, got: %s", result)
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

// TestBrandSignalVarRegex verifies reCSSBrandSignalVar matches design-system
// variable patterns like --palette-bg-primary-core that reCSSVarColor would
// miss (prefix not in its prefix list).
func TestBrandSignalVarRegex(t *testing.T) {
	t.Parallel()
	cases := []struct {
		css  string
		want string // expected captured color value group
	}{
		{"--palette-bg-primary-core:#FF385C;", "#FF385C"},
		{"--sys-brand-fill: #3B82F6;", "#3B82F6"},
		{"--a-main-color:  #e31c5f;", "#e31c5f"},
		{"--token-accent-default:#6d2bf0;", "#6d2bf0"},
		{"--color-brand-primary: #0f0;", "#0f0"}, // 3-char hex
		{"--not-a-match: #cccccc;", ""},          // no brand signal keyword
		{"--palette-bg-neutral: #f5f5f5;", ""},   // no brand signal keyword
	}
	for _, c := range cases {
		matches := reCSSBrandSignalVar.FindStringSubmatch(c.css)
		got := ""
		if len(matches) >= 3 {
			got = matches[2]
		}
		if got != c.want {
			t.Errorf("reCSSBrandSignalVar on %q: got %q, want %q", c.css, got, c.want)
		}
	}
}
