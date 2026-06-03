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
)

type PipelineConfig struct {
	MaxConcurrency  int
	AllowPrivateIPs bool
	AllowedDomains  []string
	ChromePath      string
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
	// StructuredData holds machine-readable page metadata (JSON-LD, Open Graph,
	// citation_* tags) extracted by the HTML-extraction tiers (#46) — scrapeStealth
	// and scrapeHTML, the only tiers that parse a goquery.Document. The remaining
	// tiers (markdown, browser, raw, youtube, twitter, document) leave it nil.
	// Best-effort enrichment — a nil pointer means "absent" (no markup found, or
	// a non-HTML tier produced the result), never an error.
	StructuredData *StructuredData
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

type Pipeline struct {
	client    *http.Client
	semaphore chan struct{}
	config    PipelineConfig
}

func NewPipeline(cfg PipelineConfig) *Pipeline {
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 5
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
		if result, err := p.scrapeBrowser(ctx, url, maxLength); err == nil && result != nil && len(result.Content) > 100 {
			return result, nil
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

	type tierOutcome struct {
		name   string
		result *ScrapeResult
		err    error
	}

	var outcomes []tierOutcome
	var lastResult *ScrapeResult

	for _, tier := range tiers {
		result, err := tier.fn(ctx, url, maxLength)
		if err == nil && result != nil && len(result.Content) > 100 {
			return result, nil
		}
		outcomes = append(outcomes, tierOutcome{tier.name, result, err})
		if result != nil && len(result.Content) > 0 {
			lastResult = result
		}
	}

	if lastResult != nil && len(lastResult.Content) > 0 {
		return lastResult, nil
	}

	// Compose a diagnostic error showing what each tier saw
	var parts []string
	allNetwork := true
	highestKind := ErrContent
	for _, o := range outcomes {
		switch {
		case o.err != nil:
			parts = append(parts, fmt.Sprintf("%s: %v", o.name, o.err))
			if se, ok := o.err.(*ScrapeError); ok {
				switch se.Kind {
				case ErrValidation:
					// A security/validation denial is definitive: surface it as
					// the kind regardless of any sibling tier's timeout, and never
					// downgrade to network (which would imply a misleading retry).
					highestKind = ErrValidation
					allNetwork = false
				case ErrBlocked, ErrAuth, ErrRateLimit, ErrBrowser:
					if highestKind != ErrValidation {
						highestKind = se.Kind
					}
					allNetwork = false
				case ErrNetwork:
					// keep allNetwork true
				default:
					allNetwork = false
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
		return strings.HasPrefix(path, "/watch") || strings.HasPrefix(path, "/embed")
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
	"trends.google.com",
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
