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

const (
	tavilySearchURL   = "https://api.tavily.com/search" // single endpoint for web AND news (topic switches mode)
	tavilyMaxQueryLen = 400                             // Tavily hard query cap; >400 chars => HTTP 400. Truncate before sending (#54).
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

// Images returns empty without error: Tavily has no dedicated image-search
// endpoint (#54). Matches the DuckDuckGo convention — returning an error here
// would trip the per-provider circuit breaker and break Router image fallback.
// No breaker call and no HTTP request are made.
func (t *TavilyProvider) Images(_ context.Context, _ ImageSearchParams) ([]ImageResult, error) {
	return nil, nil
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
	body := map[string]any{
		"query":        cappedWebQuery(params),          // cap user query first so an appended site: operator survives (see below)
		"max_results":  clamp(params.NumResults, 1, 20), // Tavily supports up to 20; the tool layer already caps num_results at 10
		"search_depth": "basic",                         // KISS default (1 credit); "advanced" doubles cost — not exposed
		"topic":        "general",
	}
	if tr := mapTavilyTimeRange(params.TimeRange); tr != "" {
		body["time_range"] = tr
	}

	respBody, err := t.doRequest(ctx, body)
	if err != nil {
		return nil, err
	}

	var resp tavilyResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse tavily response: %w", err)
	}

	var results []SearchResult
	for _, r := range resp.Results {
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

func (t *TavilyProvider) doNewsSearch(ctx context.Context, params NewsSearchParams) ([]NewsResult, error) {
	body := map[string]any{
		"query":        capTavilyQuery(params.Query),
		"max_results":  clamp(params.NumResults, 1, 20),
		"search_depth": "basic",
		"topic":        "news", // #54: News uses topic:"news"
	}
	if tr := mapTavilyTimeRange(params.Freshness); tr != "" {
		body["time_range"] = tr
	}

	respBody, err := t.doRequest(ctx, body)
	if err != nil {
		return nil, err
	}

	var resp tavilyResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse tavily news response: %w", err)
	}

	var results []NewsResult
	for _, r := range resp.Results {
		results = append(results, NewsResult{
			Title:       r.Title,
			URL:         r.URL,
			Source:      extractDisplayLink(r.URL),                         // Tavily has no separate source field; host is the honest source
			PublishedAt: normalizePublishedAt(r.PublishedDate, time.Now()), // ISO-normalized (Tavily news = RFC1123); empty on general => dropped by omitempty
			Snippet:     r.Content,
		})
	}
	return results, nil
}

func (t *TavilyProvider) doRequest(ctx context.Context, payload map[string]any) ([]byte, error) {
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", tavilySearchURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey) // current Tavily auth; never logged

	resp, err := t.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("tavily API rate limited: %w", circuit.ErrRateLimit)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("tavily API error %d: %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
}

// cappedWebQuery builds the web query string while keeping it under Tavily's
// 400-char limit WITHOUT corrupting an appended site:/lens operator. buildQuery
// (and the lens path upstream) append site: operators — for lenses, a whole
// "(site:a OR site:b ...)" group of ~200+ chars — to params.Query. Capping the
// combined string (the naive approach) could slice through that operator
// mid-token and send Tavily a malformed query. Instead we measure the operator
// suffix buildQuery adds and cap only the user-query portion, so the operator is
// always appended intact. If the operator suffix alone already exceeds the limit
// (pathological lens), we fall back to capping the whole thing — a truncated
// operator is no worse than the alternative of dropping the query entirely.
func cappedWebQuery(params WebSearchParams) string {
	full := buildQuery(params)
	if len([]rune(full)) <= tavilyMaxQueryLen {
		return full
	}
	suffix := full[len(params.Query):] // the operator portion buildQuery appended (byte-safe: params.Query is a prefix)
	budget := tavilyMaxQueryLen - len([]rune(suffix))
	if budget <= 0 {
		return capTavilyQuery(full) // operator suffix alone overflows; best-effort hard cap
	}
	return capTavilyQuery(params.Query, budget) + suffix
}

// capTavilyQuery truncates q to at most limit runes (default tavilyMaxQueryLen),
// enforcing Tavily's hard 400-char query limit (>400 => HTTP 400). Rune-safe
// (never splits a multi-byte UTF-8 char); no "..." suffix since this is a literal
// search query, not display text.
func capTavilyQuery(q string, limit ...int) string {
	max := tavilyMaxQueryLen
	if len(limit) > 0 {
		max = limit[0]
	}
	r := []rune(q)
	if len(r) > max {
		return string(r[:max])
	}
	return q
}

// mapTavilyTimeRange maps the project's freshness vocabulary to Tavily's
// time_range enum (day|week|month|year). "hour" has no sub-day equivalent so it
// collapses to "day"; unknown values omit the field.
func mapTavilyTimeRange(tr string) string {
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

// tavilyResponse serves both Web and News (PublishedDate is empty for general
// topic). Field names: results[]{url,title,content,score,published_date}.
type tavilyResponse struct {
	Results []struct {
		Title         string  `json:"title"`
		URL           string  `json:"url"`
		Content       string  `json:"content"`
		Score         float64 `json:"score"`
		PublishedDate string  `json:"published_date"`
	} `json:"results"`
}

var _ Provider = (*TavilyProvider)(nil)
