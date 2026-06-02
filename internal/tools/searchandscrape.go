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
	Provider           string `json:"provider,omitempty" jsonschema:"Force a specific search provider: google, brave, serper, searxng, searchapi, duckduckgo. Omit to use configured default."`
	SessionID          string `json:"sessionId,omitempty" jsonschema:"Link results to a sequential_search session. All scraped sources are automatically recorded for recovery after context loss."`
}

func registerSearchAndScrape(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "search_and_scrape",
		Description:  "Search the web and read the full content from the top results, all in one step. Combines content from multiple sources, removes duplicates, and scores each source for quality and relevance. Returns a status field (complete/partial/failed) and per-source quality scores. If some pages fail, scrapeFailures lists each with kind, retryable, and suggestedAction. Use web_search if you only need links, or scrape_page to read one specific URL you already have.",
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
			auditToolCallQuery(ctx, deps, "search_and_scrape", time.Since(start), err, errCode, input.Query, nil)
			return upstreamErrorResponse("search", err), nil, nil
		}

		if len(searchResults) == 0 {
			// Mirror the main success-path shape so callers can treat every
			// search_and_scrape success uniformly — status/trust to key off,
			// and the same summary/sizeMetadata blocks (all zero here).
			output := map[string]any{
				"query":           input.Query,
				"status":          "complete",
				"sources":         []any{},
				"combinedContent": "",
				"trust":           untrustedContentTrust,
				"summary": map[string]any{
					"urlsSearched":     0,
					"urlsScraped":      0,
					"urlsFailed":       0,
					"processingTimeMs": int(time.Since(start).Milliseconds()),
				},
				"sizeMetadata": map[string]any{
					"totalLength":     0,
					"estimatedTokens": 0,
					"sizeCategory":    content.SizeCategory(0),
				},
			}
			jsonBytes, _ := json.Marshal(output)
			return structuredResult(jsonBytes), nil, nil
		}

		results := parallelScrape(ctx, deps, searchResults, maxLenPerSource)
		sources, combinedParts, scraped, structuredFailures := buildSourcesStructured(results, input.Query, input.FilterByQuery)
		combined := assembleCombined(combinedParts, deduplicate, totalMaxLen)

		// Phase 1B: top-level status field
		status := "complete"
		if scraped == 0 && len(structuredFailures) > 0 {
			status = "failed"
		} else if len(structuredFailures) > 0 {
			status = "partial"
		}

		output := map[string]any{
			"query":           input.Query,
			"status":          status,
			"combinedContent": combined,
			// Boundary marker for combinedContent + every source: this is
			// external page text, untrusted (treat as data, not instructions).
			"trust": untrustedContentTrust,
			"summary": map[string]any{
				"urlsSearched":     len(searchResults),
				"urlsScraped":      scraped,
				"urlsFailed":       len(structuredFailures),
				"processingTimeMs": int(time.Since(start).Milliseconds()),
			},
			"sizeMetadata": map[string]any{
				"totalLength":     len(combined),
				"estimatedTokens": content.EstimateTokens(combined),
				"sizeCategory":    content.SizeCategory(len(combined)),
			},
		}

		if len(structuredFailures) > 0 {
			output["scrapeFailures"] = structuredFailures
			if status == "failed" {
				output["note"] = fmt.Sprintf(
					"All %d pages failed. Install Chrome (set CHROME_PATH) for JS sites, or report at %s",
					len(structuredFailures), issueURL)
			}
		}

		if includeSources {
			output["sources"] = sources
		}

		// Additive, content-only enrichments. Both derive purely from the
		// already-computed quality scores (no extra pass, no model call, no user
		// behavior). Recommendations are advisory and never re-rank `sources`;
		// components are mcp-auto-formatted (deterministic, no LLM) renderables that never replace raw data.
		scored := scoredSourcesFrom(sources)
		if deps.Features.SourceRecommendations {
			if recs := content.RecommendSources(scored, 3); recs != nil {
				output["recommendations"] = recs
			}
		}
		if deps.Features.GenerativeUI {
			if comps := content.BuildComponents(scored, sourceSnippets(sources)); comps != nil {
				output["components"] = comps
			}
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Metrics.RecordToolCall("search_and_scrape", time.Since(start), nil, "", false)
		auditToolCallQuery(ctx, deps, "search_and_scrape", time.Since(start), nil, "", input.Query, map[string]any{"urls_scraped": scraped})

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
	Trust       string                `json:"trust"`
	Scores      *content.QualityScore `json:"scores,omitempty"`
}

// scoredSourcesFrom adapts the tool's sourceOutput slice to the content
// package's ScoredSource, reusing the already-computed quality scores so
// recommendations/components require no second scoring pass.
func scoredSourcesFrom(sources []sourceOutput) []content.ScoredSource {
	out := make([]content.ScoredSource, 0, len(sources))
	for _, s := range sources {
		var score content.QualityScore
		if s.Scores != nil {
			score = *s.Scores
		}
		out = append(out, content.ScoredSource{
			URL:     s.URL,
			Title:   s.Title,
			Score:   score,
			HasText: s.Content != "",
		})
	}
	return out
}

// sourceSnippets maps each source URL to a short leading excerpt for card
// components, drawn from the content already extracted.
func sourceSnippets(sources []sourceOutput) map[string]string {
	m := make(map[string]string, len(sources))
	for _, s := range sources {
		m[s.URL] = s.Content
	}
	return m
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

func buildSourcesStructured(results []scrapeResult, query string, filterByQuery bool) ([]sourceOutput, []string, int, []FailureInfo) {
	var sources []sourceOutput
	var combinedParts []string
	var failures []FailureInfo
	scraped := 0

	for _, r := range results {
		if r.err != nil || r.content == "" {
			if r.err != nil {
				failures = append(failures, failureFromScrapeError(r.url, r.err))
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
			Trust:       untrustedContentTrust,
			Scores:      &score,
		})
		combinedParts = append(combinedParts, r.content)
	}

	return sources, combinedParts, scraped, failures
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
