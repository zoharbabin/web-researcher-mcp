package tools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// manyImageFiltersWarning returns an advisory for issue #659: Google's
// Custom Search JSON API's own request construction here is correct — each
// of imgSize/imgType/imgColorType/imgDominantColor/fileType is set
// independently under its documented param name (see doImageSearch in
// internal/search/google.go), so there is no mismapping or overwrite in this
// codebase's query. The actual root cause is upstream: when 3+ of these
// filters combine into an intersection narrow enough that too few images
// satisfy every constraint, Google's server silently relaxes the combined
// filter set and falls back toward broader/default ranking — the response
// carries no field indicating anything was relaxed, so results can come back
// byte-identical to an unfiltered query with no error and no signal. This
// cannot be detected or corrected client-side, so the fix is an honest
// warning rather than a request-construction change. Fires only for the
// google provider, only once 3 or more filters are combined — the threshold
// at which live testing showed the relaxation kick in.
func manyImageFiltersWarning(params search.ImageSearchParams, providerUsed string) string {
	if providerUsed != "google" {
		return ""
	}
	set := 0
	for _, v := range []string{params.Size, params.Type, params.ColorType, params.DominantColor, params.FileType} {
		if v != "" {
			set++
		}
	}
	if set < 3 {
		return ""
	}
	return "3 or more image filters were combined (size/type/color_type/dominant_color/file_type). Google's Custom Search API may silently relax combined filters when too few images satisfy every constraint at once, returning broader results than requested with no field in the response indicating this happened. If results look unfiltered, try fewer simultaneous filters or a different provider."
}

type imageSearchInput struct {
	Query         string `json:"query" jsonschema:"Descriptive search query for images (e.g. 'golden retriever puppy playing fetch'). More descriptive = better results.,required"`
	NumResults    int    `json:"num_results,omitempty" jsonschema:"Number of image results (1-200, default: 5). Brave returns up to 200; Google up to 10."`
	Size          string `json:"size,omitempty" jsonschema:"Filter by image size. Google/SearchAPI only — Brave ignores it."`
	Type          string `json:"type,omitempty" jsonschema:"Filter by image type. Google/SearchAPI only — Brave ignores it."`
	ColorType     string `json:"color_type,omitempty" jsonschema:"Filter by color mode (trans = transparent background). Google/SearchAPI only — Brave ignores it."`
	DominantColor string `json:"dominant_color,omitempty" jsonschema:"Filter by dominant color. Google/SearchAPI only — Brave ignores it."`
	FileType      string `json:"file_type,omitempty" jsonschema:"Filter by file format. Google/SearchAPI only — Brave ignores it."`
	Safe          string `json:"safe,omitempty" jsonschema:"SafeSearch level. Default: medium. On Brave images only off and strict apply (any non-off maps to strict)."`
	Country       string `json:"country,omitempty" jsonschema:"Country to localize results to, ISO 3166-1 alpha-2 (e.g. 'us', 'gb'). Honored by Brave and Google."`
	Language      string `json:"language,omitempty" jsonschema:"Language to scope results to, BCP 47 / 2-letter code (e.g. 'en', 'de'). Honored by Brave (search_lang) and Google (lr)."`
	Provider      string `json:"provider,omitempty" jsonschema:"Force a specific search provider. Omit to use configured default."`
}

func registerImageSearch(srv *mcp.Server, deps Dependencies) {
	inputSchema := mustSchemaFor[imageSearchInput]()
	inputSchema.Properties["size"].Enum = imageSizeEnum()
	inputSchema.Properties["type"].Enum = imageTypeEnum()
	inputSchema.Properties["color_type"].Enum = imageColorTypeEnum()
	inputSchema.Properties["dominant_color"].Enum = imageDominantColorEnum()
	inputSchema.Properties["file_type"].Enum = imageFileTypeEnum()
	inputSchema.Properties["safe"].Enum = webSafeEnum()
	inputSchema.Properties["provider"].Enum = webProviderEnum()
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "image_search",
		Description:  "Find images on the web matching your description. Filter by size, type (photo, clipart, line art, etc.), dominant color, or file format (Google/SearchAPI), and localize by country/language. Returns up to 200 image links per search on Brave (up to 10 on Google). Best for finding visual references or assets — use web_search if you need text content from pages that contain images. Results stay fresh for 30 minutes.",
		Annotations:  readOnlyAnnotations(true, true),
		InputSchema:  inputSchema,
		OutputSchema: imageSearchOutputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input imageSearchInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		input.Query = strings.TrimSpace(input.Query)
		if input.Query == "" {
			return toolError("query is required"), nil, nil
		}

		numResults := input.NumResults
		if numResults > maxImageResults {
			numResults = maxImageResults
		}
		if numResults <= 0 {
			numResults = 5
		}

		// Include provider + safe + locale so different providers / safe-levels /
		// regions never collide on the same query (idempotency + consistency).
		cacheKey := searchCacheKey("image", input.Query, numResults, input.Size, input.Type, input.ColorType, input.DominantColor, input.FileType, input.Safe, input.Country, input.Language, input.Provider)
		if cached, meta, ok := deps.Cache.GetWithMeta(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("image_search", time.Since(start), nil, "", true)
			rt := routingMeta(search.RoutingDecision{}, time.Since(start), true)
			auditToolCallQuery(ctx, deps, "image_search", time.Since(start), nil, "", "", map[string]any{"cache_hit": true, "routing": rt})
			return withRoutingMeta(cachedResultWithMeta(cached, meta), rt), nil, nil
		}

		provider, errResult := resolveProvider(deps, input.Provider)
		if errResult != nil {
			return errResult, nil, nil
		}

		imgParams := search.ImageSearchParams{
			Query:         input.Query,
			NumResults:    numResults,
			Size:          input.Size,
			Type:          input.Type,
			ColorType:     input.ColorType,
			DominantColor: input.DominantColor,
			FileType:      input.FileType,
			Safe:          input.Safe,
			Country:       input.Country,
			Language:      input.Language,
		}

		traceCtx, trace := search.NewRoutingTrace(ctx)
		results, err := coalescedFetch(ctx, deps, cacheKey, func() ([]search.ImageResult, error) {
			return provider.Images(traceCtx, imgParams)
		})
		if err != nil {
			errCode := "upstream_error"
			if isRateLimitError(err) {
				errCode = "rate_limited"
			}
			deps.Metrics.RecordToolCall("image_search", time.Since(start), err, errCode, false)
			auditToolCall(ctx, deps, "image_search", time.Since(start), err, errCode)
			return upstreamErrorResponse("image search", err), nil, nil
		}
		decision := trace.Decision()
		rt := routingMeta(decision, time.Since(start), false)

		output := map[string]any{
			"images":      results,
			"query":       input.Query,
			"resultCount": len(results),
			"trust":       untrustedContentTrust,
		}

		if len(results) == 0 {
			output["hints"] = buildZeroResultHints(hintProviderName(provider), nil, nil)
		}

		effectiveProvider := decision.ProviderUsed
		if effectiveProvider == "" {
			effectiveProvider = provider.Name()
		}
		if warning := manyImageFiltersWarning(imgParams, effectiveProvider); warning != "" {
			output["warning"] = warning
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Cache.Set(ctx, cacheKey, jsonBytes, 30*time.Minute)
		deps.Metrics.RecordToolCall("image_search", time.Since(start), nil, "", false)
		auditToolCallQuery(ctx, deps, "image_search", time.Since(start), nil, "", "", map[string]any{"routing": rt})

		return withRoutingMeta(structuredResult(jsonBytes), rt), nil, nil
	})
}
