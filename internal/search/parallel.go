package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

const (
	parallelMCPURL    = "https://search.parallel.ai/mcp"
	parallelUserAgent = "web-researcher-mcp (+https://github.com/zoharbabin/web-researcher-mcp)"
	parallelMaxBytes  = 5 * 1024 * 1024
)

var _ Provider = (*ParallelProvider)(nil)

// ParallelProvider searches through the anonymous Parallel Search MCP endpoint.
// It uses Fast mode without credentials and supports web search only.
type ParallelProvider struct {
	deps Deps
}

// NewParallelProvider creates a keyless web-search provider using the existing
// HTTP client (including its SSRF policy, timeouts, and injected test transport).
func NewParallelProvider(deps Deps) *ParallelProvider {
	return &ParallelProvider{deps: deps}
}

func (p *ParallelProvider) Name() string { return "parallel" }

func (p *ParallelProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	var results []SearchResult
	err := p.deps.Breaker.Execute(func() error {
		var err error
		results, err = p.search(ctx, params)
		return err
	})
	return results, err
}

// Images and News return the unsupported-capability sentinel without making
// requests or tripping the breaker.
func (p *ParallelProvider) Images(context.Context, ImageSearchParams) ([]ImageResult, error) {
	return nil, nil
}

func (p *ParallelProvider) News(context.Context, NewsSearchParams) ([]NewsResult, error) {
	return nil, nil
}

func (p *ParallelProvider) search(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	if strings.TrimSpace(params.Query) == "" {
		return nil, fmt.Errorf("parallel: query is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Copy rather than mutate the injected client shared with other providers.
	client := *p.deps.HTTPClient
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = parallelTransport{base: transport}
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "web-researcher-mcp", Version: "dev"}, nil)
	session, err := mcpClient.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: parallelMCPURL, HTTPClient: &client,
		DisableStandaloneSSE: true, MaxRetries: -1, MaxEventSize: parallelMaxBytes,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("parallel: connect: %w", err)
	}
	defer session.Close()

	query := buildQuery(params)
	if params.ExactTerms != "" {
		query += " " + strconv.Quote(params.ExactTerms)
	}
	for _, term := range strings.Fields(params.ExcludeTerms) {
		query += " -" + term
	}
	objective := query
	if params.TimeRange != "" {
		objective += ". Prefer sources published within the last " + params.TimeRange
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"objective": objective, "search_queries": []string{query}},
	})
	if err != nil {
		return nil, fmt.Errorf("parallel: search: %w", err)
	}
	if result.IsError {
		for _, content := range result.Content {
			if text, ok := content.(*mcp.TextContent); ok {
				message := text.Text
				if len(message) > 1024 {
					message = message[:1024]
				}
				return nil, fmt.Errorf("parallel: search tool error: %s", message)
			}
		}
		return nil, fmt.Errorf("parallel: search tool returned an error")
	}
	return parallelResults(result, params.NumResults)
}

// parallelTransport adds project identification to every MCP request, including
// initialization and notifications, and bounds both JSON and SSE response bodies.
type parallelTransport struct{ base http.RoundTripper }

func (t parallelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", parallelUserAgent)
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		resp.Body.Close()
		var after time.Duration
		if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
			after = time.Duration(seconds) * time.Second
		} else if date, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
			after = time.Until(date)
		}
		return nil, fmt.Errorf("parallel: rate limited: %w", &circuit.RateLimitError{After: after})
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.LimitReader(resp.Body, parallelMaxBytes), resp.Body}
	return resp, nil
}

func parallelResults(result *mcp.CallToolResult, count int) ([]SearchResult, error) {
	var data []byte
	if result.StructuredContent != nil {
		var err error
		data, err = json.Marshal(result.StructuredContent)
		if err != nil {
			return nil, fmt.Errorf("parallel: encode structured result: %w", err)
		}
	} else {
		for _, content := range result.Content {
			if text, ok := content.(*mcp.TextContent); ok {
				data = []byte(text.Text)
				break
			}
		}
	}
	var response struct {
		Results *[]struct {
			URL         string   `json:"url"`
			Title       string   `json:"title"`
			PublishDate string   `json:"publish_date"`
			Excerpts    []string `json:"excerpts"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("parallel: parse search result: %w", err)
	}
	if response.Results == nil {
		return nil, fmt.Errorf("parallel: search response is missing results")
	}
	if count <= 0 {
		count = 5
	}
	count = clamp(count, 1, 10)
	results := make([]SearchResult, 0, count)
	for _, item := range *response.Results {
		if len(results) >= count {
			break
		}
		results = append(results, SearchResult{
			Title: item.Title, URL: item.URL, Snippet: strings.Join(item.Excerpts, "\n\n"),
			DisplayLink: extractDisplayLink(item.URL),
			PublishedAt: normalizePublishedAt(item.PublishDate, time.Now()),
		})
	}
	return results, nil
}
