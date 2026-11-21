package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// ContextDevProvider supplies web-search snippets, independently of Brave's ContextProvider capability.
type ContextDevProvider struct {
	apiKey string
	deps   Deps
}

func NewContextDevProvider(apiKey string, deps Deps) *ContextDevProvider {
	return &ContextDevProvider{apiKey: apiKey, deps: deps}
}

func (c *ContextDevProvider) Name() string { return "context" }

func (c *ContextDevProvider) SupportedOperations() []Operation { return opsWeb }

func (c *ContextDevProvider) Images(_ context.Context, _ ImageSearchParams) ([]ImageResult, error) {
	return nil, nil
}

func (c *ContextDevProvider) News(_ context.Context, _ NewsSearchParams) ([]NewsResult, error) {
	return nil, nil
}

func (c *ContextDevProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	query := buildQuery(params)
	if strings.TrimSpace(query) == "" || utf8.RuneCountInString(query) > 500 {
		return nil, errors.New("context: query including site filters must contain 1–500 characters")
	}
	body := map[string]any{
		"query": query, "numResults": clamp(params.NumResults, 1, 100),
		"highlightsOptions": map[string]any{"enabled": true, "maxCharacters": 1000},
		"timeoutOpts":       map[string]any{"milliseconds": 25000, "behavior": "fail"},
	}
	switch params.TimeRange {
	case "":
	case "day":
		body["freshness"] = "last_24_hours"
	case "week", "month", "year":
		body["freshness"] = "last_" + params.TimeRange
	default:
		return nil, errors.New("context: time_range supports day, week, month, or year")
	}
	if params.Country != "" {
		body["country"] = strings.ToLower(params.Country)
	}
	if params.Language != "" {
		body["languages"] = []string{strings.ToLower(params.Language)}
	}

	var results []SearchResult
	err := c.deps.Breaker.Execute(func() error {
		payload, err := json.Marshal(body)
		if err != nil {
			return errors.New("context: invalid search request")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.context.dev/v1/web/search", bytes.NewReader(payload))
		if err != nil {
			return errors.New("context: invalid search request")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		client := *c.deps.HTTPClient
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
		resp, err := client.Do(req)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return context.Canceled
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return context.DeadlineExceeded
			}
			// Transport errors may contain credentials or upstream payloads; do not wrap them.
			return errors.New("context: API request failed")
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf("context: API rate limited: %w", circuit.ErrRateLimit)
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("context: API returned HTTP %d", resp.StatusCode)
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024+1))
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		if err != nil || len(raw) > 5*1024*1024 {
			return errors.New("context: could not read a bounded search response")
		}
		var data struct {
			Partial json.RawMessage `json:"partial"`
			Results []struct {
				Title       string  `json:"title"`
				URL         string  `json:"url"`
				Description *string `json:"description"`
				Highlights  *struct {
					Code       string    `json:"code"`
					Highlights *[]string `json:"highlights"`
				} `json:"highlights"`
			} `json:"results"`
		}
		if json.Unmarshal(raw, &data) != nil || data.Results == nil {
			return errors.New("context: invalid search response")
		}
		if len(data.Partial) > 0 && string(data.Partial) != "false" {
			return errors.New("context: incomplete or invalid search response")
		}
		results = make([]SearchResult, 0, len(data.Results))
		for _, item := range data.Results[:min(len(data.Results), clamp(params.NumResults, 1, 100))] {
			u, err := url.Parse(item.URL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || strings.TrimSpace(item.Title) == "" {
				return errors.New("context: invalid source URL or title")
			}
			snippet := ""
			if h := item.Highlights; h != nil {
				switch h.Code {
				case "SUCCESS":
					if h.Highlights == nil {
						return errors.New("context: invalid Highlights")
					}
					passages := *h.Highlights
					for i := range passages {
						passages[i] = strings.TrimSpace(passages[i])
					}
					snippet = strings.TrimSpace(strings.Join(passages, "\n"))
				case "NOT_REQUESTED", "TIMEOUT", "CONTENT_TOO_LARGE", "WEBSITE_ACCESS_ERROR", "WEBSITE_BLOCKED", "ERROR":
				default:
					return errors.New("context: invalid Highlights")
				}
			}
			if snippet == "" {
				snippet = "[Highlights unavailable; search description only.]"
				if item.Description != nil {
					snippet += " " + *item.Description
				}
			}
			if runes := []rune(snippet); len(runes) > 1000 {
				snippet = string(runes[:1000])
			}
			results = append(results, SearchResult{Title: item.Title, URL: item.URL, Snippet: snippet, DisplayLink: extractDisplayLink(item.URL)})
		}
		return nil
	})
	if errors.Is(err, circuit.ErrCircuitOpen) {
		return nil, fmt.Errorf("context: %w", circuit.ErrCircuitOpen)
	}
	if err != nil {
		return nil, err
	}
	return results, nil
}

var _ Provider = (*ContextDevProvider)(nil)
