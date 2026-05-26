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
	URL       string `json:"url" jsonschema:"The HTTP/HTTPS URL to extract content from. Supports web pages, PDFs, DOCX, PPTX, and YouTube video URLs.,required"`
	Mode      string `json:"mode,omitempty" jsonschema:"Extraction depth: full (default, up to max_length) or preview (first 5000 bytes, faster). Use preview for quick relevance checks."`
	MaxLength int    `json:"max_length,omitempty" jsonschema:"Maximum content length in bytes (default: 50000). Reduce for faster responses when you only need a summary."`
}

func registerScrapePage(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "scrape_page",
		Description:  "Read and extract the main content from any URL — web pages (including JavaScript-heavy sites), PDFs, Word docs, PowerPoint files, and YouTube transcripts. Automatically picks the best extraction method. Returns the readable text along with a citation you can use (APA/MLA format) and page metadata. Use 'preview' mode for a quick look (first ~5000 characters). Use search_and_scrape to find and read pages in one step, or web_search if you just need links. Results stay fresh for 1 hour.",
		Annotations:  readOnlyAnnotations(true, true),
		OutputSchema: scrapePageOutputSchema,
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
			auditToolCall(ctx, deps, "scrape_page", time.Since(start), nil, "")
			return structuredResult(cached), nil, nil
		}

		result, err := deps.Scraper.Scrape(ctx, input.URL, maxLength)
		if err != nil {
			deps.Metrics.RecordToolCall("scrape_page", time.Since(start), err, "upstream_error", false)
			auditToolCall(ctx, deps, "scrape_page", time.Since(start), err, "upstream_error")
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
		auditToolCall(ctx, deps, "scrape_page", time.Since(start), nil, "")

		return structuredResult(jsonBytes), nil, nil
	})
}

func scrapeCacheKey(url, mode string) string {
	h := sha256.New()
	h.Write([]byte("scrape|" + url + "|" + mode))
	return "scrape:" + hex.EncodeToString(h.Sum(nil))[:32]
}
