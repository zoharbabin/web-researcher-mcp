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
	Mode      string `json:"mode,omitempty" jsonschema:"Extraction depth: full (default, cleaned readable text up to max_length), preview (first 5000 bytes, faster), or raw (verbatim unsanitized bytes — see tool description before using)."`
	MaxLength int    `json:"max_length,omitempty" jsonschema:"Maximum content length in bytes (default: 50000). Reduce for faster responses when you only need a summary."`
	SessionID string `json:"sessionId,omitempty" jsonschema:"Link this page to a sequential_search session. The URL and title are automatically recorded as a source for recovery after context loss."`
}

// maxScrapeLength caps the requested max_length to bound memory for a single
// scrape. Applies to all modes including raw.
const maxScrapeLength = 5_000_000

// untrustedContentTrust is the value of the structured-output "trust" field on
// every response that carries scraped page text. It is an explicit,
// machine-readable boundary marker placed in the JSON envelope — NOT in the
// content string itself, where a malicious page could forge or close it
// (OWASP LLM01, indirect prompt injection). It signals to the host/agent that
// `content` is external data to be treated as data, never as instructions. The
// server cannot enforce the prompt boundary (the model and agent loop live in
// the host); this marker makes the untrusted provenance unmissable so the host
// can. See docs/SECURITY.md "Trust boundary marker".
const untrustedContentTrust = "untrusted-external-content"

func registerScrapePage(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "scrape_page",
		Description:  "Read a single URL and get back its content — web pages (including JavaScript-heavy sites), PDFs, Word/PowerPoint files, and YouTube transcripts — picking the best extraction method automatically. Returns readable text plus a ready-to-use citation. Reach for this when you already have a URL and want what's on the page; use search_and_scrape to find and read in one step, or web_search when you only need links. Modes: full (default, cleaned text), preview (a fast first look), and raw (verbatim page bytes with no sanitization — only for inspecting source like JSON or HTML, and the bytes are untrusted, so never execute or render them). Blocked pages and other failures return structured JSON (kind, retryable, suggestedAction). Results stay fresh for 1 hour.",
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
		if maxLength > maxScrapeLength {
			maxLength = maxScrapeLength
		}
		if mode == "preview" {
			maxLength = 5000
		}

		// Raw mode returns the page bytes verbatim (no sanitization, no
		// content extraction) so an LLM can inspect source such as JSON or
		// HTML. It is handled on its own path with a distinct cache key so a
		// raw response never collides with the cleaned full/preview cache.
		if mode == "raw" {
			return scrapeRaw(ctx, deps, input, maxLength, start)
		}

		cacheKey := scrapeCacheKey(input.URL, mode, maxLength)
		if cached, meta, ok := deps.Cache.GetWithMeta(ctx, cacheKey); ok {
			deps.Metrics.RecordToolCall("scrape_page", time.Since(start), nil, "", true)
			auditToolCall(ctx, deps, "scrape_page", time.Since(start), nil, "")
			return cachedResultWithMeta(cached, meta), nil, nil
		}

		result, err := deps.Scraper.Scrape(ctx, input.URL, maxLength)
		if err != nil {
			deps.Metrics.RecordToolCall("scrape_page", time.Since(start), err, "upstream_error", false)
			auditToolCall(ctx, deps, "scrape_page", time.Since(start), err, "upstream_error")
			var se *scraper.ScrapeError
			if errors.As(err, &se) {
				key := negCacheKey(input.URL, se.Kind)
				deps.Cache.Set(ctx, key, []byte("1"), negCacheTTL(se.Kind))
			}
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
			"trust":           untrustedContentTrust,
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

// scrapeRaw handles mode=="raw": it fetches the page bytes verbatim through the
// same SSRF-safe client, domain allowlist, and size limit as Scrape, but skips
// the extraction pipeline and content.Process sanitization entirely. The
// returned content is UNTRUSTED — it may contain active <script>/HTML or other
// injection payloads — so callers must never execute or render it; raw mode is
// intended only for inspecting source (JSON, HTML, plain text). The reported
// contentType is the server's real Content-Type header (may be "").
func scrapeRaw(ctx context.Context, deps Dependencies, input scrapePageInput, maxLength int, start time.Time) (*mcp.CallToolResult, any, error) {
	cacheKey := scrapeCacheKey(input.URL, "raw", maxLength)
	if cached, meta, ok := deps.Cache.GetWithMeta(ctx, cacheKey); ok {
		deps.Metrics.RecordToolCall("scrape_page", time.Since(start), nil, "", true)
		auditToolCall(ctx, deps, "scrape_page", time.Since(start), nil, "")
		return cachedResultWithMeta(cached, meta), nil, nil
	}

	result, err := deps.Scraper.ScrapeRaw(ctx, input.URL, maxLength)
	if err != nil {
		deps.Metrics.RecordToolCall("scrape_page", time.Since(start), err, "upstream_error", false)
		auditToolCall(ctx, deps, "scrape_page", time.Since(start), err, "upstream_error")
		var se *scraper.ScrapeError
		if errors.As(err, &se) {
			key := negCacheKey(input.URL, se.Kind)
			deps.Cache.Set(ctx, key, []byte("1"), negCacheTTL(se.Kind))
		}
		return scrapeErrorResponse(err, input.URL), nil, nil
	}

	contentLen := len(result.Content)
	citation := content.ExtractCitation(input.URL, result.Title, result.Author, result.SiteName, result.PublishDate)

	output := map[string]any{
		"url":             input.URL,
		"content":         result.Content,
		"contentType":     result.ContentType,
		"trust":           untrustedContentTrust,
		"contentLength":   contentLen,
		"truncated":       result.Truncated,
		"estimatedTokens": content.EstimateTokens(result.Content),
		"sizeCategory":    content.SizeCategory(contentLen),
		"citation":        citation,
		"raw":             true,
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
}

// scrapeCacheKey keys a cached scrape by URL, mode, AND the effective
// max_length. max_length must be part of the key because content is truncated
// to it before caching — without it, a small-max_length request could serve a
// later larger request a truncated body (breaking consistency across calls).
func scrapeCacheKey(url, mode string, maxLength int) string {
	h := sha256.New()
	// The version segment (v2) invalidates pre-existing cached blobs whenever
	// the response SHAPE changes, so a cache hit can never serve an envelope
	// missing a newly-added field. v2 introduced the "trust" boundary marker —
	// bumping it guarantees no upgraded deployment (incl. the shared Redis
	// cache) serves scrape content without the untrusted-content marker the
	// host may rely on. Bump again on any future output-shape change.
	fmt.Fprintf(h, "scrape|v2|%s|%s|%d", url, mode, maxLength)
	return "scrape:" + hex.EncodeToString(h.Sum(nil))[:32]
}

const issueURL = "https://github.com/zoharbabin/web-researcher-mcp/issues"

// negCacheKey builds a cache key for negative caching of scrape errors.
func negCacheKey(url string, kind scraper.ErrorKind) string {
	domain := extractDomain(url)
	return "neg:" + domain + ":" + string(mapScrapeErrorKind(kind))
}

func extractDomain(rawURL string) string {
	if idx := indexOf(rawURL, "://"); idx >= 0 {
		rawURL = rawURL[idx+3:]
	}
	if idx := indexOf(rawURL, "/"); idx >= 0 {
		rawURL = rawURL[:idx]
	}
	return rawURL
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func negCacheTTL(kind scraper.ErrorKind) time.Duration {
	switch kind {
	case scraper.ErrValidation:
		// Permanent rejection (bad scheme / SSRF / blocked host) — cache long;
		// the same URL will be rejected identically every time.
		return 4 * time.Hour
	case scraper.ErrBlocked, scraper.ErrAuth:
		return 30 * time.Minute
	case scraper.ErrRateLimit:
		return 90 * time.Second
	case scraper.ErrNetwork:
		return 2 * time.Minute
	case scraper.ErrBrowser:
		return 4 * time.Hour
	default:
		return 10 * time.Minute
	}
}

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
	case scraper.ErrValidation:
		// Permanent rejection: unsupported scheme, empty host, or an SSRF /
		// private-IP / blocked-hostname denial. Report the precise reason and
		// do NOT suggest a retry or a bug report — the URL itself must change.
		msg = fmt.Sprintf("URL rejected for %s: %s. Provide a valid public http(s) URL.", url, se.Message)
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
