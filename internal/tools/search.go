package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

type webSearchInput struct {
	Query        string `json:"query" jsonschema:"Search query (1-500 characters),required"`
	NumResults   int    `json:"num_results,omitempty" jsonschema:"Number of results to return (1-10, default: 5)"`
	TimeRange    string `json:"time_range,omitempty" jsonschema:"Time restriction: day, week, month, year"`
	Safe         string `json:"safe,omitempty" jsonschema:"Safe search level: off, medium, high"`
	Language     string `json:"language,omitempty" jsonschema:"ISO 639-1 language code"`
	Site         string `json:"site,omitempty" jsonschema:"Restrict to domain"`
	ExactTerms   string `json:"exact_terms,omitempty" jsonschema:"Exact phrase to match"`
	ExcludeTerms string `json:"exclude_terms,omitempty" jsonschema:"Terms to exclude"`
	Country      string `json:"country,omitempty" jsonschema:"ISO 3166-1 alpha-2 country code"`
	Lens         string `json:"lens,omitempty" jsonschema:"Search lens: programming, news, tech, legal, medical, finance, science, government"`
}

func registerWebSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "web_search",
		Description: "Search the web and return structured result URLs with metadata. Supports search lenses for focused domain-specific research.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input webSearchInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		if input.Query == "" {
			return toolError("query is required"), nil, nil
		}
		if len(input.Query) > 500 {
			return toolError("query must be 500 characters or less"), nil, nil
		}

		numResults := input.NumResults
		if numResults <= 0 {
			numResults = 5
		}

		cacheKey := searchCacheKey("web", input.Query, numResults, input.TimeRange, input.Safe, input.Language, input.Site, input.Lens)
		if cached, ok := deps.Cache.Get(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("web_search", time.Since(start), nil, "", true)
			ev := audit.NewEvent("tool_call", "", "")
			ev.ToolName = "web_search"
			ev.Duration = time.Since(start).Milliseconds()
			ev.Success = true
			ev.Metadata = map[string]any{"cache_hit": true, "query": input.Query}
			deps.Auditor.Log(ev)
			return textResult(string(cached)), nil, nil
		}

		params := search.WebSearchParams{
			Query:        input.Query,
			NumResults:   numResults,
			TimeRange:    input.TimeRange,
			Safe:         input.Safe,
			Language:     input.Language,
			Country:      input.Country,
			Site:         input.Site,
			ExactTerms:   input.ExactTerms,
			ExcludeTerms: input.ExcludeTerms,
		}

		if input.Lens != "" {
			registry := search.GetLensRegistry()
			lensData, ok := registry.Get(input.Lens)
			if !ok {
				return toolError(fmt.Sprintf("unknown lens: %s. Available: %v", input.Lens, registry.List())), nil, nil
			}

			if lensData.CX != "" {
				params.Query = input.Query
				params.Site = ""
			} else {
				params.Query = registry.BuildSiteQuery(input.Query, lensData)
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
			ev.Metadata = map[string]any{"query": input.Query, "error": err.Error()}
			deps.Auditor.Log(ev)
			return toolError(fmt.Sprintf("search failed: %v", err)), nil, nil
		}

		urls := make([]string, len(results))
		for i, r := range results {
			urls[i] = r.URL
		}

		output := map[string]any{
			"urls":        urls,
			"query":       input.Query,
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
		ev.Metadata = map[string]any{"query": input.Query, "result_count": len(results)}
		deps.Auditor.Log(ev)

		return textResult(string(jsonBytes)), nil, nil
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

func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
		IsError: true,
	}
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}
}
