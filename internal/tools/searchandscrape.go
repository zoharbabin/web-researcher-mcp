package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

func registerSearchAndScrape(srv *server.MCPServer, deps Dependencies) {
	tool := mcp.NewTool("search_and_scrape",
		mcp.WithDescription("Combined search and scrape pipeline. Searches the web, scrapes top results in parallel, deduplicates content, scores quality, and returns ranked sources."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		mcp.WithNumber("num_results", mcp.Description("Number of sources to scrape (1-10, default: 3)")),
		mcp.WithBoolean("include_sources", mcp.Description("Include individual source details (default: true)")),
		mcp.WithBoolean("deduplicate", mcp.Description("Remove duplicate paragraphs (default: true)")),
		mcp.WithNumber("max_length_per_source", mcp.Description("Max bytes per source (default: 50000)")),
		mcp.WithNumber("total_max_length", mcp.Description("Total max bytes for combined content (default: 300000)")),
		mcp.WithBoolean("filter_by_query", mcp.Description("Filter out low-relevance sources (default: false)")),
	)

	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		query, _ := req.GetArguments()["query"].(string)
		if query == "" {
			return toolError("query is required"), nil
		}

		numResults := intParam(req.GetArguments(), "num_results", 3)
		includeSources := boolParam(req.GetArguments(), "include_sources", true)
		deduplicate := boolParam(req.GetArguments(), "deduplicate", true)
		maxLenPerSource := intParam(req.GetArguments(), "max_length_per_source", 50000)
		totalMaxLen := intParam(req.GetArguments(), "total_max_length", 300000)
		filterByQuery := boolParam(req.GetArguments(), "filter_by_query", false)

		// Step 1: Search
		searchResults, err := deps.Search.Web(ctx, search.WebSearchParams{
			Query:      query,
			NumResults: numResults,
		})
		if err != nil {
			deps.Metrics.RecordToolCall("search_and_scrape", time.Since(start), err, "upstream_error", false)
			auditToolCall(deps, "search_and_scrape", time.Since(start), err, "upstream_error")
			return toolError(fmt.Sprintf("search failed: %v", err)), nil
		}

		if len(searchResults) == 0 {
			output := map[string]any{
				"query":           query,
				"sources":         []any{},
				"combinedContent": "",
				"summary":         map[string]int{"urlsSearched": 0, "urlsScraped": 0, "processingTimeMs": 0},
			}
			jsonBytes, _ := json.Marshal(output)
			return mcp.NewToolResultText(string(jsonBytes)), nil
		}

		// Step 2: Parallel scrape
		type scrapeResult struct {
			url     string
			title   string
			content string
			cType   string
			err     error
		}

		var wg sync.WaitGroup
		results := make([]scrapeResult, len(searchResults))

		for i, sr := range searchResults {
			wg.Add(1)
			go func(idx int, url, title string) {
				defer wg.Done()
				result, err := deps.Scraper.Scrape(ctx, url, maxLenPerSource)
				if err != nil {
					results[idx] = scrapeResult{url: url, title: title, err: err}
					return
				}
				processedContent, _ := deps.Content.Process(result.Content, maxLenPerSource)
				results[idx] = scrapeResult{
					url:     url,
					title:   title,
					content: processedContent,
					cType:   result.ContentType,
				}
			}(i, sr.URL, sr.Title)
		}
		wg.Wait()

		// Step 3: Build sources with quality scores
		type sourceOutput struct {
			URL         string               `json:"url"`
			Title       string               `json:"title,omitempty"`
			Content     string               `json:"content"`
			ContentType string               `json:"contentType"`
			Scores      *content.QualityScore `json:"scores,omitempty"`
		}

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

			src := sourceOutput{
				URL:         r.url,
				Title:       r.title,
				Content:     r.content,
				ContentType: r.cType,
				Scores:      &score,
			}
			sources = append(sources, src)
			combinedParts = append(combinedParts, r.content)
		}

		// Step 4: Dedup combined content
		combined := ""
		if deduplicate {
			for i, part := range combinedParts {
				combinedParts[i] = content.DedupContent(part)
			}
		}
		for _, part := range combinedParts {
			if len(combined)+len(part) > totalMaxLen {
				remaining := totalMaxLen - len(combined)
				if remaining > 0 {
					combined += part[:remaining]
				}
				break
			}
			combined += part + "\n\n---\n\n"
		}

		output := map[string]any{
			"query":           query,
			"combinedContent": combined,
			"summary": map[string]any{
				"urlsSearched":    len(searchResults),
				"urlsScraped":     scraped,
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
			auditToolCall(deps, "search_and_scrape", time.Since(start), nil, "")

		return mcp.NewToolResultText(string(jsonBytes)), nil
	})
}

func boolParam(args map[string]any, key string, fallback bool) bool {
	if v, ok := args[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return fallback
}
