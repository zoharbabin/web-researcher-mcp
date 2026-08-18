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

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

const (
	xquikBaseURL    = "https://xquik.com/api/v1"
	xquikMaxResults = 10
)

// XQuikProvider searches public X posts through Xquik's REST API.
type XQuikProvider struct {
	apiKey  string
	baseURL string
	deps    Deps
}

var _ Provider = (*XQuikProvider)(nil)

// NewXQuikProvider creates an Xquik provider. The API key is never logged.
func NewXQuikProvider(apiKey string, deps Deps) *XQuikProvider {
	return &XQuikProvider{apiKey: apiKey, baseURL: xquikBaseURL, deps: deps}
}

// SetBaseURL overrides the API base URL for tests.
func (p *XQuikProvider) SetBaseURL(base string) { p.baseURL = base }

func (p *XQuikProvider) Name() string { return "xquik" }

func (p *XQuikProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	var results []SearchResult
	err := p.deps.Breaker.Execute(func() error {
		var err error
		results, err = p.search(ctx, params.Query, params.NumResults, "Top")
		return err
	})
	return results, err
}

func (p *XQuikProvider) Images(_ context.Context, _ ImageSearchParams) ([]ImageResult, error) {
	return nil, nil
}

func (p *XQuikProvider) News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	var tweets []xquikTweet
	err := p.deps.Breaker.Execute(func() error {
		var err error
		tweets, err = p.fetch(ctx, params.Query, params.NumResults, "Latest")
		return err
	})
	if err != nil {
		return nil, err
	}

	now := time.Now()
	results := make([]NewsResult, 0, len(tweets))
	for _, tweet := range tweets {
		id := tweet.identifier()
		if id == "" || tweet.Author.Username == "" {
			continue
		}
		results = append(results, NewsResult{
			Title:       truncateText(tweet.Text, 120),
			URL:         xquikTweetURL(tweet.Author.Username, id),
			Source:      "@" + tweet.Author.Username,
			PublishedAt: normalizePublishedAt(tweet.CreatedAt, now),
			Snippet:     tweet.Text,
			Engagement:  tweet.engagement(),
		})
	}
	return results, nil
}

func (p *XQuikProvider) search(ctx context.Context, query string, numResults int, queryType string) ([]SearchResult, error) {
	tweets, err := p.fetch(ctx, query, numResults, queryType)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	results := make([]SearchResult, 0, len(tweets))
	for _, tweet := range tweets {
		id := tweet.identifier()
		if id == "" || tweet.Author.Username == "" {
			continue
		}
		results = append(results, SearchResult{
			Title:       truncateText(tweet.Text, 120),
			URL:         xquikTweetURL(tweet.Author.Username, id),
			Snippet:     tweet.Text,
			DisplayLink: "x.com",
			PublishedAt: normalizePublishedAt(tweet.CreatedAt, now),
			Engagement:  tweet.engagement(),
		})
	}
	return results, nil
}

func (p *XQuikProvider) fetch(ctx context.Context, query string, numResults int, queryType string) ([]xquikTweet, error) {
	if query == "" {
		return nil, fmt.Errorf("xquik: query is required")
	}

	limit := numResults
	if limit <= 0 || limit > xquikMaxResults {
		limit = xquikMaxResults
	}
	params := url.Values{}
	params.Set("q", query)
	params.Set("queryType", queryType)
	params.Set("limit", strconv.Itoa(limit))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/x/tweets/search?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("xquik: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", p.apiKey)

	resp, err := p.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xquik: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("xquik: rate limited: %w", circuit.ErrRateLimit)
	}
	if resp.StatusCode == http.StatusPaymentRequired {
		return nil, fmt.Errorf("xquik: HTTP %d: credits required", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xquik: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("xquik: read response: %w", err)
	}
	var result xquikSearchResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("xquik: parse response: %w", err)
	}
	return result.Tweets, nil
}

type xquikSearchResponse struct {
	Tweets []xquikTweet `json:"tweets"`
}

type xquikTweet struct {
	ID            string      `json:"id"`
	LegacyTweetID string      `json:"tweetId"`
	Text          string      `json:"text"`
	Author        xquikAuthor `json:"author"`
	CreatedAt     string      `json:"createdAt"`
	LikeCount     int         `json:"likeCount"`
	RetweetCount  int         `json:"retweetCount"`
	ReplyCount    int         `json:"replyCount"`
	ViewCount     int         `json:"viewCount"`
}

type xquikAuthor struct {
	Username string `json:"username"`
}

func (t xquikTweet) identifier() string {
	if t.ID != "" {
		return t.ID
	}
	return t.LegacyTweetID
}

func (t xquikTweet) engagement() *EngagementSignals {
	return &EngagementSignals{
		LikeCount:   t.LikeCount,
		RepostCount: t.RetweetCount,
		ReplyCount:  t.ReplyCount,
		ViewCount:   t.ViewCount,
	}
}

func xquikTweetURL(username, id string) string {
	return "https://x.com/" + url.PathEscape(username) + "/status/" + url.PathEscape(id)
}
