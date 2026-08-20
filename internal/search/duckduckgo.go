package search

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// DuckDuckGoProvider scrapes DuckDuckGo's HTML endpoint.
// No API key required — works as a zero-config fallback.
type DuckDuckGoProvider struct {
	baseURL string
	deps    Deps
}

func NewDuckDuckGoProvider(deps Deps) *DuckDuckGoProvider {
	return &DuckDuckGoProvider{
		baseURL: "https://html.duckduckgo.com/html",
		deps:    deps,
	}
}

func (d *DuckDuckGoProvider) Name() string { return "duckduckgo" }

func (d *DuckDuckGoProvider) SetBaseURL(url string) { d.baseURL = url }

func (d *DuckDuckGoProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	var results []SearchResult
	err := d.deps.Breaker.Execute(func() error {
		var e error
		results, e = d.doWebSearch(ctx, params)
		return e
	})
	return results, err
}

func (d *DuckDuckGoProvider) Images(_ context.Context, _ ImageSearchParams) ([]ImageResult, error) {
	return nil, nil
}

func (d *DuckDuckGoProvider) News(_ context.Context, _ NewsSearchParams) ([]NewsResult, error) {
	return nil, nil
}

func (d *DuckDuckGoProvider) doWebSearch(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	q := url.Values{}
	q.Set("q", buildQuery(params))
	q.Set("p", "-1") // moderate safe search

	if params.Country != "" {
		q.Set("kl", mapDDGRegion(params.Country))
	}
	if params.TimeRange != "" {
		q.Set("df", mapDDGTimeRange(params.TimeRange))
	}
	if params.Safe == "high" {
		q.Set("p", "1")
	} else if params.Safe == "off" {
		q.Set("p", "-2")
	}

	reqURL := d.baseURL + "/?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := d.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("duckduckgo: rate limited: %w", circuit.ErrRateLimit)
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("duckduckgo: server error %d", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("duckduckgo: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("duckduckgo: read error: %w", err)
	}

	return parseDDGResults(string(body), clamp(params.NumResults, 1, 10))
}

func parseDDGResults(html string, maxResults int) ([]SearchResult, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("duckduckgo: parse error: %w", err)
	}

	var results []SearchResult
	doc.Find(".result__a").Each(func(_ int, s *goquery.Selection) {
		if len(results) >= maxResults {
			return
		}

		title := strings.TrimSpace(s.Text())
		href, exists := s.Attr("href")
		if !exists || title == "" {
			return
		}

		realURL := extractDDGURL(href)
		if realURL == "" {
			return
		}

		var snippet string
		parent := s.Parent()
		if parent.HasClass("result__title") {
			grandparent := parent.Parent()
			snippet = strings.TrimSpace(grandparent.Find(".result__snippet").Text())
		}

		results = append(results, SearchResult{
			Title:       title,
			URL:         realURL,
			Snippet:     snippet,
			DisplayLink: extractDisplayLink(realURL),
		})
	})

	return results, nil
}

func extractDDGURL(redirectURL string) string {
	if !strings.Contains(redirectURL, "uddg=") {
		if strings.HasPrefix(redirectURL, "http") {
			return redirectURL
		}
		return ""
	}

	u, err := url.Parse(redirectURL)
	if err != nil {
		return ""
	}
	if !u.IsAbs() {
		u.Scheme = "https"
		u.Host = "duckduckgo.com"
	}

	uddg := u.Query().Get("uddg")
	if uddg == "" {
		return ""
	}

	decoded, err := url.QueryUnescape(uddg)
	if err != nil {
		return uddg
	}
	return decoded
}

func extractDisplayLink(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Host
}

// ddgRegionByCountry maps an ISO 3166-1 alpha-2 country code to DuckDuckGo's
// own `kl` region code (#641). DDG's codes are NOT simply "<country>-en" —
// most non-English-speaking regions use their local language suffix (e.g.
// Spain is "es-es", Brazil is "br-pt", Russia is "ru-ru"), and a few DDG codes
// don't even match the ISO country code (the UK is "uk", not "gb"; Saudi
// Arabia is "xa", not "sa"). This table is DDG's full officially documented
// region list; countries with more than one regional language (Canada,
// Belgium, Switzerland, Spain) map to their most widely spoken one since a
// single ISO country code can't disambiguate further. Sourced from
// DuckDuckGo's own region list, cross-checked via SerpApi's DDG regions
// reference (2026-08-19).
var ddgRegionByCountry = map[string]string{
	"AR": "ar-es", "AU": "au-en", "AT": "at-de", "BE": "be-nl", "BR": "br-pt",
	"BG": "bg-bg", "CA": "ca-en", "CL": "cl-es", "CN": "cn-zh", "CO": "co-es",
	"HR": "hr-hr", "CZ": "cz-cs", "DK": "dk-da", "EE": "ee-et", "FI": "fi-fi",
	"FR": "fr-fr", "DE": "de-de", "GR": "gr-el", "HK": "hk-tzh", "HU": "hu-hu",
	"IS": "is-is", "IN": "in-en", "ID": "id-en", "IE": "ie-en", "IL": "il-en",
	"IT": "it-it", "JP": "jp-jp", "KR": "kr-kr", "LV": "lv-lv", "LT": "lt-lt",
	"MY": "my-en", "MX": "mx-es", "NL": "nl-nl", "NZ": "nz-en", "NO": "no-no",
	"PK": "pk-en", "PE": "pe-es", "PH": "ph-en", "PL": "pl-pl", "PT": "pt-pt",
	"RO": "ro-ro", "RU": "ru-ru", "SA": "xa-ar", "SG": "sg-en", "SK": "sk-sk",
	"SI": "sl-sl", "ZA": "za-en", "ES": "es-es", "SE": "se-sv", "CH": "ch-de",
	"TW": "tw-tzh", "TH": "th-en", "TR": "tr-tr", "US": "us-en", "UA": "ua-uk",
	"GB": "uk-en", "UK": "uk-en", "VN": "vn-en",
}

// mapDDGRegion resolves an ISO 3166-1 alpha-2 country code to DuckDuckGo's
// `kl` region parameter. A code DDG has no region for falls back to "wt-wt"
// (worldwide/no region bias) rather than guessing an invalid value.
func mapDDGRegion(country string) string {
	if kl, ok := ddgRegionByCountry[strings.ToUpper(country)]; ok {
		return kl
	}
	return "wt-wt"
}

func mapDDGTimeRange(tr string) string {
	switch tr {
	case "day":
		return "d"
	case "week":
		return "w"
	case "month":
		return "m"
	case "year":
		return "y"
	default:
		return ""
	}
}
