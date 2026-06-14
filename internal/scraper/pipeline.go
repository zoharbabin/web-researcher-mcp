package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
)

type PipelineConfig struct {
	MaxConcurrency  int
	AllowPrivateIPs bool
	AllowedDomains  []string
	ChromePath      string
	// ExaAPIKey, when set, enables the Exa /contents extraction tier as a final
	// fallback (after markdown→stealth→html→browser all fail). Exa is a paid API,
	// so it is deliberately last: the free tiers win the overwhelming majority of
	// pages, and Exa only spends a request on the hard pages they cannot extract.
	// Empty (default) ⇒ the tier is absent and no Exa request is ever made.
	ExaAPIKey string
	// MaxHTMLBytes bounds the decompressed HTML body each HTML-parsing tier reads
	// before extraction (stealth, html, patents). Zero ⇒ the NewPipeline default.
	MaxHTMLBytes int
	// MaxDocumentBytes bounds the document download (PDF/DOCX/PPTX) in scrapeDocument.
	// Zero ⇒ the NewPipeline default.
	MaxDocumentBytes int
	// HNFirebaseBase overrides the production HN Firebase base URL (for tests).
	// Empty (default) ⇒ https://hacker-news.firebaseio.com/v0
	HNFirebaseBase string
	// HNAlgoliaBase overrides the production HN Algolia base URL (for tests).
	// Empty (default) ⇒ https://hn.algolia.com/api/v1
	HNAlgoliaBase string
}

type ScrapeResult struct {
	URL         string
	Content     string
	ContentType string
	Title       string
	Author      string
	SiteName    string
	PublishDate string
	Truncated   bool
	// Partial is true when the fallback returned the best-effort result across
	// all tiers but none produced confidently-complete content. Informational;
	// never blocks or errors. False for direct-router results (youtube/twitter/document).
	Partial bool
	// Tier names the extraction tier that produced this result ("markdown",
	// "stealth", "html", "browser", "exa:cached"/"exa:crawled", "youtube",
	// "twitter", "document", "raw"). Provenance for the tool layer — surfaced so a
	// caller can see whether content came from a free tier or the paid Exa
	// fallback. Empty for results from tiers that predate this field.
	Tier string
	// StructuredData holds machine-readable page metadata (JSON-LD, Open Graph,
	// citation_* tags) extracted by the HTML-extraction tiers (#46) — scrapeStealth
	// and scrapeHTML, the only tiers that parse a goquery.Document. The remaining
	// tiers (markdown, browser, raw, youtube, twitter, document) leave it nil.
	// Best-effort enrichment — a nil pointer means "absent" (no markup found, or
	// a non-HTML tier produced the result), never an error.
	StructuredData *StructuredData
	// rawHTMLBytes is the size of the decompressed HTML the HTML-parsing tiers
	// (stealth, html) read before extraction. The pipeline reads it to detect a
	// JavaScript-rendered SPA shell — a large HTML payload that yielded little
	// extracted text — and to keep escalating to the JS-executing browser tier
	// instead of accepting the partial shell (see looksLikePartialShell). Zero
	// for tiers that don't parse raw HTML (markdown, browser, document, youtube,
	// twitter, raw). Unexported: pipeline-internal provenance, never serialized.
	rawHTMLBytes int
}

// StructuredData is page-embedded, machine-readable metadata lifted verbatim
// from the HTML (#46). It is UNTRUSTED external data, subject to the same trust
// boundary as scraped content. All fields are size-bounded at extraction time
// (see extractStructuredData in html.go) because content.Process never sees it.
type StructuredData struct {
	// JSONLD holds each valid <script type="application/ld+json"> block verbatim
	// (validated with json.Valid; invalid blocks skipped). RawMessage preserves
	// arbitrary schema.org shapes (object/array/@graph) losslessly.
	JSONLD []json.RawMessage `json:"jsonLd,omitempty"`
	// OpenGraph maps og:* and article:* meta[property] to content, keys kept
	// with their prefix (e.g. "og:title", "article:published_time").
	OpenGraph map[string]string `json:"openGraph,omitempty"`
	// Citation maps Highwire <meta name="citation_*"> to content, verbatim keys
	// (e.g. "citation_title", "citation_doi").
	Citation map[string]string `json:"citation,omitempty"`
}

// IsEmpty reports whether no structured data was captured. Nil-safe receiver so
// callers can use it on a possibly-nil pointer. Exported because the tools
// package reads it to decide whether to surface the field.
func (s *StructuredData) IsEmpty() bool {
	return s == nil || (len(s.JSONLD) == 0 && len(s.OpenGraph) == 0 && len(s.Citation) == 0)
}

// Signals reduces the rich StructuredData to the decoupled signals the content
// package needs for typed source classification (#62): the top-level Schema.org
// @type of each JSON-LD block, and whether Highwire citation_* meta was present.
// Returns a non-nil result with zero fields when empty/nil. Keeping the JSON-LD
// @type parse here (the scraper already owns the raw blocks) means the content
// package never imports scraper.
func (s *StructuredData) Signals() content.StructuredSignals {
	if s == nil {
		return content.StructuredSignals{}
	}
	sig := content.StructuredSignals{HasCitationMeta: len(s.Citation) > 0}
	for _, raw := range s.JSONLD {
		if t := jsonLDTopType(raw); t != "" {
			sig.SchemaTypes = append(sig.SchemaTypes, t)
		}
	}
	return sig
}

// jsonLDTopType extracts the top-level "@type" from a JSON-LD block. Schema.org
// allows @type to be a string or array of strings; both are handled. Returns the
// first/only type, or "".
func jsonLDTopType(raw json.RawMessage) string {
	var probe struct {
		Type json.RawMessage `json:"@type"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil || len(probe.Type) == 0 {
		return ""
	}
	var single string
	if err := json.Unmarshal(probe.Type, &single); err == nil {
		return single
	}
	var many []string
	if err := json.Unmarshal(probe.Type, &many); err == nil && len(many) > 0 {
		return many[0]
	}
	return ""
}

// stampTier records which extraction tier produced a result, unless the tier
// already set it (e.g. the Exa tier records "exa:cached" vs "exa:crawled"
// provenance and must not be overwritten). Nil-safe passthrough.
func stampTier(r *ScrapeResult, tier string) *ScrapeResult {
	if r != nil && r.Tier == "" {
		r.Tier = tier
	}
	return r
}

type Pipeline struct {
	client    *http.Client
	semaphore chan struct{}
	config    PipelineConfig
}

func NewPipeline(cfg PipelineConfig) *Pipeline {
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 5
	}
	if cfg.MaxHTMLBytes <= 0 {
		cfg.MaxHTMLBytes = 8 << 20 // 8 MB
	}
	if cfg.MaxDocumentBytes <= 0 {
		cfg.MaxDocumentBytes = 50 << 20 // 50 MB
	}

	return &Pipeline{
		client:    NewSSRFSafeClient(cfg.AllowPrivateIPs),
		semaphore: make(chan struct{}, cfg.MaxConcurrency),
		config:    cfg,
	}
}

func (p *Pipeline) Scrape(ctx context.Context, rawURL string, maxLength int) (*ScrapeResult, error) {
	// Single validation chokepoint for every entry path (scrape_page and
	// search_and_scrape both flow through here). Rejects non-http(s) schemes
	// and empty hosts before any network or semaphore work.
	if err := validateScrapeURL(rawURL); err != nil {
		return nil, validationError(rawURL, "", err, err.Error())
	}

	url := rawURL

	// Acquire semaphore
	select {
	case p.semaphore <- struct{}{}:
		defer func() { <-p.semaphore }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if !p.isDomainAllowed(url) {
		return nil, validationError(url, "", nil, "access blocked: domain not in allowed list")
	}

	var result *ScrapeResult
	var err error

	switch {
	case isYouTubeURL(url):
		result, err = p.scrapeYouTube(ctx, url, maxLength)
	case isTwitterURL(url):
		result, err = p.scrapeTwitter(ctx, url, maxLength)
	case isHNURL(url):
		result, err = p.scrapeHN(ctx, url, maxLength)
	case isDocumentURL(url):
		result, err = p.scrapeDocument(ctx, url, maxLength)
	default:
		result, err = p.scrapeWithTieredFallback(ctx, url, maxLength)
	}

	if err != nil {
		return nil, classifyRawError(err, url)
	}
	return result, nil
}

func (p *Pipeline) scrapeWithTieredFallback(ctx context.Context, url string, maxLength int) (*ScrapeResult, error) {
	type namedTier struct {
		name string
		fn   func(context.Context, string, int) (*ScrapeResult, error)
	}

	hasBrowser := p.browserEnabled()

	// For known SPA domains, prefer the browser scraper first
	if hasBrowser && isSPADomain(url) {
		if result, err := p.scrapeBrowser(ctx, url, maxLength); err == nil && result != nil && len(result.Content) > 100 && !looksLikeBotWall(result.Content) {
			return stampTier(result, "browser"), nil
		}
	}

	tiers := []namedTier{
		{"markdown", p.scrapeMarkdown},
		{"stealth", p.scrapeStealth},
		{"html", p.scrapeHTML},
	}

	if hasBrowser {
		tiers = append(tiers, namedTier{"browser", p.scrapeBrowser})
	}

	// Exa /contents is the LAST tier: a paid neural extractor that recovers the
	// hard pages the free tiers cannot. It runs only when configured and only
	// after every free tier has failed to extract >100 bytes — so the common
	// path never incurs Exa cost.
	if p.config.ExaAPIKey != "" {
		tiers = append(tiers, namedTier{"exa", p.scrapeExa})
	}

	type tierOutcome struct {
		name   string
		result *ScrapeResult
		err    error
	}

	var outcomes []tierOutcome
	// best is the longest non-bot-wall result seen so far across all tiers. It is
	// the deterministic fallback when no tier produces confidently-complete
	// content (e.g. a partial SPA shell is the only thing the HTTP tiers can get
	// and the browser tier is unavailable): we return the most content extracted,
	// never silently drop to an error.
	var best *ScrapeResult

	for _, tier := range tiers {
		result, err := tier.fn(ctx, url, maxLength)
		// A bot/JS-wall interstitial (e.g. "Checking your browser…", a CAPTCHA shell)
		// is returned with a 200 and short placeholder text. Do NOT accept it as
		// content — record it as a blocked outcome so the composite error is
		// ErrBlocked, not a misleading low-quality success.
		if err == nil && result != nil && looksLikeBotWall(result.Content) {
			outcomes = append(outcomes, tierOutcome{tier.name, nil, blockedError(url, tier.name, nil, "bot/JS-wall interstitial")})
			continue
		}
		if err == nil && result != nil && len(result.Content) > 100 {
			stamped := stampTier(result, tier.name)
			// Confident, complete content wins immediately — same latency as before
			// for the overwhelming majority of pages. A partial SPA shell (a large
			// HTML payload that yielded little extracted text) does NOT short-circuit:
			// it is kept as the fallback while the pipeline keeps escalating toward
			// the JS-executing browser tier, which can render the real content.
			if !looksLikePartialShell(stamped) {
				return stamped, nil
			}
			best = betterResult(best, stamped)
			outcomes = append(outcomes, tierOutcome{tier.name, stamped, nil})
			continue
		}
		outcomes = append(outcomes, tierOutcome{tier.name, result, err})
		if result != nil && len(result.Content) > 0 && !looksLikeBotWall(result.Content) {
			best = betterResult(best, stampTier(result, tier.name))
		}
	}

	// No tier produced confidently-complete content. Return whichever tier
	// extracted the MOST — the deterministic best-of fallback (e.g. the stealth
	// shell when the browser tier was unavailable or also thin).
	if best != nil && len(best.Content) > 0 {
		best.Partial = true
		return best, nil
	}

	// Compose a diagnostic error showing what each tier saw. When tiers disagree
	// on the kind, the MOST DEFINITIVE diagnosis wins (scrapeKindPriority) — never
	// the last one seen. This is what stops a later tier's transient failure (e.g.
	// the browser tier's launch/eval error on an empty page) from masking an
	// earlier tier's authoritative signal: a 404 the HTTP tiers already saw must
	// surface as not_found, not as the browser tier's content_empty/browser error.
	var parts []string
	allNetwork := true
	highestKind := ErrContent
	bestPriority := -1
	for _, o := range outcomes {
		switch {
		case o.err != nil:
			parts = append(parts, fmt.Sprintf("%s: %v", o.name, o.err))
			if se, ok := o.err.(*ScrapeError); ok {
				// A pure-network failure leaves allNetwork true so a run where
				// every tier merely timed out is still reported as retryable
				// network; anything else is a definite per-tier diagnosis.
				if se.Kind != ErrNetwork {
					allNetwork = false
				}
				if pr := scrapeKindPriority(se.Kind); pr > bestPriority {
					bestPriority = pr
					highestKind = se.Kind
				}
			} else {
				allNetwork = false
			}
		case o.result != nil:
			parts = append(parts, fmt.Sprintf("%s: %d bytes", o.name, len(o.result.Content)))
			allNetwork = false
		default:
			parts = append(parts, fmt.Sprintf("%s: empty", o.name))
			allNetwork = false
		}
	}
	if allNetwork && len(outcomes) > 0 {
		highestKind = ErrNetwork
	}

	detail := strings.Join(parts, ", ")
	msg := fmt.Sprintf("no content extracted from %s (%s)", url, detail)

	// An SSRF / private-IP / blocked-hostname denial is a permanent security
	// rejection even when the per-tier errors were wrapped as generic network
	// errors (each tier's SSRF-safe client reports it inside its message). Such a
	// denial must never be presented as a retryable network failure, so detect it
	// in the composite detail and classify the whole result as validation.
	if isSSRFDenial(detail) {
		highestKind = ErrValidation
	}

	return nil, &ScrapeError{Kind: highestKind, Message: msg, URL: url}
}

// shell-detection thresholds. Deliberately fixed constants, not tunables: the
// goal the user asked for is a WIDER but PREDICTABLE escalation rule, so the
// behavior is fully determined by the bytes a tier already read — no sampling,
// no scoring, no environment knobs.
const (
	// shellMaxTextBytes bounds the "little extracted text" half of the signal. A
	// result longer than this is substantial enough that re-rendering in a browser
	// is not worth the latency, even if its HTML:text ratio is high (a long article
	// on a heavy page). 2KB is ~1-2 short paragraphs — below a real article, above
	// the one-line above-the-fold blurb a server-rendered SPA shell ships.
	shellMaxTextBytes = 2048
	// shellMinHTMLRatio is the HTML-bytes-to-text-bytes ratio above which a short
	// result looks like a shell rather than a genuinely short page. An SPA ships a
	// large HTML payload (JS bundles, hydration JSON, empty mount divs) for a sliver
	// of text; a real short page ships little HTML AND little text. 20:1 cleanly
	// separates the two (sentra.app/research was ~50:1; a short static page is <5:1).
	shellMinHTMLRatio = 20
)

// looksLikePartialShell reports whether a tier result is most likely a
// JavaScript-rendered SPA shell — a large static HTML payload that extracted
// into only a sliver of readable text — rather than a genuinely short but
// complete page. It is the deterministic gate that lets the pipeline keep
// escalating to the JS-executing browser tier instead of accepting the shell.
//
// The test is purely structural and uses only bytes the tier already read:
//   - the extracted text is short (<= shellMaxTextBytes), AND
//   - the raw HTML is at least shellMinHTMLRatio times larger than the text.
//
// rawHTMLBytes is zero for tiers that don't parse raw HTML (markdown, browser,
// document, …); those can never be flagged, which is correct — the browser tier
// already executed JS, so its short output is final, not a shell to escalate past.
func looksLikePartialShell(r *ScrapeResult) bool {
	if r == nil || r.rawHTMLBytes == 0 {
		return false
	}
	textLen := len(r.Content)
	if textLen == 0 || textLen > shellMaxTextBytes {
		return false
	}
	return r.rawHTMLBytes/textLen >= shellMinHTMLRatio
}

// contentQuality scores a result by effective prose bytes: raw length weighted
// by prose density (fraction of letters + spaces). Penalises nav/link shells
// dense in punctuation, digits, and slashes. Used only to order fallback
// candidates; never rejects a result. The incumbent wins on a tie so an
// equal-quality earlier (cheaper) tier is not displaced.
func contentQuality(r *ScrapeResult) float64 {
	if r == nil || len(r.Content) == 0 {
		return 0
	}
	letters, spaces := 0, 0
	for _, c := range r.Content {
		switch {
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			letters++
		case c == ' ' || c == '\t':
			spaces++
		}
	}
	density := float64(letters+spaces) / float64(len(r.Content))
	return float64(len(r.Content)) * density
}

// betterResult returns whichever of two results has higher effective prose
// quality, preferring the incumbent on a tie. Either argument may be nil.
func betterResult(incumbent, candidate *ScrapeResult) *ScrapeResult {
	if candidate == nil {
		return incumbent
	}
	if incumbent == nil || contentQuality(candidate) > contentQuality(incumbent) {
		return candidate
	}
	return incumbent
}

// ScrapeRaw fetches a URL once and returns the raw response body verbatim,
// SKIPPING the tiered extraction pipeline and content.Process sanitization.
// It still enforces the SAME security guards as Scrape: validateScrapeURL,
// the SSRF-safe HTTP client, the domain allowlist, and io.LimitReader(maxLength)
// to bound memory. The returned ContentType is the server's real MIME type
// (Content-Type header, "" if absent). Callers MUST treat the body as untrusted
// (it may contain active <script>/HTML) — raw mode is opt-in for scrape_page only.
func (p *Pipeline) ScrapeRaw(ctx context.Context, rawURL string, maxLength int) (*ScrapeResult, error) {
	if err := validateScrapeURL(rawURL); err != nil {
		return nil, validationError(rawURL, "", err, err.Error())
	}

	select {
	case p.semaphore <- struct{}{}:
		defer func() { <-p.semaphore }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if !p.isDomainAllowed(rawURL) {
		return nil, blockedError(rawURL, "", nil, "domain not in allowed list")
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, classifyRawError(err, rawURL)
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; web-researcher-mcp/1.0)")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, networkError(rawURL, "raw", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, classifyHTTPStatus(resp.StatusCode, rawURL, "raw")
	}

	limit := maxLength
	if limit <= 0 {
		limit = 1
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)))
	if err != nil {
		return nil, networkError(rawURL, "raw", err)
	}

	contentType := resp.Header.Get("Content-Type")
	// Reading up to the limit means more data likely remained on the wire.
	truncated := len(body) >= limit

	return &ScrapeResult{
		URL:         rawURL,
		Content:     string(body),
		ContentType: contentType,
		Truncated:   truncated,
	}, nil
}

func (p *Pipeline) Close() {
	closeBrowserPool()
}

// chromeDisabled is the sentinel CHROME_PATH value that turns the browser
// rendering tier off entirely (no auto-download, no detection). Useful for
// hardened/headless deployments and for deterministic tests.
const chromeDisabled = "disabled"

// browserEnabled reports whether the browser (go-rod) scraping tier should run.
// CHROME_PATH="disabled" forces it off; an explicit path forces it on; an empty
// path falls back to autodetecting a local Chromium/Chrome install.
func (p *Pipeline) browserEnabled() bool {
	if p.config.ChromePath == chromeDisabled {
		return false
	}
	return p.config.ChromePath != "" || chromeAvailable()
}

// validateScrapeURL is the single boundary validator for all scrape entry
// points. It requires an http or https scheme and a non-empty host, rejecting
// file://, gopher://, ftp://, scheme-relative ("//host"), and host-less URLs.
func validateScrapeURL(rawURL string) error {
	return ValidateScrapeURL(rawURL)
}

// ValidateScrapeURL is the exported form of the boundary validator, shared by
// tools that take a user-supplied URL to fetch or archive (e.g. archive_source)
// so they reject obviously-bad input identically — without a divergent copy. It
// does NOT do a DNS/IP check; the SSRF-safe client validates resolved IPs at
// connect time.
func ValidateScrapeURL(rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q (only http and https are allowed)", u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("URL has no host")
	}
	return nil
}

// hostnameMatches reports whether the host of rawURL equals domain or is a
// dot-bounded subdomain of it. It parses the URL and compares only the host,
// so "https://example.com.attacker.net/" does NOT match "example.com" and a
// query like "https://evil.com/?q=example.com" does NOT match either.
func hostnameMatches(rawURL, domain string) bool {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if domain == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" {
		return false
	}
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func (p *Pipeline) isDomainAllowed(url string) bool {
	if len(p.config.AllowedDomains) == 0 {
		return true
	}

	for _, domain := range p.config.AllowedDomains {
		if hostnameMatches(url, domain) {
			return true
		}
	}
	return false
}

func isYouTubeURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	path := u.Path
	switch host {
	case "youtube.com", "m.youtube.com":
		return strings.HasPrefix(path, "/watch") ||
			strings.HasPrefix(path, "/embed") ||
			strings.HasPrefix(path, "/shorts/") ||
			strings.HasPrefix(path, "/live/") ||
			strings.HasPrefix(path, "/v/")
	case "youtu.be":
		return len(strings.TrimPrefix(path, "/")) > 0
	}
	return false
}

func isDocumentURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	lowerPath := strings.ToLower(u.Path)
	if strings.HasSuffix(lowerPath, ".pdf") ||
		strings.HasSuffix(lowerPath, ".docx") ||
		strings.HasSuffix(lowerPath, ".pptx") {
		return true
	}
	// arxiv serves PDFs under the /pdf/ path on its host.
	if hostnameMatches(rawURL, "arxiv.org") && strings.HasPrefix(lowerPath, "/pdf/") {
		return true
	}
	return false
}

var knownSPADomains = []string{
	"go.dev", "pkg.go.dev",
	"patents.google.com", "scholar.google.com", "news.google.com",
	"trends.google.com", "youtube.com",
	"linkedin.com", "facebook.com", "instagram.com",
	"medium.com", "dev.to",
}

func isSPADomain(url string) bool {
	for _, domain := range knownSPADomains {
		if hostnameMatches(url, domain) {
			return true
		}
	}
	return false
}

func chromeAvailable() bool {
	paths := []string{
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/usr/bin/google-chrome",
		"/usr/bin/google-chrome-stable",
		"/opt/homebrew/bin/chromium",
		"/usr/local/bin/chromium",
		"/snap/bin/chromium",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	}
	for _, path := range paths {
		if fileExists(path) {
			return true
		}
	}
	if _, err := exec.LookPath("chromium"); err == nil {
		return true
	}
	if _, err := exec.LookPath("google-chrome"); err == nil {
		return true
	}
	return false
}

func fileExists(path string) bool {
	_, err := statFile(path)
	return err == nil
}

var statFile = func(path string) (any, error) {
	info, err := timeoutStat(path)
	return info, err
}

func timeoutStat(path string) (any, error) {
	type result struct {
		err error
	}
	ch := make(chan result, 1)
	go func() {
		// #nosec G111 -- path is from a fixed internal allowlist of chromium binary locations, not user input
		_, err := http.Dir("/").Open(path)
		ch <- result{err}
	}()
	select {
	case r := <-ch:
		return nil, r.err
	case <-time.After(100 * time.Millisecond):
		return nil, fmt.Errorf("stat timeout")
	}
}
