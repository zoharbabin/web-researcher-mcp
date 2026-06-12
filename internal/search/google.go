package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type GoogleProvider struct {
	apiKey string
	cx     string
	deps   Deps
}

func NewGoogleProvider(apiKey, cx string, deps Deps) *GoogleProvider {
	return &GoogleProvider{apiKey: apiKey, cx: cx, deps: deps}
}

func (g *GoogleProvider) Name() string { return "google" }

func (g *GoogleProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	var results []SearchResult
	err := g.deps.Breaker.Execute(func() error {
		var e error
		results, e = g.doWebSearch(ctx, params)
		return e
	})
	return results, err
}

func (g *GoogleProvider) Images(ctx context.Context, params ImageSearchParams) ([]ImageResult, error) {
	var results []ImageResult
	err := g.deps.Breaker.Execute(func() error {
		var e error
		results, e = g.doImageSearch(ctx, params)
		return e
	})
	return results, err
}

func (g *GoogleProvider) News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	var results []NewsResult
	err := g.deps.Breaker.Execute(func() error {
		var e error
		results, e = g.doNewsSearch(ctx, params)
		return e
	})
	return results, err
}

func (g *GoogleProvider) doWebSearch(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	q := url.Values{}
	q.Set("key", g.apiKey)
	q.Set("cx", g.cx)
	q.Set("q", buildQuery(params))
	q.Set("num", strconv.Itoa(clamp(params.NumResults, 1, 10)))

	if params.Safe != "" {
		q.Set("safe", params.Safe)
	}
	if params.Language != "" {
		q.Set("lr", "lang_"+params.Language)
	}
	if params.Country != "" {
		q.Set("cr", "country"+strings.ToUpper(params.Country))
	}
	if params.ExactTerms != "" {
		q.Set("exactTerms", params.ExactTerms)
	}
	if params.ExcludeTerms != "" {
		q.Set("excludeTerms", params.ExcludeTerms)
	}
	if params.TimeRange != "" {
		q.Set("dateRestrict", mapTimeRange(params.TimeRange))
	}

	apiURL := "https://www.googleapis.com/customsearch/v1?" + q.Encode()
	resp, err := g.doRequest(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	var results []SearchResult
	for _, item := range resp.Items {
		results = append(results, SearchResult{
			Title:       item.Title,
			URL:         item.Link,
			Snippet:     item.Snippet,
			DisplayLink: item.DisplayLink,
		})
	}
	return results, nil
}

func (g *GoogleProvider) doImageSearch(ctx context.Context, params ImageSearchParams) ([]ImageResult, error) {
	q := url.Values{}
	q.Set("key", g.apiKey)
	q.Set("cx", g.cx)
	q.Set("q", params.Query)
	q.Set("searchType", "image")
	q.Set("num", strconv.Itoa(clamp(params.NumResults, 1, 10)))

	if params.Size != "" {
		q.Set("imgSize", params.Size)
	}
	if params.Type != "" {
		q.Set("imgType", params.Type)
	}
	if params.ColorType != "" {
		q.Set("imgColorType", params.ColorType)
	}
	if params.DominantColor != "" {
		q.Set("imgDominantColor", params.DominantColor)
	}
	if params.FileType != "" {
		q.Set("fileType", params.FileType)
	}
	if params.Safe != "" {
		q.Set("safe", params.Safe)
	}

	apiURL := "https://www.googleapis.com/customsearch/v1?" + q.Encode()
	resp, err := g.doRequest(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	var results []ImageResult
	for _, item := range resp.Items {
		result := ImageResult{
			Title:       item.Title,
			Link:        item.Link,
			DisplayLink: item.DisplayLink,
		}
		if item.Image != nil {
			result.ThumbnailLink = item.Image.ThumbnailLink
			result.ContextLink = item.Image.ContextLink
			result.Width = item.Image.Width
			result.Height = item.Image.Height
		}
		results = append(results, result)
	}
	return results, nil
}

func (g *GoogleProvider) doNewsSearch(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	query := params.Query
	if params.Source != "" {
		query += " site:" + params.Source
	}

	q := url.Values{}
	q.Set("key", g.apiKey)
	q.Set("cx", g.cx)
	q.Set("q", query)
	q.Set("num", strconv.Itoa(clamp(params.NumResults, 1, 10)))

	// Honor the requested sort order. Only "date" applies Google's date sort;
	// relevance (the documented default) sends no sort param so the engine uses
	// its native relevance ranking. Previously this was hardcoded to "date",
	// which silently overrode the relevance default.
	if params.SortBy == "date" {
		q.Set("sort", "date")
	}

	if params.Freshness != "" {
		q.Set("dateRestrict", mapTimeRange(params.Freshness))
	}

	apiURL := "https://www.googleapis.com/customsearch/v1?" + q.Encode()
	resp, err := g.doRequest(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	var results []NewsResult
	for _, item := range resp.Items {
		results = append(results, NewsResult{
			Title:       item.Title,
			URL:         item.Link,
			Source:      item.DisplayLink,
			Snippet:     item.Snippet,
			PublishedAt: normalizePublishedAt(item.publishedAt(), time.Now()),
		})
	}
	return results, nil
}

func (g *GoogleProvider) doRequest(ctx context.Context, apiURL string) (*googleResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := g.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("google API rate limited")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("google API error %d: %s", resp.StatusCode, string(body))
	}

	var result googleResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse google response: %w", err)
	}

	return &result, nil
}

// WebWithSiteRestriction performs a site-restricted search (works forever, unaffected by sunset)
func (g *GoogleProvider) WebWithSiteRestriction(ctx context.Context, query string, sites []string, numResults int) ([]SearchResult, error) {
	siteQuery := query
	if len(sites) > 0 {
		siteOps := make([]string, len(sites))
		for i, s := range sites {
			siteOps[i] = "site:" + s
		}
		siteQuery = query + " (" + strings.Join(siteOps, " OR ") + ")"
	}

	return g.doWebSearch(ctx, WebSearchParams{
		Query:      siteQuery,
		NumResults: numResults,
	})
}

type googleResponse struct {
	Items []googleItem `json:"items"`
}

type googleItem struct {
	Title       string         `json:"title"`
	Link        string         `json:"link"`
	Snippet     string         `json:"snippet"`
	DisplayLink string         `json:"displayLink"`
	Image       *googleImage   `json:"image,omitempty"`
	PageMap     *googlePageMap `json:"pagemap,omitempty"`
}

// googlePageMap holds the structured metadata Google CSE attaches to a result.
// Only the publish-date-bearing fields are modeled; everything else is ignored.
type googlePageMap struct {
	MetaTags    []map[string]string `json:"metatags,omitempty"`
	NewsArticle []map[string]string `json:"newsarticle,omitempty"`
}

// publishedAt extracts a best-effort publish timestamp from a result's pagemap,
// checking the common Open Graph / schema.org fields news sites emit. Returns
// "" when none are present (the field is then omitted from the response).
func (it googleItem) publishedAt() string {
	if it.PageMap == nil {
		return ""
	}
	keys := []string{"article:published_time", "datepublished", "og:published_time", "publishdate", "date", "dc.date.issued"}
	for _, tags := range [][]map[string]string{it.PageMap.NewsArticle, it.PageMap.MetaTags} {
		for _, m := range tags {
			for _, k := range keys {
				if v, ok := m[k]; ok && v != "" {
					return v
				}
			}
		}
	}
	return ""
}

type googleImage struct {
	ThumbnailLink string `json:"thumbnailLink"`
	ContextLink   string `json:"contextLink"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
}

func buildQuery(params WebSearchParams) string {
	query := params.Query
	if params.Site != "" {
		query += " site:" + params.Site
	}
	return query
}

func mapTimeRange(tr string) string {
	switch tr {
	case "hour":
		return "d1" // Google only supports day granularity minimum
	case "day":
		return "d1"
	case "week":
		return "w1"
	case "month":
		return "m1"
	case "year":
		return "y1"
	default:
		return ""
	}
}

func clamp(val, min, max int) int {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

// truncateText safely truncates text to maxRunes, appending "..." if truncated.
// Operates on runes to avoid splitting multi-byte UTF-8 characters.
func truncateText(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
