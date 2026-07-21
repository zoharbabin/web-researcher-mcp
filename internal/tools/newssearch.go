package tools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

type newsSearchInput struct {
	Query      string `json:"query" jsonschema:"Topic or event to find news about. Use specific terms for precision (e.g. 'OpenAI GPT-5 release' not 'AI news').,required"`
	NumResults int    `json:"num_results,omitempty" jsonschema:"Number of articles to return (1-50, default: 5). Brave returns up to 50; Google up to 10."`
	TimeRange  string `json:"time_range,omitempty" jsonschema:"Restrict to a time period: hour, day, week (default), month, or year."`
	SortBy     string `json:"sort_by,omitempty" jsonschema:"Sort order: relevance (default) or date (newest first). Google only — Brave news has no sort param and ignores it."`
	NewsSource string `json:"news_source,omitempty" jsonschema:"Restrict to a specific news outlet domain (e.g. reuters.com, bbc.co.uk). Google only — Brave news has no source filter and ignores it."`
	Country    string `json:"country,omitempty" jsonschema:"Country to localize results to, ISO 3166-1 alpha-2 (e.g. 'us', 'gb'). Honored by Brave news."`
	Language   string `json:"language,omitempty" jsonschema:"Language to scope results to, BCP 47 / 2-letter code (e.g. 'en', 'de'). Honored by Brave news (search_lang)."`
	Safe       string `json:"safe,omitempty" jsonschema:"SafeSearch level: off, moderate, or strict. Honored by Brave news."`
	Provider   string `json:"provider,omitempty" jsonschema:"Force a specific search provider: google, brave, serper, searxng, searchapi, duckduckgo, tavily, exa, hackernews, reddit, github. Omit to use configured default."`
	SessionID  string `json:"sessionId,omitempty" jsonschema:"Link results to a sequential_search session. Sources are automatically recorded for recovery after context loss."`
}

func registerNewsSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "news_search",
		Description:  "Find recent news articles on any topic, returning each article's headline, source, publish time, and snippet. Defaults to the past week, but the freshness window is tunable for breaking news or for looking further back, and results can be limited to a single outlet. Reach for this when recency matters; use web_search for general content, academic_search for research papers, or search_and_scrape when you need the full article text. Errors come back as structured JSON. Results refresh every 15 minutes.",
		Annotations:  readOnlyAnnotations(true, true),
		OutputSchema: newsSearchOutputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input newsSearchInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		input.Query = strings.TrimSpace(input.Query)
		if input.Query == "" {
			return toolError("query is required"), nil, nil
		}

		numResults := input.NumResults
		if numResults > maxNewsResults {
			numResults = maxNewsResults
		}
		if numResults <= 0 {
			numResults = 5
		}
		freshness := input.TimeRange
		if freshness == "" {
			freshness = "week"
		}
		sortBy := input.SortBy
		if sortBy == "" {
			sortBy = "relevance"
		}

		// Include provider + locale/safe so different providers / regions / safe
		// levels never collide on the same query (idempotency + consistency).
		cacheKey := searchCacheKey("news", input.Query, numResults, freshness, sortBy, input.NewsSource, input.Country, input.Language, input.Safe, input.Provider)
		if cached, meta, ok := deps.Cache.GetWithMeta(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("news_search", time.Since(start), nil, "", true)
			rt := routingMeta(search.RoutingDecision{}, time.Since(start), true)
			auditToolCallQuery(ctx, deps, "news_search", time.Since(start), nil, "", "", map[string]any{"cache_hit": true, "routing": rt})
			return withRoutingMeta(cachedResultWithMeta(cached, meta), rt), nil, nil
		}

		provider, errResult := resolveProvider(deps, input.Provider)
		if errResult != nil {
			return errResult, nil, nil
		}

		traceCtx, trace := search.NewRoutingTrace(ctx)
		results, err := provider.News(traceCtx, search.NewsSearchParams{
			Query:      input.Query,
			NumResults: numResults,
			Freshness:  freshness,
			SortBy:     sortBy,
			Source:     input.NewsSource,
			Country:    input.Country,
			Language:   input.Language,
			Safe:       input.Safe,
		})
		if err != nil {
			errCode := "upstream_error"
			if isRateLimitError(err) {
				errCode = "rate_limited"
			}
			deps.Metrics.RecordToolCall("news_search", time.Since(start), err, errCode, false)
			auditToolCall(ctx, deps, "news_search", time.Since(start), err, errCode)
			return upstreamErrorResponse("news search", err), nil, nil
		}
		rt := routingMeta(trace.Decision(), time.Since(start), false)

		output := map[string]any{
			"articles":    results,
			"query":       input.Query,
			"resultCount": len(results),
			"trust":       untrustedContentTrust,
		}

		// Zero-result recovery hints (issue #100): same ZeroResultHints shape as
		// web/academic/patent. Only configured + healthy alternatives suggested.
		if len(results) == 0 {
			used := hintProviderName(provider)
			output["hints"] = buildNewsHints(input, freshness, used, healthyAlternatives(deps, used))
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Cache.Set(ctx, cacheKey, jsonBytes, 15*time.Minute)
		deps.Metrics.RecordToolCall("news_search", time.Since(start), nil, "", false)
		auditToolCallQuery(ctx, deps, "news_search", time.Since(start), nil, "", "", map[string]any{"routing": rt})

		if input.SessionID != "" {
			trackSources(ctx, deps, input.SessionID, newsResultsToSources(results))
		}

		return withRoutingMeta(structuredResult(jsonBytes), rt), nil, nil
	})
}
