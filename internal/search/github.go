package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

var _ Provider = (*GitHubProvider)(nil)

// GitHubProvider searches GitHub issues and pull requests via the public
// REST Search API (GET /search/issues). Zero-config — no API key required
// (10 req/min unauthenticated; 30 req/min with an optional GITHUB_TOKEN).
// Shares githubAPIRequest (internal/search/github_client.go, #394) for
// headers, auth, and error classification. See issue #282.
type GitHubProvider struct {
	token   string // optional; never logged
	baseURL string
	deps    Deps
}

// NewGitHubProvider creates the provider. token may be "" (unauthenticated,
// subject to GitHub's lower unauth Search API rate limit).
func NewGitHubProvider(token string, deps Deps) *GitHubProvider {
	return &GitHubProvider{token: token, baseURL: githubAPIBaseURL, deps: deps}
}

// SetBaseURL overrides the API base URL (testing).
func (p *GitHubProvider) SetBaseURL(base string) { p.baseURL = base }

func (p *GitHubProvider) Name() string { return "github" }

type ghSearchResponse struct {
	TotalCount int      `json:"total_count"`
	Items      []ghItem `json:"items"`
}

type ghItem struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	HTMLURL string `json:"html_url"`
	State   string `json:"state"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
	CreatedAt string `json:"created_at"`
	Reactions struct {
		TotalCount int `json:"total_count"`
	} `json:"reactions"`
	Comments    int       `json:"comments"`
	PullRequest *struct{} `json:"pull_request"` // non-nil means this item is a PR
}

func (p *GitHubProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	var results []SearchResult
	err := p.deps.Breaker.Execute(func() error {
		var er error
		results, er = p.doSearch(ctx, params)
		return er
	})
	return results, err
}

// doSearch issues one GitHub Search API call and maps the response. Called
// inside deps.Breaker.Execute — holds the actual HTTP logic.
func (p *GitHubProvider) doSearch(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	if params.Query == "" {
		return nil, fmt.Errorf("github: query is required")
	}

	n := params.NumResults
	if n <= 0 || n > 100 {
		n = 10
	}

	q := params.Query
	if tr := mapGHTimeRange(params.TimeRange); tr != "" {
		q += " " + tr
	}

	qp := url.Values{}
	qp.Set("q", q)
	qp.Set("sort", "reactions")
	qp.Set("order", "desc")
	qp.Set("per_page", strconv.Itoa(n))

	body, err := githubAPIRequest(ctx, p.deps.HTTPClient, p.baseURL, "/search/issues?"+qp.Encode(), p.token)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, nil
	}

	var parsed ghSearchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("github: parse: %w", err)
	}

	results := make([]SearchResult, 0, len(parsed.Items))
	for _, it := range parsed.Items {
		kind := "issue"
		if it.PullRequest != nil {
			kind = "pr"
		}
		date := it.CreatedAt
		if len(date) >= 10 {
			date = date[:10]
		}
		results = append(results, SearchResult{
			Title:       it.Title,
			URL:         it.HTMLURL,
			Snippet:     fmt.Sprintf("#%d [%s] %s · %d reactions · %d comments · by %s · %s", it.Number, it.State, kind, it.Reactions.TotalCount, it.Comments, it.User.Login, date),
			DisplayLink: "github.com",
			PublishedAt: normalizePublishedAt(it.CreatedAt, time.Now()),
		})
	}
	return results, nil
}

func (p *GitHubProvider) Images(_ context.Context, _ ImageSearchParams) ([]ImageResult, error) {
	return nil, nil
}

func (p *GitHubProvider) News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
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
			Source:      "github",
			PublishedAt: r.PublishedAt,
			Snippet:     r.Snippet,
		})
	}
	return news, nil
}

// mapGHTimeRange converts a canonical time-range string into a GitHub
// "created:>YYYY-MM-DD" search qualifier. Returns "" for unknown/empty
// values, in which case no qualifier is appended to the query.
func mapGHTimeRange(tr string) string {
	var delta time.Duration
	switch tr {
	case "day":
		delta = 24 * time.Hour
	case "week":
		delta = 7 * 24 * time.Hour
	case "month":
		delta = 30 * 24 * time.Hour
	case "year":
		delta = 365 * 24 * time.Hour
	default:
		return ""
	}
	return "created:>" + time.Now().Add(-delta).Format("2006-01-02")
}
