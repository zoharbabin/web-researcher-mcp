package cookievault

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

// RodDriver is the production Driver: a visible Chromium driven by go-rod.
// Construction launches nothing; the browser starts in Open.
type RodDriver struct {
	chromePath string
	headless   bool // tests only; production login must be visible
	noSandbox  bool
}

// NewRodDriver returns a headed Driver. An empty chromePath uses the
// browser already installed on the machine (Chrome first), so the user signs
// in with the browser they know. Only when none is installed does rod download
// its own Chromium.
func NewRodDriver(chromePath string) *RodDriver {
	return &RodDriver{chromePath: chromePath}
}

// WithNoSandbox runs the login and verification browsers without Chrome's
// sandbox, for containers that cannot run it (CHROME_NO_SANDBOX).
func (d *RodDriver) WithNoSandbox(v bool) *RodDriver {
	d.noSandbox = v
	return d
}

// BrowserBin returns the browser binary this driver launches ("" when go-rod
// will download one).
func (d *RodDriver) BrowserBin() string { return ResolveBrowser(d.chromePath) }

func (d *RodDriver) launch(profileDir string, headless bool) BrowserLaunch {
	return BrowserLaunch{Bin: d.BrowserBin(), Dir: profileDir, Headless: headless, NoSandbox: d.noSandbox || d.headless}
}

// Open launches Chromium on profileDir and navigates to loginURL.
func (d *RodDriver) Open(ctx context.Context, loginURL, profileDir string) (Session, error) {
	pb, err := LaunchBrowser(d.launch(profileDir, d.headless))
	if err != nil {
		return nil, err
	}
	s := &rodSession{pb: pb, done: make(chan struct{})}
	if _, err := pb.Browser.Context(ctx).Page(proto.TargetCreateTarget{URL: loginURL}); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("open login page: %w", err)
	}
	go s.watch()
	return s, nil
}

// ReadCookies opens profileDir headless, returns every cookie it holds, and
// shuts the browser down cleanly. It shows what a later headless scrape sees.
func (d *RodDriver) ReadCookies(ctx context.Context, profileDir string) ([]Cookie, error) {
	pb, err := LaunchBrowser(d.launch(profileDir, true))
	if err != nil {
		return nil, err
	}
	defer pb.Shutdown()
	raw, err := pb.Browser.Context(ctx).GetCookies()
	if err != nil {
		return nil, err
	}
	return fromRodCookies(raw), nil
}

type rodSession struct {
	pb   *ProfileBrowser
	done chan struct{}
	once sync.Once
}

// watch closes done when the last window is gone or Chromium has exited.
func (s *rodSession) watch() {
	t := time.NewTicker(300 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		pages, err := s.pb.Browser.Pages()
		if err != nil || len(pages) == 0 {
			s.finish()
			return
		}
	}
}

func (s *rodSession) finish() { s.once.Do(func() { close(s.done) }) }

func (s *rodSession) Done() <-chan struct{} { return s.done }

func (s *rodSession) Cookies(ctx context.Context) ([]Cookie, error) {
	raw, err := s.pb.Browser.Context(ctx).GetCookies()
	if err != nil {
		return nil, err
	}
	return fromRodCookies(raw), nil
}

// pageTextMax caps what PageText reads. Block pages are short.
const pageTextMax = 4096

// PageText returns the first pageTextMax characters of the first page's text.
func (s *rodSession) PageText(ctx context.Context) (string, error) {
	pages, err := s.pb.Browser.Context(ctx).Pages()
	if err != nil || len(pages) == 0 {
		return "", err
	}
	res, err := pages[0].Eval(fmt.Sprintf(`() => (document.body ? document.body.innerText : "").slice(0, %d)`, pageTextMax))
	if err != nil {
		return "", err
	}
	return res.Value.Str(), nil
}

// Close shuts the browser down cleanly and waits for it to exit, so the profile
// is fully written. It leaves the profile dir to the caller.
func (s *rodSession) Close() error {
	s.finish()
	s.pb.Shutdown()
	return nil
}
