package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

type SerperProvider struct {
	apiKey string
	deps   Deps
}

func NewSerperProvider(apiKey string, deps Deps) *SerperProvider {
	return &SerperProvider{apiKey: apiKey, deps: deps}
}

func (s *SerperProvider) Name() string { return "serper" }

func (s *SerperProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	var results []SearchResult
	err := s.deps.Breaker.Execute(func() error {
		var e error
		results, e = s.doWebSearch(ctx, params)
		return e
	})
	return results, err
}

func (s *SerperProvider) Images(ctx context.Context, params ImageSearchParams) ([]ImageResult, error) {
	var results []ImageResult
	err := s.deps.Breaker.Execute(func() error {
		var e error
		results, e = s.doImageSearch(ctx, params)
		return e
	})
	return results, err
}

func (s *SerperProvider) News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	var results []NewsResult
	err := s.deps.Breaker.Execute(func() error {
		var e error
		results, e = s.doNewsSearch(ctx, params)
		return e
	})
	return results, err
}

func (s *SerperProvider) doWebSearch(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	body := map[string]any{
		"q":   buildQuery(params),
		"num": clamp(params.NumResults, 1, 10),
	}
	if params.Country != "" {
		body["gl"] = params.Country
	}
	if params.Language != "" {
		body["hl"] = params.Language
	}

	respBody, err := s.doRequest(ctx, "https://google.serper.dev/search", body)
	if err != nil {
		return nil, err
	}

	var resp serperWebResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse serper response: %w", err)
	}

	var results []SearchResult
	for _, r := range resp.Organic {
		results = append(results, SearchResult{
			Title:       r.Title,
			URL:         r.Link,
			Snippet:     r.Snippet,
			DisplayLink: r.Link,
		})
	}
	return results, nil
}

func (s *SerperProvider) doImageSearch(ctx context.Context, params ImageSearchParams) ([]ImageResult, error) {
	body := map[string]any{
		"q":   params.Query,
		"num": clamp(params.NumResults, 1, 10),
	}

	respBody, err := s.doRequest(ctx, "https://google.serper.dev/images", body)
	if err != nil {
		return nil, err
	}

	var resp serperImageResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse serper image response: %w", err)
	}

	var results []ImageResult
	for _, r := range resp.Images {
		results = append(results, ImageResult{
			Title:         r.Title,
			Link:          r.ImageURL,
			ThumbnailLink: r.ThumbnailURL,
			DisplayLink:   r.Source,
		})
	}
	return results, nil
}

func (s *SerperProvider) doNewsSearch(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	body := map[string]any{
		"q":   params.Query,
		"num": clamp(params.NumResults, 1, 10),
	}

	respBody, err := s.doRequest(ctx, "https://google.serper.dev/news", body)
	if err != nil {
		return nil, err
	}

	var resp serperNewsResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse serper news response: %w", err)
	}

	var results []NewsResult
	for _, r := range resp.News {
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

func (s *SerperProvider) doRequest(ctx context.Context, apiURL string, payload map[string]any) ([]byte, error) {
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-KEY", s.apiKey)

	resp, err := s.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("serper: rate limited: %w", circuit.ErrRateLimit)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("serper API error %d: %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
}

type serperWebResponse struct {
	Organic []struct {
		Title   string `json:"title"`
		Link    string `json:"link"`
		Snippet string `json:"snippet"`
	} `json:"organic"`
}

type serperImageResponse struct {
	Images []struct {
		Title        string `json:"title"`
		ImageURL     string `json:"imageUrl"`
		ThumbnailURL string `json:"thumbnailUrl"`
		Source       string `json:"source"`
	} `json:"images"`
}

type serperNewsResponse struct {
	News []struct {
		Title   string `json:"title"`
		Link    string `json:"link"`
		Snippet string `json:"snippet"`
		Source  string `json:"source"`
		Date    string `json:"date"`
	} `json:"news"`
}
