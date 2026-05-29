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
	Provider   string `json:"provider,omitempty" jsonschema:"Force a specific provider. Academic: openalex, crossref. Web fallback: google, brave, serper, searxng, searchapi. Omit to use automatic selection (recommended)."`
	OpenAccess bool   `json:"open_access,omitempty" jsonschema:"Only return open-access papers with free full-text (default: false)."`
	SessionID  string `json:"sessionId,omitempty" jsonschema:"Link results to a sequential_search session. Sources are automatically recorded for recovery after context loss."`
}

func registerAcademicSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "academic_search",
		Description:  "Search peer-reviewed academic papers and scholarly literature. Returns paper details including title, authors, journal, year, abstract, citation count, and PDF links where available. Just use natural language for your query — no special syntax needed. Narrow results by year range, academic source (arxiv, pubmed, ieee, nature, springer), or filter to only open-access papers. Set pdf_only=true to only get papers you can read in full via scrape_page. Great for literature reviews, prior art research, and finding citations. Use web_search for non-academic content, or news_search for current events. Results stay fresh for 1 hour.",
		Annotations:  readOnlyAnnotations(true, true),
		OutputSchema: academicSearchOutputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input academicSearchInput) (*mcp.CallToolResult, any, error) {
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
		if numResults > 10 {
			numResults = 10
		}
		source := input.Source
		if source == "" {
			source = "all"
		}

		cacheKey := searchCacheKey("academic", input.Query, numResults, input.YearFrom, input.YearTo, source, input.Provider, input.OpenAccess, input.PDFOnly)
		if cached, meta, ok := deps.Cache.GetWithMeta(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("academic_search", time.Since(start), nil, "", true)
			auditToolCall(ctx, deps, "academic_search", time.Since(start), nil, "")
			return cachedResultWithMeta(cached, meta), nil, nil
		}

		searchParams := search.AcademicSearchParams{
			Query:      input.Query,
			YearFrom:   input.YearFrom,
			YearTo:     input.YearTo,
			Source:     source,
			NumResults: numResults,
			OpenAccess: input.OpenAccess,
			SortBy:     input.SortBy,
		}

		var results []search.AcademicResult
		var providerSource string

		// Strategy 1: If a specific provider is requested, try it directly
		if input.Provider != "" {
			as, errResult := resolveAcademicSearcher(deps, input.Provider)
			if errResult != nil {
				return errResult, nil, nil
			}
			if as != nil {
				// It's a recognized academic provider
				apiResults, err := as.Scholarly(ctx, searchParams)
				if err == nil {
					results = apiResults
					providerSource = input.Provider
				} else {
					errCode := "upstream_error"
					if isRateLimitError(err) {
						errCode = "rate_limited"
					}
					deps.Metrics.RecordToolCall("academic_search", time.Since(start), err, errCode, false)
					auditToolCall(ctx, deps, "academic_search", time.Since(start), err, errCode)
					return upstreamErrorResponse("academic search", err), nil, nil
				}
			} else {
				// Not an academic provider — treat as web search provider for fallback
				webResults, webSource, errResult := academicWebFallback(ctx, deps, input)
				if errResult != nil {
					deps.Metrics.RecordToolCall("academic_search", time.Since(start), fmt.Errorf("fallback failed"), "upstream_error", false)
					auditToolCall(ctx, deps, "academic_search", time.Since(start), fmt.Errorf("fallback failed"), "upstream_error")
					return errResult, nil, nil
				}
				results = webResults
				providerSource = webSource
			}
		}

		// Strategy 2: Try the Router's Scholarly() method (uses routing config)
		if len(results) == 0 && input.Provider == "" {
			if as, ok := deps.Search.(search.AcademicSearcher); ok {
				apiResults, err := as.Scholarly(ctx, searchParams)
				if err == nil && len(apiResults) > 0 {
					results = apiResults
					providerSource = "router"
				}
			}
		}

		// Strategy 3: Try academic providers directly (non-router mode, deterministic order)
		if len(results) == 0 && input.Provider == "" {
			for _, name := range search.SupportedAcademicProviders {
				ap, ok := deps.AcademicProviders[name]
				if !ok {
					continue
				}
				apiResults, err := ap.Scholarly(ctx, searchParams)
				if err == nil && len(apiResults) > 0 {
					results = apiResults
					providerSource = name
					break
				} else if err != nil && isRateLimitError(err) {
					break
				}
			}
		}

		// Strategy 4: Fallback — site-restricted web search (zero regression)
		if len(results) == 0 && input.Provider == "" {
			webResults, webSource, errResult := academicWebFallback(ctx, deps, input)
			if errResult != nil {
				deps.Metrics.RecordToolCall("academic_search", time.Since(start), fmt.Errorf("fallback failed"), "upstream_error", false)
				auditToolCall(ctx, deps, "academic_search", time.Since(start), fmt.Errorf("fallback failed"), "upstream_error")
				return errResult, nil, nil
			}
			results = webResults
			providerSource = webSource
		}

		// Filter PDF-only if requested
		if input.PDFOnly && len(results) > 0 {
			filtered := make([]search.AcademicResult, 0, len(results))
			for _, r := range results {
				if r.PDFUrl != "" {
					filtered = append(filtered, r)
				}
			}
			results = filtered
		}

		papers := make([]map[string]any, 0, len(results))
		for _, r := range results {
			paper := map[string]any{
				"title":  r.Title,
				"url":    r.URL,
				"source": r.Source,
			}
			if r.DOI != "" {
				paper["doi"] = r.DOI
			}
			if len(r.Authors) > 0 {
				paper["authors"] = r.Authors
			}
			if r.Journal != "" {
				paper["journal"] = r.Journal
			}
			if r.Year > 0 {
				paper["year"] = r.Year
			}
			if r.Abstract != "" {
				paper["abstract"] = r.Abstract
			}
			if r.CitationCount > 0 {
				paper["citationCount"] = r.CitationCount
			}
			if r.OpenAccess {
				paper["openAccess"] = r.OpenAccess
			}
			if r.PDFUrl != "" {
				paper["pdfUrl"] = r.PDFUrl
			}
			papers = append(papers, paper)
		}

		output := map[string]any{
			"papers":       papers,
			"query":        input.Query,
			"totalResults": len(papers),
			"resultCount":  len(papers),
			"source":       providerSource,
		}

		if len(papers) == 0 {
			output["hints"] = buildAcademicHints(input, providerSource)
		}

		jsonBytes, _ := json.Marshal(output)
		if len(papers) > 0 {
			deps.Cache.Set(ctx, cacheKey, jsonBytes, 1*time.Hour)
		}
		deps.Metrics.RecordToolCall("academic_search", time.Since(start), nil, "", false)
		auditToolCall(ctx, deps, "academic_search", time.Since(start), nil, "")

		if input.SessionID != "" {
			trackSources(ctx, deps, input.SessionID, academicResultsToSources(results))
		}

		return structuredResult(jsonBytes), nil, nil
	})
}

// academicWebFallback performs site-restricted web search as a last resort.
func academicWebFallback(ctx context.Context, deps Dependencies, input academicSearchInput) ([]search.AcademicResult, string, *mcp.CallToolResult) {
	source := input.Source
	if source == "" {
		source = "all"
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

	numResults := input.NumResults
	if numResults <= 0 {
		numResults = 5
	}

	// Use explicit provider if it's a web search provider, otherwise use default
	webProvider := input.Provider
	isAcademic := false
	for _, name := range search.SupportedAcademicProviders {
		if name == webProvider {
			isAcademic = true
			break
		}
	}
	if isAcademic {
		webProvider = ""
	}
	provider, errResult := resolveProvider(deps, webProvider)
	if errResult != nil {
		return nil, "", errResult
	}

	webResults, err := provider.Web(ctx, search.WebSearchParams{
		Query:      siteQuery,
		NumResults: numResults,
	})
	if err != nil {
		return nil, "", upstreamErrorResponse("academic search", err)
	}

	results := make([]search.AcademicResult, 0, len(webResults))
	for _, r := range webResults {
		result := search.AcademicResult{
			Title:  r.Title,
			URL:    r.URL,
			Source: detectAcademicSource(r.URL),
		}
		if r.Snippet != "" {
			result.Abstract = r.Snippet
		}
		results = append(results, result)
	}

	return results, "web_search", nil
}

// resolveAcademicSearcher returns an AcademicSearcher for a given provider name.
func resolveAcademicSearcher(deps Dependencies, providerName string) (search.AcademicSearcher, *mcp.CallToolResult) {
	if providerName == "" {
		if as, ok := deps.Search.(search.AcademicSearcher); ok {
			return as, nil
		}
		return nil, nil
	}

	// Check if it's a known academic provider
	for _, name := range search.SupportedAcademicProviders {
		if name == providerName {
			// Try router first
			if router, ok := deps.Search.(*search.Router); ok {
				if as, found := router.AcademicProviderByName(providerName); found {
					return as, nil
				}
			}
			// Try direct academic providers
			if ap, ok := deps.AcademicProviders[providerName]; ok {
				return ap, nil
			}
			envHint := academicProviderEnvHint(providerName)
			return nil, toolError(fmt.Sprintf(
				"Academic provider %q is not configured. %s See docs/API_SETUP.md for details.",
				providerName, envHint))
		}
	}

	// Not an academic-specific provider — it might be a web search provider for fallback
	// Return nil so caller falls through to web search fallback
	return nil, nil
}

func academicProviderEnvHint(name string) string {
	switch name {
	case "openalex":
		return "Set OPENALEX_EMAIL to your contact email."
	case "crossref":
		return "Set CROSSREF_EMAIL to your contact email."
	default:
		return ""
	}
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

func buildAcademicHints(input academicSearchInput, provider string) *ZeroResultHints {
	filters := map[string]string{}
	if input.YearFrom > 0 {
		filters["year_from"] = fmt.Sprintf("%d", input.YearFrom)
	}
	if input.YearTo > 0 {
		filters["year_to"] = fmt.Sprintf("%d", input.YearTo)
	}
	if input.Source != "" && input.Source != "all" {
		filters["source"] = input.Source
	}
	if input.OpenAccess {
		filters["open_access"] = "true"
	}
	if input.PDFOnly {
		filters["pdf_only"] = "true"
	}

	hints := buildZeroResultHints(provider, filters, nil)

	if input.PDFOnly {
		hints.SuggestedActions = append([]HintAction{{
			Action:    "remove_filter",
			Parameter: "pdf_only",
			Detail:    "PDF-only filter is restrictive. Remove to get papers without direct PDF links",
		}}, hints.SuggestedActions...)
	}
	if input.OpenAccess {
		hints.SuggestedActions = append([]HintAction{{
			Action:    "remove_filter",
			Parameter: "open_access",
			Detail:    "Open-access filter excludes paywalled papers. Remove for broader results",
		}}, hints.SuggestedActions...)
	}

	if len(hints.SuggestedActions) > 3 {
		hints.SuggestedActions = hints.SuggestedActions[:3]
	}
	return hints
}
