package tools

import (
	"context"
	"encoding/json"
	"fmt"
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
}

func registerImageSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "image_search",
		Description:  "Find images by query with filters for size, type, color, and format. Returns JSON with fields: images (array of {title, link, thumbnailLink, displayLink, contextLink, width, height, fileSize}), query, resultCount. Results sorted by relevance; max 10 per call, no pagination. On no matches returns resultCount: 0 with empty array; on failure returns isError with message. Subject to per-tenant rate limit (default 30 req/min) with automatic provider fallback. Use this for visual assets or image references — not for text information. Use web_search for pages containing images, or scrape_page to extract images from a known URL. Results cached 30 min.",
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
		if cached, ok := deps.Cache.Get(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("image_search", time.Since(start), nil, "", true)
			auditToolCall(deps, "image_search", time.Since(start), nil, "")
			return structuredResult(cached), nil, nil
		}

		results, err := deps.Search.Images(ctx, search.ImageSearchParams{
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
			deps.Metrics.RecordToolCall("image_search", time.Since(start), err, "upstream_error", false)
			auditToolCall(deps, "image_search", time.Since(start), err, "upstream_error")
			return toolError(fmt.Sprintf("image search failed: %v", err)), nil, nil
		}

		output := map[string]any{
			"images":      results,
			"query":       input.Query,
			"resultCount": len(results),
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Cache.Set(ctx, cacheKey, jsonBytes, 30*time.Minute)
		deps.Metrics.RecordToolCall("image_search", time.Since(start), nil, "", false)
		auditToolCall(deps, "image_search", time.Since(start), nil, "")

		return structuredResult(jsonBytes), nil, nil
	})
}
