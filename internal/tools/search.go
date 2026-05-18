package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

func registerWebSearch(srv *server.MCPServer, deps Dependencies) {
	tool := mcp.NewTool("web_search",
		mcp.WithDescription("Search the web and return structured result URLs with metadata. Supports search lenses for focused domain-specific research."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query (1-500 characters)")),
		mcp.WithNumber("num_results", mcp.Description("Number of results to return (1-10, default: 5)")),
		mcp.WithString("time_range", mcp.Description("Time restriction: day, week, month, year")),
		mcp.WithString("safe", mcp.Description("Safe search level: off, medium, high")),
		mcp.WithString("language", mcp.Description("ISO 639-1 language code")),
		mcp.WithString("site", mcp.Description("Restrict to domain")),
		mcp.WithString("exact_terms", mcp.Description("Exact phrase to match")),
		mcp.WithString("exclude_terms", mcp.Description("Terms to exclude")),
		mcp.WithString("country", mcp.Description("ISO 3166-1 alpha-2 country code")),
		mcp.WithString("lens", mcp.Description("Search lens: programming, news, tech, legal, medical, finance, science, government")),
	)

	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		query, _ := req.GetArguments()["query"].(string)
		if query == "" {
			return toolError("query is required"), nil
		}
		if len(query) > 500 {
			return toolError("query must be 500 characters or less"), nil
		}

		numResults := intParam(req.GetArguments(), "num_results", 5)
		timeRange, _ := req.GetArguments()["time_range"].(string)
		safe, _ := req.GetArguments()["safe"].(string)
		language, _ := req.GetArguments()["language"].(string)
		site, _ := req.GetArguments()["site"].(string)
		exactTerms, _ := req.GetArguments()["exact_terms"].(string)
		excludeTerms, _ := req.GetArguments()["exclude_terms"].(string)
		country, _ := req.GetArguments()["country"].(string)
		lens, _ := req.GetArguments()["lens"].(string)

		// Check cache
		cacheKey := searchCacheKey("web", query, numResults, timeRange, safe, language, site, lens)
		if cached, ok := deps.Cache.Get(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("web_search", time.Since(start), nil, "", true)
			ev := audit.NewEvent("tool_call", "", "")
			ev.ToolName = "web_search"
			ev.Duration = time.Since(start).Milliseconds()
			ev.Success = true
			ev.Metadata = map[string]any{"cache_hit": true, "query": query}
			deps.Auditor.Log(ev)
			return mcp.NewToolResultText(string(cached)), nil
		}

		params := search.WebSearchParams{
			Query:        query,
			NumResults:   numResults,
			TimeRange:    timeRange,
			Safe:         safe,
			Language:     language,
			Country:      country,
			Site:         site,
			ExactTerms:   exactTerms,
			ExcludeTerms: excludeTerms,
		}

		// Handle lens routing
		if lens != "" {
			registry := search.GetLensRegistry()
			lensData, ok := registry.Get(lens)
			if !ok {
				return toolError(fmt.Sprintf("unknown lens: %s. Available: %v", lens, registry.List())), nil
			}

			if lensData.CX != "" {
				params.Query = query
				params.Site = ""
			} else {
				params.Query = registry.BuildSiteQuery(query, lensData)
			}
		}

		results, err := deps.Search.Web(ctx, params)
		if err != nil {
			deps.Metrics.RecordToolCall("web_search", time.Since(start), err, "upstream_error", false)
			ev := audit.NewEvent("tool_call", "", "")
			ev.ToolName = "web_search"
			ev.Duration = time.Since(start).Milliseconds()
			ev.Success = false
			ev.ErrorCode = "upstream_error"
			ev.Metadata = map[string]any{"query": query, "error": err.Error()}
			deps.Auditor.Log(ev)
			return toolError(fmt.Sprintf("search failed: %v", err)), nil
		}

		urls := make([]string, len(results))
		for i, r := range results {
			urls[i] = r.URL
		}

		output := map[string]any{
			"urls":        urls,
			"query":       query,
			"resultCount": len(results),
			"results":     results,
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Cache.Set(ctx, cacheKey, jsonBytes, 30*time.Minute)
		deps.Metrics.RecordToolCall("web_search", time.Since(start), nil, "", false)
		ev := audit.NewEvent("tool_call", "", "")
		ev.ToolName = "web_search"
		ev.Duration = time.Since(start).Milliseconds()
		ev.Success = true
		ev.Metadata = map[string]any{"query": query, "result_count": len(results)}
		deps.Auditor.Log(ev)

		return mcp.NewToolResultText(string(jsonBytes)), nil
	})
}

func searchCacheKey(toolName string, parts ...any) string {
	h := sha256.New()
	h.Write([]byte(toolName))
	for _, p := range parts {
		fmt.Fprintf(h, "|%v", p)
	}
	return "search:" + hex.EncodeToString(h.Sum(nil))[:32]
}

func auditToolCall(deps Dependencies, toolName string, duration time.Duration, err error, errCode string) {
	if deps.Auditor == nil {
		return
	}
	event := audit.NewEvent("tool_call", "default", "anonymous")
	event.ToolName = toolName
	event.Duration = duration.Milliseconds()
	event.Success = err == nil
	if errCode != "" {
		event.ErrorCode = errCode
	}
	deps.Auditor.Log(event)
}

func intParam(args map[string]any, key string, fallback int) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return fallback
}

func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: msg,
			},
		},
		IsError: true,
	}
}
