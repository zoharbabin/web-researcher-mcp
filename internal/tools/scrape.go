package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/cookievault"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

type scrapePageInput struct {
	URL       string `json:"url" jsonschema:"The HTTP/HTTPS URL to extract content from. Supports web pages, PDFs, DOCX, PPTX, YouTube video URLs, Hacker News item/user/list pages (news.ycombinator.com, read natively via the HN API), GitHub README/file/gist URLs (github.com repo root, /blob/ file, or gist.github.com, read natively via the GitHub API), and Bluesky posts and profiles (bsky.app).,required"`
	Mode      string `json:"mode,omitempty" jsonschema:"Extraction depth (preview = first 5000 bytes, faster; raw = verbatim unsanitized bytes, see tool description before using). Default: full (cleaned readable text up to max_length)."`
	MaxLength int    `json:"max_length,omitempty" jsonschema:"Maximum content length in bytes for this call (default: 30000; raw mode: 50000). Results are capped near 35,000 serialized chars, so a larger value can be cut earlier: maxLengthApplied then shows the cap used. When the result has nextOffset, the page was cut: call again with offset=nextOffset to read the next part. Reduce for faster responses when you only need a summary."`
	Offset    int    `json:"offset,omitempty" jsonschema:"Byte position in the cleaned text where this call starts (default: 0). Use the nextOffset value of the previous result to read the next part of a long page. Full mode only; not allowed with mode=preview or mode=raw."`
	SessionID string `json:"sessionId,omitempty" jsonschema:"Link this page to a sequential_search session. The URL and title are automatically recorded as a source for recovery after context loss."`
	Anonymous bool   `json:"anonymous,omitempty" jsonschema:"Set true to read the page without your saved login (see site_login), as a signed-out visitor. Use it for public research so your account is not used. Default false: a saved login for the site is used automatically."`
}

// fullTextCap is the scrape cap for full mode. The whole cleaned page up to this
// size is scraped once and cached; each call then returns a max_length slice of
// it starting at offset. Text past the cap is not available.
const fullTextCap = 2_000_000

// maxInlineResultBytes is the size a full-mode result stays under once
// serialized: the content plus the citation, structured data and JSON escaping.
// The client rejects larger tool results, and its exact limit is unknown, so this
// leaves a margin below the ~40,000 chars seen to fail.
const maxInlineResultBytes = 35_000

// defaultFullMaxLength is the max_length of a full-mode call that sets none. It
// leaves room for the non-content fields inside maxInlineResultBytes.
const defaultFullMaxLength = 30_000

// defaultRawMaxLength is the max_length of a raw-mode call that sets none. Raw
// results over linkThresholdBytes are returned as a resource_link, so the inline
// budget does not apply and the earlier default stays.
const defaultRawMaxLength = 50_000

// Inline budget tuning for fitScrapePage.
const (
	// continuationMarkerReserve covers "\n\n[content truncated, continue with
	// offset=N]" appended to a cut part.
	continuationMarkerReserve = 64
	// minInlineContentBytes is the least content a part keeps. When the other
	// fields leave less room, the heavy optional ones are dropped from this
	// response (the cached copy keeps them).
	minInlineContentBytes = 8_000
	// budgetShrinkRounds is the number of re-marshal rounds that shrink by the
	// excess plus 10%. Later rounds shrink harder, so the loop always ends.
	budgetShrinkRounds = 3
	// budgetMaxRounds bounds the loop. Each round at least halves the cut part
	// after budgetShrinkRounds, so it ends well before this.
	budgetMaxRounds = 40
)

// scrapeOptionalHeavyFields are dropped from a part when the other fields leave
// under minInlineContentBytes of room. structuredData can reach 64 KB alone.
var scrapeOptionalHeavyFields = []string{"structuredData", "highlights", "githubTrustSignals", "forumSignals"}

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
	inputSchema := mustSchemaFor[scrapePageInput]()
	inputSchema.Properties["mode"].Enum = scrapeModeEnum()
	addTool(srv, &mcp.Tool{
		Name:         "scrape_page",
		Description:  "Read a single URL and get back its content — web pages (including JavaScript-heavy sites), PDFs, Word/PowerPoint files, YouTube transcripts, Hacker News item/user/list pages (read natively via the HN API), GitHub README/file/gist pages (read natively via the GitHub API), and Bluesky posts and profiles (bsky.app, read natively via the AT Protocol API) — picking the best extraction method automatically. Returns readable text plus a ready-to-use citation. Reach for this when you already have a URL and want what's on the page; use search_and_scrape to find and read in one step, or web_search when you only need links. Modes: full (default, cleaned text), preview (a fast first look), and raw (verbatim page bytes with no sanitization — only for inspecting source like JSON or HTML, and the bytes are untrusted, so never execute or render them). If the page is a peer-reviewed article that declares a DOI, that DOI is surfaced with its retraction/integrity status (evidence to check, not a verdict — you confirm the document's identity). Blocked pages, bot/JS-walls, dead links (404/410), and other failures return structured JSON (kind, retryable, suggestedAction) — a 404 is reported as a non-retryable not_found, a bot-wall as blocked. Results stay fresh for 1 hour. Signed-in pages (LinkedIn, etc.): call this tool directly on the URL. A login the user saved for the site with site_login, in this or an earlier session, is used automatically (cookies, or a saved browser profile that runs in a headless browser) and the result is marked authenticated and never cached. When the result has loginRequired=true (no saved login, or it expired), call site_login, then retry. Set anonymous=true to read as a signed-out visitor instead. A saved browser profile supports full and preview modes, not raw, and reads one page at a time per site.",
		Annotations:  readOnlyAnnotations(true, true),
		InputSchema:  inputSchema,
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

		if input.Offset < 0 {
			return toolError("offset must be 0 or greater"), nil, nil
		}
		if input.Offset > 0 && mode != "full" {
			return toolError("offset is only supported with mode=full; use the default mode to read a long page in parts"), nil, nil
		}

		maxLength := input.MaxLength
		if maxLength <= 0 {
			maxLength = defaultFullMaxLength
			if mode == "raw" {
				maxLength = defaultRawMaxLength
			}
		}
		if maxLength > maxScrapeLength {
			maxLength = maxScrapeLength
		}
		if mode == "preview" {
			maxLength = 5000
		}

		// Saved login (#241, #775): when the caller has a saved browser profile or
		// cookie bundle for this site and has consented, the scrape uses it. An
		// authenticated result is per-user, so it never touches the shared cache,
		// negative cache or artifact store (a later anonymous request must not
		// receive it). anonymous=true skips the login entirely.
		var cookies []cookievault.Cookie
		var lease *cookievault.ProfileLease
		if mode == "raw" && !input.Anonymous && hasSavedProfile(ctx, deps, input.URL) {
			return toolError("mode=raw is not available with a saved browser profile; use mode full or preview, or set anonymous=true"), nil, nil
		}
		if !input.Anonymous {
			var lerr error
			if lease, lerr = profileLeaseFor(ctx, deps, input.URL); lerr != nil {
				status, msg := "error", "The saved sign-in for this site could not be opened. Retry, or set anonymous=true to scrape without it."
				if errors.Is(lerr, cookievault.ErrProfileBusy) {
					status, msg = "busy", "The saved sign-in for this site is in use by another scrape or login. Retry in a minute."
				}
				recordToolCall(deps, "scrape_page", time.Since(start), lerr, "profile_"+status, false)
				auditToolCallQuery(ctx, deps, "scrape_page", time.Since(start), lerr, "profile_"+status, "", scrapeAuditExtra("profile", input.URL))
				recordSiteSession(deps, "scrape", "profile", status)
				return structuredError(msg, ToolError{Kind: ErrKindUpstream, Retryable: true, SuggestedAction: ActionRetryAfterDelay}), nil, nil
			}
			if lease == nil {
				cookies = vaultCookiesFor(ctx, deps, input.URL)
			}
		}
		authKind := ""
		switch {
		case lease != nil:
			authKind = "profile"
		case len(cookies) > 0:
			authKind = "cookies"
		}
		authed := authKind != ""
		auditExtra := scrapeAuditExtra(authKind, input.URL)
		profileStatus := "error"
		if lease != nil {
			defer func() {
				recordSiteSession(deps, "scrape", "profile", profileStatus)
				endProfileLease(ctx, lease, profileStatus == "ok")
			}()
		}
		if len(cookies) > 0 {
			ctx = scraper.WithCookies(ctx, cookies)
		}
		hintFor := func() string { return loginHintForKind(deps, authKind, input.URL) }

		// Raw mode returns the page bytes verbatim (no sanitization, no
		// content extraction) so an LLM can inspect source such as JSON or
		// HTML. It is handled on its own path with a distinct cache key so a
		// raw response never collides with the cleaned full/preview cache.
		if mode == "raw" {
			return scrapeRaw(ctx, deps, input, maxLength, start, authed)
		}

		// Full mode scrapes the whole page (up to fullTextCap) and caches it once,
		// so every offset is served from one entry. Preview keeps its own small cap.
		scrapeLimit := maxLength
		if mode == "full" {
			scrapeLimit = fullTextCap
		}

		cacheKey := scrapeCacheKey(input.URL, mode, maxLength)
		if !authed {
			if cached, meta, ok := deps.Cache.GetWithMeta(ctx, cacheKey); ok {
				if mode == "full" {
					cached, ok = pageScrapeEnvelope(cached, input.Offset, maxLength)
				}
				if ok {
					recordToolCall(deps, "scrape_page", time.Since(start), nil, "", true)
					auditToolCall(ctx, deps, "scrape_page", time.Since(start), nil, "")
					return cachedResultWithMeta(cached, meta), nil, nil
				}
			}

			// Negative-cache short-circuit: a recently-failed URL returns its cached
			// structured error without re-running the multi-tier scrape or holding a
			// scrape slot (ASI06 resource-exhaustion defense).
			if neg := negCacheLookup(ctx, deps, input.URL); neg != nil {
				recordToolCall(deps, "scrape_page", time.Since(start), neg, "upstream_error", true)
				auditToolCall(ctx, deps, "scrape_page", time.Since(start), neg, "upstream_error")
				return scrapeErrorResponseHint(neg, input.URL, hintFor()), nil, nil
			}
		}

		var result *scraper.ScrapeResult
		var err error
		if lease != nil {
			result, err = deps.Scraper.ScrapeWithProfile(ctx, input.URL, scrapeLimit,
				scraper.ProfileSpec{Dir: lease.Dir, Domain: lease.Meta.Domain, Browser: lease.Meta.Browser})
		} else {
			result, err = deps.Scraper.Scrape(ctx, input.URL, scrapeLimit)
		}
		if err != nil {
			recordToolCall(deps, "scrape_page", time.Since(start), err, "upstream_error", false)
			auditToolCallQuery(ctx, deps, "scrape_page", time.Since(start), err, "upstream_error", "", auditExtra)
			var se *scraper.ScrapeError
			if errors.As(err, &se) && !authed {
				writeNegCache(ctx, deps, input.URL, se)
			}
			trackScrapeOutcome(ctx, deps, input.SessionID, input.URL, err)
			return scrapeErrorResponseHint(err, input.URL, hintFor()), nil, nil
		}

		// Full mode keeps the whole cleaned page here and cuts it per request below.
		// Preview cuts at once, as before.
		var processedContent string
		if mode == "full" {
			processedContent = deps.Content.Clean(result.Content)
		} else {
			var truncated bool
			processedContent, truncated = deps.Content.Process(result.Content, maxLength)
			if truncated {
				result.Truncated = true
			}
		}

		contentLen := len(processedContent)
		// Count the cleaned page, not the pre-Process scrape. In full mode this is
		// the whole page, not just the requested part, so a short last part is not
		// flagged as thin.
		wordCount, sparsityWarning := content.SparsityWarning(processedContent)
		// A scanned PDF carries a more specific warning from the scraper (#828).
		if wordCount == 0 && result.SparsityWarning != "" {
			sparsityWarning = result.SparsityWarning
		}
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

		// Extraction quality (#240, #828). Informational only, never an error.
		// See extractionQualityLabel for the precedence.
		output["extractionQuality"] = extractionQualityLabel(wordCount, result.Partial, sparsityWarning)

		// Content-volume signal (#358). wordCount is always emitted;
		// sparsityWarning is omitted (zero-value "") when content is not thin.
		output["wordCount"] = wordCount
		// Login-wall signal: the page is probably a sign-in page, not the content.
		// With saved cookies it means the login expired; without, that none exists.
		wall := looksLikeLoginWall(input.URL, result.Title, processedContent, wordCount)
		if hint := hintFor(); hint != "" && wall {
			output["loginRequired"] = true
			output["loginHint"] = hint
		}
		// A profile that landed on a login page is not saved back, and the page is
		// not retried: the sign-in is gone and only site_login can fix it.
		switch {
		case wall:
			profileStatus = "login_wall"
		default:
			profileStatus = "ok"
		}
		if sparsityWarning != "" {
			output["sparsityWarning"] = sparsityWarning
		}

		if md := scrapeMetadata(result); md != nil {
			output["metadata"] = md
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

		// YouTube transcript highlights (#284): top-scored transcript segments.
		// Absent for non-YouTube URLs, the description-only fallback, and
		// transcripts with fewer than minHighlightSegments lines.
		if len(result.Highlights) > 0 {
			output["highlights"] = result.Highlights
		}

		// GitHub trust surface (#546): repo/owner/contributor/community-health/
		// release metadata for a github.com repo README scrape. Nil for
		// non-GitHub URLs, gists, and when every sub-fetch failed.
		if result.GitHubTrustSignals != nil {
			output["githubTrustSignals"] = result.GitHubTrustSignals
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

		if authed {
			output["authenticated"] = true
		}
		jsonBytes, _ := json.Marshal(output)
		if !authed {
			deps.Cache.Set(ctx, cacheKey, jsonBytes, time.Hour)
		}
		if mode == "full" {
			jsonBytes, _ = fitScrapePage(output, input.Offset, maxLength)
		}
		recordToolCall(deps, "scrape_page", time.Since(start), nil, "", false)
		auditToolCallQuery(ctx, deps, "scrape_page", time.Since(start), nil, "", "", auditExtra)

		if input.SessionID != "" {
			trackSources(ctx, deps, input.SessionID, []session.ResearchSource{
				{URL: input.URL, Title: result.Title, Relevance: "scraped"},
			})
			trackOutcome(ctx, deps, input.SessionID, "", true, "", input.URL)
		}

		return structuredResult(jsonBytes), nil, nil
	})
}

// setScrapePage replaces the whole-page content in a full-mode envelope with
// page, the part that starts at offset, and sets the fields that describe it.
// truncated is true exactly when nextOffset is present. appliedCap > 0 sets
// maxLengthApplied. Fields that describe the page as a whole (wordCount,
// citation, ...) are left as they are.
func setScrapePage(output map[string]any, totalLength, offset int, page string, next int, more bool, appliedCap int) {
	output["content"] = page
	output["contentLength"] = len(page)
	output["estimatedTokens"] = content.EstimateTokens(page)
	output["sizeCategory"] = content.SizeCategory(len(page))
	output["totalLength"] = totalLength
	output["offset"] = offset
	output["truncated"] = more
	if more {
		output["nextOffset"] = next
	} else {
		delete(output, "nextOffset")
	}
	if appliedCap > 0 {
		output["maxLengthApplied"] = appliedCap
	} else {
		delete(output, "maxLengthApplied")
	}
}

// fitScrapePage cuts the whole-page content in a full-mode envelope to the part
// that starts at offset, and returns the marshaled result. The part holds up to
// maxLength bytes, and less when the result would pass maxInlineResultBytes
// once serialized. Then maxLengthApplied reports the cap used, truncated is
// true and nextOffset points at the true continuation.
//
// The cap is maxInlineResultBytes minus the size of every other field. JSON
// escaping can make the content larger than its text, so the result is
// re-measured and the part shrunk until it fits.
func fitScrapePage(output map[string]any, offset, maxLength int) ([]byte, error) {
	full, _ := output["content"].(string)
	total := len(full)

	// Size of everything but the content. The placeholders are as wide as the
	// real values can be.
	overhead := func() int {
		setScrapePage(output, total, offset, "", total, true, maxScrapeLength)
		b, _ := json.Marshal(output)
		return len(b)
	}
	size := overhead()
	for _, k := range scrapeOptionalHeavyFields {
		if maxInlineResultBytes-size-continuationMarkerReserve >= minInlineContentBytes {
			break
		}
		if _, ok := output[k]; ok {
			delete(output, k)
			size = overhead()
		}
	}

	limit := min(maxLength, max(1, maxInlineResultBytes-size-continuationMarkerReserve))
	for round := 0; ; round++ {
		page, next, more := content.Slice(full, offset, limit)
		applied := 0
		if more && limit < maxLength {
			applied = limit
		}
		setScrapePage(output, total, offset, page, next, more, applied)
		b, err := json.Marshal(output)
		if err != nil || len(b) <= maxInlineResultBytes || limit <= 1 || round >= budgetMaxRounds {
			return b, err
		}

		// Shrink by the excess plus 10%. The excess is serialized bytes, and
		// escaping can turn one text byte into up to six, so divide by this
		// part's own expansion ratio to get text bytes.
		used := len(page)
		if more {
			used = next - offset
		}
		expansion := 1.0
		if cj, jerr := json.Marshal(page); jerr == nil && len(page) > 0 {
			expansion = max(1.0, float64(len(cj)-2)/float64(len(page)))
		}
		excess := float64(len(b) - maxInlineResultBytes)
		margin := 1.1
		if round >= budgetShrinkRounds {
			margin = 2.0
		}
		cut := int(excess*margin/expansion) + 1
		if round >= budgetShrinkRounds {
			cut += used / 8
		}
		limit = max(1, min(limit, used)-cut)
	}
}

// pageScrapeEnvelope applies fitScrapePage to a cached full-mode envelope. ok
// is false when the cached body cannot be read, so the caller scrapes again.
func pageScrapeEnvelope(cached []byte, offset, maxLength int) ([]byte, bool) {
	dec := json.NewDecoder(bytes.NewReader(cached))
	dec.UseNumber() // keep numbers in structuredData exact
	var output map[string]any
	if err := dec.Decode(&output); err != nil {
		return nil, false
	}
	if _, ok := output["content"].(string); !ok {
		return nil, false
	}
	paged, err := fitScrapePage(output, offset, maxLength)
	if err != nil {
		return nil, false
	}
	return paged, true
}

// scrapeAuditExtra is the audit metadata of a scrape that used a saved login: the
// kind and the site's registrable domain, never cookie or profile content. nil
// for an anonymous scrape.
func scrapeAuditExtra(authKind, rawURL string) map[string]any {
	if authKind == "" {
		return nil
	}
	domain := ""
	if u, err := url.Parse(rawURL); err == nil {
		domain = cookievault.RegistrableDomain(u.Hostname())
	}
	return map[string]any{"authKind": authKind, "domain": domain}
}

// rawAuthKind names the login a raw scrape used: raw mode never uses a profile.
func rawAuthKind(authed bool) string {
	if authed {
		return "cookies"
	}
	return ""
}

// endProfileLease saves the profile back (keep) or drops the work dir. It runs
// after the browser has exited, and finishes even if the caller's context ended.
func endProfileLease(ctx context.Context, lease *cookievault.ProfileLease, keep bool) {
	if !keep {
		lease.Discard()
		return
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	// ErrNoProfile means site_logout ran during the scrape: expected, not a fault.
	if _, err := lease.Commit(cctx); err != nil && !errors.Is(err, cookievault.ErrNoProfile) {
		slog.Warn("saved browser profile was not updated after a scrape", "err", err)
	}
}

// rawContentSizeFromCache recovers the raw content's byte length from a
// previously cached scrapeRaw response body, for the contentSizeBytes hint
// (#508). The cached body already carries "contentLength" (the pre-marshal
// len(result.Content)); on any parse failure this falls back to the full
// cached payload's size so the hint is always present, just less precise.
func rawContentSizeFromCache(cached []byte) int {
	var probe struct {
		ContentLength int `json:"contentLength"`
	}
	if err := json.Unmarshal(cached, &probe); err == nil && probe.ContentLength > 0 {
		return probe.ContentLength
	}
	return len(cached)
}

// scrapeRaw handles mode=="raw": it reuses the exact same tiered fetch
// pipeline as full mode (markdown -> stealth -> html -> browser, with the same
// anti-bot header spoofing, bot-wall detection, and tier escalation), through
// the same SSRF-safe client, domain allowlist, and size limit — and skips only
// the final extraction/content.Process sanitization step, returning the
// winning tier's pre-extraction bytes instead. The returned content is
// UNTRUSTED — it may contain active <script>/HTML or other injection
// payloads — so callers must never execute or render it; raw mode is intended
// only for inspecting source (JSON, HTML, plain text). The reported
// contentType is the server's real Content-Type header (may be "").
func scrapeRaw(ctx context.Context, deps Dependencies, input scrapePageInput, maxLength int, start time.Time, authed bool) (*mcp.CallToolResult, any, error) {
	cacheKey := scrapeCacheKey(input.URL, "raw", maxLength)
	if cached, meta, ok := deps.Cache.GetWithMeta(ctx, cacheKey); ok && !authed {
		recordToolCall(deps, "scrape_page", time.Since(start), nil, "", true)
		auditToolCall(ctx, deps, "scrape_page", time.Since(start), nil, "")
		// A cached raw body can be large; link it past the threshold (#181) while
		// keeping the cache-freshness _meta. Small bodies inline as before.
		// contentSizeBytes (#508) surfaces the raw content's byte length on the
		// inline summary itself, so a caller can judge whether the linked
		// artifact is worth a follow-up read without one.
		extra := map[string]any{"contentSizeBytes": rawContentSizeFromCache(cached)}
		return withCacheMeta(largeResultOrInlineWithFields(ctx, deps, cached, "raw page content for "+input.URL, extra), meta), nil, nil
	}

	// Negative-cache short-circuit. URL-level failures (SSRF/blocked/auth/browser/
	// network/rate-limit) are mode-independent, so a cached full-mode failure
	// applies to raw too. ErrContent is the exception: raw mode's tier-acceptance
	// bar is lower than full mode's (any non-empty, non-bot-walled rawBody wins —
	// see tieredFallback's raw branch), so a body that failed every tier's
	// full-mode extraction can still succeed under raw mode's looser bar. Never
	// let a cached full-mode ErrContent short-circuit raw mode.
	if neg := negCacheLookup(ctx, deps, input.URL); neg != nil && neg.Kind != scraper.ErrContent && !authed {
		recordToolCall(deps, "scrape_page", time.Since(start), neg, "upstream_error", true)
		auditToolCall(ctx, deps, "scrape_page", time.Since(start), neg, "upstream_error")
		return scrapeErrorResponseHint(neg, input.URL, loginHintForKind(deps, rawAuthKind(authed), input.URL)), nil, nil
	}

	result, err := deps.Scraper.ScrapeRaw(ctx, input.URL, maxLength)
	if err != nil {
		recordToolCall(deps, "scrape_page", time.Since(start), err, "upstream_error", false)
		auditToolCall(ctx, deps, "scrape_page", time.Since(start), err, "upstream_error")
		var se *scraper.ScrapeError
		if errors.As(err, &se) && !authed {
			writeNegCache(ctx, deps, input.URL, se)
		}
		trackScrapeOutcome(ctx, deps, input.SessionID, input.URL, err)
		return scrapeErrorResponseHint(err, input.URL, loginHintForKind(deps, rawAuthKind(authed), input.URL)), nil, nil
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

	if authed {
		output["authenticated"] = true
	}
	jsonBytes, _ := json.Marshal(output)
	if !authed {
		deps.Cache.Set(ctx, cacheKey, jsonBytes, time.Hour)
	}
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
	// contentSizeBytes (#508) surfaces contentLen on the inline summary itself.
	// An authenticated body is per-user: it is returned inline, never stored as a
	// shared artifact (#241).
	if authed {
		return structuredResult(jsonBytes), nil, nil
	}
	extraFields := map[string]any{"contentSizeBytes": contentLen}
	return largeResultOrInlineWithFields(ctx, deps, jsonBytes, "raw page content for "+input.URL, extraFields), nil, nil
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

// scrapeMetadata builds the scrape_page metadata block. It is written when any
// of title, author, publishedDate or siteName is set, so a page with a date but
// no title still reports it. author and title are always present in the block
// (possibly empty); publishedDate and siteName are omitted when empty. Returns
// nil when there is nothing to report.
func scrapeMetadata(r *scraper.ScrapeResult) map[string]any {
	if r.Title == "" && r.Author == "" && r.PublishDate == "" && r.SiteName == "" {
		return nil
	}
	md := map[string]any{
		"title":  r.Title,
		"author": r.Author,
	}
	if r.PublishDate != "" {
		md["publishedDate"] = r.PublishDate
	}
	if r.SiteName != "" {
		md["siteName"] = r.SiteName
	}
	return md
}

// scrapeCacheKey keys a cached scrape by URL and mode. maxLength is part of the
// key only in raw mode, where the body is cut to it before caching. Full mode
// caches the whole cleaned page and cuts it per request (offset and max_length),
// and preview always uses 5000, so for both the key ignores maxLength.
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
	// never produced. v10 adds the githubTrustSignals field (#546). v11 makes
	// wordCount/sparsityWarning count the delivered (post-dedup) content (#822)
	// and fixes the truncated flag, which no longer reports a cut of the raw
	// HTML/PDF bytes in full mode (#823). v12 adds metadata.publishedDate and
	// metadata.siteName, and writes the metadata block when any field is set
	// (#826). v13 caches the whole cleaned page in full mode (#824) instead of the
	// part cut to max_length, and adds totalLength/offset/nextOffset. A v12 blob
	// holds a cut page and has no paging fields. v14 adds the thin/empty
	// extractionQuality values (#828). v15 keeps in-page links as Markdown in
	// the extracted text (#827), so a v14 blob has the same text without them.
	// v16 stops the stealth tier picking a tiny content wrapper over the full
	// page (#838), so a v15 blob can hold only a fragment of the page.
	// Bump on any future shape change.
	if mode != "raw" {
		maxLength = 0
	}
	fmt.Fprintf(h, "scrape|v16|%s|%s|%d", url, mode, maxLength)
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
	return scrapeErrorResponseHint(err, url, "")
}

// scrapeErrorResponseHint is scrapeErrorResponse plus loginHint, appended to an
// auth-required error so the caller learns to run site_login.
func scrapeErrorResponseHint(err error, url, loginHint string) *mcp.CallToolResult {
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
		if errors.Is(se, scraper.ErrProfileOffSite) {
			msg = fmt.Sprintf("Stopped: %s was opened with a saved sign-in, which only works on its own site, and %s. If it sent you to a sign-in page, the sign-in expired: call site_login with replace=true, then retry. When the site is LinkedIn, or it still asks for a login, add use_login_window=true. To read the other site, scrape it with anonymous=true.", url, se.Message)
			break
		}
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
		msg = fmt.Sprintf("Not found: %s does not exist (404/410, or the domain could not be resolved). Check the URL.", url)
	case scraper.ErrAuth:
		msg = fmt.Sprintf("Auth required: %s is behind a login wall.", url)
		if loginHint != "" {
			msg += " " + loginHint
		}
	case scraper.ErrRateLimit:
		msg = fmt.Sprintf("Rate limited on %s. Retry in 60 seconds.", url)
	case scraper.ErrNetwork:
		msg = fmt.Sprintf("Network error on %s: %s. Check connectivity.", url, se.Message)
	default:
		msg = fmt.Sprintf("Scrape failed for %s: %v", url, err)
	}

	return structuredError(msg, te)
}

// extractionQualityLabel derives the extractionQuality value from the delivered
// word count (#828). Precedence: empty, partial, thin, complete.
//
//	empty    no words were delivered
//	partial  every tier was exhausted and the best candidate was returned
//	thin     sparsityWarning is set (fewer words than content.SparseWordThreshold)
//	complete otherwise
func extractionQualityLabel(wordCount int, partial bool, sparsityWarning string) string {
	switch {
	case wordCount == 0:
		return "empty"
	case partial:
		return "partial"
	case sparsityWarning != "":
		return "thin"
	default:
		return "complete"
	}
}
