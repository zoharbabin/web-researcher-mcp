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

var academicSites = []string{
	"arxiv.org",
	"pubmed.ncbi.nlm.nih.gov",
	"scholar.google.com",
	"ieeexplore.ieee.org",
	"dl.acm.org",
	"nature.com",
	"sciencedirect.com",
	"link.springer.com",
	"researchgate.net",
	"plos.org",
	"frontiersin.org",
	"mdpi.com",
	"wiley.com",
	"jstor.org",
	"semanticscholar.org",
	"biorxiv.org",
	"medrxiv.org",
}

var sourceToSites = map[string][]string{
	"arxiv":    {"arxiv.org"},
	"pubmed":   {"pubmed.ncbi.nlm.nih.gov"},
	"ieee":     {"ieeexplore.ieee.org"},
	"nature":   {"nature.com"},
	"springer": {"link.springer.com"},
}

func registerAcademicSearch(srv *server.MCPServer, deps Dependencies) {
	tool := mcp.NewTool("academic_search",
		mcp.WithDescription("Search academic literature across arXiv, PubMed, IEEE, Nature, Springer, and other scholarly sources. Uses site-restricted Google search (unaffected by PSE sunset)."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Academic search query")),
		mcp.WithNumber("num_results", mcp.Description("Number of results (1-10, default: 5)")),
		mcp.WithNumber("year_from", mcp.Description("Filter papers from this year (e.g., 2020)")),
		mcp.WithNumber("year_to", mcp.Description("Filter papers to this year (e.g., 2024)")),
		mcp.WithString("source", mcp.Description("Source filter: all, arxiv, pubmed, ieee, nature, springer (default: all)")),
		mcp.WithBoolean("pdf_only", mcp.Description("Only return results with PDF links (default: false)")),
		mcp.WithString("sort_by", mcp.Description("Sort by: relevance, date (default: relevance)")),
	)

	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		query, _ := req.GetArguments()["query"].(string)
		if query == "" {
			return toolError("query is required"), nil
		}

		numResults := intParam(req.GetArguments(), "num_results", 5)
		yearFrom := intParam(req.GetArguments(), "year_from", 0)
		yearTo := intParam(req.GetArguments(), "year_to", 0)
		source, _ := req.GetArguments()["source"].(string)
		if source == "" {
			source = "all"
		}
		pdfOnly := boolParam(req.GetArguments(), "pdf_only", false)

		cacheKey := searchCacheKey("academic", query, numResults, yearFrom, yearTo, source)
		if cached, ok := deps.Cache.Get(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("academic_search", time.Since(start), nil, "", true)
			auditToolCall(deps, "academic_search", time.Since(start), nil, "")
			return mcp.NewToolResultText(string(cached)), nil
		}

		// Build site-restricted query
		sites := academicSites
		if source != "all" {
			if s, ok := sourceToSites[source]; ok {
				sites = s
			}
		}

		siteOps := make([]string, len(sites))
		for i, s := range sites {
			siteOps[i] = "site:" + s
		}
		siteQuery := query + " (" + strings.Join(siteOps, " OR ") + ")"

		if yearFrom > 0 {
			siteQuery += fmt.Sprintf(" after:%d", yearFrom-1)
		}
		if yearTo > 0 {
			siteQuery += fmt.Sprintf(" before:%d", yearTo+1)
		}
		if pdfOnly {
			siteQuery += " filetype:pdf"
		}

		results, err := deps.Search.Web(ctx, search.WebSearchParams{
			Query:      siteQuery,
			NumResults: numResults,
		})
		if err != nil {
			deps.Metrics.RecordToolCall("academic_search", time.Since(start), err, "upstream_error", false)
			auditToolCall(deps, "academic_search", time.Since(start), err, "upstream_error")
			return toolError(fmt.Sprintf("academic search failed: %v", err)), nil
		}

		// Transform to academic paper format
		papers := make([]map[string]any, 0, len(results))
		for _, r := range results {
			paper := map[string]any{
				"title":  r.Title,
				"url":    r.URL,
				"source": detectAcademicSource(r.URL),
			}
			if r.Snippet != "" {
				paper["abstract"] = r.Snippet
			}
			papers = append(papers, paper)
		}

		output := map[string]any{
			"papers":       papers,
			"query":        query,
			"totalResults": len(papers),
			"resultCount":  len(papers),
			"source":       source,
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Cache.Set(ctx, cacheKey, jsonBytes, 24*time.Hour)
		deps.Metrics.RecordToolCall("academic_search", time.Since(start), nil, "", false)
			auditToolCall(deps, "academic_search", time.Since(start), nil, "")

		return mcp.NewToolResultText(string(jsonBytes)), nil
	})
}

func detectAcademicSource(url string) string {
	urlLower := strings.ToLower(url)
	for source, sites := range sourceToSites {
		for _, site := range sites {
			if strings.Contains(urlLower, site) {
				return source
			}
		}
	}
	if strings.Contains(urlLower, "scholar.google") {
		return "google_scholar"
	}
	return "other"
}
