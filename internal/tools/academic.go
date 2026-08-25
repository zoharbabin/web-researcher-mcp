package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
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
	Source     string `json:"source,omitempty" jsonschema:"Restrict to an academic source. Default: all."`
	PDFOnly    bool   `json:"pdf_only,omitempty" jsonschema:"Only return papers with direct PDF links (default: false). Useful when you plan to scrape the full paper."`
	SortBy     string `json:"sort_by,omitempty" jsonschema:"Sort order (date = newest first). Default: relevance."`
	Provider   string `json:"provider,omitempty" jsonschema:"Force a specific provider (academic, or a web-search fallback). scholarapi is paid/full-text and only used when explicitly requested — never by automatic selection. Omit to use automatic selection (recommended)."`
	OpenAccess bool   `json:"open_access,omitempty" jsonschema:"Only return open-access papers with free full-text (default: false)."`
	FullText   bool   `json:"full_text,omitempty" jsonschema:"Fetch PMC full text for open-access biomedical articles with a PubMed Central ID (default: false). Only effective when the pubmed provider is active. Substantially increases response time."`
	SessionID  string `json:"sessionId,omitempty" jsonschema:"Link results to a sequential_search session. Sources are automatically recorded for recovery after context loss."`
}

func registerAcademicSearch(srv *mcp.Server, deps Dependencies) {
	inputSchema := mustSchemaFor[academicSearchInput]()
	inputSchema.Properties["source"].Enum = academicSourceEnum()
	inputSchema.Properties["sort_by"].Enum = sortByRelevanceDateEnum()
	inputSchema.Properties["provider"].Enum = academicProviderEnum()
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "academic_search",
		Description:  "Search peer-reviewed papers and scholarly literature using plain natural language — no special syntax needed. Each result includes the paper's title, authors, journal, year, abstract, citation count, and a PDF link when one is available (pair with scrape_page to read the full text). Reach for this for literature reviews, prior-art research, and finding citations; use web_search for non-academic content or news_search for current events. Results can be narrowed by year, source, or access type. Returns structured JSON, with recovery hints when nothing matches. Results stay fresh for 1 hour.",
		Annotations:  readOnlyAnnotations(true, true),
		InputSchema:  inputSchema,
		OutputSchema: academicSearchOutputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input academicSearchInput) (*mcp.CallToolResult, any, error) {
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
		if numResults > 10 {
			numResults = 10
		}
		source := input.Source
		if source == "" {
			source = "all"
		}

		cacheKey := searchCacheKey("academic", input.Query, numResults, input.YearFrom, input.YearTo, source, input.Provider, input.OpenAccess, input.PDFOnly, input.FullText)
		if cached, meta, ok := deps.Cache.GetWithMeta(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("academic_search", time.Since(start), nil, "", true)
			rt := routingMeta(search.RoutingDecision{}, time.Since(start), true)
			auditToolCallQuery(ctx, deps, "academic_search", time.Since(start), nil, "", "", map[string]any{"cache_hit": true, "routing": rt})
			return withRoutingMeta(cachedResultWithMeta(cached, meta), rt), nil, nil
		}

		// Routing trace for the Router-routed academic path (Strategy 2). Only that
		// path goes through the Router's fallback ladder; the pinned-provider and
		// direct-provider strategies name their provider in the body's `source`
		// field and have no ladder to observe (#58 scope).
		var routeDecision search.RoutingDecision

		// Precision improvements (#209):
		// (1) Exact-phrase quoting for named-entity / acronym queries — a short
		//     all-caps token or a hyphenated trial acronym (e.g. "ZUMA-7") is quoted
		//     so providers do exact-title matching instead of loose term-matching.
		// (2) Hybrid sort for date+query — when the caller sets sort_by=date with a
		//     non-empty query we drop the date sort at the API level (relevance wins)
		//     and let the year_from/year_to filters narrow the date window instead.
		//     Pure-recency sort discards topical relevance, returning tangentially-
		//     related recent papers that merely mention the query terms.
		preparedQuery := prepareAcademicQuery(input.Query)
		sortBy := input.SortBy
		if sortBy == "date" && strings.TrimSpace(input.Query) != "" {
			sortBy = "" // relevance + year filter; not pure-recency
		}

		searchParams := search.AcademicSearchParams{
			Query:      preparedQuery,
			YearFrom:   input.YearFrom,
			YearTo:     input.YearTo,
			Source:     source,
			NumResults: numResults,
			OpenAccess: input.OpenAccess,
			SortBy:     sortBy,
			FullText:   input.FullText,
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
					trackOutcome(ctx, deps, input.SessionID, input.Provider, false, errCode, "")
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
				traceCtx, trace := search.NewRoutingTrace(ctx)
				apiResults, err := as.Scholarly(traceCtx, searchParams)
				if err == nil && len(apiResults) > 0 {
					results = apiResults
					providerSource = "router"
					routeDecision = trace.Decision()
				}
			}
		}

		// Strategy 3: Try academic providers directly (non-router mode,
		// deterministic order). A rate-limited (or otherwise failing)
		// provider is skipped, not treated as exhausting the whole ladder
		// (#503, #697) — isRateLimitError now also matches
		// circuit.ErrCircuitOpen (#664), so a mid-ladder provider with an
		// open breaker must not abort the search for every healthy provider
		// listed after it.
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

		// Quality gate (#229): drop Crossref test/placeholder records before they
		// reach the caller. The 10.5555 prefix is Crossref's reserved test prefix —
		// a nonsense query ("asdkjfh qwerty …") otherwise returns these as if real
		// ("more testing qwerty", doi:10.5555/…). Filtering them lets the empty-result
		// hints path fire instead of passing noise through. Runs before enrichment so
		// we never spend Unpaywall/retraction calls on junk.
		results = filterPlaceholderResults(results)

		// Open-access enrichment (#45): fill pdfUrl/openAccess on DOI-bearing
		// results the provider left bare, via Unpaywall. Best-effort + no-op when
		// unconfigured; runs BEFORE the pdf_only filter so resolved PDFs count.
		results = search.EnrichOpenAccess(ctx, deps.OAResolver, results)

		// Integrity enrichment (#156): flag retracted/corrected DOIs via Crossref's
		// merged Retraction Watch + publisher data, so a search never presents a
		// withdrawn paper as sound. Best-effort + no-op when unconfigured.
		results = search.EnrichRetraction(ctx, deps.RetractionResolver, results)

		// Low-confidence-domain signal (#509): defense-in-depth against upstream
		// index noise (a spam/mirror record title-matching a well-known paper) —
		// the underlying index quality issue is out of this repo's control, so
		// this only flags, never drops, a suspicious result. Runs after
		// placeholder-filtering and OA/retraction enrichment (needs the final
		// PDFUrl/CitationCount) and before the pdf_only filter/map rendering.
		results = flagLowConfidenceDomains(results)

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
			papers = append(papers, academicResultToMap(r))
		}

		output := map[string]any{
			"papers":       papers,
			"query":        input.Query,
			"totalResults": len(papers),
			"resultCount":  len(papers),
			"source":       providerSource,
			"trust":        untrustedContentTrust,
		}

		if len(papers) == 0 {
			output["hints"] = buildAcademicHints(input, providerSource)
		}

		jsonBytes, _ := json.Marshal(output)
		if len(papers) > 0 {
			deps.Cache.Set(ctx, cacheKey, jsonBytes, 1*time.Hour)
		}
		rt := routingMeta(routeDecision, time.Since(start), false)
		deps.Metrics.RecordToolCall("academic_search", time.Since(start), nil, "", false)
		auditToolCallQuery(ctx, deps, "academic_search", time.Since(start), nil, "", "", map[string]any{"routing": rt})

		if input.SessionID != "" {
			trackSources(ctx, deps, input.SessionID, academicResultsToSources(results))
			trackOutcome(ctx, deps, input.SessionID, providerSource, len(papers) > 0, "", "")
		}

		return withRoutingMeta(structuredResult(jsonBytes), rt), nil, nil
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
// Return contract: (searcher, nil) on success; (nil, errorResult) when a known
// academic provider is unconfigured; and (nil, nil) as a fall-through sentinel
// meaning "this is not an academic-specific provider — use the web-search
// fallback" (so the caller routes the query to a web provider instead).
func resolveAcademicSearcher(deps Dependencies, providerName string) (search.AcademicSearcher, *mcp.CallToolResult) {
	if providerName == "" {
		if as, ok := deps.Search.(search.AcademicSearcher); ok {
			return as, nil
		}
		return nil, nil
	}

	// isKnownAcademicName covers both auto-routed names (SupportedAcademicProviders)
	// and explicit-only names (academicProvidersExplicitOnly, e.g. "scholarapi",
	// #266) — the latter are still a real, resolvable academic provider when
	// requested by name; they are only excluded from *automatic* fallback.
	if isKnownAcademicName(providerName) {
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
		return nil, structuredError(
			fmt.Sprintf("Academic provider %q is not configured. %s", providerName, envHint),
			ToolError{
				Kind:            ErrKindConfig,
				Retryable:       false,
				SuggestedAction: ActionCheckAPIKey,
				Provider:        providerName,
			})
	}

	// Not an academic-specific provider — it might be a web search provider for fallback
	// Return nil so caller falls through to web search fallback
	return nil, nil
}

// isKnownAcademicName reports whether providerName names an academic provider
// this server knows how to construct — either auto-routed or explicit-only
// (#266) — as opposed to a web-search provider name, which resolveAcademicSearcher
// must fall through on instead of returning a "not configured" error for.
func isKnownAcademicName(providerName string) bool {
	for _, name := range search.SupportedAcademicProviders {
		if name == providerName {
			return true
		}
	}
	for _, name := range search.AcademicProvidersExplicitOnly {
		if name == providerName {
			return true
		}
	}
	return false
}

func academicProviderEnvHint(name string) string {
	switch name {
	case "openalex":
		return "Set OPENALEX_EMAIL to your contact email."
	case "crossref":
		return "Set CROSSREF_EMAIL to your contact email."
	case "semanticscholar":
		return "Semantic Scholar works without a key at a lower shared rate; set SEMANTIC_SCHOLAR_API_KEY to raise the limit."
	case "core":
		return "CORE works without a key at a lower shared rate; set CORE_API_KEY to raise the limit."
	case "exa":
		return "Set EXA_API_KEY to your Exa API key."
	case "scholarapi":
		return "Set SCHOLAR_API_KEY to your ScholarAPI key (scholarapi.net). Paid, metered — not included in automatic routing."
	default:
		return ""
	}
}

// academicResultToMap renders an AcademicResult as the tool-output JSON object,
// omitting empty fields (matching academicSearchOutputSchema). Shared by
// academic_search and citation_graph so both surfaces stay consistent. `tldr` is
// attributed as AI-generated in the schema/docs, not here.
func academicResultToMap(r search.AcademicResult) map[string]any {
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
	if r.TLDR != "" {
		paper["tldr"] = r.TLDR
	}
	if r.IsInfluential {
		paper["isInfluential"] = true
	}
	if len(r.CitationIntents) > 0 {
		paper["citationIntents"] = r.CitationIntents
	}
	if r.Retraction != nil {
		paper["retractionStatus"] = r.Retraction
	}
	if r.IsInDoaj {
		paper["isInDoaj"] = true
	}
	if r.FullText != "" {
		paper["fullText"] = r.FullText
	}
	if r.HasText {
		paper["hasText"] = true
	}
	if r.HasPDF {
		paper["hasPdf"] = true
	}
	if r.LowConfidenceDomain {
		paper["lowConfidenceDomain"] = true
	}
	return paper
}

// filterPlaceholderResults removes Crossref test/placeholder records (#229).
// Crossref reserves the 10.5555 DOI prefix for test deposits; real research is
// never published under it. Such entries surface only when a query has no genuine
// academic signal, so dropping them lets the zero-result hints path engage instead
// of presenting noise as findings. Conservative by design — it keys only on the
// reserved test prefix, never on score, so a legitimate result is never discarded.
func filterPlaceholderResults(in []search.AcademicResult) []search.AcademicResult {
	out := in[:0:0]
	for _, r := range in {
		if isPlaceholderDOI(r.DOI) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// isPlaceholderDOI reports whether a DOI is in Crossref's reserved test prefix
// (10.5555), normalizing any doi.org/ prefix first.
func isPlaceholderDOI(doi string) bool {
	d := strings.TrimSpace(strings.ToLower(doi))
	d = strings.TrimPrefix(d, "https://doi.org/")
	d = strings.TrimPrefix(d, "http://doi.org/")
	d = strings.TrimPrefix(d, "doi:")
	return strings.HasPrefix(d, "10.5555/")
}

// lowConfidenceMinPeerCitations and lowConfidenceCitationRatio bound the
// "anomalously low citation count" half of the #509 heuristic below. A
// same-titled peer must itself be reasonably well-cited (at least this many
// citations) before its count becomes a meaningful baseline — two obscure,
// lightly-cited papers that happen to share a title are not evidence of
// anything. Once that baseline exists, a candidate citing less than 1/10th of
// it is the "implausibly low for a well-known-sounding title" signal the
// issue describes.
const (
	lowConfidenceMinPeerCitations = 50
	lowConfidenceCitationRatio    = 10
)

// flagLowConfidenceDomains implements the #509 defense-in-depth signal: OpenAlex
// (and academic indexes generally) occasionally surface a spam/mirror record
// that title-matches a famous paper — hosted on an unrecognized domain, with an
// implausible citation count, sometimes ranked ahead of or alongside the real
// record. That upstream index-quality problem is out of this repo's control
// (see #509), but within one response we can compare same-titled results
// against each other: when a result's hosting domain is not a recognized
// publisher/preprint-server (content.IsAcademicHost) AND another result in the
// same response shares its (normalized) title with a citation count at least
// lowConfidenceCitationRatio times higher, the lower-cited result is flagged
// LowConfidenceDomain. This never drops or reorders results — it only adds a
// signal the model can use to hedge or prefer the higher-confidence peer.
// Conservative by design: a title with no well-cited peer in the same
// response is left unflagged, since there is nothing to compare against.
func flagLowConfidenceDomains(results []search.AcademicResult) []search.AcademicResult {
	if len(results) < 2 {
		return results
	}

	maxCitationsByTitle := make(map[string]int, len(results))
	for _, r := range results {
		key := normalizeAcademicTitle(r.Title)
		if key == "" {
			continue
		}
		if r.CitationCount > maxCitationsByTitle[key] {
			maxCitationsByTitle[key] = r.CitationCount
		}
	}

	for i := range results {
		r := &results[i]
		key := normalizeAcademicTitle(r.Title)
		if key == "" {
			continue
		}
		peerMax := maxCitationsByTitle[key]
		if peerMax < lowConfidenceMinPeerCitations {
			continue // no well-cited peer for this title to compare against
		}
		if r.CitationCount >= peerMax {
			continue // this IS the well-cited peer (or ties it) — never self-flag
		}
		if r.CitationCount*lowConfidenceCitationRatio >= peerMax {
			continue // within the tolerated ratio of the peer — not anomalous
		}
		if content.IsAcademicHost(hostingHostForResult(r)) {
			continue // recognized publisher/preprint host — never flagged
		}
		r.LowConfidenceDomain = true
	}
	return results
}

// hostingHostForResult returns the host of the location the paper is actually
// hosted at, for the domain-recognition check above. PDFUrl (the provider's
// open-access "oa_url") is preferred over URL because URL is frequently a
// doi.org resolver link — identical in host for a genuine paper and a
// DOI-bearing spam/mirror record alike, so it carries no discriminating signal
// on its own. PDFUrl/oa_url is where the content is actually served from, which
// is exactly the "hosted on a low-quality domain" signal #509 describes. Falls
// back to URL only when no PDFUrl is present.
func hostingHostForResult(r *search.AcademicResult) string {
	if r.PDFUrl != "" {
		return hostForURL(r.PDFUrl)
	}
	return hostForURL(r.URL)
}

// normalizeAcademicTitle lowercases a paper title and strips everything but
// letters/digits/spaces, collapsing whitespace, so titles that differ only by
// punctuation, casing, or an extra space still group together (e.g. "Attention
// Is All You Need" vs "Attention is all you need."). Returns "" for an empty
// or all-punctuation title so such results never join a false comparison group.
func normalizeAcademicTitle(title string) string {
	var b strings.Builder
	lastWasSpace := true // suppress leading spaces
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastWasSpace = false
		case r == ' ' || r == '\t' || r == '\n':
			if !lastWasSpace {
				b.WriteRune(' ')
				lastWasSpace = true
			}
		default:
			// punctuation/symbols dropped entirely, not treated as a separator
		}
	}
	return strings.TrimSpace(b.String())
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
	if input.FullText {
		filters["full_text"] = "true"
	}

	hints := buildZeroResultHints(provider, filters, nil)

	if input.FullText && provider != "pubmed" {
		hints.SuggestedActions = append([]HintAction{{
			Action:    "remove_filter",
			Parameter: "full_text",
			Detail:    "full_text is only fetched by the pubmed provider; it has no effect with other providers.",
		}}, hints.SuggestedActions...)
	}

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

// prepareAcademicQuery wraps the query in double quotes when it looks like a
// named-entity or acronym that should be searched as an exact phrase:
//   - A single token that is all-uppercase (e.g. "CRISPR", "ZUMA-7", "AlphaFold")
//   - A multi-token phrase containing an all-uppercase or hyphenated token (e.g.
//     "ZUMA-7 CAR-T trial") — the whole phrase is quoted for exact-phrase search
//
// Queries already containing quotes, boolean operators (AND/OR/NOT), or field
// prefixes are left untouched — the caller has expressed intent via syntax.
// The original query is returned unchanged for plain-language descriptive queries
// (all lowercase, no special tokens) so normal relevance-ranking works as usual.
func prepareAcademicQuery(query string) string {
	q := strings.TrimSpace(query)
	if q == "" {
		return query
	}
	// Leave already-quoted or operator-syntax queries alone.
	if strings.ContainsAny(q, `"`) || strings.Contains(q, " AND ") ||
		strings.Contains(q, " OR ") || strings.Contains(q, " NOT ") {
		return query
	}
	tokens := strings.Fields(q)
	for _, tok := range tokens {
		// Strip common punctuation for the check (hyphens kept: "ZUMA-7" stays "ZUMA-7").
		clean := strings.Trim(tok, ".,;:?!()")
		if clean == "" {
			continue
		}
		// All-uppercase token (length ≥ 2 to exclude single-char operators).
		if len(clean) >= 2 && clean == strings.ToUpper(clean) && strings.ContainsAny(clean, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			return `"` + q + `"`
		}
		// Hyphenated acronym-style token (e.g. "ZUMA-7", "CAR-T").
		if strings.Contains(clean, "-") {
			parts := strings.Split(clean, "-")
			allCaps := true
			for _, p := range parts {
				if p != "" && p != strings.ToUpper(p) {
					allCaps = false
					break
				}
			}
			if allCaps && len(parts) >= 2 {
				return `"` + q + `"`
			}
		}
	}
	return query
}
