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

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
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
			PublishedAt: normalizePublishedAt(r.Date, time.Now()),
			Snippet:     r.Snippet,
		})
	}
	return results, nil
}

func (s *SearchAPIProvider) Patents(ctx context.Context, params PatentSearchParams) ([]PatentResult, error) {
	var results []PatentResult
	err := s.deps.Breaker.Execute(func() error {
		var e error
		results, e = s.doPatentSearch(ctx, params)
		return e
	})
	return results, err
}

func (s *SearchAPIProvider) doPatentSearch(ctx context.Context, params PatentSearchParams) ([]PatentResult, error) {
	q := url.Values{}
	q.Set("engine", "google_patents")

	// Google Patents supports wildcard query — use * when filtering by assignee/inventor only
	query := params.Query
	if query == "" {
		query = "*"
	}
	q.Set("q", query)

	if params.NumResults > 0 {
		q.Set("num", strconv.Itoa(clamp(params.NumResults, 1, 10)))
	}
	if params.Assignee != "" {
		q.Set("assignee", params.Assignee)
	}
	if params.Inventor != "" {
		q.Set("inventor", params.Inventor)
	}
	if params.PatentOffice != "" && params.PatentOffice != "all" {
		q.Set("countries", params.PatentOffice)
	}
	if params.YearFrom > 0 {
		q.Set("after", fmt.Sprintf("filing:%d0101", params.YearFrom))
	}
	if params.YearTo > 0 {
		q.Set("before", fmt.Sprintf("filing:%d1231", params.YearTo))
	}

	body, err := s.doRequest(ctx, q)
	if err != nil {
		return nil, err
	}

	var resp searchAPIPatentResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("searchapi: failed to parse patent response: %w", err)
	}

	maxResults := clamp(params.NumResults, 1, 10)
	results := make([]PatentResult, 0, maxResults)
	for _, r := range resp.OrganicResults {
		if len(results) >= maxResults {
			break
		}
		number := extractPatentNumber(r.PatentID)
		if number == "" {
			number = extractPatentNumber(r.PublicationNumber)
		}
		result := PatentResult{
			Title:    stripHTMLTags(r.Title),
			Number:   number,
			Abstract: stripHTMLTags(r.Snippet),
			Assignee: stripHTMLTags(r.Assignee),
			Inventor: stripHTMLTags(r.Inventor),
			Filed:    r.FilingDate,
			Granted:  r.GrantDate,
			PDF:      r.PDF,
		}
		if r.Link != "" {
			result.URL = r.Link
		} else if result.Number != "" {
			result.URL = "https://patents.google.com/patent/" + result.Number
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *SearchAPIProvider) doRequest(ctx context.Context, params url.Values) ([]byte, error) {
	params.Set("api_key", s.apiKey)

	reqURL := s.baseURL + "?" + strings.ReplaceAll(params.Encode(), "%2A", "*")
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
		return nil, fmt.Errorf("searchapi: rate limited: %w", circuit.ErrRateLimit)
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

// extractPatentNumber extracts the bare patent number from SearchAPI's patent_id
// format (e.g. "patent/US9270715B2/en" → "US9270715B2").
func extractPatentNumber(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, "/")
	for _, p := range parts {
		if len(p) >= 4 && (p[0] >= 'A' && p[0] <= 'Z') {
			return p
		}
	}
	return raw
}

// stripHTMLTags removes simple HTML tags (e.g. <b>, </b>) from API responses.
func stripHTMLTags(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			b.WriteRune(r)
		}
	}
	return b.String()
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
	Title          string `json:"title"`
	Original       string `json:"original"`
	Thumbnail      string `json:"thumbnail"`
	Source         string `json:"source"`
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

type searchAPIPatentResponse struct {
	OrganicResults []searchAPIPatentResult `json:"organic_results"`
	Error          string                  `json:"error"`
}

type searchAPIPatentResult struct {
	Position          int    `json:"position"`
	Title             string `json:"title"`
	PatentID          string `json:"patent_id"`
	PublicationNumber string `json:"publication_number"`
	Link              string `json:"link"`
	Snippet           string `json:"snippet"`
	Assignee          string `json:"assignee"`
	Inventor          string `json:"inventor"`
	PriorityDate      string `json:"priority_date"`
	FilingDate        string `json:"filing_date"`
	GrantDate         string `json:"grant_date"`
	PublicationDate   string `json:"publication_date"`
	PDF               string `json:"pdf"`
	Language          string `json:"language"`
}
