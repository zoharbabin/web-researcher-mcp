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
	Query         string `json:"query" jsonschema:"Image search query,required"`
	NumResults    int    `json:"num_results,omitempty" jsonschema:"Number of results (1-10, default: 5)"`
	Size          string `json:"size,omitempty" jsonschema:"Image size: huge, icon, large, medium, small, xlarge, xxlarge"`
	Type          string `json:"type,omitempty" jsonschema:"Image type: clipart, face, lineart, stock, photo, animated"`
	ColorType     string `json:"color_type,omitempty" jsonschema:"Color type: color, gray, mono, trans"`
	DominantColor string `json:"dominant_color,omitempty" jsonschema:"Dominant color: black, blue, brown, gray, green, orange, pink, purple, red, teal, white, yellow"`
	FileType      string `json:"file_type,omitempty" jsonschema:"File type: jpg, gif, png, bmp, svg, webp"`
	Safe          string `json:"safe,omitempty" jsonschema:"Safe search: off, medium, high"`
}

func registerImageSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "image_search",
		Description: "Search for images with optional filters for size, type, color, and file format.",
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
			return textResult(string(cached)), nil, nil
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

		return textResult(string(jsonBytes)), nil, nil
	})
}
