package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

const (
	keenableSearchURL       = "https://api.keenable.ai/v1/search"
	keenableSnippetMaxRunes = 500 // cap both description and snippet fallback, same as Exa
)

// KeenableProvider serves web search from the Keenable Search API
// (POST /v1/search, X-API-Key auth). Web only: Images and News return
// (nil, nil) so the Router falls through to a capable provider.
type KeenableProvider struct {
	apiKey string
	deps   Deps
}

func NewKeenableProvider(apiKey string, deps Deps) *KeenableProvider {
	return &KeenableProvider{apiKey: apiKey, deps: deps}
}

func (k *KeenableProvider) Name() string { return "keenable" }

func (k *KeenableProvider) SupportedOperations() []Operation { return opsWeb }

func (k *KeenableProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	var results []SearchResult
	err := k.deps.Breaker.Execute(func() error {
		var e error
		results, e = k.doWebSearch(ctx, params)
		return e
	})
	return results, err
}

func (k *KeenableProvider) Images(_ context.Context, _ ImageSearchParams) ([]ImageResult, error) {
	return nil, nil
}

func (k *KeenableProvider) News(_ context.Context, _ NewsSearchParams) ([]NewsResult, error) {
	return nil, nil
}

func (k *KeenableProvider) doWebSearch(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	jsonBody, err := json.Marshal(map[string]any{
		"query":       buildQuery(params),
		"max_results": clamp(params.NumResults, 1, 20),
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", keenableSearchURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Key", k.apiKey) // never logged
	req.Header.Set("X-Keenable-Title", "web-researcher-mcp")

	resp, err := k.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("keenable: rate limited: %w", circuit.ErrRateLimit)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("keenable: API error %d: %s", resp.StatusCode, validUTF8Prefix(body))
	}

	var parsed keenableResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 5*1024*1024)).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("keenable: failed to parse response: %w", err)
	}

	var results []SearchResult
	for _, r := range parsed.Results {
		if r.URL == "" {
			continue
		}
		snippet := r.Description
		if snippet == "" {
			snippet = r.Snippet
		}
		snippet = truncateRunes(snippet, keenableSnippetMaxRunes)
		results = append(results, SearchResult{
			Title:       r.Title,
			URL:         r.URL,
			Snippet:     snippet,
			DisplayLink: extractDisplayLink(r.URL),
		})
	}
	return results, nil
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) > max {
		return string(r[:max])
	}
	return s
}

// validUTF8Prefix turns a byte-limited error body into a string without a
// broken trailing rune (a 1024-byte read can end mid-character).
func validUTF8Prefix(b []byte) string {
	return strings.ToValidUTF8(string(b), "")
}

// keenableResponse: results[]{title,url,description,snippet}.
type keenableResponse struct {
	Results []struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		Description string `json:"description"`
		Snippet     string `json:"snippet"`
	} `json:"results"`
}

var _ Provider = (*KeenableProvider)(nil)
