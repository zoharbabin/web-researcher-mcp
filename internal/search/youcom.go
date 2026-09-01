package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

const (
	youComSearchURL = "https://ydc-index.io/v1/search"
)

// YouComProvider uses You.com's Search API as an optional web and news source.
// It is intentionally read-only and degrades gracefully when a sub-capability
// is unavailable.
type YouComProvider struct {
	apiKey  string
	baseURL string
	deps    Deps
}

var _ Provider = (*YouComProvider)(nil)

func NewYouComProvider(apiKey string, deps Deps) *YouComProvider {
	return &YouComProvider{
		apiKey:  apiKey,
		baseURL: youComSearchURL,
		deps:    deps,
	}
}

// SetBaseURL overrides the API endpoint for tests.
func (y *YouComProvider) SetBaseURL(url string) { y.baseURL = url }

func (y *YouComProvider) Name() string { return "youcom" }

func (y *YouComProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	var results []SearchResult
	err := y.deps.Breaker.Execute(func() error {
		var e error
		results, e = y.doWebSearch(ctx, params)
		return e
	})
	return results, err
}

func (y *YouComProvider) Images(_ context.Context, _ ImageSearchParams) ([]ImageResult, error) {
	return nil, nil
}

func (y *YouComProvider) News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	var results []NewsResult
	err := y.deps.Breaker.Execute(func() error {
		var e error
		results, e = y.doNewsSearch(ctx, params)
		return e
	})
	return results, err
}

func (y *YouComProvider) doWebSearch(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	body := map[string]any{
		"query":      buildQuery(params),
		"count":      clamp(params.NumResults, 1, 10),
		"offset":     clamp(params.Offset, 0, 9),
		"safesearch": mapYouComSafeSearch(params.Safe),
		"language":   params.Language,
		"country":    strings.ToUpper(params.Country),
		"freshness":  mapYouComFreshness(params.TimeRange),
	}
	if body["safesearch"] == "" {
		delete(body, "safesearch")
	}
	if body["language"] == "" {
		delete(body, "language")
	}
	if body["country"] == "" {
		delete(body, "country")
	}
	if body["freshness"] == "" {
		delete(body, "freshness")
	}

	var resp youComResponse
	if err := y.doRequest(ctx, body, &resp); err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(resp.Results.Web))
	for _, r := range resp.Results.Web {
		snippet, extra := pickYouComSnippet(r.Snippets, r.Description)
		results = append(results, SearchResult{
			Title:         r.Title,
			URL:           r.URL,
			Snippet:       snippet,
			DisplayLink:   extractDisplayLink(r.URL),
			ExtraSnippets: extra,
		})
	}
	return results, nil
}

func (y *YouComProvider) doNewsSearch(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	body := map[string]any{
		"query":      params.Query,
		"count":      clamp(params.NumResults, 1, 10),
		"offset":     clamp(params.Offset, 0, 9),
		"safesearch": mapYouComSafeSearch(params.Safe),
		"language":   params.Language,
		"country":    strings.ToUpper(params.Country),
		"freshness":  mapYouComFreshness(params.Freshness),
	}
	if body["safesearch"] == "" {
		delete(body, "safesearch")
	}
	if body["language"] == "" {
		delete(body, "language")
	}
	if body["country"] == "" {
		delete(body, "country")
	}
	if body["freshness"] == "" {
		delete(body, "freshness")
	}

	var resp youComResponse
	if err := y.doRequest(ctx, body, &resp); err != nil {
		return nil, err
	}

	results := make([]NewsResult, 0, len(resp.Results.News))
	for _, r := range resp.Results.News {
		results = append(results, NewsResult{
			Title:       r.Title,
			URL:         r.URL,
			Source:      extractDisplayLink(r.URL),
			PublishedAt: normalizePublishedAt(r.PageAge, time.Now()),
			Snippet:     r.Description,
		})
	}
	return results, nil
}

func (y *YouComProvider) doRequest(ctx context.Context, payload map[string]any, out any) error {
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, y.baseURL, bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "web-researcher-mcp/1.0 (search provider)")
	req.Header.Set("X-API-Key", y.apiKey)

	resp, err := y.deps.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("you.com search API rate limited: %w", circuit.ErrRateLimit)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("you.com search API error %d: %s", resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

func mapYouComFreshness(value string) string {
	switch value {
	case "hour", "day":
		return "day"
	case "week":
		return "week"
	case "month":
		return "month"
	case "year":
		return "year"
	default:
		return ""
	}
}

func mapYouComSafeSearch(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "off":
		return "off"
	case "high", "strict":
		return "strict"
	case "moderate":
		return "moderate"
	default:
		return ""
	}
}

func pickYouComSnippet(snippets []string, description string) (string, []string) {
	cleaned := make([]string, 0, len(snippets))
	for _, s := range snippets {
		if s = strings.TrimSpace(s); s != "" {
			cleaned = append(cleaned, s)
		}
	}
	if len(cleaned) > 0 {
		return cleaned[0], cleaned[1:]
	}
	if desc := strings.TrimSpace(description); desc != "" {
		return desc, nil
	}
	return "", nil
}

type youComResponse struct {
	Results struct {
		Web  []youComWebResult  `json:"web"`
		News []youComNewsResult `json:"news"`
	} `json:"results"`
}

type youComWebResult struct {
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Snippets    []string `json:"snippets"`
}

type youComNewsResult struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	PageAge     string `json:"page_age"`
}
