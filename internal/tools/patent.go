package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// barePatentNumberRegex recognizes a query that is itself a patent number
// (e.g. "US10000000", "US10000000B2", "EP1234567A1") rather than a
// description or keyword phrase. Reuses the same office-prefix + digit shape
// as scraper.ExtractPatentNumberFromURL's start-of-string alternative, but
// requires the WHOLE trimmed query to match (case-insensitive) so a query
// like "US10000000 improvements" — a keyword search that happens to mention a
// number — is not misidentified as an exact lookup.
var barePatentNumberRegex = regexp.MustCompile(`(?i)^[A-Z]{2}\d[\dA-Z]*$`)

// landscapeOverfetchCount is how many candidates search_type=landscape
// requests from providers/web-discovery before clustering by assignee and
// truncating back to the caller's numResults (#529) — 25 is the highest
// NumResults any provider actually honors (see EPO's clamp(..., 1, 25) in
// internal/search/epo.go), so requesting more would be wasted API cost.
const landscapeOverfetchCount = 25

// looksLikePatentNumber reports whether query is shaped like a bare patent
// number rather than a free-text search phrase.
func looksLikePatentNumber(query string) bool {
	return barePatentNumberRegex.MatchString(strings.TrimSpace(query))
}

// isPatentSpecificLookup reports whether the #502 direct-lookup short-circuit
// applies: no explicit provider was pinned (that path — Strategy 1 — already
// owns the request), the caller asked for an exact lookup, and the query is
// itself shaped like a patent number rather than free text.
func isPatentSpecificLookup(provider, searchType, query string) bool {
	return provider == "" && searchType == "specific" && looksLikePatentNumber(query)
}

// patentDetailScraper is the narrow interface for fetching a single patent's
// detail page — satisfied by *scraper.Pipeline. Declared here (not only in
// tests) so the #502 short-circuit's fetch logic can be exercised with a fake
// in unit tests, mirroring the patentDetailScraper/enrichPatentsWithScraper
// pattern already used for enrichPatents in patent_enrich_test.go.
type patentDetailScraper interface {
	ScrapePatentDetail(ctx context.Context, number string) (*scraper.PatentResult, error)
}

// lookupPatentByNumber performs the #502 specific-lookup short-circuit: fetch
// exactly one patent by its number from its detail page. A fetch error, a nil
// detail, or a detail with no title (all shapes of "not found") are treated
// as a miss — returning nil — rather than an error, since the caller falls
// through to zero-result hints rather than another search strategy.
func lookupPatentByNumber(ctx context.Context, number string, ps patentDetailScraper) *scraper.PatentResult {
	detail, err := ps.ScrapePatentDetail(ctx, number)
	if err != nil || detail == nil || detail.Title == "" {
		return nil
	}
	return detail
}

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
	Provider     string `json:"provider,omitempty" jsonschema:"Force a specific patent provider: searchapi, epo, lens, uspto (patent-specific), or google, brave, serper, searxng, duckduckgo, tavily, exa (web search fallback). Omit for automatic selection based on configured providers and region."`
	SessionID    string `json:"sessionId,omitempty" jsonschema:"Link results to a sequential_search session. Sources are automatically recorded for recovery after context loss."`
}

func registerPatentSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "patent_search",
		Description:  "Search patents for prior art, competitive landscape mapping, or to look up a specific patent. Query by patent number (e.g. 'US11234567'), an invention description, a company, or an inventor — company name variations are matched automatically. Each result carries the patent's bibliographic details (title, number, abstract, assignee, inventor, dates, status). Reach for this when the question is about inventions or IP; use academic_search for research papers or web_search for general technical content. Zero-result and error responses come back as structured JSON with recovery hints. Results stay fresh for 24 hours.",
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
			rt := routingMeta(search.RoutingDecision{}, time.Since(start), true)
			auditToolCallQuery(ctx, deps, "patent_search", time.Since(start), nil, "", "", map[string]any{"cache_hit": true, "routing": rt})
			return withRoutingMeta(cachedResultWithMeta(cached, meta), rt), nil, nil
		}

		// Routing trace for the Router-routed patent paths (Strategy 2 = Router's
		// Patents ladder; Strategy 4 = web-discovery fallback through the Router).
		// The pinned-provider (Strategy 1) and direct patent-only (Strategy 3)
		// paths name their provider in the body's `source` and have no ladder.
		var routeDecision search.RoutingDecision

		// isLandscape (#529): search_type=landscape is documented as a
		// "competitive overview" distinct from prior_art's plain broad
		// search. It over-fetches a wider candidate pool (up to
		// landscapeOverfetchCount, the max any provider honors) and, once a
		// strategy below has resolved `patents`, clusters that pool by
		// assignee before truncating back down to numResults — so the same
		// query returns a genuinely different composition/ordering than
		// prior_art, not a byte-identical response.
		isLandscape := searchType == "landscape"
		requestCount := numResults
		if isLandscape {
			requestCount = landscapeOverfetchCount
		}

		searchParams := search.PatentSearchParams{
			Query:        input.Query,
			Assignee:     normalizeAssignee(input.Assignee),
			Inventor:     input.Inventor,
			CPCCode:      input.CPCCode,
			PatentOffice: input.PatentOffice,
			YearFrom:     input.YearFrom,
			YearTo:       input.YearTo,
			NumResults:   requestCount,
		}

		var patents []scraper.PatentResult

		var source string

		// webProviderRequested + pinnedWebProvider: set below when input.Provider
		// names a web-search-fallback provider (google, brave, etc.) rather than a
		// dedicated patent provider (#527) — routes exclusively through Strategy
		// 4's web-discovery using that named provider, so Strategies 2/3 (which
		// pick whichever dedicated patent provider answers first, ignoring
		// input.Provider entirely) are skipped rather than silently taking over.
		var webProviderRequested bool
		var pinnedWebProvider search.Provider

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
			} else {
				// ps == nil, errResult == nil: resolvePatentSearcher's fall-through
				// sentinel — input.Provider names a valid *web* search provider, not
				// a dedicated patent provider. Pin it now for Strategy 4 below.
				webProvider, errResult := resolveProvider(deps, input.Provider)
				if errResult != nil {
					return errResult, nil, nil
				}
				webProviderRequested = true
				pinnedWebProvider = webProvider
			}
		}

		// Specific-lookup short-circuit (#502): when the caller asked for an
		// exact patent lookup (search_type=specific) and the query is itself a
		// bare patent number (e.g. "US10000000"), fetch that one patent
		// directly from its detail page instead of running the broad-text-
		// search strategies below. Those strategies treat the query as free
		// text — providers can return tangentially related matches, and
		// Strategy 4's web-discovery fallback pads out to numResults with
		// whatever else the search turns up, producing unrelated-looking
		// results #2-5 alongside the correct #1. A direct-by-number lookup has
		// no such padding: it returns exactly the requested patent, or
		// nothing — so the broader strategies are skipped entirely rather than
		// used as a fallback on a miss.
		isSpecificNumberLookup := isPatentSpecificLookup(input.Provider, searchType, input.Query)
		if isSpecificNumberLookup {
			number := strings.ToUpper(strings.TrimSpace(input.Query))
			if detail := lookupPatentByNumber(ctx, number, deps.Scraper); detail != nil {
				patents = []scraper.PatentResult{*detail}
				source = "google_patents_direct"
			}
		}

		// Strategy 2: Try the main provider (Router implements PatentSearcher).
		// Skipped when a web-search-fallback provider was explicitly pinned
		// (#527) — Strategies 2/3 pick whichever dedicated patent provider
		// answers first, ignoring input.Provider entirely, which is exactly the
		// silent substitution this issue fixes.
		if len(patents) == 0 && source == "" && !isSpecificNumberLookup && !webProviderRequested {
			if ps, ok := deps.Search.(search.PatentSearcher); ok {
				traceCtx, trace := search.NewRoutingTrace(ctx)
				apiResults, err := ps.Patents(traceCtx, searchParams)
				if err == nil && len(apiResults) > 0 {
					patents = convertPatentResults(apiResults)
					source = deps.Search.Name()
					routeDecision = trace.Decision()
				}
			}
		}

		// Strategy 3: Try patent-only providers directly (non-router mode).
		// Iterated in the deterministic search.SupportedPatentProviders order
		// (not Go's randomized map order) so behavior doesn't vary call to
		// call, and a rate-limited provider is skipped rather than aborting
		// the whole ladder (#503) — Go map iteration order is random, so with
		// the old `break`-on-rate-limit a rate-limited provider visited early
		// would silently cut off healthy providers later in the same call.
		if len(patents) == 0 && source == "" && !isSpecificNumberLookup && !webProviderRequested {
			for _, name := range search.SupportedPatentProviders {
				pp, ok := deps.PatentProviders[name]
				if !ok {
					continue
				}
				if !pp.Metadata().MatchesRegion(input.PatentOffice) {
					continue
				}
				apiResults, err := pp.Patents(ctx, searchParams)
				if err == nil && len(apiResults) > 0 {
					patents = convertPatentResults(apiResults)
					source = name
					break
				}
				// A rate-limited (or otherwise failing) provider is skipped, not
				// treated as exhausting the whole ladder — the next provider in
				// SupportedPatentProviders order still gets a chance (#503).
			}
		}

		// Strategy 4: Fallback — discover via web search + enrich from detail
		// pages. When a web-search-fallback provider was explicitly pinned
		// (#527), use that exact provider instead of the auto-selected default —
		// this is the ONLY strategy a pinned web provider runs through, so its
		// choice is actually honored end to end, not overridden by whichever
		// dedicated patent provider Strategies 2/3 would otherwise pick.
		if len(patents) == 0 && source == "" && !isSpecificNumberLookup {
			provider := pinnedWebProvider
			if !webProviderRequested {
				var errResult *mcp.CallToolResult
				provider, errResult = resolveProvider(deps, "")
				if errResult != nil {
					return errResult, nil, nil
				}
			}

			searchQuery := buildPatentDiscoveryQuery(input)
			traceCtx, trace := search.NewRoutingTrace(ctx)
			webResults, err := provider.Web(traceCtx, search.WebSearchParams{
				Query:      searchQuery,
				NumResults: requestCount + 5,
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
				if len(patentNumbers) >= requestCount {
					break
				}
			}

			patents = enrichPatents(ctx, deps.Scraper, patentNumbers)
			if len(patents) > 0 {
				if webProviderRequested {
					// Report the caller's own pinned provider name, not the
					// generic "web_discovery" label, so the response makes clear
					// their explicit choice was honored (#527).
					source = input.Provider
				} else {
					source = "web_discovery"
				}
				routeDecision = trace.Decision()
			}
		}

		// search_type=landscape (#529): cluster the over-fetched candidate
		// pool by assignee — most-represented assignee first — then truncate
		// back down to the caller's numResults, keeping each cluster's
		// patents contiguous. This is what makes landscape's output
		// genuinely differ from prior_art's plain relevance order for the
		// identical query, rather than being an alias that only changes the
		// echoed searchType string.
		var assigneeClusters []map[string]any
		if isLandscape && len(patents) > 0 {
			patents, assigneeClusters = clusterPatentsByAssignee(patents, numResults)
		} else if len(patents) > numResults {
			patents = patents[:numResults]
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
			"trust":       untrustedContentTrust,
		}

		if len(patents) == 0 {
			output["hints"] = buildPatentHints(input, source, deps)
		}
		if len(assigneeClusters) > 0 {
			output["assigneeClusters"] = assigneeClusters
		}

		jsonBytes, _ := json.Marshal(output)
		if len(patents) > 0 {
			deps.Cache.Set(ctx, cacheKey, jsonBytes, 24*time.Hour)
		}
		rt := routingMeta(routeDecision, time.Since(start), false)
		deps.Metrics.RecordToolCall("patent_search", time.Since(start), nil, "", false)
		auditToolCallQuery(ctx, deps, "patent_search", time.Since(start), nil, "", "", map[string]any{"routing": rt})

		if input.SessionID != "" {
			trackSources(ctx, deps, input.SessionID, patentResultsToSources(patents))
		}

		return withRoutingMeta(structuredResult(jsonBytes), rt), nil, nil
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
	if input.CPCCode != "" {
		// Google Patents' own query syntax recognizes CPC=<code> as a
		// classification filter, so the web-discovery fallback (Strategy 4)
		// honors cpc_code the same as the dedicated patent providers (#530).
		parts = append(parts, "CPC="+input.CPCCode)
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

	// Cap the total enrichment time across all concurrent fetches.  Each
	// ScrapePatentDetail already sets a 15 s per-fetch timeout; without an
	// aggregate deadline a slow first fetch holds the slot and the 3-at-a-time
	// semaphore stalls all remaining numbers, turning patent_search from ~5-10 s
	// into 40-60 s when patents.google.com is slow.  25 s is generous for 3
	// concurrent 15 s fetches and still recovers gracefully via the fallback.
	enrichCtx, enrichCancel := context.WithTimeout(ctx, 25*time.Second)
	defer enrichCancel()

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

			detail, err := pipeline.ScrapePatentDetail(enrichCtx, num)
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

// clusterPatentsByAssignee groups patents by their (normalized) assignee,
// orders clusters by size descending (most prolific assignee first, ties
// broken by each cluster's first-seen order in the input to stay
// deterministic), then flattens back into a patents slice truncated to limit
// — keeping each cluster's own patents contiguous. Patents with no assignee
// form their own single-member clusters and sort by their original count (1)
// same as any other singleton, so they naturally fall after any multi-patent
// assignee cluster. Returns the reordered/truncated patents plus a summary
// of the clusters actually included in the truncated output (#529).
func clusterPatentsByAssignee(patents []scraper.PatentResult, limit int) ([]scraper.PatentResult, []map[string]any) {
	type cluster struct {
		key     string
		patents []scraper.PatentResult
	}
	order := make([]string, 0)
	byKey := make(map[string]*cluster)
	for _, p := range patents {
		key := normalizeAssignee(p.Assignee)
		if key == "" {
			key = p.Number
		}
		c, ok := byKey[key]
		if !ok {
			c = &cluster{key: key}
			byKey[key] = c
			order = append(order, key)
		}
		c.patents = append(c.patents, p)
	}

	clusters := make([]*cluster, 0, len(order))
	for _, key := range order {
		clusters = append(clusters, byKey[key])
	}
	sort.SliceStable(clusters, func(i, j int) bool {
		return len(clusters[i].patents) > len(clusters[j].patents)
	})

	result := make([]scraper.PatentResult, 0, limit)
	summary := make([]map[string]any, 0, len(clusters))
	for _, c := range clusters {
		if len(result) >= limit {
			break
		}
		assignee := c.patents[0].Assignee
		if assignee == "" {
			continue
		}
		summary = append(summary, map[string]any{"assignee": assignee, "count": len(c.patents)})
		for _, p := range c.patents {
			if len(result) >= limit {
				break
			}
			result = append(result, p)
		}
	}
	// Backfill with any un-assigneed patents (their own singleton clusters,
	// skipped above) if there's still room under limit.
	if len(result) < limit {
		for _, c := range clusters {
			if len(result) >= limit {
				break
			}
			if c.patents[0].Assignee != "" {
				continue
			}
			for _, p := range c.patents {
				if len(result) >= limit {
					break
				}
				result = append(result, p)
			}
		}
	}
	return result, summary
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

func buildPatentHints(input patentSearchInput, source string, deps Dependencies) *ZeroResultHints {
	filters := map[string]string{}
	if input.PatentOffice != "" && input.PatentOffice != "all" {
		filters["patent_office"] = input.PatentOffice
	}
	if input.YearFrom > 0 {
		filters["year_from"] = fmt.Sprintf("%d", input.YearFrom)
	}
	if input.YearTo > 0 {
		filters["year_to"] = fmt.Sprintf("%d", input.YearTo)
	}

	var alts []string
	if input.Provider != "" {
		for name := range deps.PatentProviders {
			if name != input.Provider {
				alts = append(alts, name)
			}
		}
	}

	hints := buildZeroResultHints(source, filters, alts)

	if input.Provider == "uspto" && input.PatentOffice != "" && input.PatentOffice != "US" {
		hints.Reason = "coverage_miss"
		hints.SuggestedActions = []HintAction{{
			Action: "switch_provider",
			Value:  "lens",
			Detail: "USPTO only covers US patents. Use lens or epo for worldwide coverage",
		}}
	}

	return hints
}
