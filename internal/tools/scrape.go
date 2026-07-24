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
	URL       string `json:"url" jsonschema:"The HTTP/HTTPS URL to extract content from. Supports web pages, PDFs, DOCX, PPTX, YouTube video URLs, Hacker News item/user/list pages (news.ycombinator.com, read natively via the HN API), GitHub README/file/gist URLs (github.com repo root, /blob/ file, or gist.github.com, read natively via the GitHub API), and Bluesky posts and profiles (bsky.app).,required"`
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
		Description:  "Read a single URL and get back its content — web pages (including JavaScript-heavy sites), PDFs, Word/PowerPoint files, YouTube transcripts, Hacker News item/user/list pages (read natively via the HN API), GitHub README/file/gist pages (read natively via the GitHub API), and Bluesky posts and profiles (bsky.app, read natively via the AT Protocol API) — picking the best extraction method automatically. Returns readable text plus a ready-to-use citation. Reach for this when you already have a URL and want what's on the page; use search_and_scrape to find and read in one step, or web_search when you only need links. Modes: full (default, cleaned text), preview (a fast first look), and raw (verbatim page bytes with no sanitization — only for inspecting source like JSON or HTML, and the bytes are untrusted, so never execute or render them). If the page is a peer-reviewed article that declares a DOI, that DOI is surfaced with its retraction/integrity status (evidence to check, not a verdict — you confirm the document's identity). Blocked pages, bot/JS-walls, dead links (404/410), and other failures return structured JSON (kind, retryable, suggestedAction) — a 404 is reported as a non-retryable not_found, a bot-wall as blocked. Results stay fresh for 1 hour.",
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
			trackScrapeOutcome(ctx, deps, input.SessionID, input.URL, err)
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

		// Extraction completeness signal (#240). Informational only: "partial"
		// means the pipeline exhausted every tier and returned the best-quality
		// candidate it could find (e.g. a SPA shell or a low-prose page) rather
		// than a confidently complete extraction; "complete" otherwise. Never an
		// error or a rejection — callers may still use partial content.
		if result.Partial {
			output["extractionQuality"] = "partial"
		} else {
			output["extractionQuality"] = "complete"
		}

		// Content-volume signal (#358), orthogonal to extractionQuality above:
		// extractionQuality reflects PIPELINE TIER EXHAUSTION, while wordCount/
		// sparsityWarning reflect how much prose was actually extracted — a
		// paywall/bot-wall stub can clear every tier check and still be too thin
		// for a caller to run a reliable claim check against. Both may be present
		// at once. wordCount is always emitted; sparsityWarning is omitted
		// (zero-value "") when content is not thin.
		output["wordCount"] = result.WordCount
		if result.SparsityWarning != "" {
			output["sparsityWarning"] = result.SparsityWarning
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

		// Forum engagement signals (#247): upvotes, comments, credibility note for
		// Reddit posts extracted from JSON-LD. Nil for non-Reddit URLs and any
		// non-HTML extraction tier (markdown, browser, raw, youtube, twitter,
		// document) — omitted so the key is absent rather than null.
		if result.ForumSignals != nil {
			output["forumSignals"] = result.ForumSignals
		}

		// Typed source classification (#62): source_type / authority_tier /
		// domain_category, derived from the Schema.org/Highwire signals + the
		// numeric authority score. Additive; no lens on scrape_page. Captured once
		// so the scholarly DOI gate below reuses it.
		cls := classifySource(input.URL, result.Title, processedContent, "", "", result.StructuredData)
		for k, v := range classificationFields(cls) {
			output[k] = v
		}

		// Scholarly DOI + integrity status (#199). Fires on a peer-reviewed page OR
		// an academic-domain host — the latter so detection still engages when a
		// publisher page is served through a tier that strips the citation_* meta
		// (e.g. the exa:cached text fallback), where SourceType degrades to unknown
		// but the host is still a known journal. Detection itself stays references-
		// safe (citation_doi meta, else bounded front-matter). Evidence, never a
		// verdict or an identity claim: "this DOI appears on the page; here is its
		// recorded integrity status."
		if cls.SourceType == content.SourceTypePeerReviewed || cls.DomainCategory == content.DomainCategoryAcademic {
			if doi := detectScholarlyDOI(result.StructuredData, processedContent, input.URL); doi != "" {
				output["detectedDoi"] = doi
				if deps.RetractionResolver != nil {
					if status, _, err := deps.RetractionResolver.Resolve(ctx, doi); err == nil && status != nil {
						output["retractionStatus"] = status
					}
				}
			}
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Cache.Set(ctx, cacheKey, jsonBytes, time.Hour)
		recordToolCall(deps, "scrape_page", time.Since(start), nil, "", false)
		auditToolCall(ctx, deps, "scrape_page", time.Since(start), nil, "")

		if input.SessionID != "" {
			trackSources(ctx, deps, input.SessionID, []session.ResearchSource{
				{URL: input.URL, Title: result.Title, Relevance: "scraped"},
			})
			trackOutcome(ctx, deps, input.SessionID, "", true, "", input.URL)
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
		// A cached raw body can be large; link it past the threshold (#181) while
		// keeping the cache-freshness _meta. Small bodies inline as before.
		return withCacheMeta(largeResultOrInline(ctx, deps, cached, "raw page content for "+input.URL), meta), nil, nil
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
		trackScrapeOutcome(ctx, deps, input.SessionID, input.URL, err)
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

	// Typed source classification (#62) — parity with full mode. Raw mode skips
	// structured-data extraction, so source_type falls back to the host heuristic
	// (authority_tier/domain_category are URL/host-derived and unaffected).
	for k, v := range classificationFields(classifySource(input.URL, result.Title, result.Content, "", "", result.StructuredData)) {
		output[k] = v
	}

	jsonBytes, _ := json.Marshal(output)
	deps.Cache.Set(ctx, cacheKey, jsonBytes, time.Hour)
	recordToolCall(deps, "scrape_page", time.Since(start), nil, "", false)
	auditToolCall(ctx, deps, "scrape_page", time.Since(start), nil, "")

	if input.SessionID != "" {
		trackSources(ctx, deps, input.SessionID, []session.ResearchSource{
			{URL: input.URL, Title: result.Title, Relevance: "scraped"},
		})
		trackOutcome(ctx, deps, input.SessionID, "", true, "", input.URL)
	}

	// Raw page bodies are the heaviest single-tool payload; link past the
	// threshold (#181) so the full text stays out of context until fetched.
	return largeResultOrInline(ctx, deps, jsonBytes, "raw page content for "+input.URL), nil, nil
}

// detectScholarlyDOITopBytes bounds the body fallback to the front matter of the
// document (title/abstract/header), which sits ABOVE the references list — so a
// references-list DOI can never be mistaken for the page's own DOI (#199).
const detectScholarlyDOITopBytes = 4000

// detectScholarlyDOI returns the page's own DOI, in descending order of
// authority: the publisher's Highwire citation_doi <head> meta (never a
// references artifact); a DOI embedded in the request URL path itself (the
// publisher's canonical article identifier, e.g. nejm.org/doi/full/10.1056/...
// — references-safe and present even on tiers that strip the citation meta, such
// as exa:cached); then a DOI in the first detectScholarlyDOITopBytes of the
// cleaned body (the front matter, above any references list). Returns "" when
// none is found. Reuses detectDOI/doiPattern (package tools); no new dependency.
func detectScholarlyDOI(sd *scraper.StructuredData, body, pageURL string) string {
	if sd != nil {
		if d := detectDOI(sd.Citation["citation_doi"]); d != "" {
			return d
		}
	}
	if d := detectDOI(pageURL); d != "" {
		return d
	}
	top := body
	if len(top) > detectScholarlyDOITopBytes {
		top = top[:detectScholarlyDOITopBytes]
	}
	return detectDOI(top)
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
	// (incl. via the shared Redis cache). v4 adds the typed classification fields
	// (#62: sourceType/authorityTier/domainCategory). v5 adds the scholarly
	// detectedDoi + retractionStatus fields (#199). v6 adds the extractionQuality
	// (complete/partial) completeness signal (#240). v7 adds the forumSignals field
	// (#247) for Reddit engagement metadata. v8 adds wordCount/sparsityWarning
	// (#358) — the content-volume signal. v9 adds the native Bluesky post/profile
	// scraper route (#285), whose forumSignals.platform="bluesky" value a v8 blob
	// never produced. Bump on any future shape change.
	fmt.Fprintf(h, "scrape|v9|%s|%s|%d", url, mode, maxLength)
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
		// A bot/JS-wall or 403 is the remote site refusing us — not a server bug.
		// Guide to an alternative source; do NOT suggest a bug report (matches the
		// inform_user suggestedAction set in scrapeErrorToToolError).
		msg = fmt.Sprintf("Blocked: %s uses bot detection. Try an alternative source — its content can't be read directly.", url)
	case scraper.ErrContent:
		msg = fmt.Sprintf("No content extracted from %s. May need browser rendering. Report at %s", url, issueURL)
	case scraper.ErrNotFound:
		msg = fmt.Sprintf("Not found: %s returned 404/410 — the page does not exist. Check the URL.", url)
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
