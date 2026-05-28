package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

type searchAndScrapeInput struct {
	Query              string `json:"query" jsonschema:"The research question or topic to search and extract content for. Use natural language or keyword-rich queries.,required"`
	NumResults         int    `json:"num_results,omitempty" jsonschema:"Number of top search results to scrape (1-10, default: 3). More sources = slower but more comprehensive."`
	IncludeSources     *bool  `json:"include_sources,omitempty" jsonschema:"Include per-source content and quality scores in response (default: true). Set false to reduce response size."`
	Deduplicate        *bool  `json:"deduplicate,omitempty" jsonschema:"Remove duplicate paragraphs across sources (default: true). Disable only if exact repetition matters."`
	MaxLengthPerSource int    `json:"max_length_per_source,omitempty" jsonschema:"Max content bytes extracted per source (default: 50000)."`
	TotalMaxLength     int    `json:"total_max_length,omitempty" jsonschema:"Max total bytes for combined output (default: 300000). Reduce for faster, more concise results."`
	FilterByQuery      bool   `json:"filter_by_query,omitempty" jsonschema:"Remove sources with low relevance to the query (default: false). Enable for precision over recall."`
	Provider           string `json:"provider,omitempty" jsonschema:"Force a specific search provider: google, brave, serper, searxng, searchapi. Omit to use configured default."`
	SessionID          string `json:"sessionId,omitempty" jsonschema:"Link results to a sequential_search session. All scraped sources are automatically recorded for recovery after context loss."`
}

func registerSearchAndScrape(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "search_and_scrape",
		Description:  "Search the web and read the full content from the top results, all in one step. Combines content from multiple sources, removes duplicates, and scores each source for quality and relevance. Great for in-depth research on a topic. More sources means more thorough results but takes longer (typically 2-15 seconds). Use web_search if you only need links, or scrape_page to read one specific URL you already have.",
		Annotations:  readOnlyAnnotations(true, true),
		OutputSchema: searchAndScrapeOutputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input searchAndScrapeInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		if input.Query == "" {
			return toolError("query is required"), nil, nil
		}

		numResults := input.NumResults
		if numResults <= 0 {
			numResults = 3
		}
		includeSources := input.IncludeSources == nil || *input.IncludeSources
		deduplicate := input.Deduplicate == nil || *input.Deduplicate
		maxLenPerSource := input.MaxLengthPerSource
		if maxLenPerSource <= 0 {
			maxLenPerSource = 50000
		}
		totalMaxLen := input.TotalMaxLength
		if totalMaxLen <= 0 {
			totalMaxLen = 300000
		}

		provider, errResult := resolveProvider(deps, input.Provider)
		if errResult != nil {
			return errResult, nil, nil
		}

		searchResults, err := provider.Web(ctx, search.WebSearchParams{
			Query:      input.Query,
			NumResults: numResults,
		})
		if err != nil {
			errCode := "upstream_error"
			if isRateLimitError(err) {
				errCode = "rate_limited"
			}
			deps.Metrics.RecordToolCall("search_and_scrape", time.Since(start), err, errCode, false)
			auditToolCall(ctx, deps, "search_and_scrape", time.Since(start), err, errCode)
			return upstreamErrorResponse("search", err), nil, nil
		}

		if len(searchResults) == 0 {
			output := map[string]any{
				"query":           input.Query,
				"sources":         []any{},
				"combinedContent": "",
				"summary":         map[string]int{"urlsSearched": 0, "urlsScraped": 0, "processingTimeMs": 0},
			}
			jsonBytes, _ := json.Marshal(output)
			return structuredResult(jsonBytes), nil, nil
		}

		results := parallelScrape(ctx, deps, searchResults, maxLenPerSource)
		sources, combinedParts, scraped, failures := buildSources(results, input.Query, input.FilterByQuery)
		combined := assembleCombined(combinedParts, deduplicate, totalMaxLen)

		output := map[string]any{
			"query":           input.Query,
			"combinedContent": combined,
			"summary": map[string]any{
				"urlsSearched":     len(searchResults),
				"urlsScraped":      scraped,
				"processingTimeMs": int(time.Since(start).Milliseconds()),
			},
			"sizeMetadata": map[string]any{
				"totalLength":     len(combined),
				"estimatedTokens": content.EstimateTokens(combined),
				"sizeCategory":    content.SizeCategory(len(combined)),
			},
		}

		if len(failures) > 0 {
			output["scrapeFailures"] = failures
			if scraped == 0 {
				output["note"] = fmt.Sprintf(
					"All %d pages failed to scrape. This may indicate the sites require JavaScript rendering (install Chrome and set CHROME_PATH), "+
						"use bot detection, or require authentication. If this is unexpected, report at %s",
					len(failures), issueURL)
			}
		}

		if includeSources {
			output["sources"] = sources
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Metrics.RecordToolCall("search_and_scrape", time.Since(start), nil, "", false)
		auditToolCall(ctx, deps, "search_and_scrape", time.Since(start), nil, "")

		if input.SessionID != "" {
			trackSources(ctx, deps, input.SessionID, sourceOutputsToSources(sources))
		}

		return structuredResult(jsonBytes), nil, nil
	})
}

type scrapeResult struct {
	url     string
	title   string
	content string
	cType   string
	err     error
}

type sourceOutput struct {
	URL         string                `json:"url"`
	Title       string                `json:"title,omitempty"`
	Content     string                `json:"content"`
	ContentType string                `json:"contentType"`
	Scores      *content.QualityScore `json:"scores,omitempty"`
}

func parallelScrape(ctx context.Context, deps Dependencies, searchResults []search.SearchResult, maxLen int) []scrapeResult {
	var wg sync.WaitGroup
	results := make([]scrapeResult, len(searchResults))

	for i, sr := range searchResults {
		wg.Add(1)
		go func(idx int, url, title string) {
			defer wg.Done()
			result, err := deps.Scraper.Scrape(ctx, url, maxLen)
			if err != nil {
				results[idx] = scrapeResult{url: url, title: title, err: err}
				return
			}
			processedContent, _ := deps.Content.Process(result.Content, maxLen)
			results[idx] = scrapeResult{
				url:     url,
				title:   title,
				content: processedContent,
				cType:   result.ContentType,
			}
		}(i, sr.URL, sr.Title)
	}
	wg.Wait()
	return results
}

type scrapeFailureOutput struct {
	URL    string `json:"url"`
	Reason string `json:"reason"`
	Kind   string `json:"kind,omitempty"`
}

func buildSources(results []scrapeResult, query string, filterByQuery bool) ([]sourceOutput, []string, int, []scrapeFailureOutput) {
	var sources []sourceOutput
	var combinedParts []string
	var failures []scrapeFailureOutput
	scraped := 0

	for _, r := range results {
		if r.err != nil || r.content == "" {
			if r.err != nil {
				f := scrapeFailureOutput{URL: r.url, Reason: r.err.Error()}
				if se, ok := r.err.(*scraper.ScrapeError); ok {
					f.Kind = scrapeErrorKindName(se.Kind)
				}
				failures = append(failures, f)
			}
			continue
		}
		scraped++

		score := content.ScoreQuality(content.QualityInput{
			Content: r.content,
			URL:     r.url,
			Title:   r.title,
			Query:   query,
		})

		if filterByQuery && score.Relevance < 0.3 {
			continue
		}

		sources = append(sources, sourceOutput{
			URL:         r.url,
			Title:       r.title,
			Content:     r.content,
			ContentType: r.cType,
			Scores:      &score,
		})
		combinedParts = append(combinedParts, r.content)
	}

	return sources, combinedParts, scraped, failures
}

func scrapeErrorKindName(kind scraper.ErrorKind) string {
	switch kind {
	case scraper.ErrNetwork:
		return "network"
	case scraper.ErrBlocked:
		return "blocked"
	case scraper.ErrBrowser:
		return "browser_unavailable"
	case scraper.ErrContent:
		return "no_content"
	case scraper.ErrAuth:
		return "auth_required"
	case scraper.ErrRateLimit:
		return "rate_limited"
	default:
		return "unknown"
	}
}

func assembleCombined(parts []string, deduplicate bool, totalMaxLen int) string {
	if deduplicate {
		for i, part := range parts {
			parts[i] = content.DedupContent(part)
		}
	}

	var combined string
	for _, part := range parts {
		if len(combined)+len(part) > totalMaxLen {
			remaining := totalMaxLen - len(combined)
			if remaining > 0 {
				combined += part[:remaining]
			}
			break
		}
		combined += part + "\n\n---\n\n"
	}
	return combined
}
