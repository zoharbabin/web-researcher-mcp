package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type SearXNGProvider struct {
	baseURL   string
	basicAuth string            // "user:password"; "" => no Basic auth
	headers   map[string]string // validated static headers; nil/empty => none
	deps      Deps
}

func NewSearXNGProvider(baseURL, basicAuth string, headers map[string]string, deps Deps) *SearXNGProvider {
	return &SearXNGProvider{baseURL: baseURL, basicAuth: basicAuth, headers: headers, deps: deps}
}

func (s *SearXNGProvider) Name() string { return "searxng" }

func (s *SearXNGProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	q := url.Values{}
	q.Set("q", buildQuery(params))
	q.Set("format", "json")
	q.Set("categories", "general")

	if params.Language != "" {
		q.Set("language", params.Language)
	}
	if params.TimeRange != "" {
		q.Set("time_range", mapSearXNGTimeRange(params.TimeRange))
	}

	apiURL := s.baseURL + "/search?" + q.Encode()
	body, err := s.doRequest(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	var resp searxngResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse searxng response: %w", err)
	}

	num := clamp(params.NumResults, 1, 10)
	var results []SearchResult
	for i, r := range resp.Results {
		if i >= num {
			break
		}
		results = append(results, SearchResult{
			Title:       r.Title,
			URL:         r.URL,
			Snippet:     r.Content,
			DisplayLink: r.URL,
			PublishedAt: normalizePublishedAt(r.PublishedDate, time.Now()),
		})
	}
	return results, nil
}

func (s *SearXNGProvider) Images(ctx context.Context, params ImageSearchParams) ([]ImageResult, error) {
	q := url.Values{}
	q.Set("q", params.Query)
	q.Set("format", "json")
	q.Set("categories", "images")

	apiURL := s.baseURL + "/search?" + q.Encode()
	body, err := s.doRequest(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	var resp searxngResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse searxng image response: %w", err)
	}

	num := clamp(params.NumResults, 1, 10)
	var results []ImageResult
	for i, r := range resp.Results {
		if i >= num {
			break
		}
		results = append(results, ImageResult{
			Title:         r.Title,
			Link:          r.ImgSrc,
			ThumbnailLink: r.ThumbnailSrc,
			DisplayLink:   r.URL,
		})
	}
	return results, nil
}

func (s *SearXNGProvider) News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	q := url.Values{}
	q.Set("q", params.Query)
	q.Set("format", "json")
	q.Set("categories", "news")

	if params.Freshness != "" {
		q.Set("time_range", mapSearXNGTimeRange(params.Freshness))
	}

	apiURL := s.baseURL + "/search?" + q.Encode()
	body, err := s.doRequest(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	var resp searxngResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse searxng news response: %w", err)
	}

	num := clamp(params.NumResults, 1, 10)
	var results []NewsResult
	for i, r := range resp.Results {
		if i >= num {
			break
		}
		results = append(results, NewsResult{
			Title:       r.Title,
			URL:         r.URL,
			Source:      r.Engine,
			PublishedAt: normalizePublishedAt(r.PublishedDate, time.Now()),
			Snippet:     r.Content,
		})
	}
	return results, nil
}

func (s *SearXNGProvider) doRequest(ctx context.Context, apiURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	// Auth injection (operator infrastructure credentials, validated at config
	// load). Basic auth first; custom headers after, so a custom Authorization
	// header in SEARXNG_HEADERS deterministically overrides Basic (last wins).
	// The user/pass non-empty guard mirrors config.splitSearXNGBasicAuth so a
	// half-formed credential (e.g. "user:") is never put on the wire even in
	// STDIO mode, where config errors are logged-but-not-fatal. s.headers only
	// ever holds entries that passed config validation, so the loop cannot
	// inject a malformed header.
	if user, pass, found := strings.Cut(s.basicAuth, ":"); found && user != "" && pass != "" {
		req.SetBasicAuth(user, pass)
	}
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}

	resp, err := s.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("searxng error %d", resp.StatusCode)
	}

	return io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
}

func mapSearXNGTimeRange(tr string) string {
	switch tr {
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

type searxngResponse struct {
	Results []searxngResult `json:"results"`
}

type searxngResult struct {
	Title         string `json:"title"`
	URL           string `json:"url"`
	Content       string `json:"content"`
	Engine        string `json:"engine"`
	PublishedDate string `json:"publishedDate,omitempty"`
	ImgSrc        string `json:"img_src,omitempty"`
	ThumbnailSrc  string `json:"thumbnail_src,omitempty"`
}
