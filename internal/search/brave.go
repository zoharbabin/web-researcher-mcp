package search

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type BraveProvider struct {
	apiKey string
	deps   Deps
}

func NewBraveProvider(apiKey string, deps Deps) *BraveProvider {
	return &BraveProvider{apiKey: apiKey, deps: deps}
}

func (b *BraveProvider) Name() string { return "brave" }

func (b *BraveProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	var results []SearchResult
	err := b.deps.Breaker.Execute(func() error {
		var e error
		results, e = b.doWebSearch(ctx, params)
		return e
	})
	return results, err
}

func (b *BraveProvider) Images(ctx context.Context, params ImageSearchParams) ([]ImageResult, error) {
	var results []ImageResult
	err := b.deps.Breaker.Execute(func() error {
		var e error
		results, e = b.doImageSearch(ctx, params)
		return e
	})
	return results, err
}

func (b *BraveProvider) News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	var results []NewsResult
	err := b.deps.Breaker.Execute(func() error {
		var e error
		results, e = b.doNewsSearch(ctx, params)
		return e
	})
	return results, err
}

func (b *BraveProvider) doWebSearch(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	q := url.Values{}
	q.Set("q", buildQuery(params))
	q.Set("count", strconv.Itoa(clamp(params.NumResults, 1, 10)))

	if params.Country != "" {
		q.Set("country", params.Country)
	}
	if params.Language != "" {
		q.Set("search_lang", params.Language)
	}
	if params.TimeRange != "" {
		q.Set("freshness", mapBraveFreshness(params.TimeRange))
	}
	if params.Safe != "" && params.Safe != "off" {
		q.Set("safesearch", "moderate")
	}

	apiURL := "https://api.search.brave.com/res/v1/web/search?" + q.Encode()
	body, err := b.doRequest(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	var resp braveWebResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse brave response: %w", err)
	}

	var results []SearchResult
	for _, r := range resp.Web.Results {
		results = append(results, SearchResult{
			Title:       r.Title,
			URL:         r.URL,
			Snippet:     r.Description,
			DisplayLink: r.URL,
		})
	}
	return results, nil
}

func (b *BraveProvider) doImageSearch(ctx context.Context, params ImageSearchParams) ([]ImageResult, error) {
	q := url.Values{}
	q.Set("q", params.Query)
	q.Set("count", strconv.Itoa(clamp(params.NumResults, 1, 10)))

	if params.Size != "" {
		q.Set("size", params.Size)
	}
	if params.Type != "" {
		q.Set("type", params.Type)
	}

	apiURL := "https://api.search.brave.com/res/v1/images/search?" + q.Encode()
	body, err := b.doRequest(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	var resp braveImageResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse brave image response: %w", err)
	}

	var results []ImageResult
	for _, r := range resp.Results {
		results = append(results, ImageResult{
			Title:         r.Title,
			Link:          r.URL,
			ThumbnailLink: r.Thumbnail.Src,
			DisplayLink:   r.Source,
		})
	}
	return results, nil
}

func (b *BraveProvider) doNewsSearch(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	q := url.Values{}
	q.Set("q", params.Query)
	q.Set("count", strconv.Itoa(clamp(params.NumResults, 1, 10)))

	if params.Freshness != "" {
		q.Set("freshness", mapBraveFreshness(params.Freshness))
	}

	apiURL := "https://api.search.brave.com/res/v1/news/search?" + q.Encode()
	body, err := b.doRequest(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	var resp braveNewsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse brave news response: %w", err)
	}

	var results []NewsResult
	for _, r := range resp.Results {
		results = append(results, NewsResult{
			Title:       r.Title,
			URL:         r.URL,
			Source:      r.MetaURL.Hostname,
			PublishedAt: normalizePublishedAt(r.Age, time.Now()),
			Snippet:     r.Description,
		})
	}
	return results, nil
}

func (b *BraveProvider) doRequest(ctx context.Context, apiURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("X-Subscription-Token", b.apiKey)

	resp, err := b.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("brave API rate limited")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("brave API error %d: %s", resp.StatusCode, string(body))
	}

	var reader io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("brave: failed to decompress response: %w", err)
		}
		defer gr.Close()
		reader = gr
	}

	return io.ReadAll(io.LimitReader(reader, 5*1024*1024))
}

func mapBraveFreshness(tr string) string {
	switch tr {
	case "hour":
		return "pd"
	case "day":
		return "pd"
	case "week":
		return "pw"
	case "month":
		return "pm"
	case "year":
		return "py"
	default:
		return ""
	}
}

type braveWebResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

type braveImageResponse struct {
	Results []struct {
		Title     string `json:"title"`
		URL       string `json:"url"`
		Source    string `json:"source"`
		Thumbnail struct {
			Src string `json:"src"`
		} `json:"thumbnail"`
	} `json:"results"`
}

type braveNewsResponse struct {
	Results []struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		Description string `json:"description"`
		Age         string `json:"age"`
		MetaURL     struct {
			Hostname string `json:"hostname"`
		} `json:"meta_url"`
	} `json:"results"`
}
