package search

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

var _ Provider = (*RedditProvider)(nil)

// RedditProvider searches Reddit posts via the public Atom RSS search feed.
// No API key required — works as a zero-config provider. Circuit breaking is
// handled at the Router layer (see internal/search/router.go), not in-provider.
type RedditProvider struct {
	client  *http.Client
	baseURL string
}

type redditAtomFeed struct {
	XMLName xml.Name          `xml:"feed"`
	Entries []redditAtomEntry `xml:"entry"`
}

type redditAtomEntry struct {
	Title     string             `xml:"title"`
	Links     []redditAtomLink   `xml:"link"`
	Author    redditAtomAuthor   `xml:"author"`
	Published string             `xml:"published"`
	Updated   string             `xml:"updated"`
	Category  redditAtomCategory `xml:"category"`
	Content   string             `xml:"content"`
}

type redditAtomLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

type redditAtomAuthor struct {
	Name string `xml:"name"`
}

type redditAtomCategory struct {
	Term  string `xml:"term,attr"`
	Label string `xml:"label,attr"`
}

func NewRedditProvider(deps Deps) *RedditProvider {
	return &RedditProvider{client: deps.HTTPClient, baseURL: "https://www.reddit.com"}
}

func (p *RedditProvider) Name() string { return "reddit" }

func (p *RedditProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	qp := url.Values{}
	qp.Set("q", params.Query)
	qp.Set("sort", "relevance")
	qp.Set("t", mapRedditTimeRange(params.TimeRange))
	qp.Set("limit", strconv.Itoa(clamp(params.NumResults, 1, 25)))

	reqURL := p.baseURL + "/search.rss?" + qp.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("reddit: %w", err)
	}
	req.Header.Set("User-Agent", "web-researcher-mcp/1.0 (search provider)")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reddit: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("reddit: rate limited: %w", circuit.ErrRateLimit)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("reddit: HTTP %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reddit: %w", err)
	}

	var feed redditAtomFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("reddit: parse feed: %w", err)
	}

	now := time.Now()
	results := make([]SearchResult, 0, len(feed.Entries))
	for _, e := range feed.Entries {
		link := entryLink(e.Links)
		displayLink := "reddit.com"
		if e.Category.Term != "" {
			displayLink = "reddit.com/r/" + e.Category.Term
		}
		results = append(results, SearchResult{
			Title:       e.Title,
			URL:         link,
			Snippet:     fmt.Sprintf("%s · u/%s · %s", e.Category.Label, e.Author.Name, publishedDate(e.Published)),
			DisplayLink: displayLink,
			PublishedAt: normalizePublishedAt(e.Published, now),
		})
	}
	return results, nil
}

func (p *RedditProvider) Images(_ context.Context, _ ImageSearchParams) ([]ImageResult, error) {
	return nil, nil
}

func (p *RedditProvider) News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
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
			Title:       r.Title,
			URL:         r.URL,
			Source:      "reddit",
			PublishedAt: r.PublishedAt,
			Snippet:     r.Snippet,
			Engagement:  r.Engagement,
		})
	}
	return news, nil
}

// entryLink returns the best URL from an Atom entry's <link> elements:
// prefers rel="alternate", falls back to any href, or "" if none present.
func entryLink(links []redditAtomLink) string {
	for _, l := range links {
		if l.Rel == "alternate" {
			return l.Href
		}
	}
	for _, l := range links {
		if l.Href != "" {
			return l.Href
		}
	}
	return ""
}

// mapRedditTimeRange maps a canonical time-range string to Reddit's t=
// parameter. Supported: hour, day, week, month, year. Anything else, including
// empty/"all", defaults to "month".
func mapRedditTimeRange(tr string) string {
	switch tr {
	case "hour", "day", "week", "month", "year":
		return tr
	default:
		return "month"
	}
}

// publishedDate extracts the YYYY-MM-DD portion of an Atom <published> value
// for display in the snippet; returns the raw value unchanged if it can't be
// parsed as RFC3339.
func publishedDate(raw string) string {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.Format("2006-01-02")
	}
	return raw
}
