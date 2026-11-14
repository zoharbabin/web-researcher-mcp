package cookievault

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/content"
)

// Errors returned by Capturer.
var (
	ErrCaptureBusy     = errors.New("cookievault: all login-capture slots are in use")
	ErrCaptureTimeout  = errors.New("cookievault: login window was not closed before the timeout")
	ErrInvalidLoginURL = errors.New("cookievault: login URL must be http(s) with a registrable domain")
	ErrNothingCaptured = errors.New("cookievault: no cookies were set for this domain")
	// ErrNotSignedIn: the user's browser was reached but holds no cookies for the site.
	ErrNotSignedIn = errors.New("cookievault: the browser holds no cookies for this site")
	// ErrNoSignInSeen: the window closed with no new persistent cookie since it
	// opened, so the user did not sign in (only the site's tracking cookies are
	// there). Nothing is stored.
	ErrNoSignInSeen = errors.New("cookievault: no sign-in was seen in the login window")
	// ErrSiteBlocked: the site showed a bot-block page in the window and the
	// user never signed in. Retrying the window will not help. Nothing is stored.
	ErrSiteBlocked = errors.New("cookievault: the site blocked the login window")
	// ErrProfileNotSaved: the browser did not write the sign-in to disk, so a
	// later headless scrape would not be signed in. Nothing is stored.
	ErrProfileNotSaved = errors.New("cookievault: the browser did not save the sign-in")
	// ErrProfilesUnavailable: no profile store is configured.
	ErrProfilesUnavailable = errors.New("cookievault: saved browser profiles are not available")
	// ErrLoginURLRejected wraps the HostValidator error when the login host is
	// not allowed (e.g. a private address).
	ErrLoginURLRejected = errors.New("cookievault: login URL host is not allowed")
)

const capturePollInterval = 500 * time.Millisecond

// signInBaselineWait is how long after the window opens its cookies count as
// "before sign-in". A person cannot sign in faster; whatever the site sets in
// this time (page-load and tracking cookies) is not proof of a login.
const signInBaselineWait = 3 * time.Second

// Source says where captured cookies came from.
type Source string

// Capture sources.
const (
	// SourceBrowser: read from the user's own running browser via an Attacher.
	SourceBrowser Source = "browser"
	// SourceLoginWindow: the user signed in inside a fresh login window.
	SourceLoginWindow Source = "login_window"
)

// Driver opens a visible browser for a human to log in. It never launches a
// browser until Open or ReadCookies is called.
type Driver interface {
	// Open starts a browser with profileDir as its profile and navigates to loginURL.
	Open(ctx context.Context, loginURL, profileDir string) (Session, error)
	// ReadCookies starts a headless browser on profileDir, returns the cookies
	// it holds and shuts it down. It proves what a later scrape will see.
	ReadCookies(ctx context.Context, profileDir string) ([]Cookie, error)
	// BrowserBin is the browser binary Open and ReadCookies use.
	BrowserBin() string
}

// Session is one open login browser.
type Session interface {
	// Cookies returns every cookie currently held by the browser.
	Cookies(ctx context.Context) ([]Cookie, error)
	// PageText returns the first few KB of visible text on the window's first
	// page (its frame URLs when it has no text), or "" when there is no page.
	// The caller only matches it against block-page markers and never stores it.
	PageText(ctx context.Context) (string, error)
	// Done is closed when the user closes the browser.
	Done() <-chan struct{}
	// Close shuts the browser down and waits until it has exited and written
	// its profile. It is safe to call more than once.
	Close() error
}

// HostValidator rejects hosts that must not be contacted (SSRF guard).
type HostValidator func(ctx context.Context, host string) error

// Capturer runs interactive login sessions. A session ends when the user closes
// the browser window. It holds no global state: concurrency, timeout and driver
// are per instance.
type Capturer struct {
	driver   Driver
	validate HostValidator
	slots    chan struct{}
	timeout  time.Duration
	poll     time.Duration
	baseline time.Duration
	attacher Attacher
	profiles *ProfileStore
}

// WithSignInBaseline sets how long after a login window opens its cookies count
// as "before sign-in" (default 3 s). Zero turns the new-cookie check into
// "any persistent cookie", for tests whose fake window signs in at once.
func (c *Capturer) WithSignInBaseline(d time.Duration) *Capturer {
	c.baseline = d
	return c
}

// WithProfiles sets where LoginWindow saves the browser profile. Without it
// LoginWindow reports ErrProfilesUnavailable.
func (c *Capturer) WithProfiles(p *ProfileStore) *Capturer {
	c.profiles = p
	return c
}

// WithAttacher sets how CaptureFromBrowser reads cookies from the user's own
// running browser. A nil attacher makes that path report ErrAttachUnavailable.
func (c *Capturer) WithAttacher(a Attacher) *Capturer {
	c.attacher = a
	return c
}

// CanAttach reports whether site_login can read the user's own running browser.
// It is false without an Attacher (HTTP mode), where only the login window works.
func (c *Capturer) CanAttach() bool { return c.attacher != nil }

// NewCapturer returns a Capturer allowing at most maxConcurrent sessions, each
// limited to timeout. Construction launches nothing.
func NewCapturer(driver Driver, validate HostValidator, maxConcurrent int, timeout time.Duration) *Capturer {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &Capturer{
		driver:   driver,
		validate: validate,
		slots:    make(chan struct{}, maxConcurrent),
		timeout:  timeout,
		poll:     capturePollInterval,
		baseline: signInBaselineWait,
	}
}

// checkLoginURL parses loginURL and applies the host guard. It returns the URL
// and its registrable domain.
func (c *Capturer) checkLoginURL(ctx context.Context, loginURL string) (*url.URL, string, error) {
	u, err := url.Parse(loginURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, "", ErrInvalidLoginURL
	}
	domain := RegistrableDomain(u.Hostname())
	if domain == "" {
		return nil, "", ErrInvalidLoginURL
	}
	if c.validate != nil {
		if err := c.validate(ctx, u.Hostname()); err != nil {
			return nil, "", fmt.Errorf("%w: %w", ErrLoginURLRejected, err)
		}
	}
	return u, domain, nil
}

// takeSlot reserves one capture slot, or fails fast with ErrCaptureBusy.
func (c *Capturer) takeSlot() (release func(), err error) {
	select {
	case c.slots <- struct{}{}:
		return func() { <-c.slots }, nil
	default:
		return nil, ErrCaptureBusy
	}
}

// CaptureFromBrowser returns the registrable domain of loginURL and its cookies,
// read from the user's own signed-in browser through the Attacher. It never opens
// a window: ErrAttachUnavailable or ErrNotSignedIn tell the caller what to fix.
// Nothing is stored here.
func (c *Capturer) CaptureFromBrowser(ctx context.Context, loginURL string) (string, []Cookie, error) {
	_, domain, err := c.checkLoginURL(ctx, loginURL)
	if err != nil {
		return "", nil, err
	}
	release, err := c.takeSlot()
	if err != nil {
		return "", nil, err
	}
	defer release()
	if c.attacher == nil {
		return "", nil, ErrAttachUnavailable
	}
	cookies, err := c.attacher.Attach(ctx, domain)
	if ctx.Err() != nil {
		return "", nil, ctx.Err()
	}
	if err != nil {
		return "", nil, ErrAttachUnavailable
	}
	if len(cookies) == 0 {
		return "", nil, ErrNotSignedIn
	}
	return domain, cookies, nil
}

// LoginWindow opens loginURL in a visible browser on sub's saved profile for
// that site (or a new one). The user signs in and closes the window; the browser
// is then shut down cleanly, relaunched headless to prove the sign-in reached
// disk, and the profile is stored encrypted. On timeout, cancellation or any
// error nothing is stored and the work dir is removed. Needs WithProfiles.
func (c *Capturer) LoginWindow(ctx context.Context, sub Subject, loginURL string) (ProfileSummary, error) {
	u, domain, err := c.checkLoginURL(ctx, loginURL)
	if err != nil {
		return ProfileSummary{}, err
	}
	if c.profiles == nil {
		return ProfileSummary{}, ErrProfilesUnavailable
	}
	release, err := c.takeSlot()
	if err != nil {
		return ProfileSummary{}, err
	}
	defer release()

	lease, err := c.profiles.Begin(ctx, sub, u.Hostname())
	if err != nil {
		return ProfileSummary{}, err
	}
	defer lease.Discard()

	windowCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	sess, err := c.driver.Open(windowCtx, u.String(), lease.Dir)
	if err != nil {
		return ProfileSummary{}, fmt.Errorf("open login browser: %w", err)
	}
	var closeOnce sync.Once
	closeSession := func() { closeOnce.Do(func() { _ = sess.Close() }) }
	defer closeSession()

	var last []Cookie
	opened := time.Now()
	before := map[cookieID]bool{} // persistent cookies seen before anyone could sign in
	blocked := false              // the last readable page was a bot-block page
	snapshot := func() {
		// An empty reading means no page is left (the user just closed the
		// window), so keep what the last real page said.
		if text, err := sess.PageText(windowCtx); err == nil && text != "" {
			blocked = content.LooksLikeBotWall(text)
		}
		cookies, err := sess.Cookies(windowCtx)
		if err != nil {
			return
		}
		last = cookies
		if time.Since(opened) <= c.baseline {
			for _, ck := range persistentCookies(FilterToDomain(cookies, domain, time.Now())) {
				before[ck.id()] = true
			}
		}
	}
	ticker := time.NewTicker(c.poll)
	defer ticker.Stop()
wait:
	for {
		select {
		case <-sess.Done():
			snapshot()
			break wait
		case <-ticker.C:
			snapshot()
		case <-windowCtx.Done():
			if errors.Is(windowCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
				return ProfileSummary{}, ErrCaptureTimeout
			}
			return ProfileSummary{}, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return ProfileSummary{}, err
	}

	// With no sign-in evidence, a block page explains why better than the
	// generic errors do.
	noSignIn := func(err error) error {
		if blocked {
			return ErrSiteBlocked
		}
		return err
	}
	seen := FilterToDomain(last, domain, time.Now())
	if len(seen) == 0 {
		return ProfileSummary{}, noSignIn(ErrNothingCaptured)
	}
	persistent := persistentCookies(seen)
	if len(persistent) == 0 {
		// A session-only sign-in is never written to the profile on disk.
		return ProfileSummary{}, ErrProfileNotSaved
	}

	// A first login must have added a persistent cookie after the page loaded.
	// Re-signing in over a saved profile may only refresh existing ones.
	if lease.Fresh && !hasNewCookie(persistent, before) {
		return ProfileSummary{}, noSignIn(ErrNoSignInSeen)
	}

	// The window is gone. Finish within a bounded time even if the caller's
	// context ends now; the work dir is removed by the deferred Discard.
	postCtx, postCancel := context.WithTimeout(context.WithoutCancel(ctx), postWindowBudget)
	defer postCancel()
	closeSession() // graceful: waits for the browser to exit and flush
	onDisk, err := c.driver.ReadCookies(postCtx, lease.Dir)
	if err != nil {
		return ProfileSummary{}, fmt.Errorf("verify saved sign-in: %w", err)
	}
	if !containsAll(FilterToDomain(onDisk, domain, time.Now()), persistent) {
		return ProfileSummary{}, ErrProfileNotSaved
	}
	lease.Meta.Browser = c.driver.BrowserBin()
	return lease.Commit(postCtx)
}

// postWindowBudget bounds the clean browser exit, the headless check and the
// archive write after the login window closes.
const postWindowBudget = 90 * time.Second

// persistentCookies returns the cookies a browser writes to disk (those with an
// expiry that has not passed).
func persistentCookies(cookies []Cookie) []Cookie {
	out := make([]Cookie, 0, len(cookies))
	for _, c := range cookies {
		if !c.Expires.IsZero() && c.Expires.After(time.Now().Add(time.Minute)) {
			out = append(out, c)
		}
	}
	return out
}

// containsAll reports whether every cookie in want (by name, domain and path)
// is present in have.
func containsAll(have, want []Cookie) bool {
	got := make(map[cookieID]bool, len(have))
	for _, c := range have {
		got[c.id()] = true
	}
	for _, c := range want {
		if !got[c.id()] {
			return false
		}
	}
	return true
}

// cookieID is a cookie's identity (name, domain, path), without its value.
type cookieID struct{ name, domain, path string }

func (c Cookie) id() cookieID { return cookieID{c.Name, c.Domain, c.Path} }

// hasNewCookie reports whether any cookie in cookies is not in before.
func hasNewCookie(cookies []Cookie, before map[cookieID]bool) bool {
	for _, c := range cookies {
		if !before[c.id()] {
			return true
		}
	}
	return false
}
