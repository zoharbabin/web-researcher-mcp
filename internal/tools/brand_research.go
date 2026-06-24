package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// brandResearchInput defines the brand_research tool input.
type brandResearchInput struct {
	URL                 string `json:"url,omitempty" jsonschema:"Domain or URL of the company to research. Preferred over company_name when both are supplied."`
	CompanyName         string `json:"company_name,omitempty" jsonschema:"Company name used to resolve the domain when url is omitted. At least one of url or company_name is required."`
	Depth               string `json:"depth,omitempty" jsonschema:"Research depth: quick (API+meta only), standard (default, adds CSS and brand-page probe), full (adds web search for external guidelines and design-system links)."`
	IncludeDesignTokens bool   `json:"include_design_tokens,omitempty" jsonschema:"When true, include a W3C DTCG-formatted design_tokens object alongside the flat color and typography fields."`
	SessionID           string `json:"sessionId,omitempty" jsonschema:"Link this research to a sequential_search session."`
}

// Output structs — all defined in this file.
type brandResearchResult struct {
	Identity      brandIdentity    `json:"identity"`
	Colors        *brandColors     `json:"colors,omitempty"`
	Logos         *brandLogos      `json:"logos,omitempty"`
	Typography    *brandTypography `json:"typography,omitempty"`
	ToneOfVoice   *brandTone       `json:"tone_of_voice,omitempty"`
	Social        *brandSocial     `json:"social,omitempty"`
	Sources       []brandSource    `json:"sources"`
	GuidelinesURL string           `json:"guidelines_url,omitempty"`
	DesignTokens  map[string]any   `json:"design_tokens,omitempty"`
	Coverage      brandCoverage    `json:"coverage"`
	CacheAge      int              `json:"cache_age"`
	Trust         string           `json:"trust"`
}

type brandIdentity struct {
	Name        string         `json:"name"`
	Domain      string         `json:"domain"`
	Tagline     string         `json:"tagline,omitempty"`
	Description string         `json:"description,omitempty"`
	Industry    string         `json:"industry,omitempty"`
	Founded     int            `json:"founded,omitempty"`
	Location    *brandLocation `json:"location,omitempty"`
}

type brandLocation struct {
	City        string `json:"city,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
}

type brandColors struct {
	Primary       string       `json:"primary,omitempty"`
	Secondary     string       `json:"secondary,omitempty"`
	Accent        string       `json:"accent,omitempty"`
	Background    string       `json:"background,omitempty"`
	Surface       string       `json:"surface,omitempty"`
	Text          string       `json:"text,omitempty"`
	TextSecondary string       `json:"text_secondary,omitempty"`
	Palette       []brandColor `json:"palette,omitempty"`
}

type brandColor struct {
	Hex        string `json:"hex"`
	Name       string `json:"name,omitempty"`
	Role       string `json:"role,omitempty"`
	Brightness int    `json:"brightness,omitempty"`
}

type brandLogos struct {
	Primary *brandLogoAsset `json:"primary,omitempty"`
	Dark    *brandLogoAsset `json:"dark,omitempty"`
	Icon    *brandLogoAsset `json:"icon,omitempty"`
	Favicon string          `json:"favicon,omitempty"`
	OGImage string          `json:"og_image,omitempty"`
}

type brandLogoAsset struct {
	URL    string `json:"url"`
	Format string `json:"format"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

type brandTypography struct {
	Heading        *brandFont       `json:"heading,omitempty"`
	Body           *brandFont       `json:"body,omitempty"`
	Mono           *brandFont       `json:"mono,omitempty"`
	GoogleFontsURL string           `json:"google_fonts_url,omitempty"`
	Scale          []typeScaleEntry `json:"scale,omitempty"`
}

type brandFont struct {
	Family   string `json:"family"`
	Weights  []int  `json:"weights,omitempty"`
	Origin   string `json:"origin,omitempty"`
	OriginID string `json:"origin_id,omitempty"`
}

type typeScaleEntry struct {
	Level      string `json:"level"`
	FontSize   string `json:"font_size"`
	Weight     int    `json:"weight,omitempty"`
	LineHeight string `json:"line_height,omitempty"`
}

type brandTone struct {
	Summary     string            `json:"summary,omitempty"`
	Attributes  []string          `json:"attributes,omitempty"`
	DosAndDonts *brandDosAndDonts `json:"dos_and_donts,omitempty"`
}

type brandDosAndDonts struct {
	Dos   []string `json:"dos,omitempty"`
	Donts []string `json:"donts,omitempty"`
}

type brandSocial struct {
	Twitter   string `json:"twitter,omitempty"`
	LinkedIn  string `json:"linkedin,omitempty"`
	GitHub    string `json:"github,omitempty"`
	YouTube   string `json:"youtube,omitempty"`
	Facebook  string `json:"facebook,omitempty"`
	Instagram string `json:"instagram,omitempty"`
}

type brandSource struct {
	Name   string   `json:"name"`
	URL    string   `json:"url,omitempty"`
	Fields []string `json:"fields"`
}

type brandCoverage struct {
	Colors      string `json:"colors"`
	Logos       string `json:"logos"`
	Typography  string `json:"typography"`
	ToneOfVoice string `json:"tone_of_voice"`
}

// CSS extraction regexes — no external CSS parser (Zero-Dependency Mandate).
var (
	// reCSSVarColor matches CSS custom properties whose name starts with a
	// known design-token prefix (e.g. --color-*, --brand-*, --palette-*).
	reCSSVarColor = regexp.MustCompile(`(?i)--(?:color|brand|primary|secondary|accent|bg|background|text|foreground|surface|palette|theme|token|ds)[-\w]*\s*:\s*(#[0-9a-fA-F]{3,8}|rgba?\([^)]+\)|hsla?\([^)]+\))`)
	// reCSSBrandSignalVar matches any CSS custom property whose name contains
	// a brand-signal keyword at any position (covers --palette-bg-primary-core,
	// --sys-brand-fill, etc.).  Full match is used for name extraction; group 1
	// is the value.
	reCSSBrandSignalVar = regexp.MustCompile(`(?i)--([-\w]*(?:primary|brand|accent|main|core)[-\w]*)\s*:\s*(#[0-9a-fA-F]{3,8}|rgba?\([^)]+\)|hsla?\([^)]+\))`)
	reConditionalAtRule = regexp.MustCompile(`@(?:media|supports|-moz-document)\b`)
	reCSSColorProp      = regexp.MustCompile(`(?i)(?:^|[{;])\s*(?:color|background(?:-color)?|fill|stroke)\s*:\s*(#[0-9a-fA-F]{3,8})`)
	reCSSFontFamily     = regexp.MustCompile(`(?i)font-family\s*:\s*([^;}{]+)`)
	reGoogleFonts       = regexp.MustCompile(`fonts\.googleapis\.com/css[^"']*family=([^&"'\s]+)`)
	reAdobeFonts        = regexp.MustCompile(`(?:https?:)?//use\.typekit\.net/([a-z0-9]+)\.js`)
	reHexColor          = regexp.MustCompile(`#[0-9a-fA-F]{6}\b`)
	reToneHeading       = regexp.MustCompile(`(?i)^(?:tone(?:\s+of\s+voice)?|brand\s+voice|writing\s+style|language\s+&\s+tone|voice\s+&\s+tone|brand\s+personality|our\s+voice|our\s+tone)[:\s]*$`)
	reCSSVarFont        = regexp.MustCompile(`(?i)(--(?:font|typography|typeface)[-\w]*)\s*:\s*([^;}{]+)`)
)

func registerBrandResearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "brand_research",
		Description:  "Research a company's complete brand identity — colors, logos, typography, tone of voice, and social handles — from any domain or company name. Combines BrandFetch API (when configured), CSS extraction, structured-data parsing, and brand-page probing. Returns structured JSON ready for AI content generation. Gracefully degrades without BRANDFETCH_API_KEY. Results cached 24h; check cache_age field. For raw HTML extraction use scrape_page; for finding brand mentions use web_search; for social and news coverage use news_search.",
		Annotations:  readOnlyAnnotations(true, true),
		OutputSchema: brandResearchOutputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input brandResearchInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		input.URL = strings.TrimSpace(input.URL)
		input.CompanyName = strings.TrimSpace(input.CompanyName)

		if input.URL == "" && input.CompanyName == "" {
			return toolError("url or company_name is required"), nil, nil
		}

		depth := input.Depth
		if depth == "" {
			depth = "standard"
		}
		if depth != "quick" && depth != "standard" && depth != "full" {
			depth = "standard"
		}

		// Resolve canonical domain.
		domain, companyName, err := resolveBrandDomain(ctx, deps, input.URL, input.CompanyName)
		if err != nil {
			return toolError("could not resolve domain: " + err.Error()), nil, nil
		}

		// Cache lookup.
		cacheKey := brandCacheKey(domain, depth)
		if cached, meta, ok := deps.Cache.GetWithMeta(ctx, cacheKey); ok && meta != nil {
			var res brandResearchResult
			if err := json.Unmarshal(cached, &res); err == nil {
				res.CacheAge = meta.AgeSeconds()
				b, _ := json.Marshal(res)
				deps.Metrics.RecordToolCall("brand_research", time.Since(start), nil, "", false)
				return structuredResult(b), nil, nil
			}
		}

		// Run all tiers concurrently.
		var (
			mu     sync.Mutex
			result = &brandResearchResult{
				Identity: brandIdentity{Name: companyName, Domain: domain},
				Sources:  []brandSource{},
				Trust:    untrustedContentTrust,
			}
			tier1Done bool // BrandFetch quality flag
		)

		var wg sync.WaitGroup

		// Tier 1: BrandFetch API
		if deps.BrandFetchAPIKey != "" {
			wg.Add(1)
			go func() {
				defer wg.Done()
				src, hasQuality := fetchBrandFetch(ctx, deps.BrandFetchAPIKey, domain, depth, result)
				mu.Lock()
				defer mu.Unlock()
				if src != nil {
					result.Sources = append(result.Sources, *src)
				}
				tier1Done = hasQuality
			}()
		}

		// Tier 2: Homepage meta + structured data
		wg.Add(1)
		go func() {
			defer wg.Done()
			src := fetchHomepageMeta(ctx, deps, domain, result, &mu)
			if src != nil {
				mu.Lock()
				result.Sources = append(result.Sources, *src)
				mu.Unlock()
			}
		}()

		wg.Wait()

		// Tier 3: CSS extraction (skip if BrandFetch already set colors+fonts)
		if !tier1Done || result.Colors == nil || result.Typography == nil {
			if src := fetchBrandCSS(ctx, deps, domain, result, &mu); src != nil {
				mu.Lock()
				result.Sources = append(result.Sources, *src)
				mu.Unlock()
			}
		}

		// Tier 4: Brand guidelines page probe (depth >= standard)
		if depth == "standard" || depth == "full" {
			if src := probeBrandPage(ctx, deps, domain, result, &mu, depth); src != nil {
				mu.Lock()
				result.Sources = append(result.Sources, *src)
				mu.Unlock()
			}
		}

		// Tier 5: Web search (depth == full only)
		if depth == "full" {
			if src := searchBrandGuidelines(ctx, deps, companyName, domain, result, &mu); src != nil {
				mu.Lock()
				result.Sources = append(result.Sources, *src)
				mu.Unlock()
			}
		}

		// Compute coverage.
		result.Coverage = computeBrandCoverage(result)

		// W3C DTCG design tokens.
		if input.IncludeDesignTokens {
			result.DesignTokens = buildDesignTokens(result)
		}

		// Cache store.
		if b, err := json.Marshal(result); err == nil {
			deps.Cache.Set(ctx, cacheKey, b, 24*time.Hour)
		}

		b, _ := json.Marshal(result)
		deps.Metrics.RecordToolCall("brand_research", time.Since(start), nil, "", false)
		auditToolCallQuery(ctx, deps, "brand_research", time.Since(start), nil, "", domain, map[string]any{"depth": depth})
		return structuredResult(b), nil, nil
	})
}

// ─── Domain resolution ─────────────────────────────────────────────────────

func resolveBrandDomain(ctx context.Context, deps Dependencies, rawURL, companyName string) (domain, name string, err error) {
	if rawURL != "" {
		domain = canonicalDomain(rawURL)
		if domain == "" {
			return "", "", fmt.Errorf("invalid URL: %q", rawURL)
		}
		if companyName != "" {
			name = companyName
		} else {
			name = domainToName(domain)
		}
		return domain, name, nil
	}
	// company_name only: try BrandFetch search first, then web search.
	if deps.BrandFetchAPIKey != "" {
		if d := searchBrandFetchDomain(ctx, deps.BrandFetchAPIKey, companyName); d != "" {
			return d, companyName, nil
		}
	}
	// Web search fallback.
	results, err := deps.Search.Web(ctx, search.WebSearchParams{Query: `"` + companyName + `" official website`, NumResults: 1})
	if err == nil && len(results) > 0 {
		d := canonicalDomain(results[0].URL)
		if d != "" {
			return rootDomain(d), companyName, nil
		}
	}
	// Last resort: treat company_name as a domain candidate.
	d := canonicalDomain(companyName)
	if d != "" {
		return d, companyName, nil
	}
	return "", "", fmt.Errorf("could not resolve domain for %q", companyName)
}

func canonicalDomain(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	// Reject obvious non-domains (e.g. bare words).
	if !strings.Contains(host, ".") {
		return ""
	}
	return host
}

// rootDomain strips well-known informational subdomains so that a search result
// like "support.apple.com" resolves to the brand root "apple.com".
// It only removes the leading component when that component is a known
// non-brand subdomain; it never touches two-part domains (e.g. "apple.com").
var informationalSubdomains = map[string]bool{
	"support": true, "help": true, "docs": true, "developer": true,
	"developers": true, "community": true, "forum": true, "forums": true,
	"kb": true, "status": true, "blog": true, "news": true, "press": true,
	"newsroom": true, "careers": true, "jobs": true, "shop": true,
	"store": true, "m": true, "mobile": true, "learn": true,
	"education": true, "training": true, "api": true, "cdn": true,
	"static": true, "assets": true, "media": true,
}

func rootDomain(domain string) string {
	parts := strings.SplitN(domain, ".", 2)
	if len(parts) == 2 && informationalSubdomains[parts[0]] && strings.Contains(parts[1], ".") {
		return parts[1]
	}
	return domain
}

func domainToName(domain string) string {
	parts := strings.Split(domain, ".")
	if len(parts) > 0 {
		n := parts[0]
		if len(n) > 0 {
			return strings.ToUpper(n[:1]) + n[1:]
		}
	}
	return domain
}

func searchBrandFetchDomain(ctx context.Context, apiKey, query string) string {
	client := scraper.NewSSRFSafeClient(false)
	reqURL := "https://api.brandfetch.io/v2/search?query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		return ""
	}
	defer resp.Body.Close()
	var results []struct {
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil || len(results) == 0 {
		return ""
	}
	return results[0].Domain
}

// ─── Tier 1: BrandFetch API ────────────────────────────────────────────────

func fetchBrandFetch(ctx context.Context, apiKey, domain, depth string, result *brandResearchResult) (*brandSource, bool) {
	client := scraper.NewSSRFSafeClient(false)

	// Brand API.
	brandURL := "https://api.brandfetch.io/v2/brands/" + domain
	req, err := http.NewRequestWithContext(ctx, "GET", brandURL, nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		return nil, false
	}
	defer resp.Body.Close()

	var bfResp struct {
		Name   string `json:"name"`
		Domain string `json:"domain"`
		Logos  []struct {
			Theme   string `json:"theme"`
			Formats []struct {
				Src    string `json:"src"`
				Format string `json:"format"`
				Width  int    `json:"width"`
				Height int    `json:"height"`
			} `json:"formats"`
		} `json:"logos"`
		Colors []struct {
			Hex  string `json:"hex"`
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"colors"`
		Fonts []struct {
			Name     string `json:"name"`
			Type     string `json:"type"`
			Origin   string `json:"origin"`
			OriginID string `json:"originId"`
			Weights  []int  `json:"weights"`
		} `json:"fonts"`
		Company struct {
			Industry string `json:"industry"`
			Founded  int    `json:"foundedYear"`
			Location struct {
				City        string `json:"city"`
				CountryCode string `json:"countryCode"`
			} `json:"location"`
		} `json:"company"`
		Links []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"links"`
		QualityScore float64 `json:"qualityScore"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&bfResp); err != nil {
		return nil, false
	}

	fields := []string{}

	// Identity.
	if bfResp.Name != "" {
		result.Identity.Name = bfResp.Name
		fields = append(fields, "identity.name")
	}
	if bfResp.Company.Industry != "" {
		result.Identity.Industry = bfResp.Company.Industry
		fields = append(fields, "identity.industry")
	}
	if bfResp.Company.Founded > 0 {
		result.Identity.Founded = bfResp.Company.Founded
		fields = append(fields, "identity.founded")
	}
	if bfResp.Company.Location.City != "" || bfResp.Company.Location.CountryCode != "" {
		result.Identity.Location = &brandLocation{
			City:        bfResp.Company.Location.City,
			CountryCode: bfResp.Company.Location.CountryCode,
		}
		fields = append(fields, "identity.location")
	}

	// Logos.
	if len(bfResp.Logos) > 0 {
		logos := &brandLogos{}
		for _, logo := range bfResp.Logos {
			// Pick best format: prefer SVG, then PNG.
			var asset *brandLogoAsset
			for _, f := range logo.Formats {
				if f.Src == "" {
					continue
				}
				if asset == nil || (f.Format == "svg" && asset.Format != "svg") || (f.Format == "png" && asset.Format == "ico") {
					asset = &brandLogoAsset{URL: f.Src, Format: f.Format, Width: f.Width, Height: f.Height}
				}
			}
			if asset == nil {
				continue
			}
			switch logo.Theme {
			case "dark", "dark-background":
				if logos.Dark == nil {
					logos.Dark = asset
				}
			case "icon", "symbol":
				if logos.Icon == nil {
					logos.Icon = asset
				}
			default:
				if logos.Primary == nil {
					logos.Primary = asset
				}
			}
		}
		if logos.Primary != nil || logos.Dark != nil || logos.Icon != nil {
			result.Logos = logos
			fields = append(fields, "logos")
		}
	}

	// Colors.
	if len(bfResp.Colors) > 0 {
		colors := &brandColors{}
		var palette []brandColor
		for _, c := range bfResp.Colors {
			hex := normalizeHex(c.Hex)
			if hex == "" {
				continue
			}
			bc := brandColor{Hex: hex, Name: c.Name, Brightness: hexBrightness(hex)}
			switch c.Type {
			case "brand":
				if colors.Primary == "" {
					colors.Primary = hex
					bc.Role = "primary"
				}
			case "dark":
				if colors.Text == "" {
					colors.Text = hex
					bc.Role = "neutral"
				}
			case "light":
				if colors.Background == "" {
					colors.Background = hex
					bc.Role = "neutral"
				}
			case "accent":
				if colors.Accent == "" {
					colors.Accent = hex
					bc.Role = "accent"
				}
			}
			palette = append(palette, bc)
		}
		colors.Palette = palette
		result.Colors = colors
		fields = append(fields, "colors")
	}

	// Fonts.
	if len(bfResp.Fonts) > 0 {
		typo := &brandTypography{}
		for _, f := range bfResp.Fonts {
			if f.Name == "" {
				continue
			}
			bf := &brandFont{Family: f.Name, Weights: f.Weights, Origin: f.Origin, OriginID: f.OriginID}
			switch f.Type {
			case "title", "heading":
				if typo.Heading == nil {
					typo.Heading = bf
				}
			case "body":
				if typo.Body == nil {
					typo.Body = bf
				}
			case "mono", "code":
				if typo.Mono == nil {
					typo.Mono = bf
				}
			default:
				if typo.Heading == nil {
					typo.Heading = bf
				}
			}
		}
		if typo.Heading != nil || typo.Body != nil {
			result.Typography = typo
			fields = append(fields, "typography")
		}
	}

	// Social links.
	if len(bfResp.Links) > 0 {
		social := &brandSocial{}
		for _, link := range bfResp.Links {
			lname := strings.ToLower(link.Name)
			switch {
			case strings.Contains(lname, "twitter") || strings.Contains(lname, "x.com"):
				social.Twitter = link.URL
			case strings.Contains(lname, "linkedin"):
				social.LinkedIn = link.URL
			case strings.Contains(lname, "github"):
				social.GitHub = link.URL
			case strings.Contains(lname, "youtube"):
				social.YouTube = link.URL
			case strings.Contains(lname, "facebook"):
				social.Facebook = link.URL
			case strings.Contains(lname, "instagram"):
				social.Instagram = link.URL
			}
		}
		if social.Twitter != "" || social.LinkedIn != "" || social.GitHub != "" {
			result.Social = social
			fields = append(fields, "social")
		}
	}

	// BrandFetch Context API (depth >= standard).
	hasQuality := bfResp.QualityScore > 0.6
	if (depth == "standard" || depth == "full") && apiKey != "" {
		fetchBrandFetchContext(ctx, client, apiKey, domain, result)
	}

	src := &brandSource{Name: "brandfetch_api", URL: brandURL, Fields: fields}
	return src, hasQuality
}

func fetchBrandFetchContext(ctx context.Context, client *http.Client, apiKey, domain string, result *brandResearchResult) {
	ctxURL := "https://api.brandfetch.io/v2/context/" + domain
	req, err := http.NewRequestWithContext(ctx, "GET", ctxURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		return
	}
	defer resp.Body.Close()

	var ctxResp struct {
		Identity struct {
			Tagline string `json:"tagline"`
		} `json:"identity"`
		Brand struct {
			VoiceSummary string   `json:"voiceSummary"`
			Attributes   []string `json:"attributes"`
		} `json:"brand"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ctxResp); err != nil {
		return
	}

	if ctxResp.Identity.Tagline != "" && result.Identity.Tagline == "" {
		result.Identity.Tagline = ctxResp.Identity.Tagline
	}
	if ctxResp.Brand.VoiceSummary != "" || len(ctxResp.Brand.Attributes) > 0 {
		if result.ToneOfVoice == nil {
			result.ToneOfVoice = &brandTone{}
		}
		if ctxResp.Brand.VoiceSummary != "" && result.ToneOfVoice.Summary == "" {
			result.ToneOfVoice.Summary = ctxResp.Brand.VoiceSummary
		}
		if len(ctxResp.Brand.Attributes) > 0 && len(result.ToneOfVoice.Attributes) == 0 {
			result.ToneOfVoice.Attributes = ctxResp.Brand.Attributes
		}
	}
}

// ─── Tier 2: Homepage meta + structured data ───────────────────────────────

func fetchHomepageMeta(ctx context.Context, deps Dependencies, domain string, result *brandResearchResult, mu *sync.Mutex) *brandSource {
	rawURL := "https://" + domain
	scrapeResult, err := deps.Scraper.Scrape(ctx, rawURL, 200000)
	if err != nil {
		return nil
	}

	fields := []string{}

	// Parse HTML for logo chain and meta tags using goquery.
	// Track whether extractMetaTags successfully wrote identity.name from a real DOM tag.
	// We detect this by watching whether "identity.name" is appended to fields (not by
	// comparing values — the value may equal domainToName, e.g. og:site_name="Vercel").
	nameSetFromDOM := false
	rawResult, rawErr := deps.Scraper.ScrapeRaw(ctx, rawURL, 500000)
	if rawErr == nil && rawResult != nil && rawResult.Content != "" {
		doc, docErr := goquery.NewDocumentFromReader(strings.NewReader(rawResult.Content))
		if docErr == nil {
			mu.Lock()
			lenBefore := len(fields)
			extractMetaTags(doc, domain, result, &fields)
			// Any "identity.name" appended means a real DOM meta tag set the name.
			for _, f := range fields[lenBefore:] {
				if f == "identity.name" {
					nameSetFromDOM = true
					break
				}
			}
			extractLogoChain(doc, domain, result, &fields)
			mu.Unlock()
		}
	}

	// Structured data (JSON-LD / OpenGraph) from ScrapeResult.
	if scrapeResult != nil && scrapeResult.StructuredData != nil {
		mu.Lock()
		extractStructuredBrandData(scrapeResult.StructuredData, domain, result, &fields)
		mu.Unlock()
	}

	// Site name / title fallback for Identity.Name — only when no trusted DOM meta tag
	// already set it (prevents SiteName/Title from overwriting a clean og:site_name).
	// Also guard against a SiteName/Title that merely echoes the subdomain label
	// (e.g. "Docs" from docs.stripe.com — identical to domainToName of the subdomain).
	mu.Lock()
	rootName := domainToName(rootDomain(domain))
	if !nameSetFromDOM && (result.Identity.Name == "" || result.Identity.Name == domainToName(domain)) {
		tryName := func(raw string) string {
			n := cleanPageTitle(raw)
			// Reject if it equals the subdomain label (e.g. "Docs", "Support").
			if n == domainToName(domain) {
				return ""
			}
			return n
		}
		if scrapeResult != nil && scrapeResult.SiteName != "" {
			if n := tryName(scrapeResult.SiteName); n != "" {
				result.Identity.Name = n
				fields = append(fields, "identity.name")
			}
		}
		if result.Identity.Name == "" || result.Identity.Name == domainToName(domain) {
			if scrapeResult != nil && scrapeResult.Title != "" {
				if n := tryName(scrapeResult.Title); n != "" {
					result.Identity.Name = n
					fields = append(fields, "identity.name")
				}
			}
		}
		// Last resort: use the root domain name (e.g. "Stripe" from docs.stripe.com).
		if result.Identity.Name == "" || result.Identity.Name == domainToName(domain) {
			if rootName != domainToName(domain) && rootName != "" {
				result.Identity.Name = rootName
				fields = append(fields, "identity.name")
			}
		}
	}
	mu.Unlock()

	if len(fields) == 0 {
		return nil
	}
	return &brandSource{Name: "homepage_meta", URL: rawURL, Fields: fields}
}

func extractMetaTags(doc *goquery.Document, domain string, result *brandResearchResult, fields *[]string) {
	// Theme color → primary color fallback.
	if result.Colors == nil {
		doc.Find("meta[name='theme-color'], meta[name='msapplication-TileColor']").Each(func(_ int, s *goquery.Selection) {
			if content, ok := s.Attr("content"); ok {
				hex := normalizeHex(content)
				if hex != "" && result.Colors == nil {
					result.Colors = &brandColors{Primary: hex}
					*fields = append(*fields, "colors.primary")
				}
			}
		})
	}

	// og:site_name → brand name (more reliable than full <title>).
	if result.Identity.Name == "" || result.Identity.Name == domainToName(domain) {
		doc.Find("meta[property='og:site_name']").First().Each(func(_ int, s *goquery.Selection) {
			if content, ok := s.Attr("content"); ok && strings.TrimSpace(content) != "" {
				// Some sites set og:site_name to their hero tagline; clean it.
				result.Identity.Name = cleanPageTitle(strings.TrimSpace(content))
				*fields = append(*fields, "identity.name")
			}
		})
	}
	// application-name as second DOM fallback.
	if result.Identity.Name == "" || result.Identity.Name == domainToName(domain) {
		doc.Find("meta[name='application-name']").First().Each(func(_ int, s *goquery.Selection) {
			if content, ok := s.Attr("content"); ok && strings.TrimSpace(content) != "" {
				result.Identity.Name = cleanPageTitle(strings.TrimSpace(content))
				*fields = append(*fields, "identity.name")
			}
		})
	}
}

func extractLogoChain(doc *goquery.Document, domain string, result *brandResearchResult, fields *[]string) {
	if result.Logos != nil && result.Logos.Primary != nil {
		return // BrandFetch already set logos
	}
	if result.Logos == nil {
		result.Logos = &brandLogos{}
	}

	// Priority chain per issue spec.
	var candidate *brandLogoAsset

	// 1. SVG icon link
	doc.Find("link[rel='icon'][type='image/svg+xml']").First().Each(func(_ int, s *goquery.Selection) {
		if href, ok := s.Attr("href"); ok && href != "" && candidate == nil {
			candidate = &brandLogoAsset{URL: resolveURL("https://"+domain, href), Format: "svg"}
		}
	})
	// 2. apple-touch-icon 180x180
	if candidate == nil {
		doc.Find("link[rel='apple-touch-icon'][sizes='180x180']").First().Each(func(_ int, s *goquery.Selection) {
			if href, ok := s.Attr("href"); ok && href != "" {
				candidate = &brandLogoAsset{URL: resolveURL("https://"+domain, href), Format: "png"}
			}
		})
	}
	// 3-4. icon 192 / 32
	for _, sz := range []string{"192x192", "32x32"} {
		if candidate != nil {
			break
		}
		doc.Find("link[rel='icon'][sizes='" + sz + "']").First().Each(func(_ int, s *goquery.Selection) {
			if href, ok := s.Attr("href"); ok && href != "" && candidate == nil {
				candidate = &brandLogoAsset{URL: resolveURL("https://"+domain, href), Format: "png"}
			}
		})
	}
	// 5. shortcut icon
	if candidate == nil {
		doc.Find("link[rel='shortcut icon']").First().Each(func(_ int, s *goquery.Selection) {
			if href, ok := s.Attr("href"); ok && href != "" {
				candidate = &brandLogoAsset{URL: resolveURL("https://"+domain, href), Format: "ico"}
			}
		})
	}
	// 6. /favicon.ico probe — just set URL, don't fetch
	if candidate == nil {
		candidate = &brandLogoAsset{URL: "https://" + domain + "/favicon.ico", Format: "ico"}
	}

	if candidate != nil {
		if result.Logos.Icon == nil {
			result.Logos.Icon = candidate
			*fields = append(*fields, "logos.icon")
		}
	}

	// og:image / twitter:image as OGImage.
	doc.Find("meta[property='og:image'], meta[name='twitter:image']").First().Each(func(_ int, s *goquery.Selection) {
		if content, ok := s.Attr("content"); ok && content != "" && result.Logos.OGImage == "" {
			result.Logos.OGImage = content
			*fields = append(*fields, "logos.og_image")
		}
	})

	// Header/nav logo image fallback.
	if result.Logos.Primary == nil {
		doc.Find("header img[src*='logo'], nav img[src*='logo'], a[href='/'] img").First().Each(func(_ int, s *goquery.Selection) {
			if src, ok := s.Attr("src"); ok && src != "" {
				ext := "png"
				if strings.HasSuffix(strings.ToLower(src), ".svg") {
					ext = "svg"
				}
				result.Logos.Primary = &brandLogoAsset{URL: resolveURL("https://"+domain, src), Format: ext}
				*fields = append(*fields, "logos.primary")
			}
		})
	}
}

// extractStructuredBrandData pulls identity and description from JSON-LD and
// OpenGraph metadata present on the scraped homepage.
func extractStructuredBrandData(sd *scraper.StructuredData, domain string, result *brandResearchResult, fields *[]string) {
	if sd == nil {
		return
	}

	// OpenGraph: description and site name.
	if og := sd.OpenGraph; len(og) > 0 {
		if desc, ok := og["og:description"]; ok && desc != "" && result.Identity.Description == "" {
			result.Identity.Description = desc
			*fields = append(*fields, "identity.description")
		}
		if siteName, ok := og["og:site_name"]; ok && siteName != "" && (result.Identity.Name == "" || result.Identity.Name == domainToName(domain)) {
			result.Identity.Name = siteName
			*fields = append(*fields, "identity.name")
		}
	}

	// JSON-LD: organization name and description.
	for _, raw := range sd.JSONLD {
		var block map[string]any
		if err := json.Unmarshal(raw, &block); err != nil {
			continue
		}
		typeVal, _ := block["@type"].(string)
		if typeVal != "Organization" && typeVal != "Corporation" && typeVal != "LocalBusiness" {
			continue
		}
		if name, ok := block["name"].(string); ok && name != "" && (result.Identity.Name == "" || result.Identity.Name == domainToName(domain)) {
			result.Identity.Name = name
			*fields = append(*fields, "identity.name")
		}
		if desc, ok := block["description"].(string); ok && desc != "" && result.Identity.Description == "" {
			result.Identity.Description = desc
			*fields = append(*fields, "identity.description")
		}
		break
	}
}

// ─── Tier 3: CSS extraction ────────────────────────────────────────────────

func fetchBrandCSS(ctx context.Context, deps Dependencies, domain string, result *brandResearchResult, mu *sync.Mutex) *brandSource {
	rawURL := "https://" + domain
	rawResult, err := deps.Scraper.ScrapeRaw(ctx, rawURL, 500000)
	if err != nil || rawResult == nil {
		return nil
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(rawResult.Content))
	if err != nil {
		return nil
	}

	// Collect inline <style> block content and up to 5 external stylesheet URLs.
	// Inline styles are parsed first — they commonly hold CSS design-token variables.
	var inlineCSS string
	doc.Find("style").Each(func(_ int, s *goquery.Selection) {
		inlineCSS += s.Text() + "\n"
	})

	var cssURLs []string
	doc.Find("link[rel='stylesheet']").Each(func(_ int, s *goquery.Selection) {
		if len(cssURLs) >= 5 {
			return
		}
		if href, ok := s.Attr("href"); ok && href != "" {
			cssURLs = append(cssURLs, resolveURL(rawURL, href))
		}
	})

	if len(cssURLs) == 0 && inlineCSS == "" {
		return nil
	}

	// Fetch external CSS concurrently (semaphore of 8).
	sem := make(chan struct{}, 8)
	type cssEntry struct {
		content string
		url     string
	}
	cssResults := make([]cssEntry, len(cssURLs)+1) // slot 0 reserved for inline CSS
	cssResults[0] = cssEntry{content: inlineCSS, url: rawURL + "#inline"}
	var cwg sync.WaitGroup
	client := scraper.NewSSRFSafeClient(false)
	for i, cssURL := range cssURLs {
		cwg.Add(1)
		go func(idx int, u string) {
			defer cwg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
			if err != nil {
				return
			}
			req.Header.Set("Accept-Language", "en-US,en;q=0.9")
			resp, err := client.Do(req)
			if err != nil || resp.StatusCode != 200 {
				if resp != nil {
					resp.Body.Close()
				}
				return
			}
			defer resp.Body.Close()
			// Read up to 200KB per file.
			buf := make([]byte, 200*1024)
			n, _ := resp.Body.Read(buf)
			cssResults[idx+1] = cssEntry{content: string(buf[:n]), url: u}
		}(i, cssURL)
	}
	cwg.Wait()

	// Parse colors and fonts from all CSS.
	colorCounts := map[string]int{}
	colorNames := map[string]string{}
	var fontFamilies []string
	var googleFontsURL string
	var adobeFontsKit string

	for _, entry := range cssResults {
		if entry.content == "" {
			continue
		}
		// Strip @media/@supports/@-moz-document blocks before colour counting so
		// browser-quirk overrides (P3 wide-gamut, Firefox fixes) don't inflate
		// the frequency of colours that only appear in those conditional blocks.
		css := stripConditionalAtRules(entry.content)

		// CSS variable colors (highest signal — 2× weight multiplier for brand/primary/main names).
		// Pass 1: variables whose prefix is a known design-token namespace.
		pass1Hexes := map[string]bool{}
		for _, match := range reCSSVarColor.FindAllStringSubmatch(css, -1) {
			if len(match) < 2 {
				continue
			}
			varName := strings.ToLower(match[0])
			hex := normalizeColorValue(match[1])
			if hex == "" {
				continue
			}
			pass1Hexes[hex] = true
			weight := 1
			if strings.Contains(varName, "primary") || strings.Contains(varName, "brand") || strings.Contains(varName, "main") {
				weight = 2
			}
			colorCounts[hex] += weight
			if colorNames[hex] == "" {
				colorNames[hex] = extractVarName(varName)
			}
		}

		// Pass 2: any CSS variable whose name contains a brand-signal keyword
		// (e.g. --palette-bg-primary-core, --sys-brand-fill).  These get 2×
		// weight and skip hexes already counted in pass 1 to avoid double-counting.
		for _, match := range reCSSBrandSignalVar.FindAllStringSubmatch(css, -1) {
			if len(match) < 3 {
				continue
			}
			hex := normalizeColorValue(match[2])
			if hex == "" || pass1Hexes[hex] {
				continue
			}
			varName := strings.ToLower("--" + match[1])
			colorCounts[hex] += 2
			if colorNames[hex] == "" {
				colorNames[hex] = extractVarName(varName)
			}
		}

		// Direct property colors.
		for _, match := range reCSSColorProp.FindAllStringSubmatch(css, -1) {
			if len(match) < 2 {
				continue
			}
			hex := normalizeColorValue(match[1])
			if hex != "" {
				colorCounts[hex]++
			}
		}

		// Fonts — two-pass: first build CSS custom property map, then resolve.
		fontVarMap := map[string]string{}
		for _, m := range reCSSVarFont.FindAllStringSubmatch(css, -1) {
			if len(m) < 3 {
				continue
			}
			varName := strings.ToLower(strings.TrimSpace(m[1]))
			varVal := strings.TrimSpace(m[2])
			// Store all values including var() references so resolveFontVar
			// can chain-resolve them up to 3 levels deep.
			fontVarMap[varName] = varVal
		}
		for _, match := range reCSSFontFamily.FindAllStringSubmatch(css, -1) {
			if len(match) < 2 {
				continue
			}
			raw := strings.TrimSpace(match[1])
			// Resolve CSS variable reference.
			if strings.HasPrefix(strings.ToLower(raw), "var(--") {
				raw = resolveFontVar(raw, fontVarMap)
			}
			family := cleanFontFamily(raw)
			if family != "" {
				fontFamilies = append(fontFamilies, family)
			}
		}

		// Google Fonts.
		if m := reGoogleFonts.FindStringSubmatch(css); m != nil && googleFontsURL == "" {
			googleFontsURL = "https://fonts.googleapis.com/css?family=" + m[1]
		}

		// Adobe Fonts.
		if m := reAdobeFonts.FindStringSubmatch(css); m != nil && adobeFontsKit == "" {
			adobeFontsKit = m[1]
		}
	}

	mu.Lock()
	defer mu.Unlock()

	fields := []string{}

	// Colors from CSS.
	if len(colorCounts) > 0 {
		var sorted []cssColorScored
		for hex, count := range colorCounts {
			sorted = append(sorted, cssColorScored{hex, count})
		}
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].count > sorted[j].count })
		if len(sorted) > 8 {
			sorted = sorted[:8]
		}

		if result.Colors == nil {
			result.Colors = &brandColors{}
		}
		for _, s := range sorted {
			role := guessColorRole(colorNames[s.hex])
			bc := brandColor{Hex: s.hex, Name: colorNames[s.hex], Role: role, Brightness: hexBrightness(s.hex)}
			result.Colors.Palette = append(result.Colors.Palette, bc)
		}
		if result.Colors.Primary == "" || isNearNeutral(result.Colors.Primary) {
			if chromatic := pickChromaticPrimary(sorted); chromatic != "" {
				result.Colors.Primary = chromatic
			} else if isNearNeutral(result.Colors.Primary) {
				// CSS found no chromatic primary — clear the near-neutral placeholder
				// so the result reports no primary rather than a misleading white/black.
				result.Colors.Primary = ""
			}
		}
		fields = append(fields, "colors")
	}

	// Fonts from CSS.
	if len(fontFamilies) > 0 && result.Typography == nil {
		result.Typography = &brandTypography{}
		seen := map[string]bool{}
		for _, f := range fontFamilies {
			if seen[f] {
				continue
			}
			seen[f] = true
			origin := "custom"
			if googleFontsURL != "" {
				origin = "google"
			} else if adobeFontsKit != "" {
				origin = "adobe"
			}
			bf := &brandFont{Family: f, Origin: origin}
			if adobeFontsKit != "" {
				bf.OriginID = adobeFontsKit
			}
			if result.Typography.Heading == nil {
				result.Typography.Heading = bf
			} else if result.Typography.Body == nil {
				result.Typography.Body = bf
				break
			}
		}
		if googleFontsURL != "" {
			result.Typography.GoogleFontsURL = googleFontsURL
		}
		fields = append(fields, "typography")
	}

	if len(fields) == 0 {
		return nil
	}
	return &brandSource{Name: "css_extraction", Fields: fields}
}

// ─── Tier 4: Brand guidelines page probe ──────────────────────────────────

var brandPageSubdomains = []string{"brand", "press", "newsroom", "media", "design"}

var brandPagePaths = []string{
	"/brand", "/brand-guidelines", "/brandbook", "/brand-center",
	"/brand-assets", "/press", "/presskit", "/press-kit",
	"/press-room", "/newsroom", "/media", "/media-kit",
	"/mediakit", "/design", "/design-system", "/styleguide",
	"/style-guide", "/guidelines", "/identity", "/visual-identity",
	"/about/brand",
}

func probeBrandPage(ctx context.Context, deps Dependencies, domain string, result *brandResearchResult, mu *sync.Mutex, depth string) *brandSource {
	// Each candidate carries a priority so dedicated brand subdomains
	// (brand.*, press.*) beat generic path matches even when the path URL
	// responds faster.  Lower priority number = better match.
	type candidate struct {
		url      string
		priority int
	}

	var candidates []candidate
	for i, sub := range brandPageSubdomains {
		candidates = append(candidates, candidate{"https://" + sub + "." + domain, i})
	}
	pathBase := len(brandPageSubdomains)
	for i, path := range brandPagePaths {
		candidates = append(candidates, candidate{"https://" + domain + path, pathBase + i})
	}

	client := scraper.NewSSRFSafeClient(false)
	sem := make(chan struct{}, 8)
	homepageURL := "https://" + domain + "/"

	// Collect all valid matches; pick the best priority after all probes finish.
	matches := make(chan candidate, len(candidates))

	var pwg sync.WaitGroup
	for _, c := range candidates {
		pwg.Add(1)
		go func(c candidate) {
			defer pwg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			req, err := http.NewRequestWithContext(ctx, "HEAD", c.url, nil)
			if err != nil {
				return
			}
			// Use a browser-like UA — some brand portals block bot UAs even on HEAD.
			req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			resp.Body.Close()

			if resp.StatusCode == 200 {
				finalURL := resp.Request.URL.String()
				// Reject redirects back to the homepage.
				if hostsMatch(finalURL, homepageURL) {
					return
				}
				// Reject redirects that landed on a different host than the
				// candidate (e.g. kaltura.com/media → kmc.kaltura.com is their
				// legacy app, not a brand page). A path redirect on the same
				// host is fine (brand.acme.com → brand.acme.com/en/).
				if candidateParsed, err2 := url.Parse(c.url); err2 == nil {
					if finalParsed, err3 := url.Parse(finalURL); err3 == nil {
						if !strings.EqualFold(finalParsed.Hostname(), candidateParsed.Hostname()) {
							return
						}
					}
				}
				if looksLikeBrandPage(ctx, client, c.url) {
					matches <- c
				}
			}
		}(c)
	}
	pwg.Wait()
	close(matches)

	// Pick the highest-priority (lowest number) valid match.
	best := candidate{priority: len(candidates) + 1}
	for m := range matches {
		if m.priority < best.priority {
			best = m
		}
	}
	guidelinesURL := best.url
	if guidelinesURL == "" {
		return nil
	}

	mu.Lock()
	if result.GuidelinesURL == "" {
		result.GuidelinesURL = guidelinesURL
	}
	mu.Unlock()

	// Scrape the page for additional colors + tone signals.
	fields := []string{"guidelines_url"}
	if depth == "standard" || depth == "full" {
		scrapeResult, err := deps.Scraper.Scrape(ctx, guidelinesURL, 100000)
		if err == nil && scrapeResult != nil {
			mu.Lock()
			extractBrandPageContent(scrapeResult.Content, domain, result, &fields)
			mu.Unlock()
		}
	}

	return &brandSource{Name: "brand_page", URL: guidelinesURL, Fields: fields}
}

func extractBrandPageContent(content, domain string, result *brandResearchResult, fields *[]string) {
	// Hex codes in text.
	hexMatches := reHexColor.FindAllString(content, -1)
	if len(hexMatches) > 0 && result.Colors != nil {
		seen := map[string]bool{}
		for _, h := range result.Colors.Palette {
			seen[h.Hex] = true
		}
		for _, hex := range hexMatches {
			hex = strings.ToLower(hex)
			if !seen[hex] {
				result.Colors.Palette = append(result.Colors.Palette, brandColor{Hex: hex, Role: "neutral", Brightness: hexBrightness(hex)})
				seen[hex] = true
				if len(result.Colors.Palette) >= 12 {
					break
				}
			}
		}
	}

	// Tone of voice sections.
	if result.ToneOfVoice == nil {
		lines := strings.Split(content, "\n")
		var toneLines []string
		capture := false
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if reToneHeading.MatchString(line) {
				capture = true
				continue
			}
			if capture && line != "" {
				toneLines = append(toneLines, line)
				if len(toneLines) >= 5 {
					break
				}
			}
		}
		if len(toneLines) > 0 {
			result.ToneOfVoice = &brandTone{Summary: strings.Join(toneLines, " ")}
			*fields = append(*fields, "tone_of_voice")
		}
	}
}

// ─── Tier 5: Web search ────────────────────────────────────────────────────

// knownBrandHosts are third-party platforms that legitimately host brand portals.
// github.com/github.io are intentionally excluded here — GitHub results are
// only accepted when the query explicitly targets site:github.com.
var knownBrandHosts = []string{
	"brandfetch.io", "brand.ai", "frontify.com", "bynder.com",
	"corebook.io", "marq.com", "lucidpress.com", "canto.com",
	"canva.com", "figma.com",
}

func searchBrandGuidelines(ctx context.Context, deps Dependencies, companyName, domain string, result *brandResearchResult, mu *sync.Mutex) *brandSource {
	queries := []string{
		`"` + companyName + `" brand guidelines OR brand kit OR brand book -filetype:pdf`,
		`"` + companyName + `" design system site:github.com`,
		`"` + companyName + `" figma brand kit`,
	}

	fields := []string{}
	for _, q := range queries {
		results, err := deps.Search.Web(ctx, search.WebSearchParams{Query: q, NumResults: 5})
		if err != nil || len(results) == 0 {
			continue
		}
		for _, r := range results {
			// Skip PDF/document results and template/category pages — we want live brand portal pages.
			urlLower := strings.ToLower(r.URL)
			if strings.HasSuffix(urlLower, ".pdf") ||
				strings.HasSuffix(urlLower, ".docx") ||
				strings.HasSuffix(urlLower, ".pptx") {
				continue
			}
			if strings.Contains(urlLower, "/templates/") ||
				strings.Contains(urlLower, "/template/") ||
				strings.Contains(urlLower, "/category/") ||
				strings.Contains(urlLower, "/tag/") ||
				strings.HasSuffix(urlLower, "-template") ||
				strings.HasSuffix(urlLower, "-templates") ||
				strings.HasSuffix(urlLower, "_template") ||
				strings.HasSuffix(urlLower, "_templates") {
				continue
			}
			// Only accept results from the company's own domain, known brand-portal hosts,
			// or GitHub when the query explicitly targets site:github.com.
			if parsed, err := url.Parse(r.URL); err == nil {
				host := strings.ToLower(parsed.Hostname())
				ownDomain := !strings.Contains(domain, ".") || strings.HasSuffix(host, "."+domain) || host == domain
				knownHost := false
				for _, kh := range knownBrandHosts {
					if strings.HasSuffix(host, kh) || host == kh {
						knownHost = true
						break
					}
				}
				// For GitHub, also require the repo org to match the company's
				// primary domain label (e.g. "vercel" in "vercel.com").
				githubOK := false
				if strings.Contains(q, "site:github.com") &&
					(host == "github.com" || strings.HasSuffix(host, ".github.io")) {
					domainLabel := strings.SplitN(domain, ".", 2)[0]
					pathParts := strings.SplitN(strings.TrimPrefix(parsed.Path, "/"), "/", 3)
					orgMatch := len(pathParts) > 0 && strings.EqualFold(pathParts[0], domainLabel)
					// github.io repos are <org>.github.io — match the host label
					hostLabel := strings.SplitN(host, ".", 2)[0]
					githubOK = orgMatch || strings.EqualFold(hostLabel, domainLabel)
				}
				if !ownDomain && !knownHost && !githubOK {
					continue
				}
			}
			fields = append(fields, "guidelines_url")
			mu.Lock()
			if result.GuidelinesURL == "" {
				result.GuidelinesURL = r.URL
			}
			mu.Unlock()
			break
		}
	}

	if len(fields) == 0 {
		return nil
	}
	return &brandSource{Name: "web_search", Fields: fields}
}

// ─── Coverage + Design Tokens ──────────────────────────────────────────────

func computeBrandCoverage(result *brandResearchResult) brandCoverage {
	cov := brandCoverage{Colors: "none", Logos: "none", Typography: "none", ToneOfVoice: "none"}

	if result.Colors != nil {
		if result.Colors.Primary != "" && len(result.Colors.Palette) >= 3 {
			cov.Colors = "full"
		} else {
			cov.Colors = "partial"
		}
	}

	if result.Logos != nil {
		if (result.Logos.Primary != nil) && (result.Logos.Icon != nil || result.Logos.Favicon != "") {
			cov.Logos = "full"
		} else {
			cov.Logos = "partial"
		}
	}

	if result.Typography != nil {
		if result.Typography.Heading != nil && result.Typography.Body != nil {
			cov.Typography = "full"
		} else {
			cov.Typography = "partial"
		}
	}

	if result.ToneOfVoice != nil {
		if result.ToneOfVoice.Summary != "" && len(result.ToneOfVoice.Attributes) > 0 {
			cov.ToneOfVoice = "found"
		} else {
			cov.ToneOfVoice = "inferred"
		}
	}

	return cov
}

func buildDesignTokens(result *brandResearchResult) map[string]any {
	tokens := map[string]any{}

	if result.Colors != nil {
		colorGroup := map[string]any{}
		if result.Colors.Primary != "" {
			colorGroup["brand"] = map[string]any{"$value": result.Colors.Primary, "$type": "color"}
		}
		if result.Colors.Accent != "" {
			colorGroup["accent"] = map[string]any{"$value": result.Colors.Accent, "$type": "color"}
		}
		if result.Colors.Background != "" {
			colorGroup["background"] = map[string]any{"$value": result.Colors.Background, "$type": "color"}
		}
		if result.Colors.Text != "" {
			colorGroup["text"] = map[string]any{"$value": result.Colors.Text, "$type": "color"}
		}
		if result.Colors.Secondary != "" {
			colorGroup["secondary"] = map[string]any{"$value": result.Colors.Secondary, "$type": "color"}
		}
		if len(colorGroup) > 0 {
			tokens["color"] = colorGroup
		}
	}

	if result.Typography != nil {
		fontGroup := map[string]any{}
		if result.Typography.Heading != nil {
			fontGroup["heading"] = map[string]any{"$value": result.Typography.Heading.Family, "$type": "fontFamily"}
		}
		if result.Typography.Body != nil {
			fontGroup["body"] = map[string]any{"$value": result.Typography.Body.Family, "$type": "fontFamily"}
		}
		if len(fontGroup) > 0 {
			tokens["font"] = fontGroup
		}
	}

	if len(tokens) == 0 {
		return nil
	}
	return tokens
}

// ─── Cache helpers ─────────────────────────────────────────────────────────

func brandCacheKey(domain, depth string) string {
	h := sha256.Sum256([]byte("brand_research:" + domain + ":" + depth))
	return fmt.Sprintf("%x", h)
}

// ─── Color utilities ───────────────────────────────────────────────────────

func normalizeHex(s string) string {
	s = strings.TrimSpace(s)
	return normalizeColorValue(s)
}

func normalizeColorValue(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "#") {
		hex := strings.TrimPrefix(s, "#")
		switch len(hex) {
		case 3:
			return "#" + strings.ToLower(string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]}))
		case 6:
			return "#" + strings.ToLower(hex)
		case 8: // RGBA — strip alpha
			return "#" + strings.ToLower(hex[:6])
		}
		return ""
	}
	if strings.HasPrefix(strings.ToLower(s), "rgb(") {
		s2 := strings.TrimPrefix(strings.ToLower(s), "rgb(")
		s2 = strings.TrimSuffix(s2, ")")
		parts := strings.Split(s2, ",")
		if len(parts) == 3 {
			r := parseColorComponent(parts[0])
			g := parseColorComponent(parts[1])
			b := parseColorComponent(parts[2])
			if r >= 0 && g >= 0 && b >= 0 {
				return fmt.Sprintf("#%02x%02x%02x", r, g, b)
			}
		}
	}
	if strings.HasPrefix(strings.ToLower(s), "rgba(") {
		s2 := strings.TrimPrefix(strings.ToLower(s), "rgba(")
		s2 = strings.TrimSuffix(s2, ")")
		parts := strings.Split(s2, ",")
		if len(parts) == 4 {
			r := parseColorComponent(parts[0])
			g := parseColorComponent(parts[1])
			b := parseColorComponent(parts[2])
			if r >= 0 && g >= 0 && b >= 0 {
				return fmt.Sprintf("#%02x%02x%02x", r, g, b)
			}
		}
	}
	if strings.HasPrefix(strings.ToLower(s), "hsl(") || strings.HasPrefix(strings.ToLower(s), "hsla(") {
		return hslToHex(s)
	}
	return ""
}

func parseColorComponent(s string) int {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "%") {
		pct, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
		if err != nil {
			return -1
		}
		return int(math.Round(pct / 100 * 255))
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return v
}

func hslToHex(s string) string {
	s = strings.ToLower(s)
	s = strings.TrimPrefix(s, "hsla(")
	s = strings.TrimPrefix(s, "hsl(")
	s = strings.TrimSuffix(s, ")")
	parts := strings.Split(s, ",")
	if len(parts) < 3 {
		return ""
	}
	h, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	sStr := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(parts[1]), "%"))
	lStr := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(parts[2]), "%"))
	sat, err2 := strconv.ParseFloat(sStr, 64)
	light, err3 := strconv.ParseFloat(lStr, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return ""
	}
	sat /= 100
	light /= 100
	r, g, b := hslToRGB(h, sat, light)
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

func hslToRGB(h, s, l float64) (uint8, uint8, uint8) {
	if s == 0 {
		v := uint8(math.Round(l * 255))
		return v, v, v
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	r := hueToRGB(p, q, h/360+1.0/3)
	g := hueToRGB(p, q, h/360)
	b := hueToRGB(p, q, h/360-1.0/3)
	return uint8(math.Round(r * 255)), uint8(math.Round(g * 255)), uint8(math.Round(b * 255))
}

func hueToRGB(p, q, t float64) float64 {
	if t < 0 {
		t++
	}
	if t > 1 {
		t--
	}
	switch {
	case t < 1.0/6:
		return p + (q-p)*6*t
	case t < 1.0/2:
		return q
	case t < 2.0/3:
		return p + (q-p)*(2.0/3-t)*6
	default:
		return p
	}
}

func hexBrightness(hex string) int {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) < 6 {
		return 0
	}
	r, _ := strconv.ParseInt(hex[0:2], 16, 64)
	g, _ := strconv.ParseInt(hex[2:4], 16, 64)
	b, _ := strconv.ParseInt(hex[4:6], 16, 64)
	// Relative luminance approximation (sRGB).
	lum := (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 255 * 100
	return int(math.Round(lum))
}

func extractVarName(varDef string) string {
	// Extract the CSS variable name from a full match like "--color-primary: #xxx"
	parts := strings.SplitN(varDef, ":", 2)
	if len(parts) == 0 {
		return ""
	}
	name := strings.TrimSpace(parts[0])
	name = strings.TrimPrefix(name, "--")
	name = strings.ReplaceAll(name, "-", " ")
	return strings.TrimSpace(name)
}

func guessColorRole(name string) string {
	name = strings.ToLower(name)
	switch {
	case strings.Contains(name, "primary") || strings.Contains(name, "brand") || strings.Contains(name, "main"):
		return "primary"
	case strings.Contains(name, "secondary"):
		return "secondary"
	case strings.Contains(name, "accent"):
		return "accent"
	default:
		return "neutral"
	}
}

// iconFontBlocklist excludes icon/symbol fonts that are not text typefaces.
var iconFontBlocklist = map[string]bool{
	"webflow-icons": true, "fontawesome": true, "font awesome 5 free": true,
	"font awesome 5 brands": true, "font awesome 5 pro": true, "font awesome 6 free": true,
	"font awesome 6 brands": true, "font awesome 6 pro": true, "fa": true, "fa-solid": true,
	"material icons": true, "material icons outlined": true, "material icons round": true,
	"material icons sharp": true, "material symbols outlined": true, "material symbols rounded": true,
	"material symbols sharp": true, "ionicons": true, "octicons": true, "feather": true,
	"bootstrap-icons": true, "remixicon": true, "tabler-icons": true, "phosphor": true,
	"icomoon": true, "linearicons": true, "glyphicons halflings regular": true,
}

func cleanFontFamily(raw string) string {
	// Strip !important and CSS modifiers before any other processing.
	raw = strings.ReplaceAll(raw, "!important", "")
	raw = strings.TrimSpace(raw)
	// Skip unresolved CSS variable references.
	if strings.HasPrefix(strings.ToLower(raw), "var(--") {
		return ""
	}
	parts := strings.SplitN(raw, ",", 2)
	font := strings.TrimSpace(parts[0])
	font = strings.Trim(font, "'\"")
	font = strings.TrimSpace(font)
	lower := strings.ToLower(font)
	// Skip generic families.
	generic := map[string]bool{
		"serif": true, "sans-serif": true, "monospace": true, "cursive": true,
		"fantasy": true, "inherit": true, "initial": true, "unset": true,
		"system-ui": true, "-apple-system": true, "revert": true, "revert-layer": true,
		"none": true,
	}
	if generic[lower] {
		return ""
	}
	// Skip icon / symbol fonts.
	if iconFontBlocklist[lower] {
		return ""
	}
	return font
}

// cleanPageTitle strips tagline content from a raw <title> string, returning the
// brand-name token. It handles both "Acme | tagline" and "tagline | Acme" layouts
// by picking the shorter side when the trailing token is ≤ 30 chars (e.g. "Notion",
// "Spotify") and falling back to the leading token otherwise.
func cleanPageTitle(title string) string {
	title = strings.TrimSpace(title)
	// Separators in priority order: pipe, em-dash, en-dash, colon-space, spaced hyphen.
	for _, sep := range []string{" | ", " — ", " – ", ": ", " - "} {
		idx := strings.Index(title, sep)
		if idx <= 0 {
			continue
		}
		before := strings.TrimSpace(title[:idx])
		after := strings.TrimSpace(title[idx+len(sep):])
		// If the trailing token is short (≤30 chars) and shorter than the leading
		// token, the brand name is likely on the right (e.g. "tagline | Brand").
		if len(after) > 0 && len(after) <= 30 && len(after) < len(before) {
			return after
		}
		return before
	}
	return title
}

// hexSaturation returns the HSL saturation (0–1) for a normalised hex colour string.
func hexSaturation(hex string) float64 {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) < 6 {
		return 0
	}
	r64, _ := strconv.ParseInt(hex[0:2], 16, 64)
	g64, _ := strconv.ParseInt(hex[2:4], 16, 64)
	b64, _ := strconv.ParseInt(hex[4:6], 16, 64)
	rf, gf, bf := float64(r64)/255, float64(g64)/255, float64(b64)/255
	maxC := math.Max(rf, math.Max(gf, bf))
	minC := math.Min(rf, math.Min(gf, bf))
	delta := maxC - minC
	if delta == 0 {
		return 0
	}
	l := (maxC + minC) / 2
	if l < 0.5 {
		return delta / (maxC + minC)
	}
	return delta / (2 - maxC - minC)
}

// cssColorScored is the frequency-scored entry used during CSS color analysis.
type cssColorScored struct {
	hex   string
	count int
}

// stripConditionalAtRules removes @media, @supports, and @-moz-document blocks from
// a CSS string so that browser-quirk overrides (P3 wide-gamut, Firefox fixes, dark
// mode) don't inflate the frequency count of colours that only appear in those blocks.
// Uses brace-balance tracking — no external parser (Zero-Dependency Mandate).
func stripConditionalAtRules(css string) string {
	var out strings.Builder
	out.Grow(len(css))
	i := 0
	for i < len(css) {
		loc := reConditionalAtRule.FindStringIndex(css[i:])
		if loc == nil {
			out.WriteString(css[i:])
			break
		}
		start := i + loc[0]
		out.WriteString(css[i:start])
		// Find the opening brace of this at-rule's block.
		brace := strings.Index(css[start:], "{")
		if brace == -1 {
			// Malformed — keep the rest verbatim.
			out.WriteString(css[start:])
			break
		}
		// Walk forward tracking brace depth to find the matching close brace.
		depth := 0
		j := start + brace
		for j < len(css) {
			if css[j] == '{' {
				depth++
			} else if css[j] == '}' {
				depth--
				if depth == 0 {
					j++ // skip the closing brace
					break
				}
			}
			j++
		}
		i = j // resume after the stripped block
	}
	return out.String()
}

// isNearNeutral returns true when a hex colour is effectively white, near-white,
// grey, near-grey, or black — and therefore unsuitable as a brand primary.
// Upper brightness threshold is 87 (not 100) — very light tints (e.g. pale lavender
// at brightness=92) look near-white despite having HSL sat=1.0.
// Saturation threshold is 0.07 — covers true grays (#a1a1aa sat=0.05) while
// preserving desaturated-but-chromatic blues, purples and teals.
// Uses the same brightness and saturation thresholds as pickChromaticPrimary.
func isNearNeutral(hex string) bool {
	sat := hexSaturation(hex)
	b := hexBrightness(hex)
	return sat < 0.07 || b < 3 || b > 87
}

// pickChromaticPrimary selects the most-chromatic non-neutral colour from a
// frequency-sorted slice, weighted by both saturation and frequency so that a
// high-saturation colour appearing only in a media-query override (count=1–2)
// cannot beat a moderately-saturated colour that appears throughout the stylesheet.
// Score = saturation × log2(count+1). Returns "" if all candidates are near-neutral
// or extreme-brightness artifacts (e.g. pure yellow brightness=89, white, black).
func pickChromaticPrimary(sorted []cssColorScored) string {
	if len(sorted) == 0 {
		return ""
	}
	best := ""
	bestScore := -1.0
	for _, s := range sorted {
		sat := hexSaturation(s.hex)
		b := hexBrightness(s.hex)
		// Reject near-neutrals (very low saturation), very dark (b<3) and
		// very light tints (b>87) — HSL sat=1.0 can occur on pale tints that
		// are perceptually near-white (e.g. #e2e4ff brightness≈93).
		if sat < 0.07 || b < 3 || b > 87 {
			continue
		}
		score := sat * math.Log2(float64(s.count)+1)
		if score > bestScore {
			bestScore = score
			best = s.hex
		}
	}
	return best
}

// resolveFontVar resolves a CSS var(--font-xxx) reference using the supplied map.
// Chains up to 3 levels deep to handle cross-referencing custom properties.
func resolveFontVar(value string, varMap map[string]string) string {
	current := strings.TrimSpace(value)
	for range 3 {
		lower := strings.ToLower(current)
		if !strings.HasPrefix(lower, "var(--") {
			return current
		}
		inner := lower[len("var("):]
		inner = strings.TrimSuffix(inner, ")")
		inner = strings.SplitN(inner, ",", 2)[0]
		inner = strings.TrimSpace(inner)
		resolved, ok := varMap[inner]
		if !ok {
			return "" // unresolved — cleanFontFamily will drop it
		}
		current = strings.TrimSpace(resolved)
	}
	return current
}

// looksLikeBrandPage fetches the first 4KB of a URL and checks that it is a
// genuine brand/press/design page rather than a soft-404, login screen, or redirect.
// Brand subdomains (brand.*, press.*, newsroom.*) skip this check — their hostname
// is sufficient evidence.
func looksLikeBrandPage(ctx context.Context, client *http.Client, u string) bool {
	// A dedicated brand/press/newsroom subdomain is self-evidently a brand page
	// — skip the content check entirely.
	if parsed, err := url.Parse(u); err == nil {
		host := strings.ToLower(parsed.Hostname())
		sub := strings.SplitN(host, ".", 2)[0]
		switch sub {
		case "brand", "press", "newsroom", "media-kit", "brandbook":
			return true
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Range", "bytes=0-4095")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return false
	}
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	if n < 200 {
		return false
	}
	body := strings.ToLower(string(buf[:n]))
	// Reject soft-404 and auth/login pages.
	for _, marker := range []string{"404", "not found", "page not found", "doesn't exist", "sign in", "log in", "login required"} {
		if strings.Contains(body, marker) {
			return false
		}
	}
	// Multi-word/specific signals are high-confidence on their own.
	strongSignals := []string{
		"media kit", "brand kit", "brand book", "brandbook", "brand guide",
		"brand portal", "brand hub", "brand center", "brand centre",
		"guidelines", "design system", "style guide",
		"brand asset", "brand color", "brand colour", "brand font",
		"press kit", "download logo",
	}
	for _, s := range strongSignals {
		if strings.Contains(body, s) {
			return true
		}
	}
	// Single-word signals require at least 3 co-occurring matches to avoid
	// false positives from pages that merely mention "logo" or "color" in
	// CSS class names or boilerplate (e.g. a generic app page with class="logo-img color-primary").
	weakSignals := []string{"brand", "logo", "colour", "typeface", "typography", "color", "font", "press"}
	count := 0
	for _, s := range weakSignals {
		if strings.Contains(body, s) {
			count++
		}
	}
	return count >= 3
}

// ─── URL helpers ───────────────────────────────────────────────────────────

func resolveURL(base, ref string) string {
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	if strings.HasPrefix(ref, "//") {
		return "https:" + ref
	}
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}

func hostsMatch(a, b string) bool {
	ua, err := url.Parse(a)
	if err != nil {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil {
		return false
	}
	return strings.EqualFold(ua.Hostname(), ub.Hostname())
}
