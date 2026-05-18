package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

func registerPatentSearch(srv *server.MCPServer, deps Dependencies) {
	tool := mcp.NewTool("patent_search",
		mcp.WithDescription("Search patent databases via Google Patents. Supports prior art search, landscape analysis, and specific patent lookup. Uses site-restricted search (unaffected by PSE sunset)."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Patent search query or patent number")),
		mcp.WithNumber("num_results", mcp.Description("Number of results (1-10, default: 5)")),
		mcp.WithString("search_type", mcp.Description("Search type: prior_art, specific, landscape (default: prior_art)")),
		mcp.WithString("patent_office", mcp.Description("Patent office: all, US, EP, WO, JP, CN, KR (default: all)")),
		mcp.WithString("assignee", mcp.Description("Company/assignee name")),
		mcp.WithString("inventor", mcp.Description("Inventor name")),
		mcp.WithString("cpc_code", mcp.Description("CPC classification code (e.g., G06F)")),
		mcp.WithNumber("year_from", mcp.Description("Filing year from")),
		mcp.WithNumber("year_to", mcp.Description("Filing year to")),
	)

	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		query, _ := req.GetArguments()["query"].(string)
		if query == "" {
			return toolError("query is required"), nil
		}

		numResults := intParam(req.GetArguments(), "num_results", 5)
		searchType, _ := req.GetArguments()["search_type"].(string)
		if searchType == "" {
			searchType = "prior_art"
		}
		patentOffice, _ := req.GetArguments()["patent_office"].(string)
		assignee, _ := req.GetArguments()["assignee"].(string)
		inventor, _ := req.GetArguments()["inventor"].(string)
		cpcCode, _ := req.GetArguments()["cpc_code"].(string)
		yearFrom := intParam(req.GetArguments(), "year_from", 0)
		yearTo := intParam(req.GetArguments(), "year_to", 0)

		cacheKey := searchCacheKey("patent", query, numResults, searchType, patentOffice, assignee, cpcCode)
		if cached, ok := deps.Cache.Get(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("patent_search", time.Since(start), nil, "", true)
			auditToolCall(deps, "patent_search", time.Since(start), nil, "")
			return mcp.NewToolResultText(string(cached)), nil
		}

		// Build patent search query
		searchQuery := buildPatentQuery(query, assignee, inventor, cpcCode, patentOffice, yearFrom, yearTo)

		results, err := deps.Search.Web(ctx, search.WebSearchParams{
			Query:      searchQuery + " site:patents.google.com",
			NumResults: numResults,
		})
		if err != nil {
			deps.Metrics.RecordToolCall("patent_search", time.Since(start), err, "upstream_error", false)
			auditToolCall(deps, "patent_search", time.Since(start), err, "upstream_error")
			return toolError(fmt.Sprintf("patent search failed: %v", err)), nil
		}

		patents := make([]map[string]any, 0, len(results))
		for _, r := range results {
			patent := map[string]any{
				"title":  r.Title,
				"url":    r.URL,
				"number": extractPatentNumber(r.URL),
			}
			if r.Snippet != "" {
				patent["abstract"] = r.Snippet
			}
			patents = append(patents, patent)
		}

		output := map[string]any{
			"patents":     patents,
			"query":       query,
			"searchType":  searchType,
			"resultCount": len(patents),
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Cache.Set(ctx, cacheKey, jsonBytes, 24*time.Hour)
		deps.Metrics.RecordToolCall("patent_search", time.Since(start), nil, "", false)
			auditToolCall(deps, "patent_search", time.Since(start), nil, "")

		return mcp.NewToolResultText(string(jsonBytes)), nil
	})
}

func buildPatentQuery(query, assignee, inventor, cpcCode, office string, yearFrom, yearTo int) string {
	parts := []string{query}

	if assignee != "" {
		variations := companyVariations(assignee)
		parts = append(parts, "("+strings.Join(variations, " OR ")+")")
	}
	if inventor != "" {
		parts = append(parts, fmt.Sprintf("inventor:%q", inventor))
	}
	if cpcCode != "" {
		parts = append(parts, fmt.Sprintf("cpc:%s", cpcCode))
	}
	if office != "" && office != "all" {
		parts = append(parts, fmt.Sprintf("country:%s", office))
	}
	if yearFrom > 0 {
		parts = append(parts, fmt.Sprintf("after:%d", yearFrom-1))
	}
	if yearTo > 0 {
		parts = append(parts, fmt.Sprintf("before:%d", yearTo+1))
	}

	return strings.Join(parts, " ")
}

func companyVariations(name string) []string {
	name = strings.TrimSpace(name)
	variations := []string{
		fmt.Sprintf("%q", name),
	}

	// Without common suffixes
	suffixes := []string{" Inc", " Inc.", " LLC", " Ltd", " Ltd.", " Corp", " Corp.", " Co.", " GmbH", " AG"}
	baseName := name
	for _, s := range suffixes {
		baseName = strings.TrimSuffix(baseName, s)
	}
	if baseName != name {
		variations = append(variations, fmt.Sprintf("%q", baseName))
	}

	// No spaces variant
	noSpaces := strings.ReplaceAll(baseName, " ", "")
	if noSpaces != baseName {
		variations = append(variations, fmt.Sprintf("%q", noSpaces))
	}

	return variations
}

func extractPatentNumber(url string) string {
	// patents.google.com/patent/US1234567A/en
	parts := strings.Split(url, "/patent/")
	if len(parts) >= 2 {
		number := parts[1]
		if idx := strings.Index(number, "/"); idx > 0 {
			number = number[:idx]
		}
		return number
	}
	return ""
}
