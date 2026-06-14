package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

var _ Provider = (*HNProvider)(nil)

// HNProvider searches HackerNews stories via the Algolia HN Search API.
// No API key required — works as a zero-config provider.
type HNProvider struct {
	client  *http.Client
	baseURL string
}

type hnAlgoliaResponse struct {
	Hits   []hnAlgoliaHit `json:"hits"`
	NbHits int            `json:"nbHits"`
}

type hnAlgoliaHit struct {
	ObjectID    string   `json:"objectID"`
	StoryID     int      `json:"story_id"`
	Author      string   `json:"author"`
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Points      int      `json:"points"`
	NumComments int      `json:"num_comments"`
	CreatedAt   string   `json:"created_at"`
	Tags        []string `json:"_tags"`
}

func NewHNProvider(deps Deps) *HNProvider {
	return &HNProvider{client: deps.HTTPClient, baseURL: "https://hn.algolia.com/api/v1"}
}

func (p *HNProvider) Name() string { return "hackernews" }

func (p *HNProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	qp := url.Values{}
	qp.Set("query", params.Query)
	n := params.NumResults
	if n <= 0 || n > 100 {
		n = 10
	}
	qp.Set("hitsPerPage", strconv.Itoa(n))
	qp.Set("tags", "story")
	qp.Set("page", "0")

	if params.TimeRange != "" {
		ts := mapHNTimeRange(params.TimeRange)
		if ts != "" {
			qp.Set("numericFilters", "created_at_i>"+ts)
		}
	}

	reqURL := p.baseURL + "/search?" + qp.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("hackernews: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hackernews: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("hackernews: rate limited")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hackernews: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("hackernews: %w", err)
	}

	var algoliaResp hnAlgoliaResponse
	if err := json.Unmarshal(body, &algoliaResp); err != nil {
		return nil, fmt.Errorf("hackernews: %w", err)
	}

	var results []SearchResult
	for _, h := range algoliaResp.Hits {
		itemURL := h.URL
		if itemURL == "" {
			itemURL = fmt.Sprintf("https://news.ycombinator.com/item?id=%d", h.StoryID)
		}
		date := h.CreatedAt
		if len(date) > 10 {
			date = date[:10]
		}
		results = append(results, SearchResult{
			Title:       h.Title,
			URL:         itemURL,
			Snippet:     fmt.Sprintf("%d pts · %d comments · by %s · %s", h.Points, h.NumComments, h.Author, date),
			DisplayLink: "news.ycombinator.com",
		})
	}
	return results, nil
}

func (p *HNProvider) Images(_ context.Context, _ ImageSearchParams) ([]ImageResult, error) {
	return nil, nil
}

func (p *HNProvider) News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	webParams := WebSearchParams{
		Query:      params.Query,
		NumResults: params.NumResults,
		TimeRange:  params.Freshness,
	}
	results, err := p.Web(ctx, webParams)
	if err != nil {
		return nil, err
	}
	news := make([]NewsResult, 0, len(results))
	for _, r := range results {
		news = append(news, NewsResult{
			Title:   r.Title,
			URL:     r.URL,
			Source:  "hackernews",
			Snippet: r.Snippet,
		})
	}
	return news, nil
}

// mapHNTimeRange converts a canonical time-range string to a Unix timestamp
// cutoff string for Algolia's numericFilters parameter.
func mapHNTimeRange(tr string) string {
	now := time.Now().Unix()
	var delta int64
	switch tr {
	case "hour":
		delta = 3600
	case "day":
		delta = 86400
	case "week":
		delta = 7 * 86400
	case "month":
		delta = 30 * 86400
	case "year":
		delta = 365 * 86400
	default:
		return ""
	}
	return strconv.FormatInt(now-delta, 10)
}
