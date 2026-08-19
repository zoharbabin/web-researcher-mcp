package scraper

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
)

type browserPool struct {
	mu          sync.Mutex
	browser     *rod.Browser
	launcher    *launcher.Launcher
	initErr     error
	idleTimeout time.Duration
	idleTimer   *time.Timer
	activeUses  int
}

var (
	pool     *browserPool
	poolOnce sync.Once
)

// getBrowserPool returns the singleton browser pool, launching Chromium if it
// is not already running. idleTimeout (#460) is re-armed on every call that
// finds (or creates) a live browser: if no caller touches the pool again
// within idleTimeout, it is closed automatically, bounding how long a single
// browser-tier scrape pins a Chromium process — and its macOS Dock icon —
// after activity stops. Zero disables the idle-close timer.
func getBrowserPool(chromePath string, maxPages int, idleTimeout time.Duration) *browserPool {
	poolOnce.Do(func() {
		pool = &browserPool{}
	})
	_ = maxPages // parameter retained for call-site compatibility; page-level limiting is handled by Pipeline.browserSemaphore (#472)
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.browser != nil && !pool.connectedLocked() {
		// The Chromium process behind bp.browser has died since init() ran (OOM,
		// malicious page, Chromium bug) but the pointer is still non-nil, so the
		// old `== nil` check never fired — every subsequent call would hang
		// against a dead CDP connection until the caller's own 30s timeout
		// (issue #464). Relaunch now so the caller gets a live browser instead.
		slog.Warn("browser pool detected dead connection", "phase", "liveness-check", "action", "relaunching", "chromePath", chromePath)
		pool.closeLocked()
	}
	if pool.browser == nil {
		pool.init(chromePath)
	}
	pool.idleTimeout = idleTimeout
	if pool.browser != nil {
		pool.touchLocked()
	}
	return pool
}

// touchLocked (re)arms the idle-close timer from now. A no-op while the pool
// is in active use (bp.activeUses > 0) — acquire()/release() bracket that
// window so the timer can never fire mid-operation. Caller must hold bp.mu.
func (bp *browserPool) touchLocked() {
	if bp.idleTimer != nil {
		bp.idleTimer.Stop()
		bp.idleTimer = nil
	}
	if bp.idleTimeout <= 0 || bp.activeUses > 0 {
		return
	}
	// Capture idleTimeout under the lock rather than reading bp.idleTimeout
	// from the callback goroutine, which runs without bp.mu held.
	timeout := bp.idleTimeout
	var timer *time.Timer
	timer = time.AfterFunc(timeout, func() {
		bp.mu.Lock()
		defer bp.mu.Unlock()
		// A later touchLocked/acquire call may have replaced this timer (or
		// put the pool back in active use) between it firing and this
		// goroutine acquiring bp.mu — in that case this fire is stale.
		if bp.idleTimer != timer || bp.activeUses > 0 {
			return
		}
		bp.idleTimer = nil
		slog.Info("browser pool idle timeout reached, closing", "idleTimeout", timeout)
		bp.closeLocked()
	})
	bp.idleTimer = timer
}

// acquire marks the pool as actively in use, disarming the idle-close timer
// so a long-running scrape can never have its browser closed out from under
// it. Pairs with release(), which re-arms the timer once the last active use
// ends. Returns the current *rod.Browser (nil if unavailable).
func (bp *browserPool) acquire() *rod.Browser {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	bp.activeUses++
	if bp.idleTimer != nil {
		bp.idleTimer.Stop()
		bp.idleTimer = nil
	}
	return bp.browser
}

// release ends one active use started by acquire(). Once the last active use
// ends, the idle-close timer is (re)armed starting now.
func (bp *browserPool) release() {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if bp.activeUses > 0 {
		bp.activeUses--
	}
	if bp.activeUses == 0 {
		bp.touchLocked()
	}
}

// livenessCheckTimeout bounds only the CDP round-trip used to detect a dead
// browser connection (issue #464) — short enough that the probe itself
// resolves quickly, long enough that a briefly slow-but-alive browser is not
// misdiagnosed as dead. It does NOT bound the full recovery path: the
// getBrowserPool/init() relaunch that follows a detected-dead connection has
// no deadline and can exceed the 30s scrapeBrowser budget on a cold or slow
// host.
const livenessCheckTimeout = 2 * time.Second

// connectedLocked reports whether bp.browser's CDP connection is still alive
// by making a lightweight, bounded-timeout round-trip call (Browser.getVersion)
// to the Chromium process. go-rod has no dedicated IsConnected() method; a
// bounded protocol call is the standard liveness probe for a possibly-dead
// connection — a genuinely dead websocket surfaces the error near-instantly
// (the read loop's error path notifies all pending calls), while a merely
// slow-but-alive browser has up to livenessCheckTimeout to respond.
// Caller must hold bp.mu.
func (bp *browserPool) connectedLocked() bool {
	if bp.browser == nil {
		return false
	}
	_, err := bp.browser.Timeout(livenessCheckTimeout).Version()
	return err == nil
}

func (bp *browserPool) init(chromePath string) {
	l := launcher.New()
	if chromePath != "" {
		l = l.Bin(chromePath)
	}
	// Use --headless=new (Chrome 112+): better GPU-pipeline parity with headed
	// Chrome and does NOT embed "HeadlessChrome/X.Y.Z" in the UA string (legacy
	// --headless embeds it; go-rod/stealth does not patch the UA at all).
	l = l.HeadlessNew(true).
		Set("disable-gpu").
		Set("no-sandbox").
		Set("disable-dev-shm-usage").
		Set("no-first-run").
		// Delete the two automation-signal flags that go-rod's launcher.New() adds
		// unconditionally and that bot-wall systems detect before any JS runs.
		//
		// --enable-automation is the critical one: Blink reads it at startup to set
		// navigator.webdriver=true at the C++ layer and show the automation infobar,
		// BEFORE go-rod/stealth's JS patch ("delete Object.getPrototypeOf(navigator)
		// .webdriver") can suppress it. go-rod's own NewAppMode() calls this same
		// Delete("enable-automation") for exactly this reason.
		//
		// --metrics-recording-only is a Chrome internal test-harness flag with no
		// legitimate use in content extraction — its presence in navigator-exposed
		// state is a detectable fingerprint.
		//
		// Other go-rod defaults (disable-background-networking, disable-sync, etc.)
		// are retained because they are operational and removing them can cause
		// Chrome to hang during startup on some hosts.
		Delete("enable-automation").
		Delete("metrics-recording-only").
		// Defense-in-depth: suppresses AutomationControlled at the Blink feature
		// flag level. Redundant after deleting enable-automation (Blink will not
		// set the property), but harmless as a second layer.
		Set("disable-blink-features", "AutomationControlled")

	controlURL, err := l.Launch()
	if err != nil {
		bp.initErr = fmt.Errorf("chrome launch failed: %w", err)
		slog.Warn("browser pool init failed", "phase", "launch", "error", err, "chromePath", chromePath)
		return
	}

	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		bp.initErr = fmt.Errorf("chrome connect failed: %w", err)
		slog.Warn("browser pool init failed", "phase", "connect", "error", err)
		l.Kill()
		return
	}

	bp.browser = browser
	bp.launcher = l
}

func (bp *browserPool) close() {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	bp.closeLocked()
}

// closeLocked is the body of close(), factored out so getBrowserPool's
// liveness-check-then-relaunch path (issue #464) can tear down a dead
// browser without releasing and re-acquiring bp.mu (which would let another
// caller race in between). Caller must hold bp.mu.
func (bp *browserPool) closeLocked() {
	if bp.idleTimer != nil {
		bp.idleTimer.Stop()
		bp.idleTimer = nil
	}
	if bp.browser != nil {
		// Bound this CDP round-trip (issue #464): closeLocked() is now also
		// reached when the browser is already known-dead (liveness check
		// failed in getBrowserPool), in which case an un-timed-out Close()
		// call would itself hang forever — Browser.Close() has no deadline by
		// default (b.ctx defaults to context.Background()). A dead connection
		// still returns near-instantly via the read loop's error path; this
		// timeout only guards the case where it doesn't.
		_ = bp.browser.Timeout(livenessCheckTimeout).Close()
		bp.browser = nil
	}
	if bp.launcher != nil {
		bp.launcher.Kill()
		// Cleanup blocks until the killed process's Wait() goroutine observes
		// exit, then removes UserDataDir (cached cookies/page content) from
		// disk (issue #393, SOC 2 confidentiality: no retention beyond the
		// browser's own lifetime). Kill() already sleeps 1s waiting for
		// children to be ready to receive the signal, so this adds no new
		// unbounded wait — the process is already dying by the time we get here.
		bp.launcher.Cleanup()
		bp.launcher = nil
	}
}

func (p *Pipeline) scrapeBrowser(ctx context.Context, url string, maxLength int, raw bool) (*ScrapeResult, error) {
	// Defensive: callers gate on browserEnabled(), but never let the "disabled"
	// sentinel reach the browser pool as if it were a real binary path.
	if p.config.ChromePath == chromeDisabled {
		return nil, browserError(url, nil, "browser tier disabled (CHROME_PATH=disabled)")
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	bp := getBrowserPool(p.config.ChromePath, p.config.MaxBrowserConcurrency, p.config.BrowserIdleTimeout)
	browser := bp.acquire()
	defer bp.release()

	if browser == nil {
		msg := "browser not available (chrome not found)"
		if bp.initErr != nil {
			msg = bp.initErr.Error()
		}
		return nil, browserError(url, bp.initErr, msg)
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

	// CDP-level UA override: pins a real macOS Chrome UA on every network
	// request this page makes. go-rod/stealth does not patch the UA string at
	// all; --headless=new avoids "HeadlessChrome" in the Blink-generated UA, but
	// a CDP override is the definitive fix and also locks the Accept-Language and
	// platform hints to values consistent with a real macOS user.
	// stealthUA is a compile-time constant — never derived from config or input.
	const stealthUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	if err := page.SetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent:      stealthUA,
		AcceptLanguage: "en-US,en;q=0.9",
		Platform:       "MacIntel",
	}); err != nil {
		slog.Warn("browser UA override failed", "url", url, "error", err)
	}

	// CDP timezone emulation: a UTC system timezone on a US-geolocated IP is a
	// detectable mismatch used by Cloudflare Turnstile and PerimeterX. Pinning to
	// America/New_York (most common US timezone by population) is consistent with
	// the macOS/US UA above. Non-fatal: older Chromium builds may not support it.
	if err := (proto.EmulationSetTimezoneOverride{
		TimezoneID: "America/New_York",
	}).Call(page); err != nil {
		slog.Warn("browser timezone override failed", "url", url, "error", err)
	}

	// Subscribe to the CDP events proving the main document's HTTP status
	// before triggering navigation — CDP events are asynchronous, so a
	// subscription started after Navigate() can race and miss them. Track
	// every Document-type response for this page's top-level frame (a
	// redirect chain fires responseReceived once per hop, all with the SAME
	// FrameID) and keep only the LAST one — the final response once redirects
	// settle. Stop once PageFrameNavigated confirms the frame's navigation
	// committed, mirroring the same pattern Page.Reload() already uses to
	// detect navigation completion.
	var mainStatus int
	var gotMainStatus bool
	waitNav := page.EachEvent(
		func(e *proto.NetworkResponseReceived) bool {
			if e.Type == proto.NetworkResourceTypeDocument && e.FrameID == page.FrameID && e.Response != nil {
				mainStatus = e.Response.Status
				gotMainStatus = true
			}
			return false
		},
		func(e *proto.PageFrameNavigated) bool {
			return e.Frame.ID == page.FrameID
		},
	)

	err = page.Navigate(url)
	if err != nil {
		return nil, networkError(url, "browser", fmt.Errorf("navigation failed: %w", err))
	}
	waitNav()

	// A genuine HTTP 4xx/5xx on the main document (e.g. a dead DOI-resolver
	// landing page) must be reported as a definite scrape failure — the three
	// HTTP-based tiers already do this via classifyHTTPStatus (markdown.go,
	// html.go, stealth.go). Unlike them, CDP navigation never surfaces a
	// status code through page.Navigate()'s own return value, so without this
	// check a 404's "Page Not Found" HTML was silently returned as if it were
	// real content (issue #432). gotMainStatus can be false when the main
	// document's responseReceived event never fired (rare CDP timing edge) —
	// in that case there is no signal either way, so extraction proceeds as
	// before rather than guessing.
	if gotMainStatus && mainStatus >= 400 {
		return nil, classifyHTTPStatus(mainStatus, url, "browser")
	}

	// Wait for the page to reach a stable DOM state. SPAs need longer — they
	// hydrate after the initial HTML load.
	//
	// WaitLoad and WaitStable can both block indefinitely on pages with persistent
	// background polling (e.g. Frontify's React SPA keeps WebSocket connections
	// open). We guard each phase with a timeout sub-context so the 30s outer
	// deadline is not consumed waiting for an idle state that never arrives.
	waitTime := 1500 * time.Millisecond
	if isSPADomain(url) {
		waitTime = 3 * time.Second
	}

	// Phase 1: wait for window.onload (capped at waitTime).
	loadCtx, loadCancel := context.WithTimeout(ctx, waitTime)
	_ = page.Context(loadCtx).WaitLoad()
	loadCancel()

	// Phase 2: wait for the DOM to stabilise after React/Vue hydration (capped
	// at waitTime to prevent infinite hang on pages with persistent network
	// activity). Ignore any deadline error — extract whatever the page has.
	stableCtx, stableCancel := context.WithTimeout(ctx, waitTime)
	_ = page.Context(stableCtx).WaitStable(waitTime / 3)
	stableCancel()

	// Extract content via JavaScript
	content, err := extractPageContent(page)
	if err != nil {
		return nil, err
	}

	if len(content) < 100 {
		return nil, nil
	}

	var title string
	if el, err := page.Element("title"); err == nil && el != nil {
		if t, err := el.Text(); err == nil {
			title = strings.TrimSpace(t)
		}
	}

	truncated := false
	if len(content) > maxLength {
		// F19: back up to the start of a valid UTF-8 rune so we never slice mid-sequence.
		// json.Marshal would silently replace broken sequences with U+FFFD otherwise.
		for maxLength > 0 && !utf8.RuneStart(content[maxLength]) {
			maxLength--
		}
		content = content[:maxLength]
		truncated = true
	}

	res := &ScrapeResult{
		URL:         url,
		Content:     content,
		ContentType: "html",
		Title:       title,
		Truncated:   truncated,
	}

	// Raw mode's "unfiltered" bytes for this tier are the rendered DOM
	// (page.HTML(), post-JS-execution) rather than the JS-extracted text in
	// Content — there is no pre-render HTTP body to fall back to once the
	// browser tier ran. Best-effort only: a failure here just leaves rawBody
	// empty, and ScrapeRaw falls back to Content in that case.
	if raw {
		if html, herr := page.HTML(); herr == nil {
			if len(html) > maxLength {
				html = truncateBytes(html, maxLength)
				res.Truncated = true
			}
			res.rawBody = html
			res.rawContentType = "text/html; charset=utf-8"
		}
	}

	return res, nil
}

func extractPageContent(page *rod.Page) (string, error) {
	// page.Eval() wraps the JS as (fn).apply(this, []), so this must be a
	// function expression — NOT an IIFE. Passing an IIFE returns a string, and
	// string.apply() throws TypeError.
	js := `() => {
		// Read-only extraction — never mutate the live DOM (React fiber reconciliation
		// throws TypeError if you remove nodes it owns).
		// safeText: innerText can throw on SVG/custom elements; textContent is safe.
		function safeText(el) {
			if (!el) return '';
			try { return (el.innerText || el.textContent || '').trim(); }
			catch(e) { try { return (el.textContent || '').trim(); } catch(e2) { return ''; } }
		}

		try {
			// Selector priority: semantic containers first, then SPA roots (#app/#root),
			// then brand-portal class patterns last.
			//
			// IMPORTANT: #app/#root must precede [class*="brand-"] / [class*="portal-"]
			// AND the native main element. Brand portal SPAs (e.g. Corebook.io at
			// brand.kaltura.com) render content inside #app/#root; the <main> element is
			// the outermost layout shell including sidebars. Placing main after the SPA
			// mounts ensures React-rendered color swatches are preferred over
			// navigation-heavy outer containers.
			const selectors = [
				'article', '[role="main"]',
				'#app', '#root',
				'main',
				'[class*="BrandPage"]', '[class*="Guideline"]', '[class*="Library"]',
				'[class*="brand-"]', '[class*="portal-"]',
				'.post-content', '.article-content', '.entry-content',
				'#content', '.content',
			];
			for (const sel of selectors) {
				try {
					const el = document.querySelector(sel);
					if (el) { const t = safeText(el); if (t.length > 200) return t; }
				} catch(e) {}
			}
			// Full-body fallback.
			return safeText(document.body || document.documentElement);
		} catch(e) {
			try { return (document.body || document.documentElement || {}).textContent || ''; }
			catch(e2) { return ''; }
		}
	}`

	result, err := page.Eval(js)
	if err != nil {
		return "", fmt.Errorf("content extraction failed: %w", err)
	}

	return result.Value.Str(), nil
}

// extractPageLinks returns absolute URLs of all rendered <a href> elements.
// Relative hrefs (bare paths, ./relative, ../parent, and root-relative /paths)
// are all resolved against the page's current URL via the URL constructor.
func extractPageLinks(page *rod.Page) ([]string, error) {
	js := `() => {
		try {
			const seen = new Set();
			const out = [];
			document.querySelectorAll('a[href]').forEach(a => {
				try {
					let href = a.getAttribute('href') || '';
					if (!href || href.startsWith('#') || href.startsWith('javascript:') || href.startsWith('mailto:')) return;
					// F17: resolve all relative forms (bare path, ./, ../, root-relative)
					// against the current page URL in one expression.
					try { href = new URL(href, window.location.href).href; } catch(e) { return; }
					if (!href.startsWith('http')) return;
					if (!seen.has(href)) { seen.add(href); out.push(href); }
				} catch(e) {}
			});
			return out.join('\n');
		} catch(e) { return ''; }
	}`
	result, err := page.Eval(js)
	if err != nil {
		return nil, fmt.Errorf("link extraction failed: %w", err)
	}
	raw := strings.TrimSpace(result.Value.Str())
	if raw == "" {
		return nil, nil
	}
	return strings.Split(raw, "\n"), nil
}

func closeBrowserPool() {
	if pool != nil {
		pool.close()
	}
}
