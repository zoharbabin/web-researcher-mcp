package search

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

const (
	parallelMCPURL    = "https://search.parallel.ai/mcp"
	parallelUserAgent = "web-researcher-mcp (+https://github.com/zoharbabin/web-researcher-mcp)"
	parallelMaxBytes  = 5 * 1024 * 1024
	parallelMaxErrLen = 1024 // runes of an upstream error message kept in ours
	// parallelMaxRetryAfter caps a Retry-After the endpoint advertises. The
	// breaker stays open for at least that long, so an absurd (or hostile)
	// header must not be able to disable the provider indefinitely.
	parallelMaxRetryAfter = 15 * time.Minute
)

// parallelMaxSnippetRunes bounds vendor-supplied excerpt text per result,
// matching the cap the Exa adapter applies to its own extracted text.
const parallelMaxSnippetRunes = 500

var _ Provider = (*ParallelProvider)(nil)

// ParallelProvider searches through the Parallel Search MCP endpoint. It is
// opt-in (PARALLEL_ENABLED) and supports web search only. Without a key it
// runs in anonymous Fast mode; PARALLEL_API_KEY raises the rate limit.
type ParallelProvider struct {
	deps   Deps
	apiKey string
	// sessionID is a random per-process identifier. Parallel keys free-tier
	// rate limiting (and log correlation) on it and documents reusing one
	// stable value rather than minting one per call, which would split the
	// accounting. It carries no user or tenant data.
	sessionID string
}

// NewParallelProvider creates the web-search provider using the existing HTTP
// client (including its SSRF policy, timeouts, and injected test transport).
// apiKey is optional; empty keeps the connection anonymous.
func NewParallelProvider(apiKey string, deps Deps) *ParallelProvider {
	return &ParallelProvider{deps: deps, apiKey: apiKey, sessionID: newParallelSessionID()}
}

// newParallelSessionID returns a 32-character random hex string, the shape
// Parallel documents for session_id. A failed read from crypto/rand is not
// fatal: the field is optional, so the provider just omits it.
func newParallelSessionID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(buf[:])
}

// cleanParallelText drops control characters and collapses whitespace, so a
// term can't carry newlines or NULs into the upstream query.
func cleanParallelText(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)), " ")
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

// SupportedOperations implements OperationSupport: web search only.
func (p *ParallelProvider) SupportedOperations() []Operation { return opsWeb }

// Images and News return the unsupported-capability sentinel without making
// requests or tripping the breaker.
func (p *ParallelProvider) Images(context.Context, ImageSearchParams) ([]ImageResult, error) {
	return nil, nil
}

func (p *ParallelProvider) News(context.Context, NewsSearchParams) ([]NewsResult, error) {
	return nil, nil
}

func (p *ParallelProvider) search(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	params.Query = cleanParallelText(params.Query)
	if params.Query == "" {
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
	client.Transport = parallelTransport{base: transport, apiKey: p.apiKey}
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
	if exact := cleanParallelText(strings.ReplaceAll(params.ExactTerms, `"`, " ")); exact != "" {
		query += ` "` + exact + `"`
	}
	for _, term := range strings.Fields(params.ExcludeTerms) {
		if term = strings.TrimLeft(cleanParallelText(term), "-"); term != "" {
			query += " -" + term
		}
	}
	objective := query
	if params.TimeRange != "" {
		objective += ". Prefer sources published within the last " + params.TimeRange
	}
	args := map[string]any{"objective": objective, "search_queries": []string{query}}
	if p.sessionID != "" {
		args["session_id"] = p.sessionID
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "web_search", Arguments: args})
	if err != nil {
		return nil, fmt.Errorf("parallel: search: %w", err)
	}
	if result.IsError {
		for _, content := range result.Content {
			if text, ok := content.(*mcp.TextContent); ok {
				return nil, fmt.Errorf("parallel: search tool error: %s", truncateText(text.Text, parallelMaxErrLen))
			}
		}
		return nil, fmt.Errorf("parallel: search tool returned an error")
	}
	return parallelResults(result, params.NumResults)
}

// parallelTransport adds project identification (and the optional API key) to
// every MCP request, including initialization and notifications, and bounds
// both JSON and SSE response bodies.
type parallelTransport struct {
	base   http.RoundTripper
	apiKey string
}

func (t parallelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", parallelUserAgent)
	// http.Client sends redirect hops through this transport too, so pin the key
	// to the Parallel endpoint itself or a redirect would hand it to another host.
	if t.apiKey != "" && isParallelEndpoint(req.URL) {
		req.Header.Set("Authorization", "Bearer "+t.apiKey)
	} else {
		req.Header.Del("Authorization")
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		resp.Body.Close()
		after := parallelRetryAfter(resp.Header.Get("Retry-After"), time.Now())
		return nil, fmt.Errorf("parallel: rate limited: %w", &circuit.RateLimitError{After: after})
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.LimitReader(resp.Body, parallelMaxBytes), resp.Body}
	return resp, nil
}

// isParallelEndpoint reports whether u is on the scheme and host of parallelMCPURL.
func isParallelEndpoint(u *url.URL) bool {
	endpoint, err := url.Parse(parallelMCPURL)
	return err == nil && u != nil && u.Scheme == endpoint.Scheme && strings.EqualFold(u.Host, endpoint.Host)
}

// parallelRetryAfter parses a Retry-After header (delta-seconds or HTTP date)
// into the cooldown the breaker should honor. A missing, unparseable, or
// non-positive value yields 0, which leaves the breaker on its configured
// default; anything longer than parallelMaxRetryAfter is clamped so a bad
// header cannot hold the breaker open.
func parallelRetryAfter(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	var after time.Duration
	if seconds, err := strconv.ParseInt(header, 10, 64); err == nil {
		if seconds > int64(parallelMaxRetryAfter/time.Second) {
			return parallelMaxRetryAfter // also avoids overflow in the multiply below
		}
		after = time.Duration(seconds) * time.Second
	} else if date, err := http.ParseTime(header); err == nil {
		after = date.Sub(now)
	}
	if after <= 0 {
		return 0
	}
	return min(after, parallelMaxRetryAfter)
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
	if len(data) > parallelMaxBytes {
		return nil, fmt.Errorf("parallel: search result exceeds %d bytes", parallelMaxBytes)
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
		if u, err := url.Parse(item.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			continue
		}
		results = append(results, SearchResult{
			Title: item.Title, URL: item.URL, Snippet: truncateText(strings.Join(item.Excerpts, "\n\n"), parallelMaxSnippetRunes),
			DisplayLink: extractDisplayLink(item.URL),
			PublishedAt: normalizePublishedAt(item.PublishDate, time.Now()),
		})
	}
	return results, nil
}
