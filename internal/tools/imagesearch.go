package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

func registerImageSearch(srv *server.MCPServer, deps Dependencies) {
	tool := mcp.NewTool("image_search",
		mcp.WithDescription("Search for images with optional filters for size, type, color, and file format."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Image search query")),
		mcp.WithNumber("num_results", mcp.Description("Number of results (1-10, default: 5)")),
		mcp.WithString("size", mcp.Description("Image size: huge, icon, large, medium, small, xlarge, xxlarge")),
		mcp.WithString("type", mcp.Description("Image type: clipart, face, lineart, stock, photo, animated")),
		mcp.WithString("color_type", mcp.Description("Color type: color, gray, mono, trans")),
		mcp.WithString("dominant_color", mcp.Description("Dominant color: black, blue, brown, gray, green, orange, pink, purple, red, teal, white, yellow")),
		mcp.WithString("file_type", mcp.Description("File type: jpg, gif, png, bmp, svg, webp")),
		mcp.WithString("safe", mcp.Description("Safe search: off, medium, high")),
	)

	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		query, _ := req.GetArguments()["query"].(string)
		if query == "" {
			return toolError("query is required"), nil
		}

		numResults := intParam(req.GetArguments(), "num_results", 5)
		size, _ := req.GetArguments()["size"].(string)
		imgType, _ := req.GetArguments()["type"].(string)
		colorType, _ := req.GetArguments()["color_type"].(string)
		dominantColor, _ := req.GetArguments()["dominant_color"].(string)
		fileType, _ := req.GetArguments()["file_type"].(string)
		safe, _ := req.GetArguments()["safe"].(string)

		cacheKey := searchCacheKey("image", query, numResults, size, imgType, colorType, dominantColor, fileType)
		if cached, ok := deps.Cache.Get(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("image_search", time.Since(start), nil, "", true)
			auditToolCall(deps, "image_search", time.Since(start), nil, "")
			return mcp.NewToolResultText(string(cached)), nil
		}

		results, err := deps.Search.Images(ctx, search.ImageSearchParams{
			Query:         query,
			NumResults:    numResults,
			Size:          size,
			Type:          imgType,
			ColorType:     colorType,
			DominantColor: dominantColor,
			FileType:      fileType,
			Safe:          safe,
		})
		if err != nil {
			deps.Metrics.RecordToolCall("image_search", time.Since(start), err, "upstream_error", false)
			auditToolCall(deps, "image_search", time.Since(start), err, "upstream_error")
			return toolError(fmt.Sprintf("image search failed: %v", err)), nil
		}

		output := map[string]any{
			"images":      results,
			"query":       query,
			"resultCount": len(results),
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Cache.Set(ctx, cacheKey, jsonBytes, 30*time.Minute)
		deps.Metrics.RecordToolCall("image_search", time.Since(start), nil, "", false)
		auditToolCall(deps, "image_search", time.Since(start), nil, "")

		return mcp.NewToolResultText(string(jsonBytes)), nil
	})
}
