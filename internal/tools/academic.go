package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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

type academicSearchInput struct {
	Query      string `json:"query" jsonschema:"Research topic or paper title to search for. Use technical terms and specific concepts for best results.,required"`
	NumResults int    `json:"num_results,omitempty" jsonschema:"Number of papers to return (1-10, default: 5)."`
	YearFrom   int    `json:"year_from,omitempty" jsonschema:"Only include papers published in or after this year (e.g. 2020)."`
	YearTo     int    `json:"year_to,omitempty" jsonschema:"Only include papers published in or before this year (e.g. 2024)."`
	Source     string `json:"source,omitempty" jsonschema:"Restrict to an academic source: all (default), arxiv, pubmed, ieee, nature, springer."`
	PDFOnly    bool   `json:"pdf_only,omitempty" jsonschema:"Only return papers with direct PDF links (default: false). Useful when you plan to scrape the full paper."`
	SortBy     string `json:"sort_by,omitempty" jsonschema:"Sort order: relevance (default) or date (newest first)."`
}

func registerAcademicSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "academic_search",
		Description: "Search peer-reviewed academic papers across arXiv, PubMed, IEEE, Nature, Springer, and 12 other scholarly databases. Use this for scientific research, literature reviews, or finding citations — not for general web content. Returns paper title, abstract, authors, URL, and source. Filter by year range and source. Results cached 24 hours.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input academicSearchInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		if input.Query == "" {
			return toolError("query is required"), nil, nil
		}

		numResults := input.NumResults
		if numResults <= 0 {
			numResults = 5
		}
		source := input.Source
		if source == "" {
			source = "all"
		}

		cacheKey := searchCacheKey("academic", input.Query, numResults, input.YearFrom, input.YearTo, source)
		if cached, ok := deps.Cache.Get(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("academic_search", time.Since(start), nil, "", true)
			auditToolCall(deps, "academic_search", time.Since(start), nil, "")
			return textResult(string(cached)), nil, nil
		}

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
		siteQuery := input.Query + " (" + strings.Join(siteOps, " OR ") + ")"

		if input.YearFrom > 0 {
			siteQuery += fmt.Sprintf(" after:%d", input.YearFrom-1)
		}
		if input.YearTo > 0 {
			siteQuery += fmt.Sprintf(" before:%d", input.YearTo+1)
		}
		if input.PDFOnly {
			siteQuery += " filetype:pdf"
		}

		results, err := deps.Search.Web(ctx, search.WebSearchParams{
			Query:      siteQuery,
			NumResults: numResults,
		})
		if err != nil {
			deps.Metrics.RecordToolCall("academic_search", time.Since(start), err, "upstream_error", false)
			auditToolCall(deps, "academic_search", time.Since(start), err, "upstream_error")
			return toolError(fmt.Sprintf("academic search failed: %v", err)), nil, nil
		}

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
			"query":        input.Query,
			"totalResults": len(papers),
			"resultCount":  len(papers),
			"source":       source,
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Cache.Set(ctx, cacheKey, jsonBytes, 24*time.Hour)
		deps.Metrics.RecordToolCall("academic_search", time.Since(start), nil, "", false)
		auditToolCall(deps, "academic_search", time.Since(start), nil, "")

		return textResult(string(jsonBytes)), nil, nil
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
