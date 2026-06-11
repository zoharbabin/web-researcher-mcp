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
	"github.com/zoharbabin/web-researcher-mcp/internal/consent"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// maxNumResults is the documented (jsonschema 1-10) upper bound on result count,
// enforced server-side at the tool boundary so a request can't drive unbounded
// goroutine fan-out or upstream-provider billing regardless of provider behavior
// (defense-in-depth; OWASP Agentic ASI06).
const maxNumResults = 10

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
	Lens         string `json:"lens,omitempty" jsonschema:"Focus your search on trusted sites in a specific field: docs, academic, academic-extended, clinical, security, journalism, programming, devops, news, tech, legal, medical, finance, science, government. Only one lens can be active at a time (overrides the site parameter)."`
	Provider     string `json:"provider,omitempty" jsonschema:"Choose which search engine to use for this query: google, brave, serper, searxng, searchapi, duckduckgo, tavily, exa. Leave empty to use the default. Returns an error if the chosen provider isn't set up."`
	SessionID    string `json:"sessionId,omitempty" jsonschema:"Link results to a sequential_search session. Sources are automatically recorded in the session for recovery after context loss."`
	Claim        string `json:"claim,omitempty" jsonschema:"Optional claim to evaluate against each result's snippet. When set, each result gains a claimSignal (the most claim-relevant snippet sentence) to help triage which links to read; for full-text evidence use search_and_scrape with claim. Evidence only — the server never decides supports/contradicts."`
}

func registerWebSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "web_search",
		Description:  "Search the web and get a list of relevant pages with titles and snippets — without reading the full page content. Narrow results to one domain with the site parameter, or apply a search lens to restrict to trusted sites in a field (see the lens parameter for the full list). Use search_and_scrape if you need full page text, news_search for current events, or academic_search for research papers. Results stay fresh for 30 minutes; use time_range to get more recent results.",
		Annotations:  readOnlyAnnotations(true, true),
		OutputSchema: webSearchOutputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input webSearchInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		input.Query = strings.TrimSpace(input.Query)
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
		if numResults > maxNumResults {
			numResults = maxNumResults // clamp to the documented ceiling (defense-in-depth, ASI06)
		}

		// The cache key MUST include every result-affecting parameter — most
		// importantly the provider, so two providers queried with the same terms
		// do not collide and serve each other's cached results (idempotency +
		// consistency across calls).
		cacheKey := searchCacheKey("web", input.Query, numResults, input.TimeRange, input.Safe, input.Language, input.Site, input.Lens, input.Provider, input.ExactTerms, input.ExcludeTerms, input.Country, input.Claim)
		if cached, meta, ok := deps.Cache.GetWithMeta(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("web_search", time.Since(start), nil, "", true)
			rt := routingMeta(search.RoutingDecision{}, time.Since(start), true)
			auditToolCallQuery(ctx, deps, "web_search", time.Since(start), nil, "", input.Query, map[string]any{"cache_hit": true, "routing": rt})
			return withRoutingMeta(cachedResultWithMeta(cached, meta), rt), nil, nil
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

			// A lens OVERRIDES the site parameter (per the schema contract): the
			// lens already scopes the search to its own domain set, so a sibling
			// site: filter would AND with it and over-constrain to nothing. Clear
			// it on BOTH paths — the CX engine is itself the scope, and the
			// operator-injection path bakes the lens domains into the query.
			params.Site = ""
			if lensData.CX != "" {
				params.Query = input.Query
			} else {
				params.Query = registry.BuildSiteQuery(input.Query, lensData)
			}
		}

		provider, errResult := resolveProvider(deps, input.Provider)
		if errResult != nil {
			return errResult, nil, nil
		}

		// Install a routing trace so a multi-provider Router records which
		// provider served, what was attempted, and whether a fallback fired
		// (#58). A non-Router provider leaves the trace empty (no routing _meta).
		traceCtx, trace := search.NewRoutingTrace(ctx)
		results, err := provider.Web(traceCtx, params)
		if err != nil {
			errCode := "upstream_error"
			if isRateLimitError(err) {
				errCode = "rate_limited"
			}
			deps.Metrics.RecordToolCall("web_search", time.Since(start), err, errCode, false)
			auditToolCallQuery(ctx, deps, "web_search", time.Since(start), err, errCode, input.Query, nil)
			return upstreamErrorResponse("search", err), nil, nil
		}
		rt := routingMeta(trace.Decision(), time.Since(start), false)

		urls := make([]string, len(results))
		for i, r := range results {
			urls[i] = r.URL
		}

		output := map[string]any{
			"urls":        urls,
			"query":       input.Query,
			"resultCount": len(results),
			"results":     results,
			"trust":       untrustedContentTrust,
		}

		// Enrich results with domain reputation (#198) and optional claim signal
		// (#66). enrichResultsWithReputation always attaches sourceReputation for
		// known hosts (descriptive, never gates/reorders) and adds claimSignal
		// when a claim is set — one pass covers both enhancements.
		output["results"] = enrichResultsWithReputation(results, input.Claim)

		// Zero-result recovery hints (issue #100): reuse the shared
		// ZeroResultHints machinery (parity with academic_search/patent_search).
		// Only configured + healthy alternative providers are suggested.
		if len(results) == 0 {
			used := hintProviderName(provider)
			output["hints"] = buildWebHints(input, used, healthyAlternatives(deps, used))
		}

		const webSearchTTL = 30 * time.Minute
		jsonBytes, _ := json.Marshal(output)
		deps.Cache.Set(ctx, cacheKey, jsonBytes, webSearchTTL)
		deps.Metrics.RecordToolCall("web_search", time.Since(start), nil, "", false)
		auditToolCallQuery(ctx, deps, "web_search", time.Since(start), nil, "", input.Query, map[string]any{"result_count": len(results), "routing": rt})

		if input.SessionID != "" {
			trackSources(ctx, deps, input.SessionID, searchResultsToSources(results))
		}

		return withRoutingMeta(freshResult(jsonBytes, webSearchTTL), rt), nil, nil
	})
}

func searchCacheKey(toolName string, parts ...any) string {
	h := sha256.New()
	h.Write([]byte(toolName))
	// Version segment: bump whenever the cached response SHAPE changes so a
	// post-upgrade cache hit can never serve a blob missing a new field. v2
	// added the "trust" untrusted-content marker to every search-family output;
	// v3 added the optional zero-result "hints" object to web_search/news_search
	// (#100); v4 added sourceReputation to every web_search result (#198).
	h.Write([]byte("|v4"))
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

// auditToolDenial records a refused tool call (no_consent / not_member /
// unauthenticated) on the highest-sensitivity tools so refusals are visible in
// both the audit trail and Prometheus, with metrics PARITY against successes.
// It emits Success=false + the errCode WITHOUT a synthetic error message (the
// reason is the errCode), then records the tool-call metric. No PII: only the
// identity-from-context and the errCode are recorded, never tool input.
func auditToolDenial(ctx context.Context, deps Dependencies, toolName string, duration time.Duration, errCode string) {
	if deps.Auditor != nil {
		event := audit.NewEvent("tool_call", auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx))
		event.ToolName = toolName
		if rid := auth.RequestIDFromContext(ctx); rid != "" {
			event.RequestID = rid
		}
		event.Duration = duration.Milliseconds()
		event.Success = false
		event.ErrorCode = errCode
		event.SourceIP = auth.SourceIPFromContext(ctx)
		deps.Auditor.Log(event)
	}
	if deps.Metrics != nil {
		// errCode marks it as an error in the per-tool metrics; nil error keeps
		// the message out (the code is the reason). cacheHit=false.
		deps.Metrics.RecordToolCall(toolName, duration, errSentinel(errCode), errCode, false)
	}
}

// errSentinel wraps an errCode as a minimal error so RecordToolCall classifies
// the call as a failure in mcp_tool_calls_errors_total without leaking detail.
type errSentinel string

func (e errSentinel) Error() string { return string(e) }

// recordToolCall is a nil-safe wrapper over deps.Metrics.RecordToolCall.
// deps.Metrics is optional in this package (minimal embeddings/tests may leave
// it unset), so callers route through here to avoid a nil-pointer panic.
func recordToolCall(deps Dependencies, tool string, dur time.Duration, err error, errCode string, cacheHit bool) {
	if deps.Metrics != nil {
		deps.Metrics.RecordToolCall(tool, dur, err, errCode, cacheHit)
	}
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
		if v == nil {
			continue // skip absent optional metadata
		}
		// A typed-nil map (e.g. a nil routing block) is not == nil through an
		// interface; drop it so audit metadata never carries an empty "routing".
		if m, ok := v.(map[string]any); ok && m == nil {
			continue
		}
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

	// Recent-errors ring (#81): record a redacted sample on every error path so
	// diagnostics://errors/recent reflects live failures. The cause is redacted
	// inside the ring; kind is the errCode; provider/tier come from extra when
	// the caller supplied them. Tenant-scoped for the per-caller Resource view.
	if err != nil && deps.Metrics != nil {
		provider, _ := extra["provider"].(string)
		tier, _ := extra["tier"].(string)
		deps.Metrics.RecordError(metrics.ErrorRecord{
			Tool:     toolName,
			Kind:     errCode,
			Provider: provider,
			Tier:     tier,
			TenantID: auth.TenantIDFromContext(ctx),
			Cause:    err.Error(),
		})
	}

	// Aggregate per-tenant usage (#91) at the same chokepoint as audit/PodID.
	// Aggregate-only: counts + latency keyed by tenant_id, no query/content.
	// Anonymous (STDIO) tenant is ignored inside RecordTenantCall.
	if deps.Metrics != nil {
		provider, _ := extra["provider"].(string)
		cacheHit, _ := extra["cache_hit"].(bool)
		deps.Metrics.RecordTenantCall(auth.TenantIDFromContext(ctx), provider, duration, err != nil, cacheHit)
	}

	// Per-user analytics (#92): consent-gated profiling, distinct from the
	// aggregate tenant metrics above. Recorded ONLY when the feature is enabled
	// (non-Noop recorder) AND the user has consented to the "analytics" purpose.
	// The Noop recorder makes this a no-op when the feature is off.
	if deps.UserAnalytics != nil && deps.Consent != nil &&
		deps.Consent.HasConsent(ctx, consent.PurposeAnalytics) {
		deps.UserAnalytics.Record(ctx, auth.TenantIDFromContext(ctx), auth.UserIDFromContext(ctx), toolName)
	}
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
// that implement PatentSearcher (e.g. SearchAPI). Return contract mirrors
// resolveAcademicSearcher: (searcher, nil) on success; (nil, errorResult) for a
// known-but-unconfigured patent provider or a wholly unknown name; and
// (nil, nil) as a fall-through sentinel when the name is a valid *web* provider,
// signaling the caller to use web-search fallback instead.
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
		search.SupportedAnswerProviders,
		search.SupportedStructuredProviders,
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
