package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

func registerNewsSearch(srv *server.MCPServer, deps Dependencies) {
	tool := mcp.NewTool("news_search",
		mcp.WithDescription("Search for news articles with freshness control and source filtering."),
		mcp.WithString("query", mcp.Required(), mcp.Description("News search query")),
		mcp.WithNumber("num_results", mcp.Description("Number of results (1-10, default: 5)")),
		mcp.WithString("freshness", mcp.Description("Time range: hour, day, week, month, year (default: week)")),
		mcp.WithString("sort_by", mcp.Description("Sort order: relevance, date (default: relevance)")),
		mcp.WithString("news_source", mcp.Description("Filter by news source domain")),
	)

	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		query, _ := req.GetArguments()["query"].(string)
		if query == "" {
			return toolError("query is required"), nil
		}

		numResults := intParam(req.GetArguments(), "num_results", 5)
		freshness, _ := req.GetArguments()["freshness"].(string)
		if freshness == "" {
			freshness = "week"
		}
		sortBy, _ := req.GetArguments()["sort_by"].(string)
		if sortBy == "" {
			sortBy = "relevance"
		}
		newsSource, _ := req.GetArguments()["news_source"].(string)

		cacheKey := searchCacheKey("news", query, numResults, freshness, sortBy, newsSource)
		if cached, ok := deps.Cache.Get(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("news_search", time.Since(start), nil, "", true)
			auditToolCall(deps, "news_search", time.Since(start), nil, "")
			return mcp.NewToolResultText(string(cached)), nil
		}

		results, err := deps.Search.News(ctx, search.NewsSearchParams{
			Query:      query,
			NumResults: numResults,
			Freshness:  freshness,
			SortBy:     sortBy,
			Source:     newsSource,
		})
		if err != nil {
			deps.Metrics.RecordToolCall("news_search", time.Since(start), err, "upstream_error", false)
			auditToolCall(deps, "news_search", time.Since(start), err, "upstream_error")
			return toolError(fmt.Sprintf("news search failed: %v", err)), nil
		}

		output := map[string]any{
			"articles":    results,
			"query":       query,
			"resultCount": len(results),
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Cache.Set(ctx, cacheKey, jsonBytes, 15*time.Minute)
		deps.Metrics.RecordToolCall("news_search", time.Since(start), nil, "", false)
		auditToolCall(deps, "news_search", time.Since(start), nil, "")

		return mcp.NewToolResultText(string(jsonBytes)), nil
	})
}
