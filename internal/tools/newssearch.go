package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

type newsSearchInput struct {
	Query      string `json:"query" jsonschema:"Topic or event to find news about. Use specific terms for precision (e.g. 'OpenAI GPT-5 release' not 'AI news').,required"`
	NumResults int    `json:"num_results,omitempty" jsonschema:"Number of articles to return (1-10, default: 5)."`
	Freshness  string `json:"freshness,omitempty" jsonschema:"How recent articles must be: hour, day, week (default), month, or year."`
	SortBy     string `json:"sort_by,omitempty" jsonschema:"Sort order: relevance (default) or date (newest first)."`
	NewsSource string `json:"news_source,omitempty" jsonschema:"Restrict to a specific news outlet domain (e.g. reuters.com, bbc.co.uk)."`
	Provider   string `json:"provider,omitempty" jsonschema:"Force a specific search provider: google, brave, serper, searxng, searchapi. Omit to use configured default."`
}

func registerNewsSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "news_search",
		Description:  "Search recent news articles by topic with time-based freshness filtering. Returns JSON with fields: articles (array of {title, url, source, publishedAt, snippet}), query, resultCount. Default freshness is 'week'; use 'hour' or 'day' for breaking news. On no matches returns resultCount: 0 with empty array; on failure returns isError with message. Subject to upstream API quotas with automatic provider fallback. Coverage depends on configured search provider. Use web_search instead for evergreen/non-news content; use academic_search for peer-reviewed findings; use search_and_scrape if you need full article text beyond snippets. Results cached 15 min due to news volatility.",
		Annotations:  readOnlyAnnotations(true, true),
		OutputSchema: newsSearchOutputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input newsSearchInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		if input.Query == "" {
			return toolError("query is required"), nil, nil
		}

		numResults := input.NumResults
		if numResults <= 0 {
			numResults = 5
		}
		freshness := input.Freshness
		if freshness == "" {
			freshness = "week"
		}
		sortBy := input.SortBy
		if sortBy == "" {
			sortBy = "relevance"
		}

		cacheKey := searchCacheKey("news", input.Query, numResults, freshness, sortBy, input.NewsSource)
		if cached, ok := deps.Cache.Get(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("news_search", time.Since(start), nil, "", true)
			auditToolCall(ctx, deps, "news_search", time.Since(start), nil, "")
			return structuredResult(cached), nil, nil
		}

		provider, errResult := resolveProvider(deps, input.Provider)
		if errResult != nil {
			return errResult, nil, nil
		}

		results, err := provider.News(ctx, search.NewsSearchParams{
			Query:      input.Query,
			NumResults: numResults,
			Freshness:  freshness,
			SortBy:     sortBy,
			Source:     input.NewsSource,
		})
		if err != nil {
			errCode := "upstream_error"
			if isRateLimitError(err) {
				errCode = "rate_limited"
			}
			deps.Metrics.RecordToolCall("news_search", time.Since(start), err, errCode, false)
			auditToolCall(ctx, deps, "news_search", time.Since(start), err, errCode)
			if isRateLimitError(err) {
				return rateLimitError(err), nil, nil
			}
			return toolError(fmt.Sprintf("news search failed: %v", err)), nil, nil
		}

		output := map[string]any{
			"articles":    results,
			"query":       input.Query,
			"resultCount": len(results),
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Cache.Set(ctx, cacheKey, jsonBytes, 15*time.Minute)
		deps.Metrics.RecordToolCall("news_search", time.Since(start), nil, "", false)
		auditToolCall(ctx, deps, "news_search", time.Since(start), nil, "")

		return structuredResult(jsonBytes), nil, nil
	})
}
