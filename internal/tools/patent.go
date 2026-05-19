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

type patentSearchInput struct {
	Query        string `json:"query" jsonschema:"Patent search query or patent number,required"`
	NumResults   int    `json:"num_results,omitempty" jsonschema:"Number of results (1-10, default: 5)"`
	SearchType   string `json:"search_type,omitempty" jsonschema:"Search type: prior_art, specific, landscape (default: prior_art)"`
	PatentOffice string `json:"patent_office,omitempty" jsonschema:"Patent office: all, US, EP, WO, JP, CN, KR (default: all)"`
	Assignee     string `json:"assignee,omitempty" jsonschema:"Company/assignee name"`
	Inventor     string `json:"inventor,omitempty" jsonschema:"Inventor name"`
	CPCCode      string `json:"cpc_code,omitempty" jsonschema:"CPC classification code (e.g., G06F)"`
	YearFrom     int    `json:"year_from,omitempty" jsonschema:"Filing year from"`
	YearTo       int    `json:"year_to,omitempty" jsonschema:"Filing year to"`
}

func registerPatentSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "patent_search",
		Description: "Search patent databases via Google Patents. Supports prior art search, landscape analysis, and specific patent lookup. Uses site-restricted search (unaffected by PSE sunset).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input patentSearchInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		if input.Query == "" {
			return toolError("query is required"), nil, nil
		}

		numResults := input.NumResults
		if numResults <= 0 {
			numResults = 5
		}
		searchType := input.SearchType
		if searchType == "" {
			searchType = "prior_art"
		}

		cacheKey := searchCacheKey("patent", input.Query, numResults, searchType, input.PatentOffice, input.Assignee, input.CPCCode)
		if cached, ok := deps.Cache.Get(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("patent_search", time.Since(start), nil, "", true)
			auditToolCall(deps, "patent_search", time.Since(start), nil, "")
			return textResult(string(cached)), nil, nil
		}

		searchQuery := buildPatentQuery(input.Query, input.Assignee, input.Inventor, input.CPCCode, input.PatentOffice, input.YearFrom, input.YearTo)

		results, err := deps.Search.Web(ctx, search.WebSearchParams{
			Query:      searchQuery + " site:patents.google.com",
			NumResults: numResults,
		})
		if err != nil {
			deps.Metrics.RecordToolCall("patent_search", time.Since(start), err, "upstream_error", false)
			auditToolCall(deps, "patent_search", time.Since(start), err, "upstream_error")
			return toolError(fmt.Sprintf("patent search failed: %v", err)), nil, nil
		}

		patents := make([]map[string]any, 0, len(results))
		for _, r := range results {
			number := extractPatentNumber(r.URL)
			if input.PatentOffice != "" && input.PatentOffice != "all" && !matchesPatentOffice(number, input.PatentOffice) {
				continue
			}
			patent := map[string]any{
				"title":  r.Title,
				"url":    r.URL,
				"number": number,
			}
			if r.Snippet != "" {
				patent["abstract"] = r.Snippet
			}
			patents = append(patents, patent)
		}

		output := map[string]any{
			"patents":     patents,
			"query":       input.Query,
			"searchType":  searchType,
			"resultCount": len(patents),
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Cache.Set(ctx, cacheKey, jsonBytes, 24*time.Hour)
		deps.Metrics.RecordToolCall("patent_search", time.Since(start), nil, "", false)
		auditToolCall(deps, "patent_search", time.Since(start), nil, "")

		return textResult(string(jsonBytes)), nil, nil
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

	suffixes := []string{" Inc", " Inc.", " LLC", " Ltd", " Ltd.", " Corp", " Corp.", " Co.", " GmbH", " AG"}
	baseName := name
	for _, s := range suffixes {
		baseName = strings.TrimSuffix(baseName, s)
	}
	if baseName != name {
		variations = append(variations, fmt.Sprintf("%q", baseName))
	}

	noSpaces := strings.ReplaceAll(baseName, " ", "")
	if noSpaces != baseName {
		variations = append(variations, fmt.Sprintf("%q", noSpaces))
	}

	return variations
}

func extractPatentNumber(url string) string {
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

func matchesPatentOffice(patentNumber, office string) bool {
	if patentNumber == "" {
		return false
	}
	prefix := strings.ToUpper(office)
	number := strings.ToUpper(patentNumber)

	officePrefixes := map[string][]string{
		"US": {"US"},
		"EP": {"EP"},
		"WO": {"WO"},
		"JP": {"JP"},
		"CN": {"CN"},
		"KR": {"KR"},
	}

	prefixes, ok := officePrefixes[prefix]
	if !ok {
		return true
	}

	for _, p := range prefixes {
		if strings.HasPrefix(number, p) {
			return true
		}
	}
	return false
}
