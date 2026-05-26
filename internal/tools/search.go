package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

type webSearchInput struct {
	Query        string `json:"query" jsonschema:"The search query text (1-500 chars). Be specific with key terms and qualifiers for better results.,required"`
	NumResults   int    `json:"num_results,omitempty" jsonschema:"Number of results to return (1-10). Default: 5. Higher values increase latency."`
	TimeRange    string `json:"time_range,omitempty" jsonschema:"Restrict to a time period: day, week, month, or year. Omit for all-time results."`
	Safe         string `json:"safe,omitempty" jsonschema:"SafeSearch level: off, medium (default), or high."`
	Language     string `json:"language,omitempty" jsonschema:"Filter by language using ISO 639-1 code (e.g. en, fr, de)."`
	Site         string `json:"site,omitempty" jsonschema:"Restrict to a single domain (e.g. stackoverflow.com). Cannot combine with lens."`
	ExactTerms   string `json:"exact_terms,omitempty" jsonschema:"Phrase that must appear verbatim in results."`
	ExcludeTerms string `json:"exclude_terms,omitempty" jsonschema:"Terms to exclude from results (space-separated)."`
	Country      string `json:"country,omitempty" jsonschema:"Restrict to a country using ISO 3166-1 alpha-2 code (e.g. US, GB)."`
	Lens         string `json:"lens,omitempty" jsonschema:"Apply a curated domain-restricted search lens: programming, news, tech, legal, medical, finance, science, government. Overrides site parameter."`
	Provider     string `json:"provider,omitempty" jsonschema:"Force a specific search provider for this query: google, brave, serper, searxng, searchapi. Omit to use the configured default. Returns an error if the requested provider is not configured."`
}

func registerWebSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "web_search",
		Description:  "Search the web for URLs and metadata without fetching page content. Returns JSON with fields: urls (string array), results (array of {title, url, snippet, displayLink}), query, resultCount. lens and site are mutually exclusive (lens overrides site if both provided). On no matches returns resultCount: 0 with empty results array; on failure returns isError with message. Subject to upstream API quotas with automatic provider fallback and circuit breaker recovery. Supports lenses (programming, news, tech, legal, medical, finance, science, government) for domain-restricted search. Use search_and_scrape instead when you need full page content; use news_search for time-sensitive current events; use academic_search for scholarly papers. Results cached 30 min; use time_range to constrain freshness when cache staleness is a concern.",
		Annotations:  readOnlyAnnotations(true, true),
		OutputSchema: webSearchOutputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input webSearchInput) (*mcp.CallToolResult, any, error) {
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

		cacheKey := searchCacheKey("web", input.Query, numResults, input.TimeRange, input.Safe, input.Language, input.Site, input.Lens)
		if cached, ok := deps.Cache.Get(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("web_search", time.Since(start), nil, "", true)
			ev := audit.NewEvent("tool_call", auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx))
			ev.ToolName = "web_search"
			ev.Duration = time.Since(start).Milliseconds()
			ev.Success = true
			ev.Metadata = map[string]any{"cache_hit": true, "query": input.Query}
			deps.Auditor.Log(ev)
			return structuredResult(cached), nil, nil
		}

		params := search.WebSearchParams{
			Query:        input.Query,
			NumResults:   numResults,
			TimeRange:    input.TimeRange,
			Safe:         input.Safe,
			Language:     input.Language,
			Country:      input.Country,
			Site:         input.Site,
			ExactTerms:   input.ExactTerms,
			ExcludeTerms: input.ExcludeTerms,
		}

		if input.Lens != "" {
			registry := search.GetLensRegistry()
			lensData, ok := registry.Get(input.Lens)
			if !ok {
				return toolError(fmt.Sprintf("unknown lens: %s. Available: %v", input.Lens, registry.List())), nil, nil
			}

			if lensData.CX != "" {
				params.Query = input.Query
				params.Site = ""
			} else {
				params.Query = registry.BuildSiteQuery(input.Query, lensData)
			}
		}

		provider, errResult := resolveProvider(deps, input.Provider)
		if errResult != nil {
			return errResult, nil, nil
		}

		results, err := provider.Web(ctx, params)
		if err != nil {
			errCode := "upstream_error"
			if isRateLimitError(err) {
				errCode = "rate_limited"
			}
			deps.Metrics.RecordToolCall("web_search", time.Since(start), err, errCode, false)
			ev := audit.NewEvent("tool_call", auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx))
			ev.ToolName = "web_search"
			ev.Duration = time.Since(start).Milliseconds()
			ev.Success = false
			ev.ErrorCode = errCode
			ev.Metadata = map[string]any{"query": input.Query, "error": err.Error()}
			deps.Auditor.Log(ev)
			if isRateLimitError(err) {
				return rateLimitError(err), nil, nil
			}
			return toolError(fmt.Sprintf("search failed: %v", err)), nil, nil
		}

		urls := make([]string, len(results))
		for i, r := range results {
			urls[i] = r.URL
		}

		output := map[string]any{
			"urls":        urls,
			"query":       input.Query,
			"resultCount": len(results),
			"results":     results,
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Cache.Set(ctx, cacheKey, jsonBytes, 30*time.Minute)
		deps.Metrics.RecordToolCall("web_search", time.Since(start), nil, "", false)
		ev := audit.NewEvent("tool_call", auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx))
		ev.ToolName = "web_search"
		ev.Duration = time.Since(start).Milliseconds()
		ev.Success = true
		ev.Metadata = map[string]any{"query": input.Query, "result_count": len(results)}
		deps.Auditor.Log(ev)

		return structuredResult(jsonBytes), nil, nil
	})
}

func searchCacheKey(toolName string, parts ...any) string {
	h := sha256.New()
	h.Write([]byte(toolName))
	for _, p := range parts {
		fmt.Fprintf(h, "|%v", p)
	}
	return "search:" + hex.EncodeToString(h.Sum(nil))[:32]
}

func auditToolCall(ctx context.Context, deps Dependencies, toolName string, duration time.Duration, err error, errCode string) {
	if deps.Auditor == nil {
		return
	}
	tenantID := auth.TenantIDFromContext(ctx)
	userID := auth.UserIDFromContext(ctx)
	event := audit.NewEvent("tool_call", tenantID, userID)
	event.ToolName = toolName
	event.Duration = duration.Milliseconds()
	event.Success = err == nil
	if errCode != "" {
		event.ErrorCode = errCode
	}
	deps.Auditor.Log(event)
}

func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
		IsError: true,
	}
}

func rateLimitError(err error) *mcp.CallToolResult {
	msg := fmt.Sprintf("Rate limited by upstream provider: %v. Retry after 60s, or use a different search provider.", err)
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
		IsError: true,
	}
}

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "rate limited") || strings.Contains(s, "429") || strings.Contains(s, "quota")
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}
}

func structuredResult(jsonBytes []byte) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(jsonBytes)},
		},
		StructuredContent: json.RawMessage(jsonBytes),
	}
}

// resolveProvider returns the search provider to use for a request.
// If providerName is empty, returns deps.Search (the configured default/router).
// If providerName is set, attempts to resolve it from the router.
// Returns (nil, errorResult) if the provider cannot be resolved.
func resolveProvider(deps Dependencies, providerName string) (search.Provider, *mcp.CallToolResult) {
	if providerName == "" {
		return deps.Search, nil
	}

	// Check if it's a known/supported provider name (web or patent-specific)
	supported := false
	for _, name := range search.SupportedProviders {
		if name == providerName {
			supported = true
			break
		}
	}
	if !supported {
		for _, name := range search.SupportedPatentProviders {
			if name == providerName {
				supported = true
				break
			}
		}
	}

	if !supported {
		all := append(search.SupportedProviders, search.SupportedPatentProviders...)
		return nil, toolError(fmt.Sprintf(
			"Unknown search provider %q. Supported providers: %s. "+
				"If you'd like us to add support for this provider, please open an issue at "+
				"https://github.com/zoharbabin/web-researcher-mcp/issues",
			providerName, strings.Join(all, ", ")))
	}

	// Try to get it from the router
	if router, ok := deps.Search.(*search.Router); ok {
		p, found := router.ProviderByName(providerName)
		if found {
			return p, nil
		}
	} else if deps.Search.Name() == providerName {
		return deps.Search, nil
	}

	return nil, toolError(fmt.Sprintf(
		"Search provider %q is not configured. To use it, set the appropriate API key "+
			"in your environment. See .env.example for required variables per provider.",
		providerName))
}

// resolvePatentSearcher returns a PatentSearcher for a given provider name.
// Checks the main provider, router, patent-only providers, and full providers
// that implement PatentSearcher (e.g. SearchAPI).
func resolvePatentSearcher(deps Dependencies, providerName string) (search.PatentSearcher, *mcp.CallToolResult) {
	if providerName == "" {
		// Check if the main provider implements PatentSearcher
		if ps, ok := deps.Search.(search.PatentSearcher); ok {
			return ps, nil
		}
		return nil, nil
	}

	// Try router's patent provider lookup (covers both full and patent-only providers)
	if router, ok := deps.Search.(*search.Router); ok {
		if ps, found := router.PatentProviderByName(providerName); found {
			return ps, nil
		}
	}

	// Try direct patent providers
	if pp, ok := deps.PatentProviders[providerName]; ok {
		return pp, nil
	}

	// Check if the main provider matches the requested name and implements PatentSearcher
	// (single-provider mode: e.g. SEARCH_PROVIDER=searchapi without routing)
	if deps.Search.Name() == providerName {
		if ps, ok := deps.Search.(search.PatentSearcher); ok {
			return ps, nil
		}
	}

	// Check if it's a known patent provider that's not configured
	for _, name := range search.SupportedPatentProviders {
		if name == providerName {
			envHint := patentProviderEnvHint(providerName)
			return nil, toolError(fmt.Sprintf(
				"Patent provider %q is not configured. %s See .env.example for details.",
				providerName, envHint))
		}
	}

	return nil, nil
}

func patentProviderEnvHint(name string) string {
	switch name {
	case "epo":
		return "Set EPO_OPS_CONSUMER_KEY and EPO_OPS_CONSUMER_SECRET."
	case "lens":
		return "Set LENS_API_TOKEN."
	case "uspto":
		return "Set USPTO_API_KEY."
	case "searchapi":
		return "Set SEARCHAPI_API_KEY."
	default:
		return ""
	}
}
