package scraper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/zoharbabin/web-researcher-mcp/internal/cookievault"
)

// ProfileSpec names a tool-owned browser profile unpacked on disk (issue #775).
type ProfileSpec struct {
	Dir     string // unpacked profile dir, owned by the caller
	Domain  string // registrable domain the profile is signed in to
	Browser string // binary that created the profile; "" or missing falls back to config
}

// ErrProfileOffSite marks a scrape that a saved profile stopped because the page
// went to another site. The sign-in only works on its own domain.
var ErrProfileOffSite = errors.New("scraper: page went to a site the saved sign-in does not cover")

const (
	profileScrapeTimeout = 60 * time.Second
	profileStableFor     = time.Second
	profileStableCap     = 15 * time.Second
	profilePollEvery     = 400 * time.Millisecond
)

// scrapeWithProfile is the body of ScrapeWithProfile. It reads rawURL in a headless browser started on spec.Dir, so
// the page sees the sign-in the user made in that same profile. Unlike the
// cookie paths it sets no user-agent, timezone or stealth override: a replayed
// session is rejected by sites such as LinkedIn, an in-profile one is not
// (issue #775, rule 7). It goes through the browser-tier limits, and every
// main-frame hop is checked, so the profile never leaves its own site or
// reaches a private address. The caller repacks spec.Dir afterwards.
func (p *Pipeline) scrapeWithProfile(ctx context.Context, rawURL string, maxLength int, spec ProfileSpec) (*ScrapeResult, error) {
	if err := validateScrapeURL(rawURL); err != nil {
		return nil, validationError(rawURL, "", err, err.Error())
	}
	if !p.isDomainAllowed(rawURL) {
		return nil, validationError(rawURL, "", nil, "access blocked: domain not in allowed list")
	}
	if !onProfileSite(rawURL, spec.Domain) {
		return nil, validationError(rawURL, "browser", nil, "the saved sign-in is for "+spec.Domain+" only")
	}
	if err := validateURLHost(ctx, rawURL, p.config.AllowPrivateIPs); err != nil {
		return nil, validationError(rawURL, "browser", err, "profile navigation blocked: "+err.Error())
	}
	if p.config.ChromePath == chromeDisabled {
		return nil, browserError(rawURL, nil, "browser tier disabled (CHROME_PATH=disabled)")
	}

	release, err := p.acquireTier(ctx, "browser")
	if err != nil {
		return nil, err
	}
	defer release()

	ctx, cancel := context.WithTimeout(ctx, profileScrapeTimeout)
	defer cancel()

	bin := spec.Browser
	if bin != "" {
		if _, err := os.Stat(bin); err != nil {
			bin = ""
		}
	}
	if bin == "" {
		bin = cookievault.ResolveBrowser(p.config.ChromePath)
	}
	pb, err := cookievault.LaunchBrowser(cookievault.BrowserLaunch{Bin: bin, Dir: spec.Dir, Headless: true, NoSandbox: p.config.ChromeNoSandbox})
	if err != nil {
		return nil, browserError(rawURL, err, "could not start the browser for the saved sign-in")
	}
	// Shutdown waits for the browser to exit, so the repacked profile is complete.
	defer pb.Shutdown()

	page, err := pb.Browser.Context(ctx).Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return nil, browserError(rawURL, err, "could not open a page")
	}
	defer func() { _ = page.Close() }()

	// Every request is checked. Pages (documents) must stay on the profile's
	// site and resolve to public addresses. Other requests (scripts, images,
	// XHR) may go to any site that resolves to a public address, so the signed-in
	// page cannot reach internal hosts.
	var offSite atomic.Pointer[string]
	var hostVerdicts sync.Map // host -> public (bool)
	public := func(host string) bool {
		if host == "" {
			return true
		}
		if v, ok := hostVerdicts.Load(host); ok {
			return v.(bool)
		}
		ok := ValidateHost(ctx, host, p.config.AllowPrivateIPs) == nil
		hostVerdicts.Store(host, ok)
		return ok
	}
	router := page.HijackRequests()
	if err := router.Add("*", "", func(h *rod.Hijack) {
		u := h.Request.URL()
		if u == nil {
			h.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		}
		host := u.Hostname()
		isDoc := h.Request.Type() == proto.NetworkResourceTypeDocument
		if blockProfileRequest(isDoc, u.String(), host, spec.Domain, public) {
			if isDoc && !onProfileSite(u.String(), spec.Domain) {
				offSite.CompareAndSwap(nil, &host)
			}
			h.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
			return
		}
		h.ContinueRequest(&proto.FetchContinueRequest{})
	}); err != nil {
		return nil, browserError(rawURL, err, "could not guard navigation")
	}
	go router.Run()
	defer func() { _ = router.Stop() }()

	var mainStatus int
	var gotStatus bool
	waitNav := page.EachEvent(
		func(e *proto.NetworkResponseReceived) bool {
			if e.Type == proto.NetworkResourceTypeDocument && e.FrameID == page.FrameID && e.Response != nil {
				mainStatus, gotStatus = e.Response.Status, true
			}
			return false
		},
		func(e *proto.PageFrameNavigated) bool { return e.Frame.ID == page.FrameID },
	)
	if err := page.Navigate(rawURL); err != nil {
		if host := offSite.Load(); host != nil {
			return nil, offSiteError(rawURL, *host)
		}
		return nil, networkError(rawURL, "browser", fmt.Errorf("navigation failed: %w", err))
	}
	waitNav()
	if host := offSite.Load(); host != nil {
		return nil, offSiteError(rawURL, *host)
	}
	if gotStatus && mainStatus >= 400 {
		return nil, classifyHTTPStatus(mainStatus, rawURL, "browser")
	}

	loadCtx, loadCancel := context.WithTimeout(ctx, 5*time.Second)
	_ = page.Context(loadCtx).WaitLoad()
	loadCancel()

	text, err := stableContent(ctx, page, rawURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" {
		return nil, contentError(rawURL, "the page had no readable text")
	}

	// Eval, not page.Element("title"): Element waits for a missing node until the
	// context ends, which would stall every title-less page for a full minute.
	var title string
	if v, err := page.Context(ctx).Eval(`() => document.title`); err == nil {
		title = strings.TrimSpace(v.Value.Str())
	}

	truncated := false
	if len(text) > maxLength {
		for maxLength > 0 && !utf8.RuneStart(text[maxLength]) {
			maxLength--
		}
		text, truncated = text[:maxLength], true
	}
	res := &ScrapeResult{URL: rawURL, Content: text, ContentType: "html", Title: title, Truncated: truncated, Tier: "browser"}
	stampSparsity(res)
	return res, nil
}

// stableContent polls the page text until it stops changing for
// profileStableFor, or profileStableCap passes. Feed pages render late.
// Short text is returned too: the caller uses it to spot a login wall.
func stableContent(ctx context.Context, page *rod.Page, rawURL string) (string, error) {
	var last string
	stableSince := time.Now()
	deadline := time.Now().Add(profileStableCap)
	for {
		cur, err := extractPageContent(page.Context(ctx))
		if err != nil {
			if ctx.Err() != nil {
				return last, nil
			}
			return "", browserError(rawURL, err, "could not read the page")
		}
		if cur != last {
			last, stableSince = cur, time.Now()
		}
		if time.Since(stableSince) >= profileStableFor || time.Now().After(deadline) {
			return last, nil
		}
		select {
		case <-ctx.Done():
			return last, nil
		case <-time.After(profilePollEvery):
		}
	}
}

// blockProfileRequest decides whether a request from a profile scrape is
// stopped. A page (document) must be on the profile's domain and public. Any
// other request only has to go to a public host.
func blockProfileRequest(isDocument bool, rawURL, host, domain string, public func(host string) bool) bool {
	if isDocument && !onProfileSite(rawURL, domain) {
		return true
	}
	return !public(host)
}

// offSiteError is the permanent rejection for a page that left the profile's site.
func offSiteError(rawURL, host string) *ScrapeError {
	return newScrapeError(ErrValidation, rawURL, "browser", ErrProfileOffSite, "the page went to "+host)
}

// onProfileSite reports whether rawURL is on domain or one of its subdomains.
func onProfileSite(rawURL, domain string) bool {
	return domain != "" && hostnameMatches(rawURL, domain)
}
