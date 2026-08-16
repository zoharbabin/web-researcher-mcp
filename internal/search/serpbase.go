package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// SerpBaseProvider implements the SerpBase Google Search API
// (https://serpbase.dev) — Google organic results (plus AI Overviews when the
// SERP has them) as JSON, no browser and no scraping. SerpBase uses a POST
// JSON contract with the key in the X-API-Key header; the response envelope
// is {"status": 0, "organic": [...]} where status != 0 is a business error
// (the API returns HTTP 200 with a non-zero status for request-level
// failures). Web search only: the API has no image/news endpoints, so Images
// and News return (nil, nil) and Router fallback handles those operations.
type SerpBaseProvider struct {
	apiKey  string
	baseURL string
	deps    Deps
}

func NewSerpBaseProvider(apiKey string, deps Deps) *SerpBaseProvider {
	return &SerpBaseProvider{
		apiKey:  apiKey,
		baseURL: "https://api.serpbase.dev/google/search",
		deps:    deps,
	}
}

// SetBaseURL overrides the API base URL (testing).
func (p *SerpBaseProvider) SetBaseURL(base string) { p.baseURL = base }

func (p *SerpBaseProvider) Name() string { return "serpbase" }

func (p *SerpBaseProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	var results []SearchResult
	err := p.deps.Breaker.Execute(func() error {
		var e error
		results, e = p.doWebSearch(ctx, params)
		return e
	})
	return results, err
}

func (p *SerpBaseProvider) Images(ctx context.Context, params ImageSearchParams) ([]ImageResult, error) {
	return nil, nil // no image endpoint; Router fallback handles image queries
}

func (p *SerpBaseProvider) News(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	return nil, nil // no news endpoint; Router fallback handles news queries
}

func (p *SerpBaseProvider) doWebSearch(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
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

	respBody, err := p.doRequest(ctx, body)
	if err != nil {
		return nil, err
	}

	var resp serpBaseResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse serpbase response: %w", err)
	}
	if resp.Status != 0 {
		return nil, fmt.Errorf("serpbase API error: status=%d message=%q", resp.Status, resp.Message)
	}

	var results []SearchResult
	for _, r := range resp.Organic {
		results = append(results, SearchResult{
			Title:       r.Title,
			URL:         r.Link,
			Snippet:     r.Snippet,
			DisplayLink: r.DisplayLink,
		})
	}
	return results, nil
}

func (p *SerpBaseProvider) doRequest(ctx context.Context, payload map[string]any) ([]byte, error) {
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", p.apiKey)

	resp, err := p.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("serpbase: rate limited: %w", circuit.ErrRateLimit)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("serpbase API error %d: %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
}

type serpBaseResponse struct {
	Status  int    `json:"status"`
	Message string `json:"message,omitempty"`
	Organic []struct {
		Title       string `json:"title"`
		Link        string `json:"link"`
		URL         string `json:"url"`
		DisplayLink string `json:"display_link"`
		Snippet     string `json:"snippet"`
	} `json:"organic"`
}
