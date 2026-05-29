package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

type scrapePageInput struct {
	URL       string `json:"url" jsonschema:"The HTTP/HTTPS URL to extract content from. Supports web pages, PDFs, DOCX, PPTX, and YouTube video URLs.,required"`
	Mode      string `json:"mode,omitempty" jsonschema:"Extraction depth: full (default, up to max_length) or preview (first 5000 bytes, faster). Use preview for quick relevance checks."`
	MaxLength int    `json:"max_length,omitempty" jsonschema:"Maximum content length in bytes (default: 50000). Reduce for faster responses when you only need a summary."`
	SessionID string `json:"sessionId,omitempty" jsonschema:"Link this page to a sequential_search session. The URL and title are automatically recorded as a source for recovery after context loss."`
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
		if cached, meta, ok := deps.Cache.GetWithMeta(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("scrape_page", time.Since(start), nil, "", true)
			auditToolCall(ctx, deps, "scrape_page", time.Since(start), nil, "")
			return cachedResultWithMeta(cached, meta), nil, nil
		}

		result, err := deps.Scraper.Scrape(ctx, input.URL, maxLength)
		if err != nil {
			deps.Metrics.RecordToolCall("scrape_page", time.Since(start), err, "upstream_error", false)
			auditToolCall(ctx, deps, "scrape_page", time.Since(start), err, "upstream_error")
			return scrapeErrorResponse(err, input.URL), nil, nil
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

		if input.SessionID != "" {
			trackSources(ctx, deps, input.SessionID, []session.ResearchSource{
				{URL: input.URL, Title: result.Title, Relevance: "scraped"},
			})
		}

		return structuredResult(jsonBytes), nil, nil
	})
}

func scrapeCacheKey(url, mode string) string {
	h := sha256.New()
	h.Write([]byte("scrape|" + url + "|" + mode))
	return "scrape:" + hex.EncodeToString(h.Sum(nil))[:32]
}

const issueURL = "https://github.com/zoharbabin/web-researcher-mcp/issues"

func scrapeErrorResponse(err error, url string) *mcp.CallToolResult {
	var se *scraper.ScrapeError
	if !errors.As(err, &se) {
		return structuredError(
			fmt.Sprintf("Scrape failed for %s: %v", url, err),
			ToolError{Kind: ErrKindUpstream, Retryable: true, SuggestedAction: ActionRetryAfterDelay},
		)
	}

	te := scrapeErrorToToolError(se)
	var msg string
	switch se.Kind {
	case scraper.ErrBrowser:
		msg = fmt.Sprintf("Scrape failed: Chrome unavailable. Set CHROME_PATH or install Chrome. Report at %s", issueURL)
	case scraper.ErrBlocked:
		msg = fmt.Sprintf("Blocked: %s uses bot detection. Try alternative source or report at %s", url, issueURL)
	case scraper.ErrContent:
		msg = fmt.Sprintf("No content extracted from %s. May need browser rendering. Report at %s", url, issueURL)
	case scraper.ErrAuth:
		msg = fmt.Sprintf("Auth required: %s is behind a login wall.", url)
	case scraper.ErrRateLimit:
		msg = fmt.Sprintf("Rate limited on %s. Retry in 60 seconds.", url)
	case scraper.ErrNetwork:
		msg = fmt.Sprintf("Network error on %s: %s. Check connectivity.", url, se.Message)
	default:
		msg = fmt.Sprintf("Scrape failed for %s: %v", url, err)
	}

	return structuredError(msg, te)
}
