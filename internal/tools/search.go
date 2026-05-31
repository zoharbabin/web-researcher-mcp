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
	Lens         string `json:"lens,omitempty" jsonschema:"Focus your search on trusted sites in a specific field: docs, academic, clinical, security, journalism, programming, news, tech, legal, medical, finance, science, government. Only one lens can be active at a time (overrides the site parameter)."`
	Provider     string `json:"provider,omitempty" jsonschema:"Choose which search engine to use for this query: google, brave, serper, searxng, searchapi, duckduckgo. Leave empty to use the default. Returns an error if the chosen provider isn't set up."`
	SessionID    string `json:"sessionId,omitempty" jsonschema:"Link results to a sequential_search session. Sources are automatically recorded in the session for recovery after context loss."`
}

func registerWebSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "web_search",
		Description:  "Search the web and get a list of relevant pages with titles and snippets — without reading the full page content. Narrow results to one domain with the site parameter, or apply a search lens to restrict to trusted sites in a field (see the lens parameter for the full list). Use search_and_scrape if you need full page text, news_search for current events, or academic_search for research papers. Results stay fresh for 30 minutes; use time_range to get more recent results.",
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

		// The cache key MUST include every result-affecting parameter — most
		// importantly the provider, so two providers queried with the same terms
		// do not collide and serve each other's cached results (idempotency +
		// consistency across calls).
		cacheKey := searchCacheKey("web", input.Query, numResults, input.TimeRange, input.Safe, input.Language, input.Site, input.Lens, input.Provider, input.ExactTerms, input.ExcludeTerms, input.Country)
		if cached, meta, ok := deps.Cache.GetWithMeta(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("web_search", time.Since(start), nil, "", true)
			auditToolCallQuery(ctx, deps, "web_search", time.Since(start), nil, "", input.Query, map[string]any{"cache_hit": true})
			return cachedResultWithMeta(cached, meta), nil, nil
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
			auditToolCallQuery(ctx, deps, "web_search", time.Since(start), err, errCode, input.Query, nil)
			return upstreamErrorResponse("search", err), nil, nil
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

		const webSearchTTL = 30 * time.Minute
		jsonBytes, _ := json.Marshal(output)
		deps.Cache.Set(ctx, cacheKey, jsonBytes, webSearchTTL)
		deps.Metrics.RecordToolCall("web_search", time.Since(start), nil, "", false)
		auditToolCallQuery(ctx, deps, "web_search", time.Since(start), nil, "", input.Query, map[string]any{"result_count": len(results)})

		if input.SessionID != "" {
			trackSources(ctx, deps, input.SessionID, searchResultsToSources(results))
		}

		return freshResult(jsonBytes, webSearchTTL), nil, nil
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

// auditToolCall is the shared audit sink for tool handlers. It builds a
// tool_call event, correlates it with the request via
// auth.RequestIDFromContext (H6), and records duration/success/error. The
// error string is passed through audit.MaskSecrets so credentials echoed by
// upstream errors never persist. This signature is used by every tool; the
// query-bearing variant auditToolCallQuery layers privacy-gated metadata on top.
func auditToolCall(ctx context.Context, deps Dependencies, toolName string, duration time.Duration, err error, errCode string) {
	auditToolCallQuery(ctx, deps, toolName, duration, err, errCode, "", nil)
}

// auditToolCallQuery is the metadata-aware variant for query tools.
//
// Privacy (decision f / DOC-VERIFY): the raw query is attached to metadata
// only when the auditor reports IncludeRequestBody()==true
// (AUDIT_INCLUDE_REQUEST_BODY). Otherwise only the query length is recorded —
// never the text. All metadata string values are passed through
// audit.MaskSecrets so secrets never persist to the audit sink.
func auditToolCallQuery(ctx context.Context, deps Dependencies, toolName string, duration time.Duration, err error, errCode, query string, extra map[string]any) {
	if deps.Auditor == nil {
		return
	}
	event := audit.NewEvent("tool_call", auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx))
	event.ToolName = toolName
	if rid := auth.RequestIDFromContext(ctx); rid != "" {
		event.RequestID = rid
	}
	event.Duration = duration.Milliseconds()
	event.Success = err == nil
	if errCode != "" {
		event.ErrorCode = errCode
	}

	meta := make(map[string]any, len(extra)+2)
	for k, v := range extra {
		if s, ok := v.(string); ok {
			meta[k] = audit.MaskSecrets(s)
		} else {
			meta[k] = v
		}
	}
	if query != "" {
		if deps.Auditor.IncludeRequestBody() {
			meta["query"] = audit.MaskSecrets(query)
		} else {
			meta["query_length"] = len(query)
		}
	}
	if err != nil {
		meta["error"] = audit.MaskSecrets(err.Error())
	}
	if len(meta) > 0 {
		event.Metadata = meta
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
	seconds := 60
	provider := extractProviderName(err)
	return structuredError(
		fmt.Sprintf("Rate limited (%s). Wait 60 seconds and retry, or try a different provider.", provider),
		ToolError{
			Kind:              ErrKindRateLimit,
			Retryable:         true,
			RetryAfterSeconds: &seconds,
			SuggestedAction:   ActionRetryAfterDelay,
			Provider:          provider,
		},
	)
}

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "rate limited") || strings.Contains(s, "429") || strings.Contains(s, "quota")
}

func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "401") || strings.Contains(s, "API key not valid") || strings.Contains(s, "unauthorized") || strings.Contains(s, "INVALID_ARGUMENT")
}

func upstreamErrorResponse(toolName string, err error) *mcp.CallToolResult {
	if isRateLimitError(err) {
		return rateLimitError(err)
	}
	provider := extractProviderName(err)
	// Upstream error strings occasionally echo back credentials embedded in a
	// request URL or an API response (e.g. ?key=AIza...). Mask before the text
	// reaches an LLM-facing result or any downstream audit log.
	detail := audit.MaskSecrets(err.Error())
	if isAuthError(err) {
		return structuredError(
			fmt.Sprintf("%s: authentication failed. Check API key in .env.example.", toolName),
			ToolError{
				Kind:            ErrKindAuth,
				Retryable:       false,
				SuggestedAction: ActionCheckAPIKey,
				Provider:        provider,
				Detail:          detail,
			},
		)
	}
	return structuredError(
		fmt.Sprintf("%s failed: %s. Try a different provider or report at %s", toolName, detail, issueURL),
		ToolError{
			Kind:            ErrKindUpstream,
			Retryable:       true,
			SuggestedAction: ActionTryDifferentProvider,
			Provider:        provider,
			Detail:          detail,
		},
	)
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

	// Check if it's a known/supported provider name
	supported := false
	for _, name := range allSupportedProviders() {
		if name == providerName {
			supported = true
			break
		}
	}

	if !supported {
		return nil, structuredError(
			fmt.Sprintf("Unknown search provider %q. Supported providers: %s.",
				providerName, strings.Join(allSupportedProviders(), ", ")),
			ToolError{
				Kind:            ErrKindConfig,
				Retryable:       false,
				SuggestedAction: ActionTryDifferentProvider,
				Alternatives:    allSupportedProviders(),
			})
	}

	// Check if the default provider matches
	if deps.Search.Name() == providerName {
		return deps.Search, nil
	}

	// Check all available providers (any provider with credentials is instantiated)
	if p, ok := deps.SearchProviders[providerName]; ok {
		return p, nil
	}

	// Try the router if available (for backward compatibility)
	if router, ok := deps.Search.(*search.Router); ok {
		if p, found := router.ProviderByName(providerName); found {
			return p, nil
		}
	}

	return nil, structuredError(
		fmt.Sprintf("Search provider %q is not configured. Set the appropriate API key. See .env.example.",
			providerName),
		ToolError{
			Kind:            ErrKindConfig,
			Retryable:       false,
			SuggestedAction: ActionCheckAPIKey,
			Provider:        providerName,
		})
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
			return nil, structuredError(
				fmt.Sprintf("Patent provider %q is not configured. %s", providerName, envHint),
				ToolError{
					Kind:            ErrKindConfig,
					Retryable:       false,
					SuggestedAction: ActionCheckAPIKey,
					Provider:        providerName,
				})
		}
	}

	// Check if it's a known web search provider (valid for fallback)
	for _, name := range search.SupportedProviders {
		if name == providerName {
			return nil, nil
		}
	}

	// Completely unknown provider — return error with full list
	return nil, structuredError(
		fmt.Sprintf("Unknown patent provider %q. Supported providers: %s.",
			providerName, strings.Join(allSupportedProviders(), ", ")),
		ToolError{
			Kind:            ErrKindConfig,
			Retryable:       false,
			SuggestedAction: ActionTryDifferentProvider,
			Alternatives:    allSupportedProviders(),
		})
}

func allSupportedProviders() []string {
	seen := make(map[string]bool)
	var all []string
	for _, lists := range [][]string{
		search.SupportedProviders,
		search.SupportedPatentProviders,
		search.SupportedAcademicProviders,
	} {
		for _, name := range lists {
			if !seen[name] {
				seen[name] = true
				all = append(all, name)
			}
		}
	}
	return all
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
