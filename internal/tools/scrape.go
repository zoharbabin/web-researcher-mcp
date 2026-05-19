package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
)

type scrapePageInput struct {
	URL       string `json:"url" jsonschema:"URL to scrape (must be HTTP or HTTPS),required"`
	Mode      string `json:"mode,omitempty" jsonschema:"Extraction mode: full (default) or preview"`
	MaxLength int    `json:"max_length,omitempty" jsonschema:"Maximum content length in bytes (default: 50000)"`
}

func registerScrapePage(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "scrape_page",
		Description: "Extract content from a URL. Supports web pages, PDFs, DOCX, PPTX, and YouTube videos. Uses tiered extraction: markdown negotiation, HTML parsing, headless browser.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input scrapePageInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		if input.URL == "" {
			return toolError("url is required"), nil, nil
		}

		mode := input.Mode
		if mode == "" {
			mode = "full"
		}

		maxLength := input.MaxLength
		if maxLength <= 0 {
			maxLength = 50000
		}
		if mode == "preview" {
			maxLength = 5000
		}

		cacheKey := scrapeCacheKey(input.URL, mode)
		if cached, ok := deps.Cache.Get(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("scrape_page", time.Since(start), nil, "", true)
			auditToolCall(deps, "scrape_page", time.Since(start), nil, "")
			return textResult(string(cached)), nil, nil
		}

		result, err := deps.Scraper.Scrape(ctx, input.URL, maxLength)
		if err != nil {
			deps.Metrics.RecordToolCall("scrape_page", time.Since(start), err, "upstream_error", false)
			auditToolCall(deps, "scrape_page", time.Since(start), err, "upstream_error")
			return toolError(fmt.Sprintf("scrape failed: %v", err)), nil, nil
		}

		processedContent, truncated := deps.Content.Process(result.Content, maxLength)
		if truncated {
			result.Truncated = true
		}

		contentLen := len(processedContent)
		citation := content.ExtractCitation(input.URL, result.Title, result.Author, result.SiteName, result.PublishDate)

		output := map[string]any{
			"url":             input.URL,
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

		return textResult(string(jsonBytes)), nil, nil
	})
}

func scrapeCacheKey(url, mode string) string {
	h := sha256.New()
	h.Write([]byte("scrape|" + url + "|" + mode))
	return "scrape:" + hex.EncodeToString(h.Sum(nil))[:32]
}
