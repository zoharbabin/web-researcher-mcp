package scraper

import (
	"context"
	"fmt"
	"net/http"
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
		return nil, fmt.Errorf("domain not in allowed list")
	}

	// Detect content type — route to specialized scrapers
	if isYouTubeURL(url) {
		return p.scrapeYouTube(ctx, url, maxLength)
	}

	if isDocumentURL(url) {
		return p.scrapeDocument(ctx, url, maxLength)
	}

	return p.scrapeWithTieredFallback(ctx, url, maxLength)
}

func (p *Pipeline) scrapeWithTieredFallback(ctx context.Context, url string, maxLength int) (*ScrapeResult, error) {
	type scrapeFunc func(context.Context, string, int) (*ScrapeResult, error)

	hasBrowser := p.config.ChromePath != "" || chromeAvailable()

	// For known SPA domains, prefer the browser scraper first
	if hasBrowser && isSPADomain(url) {
		if result, err := p.scrapeBrowser(ctx, url, maxLength); err == nil && result != nil && len(result.Content) > 100 {
			return result, nil
		}
	}

	tiers := []scrapeFunc{
		p.scrapeMarkdown,
		p.scrapeStealth,
		p.scrapeHTML,
	}

	if hasBrowser {
		tiers = append(tiers, p.scrapeBrowser)
	}

	var lastResult *ScrapeResult
	var lastErr error

	for _, fn := range tiers {
		result, err := fn(ctx, url, maxLength)
		if err == nil && result != nil && len(result.Content) > 100 {
			return result, nil
		}
		if result != nil {
			lastResult = result
		}
		if err != nil {
			lastErr = err
		}
	}

	if lastResult != nil && len(lastResult.Content) > 0 {
		return lastResult, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no content extracted from %s", url)
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
		strings.Contains(lower, "application/pdf")
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
	// Check common Chrome paths
	paths := []string{
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/usr/bin/google-chrome",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	}
	for _, path := range paths {
		if fileExists(path) {
			return true
		}
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
