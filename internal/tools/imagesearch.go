package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

type imageSearchInput struct {
	Query         string `json:"query" jsonschema:"Descriptive search query for images (e.g. 'golden retriever puppy playing fetch'). More descriptive = better results.,required"`
	NumResults    int    `json:"num_results,omitempty" jsonschema:"Number of image results (1-10, default: 5)."`
	Size          string `json:"size,omitempty" jsonschema:"Filter by image size: huge, icon, large, medium, small, xlarge, xxlarge."`
	Type          string `json:"type,omitempty" jsonschema:"Filter by image type: clipart, face, lineart, stock, photo, animated."`
	ColorType     string `json:"color_type,omitempty" jsonschema:"Filter by color mode: color, gray, mono, trans (transparent background)."`
	DominantColor string `json:"dominant_color,omitempty" jsonschema:"Filter by dominant color: black, blue, brown, gray, green, orange, pink, purple, red, teal, white, yellow."`
	FileType      string `json:"file_type,omitempty" jsonschema:"Filter by file format: jpg, gif, png, bmp, svg, webp."`
	Safe          string `json:"safe,omitempty" jsonschema:"SafeSearch level: off, medium (default), high."`
	Provider      string `json:"provider,omitempty" jsonschema:"Force a specific search provider: google, brave, serper, searxng, searchapi, duckduckgo. Omit to use configured default."`
}

func registerImageSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "image_search",
		Description:  "Find images on the web matching your description. Filter by size, type (photo, clipart, line art, etc.), dominant color, or file format. Returns up to 10 image links per search. Best for finding visual references or assets — use web_search if you need text content from pages that contain images. Results stay fresh for 30 minutes.",
		Annotations:  readOnlyAnnotations(true, true),
		OutputSchema: imageSearchOutputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input imageSearchInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		if input.Query == "" {
			return toolError("query is required"), nil, nil
		}

		numResults := input.NumResults
		if numResults <= 0 {
			numResults = 5
		}

		cacheKey := searchCacheKey("image", input.Query, numResults, input.Size, input.Type, input.ColorType, input.DominantColor, input.FileType)
		if cached, meta, ok := deps.Cache.GetWithMeta(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("image_search", time.Since(start), nil, "", true)
			auditToolCall(ctx, deps, "image_search", time.Since(start), nil, "")
			return cachedResultWithMeta(cached, meta), nil, nil
		}

		provider, errResult := resolveProvider(deps, input.Provider)
		if errResult != nil {
			return errResult, nil, nil
		}

		results, err := provider.Images(ctx, search.ImageSearchParams{
			Query:         input.Query,
			NumResults:    numResults,
			Size:          input.Size,
			Type:          input.Type,
			ColorType:     input.ColorType,
			DominantColor: input.DominantColor,
			FileType:      input.FileType,
			Safe:          input.Safe,
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

		output := map[string]any{
			"images":      results,
			"query":       input.Query,
			"resultCount": len(results),
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Cache.Set(ctx, cacheKey, jsonBytes, 30*time.Minute)
		deps.Metrics.RecordToolCall("image_search", time.Since(start), nil, "", false)
		auditToolCall(ctx, deps, "image_search", time.Since(start), nil, "")

		return structuredResult(jsonBytes), nil, nil
	})
}
