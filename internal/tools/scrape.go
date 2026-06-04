package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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

// userAssertedContentTrust marks content the user themselves supplied and the
// server merely stored verbatim (memory_recall notes). It is a DISTINCT, honest
// value from untrustedContentTrust: the server cannot know whether a saved note
// originated from a scraped page or the user's own words, so it does not claim
// "external" — only that the host should treat recalled text as data, not
// instructions. Same envelope-level boundary marker, different provenance.
const userAssertedContentTrust = "user-asserted-content"

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
			recordToolCall(deps, "scrape_page", time.Since(start), nil, "", true)
			auditToolCall(ctx, deps, "scrape_page", time.Since(start), nil, "")
			return cachedResultWithMeta(cached, meta), nil, nil
		}

		// Negative-cache short-circuit: a recently-failed URL returns its cached
		// structured error without re-running the multi-tier scrape or holding a
		// scrape slot (ASI06 resource-exhaustion defense).
		if neg := negCacheLookup(ctx, deps, input.URL); neg != nil {
			recordToolCall(deps, "scrape_page", time.Since(start), neg, "upstream_error", true)
			auditToolCall(ctx, deps, "scrape_page", time.Since(start), neg, "upstream_error")
			return scrapeErrorResponse(neg, input.URL), nil, nil
		}

		result, err := deps.Scraper.Scrape(ctx, input.URL, maxLength)
		if err != nil {
			recordToolCall(deps, "scrape_page", time.Since(start), err, "upstream_error", false)
			auditToolCall(ctx, deps, "scrape_page", time.Since(start), err, "upstream_error")
			var se *scraper.ScrapeError
			if errors.As(err, &se) {
				writeNegCache(ctx, deps, input.URL, se)
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

		// Extraction provenance: which tier produced the content. Surfaced so a
		// caller can see when content came from the paid Exa fallback ("exa:cached"
		// /"exa:crawled") rather than a free local tier. Omitted when unknown.
		if result.Tier != "" {
			output["extractedBy"] = result.Tier
		}

		// Structured data (#46) is additive and present only when the HTML tier
		// captured JSON-LD/OG/citation markup; IsEmpty() is nil-safe, so the key
		// is simply omitted for raw/PDF/markdown-tier results and plain pages.
		if !result.StructuredData.IsEmpty() {
			output["structuredData"] = result.StructuredData
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Cache.Set(ctx, cacheKey, jsonBytes, time.Hour)
		recordToolCall(deps, "scrape_page", time.Since(start), nil, "", false)
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
		recordToolCall(deps, "scrape_page", time.Since(start), nil, "", true)
		auditToolCall(ctx, deps, "scrape_page", time.Since(start), nil, "")
		return cachedResultWithMeta(cached, meta), nil, nil
	}

	// Negative-cache short-circuit. URL-level failures (SSRF/blocked/auth/browser/
	// network/rate-limit) are mode-independent, so a cached full-mode failure
	// applies to raw too. ErrContent is the exception: it means extraction found
	// nothing, but raw skips extraction and may still return bytes — so never let
	// a cached ErrContent short-circuit raw mode.
	if neg := negCacheLookup(ctx, deps, input.URL); neg != nil && neg.Kind != scraper.ErrContent {
		recordToolCall(deps, "scrape_page", time.Since(start), neg, "upstream_error", true)
		auditToolCall(ctx, deps, "scrape_page", time.Since(start), neg, "upstream_error")
		return scrapeErrorResponse(neg, input.URL), nil, nil
	}

	result, err := deps.Scraper.ScrapeRaw(ctx, input.URL, maxLength)
	if err != nil {
		recordToolCall(deps, "scrape_page", time.Since(start), err, "upstream_error", false)
		auditToolCall(ctx, deps, "scrape_page", time.Since(start), err, "upstream_error")
		var se *scraper.ScrapeError
		if errors.As(err, &se) {
			writeNegCache(ctx, deps, input.URL, se)
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
	recordToolCall(deps, "scrape_page", time.Since(start), nil, "", false)
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
	// The version segment invalidates pre-existing cached blobs whenever the
	// response SHAPE changes, so a cache hit can never serve an envelope missing
	// a newly-added field. v2 introduced the "trust" boundary marker; v3 adds
	// GFM markdown-table content (#48) and the optional structuredData field
	// (#46) — both change the full-mode response shape, so a v2 blob would serve
	// table-less/garbled content and omit structuredData after an upgrade
	// (incl. via the shared Redis cache). Bump again on any future shape change.
	fmt.Fprintf(h, "scrape|v3|%s|%s|%d", url, mode, maxLength)
	return "scrape:" + hex.EncodeToString(h.Sum(nil))[:32]
}

const issueURL = "https://github.com/zoharbabin/web-researcher-mcp/issues"

// negCacheKey builds the negative-cache key for a URL. The kind is NOT part of
// the key (it is unknown before scraping); it is stored as the VALUE so a later
// request can read it back and short-circuit without re-running the full
// multi-tier scrape (OWASP Agentic ASI06: a recently-failed URL must not tie up
// a scrape slot on every retry). Keyed by the FULL URL — keying by domain would
// collide distinct paths on the same host and mask their specific errors.
func negCacheKey(url string) string {
	h := sha256.New()
	fmt.Fprintf(h, "negv2|%s", url)
	return "neg:" + hex.EncodeToString(h.Sum(nil))[:32]
}

// writeNegCache records that scraping url failed with se.Kind + its original
// message, for negCacheTTL. The value is "kind\x00message" so a cache hit
// reconstructs the SAME error text as a fresh failure, not a generic
// placeholder. (Secret masking is applied later, by the audit sink, to whatever
// error is recorded — the reconstructed message flows through that identical
// path, so a cached failure is masked exactly like a live one.)
func writeNegCache(ctx context.Context, deps Dependencies, url string, se *scraper.ScrapeError) {
	val := strconv.Itoa(int(se.Kind)) + "\x00" + se.Message
	deps.Cache.Set(ctx, negCacheKey(url), []byte(val), negCacheTTL(se.Kind))
}

// negCacheLookup returns a reconstructed ScrapeError if url is in the negative
// cache, or nil. The cached value is "kind\x00message", so the reconstructed
// error carries the SAME message as the original failure, preserving error
// detail (the message may embed the URL; the audit sink masks it identically).
func negCacheLookup(ctx context.Context, deps Dependencies, url string) *scraper.ScrapeError {
	v, ok := deps.Cache.Get(ctx, negCacheKey(url))
	if !ok {
		return nil
	}
	s := string(v)
	kindStr, msg := s, ""
	if i := indexOf(s, "\x00"); i >= 0 {
		kindStr, msg = s[:i], s[i+1:]
	}
	n, err := strconv.Atoi(kindStr)
	if err != nil {
		return nil
	}
	return &scraper.ScrapeError{Kind: scraper.ErrorKind(n), Message: msg, URL: url}
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
