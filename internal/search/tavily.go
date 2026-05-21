package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type TavilyProvider struct {
	apiKey string
	deps   Deps
}

func NewTavilyProvider(apiKey string, deps Deps) *TavilyProvider {
	return &TavilyProvider{apiKey: apiKey, deps: deps}
}

func (t *TavilyProvider) Name() string { return "tavily" }

func (t *TavilyProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	var results []SearchResult
	err := t.deps.Breaker.Execute(func() error {
		var e error
		results, e = t.doWebSearch(ctx, params)
		return e
	})
	return results, err
}

func (t *TavilyProvider) Images(_ context.Context, _ ImageSearchParams) ([]ImageResult, error) {
	return nil, fmt.Errorf("tavily provider does not support image search")
}

func (t *TavilyProvider) News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	var results []NewsResult
	err := t.deps.Breaker.Execute(func() error {
		var e error
		results, e = t.doNewsSearch(ctx, params)
		return e
	})
	return results, err
}

func (t *TavilyProvider) doWebSearch(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	reqBody := tavilyRequest{
		APIKey:      t.apiKey,
		Query:       buildQuery(params),
		MaxResults:  clamp(params.NumResults, 1, 20),
		SearchDepth: "basic",
		Topic:       "general",
	}

	body, err := t.doRequest(ctx, reqBody)
	if err != nil {
		return nil, err
	}

	var resp tavilyResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse tavily response: %w", err)
	}

	var results []SearchResult
	for _, r := range resp.Results {
		results = append(results, SearchResult{
			Title:       r.Title,
			URL:         r.URL,
			Snippet:     r.Content,
			DisplayLink: r.URL,
		})
	}
	return results, nil
}

func (t *TavilyProvider) doNewsSearch(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	reqBody := tavilyRequest{
		APIKey:      t.apiKey,
		Query:       params.Query,
		MaxResults:  clamp(params.NumResults, 1, 20),
		SearchDepth: "basic",
		Topic:       "news",
	}

	body, err := t.doRequest(ctx, reqBody)
	if err != nil {
		return nil, err
	}

	var resp tavilyResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse tavily news response: %w", err)
	}

	var results []NewsResult
	for _, r := range resp.Results {
		results = append(results, NewsResult{
			Title:       r.Title,
			URL:         r.URL,
			Source:      r.URL,
			PublishedAt: r.PublishedDate,
			Snippet:     r.Content,
		})
	}
	return results, nil
}

func (t *TavilyProvider) doRequest(ctx context.Context, reqBody tavilyRequest) ([]byte, error) {
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("tavily: failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := t.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("tavily API rate limited")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("tavily API error %d: %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
}

type tavilyRequest struct {
	APIKey      string `json:"api_key"`
	Query       string `json:"query"`
	SearchDepth string `json:"search_depth,omitempty"`
	MaxResults  int    `json:"max_results,omitempty"`
	Topic       string `json:"topic,omitempty"`
}

type tavilyResponse struct {
	Results []struct {
		Title         string  `json:"title"`
		URL           string  `json:"url"`
		Content       string  `json:"content"`
		Score         float64 `json:"score"`
		PublishedDate string  `json:"published_date,omitempty"`
	} `json:"results"`
}

// Ensure TavilyProvider implements Provider at compile time.
var _ Provider = (*TavilyProvider)(nil)
