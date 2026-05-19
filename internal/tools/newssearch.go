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
	Query      string `json:"query" jsonschema:"News search query,required"`
	NumResults int    `json:"num_results,omitempty" jsonschema:"Number of results (1-10, default: 5)"`
	Freshness  string `json:"freshness,omitempty" jsonschema:"Time range: hour, day, week, month, year (default: week)"`
	SortBy     string `json:"sort_by,omitempty" jsonschema:"Sort order: relevance, date (default: relevance)"`
	NewsSource string `json:"news_source,omitempty" jsonschema:"Filter by news source domain"`
}

func registerNewsSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "news_search",
		Description: "Search for news articles with freshness control and source filtering.",
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
			auditToolCall(deps, "news_search", time.Since(start), nil, "")
			return textResult(string(cached)), nil, nil
		}

		results, err := deps.Search.News(ctx, search.NewsSearchParams{
			Query:      input.Query,
			NumResults: numResults,
			Freshness:  freshness,
			SortBy:     sortBy,
			Source:     input.NewsSource,
		})
		if err != nil {
			deps.Metrics.RecordToolCall("news_search", time.Since(start), err, "upstream_error", false)
			auditToolCall(deps, "news_search", time.Since(start), err, "upstream_error")
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
		auditToolCall(deps, "news_search", time.Since(start), nil, "")

		return textResult(string(jsonBytes)), nil, nil
	})
}
