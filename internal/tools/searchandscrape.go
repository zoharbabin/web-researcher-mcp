package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
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
}

func registerSearchAndScrape(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "search_and_scrape",
		Description:  "Search the web and extract full content from top results in one call. Scrapes in parallel (max 5 concurrent), deduplicates content across sources, and scores each source on relevance and quality. Returns JSON with fields: query, combinedContent, sources (array of {url, title, content, contentType, scores} — included when include_sources=true), summary ({urlsSearched, urlsScraped, processingTimeMs}), sizeMetadata ({totalLength, estimatedTokens, sizeCategory}). On zero search matches returns empty combinedContent with urlsSearched: 0. Individual scrape failures are silently skipped (urlsScraped < urlsSearched indicates partial failures). num_results controls sources scraped (more = slower, typically 2-15s). Subject to upstream API quotas with automatic provider fallback. Use web_search instead if you only need URLs; use scrape_page for a single known URL. Not cached (combines live search + scrape).",
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

		searchResults, err := deps.Search.Web(ctx, search.WebSearchParams{
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
			if isRateLimitError(err) {
				return rateLimitError(err), nil, nil
			}
			return toolError(fmt.Sprintf("search failed: %v", err)), nil, nil
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
		sources, combinedParts, scraped := buildSources(results, input.Query, input.FilterByQuery)
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

		if includeSources {
			output["sources"] = sources
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Metrics.RecordToolCall("search_and_scrape", time.Since(start), nil, "", false)
		auditToolCall(ctx, deps, "search_and_scrape", time.Since(start), nil, "")

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

func buildSources(results []scrapeResult, query string, filterByQuery bool) ([]sourceOutput, []string, int) {
	var sources []sourceOutput
	var combinedParts []string
	scraped := 0

	for _, r := range results {
		if r.err != nil || r.content == "" {
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

	return sources, combinedParts, scraped
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
