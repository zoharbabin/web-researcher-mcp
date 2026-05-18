package scraper

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
)

type browserPool struct {
	mu       sync.Mutex
	browser  *rod.Browser
	launcher *launcher.Launcher
	maxPages int
}

var (
	pool     *browserPool
	poolOnce sync.Once
)

func getBrowserPool(chromePath string, maxPages int) *browserPool {
	poolOnce.Do(func() {
		pool = &browserPool{maxPages: maxPages}
	})
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.browser == nil {
		pool.init(chromePath)
	}
	return pool
}

func (bp *browserPool) init(chromePath string) {
	l := launcher.New()
	if chromePath != "" {
		l = l.Bin(chromePath)
	}
	l = l.Headless(true).
		Set("disable-gpu").
		Set("no-sandbox").
		Set("disable-dev-shm-usage").
		Set("disable-background-networking").
		Set("disable-default-apps").
		Set("disable-extensions").
		Set("disable-sync").
		Set("disable-translate").
		Set("metrics-recording-only").
		Set("no-first-run")

	controlURL, err := l.Launch()
	if err != nil {
		return
	}

	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		l.Kill()
		return
	}

	bp.browser = browser
	bp.launcher = l
}

func (bp *browserPool) close() {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if bp.browser != nil {
		bp.browser.Close()
		bp.browser = nil
	}
	if bp.launcher != nil {
		bp.launcher.Kill()
		bp.launcher = nil
	}
}

func (p *Pipeline) scrapeBrowser(ctx context.Context, url string, maxLength int) (*ScrapeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	bp := getBrowserPool(p.config.ChromePath, p.config.MaxConcurrency)
	bp.mu.Lock()
	browser := bp.browser
	bp.mu.Unlock()

	if browser == nil {
		return nil, fmt.Errorf("browser not available")
	}

	page, err := stealth.Page(browser)
	if err != nil {
		return nil, fmt.Errorf("failed to create stealth page: %w", err)
	}
	defer page.Close()

	page = page.Context(ctx)

	if err := page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width:  1920,
		Height: 1080,
	}); err != nil {
		return nil, err
	}

	err = page.Navigate(url)
	if err != nil {
		return nil, fmt.Errorf("navigation failed: %w", err)
	}

	err = page.WaitStable(500 * time.Millisecond)
	if err != nil {
		// Not fatal — page might still have content
	}

	// Extract content via JavaScript
	content, err := extractPageContent(page)
	if err != nil {
		return nil, err
	}

	if len(content) < 100 {
		return nil, nil
	}

	title, _ := page.MustElement("title").Text()
	title = strings.TrimSpace(title)

	truncated := false
	if len(content) > maxLength {
		content = content[:maxLength]
		truncated = true
	}

	return &ScrapeResult{
		URL:         url,
		Content:     content,
		ContentType: "html",
		Title:       title,
		Truncated:   truncated,
	}, nil
}

func extractPageContent(page *rod.Page) (string, error) {
	js := `(() => {
		// Remove non-content elements
		const remove = ['script', 'style', 'nav', 'footer', 'header', 'aside',
			'.sidebar', '.menu', '.ad', '.advertisement', '.cookie-banner', '.popup',
			'[role="navigation"]', '[role="banner"]', '[role="complementary"]'];
		remove.forEach(sel => {
			document.querySelectorAll(sel).forEach(el => el.remove());
		});

		// Try article/main content first
		const selectors = ['article', '[role="main"]', 'main', '.post-content',
			'.article-content', '.entry-content', '#content', '.content'];
		for (const sel of selectors) {
			const el = document.querySelector(sel);
			if (el && el.innerText.trim().length > 200) {
				return el.innerText.trim();
			}
		}

		// Fall back to body
		return document.body ? document.body.innerText.trim() : '';
	})()`

	result, err := page.Eval(js)
	if err != nil {
		return "", fmt.Errorf("content extraction failed: %w", err)
	}

	return result.Value.Str(), nil
}

func closeBrowserPool() {
	if pool != nil {
		pool.close()
	}
}
