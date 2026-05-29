package scraper

import (
	"context"
	"fmt"
	"net/http"
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

func (p *Pipeline) Scrape(ctx context.Context, url string, maxLength int) (*ScrapeResult, error) {
	// Acquire semaphore
	select {
	case p.semaphore <- struct{}{}:
		defer func() { <-p.semaphore }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if !p.isDomainAllowed(url) {
		return nil, blockedError(url, "", nil, "domain not in allowed list")
	}

	var result *ScrapeResult
	var err error

	switch {
	case isYouTubeURL(url):
		result, err = p.scrapeYouTube(ctx, url, maxLength)
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

	hasBrowser := p.config.ChromePath != "" || chromeAvailable()

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
	var highestKind ErrorKind = ErrContent
	for _, o := range outcomes {
		switch {
		case o.err != nil:
			parts = append(parts, fmt.Sprintf("%s: %v", o.name, o.err))
			if se, ok := o.err.(*ScrapeError); ok {
				switch se.Kind {
				case ErrBlocked, ErrAuth, ErrRateLimit, ErrBrowser:
					highestKind = se.Kind
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
	return nil, &ScrapeError{Kind: highestKind, Message: msg, URL: url}
}

func (p *Pipeline) Close() {
	closeBrowserPool()
}

func (p *Pipeline) isDomainAllowed(url string) bool {
	if len(p.config.AllowedDomains) == 0 {
		return true
	}

	for _, domain := range p.config.AllowedDomains {
		if strings.Contains(url, domain) {
			return true
		}
	}
	return false
}

func isYouTubeURL(url string) bool {
	return strings.Contains(url, "youtube.com/watch") ||
		strings.Contains(url, "youtu.be/") ||
		strings.Contains(url, "youtube.com/embed")
}

func isDocumentURL(url string) bool {
	lower := strings.ToLower(url)
	return strings.HasSuffix(lower, ".pdf") ||
		strings.HasSuffix(lower, ".docx") ||
		strings.HasSuffix(lower, ".pptx") ||
		strings.Contains(lower, "application/pdf") ||
		strings.Contains(lower, "arxiv.org/pdf/")
}

var knownSPADomains = []string{
	"go.dev", "pkg.go.dev",
	"patents.google.com", "scholar.google.com", "news.google.com",
	"trends.google.com", "twitter.com", "x.com",
	"linkedin.com", "facebook.com", "instagram.com",
	"medium.com", "dev.to",
}

func isSPADomain(url string) bool {
	for _, domain := range knownSPADomains {
		if strings.Contains(url, domain) {
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
