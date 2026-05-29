package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

type patentSearchInput struct {
	Query        string `json:"query,omitempty" jsonschema:"Patent search terms, invention description, or patent number (e.g. 'US11234567' or 'machine learning video encoding'). Not required when assignee or inventor is provided."`
	NumResults   int    `json:"num_results,omitempty" jsonschema:"Number of patents to return (1-10, default: 5)."`
	SearchType   string `json:"search_type,omitempty" jsonschema:"Search strategy: prior_art (default, broad technical search), specific (exact patent lookup), landscape (competitive overview)."`
	PatentOffice string `json:"patent_office,omitempty" jsonschema:"Restrict to patent office: all (default), US, EP, WO, JP, CN, KR."`
	Assignee     string `json:"assignee,omitempty" jsonschema:"Company or organization that owns the patent (auto-generates name variations for matching)."`
	Inventor     string `json:"inventor,omitempty" jsonschema:"Name of the inventor to filter by."`
	CPCCode      string `json:"cpc_code,omitempty" jsonschema:"Cooperative Patent Classification code to narrow by technology area (e.g. G06F for computing, H04L for networking)."`
	YearFrom     int    `json:"year_from,omitempty" jsonschema:"Only include patents filed in or after this year."`
	YearTo       int    `json:"year_to,omitempty" jsonschema:"Only include patents filed in or before this year."`
	Provider     string `json:"provider,omitempty" jsonschema:"Force a specific patent provider: searchapi, epo, lens, uspto (patent-specific), or google, brave, serper, searxng (web search fallback). Omit for automatic selection based on configured providers and region."`
	SessionID    string `json:"sessionId,omitempty" jsonschema:"Link results to a sequential_search session. Sources are automatically recorded for recovery after context loss."`
}

func registerPatentSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "patent_search",
		Description:  "Search patents for prior art, competitive landscapes, or specific patents by number. You can search by patent number (e.g. 'US11234567'), by invention description in plain language, by company name, or by inventor. Choose a search strategy: prior_art (broad technical search), specific (exact patent lookup), or landscape (see what competitors are patenting). Handles company name variations automatically (e.g. finds 'Apple' patents whether filed as Apple Inc, Apple LLC, etc.). Use academic_search for published research papers, or web_search for general technical content. Results stay fresh for 24 hours.",
		Annotations:  readOnlyAnnotations(true, true),
		OutputSchema: patentSearchOutputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input patentSearchInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		if input.Query == "" && input.Assignee == "" && input.Inventor == "" {
			return toolError("query, assignee, or inventor is required"), nil, nil
		}

		numResults := input.NumResults
		if numResults <= 0 {
			numResults = 5
		}
		if numResults > 10 {
			numResults = 10
		}
		searchType := input.SearchType
		if searchType == "" {
			searchType = "prior_art"
		}

		cacheKey := searchCacheKey("patent", input.Query, numResults, searchType, input.PatentOffice, input.Assignee, input.CPCCode, input.Provider)
		if cached, meta, ok := deps.Cache.GetWithMeta(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("patent_search", time.Since(start), nil, "", true)
			auditToolCall(ctx, deps, "patent_search", time.Since(start), nil, "")
			return cachedResultWithMeta(cached, meta), nil, nil
		}

		searchParams := search.PatentSearchParams{
			Query:        input.Query,
			Assignee:     normalizeAssignee(input.Assignee),
			Inventor:     input.Inventor,
			PatentOffice: input.PatentOffice,
			YearFrom:     input.YearFrom,
			YearTo:       input.YearTo,
			NumResults:   numResults,
		}

		var patents []scraper.PatentResult

		var source string

		// Strategy 1: If a specific provider is requested, try it directly
		if input.Provider != "" {
			ps, errResult := resolvePatentSearcher(deps, input.Provider)
			if errResult != nil {
				return errResult, nil, nil
			}
			if ps != nil {
				apiResults, err := ps.Patents(ctx, searchParams)
				if err == nil && len(apiResults) > 0 {
					patents = convertPatentResults(apiResults)
					source = input.Provider
				} else if err != nil {
					errCode := "upstream_error"
					if isRateLimitError(err) {
						errCode = "rate_limited"
					}
					deps.Metrics.RecordToolCall("patent_search", time.Since(start), err, errCode, false)
					auditToolCall(ctx, deps, "patent_search", time.Since(start), err, errCode)
					return upstreamErrorResponse("patent search", err), nil, nil
				} else {
					// Provider returned nil results (e.g., USPTO for non-US office).
					// When a specific provider was explicitly requested, don't silently
					// fall back — return empty results from that provider.
					source = input.Provider
				}
			}
		}

		// Strategy 2: Try the main provider (Router implements PatentSearcher)
		if len(patents) == 0 && source == "" {
			if ps, ok := deps.Search.(search.PatentSearcher); ok {
				apiResults, err := ps.Patents(ctx, searchParams)
				if err == nil && len(apiResults) > 0 {
					patents = convertPatentResults(apiResults)
					source = deps.Search.Name()
				}
			}
		}

		// Strategy 3: Try patent-only providers directly (non-router mode)
		if len(patents) == 0 && source == "" {
			for name, pp := range deps.PatentProviders {
				if !pp.Metadata().MatchesRegion(input.PatentOffice) {
					continue
				}
				apiResults, err := pp.Patents(ctx, searchParams)
				if err == nil && len(apiResults) > 0 {
					patents = convertPatentResults(apiResults)
					source = name
					break
				} else if err != nil && isRateLimitError(err) {
					break
				}
			}
		}

		// Strategy 4: Fallback — discover via web search + enrich from detail pages
		if len(patents) == 0 && source == "" {
			provider, errResult := resolveProvider(deps, "")
			if errResult != nil {
				return errResult, nil, nil
			}

			searchQuery := buildPatentDiscoveryQuery(input)
			webResults, err := provider.Web(ctx, search.WebSearchParams{
				Query:      searchQuery,
				NumResults: numResults + 5,
			})
			if err != nil {
				errCode := "upstream_error"
				if isRateLimitError(err) {
					errCode = "rate_limited"
				}
				deps.Metrics.RecordToolCall("patent_search", time.Since(start), err, errCode, false)
				auditToolCall(ctx, deps, "patent_search", time.Since(start), err, errCode)
				return upstreamErrorResponse("patent search", err), nil, nil
			}

			var patentNumbers []string
			seen := make(map[string]bool)
			for _, r := range webResults {
				number := scraper.ExtractPatentNumberFromURL(r.URL)
				if number == "" {
					continue
				}
				if input.PatentOffice != "" && input.PatentOffice != "all" && !matchesPatentOffice(number, input.PatentOffice) {
					continue
				}
				if !seen[number] {
					seen[number] = true
					patentNumbers = append(patentNumbers, number)
				}
				if len(patentNumbers) >= numResults {
					break
				}
			}

			patents = enrichPatents(ctx, deps.Scraper, patentNumbers)
			if len(patents) > 0 {
				source = "web_discovery"
			}
		}

		// Build the Google Patents search URL for reference
		params := scraper.PatentSearchParams{
			Query:        input.Query,
			Assignee:     normalizeAssignee(input.Assignee),
			Inventor:     input.Inventor,
			CPCCode:      input.CPCCode,
			PatentOffice: input.PatentOffice,
			YearFrom:     input.YearFrom,
			YearTo:       input.YearTo,
			NumResults:   numResults,
		}

		output := map[string]any{
			"patents":     patents,
			"query":       input.Query,
			"searchType":  searchType,
			"resultCount": len(patents),
			"source":      source,
			"searchUrl":   scraper.BuildGooglePatentsURL(params),
		}

		jsonBytes, _ := json.Marshal(output)
		if len(patents) > 0 {
			deps.Cache.Set(ctx, cacheKey, jsonBytes, 24*time.Hour)
		}
		deps.Metrics.RecordToolCall("patent_search", time.Since(start), nil, "", false)
		auditToolCall(ctx, deps, "patent_search", time.Since(start), nil, "")

		if input.SessionID != "" {
			trackSources(ctx, deps, input.SessionID, patentResultsToSources(patents))
		}

		return structuredResult(jsonBytes), nil, nil
	})
}

func buildPatentDiscoveryQuery(input patentSearchInput) string {
	var parts []string
	if input.Query != "" {
		parts = append(parts, input.Query)
	}
	if input.Assignee != "" {
		parts = append(parts, fmt.Sprintf("%q", input.Assignee))
	}
	if input.Inventor != "" {
		parts = append(parts, fmt.Sprintf("inventor:%q", input.Inventor))
	}
	if input.PatentOffice != "" && input.PatentOffice != "all" {
		parts = append(parts, input.PatentOffice)
	}
	if input.YearFrom > 0 {
		parts = append(parts, fmt.Sprintf("%d", input.YearFrom))
	}
	if input.YearTo > 0 && input.YearTo != input.YearFrom {
		parts = append(parts, fmt.Sprintf("%d", input.YearTo))
	}

	if len(parts) == 0 {
		parts = append(parts, "patent")
	}

	return strings.Join(parts, " ") + " site:patents.google.com"
}

func enrichPatents(ctx context.Context, pipeline *scraper.Pipeline, numbers []string) []scraper.PatentResult {
	if len(numbers) == 0 {
		return nil
	}

	results := make([]scraper.PatentResult, len(numbers))
	var wg sync.WaitGroup

	// Limit concurrency to 3 parallel detail fetches
	sem := make(chan struct{}, 3)

	for i, number := range numbers {
		wg.Add(1)
		go func(idx int, num string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			detail, err := pipeline.ScrapePatentDetail(ctx, num)
			if err != nil || detail == nil {
				// Fallback: return minimal info
				results[idx] = scraper.PatentResult{
					Number: num,
					URL:    "https://patents.google.com/patent/" + num,
				}
				return
			}
			results[idx] = *detail
		}(i, number)
	}

	wg.Wait()

	// Filter out empty results
	var filtered []scraper.PatentResult
	for _, r := range results {
		if r.Number != "" {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func normalizeAssignee(assignee string) string {
	if assignee == "" {
		return ""
	}
	assignee = strings.TrimSpace(assignee)
	suffixes := []string{" Inc", " Inc.", " LLC", " Ltd", " Ltd.", " Corp", " Corp.", " Co.", " GmbH", " AG"}
	for _, s := range suffixes {
		assignee = strings.TrimSuffix(assignee, s)
	}
	return assignee
}

func convertPatentResults(apiResults []search.PatentResult) []scraper.PatentResult {
	results := make([]scraper.PatentResult, 0, len(apiResults))
	for _, r := range apiResults {
		results = append(results, scraper.PatentResult{
			Title:    r.Title,
			Number:   r.Number,
			URL:      r.URL,
			Abstract: r.Abstract,
			Assignee: r.Assignee,
			Inventor: r.Inventor,
			Filed:    r.Filed,
			Granted:  r.Granted,
			PDF:      r.PDF,
			Status:   r.Status,
		})
	}
	return results
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
