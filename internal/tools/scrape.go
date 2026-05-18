package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
)

func registerScrapePage(srv *server.MCPServer, deps Dependencies) {
	tool := mcp.NewTool("scrape_page",
		mcp.WithDescription("Extract content from a URL. Supports web pages, PDFs, DOCX, PPTX, and YouTube videos. Uses tiered extraction: markdown negotiation, HTML parsing, headless browser."),
		mcp.WithString("url", mcp.Required(), mcp.Description("URL to scrape (must be HTTP or HTTPS)")),
		mcp.WithString("mode", mcp.Description("Extraction mode: full (default) or preview")),
		mcp.WithNumber("max_length", mcp.Description("Maximum content length in bytes (default: 50000)")),
	)

	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		url, _ := req.GetArguments()["url"].(string)
		if url == "" {
			return toolError("url is required"), nil
		}

		mode, _ := req.GetArguments()["mode"].(string)
		if mode == "" {
			mode = "full"
		}

		maxLength := intParam(req.GetArguments(), "max_length", 50000)
		if mode == "preview" {
			maxLength = 5000
		}

		// Cache check
		cacheKey := scrapeCacheKey(url, mode)
		if cached, ok := deps.Cache.Get(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("scrape_page", time.Since(start), nil, "", true)
			auditToolCall(deps, "scrape_page", time.Since(start), nil, "")
			return mcp.NewToolResultText(string(cached)), nil
		}

		result, err := deps.Scraper.Scrape(ctx, url, maxLength)
		if err != nil {
			deps.Metrics.RecordToolCall("scrape_page", time.Since(start), err, "upstream_error", false)
			auditToolCall(deps, "scrape_page", time.Since(start), err, "upstream_error")
			return toolError(fmt.Sprintf("scrape failed: %v", err)), nil
		}

		processedContent, truncated := deps.Content.Process(result.Content, maxLength)
		if truncated {
			result.Truncated = true
		}

		contentLen := len(processedContent)
		citation := content.ExtractCitation(url, result.Title, result.Author, result.SiteName, result.PublishDate)

		output := map[string]any{
			"url":             url,
			"content":         processedContent,
			"contentType":     result.ContentType,
			"contentLength":   contentLen,
			"truncated":       result.Truncated,
			"estimatedTokens": content.EstimateTokens(processedContent),
			"sizeCategory":    content.SizeCategory(contentLen),
			"citation":        citation,
		}

		if result.Title != "" {
			output["metadata"] = map[string]any{
				"title":  result.Title,
				"author": result.Author,
			}
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Cache.Set(ctx, cacheKey, jsonBytes, time.Hour)
		deps.Metrics.RecordToolCall("scrape_page", time.Since(start), nil, "", false)
		auditToolCall(deps, "scrape_page", time.Since(start), nil, "")

		return mcp.NewToolResultText(string(jsonBytes)), nil
	})
}

func scrapeCacheKey(url, mode string) string {
	h := sha256.New()
	h.Write([]byte("scrape|" + url + "|" + mode))
	return "scrape:" + hex.EncodeToString(h.Sum(nil))[:32]
}
