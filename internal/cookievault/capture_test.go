package cookievault

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// persistCookie is a cookie a browser writes to disk (it has an expiry).
func persistCookie(name, value, domain string) Cookie {
	c := sessionCookie(name, value, domain)
	c.Expires = time.Now().Add(24 * time.Hour)
	return c
}

type fakeSession struct {
	cookies  []Cookie
	cookErr  error
	text     string // what PageText returns
	done     chan struct{}
	closed   atomic.Int32
	doneOnce sync.Once
}

func newFakeSession(cookies ...Cookie) *fakeSession {
	return &fakeSession{cookies: cookies, done: make(chan struct{})}
}

func (s *fakeSession) Cookies(context.Context) ([]Cookie, error) { return s.cookies, s.cookErr }
func (s *fakeSession) PageText(context.Context) (string, error)  { return s.text, nil }
func (s *fakeSession) Done() <-chan struct{}                     { return s.done }
func (s *fakeSession) Close() error                              { s.closed.Add(1); return nil }
func (s *fakeSession) userClosesWindow()                         { s.doneOnce.Do(func() { close(s.done) }) }

type fakeDriver struct {
	mu     sync.Mutex
	opens  int
	reads  int
	dirs   []string
	url    string
	err    error
	sess   *fakeSession
	opened chan struct{}
	onOpen func(dir string)

	// onDisk is what ReadCookies returns; nil means "what the session held".
	onDisk  []Cookie
	readErr error
	// closedAtRead records whether the session was closed when ReadCookies ran.
	closedAtRead bool
	bin          string
}

func (d *fakeDriver) Open(_ context.Context, loginURL, dir string) (Session, error) {
	d.mu.Lock()
	d.opens++
	d.dirs = append(d.dirs, dir)
	d.url = loginURL
	d.mu.Unlock()
	if d.onOpen != nil {
		d.onOpen(dir)
	}
	if d.err != nil {
		return nil, d.err
	}
	if d.opened != nil {
		close(d.opened)
	}
	return d.sess, nil
}

func (d *fakeDriver) ReadCookies(context.Context, string) ([]Cookie, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reads++
	d.closedAtRead = d.sess != nil && d.sess.closed.Load() > 0
	if d.readErr != nil {
		return nil, d.readErr
	}
	if d.onDisk != nil {
		return d.onDisk, nil
	}
	if d.sess != nil {
		return d.sess.cookies, nil
	}
	return nil, nil
}

func (d *fakeDriver) BrowserBin() string {
	if d.bin != "" {
		return d.bin
	}
	return "/usr/bin/fake-chrome"
}

func (d *fakeDriver) lastDir() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dirs[len(d.dirs)-1]
}

func fastCapturer(t *testing.T, d Driver, slots int, timeout time.Duration) *Capturer {
	t.Helper()
	c := NewCapturer(d, nil, slots, timeout).WithProfiles(newTestProfileStore(t))
	c.poll = 5 * time.Millisecond
	c.baseline = 0 // most tests sign in at once; see TestLoginWindowNeedsNewCookie
	return c
}

func workDirEntries(t *testing.T, c *Capturer) int {
	t.Helper()
	entries, err := os.ReadDir(c.profiles.workDir)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func TestLoginWindowSavesVerifiedProfile(t *testing.T) {
	sess := newFakeSession(
		persistCookie("sid", "v", ".example.com"),
		persistCookie("trk", "x", ".tracker.net"),
	)
	d := &fakeDriver{sess: sess, bin: "/opt/chrome"}
	d.onOpen = func(dir string) { _ = os.WriteFile(dir+"/Cookies", []byte("signed-in"), 0o600) }
	c := fastCapturer(t, d, 1, time.Second)
	go func() { time.Sleep(30 * time.Millisecond); sess.userClosesWindow() }()

	sum, err := c.LoginWindow(context.Background(), subjA, "https://www.example.com/login")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Domain != "example.com" {
		t.Errorf("domain = %q", sum.Domain)
	}
	if !d.closedAtRead {
		t.Error("headless check ran before the login browser was closed")
	}
	l, err := c.profiles.Acquire(context.Background(), subjA, "example.com")
	if err != nil {
		t.Fatalf("saved profile not found: %v", err)
	}
	defer l.Discard()
	if l.Meta.Browser != "/opt/chrome" {
		t.Errorf("meta browser = %q", l.Meta.Browser)
	}
	b, err := os.ReadFile(l.Dir + "/Cookies")
	if err != nil || string(b) != "signed-in" {
		t.Errorf("profile content = %q, %v", b, err)
	}
	if _, err := c.profiles.Acquire(context.Background(), subjB, "example.com"); !errors.Is(err, ErrNoProfile) {
		t.Errorf("other tenant sees the profile: %v", err)
	}
}

// TestProfileLoginVerifiesPersistedCookies: the headless check must see every
// persistent cookie, else nothing is stored.
func TestProfileLoginVerifiesPersistedCookies(t *testing.T) {
	sess := newFakeSession(persistCookie("sid", "v", "example.com"), persistCookie("li", "w", "example.com"))
	sess.userClosesWindow()
	d := &fakeDriver{sess: sess, onDisk: []Cookie{persistCookie("sid", "v", "example.com")}}
	c := fastCapturer(t, d, 1, time.Second)

	if _, err := c.LoginWindow(context.Background(), subjA, "https://example.com/login"); !errors.Is(err, ErrProfileNotSaved) {
		t.Fatalf("err = %v, want ErrProfileNotSaved", err)
	}
	if got := c.profiles.List(context.Background(), subjA); len(got) != 0 {
		t.Errorf("profile stored despite failed verification: %+v", got)
	}
	if n := workDirEntries(t, c); n != 0 {
		t.Errorf("%d work dirs left", n)
	}
}

func TestLoginWindowSessionOnlyCookiesAreNotSaved(t *testing.T) {
	sess := newFakeSession(sessionCookie("sid", "v", "example.com"))
	sess.userClosesWindow()
	d := &fakeDriver{sess: sess}
	c := fastCapturer(t, d, 1, time.Second)
	if _, err := c.LoginWindow(context.Background(), subjA, "https://example.com/login"); !errors.Is(err, ErrProfileNotSaved) {
		t.Fatalf("err = %v, want ErrProfileNotSaved", err)
	}
	if d.reads != 0 {
		t.Error("headless check ran for a session-only sign-in")
	}
}

func TestLoginWindowUnavailableWithoutStore(t *testing.T) {
	c := NewCapturer(&fakeDriver{sess: newFakeSession()}, nil, 1, time.Second)
	if _, err := c.LoginWindow(context.Background(), subjA, "https://example.com/login"); !errors.Is(err, ErrProfilesUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestCaptureProfileDirRemovedOnEveryPath(t *testing.T) {
	boom := errors.New("boom")
	type env struct {
		d      *fakeDriver
		c      *Capturer
		ctx    context.Context
		cancel context.CancelFunc
	}
	mk := func(t *testing.T, d *fakeDriver, timeout time.Duration, ctx context.Context, cancel context.CancelFunc) env {
		return env{d, fastCapturer(t, d, 1, timeout), ctx, cancel}
	}
	closed := func(cookies ...Cookie) *fakeSession {
		s := newFakeSession(cookies...)
		s.userClosesWindow()
		return s
	}
	cases := map[string]func(t *testing.T) env{
		"success": func(t *testing.T) env {
			ctx, cancel := context.WithCancel(context.Background())
			return mk(t, &fakeDriver{sess: closed(persistCookie("sid", "v", "example.com"))}, time.Second, ctx, cancel)
		},
		"open error": func(t *testing.T) env {
			ctx, cancel := context.WithCancel(context.Background())
			return mk(t, &fakeDriver{err: boom}, time.Second, ctx, cancel)
		},
		"timeout": func(t *testing.T) env {
			ctx, cancel := context.WithCancel(context.Background())
			return mk(t, &fakeDriver{sess: newFakeSession()}, 30*time.Millisecond, ctx, cancel)
		},
		"cancel": func(t *testing.T) env {
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(400*time.Millisecond, cancel)
			return mk(t, &fakeDriver{sess: newFakeSession()}, 10*time.Second, ctx, cancel)
		},
		"nothing captured": func(t *testing.T) env {
			ctx, cancel := context.WithCancel(context.Background())
			return mk(t, &fakeDriver{sess: closed()}, time.Second, ctx, cancel)
		},
		"not saved": func(t *testing.T) env {
			ctx, cancel := context.WithCancel(context.Background())
			return mk(t, &fakeDriver{sess: closed(persistCookie("sid", "v", "example.com")), onDisk: []Cookie{}}, time.Second, ctx, cancel)
		},
		"headless check fails": func(t *testing.T) env {
			ctx, cancel := context.WithCancel(context.Background())
			return mk(t, &fakeDriver{sess: closed(persistCookie("sid", "v", "example.com")), readErr: boom}, time.Second, ctx, cancel)
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			e := build(t)
			defer e.cancel()
			var existedDuring bool
			e.d.onOpen = func(dir string) {
				st, err := os.Stat(dir)
				existedDuring = err == nil && st.IsDir()
				_ = os.WriteFile(dir+"/Cookies", []byte("x"), 0o600)
			}
			_, _ = e.c.LoginWindow(e.ctx, subjA, "https://example.com/login")
			if !existedDuring {
				t.Fatal("profile dir did not exist while the browser was open")
			}
			if _, err := os.Stat(e.d.lastDir()); !os.IsNotExist(err) {
				t.Errorf("profile dir %s still exists (err=%v)", e.d.lastDir(), err)
			}
			if len(e.c.slots) != 0 {
				t.Error("slot leaked")
			}
			if e.d.sess != nil && e.d.sess.closed.Load() == 0 {
				t.Error("login browser not closed")
			}
		})
	}
	t.Run("panic in driver", func(t *testing.T) {
		d := &fakeDriver{sess: newFakeSession()}
		d.onOpen = func(string) { panic("driver crashed") }
		c := fastCapturer(t, d, 1, time.Second)
		func() {
			defer func() { _ = recover() }()
			_, _ = c.LoginWindow(context.Background(), subjA, "https://example.com/login")
		}()
		if _, err := os.Stat(d.lastDir()); !os.IsNotExist(err) {
			t.Errorf("profile dir survived a panic (err=%v)", err)
		}
		if len(c.slots) != 0 {
			t.Error("slot leaked after panic")
		}
		// The profile lock is released too: a new login can start.
		l, err := c.profiles.Begin(context.Background(), subjA, "example.com")
		if err != nil {
			t.Fatalf("profile lock leaked after panic: %v", err)
		}
		l.Discard()
	})
}

func TestCaptureSemaphoreBound(t *testing.T) {
	s := newFakeSession(persistCookie("sid", "v", "example.com"))
	d := &fakeDriver{sess: s, opened: make(chan struct{})}
	c := fastCapturer(t, d, 1, 5*time.Second)
	errCh := make(chan error, 1)
	go func() {
		_, err := c.LoginWindow(context.Background(), subjA, "https://example.com/login")
		errCh <- err
	}()
	<-d.opened
	if _, err := c.LoginWindow(context.Background(), subjB, "https://example.com/login"); !errors.Is(err, ErrCaptureBusy) {
		t.Fatalf("second capture err = %v, want ErrCaptureBusy", err)
	}
	d.mu.Lock()
	opens := d.opens
	d.mu.Unlock()
	if opens != 1 {
		t.Errorf("driver opened %d times, want 1", opens)
	}
	s.userClosesWindow()
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	// Slot is free again.
	s2 := newFakeSession(persistCookie("sid", "v", "example.com"))
	s2.userClosesWindow()
	d.sess, d.opened = s2, nil
	if _, err := c.LoginWindow(context.Background(), subjA, "https://example.com/login"); err != nil {
		t.Errorf("capture after release: %v", err)
	}
}

func TestCaptureTimeoutAndCancel(t *testing.T) {
	d := &fakeDriver{sess: newFakeSession(persistCookie("sid", "v", "example.com"))}
	c := fastCapturer(t, d, 1, 40*time.Millisecond)
	if _, err := c.LoginWindow(context.Background(), subjA, "https://example.com/login"); !errors.Is(err, ErrCaptureTimeout) {
		t.Errorf("timeout: %v", err)
	}
	if d.sess.closed.Load() == 0 {
		t.Error("browser not closed on timeout")
	}

	d2 := &fakeDriver{sess: newFakeSession(persistCookie("sid", "v", "example.com"))}
	c2 := fastCapturer(t, d2, 1, 10*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	if _, err := c2.LoginWindow(ctx, subjA, "https://example.com/login"); !errors.Is(err, context.Canceled) {
		t.Errorf("cancel: %v", err)
	}
	if d2.sess.closed.Load() == 0 {
		t.Error("browser not closed on cancel")
	}
	if got := c2.profiles.List(context.Background(), subjA); len(got) != 0 {
		t.Errorf("profile stored after cancel: %+v", got)
	}
}

func TestCaptureRejectsBadURLsBeforeLaunch(t *testing.T) {
	d := &fakeDriver{sess: newFakeSession()}
	c := fastCapturer(t, d, 1, time.Second).WithAttacher(&fakeAttacher{})
	for _, u := range []string{
		"", "not a url", "file:///etc/passwd", "javascript:alert(1)", "ftp://example.com",
		"http://127.0.0.1/login", "http://localhost:8080/", "https://[::1]/", "https://intranet/login",
		"https://user:pass@example.com/", "https://co.uk/",
	} {
		if _, err := c.LoginWindow(context.Background(), subjA, u); !errors.Is(err, ErrInvalidLoginURL) {
			t.Errorf("LoginWindow(%q) err = %v, want ErrInvalidLoginURL", u, err)
		}
		if _, _, err := c.CaptureFromBrowser(context.Background(), u); !errors.Is(err, ErrInvalidLoginURL) {
			t.Errorf("CaptureFromBrowser(%q) err = %v, want ErrInvalidLoginURL", u, err)
		}
	}
	// SSRF validator runs before launch and its error is surfaced.
	deny := errors.New("blocked")
	c.validate = func(context.Context, string) error { return deny }
	if _, err := c.LoginWindow(context.Background(), subjA, "https://example.com/"); !errors.Is(err, deny) {
		t.Errorf("validator err = %v", err)
	}
	if d.opens != 0 {
		t.Errorf("browser opened %d times for rejected URLs", d.opens)
	}
}

func TestConstructionLaunchesNoBrowser(t *testing.T) {
	d := &fakeDriver{sess: newFakeSession()}
	_ = NewCapturer(d, nil, 2, time.Second)
	if d.opens != 0 || d.reads != 0 {
		t.Error("NewCapturer opened a browser")
	}
	rd := NewRodDriver("/nonexistent/chrome")
	if rd == nil {
		t.Fatal("nil driver")
	}
	if rd.headless {
		t.Error("production driver must be headed")
	}
}

func TestMultiInstanceCapturer(t *testing.T) {
	s1 := newFakeSession(persistCookie("sid", "one", "example.com"))
	d1 := &fakeDriver{sess: s1, opened: make(chan struct{})}
	c1 := fastCapturer(t, d1, 1, 5*time.Second)

	s2 := newFakeSession(persistCookie("sid", "two", "example.com"))
	s2.userClosesWindow()
	d2 := &fakeDriver{sess: s2}
	c2 := fastCapturer(t, d2, 1, 5*time.Second)

	res := make(chan error, 1)
	go func() {
		_, err := c1.LoginWindow(context.Background(), subjA, "https://example.com/login")
		res <- err
	}()
	<-d1.opened
	// c1 is busy; c2 must be unaffected and have its own store.
	if _, err := c2.LoginWindow(context.Background(), subjA, "https://example.com/login"); err != nil {
		t.Fatalf("c2: %v", err)
	}
	if got := c1.profiles.List(context.Background(), subjA); len(got) != 0 {
		t.Errorf("c1 store sees c2's profile: %+v", got)
	}
	s1.userClosesWindow()
	if err := <-res; err != nil {
		t.Errorf("c1: %v", err)
	}
	if d1.dirs[0] == d2.dirs[0] {
		t.Error("instances share a profile dir")
	}
}

// sessionWithCookies lets a test change what the window holds while it runs.
type mutableSession struct {
	*fakeSession
	mu sync.Mutex
}

func (m *mutableSession) Cookies(ctx context.Context) ([]Cookie, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fakeSession.Cookies(ctx)
}

func (m *mutableSession) set(c ...Cookie) {
	m.mu.Lock()
	m.cookies = c
	m.mu.Unlock()
}

func TestLoginWindowNeedsNewCookie(t *testing.T) {
	tracker := persistCookie("_ga", "x", ".example.com")
	run := func(t *testing.T, signIn bool) error {
		t.Helper()
		sess := &mutableSession{fakeSession: newFakeSession(tracker)}
		// The driver hands out the mutable view so Cookies sees later changes.
		d := &fakeDriver{sess: sess.fakeSession}
		c := fastCapturer(t, &mutableDriver{fakeDriver: d, sess: sess}, 1, 5*time.Second)
		c.baseline = 200 * time.Millisecond
		go func() {
			time.Sleep(400 * time.Millisecond)
			if signIn {
				sess.set(tracker, persistCookie("li_at", "secret", ".example.com"))
				time.Sleep(50 * time.Millisecond)
			}
			sess.userClosesWindow()
		}()
		_, err := c.LoginWindow(context.Background(), subjA, "https://www.example.com/login")
		return err
	}
	if err := run(t, false); !errors.Is(err, ErrNoSignInSeen) {
		t.Fatalf("closing without signing in: got %v, want ErrNoSignInSeen", err)
	}
	if err := run(t, true); err != nil {
		t.Fatalf("signing in: %v", err)
	}
}

func TestLoginWindowSiteBlocked(t *testing.T) {
	const blockPage = "Access is temporarily restricted\nWe detected unusual activity from your device or network."
	run := func(t *testing.T, text string, cookies ...Cookie) error {
		t.Helper()
		sess := newFakeSession(cookies...)
		sess.text = text
		c := fastCapturer(t, &fakeDriver{sess: sess}, 1, 5*time.Second)
		c.baseline = time.Minute // cookies present at open are not a sign-in
		go func() {
			time.Sleep(50 * time.Millisecond)
			sess.userClosesWindow()
		}()
		_, err := c.LoginWindow(context.Background(), subjA, "https://www.example.com/login")
		return err
	}
	tracker := persistCookie("_ga", "x", ".example.com")
	tests := []struct {
		name    string
		text    string
		cookies []Cookie
		want    error
	}{
		{"block page, no cookies", blockPage, nil, ErrSiteBlocked},
		{"block page, tracking cookie only", blockPage, []Cookie{tracker}, ErrSiteBlocked},
		{"normal page, tracking cookie only", "Sign in to your account", []Cookie{tracker}, ErrNoSignInSeen},
		{"normal page, no cookies", "Sign in to your account", nil, ErrNothingCaptured},
		{"normal page, no text", "", nil, ErrNothingCaptured},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := run(t, tt.text, tt.cookies...); !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
	t.Run("signed in despite block-page text", func(t *testing.T) {
		sess := newFakeSession(tracker)
		sess.text = blockPage
		ms := &mutableSession{fakeSession: sess}
		c := fastCapturer(t, &mutableDriver{fakeDriver: &fakeDriver{sess: sess}, sess: ms}, 1, 5*time.Second)
		c.baseline = 100 * time.Millisecond
		go func() {
			time.Sleep(250 * time.Millisecond)
			ms.set(tracker, persistCookie("li_at", "s", ".example.com"))
			time.Sleep(50 * time.Millisecond)
			sess.userClosesWindow()
		}()
		if _, err := c.LoginWindow(context.Background(), subjA, "https://www.example.com/login"); err != nil {
			t.Fatalf("a sign-in must win over page text: %v", err)
		}
	})
}

// mutableDriver returns the mutable session from Open.
type mutableDriver struct {
	*fakeDriver
	sess *mutableSession
}

func (m *mutableDriver) Open(ctx context.Context, loginURL, dir string) (Session, error) {
	if _, err := m.fakeDriver.Open(ctx, loginURL, dir); err != nil {
		return nil, err
	}
	return m.sess, nil
}

func (m *mutableDriver) ReadCookies(ctx context.Context, dir string) ([]Cookie, error) {
	return m.sess.Cookies(ctx)
}
