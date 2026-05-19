package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

type SearchAPIProvider struct {
	apiKey  string
	baseURL string
	deps    Deps
}

func NewSearchAPIProvider(apiKey string, deps Deps) *SearchAPIProvider {
	return &SearchAPIProvider{
		apiKey:  apiKey,
		baseURL: "https://www.searchapi.io/api/v1/search",
		deps:    deps,
	}
}

func (s *SearchAPIProvider) Name() string { return "searchapi" }

// SetBaseURL overrides the API base URL (used in testing).
func (s *SearchAPIProvider) SetBaseURL(url string) { s.baseURL = url }

func (s *SearchAPIProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	var results []SearchResult
	err := s.deps.Breaker.Execute(func() error {
		var e error
		results, e = s.doWebSearch(ctx, params)
		return e
	})
	return results, err
}

func (s *SearchAPIProvider) Images(ctx context.Context, params ImageSearchParams) ([]ImageResult, error) {
	var results []ImageResult
	err := s.deps.Breaker.Execute(func() error {
		var e error
		results, e = s.doImageSearch(ctx, params)
		return e
	})
	return results, err
}

func (s *SearchAPIProvider) News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	var results []NewsResult
	err := s.deps.Breaker.Execute(func() error {
		var e error
		results, e = s.doNewsSearch(ctx, params)
		return e
	})
	return results, err
}

func (s *SearchAPIProvider) doWebSearch(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	q := url.Values{}
	q.Set("engine", "google")
	q.Set("q", buildQuery(params))
	q.Set("num", strconv.Itoa(clamp(params.NumResults, 1, 10)))

	if params.Country != "" {
		q.Set("gl", params.Country)
	}
	if params.Language != "" {
		q.Set("hl", params.Language)
	}
	if params.Safe != "" && params.Safe != "off" {
		q.Set("safe", "active")
	}
	if params.TimeRange != "" {
		q.Set("time_period", mapSearchAPITimePeriod(params.TimeRange))
	}

	body, err := s.doRequest(ctx, q)
	if err != nil {
		return nil, err
	}

	var resp searchAPIWebResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("searchapi: failed to parse response: %w", err)
	}

	results := make([]SearchResult, 0, len(resp.OrganicResults))
	for _, r := range resp.OrganicResults {
		results = append(results, SearchResult{
			Title:       r.Title,
			URL:         r.Link,
			Snippet:     r.Snippet,
			DisplayLink: r.DisplayedLink,
		})
	}
	return results, nil
}

func (s *SearchAPIProvider) doImageSearch(ctx context.Context, params ImageSearchParams) ([]ImageResult, error) {
	q := url.Values{}
	q.Set("engine", "google_images")
	q.Set("q", params.Query)
	q.Set("num", strconv.Itoa(clamp(params.NumResults, 1, 10)))

	if params.Safe != "" && params.Safe != "off" {
		q.Set("safe", "active")
	}

	var tbsParts []string
	if params.Size != "" {
		tbsParts = append(tbsParts, "isz:"+mapSearchAPIImageSize(params.Size))
	}
	if params.Type != "" {
		tbsParts = append(tbsParts, "itp:"+params.Type)
	}
	if params.ColorType != "" {
		tbsParts = append(tbsParts, "ic:"+mapSearchAPIColorType(params.ColorType))
	}
	if params.DominantColor != "" {
		tbsParts = append(tbsParts, "isc:"+params.DominantColor)
	}
	if params.FileType != "" {
		tbsParts = append(tbsParts, "ift:"+params.FileType)
	}
	if len(tbsParts) > 0 {
		tbs := ""
		for i, p := range tbsParts {
			if i > 0 {
				tbs += ","
			}
			tbs += p
		}
		q.Set("tbs", tbs)
	}

	body, err := s.doRequest(ctx, q)
	if err != nil {
		return nil, err
	}

	var resp searchAPIImageResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("searchapi: failed to parse image response: %w", err)
	}

	results := make([]ImageResult, 0, len(resp.Images))
	for _, r := range resp.Images {
		results = append(results, ImageResult{
			Title:         r.Title,
			Link:          r.Original,
			ThumbnailLink: r.Thumbnail,
			DisplayLink:   r.Source,
			Width:         r.OriginalWidth,
			Height:        r.OriginalHeight,
		})
	}
	return results, nil
}

func (s *SearchAPIProvider) doNewsSearch(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	q := url.Values{}
	q.Set("engine", "google_news")
	q.Set("q", params.Query)
	q.Set("num", strconv.Itoa(clamp(params.NumResults, 1, 10)))

	if params.Freshness != "" {
		q.Set("time_period", mapSearchAPITimePeriod(params.Freshness))
	}
	if params.SortBy == "date" {
		q.Set("sort_by", "most_recent")
	}

	body, err := s.doRequest(ctx, q)
	if err != nil {
		return nil, err
	}

	var resp searchAPINewsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("searchapi: failed to parse news response: %w", err)
	}

	results := make([]NewsResult, 0, len(resp.NewsResults))
	for _, r := range resp.NewsResults {
		results = append(results, NewsResult{
			Title:       r.Title,
			URL:         r.Link,
			Source:      r.Source,
			PublishedAt: r.Date,
			Snippet:     r.Snippet,
		})
	}
	return results, nil
}

func (s *SearchAPIProvider) doRequest(ctx context.Context, params url.Values) ([]byte, error) {
	params.Set("api_key", s.apiKey)

	reqURL := s.baseURL + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")

	resp, err := s.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("searchapi: rate limited")
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("searchapi: authentication failed (check SEARCHAPI_API_KEY)")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("searchapi: API error %d: %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
}

func mapSearchAPITimePeriod(tr string) string {
	switch tr {
	case "hour":
		return "last_hour"
	case "day":
		return "last_day"
	case "week":
		return "last_week"
	case "month":
		return "last_month"
	case "year":
		return "last_year"
	default:
		return ""
	}
}

func mapSearchAPIImageSize(size string) string {
	switch size {
	case "icon":
		return "i"
	case "small":
		return "s"
	case "medium":
		return "m"
	case "large":
		return "l"
	case "xlarge", "xxlarge", "huge":
		return "lt"
	default:
		return ""
	}
}

func mapSearchAPIColorType(ct string) string {
	switch ct {
	case "color":
		return "color"
	case "gray":
		return "gray"
	case "mono":
		return "gray"
	case "trans":
		return "trans"
	default:
		return ""
	}
}

// Response types

type searchAPIWebResponse struct {
	OrganicResults []searchAPIOrganicResult `json:"organic_results"`
}

type searchAPIOrganicResult struct {
	Position      int    `json:"position"`
	Title         string `json:"title"`
	Link          string `json:"link"`
	Snippet       string `json:"snippet"`
	DisplayedLink string `json:"displayed_link"`
}

type searchAPIImageResponse struct {
	Images []searchAPIImageResult `json:"images_results"`
}

type searchAPIImageResult struct {
	Title         string `json:"title"`
	Original      string `json:"original"`
	Thumbnail     string `json:"thumbnail"`
	Source        string `json:"source"`
	OriginalWidth  int    `json:"original_width"`
	OriginalHeight int    `json:"original_height"`
}

type searchAPINewsResponse struct {
	NewsResults []searchAPINewsResult `json:"news_results"`
}

type searchAPINewsResult struct {
	Title   string `json:"title"`
	Link    string `json:"link"`
	Source  string `json:"source"`
	Date    string `json:"date"`
	Snippet string `json:"snippet"`
}
