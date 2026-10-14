package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/net/dns/dnsmessage"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/cache"
	"github.com/zoharbabin/web-researcher-mcp/internal/consent"
	"github.com/zoharbabin/web-researcher-mcp/internal/content"
	"github.com/zoharbabin/web-researcher-mcp/internal/cookievault"
	"github.com/zoharbabin/web-researcher-mcp/internal/datasubject"
	"github.com/zoharbabin/web-researcher-mcp/internal/metrics"
	"github.com/zoharbabin/web-researcher-mcp/internal/persist"
	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

// Tests for site_login / site_logout and the vault-backed scrape path (issue #241).

const (
	vaultTestDomain = "vaulttest.com"
	vaultTestHost   = "app.vaulttest.com"
	canaryValue     = "CANARY-9f3b7c1e-session-token"
	privateMarker   = "PRIVATE-MEMBER-DASHBOARD"
	publicMarker    = "PUBLIC-LOGIN-WALL"
)

// fakeLoginDriver stands in for the visible browser. The "user" closes the
// window immediately; cookies is what the browser held at that moment.
type fakeLoginDriver struct {
	mu      sync.Mutex
	cookies []cookievault.Cookie
	opens   int
}

func (d *fakeLoginDriver) Open(_ context.Context, _, _ string) (cookievault.Session, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.opens++
	done := make(chan struct{})
	close(done)
	return &fakeLoginSession{cookies: append([]cookievault.Cookie(nil), d.cookies...), done: done}, nil
}

// ReadCookies returns what the browser would hold after a restart: the same
// cookies, as if the site wrote them to disk.
func (d *fakeLoginDriver) ReadCookies(context.Context, string) ([]cookievault.Cookie, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]cookievault.Cookie(nil), d.cookies...), nil
}

func (d *fakeLoginDriver) BrowserBin() string { return "/fake/chrome" }

func (d *fakeLoginDriver) openCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.opens
}

type fakeLoginSession struct {
	cookies []cookievault.Cookie
	done    chan struct{}
}

func (s *fakeLoginSession) Cookies(context.Context) ([]cookievault.Cookie, error) {
	return s.cookies, nil
}
func (s *fakeLoginSession) Done() <-chan struct{} { return s.done }
func (s *fakeLoginSession) Close() error          { return nil }

// fakeDNSNames holds the names useFakeDNS currently maps to 127.0.0.1.
var fakeDNSNames sync.Map

// installFakeDNS replaces the process resolver once, for the whole test binary
// (TestMain), so no test ever writes net.DefaultResolver while other tests'
// dials read it. Names registered through useFakeDNS resolve to 127.0.0.1;
// every other name goes to the original resolver. Returns a stop func.
func installFakeDNS() func() {
	pc, err := (&net.ListenConfig{}).ListenPacket(context.Background(), "udp", "127.0.0.1:0")
	if err != nil {
		return func() {}
	}
	orig := net.DefaultResolver
	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var p dnsmessage.Parser
			hdr, err := p.Start(buf[:n])
			if err != nil {
				continue
			}
			q, err := p.Question()
			if err != nil {
				continue
			}
			name := strings.ToLower(q.Name.String())
			_, fake := fakeDNSNames.Load(name)
			var ips []net.IP
			if fake {
				ips = []net.IP{net.IPv4(127, 0, 0, 1)}
			} else if addrs, lerr := orig.LookupIPAddr(context.Background(), strings.TrimSuffix(name, ".")); lerr == nil {
				for _, a := range addrs {
					ips = append(ips, a.IP)
				}
			}
			rh := dnsmessage.Header{ID: hdr.ID, Response: true, Authoritative: true, RecursionAvailable: true}
			if len(ips) == 0 && !fake {
				rh.RCode = dnsmessage.RCodeNameError
			}
			b := dnsmessage.NewBuilder(nil, rh)
			_ = b.StartQuestions()
			_ = b.Question(q)
			_ = b.StartAnswers()
			for _, ip := range ips {
				if v4 := ip.To4(); v4 != nil && q.Type == dnsmessage.TypeA {
					_ = b.AResource(dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 1}, dnsmessage.AResource{A: [4]byte(v4)})
				}
			}
			out, err := b.Finish()
			if err != nil {
				continue
			}
			_, _ = pc.WriteTo(out, addr)
		}
	}()
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "udp", pc.LocalAddr().String())
		},
	}
	return func() { _ = pc.Close() }
}

// useFakeDNS makes every name in names resolve to 127.0.0.1 for this test, so
// tests can use real registrable domains (an IP or localhost has none) without
// network access. Needs installFakeDNS from TestMain.
func useFakeDNS(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		key := strings.ToLower(n) + "."
		fakeDNSNames.Store(key, struct{}{})
		t.Cleanup(func() { fakeDNSNames.Delete(key) })
	}
}

// loginSite is a login-gated origin: pages carry privateMarker only when the
// request has the canary session cookie.
type loginSite struct {
	*httptest.Server
	mu      sync.Mutex
	cookies []string
}

func newLoginSite(t *testing.T) *loginSite {
	t.Helper()
	ls := &loginSite{}
	ls.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ls.mu.Lock()
		ls.cookies = append(ls.cookies, r.Header.Get("Cookie"))
		ls.mu.Unlock()
		marker := publicMarker
		if c, err := r.Cookie("sid"); err == nil && c.Value == canaryValue {
			marker = privateMarker
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><head><title>Dash</title></head><body><article><p>" + marker + " " +
			strings.Repeat("Enough body text for the HTML tier to accept this page as real content. ", 6) +
			"</p></article></body></html>"))
	}))
	t.Cleanup(ls.Close)
	return ls
}

func (ls *loginSite) pageURL() string {
	_, port, _ := net.SplitHostPort(ls.Listener.Addr().String())
	return "http://" + vaultTestHost + ":" + port + "/dash"
}

func (ls *loginSite) requestCount() int {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return len(ls.cookies)
}

func (ls *loginSite) sawCookie() bool {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	for _, c := range ls.cookies {
		if strings.Contains(c, "sid=") {
			return true
		}
	}
	return false
}

// mapCache is a real, inspectable cache so tests can prove nothing was stored.
type mapCache struct {
	mu   sync.Mutex
	data map[string][]byte
	sets int
}

func newMapCache() *mapCache { return &mapCache{data: map[string][]byte{}} }

func (c *mapCache) Get(_ context.Context, k string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.data[k]
	return v, ok
}

func (c *mapCache) GetWithMeta(ctx context.Context, k string) ([]byte, *cache.EntryMeta, bool) {
	v, ok := c.Get(ctx, k)
	if !ok {
		return nil, nil, false
	}
	return v, &cache.EntryMeta{StoredAt: time.Now(), TTL: time.Hour}, true
}

func (c *mapCache) Set(_ context.Context, k string, v []byte, _ time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[k] = v
	c.sets++
}

func (c *mapCache) Delete(_ context.Context, k string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, k)
}
func (c *mapCache) Flush()       {}
func (c *mapCache) Close() error { return nil }

func (c *mapCache) contains(s string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.data {
		if strings.Contains(k, s) || bytes.Contains(v, []byte(s)) {
			return true
		}
	}
	return false
}

func (c *mapCache) setCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sets
}

// vaultEnv is a full deps wiring for the vault tests.
type vaultEnv struct {
	deps    Dependencies
	site    *loginSite
	cache   *mapCache
	driver  *fakeLoginDriver
	consent *consent.StoreManager
	auditor *capturingAuditor
	logs    *bytes.Buffer
	profDir string
}

func canaryCookies() []cookievault.Cookie {
	return []cookievault.Cookie{{Name: "sid", Value: canaryValue, Domain: "." + vaultTestDomain, Path: "/", HTTPOnly: true}}
}

// persistentCanaryCookies is canaryCookies with an expiry, as a site writes a
// "remember me" sign-in: only these reach the browser profile on disk.
func persistentCanaryCookies() []cookievault.Cookie {
	cs := canaryCookies()
	for i := range cs {
		cs[i].Expires = time.Now().Add(24 * time.Hour)
	}
	return cs
}

const profileTestKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func newVaultEnv(t *testing.T) *vaultEnv {
	t.Helper()
	useFakeDNS(t, vaultTestHost)
	e := &vaultEnv{
		site:    newLoginSite(t),
		cache:   newMapCache(),
		driver:  &fakeLoginDriver{cookies: persistentCanaryCookies()},
		consent: consent.NewStoreManager(persist.NewMemoryStore()),
		auditor: &capturingAuditor{},
		logs:    &bytes.Buffer{},
	}
	e.profDir = filepath.Join(t.TempDir(), "profiles")
	profiles, err := cookievault.NewProfileStore(e.profDir, profileTestKey, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	e.deps = Dependencies{
		Cache:   e.cache,
		Search:  &mockProvider{},
		Scraper: scraper.NewPipeline(scraper.PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ChromePath: "disabled", JinaDisabled: true}),
		Content: content.NewProcessor(),
		Sessions: func() session.Manager {
			m, _ := session.NewManager(session.Config{MaxSessions: 10})
			return m
		}(),
		Metrics:        metrics.NewCollector(),
		Auditor:        e.auditor,
		Logger:         slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Consent:        e.consent,
		CookieVault:    cookievault.NewPersistStore(persist.NewMemoryStore(), time.Hour),
		ProfileStore:   profiles,
		CookieCapturer: cookievault.NewCapturer(e.driver, nil, 1, time.Minute).WithProfiles(profiles).WithSignInBaseline(0),
	}
	return e
}

func (e *vaultEnv) grant(t *testing.T, tenant, user string) {
	t.Helper()
	if err := e.consent.Record(context.Background(), consent.Record{
		TenantID: tenant, UserID: user, Purpose: consent.PurposeSessionCapture,
		Granted: true, DecidedAt: "2026-10-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
}

func (e *vaultEnv) seed(t *testing.T, tenant, user string) {
	t.Helper()
	if _, err := e.deps.CookieVault.Put(context.Background(), cookievault.Subject{TenantID: tenant, UserID: user}, vaultTestDomain, canaryCookies()); err != nil {
		t.Fatal(err)
	}
}

// seedProfile stores a saved browser profile for the subject on vaulttest.com. It
// uses its own store instance, so the env's store sees it as already on disk.
func (e *vaultEnv) seedProfile(t *testing.T, tenant, user string) {
	t.Helper()
	seed, err := cookievault.NewProfileStore(e.profDir, profileTestKey, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	l, err := seed.Begin(context.Background(), cookievault.Subject{TenantID: tenant, UserID: user}, vaultTestDomain)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(l.Dir, "Default"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.Dir, "Default", "Cookies"), []byte("profile-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	l.Meta.Browser = "/fake/chrome"
	if _, err := l.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (e *vaultEnv) client(t *testing.T, tenant, user string) *mcp.ClientSession {
	t.Helper()
	srv := createIdentityTestServer(e.deps, tenant, user)
	sess := connectTestClient(context.Background(), t, srv)
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func callVaultTool(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) (map[string]any, string) {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("CallTool(%s): no content", name)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	var out map[string]any
	_ = json.Unmarshal([]byte(text), &out)
	return out, text
}

func TestVaultBackedScrapeBypassesCache(t *testing.T) {
	e := newVaultEnv(t)
	e.grant(t, "default", "alice")
	e.seed(t, "default", "alice")
	sess := e.client(t, "default", "alice")

	for _, mode := range []string{"full", "preview", "raw"} {
		before := e.site.requestCount()
		for i := 0; i < 2; i++ {
			out, text := callVaultTool(t, sess, "scrape_page", map[string]any{"url": e.site.pageURL(), "mode": mode})
			if out["authenticated"] != true {
				t.Fatalf("%s: authenticated = %v, want true (%s)", mode, out["authenticated"], text)
			}
			if !strings.Contains(text, privateMarker) {
				t.Fatalf("%s: private content missing", mode)
			}
		}
		if got := e.site.requestCount() - before; got < 2 {
			t.Errorf("%s: origin saw %d requests for 2 calls; the second was served from a cache", mode, got)
		}
	}
	if n := e.cache.setCount(); n != 0 {
		t.Errorf("cache received %d writes from vault-backed scrapes, want 0", n)
	}
	if e.cache.contains(privateMarker) {
		t.Error("authenticated content found in the cache")
	}
}

func TestAnonymousAfterAuthenticatedNeverServedFromCache(t *testing.T) {
	e := newVaultEnv(t)
	e.grant(t, "default", "alice")
	e.seed(t, "default", "alice")
	alice := e.client(t, "default", "alice")
	bob := e.client(t, "default", "bob")

	for _, mode := range []string{"full", "raw"} {
		if _, text := callVaultTool(t, alice, "scrape_page", map[string]any{"url": e.site.pageURL(), "mode": mode}); !strings.Contains(text, privateMarker) {
			t.Fatalf("%s: alice should see private content", mode)
		}
		out, text := callVaultTool(t, bob, "scrape_page", map[string]any{"url": e.site.pageURL(), "mode": mode})
		if strings.Contains(text, privateMarker) {
			t.Fatalf("%s: anonymous request after an authenticated one received private content", mode)
		}
		if !strings.Contains(text, publicMarker) {
			t.Fatalf("%s: bob should get the public page, got: %s", mode, text)
		}
		if _, set := out["authenticated"]; set {
			t.Errorf("%s: anonymous result must not carry the authenticated flag", mode)
		}
	}
}

func TestCanaryCookieNeverLeaks(t *testing.T) {
	e := newVaultEnv(t)
	e.grant(t, "default", "alice")
	e.seed(t, "default", "alice")
	sess := e.client(t, "default", "alice")
	subj := datasubject.Subject{TenantID: "default", UserID: "alice"}

	var surfaces []string
	_, text := callVaultTool(t, sess, "scrape_page", map[string]any{"url": e.site.pageURL()})
	surfaces = append(surfaces, text)
	_, text = callVaultTool(t, sess, "scrape_page", map[string]any{"url": e.site.pageURL(), "mode": "raw"})
	surfaces = append(surfaces, text)
	if !e.site.sawCookie() {
		t.Fatal("test is vacuous: the origin never received the session cookie")
	}

	// The login window saves a profile; its cookies are the canary too.
	_, text = callVaultTool(t, sess, "site_login", map[string]any{"url": "http://" + vaultTestHost + "/login", "use_login_window": true, "replace": true})
	surfaces = append(surfaces, text)

	// Data-subject exports and every audit event and log line.
	for name, ex := range map[string]datasubject.Exporter{
		"cookies":  cookievault.AsDataSubject(e.deps.CookieVault),
		"profiles": cookievault.AsProfileDataSubject(e.deps.ProfileStore),
	} {
		if name == "cookies" {
			continue // the window login replaced the bundle; profiles are exported below
		}
		exp, err := ex.ExportSubject(context.Background(), subj)
		if err != nil || exp == nil {
			t.Fatalf("%s export = %v, %v; want a summary", name, exp, err)
		}
		b, _ := json.Marshal(exp)
		surfaces = append(surfaces, string(b))
	}
	e.auditor.mu.Lock()
	for _, ev := range e.auditor.events {
		eb, _ := json.Marshal(ev)
		surfaces = append(surfaces, string(eb))
	}
	e.auditor.mu.Unlock()
	surfaces = append(surfaces, e.logs.String())

	_, text = callVaultTool(t, sess, "site_logout", map[string]any{"domain": vaultTestHost})
	surfaces = append(surfaces, text)

	for i, s := range surfaces {
		if strings.Contains(s, canaryValue) {
			t.Errorf("surface %d leaked the cookie value: %.200s", i, s)
		}
	}
}

func TestSiteLoginRequiresConsent(t *testing.T) {
	e := newVaultEnv(t)
	anon := e.client(t, "default", "anonymous")
	out, _ := callVaultTool(t, anon, "site_login", map[string]any{"url": "http://" + vaultTestHost + "/login"})
	if out["status"] != "unavailable" {
		t.Errorf("anonymous status = %v, want unavailable", out["status"])
	}

	alice := e.client(t, "default", "alice")
	out, _ = callVaultTool(t, alice, "site_login", map[string]any{"url": "http://" + vaultTestHost + "/login"})
	if out["status"] != "no_consent" {
		t.Errorf("status without consent = %v, want no_consent", out["status"])
	}
	if n := e.driver.openCount(); n != 0 {
		t.Errorf("browser opened %d times without consent", n)
	}
	if got := e.deps.CookieVault.List(context.Background(), cookievault.Subject{TenantID: "default", UserID: "alice"}); len(got) != 0 {
		t.Errorf("vault holds %d bundles without consent", len(got))
	}

	e.grant(t, "default", "alice")
	out, text := callVaultTool(t, alice, "site_login", map[string]any{"url": "http://" + vaultTestHost + "/login", "use_login_window": true})
	if out["status"] != "ok" || out["domain"] != vaultTestDomain || out["source"] != "login_window" || out["kind"] != "profile" {
		t.Fatalf("with consent: %s", text)
	}
	if got := e.deps.ProfileStore.List(context.Background(), cookievault.Subject{TenantID: "default", UserID: "alice"}); len(got) != 1 {
		t.Errorf("profile store holds %d profiles, want 1", len(got))
	}
}

func TestSiteLoginDefaultNeedsBrowserNotWindow(t *testing.T) {
	e := newVaultEnv(t)
	e.grant(t, "default", "alice")
	alice := e.client(t, "default", "alice")
	url := "http://" + vaultTestHost + "/login"

	// Browser not reachable: tell the user how to enable it, open no window.
	e.deps.CookieCapturer.WithAttacher(failAttacher{})
	out, text := callVaultTool(t, alice, "site_login", map[string]any{"url": url})
	if out["status"] != "browser_unavailable" || !strings.Contains(text, "remote-debugging") || !strings.Contains(text, "use_login_window") {
		t.Fatalf("unreachable browser: %s", text)
	}

	// Browser reached but not signed in to the site.
	e.deps.CookieCapturer.WithAttacher(emptyAttacher{})
	out, text = callVaultTool(t, alice, "site_login", map[string]any{"url": url})
	if out["status"] != "not_signed_in" || !strings.Contains(text, "use_login_window") {
		t.Fatalf("empty browser: %s", text)
	}

	if n := e.driver.openCount(); n != 0 {
		t.Errorf("login window opened %d times without use_login_window", n)
	}
	if got := e.deps.CookieVault.List(context.Background(), cookievault.Subject{TenantID: "default", UserID: "alice"}); len(got) != 0 {
		t.Errorf("vault holds %d bundles after failed logins", len(got))
	}
}

// Over HTTP there is no attacher: site_login must open the window by itself, not
// fail with a hint about a browser the server cannot reach.
func TestSiteLoginWithoutAttacherUsesWindow(t *testing.T) {
	e := newVaultEnv(t)
	e.grant(t, "default", "alice")
	alice := e.client(t, "default", "alice")
	out, text := callVaultTool(t, alice, "site_login", map[string]any{"url": "http://" + vaultTestHost + "/login"})
	if out["status"] != "ok" || out["kind"] != "profile" || out["source"] != "login_window" {
		t.Fatalf("no attacher: %s", text)
	}
	if n := e.driver.openCount(); n != 1 {
		t.Errorf("login window opened %d times, want 1", n)
	}
}

// failAttacher cannot reach the user's browser.
type failAttacher struct{}

func (failAttacher) Attach(context.Context, string) ([]cookievault.Cookie, error) {
	return nil, cookievault.ErrAttachUnavailable
}

// emptyAttacher reaches the browser but finds no cookies for the site.
type emptyAttacher struct{}

func (emptyAttacher) Attach(context.Context, string) ([]cookievault.Cookie, error) { return nil, nil }

func TestScrapeSkipsVaultWithoutConsent(t *testing.T) {
	e := newVaultEnv(t)
	e.seed(t, "default", "alice") // bundle exists, consent was never granted
	sess := e.client(t, "default", "alice")
	out, text := callVaultTool(t, sess, "scrape_page", map[string]any{"url": e.site.pageURL()})
	if strings.Contains(text, privateMarker) || out["authenticated"] == true {
		t.Fatalf("vault used without session_capture consent: %s", text)
	}
	if e.site.sawCookie() {
		t.Error("origin received a cookie without consent")
	}
}

func TestSiteLoginNotRegisteredWithoutCapturer(t *testing.T) {
	names := func(deps Dependencies) map[string]bool {
		sess := connectTestClient(context.Background(), t, createTestServer(deps))
		defer sess.Close()
		res, err := sess.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]bool{}
		for _, tl := range res.Tools {
			m[tl.Name] = true
		}
		return m
	}
	deps := setupTestDeps()
	deps.CookieCapturer = nil
	if got := names(deps); got["site_login"] || !got["site_logout"] {
		t.Errorf("no capturer: site_login=%v site_logout=%v, want false/true", got["site_login"], got["site_logout"])
	}
	deps.CookieVault = nil
	if got := names(deps); got["site_login"] || got["site_logout"] {
		t.Errorf("no vault: site_login=%v site_logout=%v, want false/false", got["site_login"], got["site_logout"])
	}
	if got := names(setupTestDeps()); !got["site_login"] || !got["site_logout"] {
		t.Error("with vault and capturer both tools must register")
	}
}

func TestSiteLogoutDeletesAndIsIdempotent(t *testing.T) {
	e := newVaultEnv(t)
	e.grant(t, "default", "alice")
	e.seed(t, "default", "alice")
	sess := e.client(t, "default", "alice")

	out, _ := callVaultTool(t, sess, "site_logout", map[string]any{"domain": "https://" + vaultTestHost + "/x"})
	if out["status"] != "ok" || out["removed"] != true || out["domain"] != vaultTestDomain {
		t.Fatalf("first logout = %v", out)
	}
	out, _ = callVaultTool(t, sess, "site_logout", map[string]any{"domain": vaultTestDomain})
	if out["status"] != "ok" || out["removed"] != false {
		t.Fatalf("second logout = %v, want ok/removed=false", out)
	}
	_, text := callVaultTool(t, sess, "scrape_page", map[string]any{"url": e.site.pageURL()})
	if strings.Contains(text, privateMarker) {
		t.Error("scrape after logout still authenticated")
	}

	anon := e.client(t, "default", "anonymous")
	if out, _ := callVaultTool(t, anon, "site_logout", map[string]any{"domain": vaultTestDomain}); out["status"] != "unavailable" {
		t.Errorf("anonymous logout status = %v, want unavailable", out["status"])
	}
}

func TestSiteLogoutLeavesNoCacheEntries(t *testing.T) {
	e := newVaultEnv(t)
	e.grant(t, "default", "alice")
	e.seed(t, "default", "alice")
	sess := e.client(t, "default", "alice")
	for _, mode := range []string{"full", "raw"} {
		callVaultTool(t, sess, "scrape_page", map[string]any{"url": e.site.pageURL(), "mode": mode})
	}
	callVaultTool(t, sess, "site_logout", map[string]any{"domain": vaultTestDomain})
	if e.cache.contains(privateMarker) || e.cache.contains("vaulttest") || e.cache.setCount() != 0 {
		t.Error("cache holds entries derived from the authenticated session")
	}
	if got := e.deps.CookieVault.List(context.Background(), cookievault.Subject{TenantID: "default", UserID: "alice"}); len(got) != 0 {
		t.Errorf("vault still lists %d bundles after logout", len(got))
	}
}

func TestSiteLoginRejectsBadURLs(t *testing.T) {
	e := newVaultEnv(t)
	e.deps.CookieCapturer = cookievault.NewCapturer(e.driver, func(ctx context.Context, h string) error {
		return scraper.ValidateHost(ctx, h, false)
	}, 1, time.Minute).WithProfiles(e.deps.ProfileStore)
	e.grant(t, "default", "alice")
	sess := e.client(t, "default", "alice")

	cases := map[string]string{
		"ftp://" + vaultTestHost + "/":                  "invalid_url",
		"file:///etc/passwd":                            "invalid_url",
		"javascript:alert(1)":                           "invalid_url",
		"http://user:pw@" + vaultTestHost + "/":         "invalid_url",
		"http://127.0.0.1/login":                        "invalid_url",
		"http://localhost/login":                        "invalid_url",
		"http://169.254.169.254/latest/meta-data/":      "invalid_url",
		"http://" + vaultTestHost + "/login":            "blocked", // resolves to loopback
		"https://co.uk/login":                           "invalid_url",
		"http://[::1]/login":                            "invalid_url",
		"http://" + vaultTestHost + ".evil.invalid/x/y": "invalid_url",
	}
	for u, want := range cases {
		out, text := callVaultTool(t, sess, "site_login", map[string]any{"url": u})
		if out["status"] != want {
			t.Errorf("%s: status = %v, want %s (%s)", u, out["status"], want, text)
		}
	}
	if n := e.driver.openCount(); n != 0 {
		t.Errorf("browser opened %d times for rejected URLs", n)
	}
}

func TestSiteLoginDescriptionDisclosesToS(t *testing.T) {
	var desc string
	for _, tl := range listTools(t) {
		if tl.Name == "site_login" {
			desc = strings.ToLower(tl.Description)
		}
	}
	if desc == "" {
		t.Fatal("site_login is not registered in the default test deps")
	}
	for _, want := range []string{"terms of service", "cookies", "never your password", "session_capture", "site_logout", "encrypted", "ttl"} {
		if !strings.Contains(desc, want) {
			t.Errorf("site_login description does not mention %q", want)
		}
	}
}

// spyVault counts reads so a test can prove the vault was never consulted.
type spyVault struct {
	cookievault.Store
	gets atomic.Int32
}

func (s *spyVault) Get(ctx context.Context, sub cookievault.Subject, host string) (*cookievault.Bundle, bool) {
	s.gets.Add(1)
	return s.Store.Get(ctx, sub, host)
}

func TestNoVaultReadWhenDisabled(t *testing.T) {
	e := newVaultEnv(t)
	spy := &spyVault{Store: e.deps.CookieVault}
	e.deps.CookieVault = spy
	e.seed(t, "default", "alice")

	// No consent, anonymous identity and a non-identified tenant: no vault read.
	for _, who := range [][2]string{{"default", "alice"}, {"default", "anonymous"}} {
		sess := e.client(t, who[0], who[1])
		callVaultTool(t, sess, "scrape_page", map[string]any{"url": e.site.pageURL()})
	}
	if n := spy.gets.Load(); n != 0 {
		t.Errorf("vault read %d times without identity+consent", n)
	}

	// Feature off entirely: scrape_page works and never panics on a nil vault.
	e.deps.CookieVault = nil
	e.deps.CookieCapturer = nil
	sess := e.client(t, "default", "alice")
	if _, text := callVaultTool(t, sess, "scrape_page", map[string]any{"url": e.site.pageURL()}); !strings.Contains(text, publicMarker) {
		t.Errorf("scrape with the vault disabled failed: %s", text)
	}
}

// corruptingStore returns garbage for vault bundle reads once armed.
type corruptingStore struct {
	persist.Store
	armed atomic.Bool
}

func (c *corruptingStore) Get(ctx context.Context, key string) ([]byte, bool) {
	if c.armed.Load() && strings.HasPrefix(key, "cv:b:") {
		return []byte("\x00not-ciphertext-or-json"), true
	}
	return c.Store.Get(ctx, key)
}

func TestCorruptVaultEntryDegradesToAnonymous(t *testing.T) {
	e := newVaultEnv(t)
	kv := &corruptingStore{Store: persist.NewMemoryStore()}
	e.deps.CookieVault = cookievault.NewPersistStore(kv, time.Hour)
	e.grant(t, "default", "alice")
	e.seed(t, "default", "alice")
	kv.armed.Store(true)

	sess := e.client(t, "default", "alice")
	out, text := callVaultTool(t, sess, "scrape_page", map[string]any{"url": e.site.pageURL()})
	if out["authenticated"] == true || strings.Contains(text, privateMarker) {
		t.Fatalf("corrupt bundle was used: %s", text)
	}
	if !strings.Contains(text, publicMarker) {
		t.Errorf("scrape did not degrade to an anonymous result: %s", text)
	}
}

// sanity: audit events from the denial paths carry the identity but no input.
func TestSiteLoginDenialIsAudited(t *testing.T) {
	e := newVaultEnv(t)
	sess := e.client(t, "default", "alice")
	callVaultTool(t, sess, "site_login", map[string]any{"url": "http://" + vaultTestHost + "/login?token=" + canaryValue})
	e.auditor.mu.Lock()
	defer e.auditor.mu.Unlock()
	if len(e.auditor.events) != 1 {
		t.Fatalf("got %d audit events, want 1", len(e.auditor.events))
	}
	ev := e.auditor.events[0]
	b, _ := json.Marshal(ev)
	if ev.Success || ev.ErrorCode != "no_consent" || ev.ToolName != "site_login" || strings.Contains(string(b), canaryValue) {
		t.Errorf("unexpected audit event: %s", b)
	}
}

func (e *vaultEnv) profiles(tenant, user string) []cookievault.ProfileSummary {
	return e.deps.ProfileStore.List(context.Background(), cookievault.Subject{TenantID: tenant, UserID: user})
}

func TestSiteLoginProfileRequiresConsent(t *testing.T) {
	e := newVaultEnv(t)
	alice := e.client(t, "default", "alice")
	out, _ := callVaultTool(t, alice, "site_login", map[string]any{"url": "http://" + vaultTestHost + "/login", "use_login_window": true})
	if out["status"] != "no_consent" {
		t.Fatalf("status = %v, want no_consent", out["status"])
	}
	if n := e.driver.openCount(); n != 0 {
		t.Errorf("browser opened %d times without consent", n)
	}
	if got := e.profiles("default", "alice"); len(got) != 0 {
		t.Errorf("%d profiles saved without consent", len(got))
	}
}

// A saved profile is used only with consent, only by its owner, and anonymous=true
// skips it. With Chrome disabled in these deps a used profile shows up as a
// browser error, an unused one as the public page.
func TestScrapeProfileGating(t *testing.T) {
	e := newVaultEnv(t)
	e.seedProfile(t, "default", "alice")
	alice := e.client(t, "default", "alice")
	bob := e.client(t, "default", "bob")
	args := map[string]any{"url": e.site.pageURL()}

	_, text := callVaultTool(t, alice, "scrape_page", args)
	if !strings.Contains(text, publicMarker) {
		t.Fatalf("profile used without consent: %s", text)
	}

	e.grant(t, "default", "alice")
	setsBefore := e.cache.setCount()
	out, text := callVaultTool(t, alice, "scrape_page", args)
	if n := e.cache.setCount(); n != setsBefore {
		t.Errorf("a profile scrape wrote %d cache entries (failures must not be negative-cached either)", n-setsBefore)
	}
	if strings.Contains(text, publicMarker) || !strings.Contains(text, "Chrome unavailable") {
		t.Fatalf("consenting owner did not go through the profile path: %s", text)
	}
	if out["authenticated"] == true {
		t.Error("a failed profile scrape must not claim authenticated")
	}

	_, text = callVaultTool(t, alice, "scrape_page", map[string]any{"url": e.site.pageURL(), "anonymous": true})
	if !strings.Contains(text, publicMarker) {
		t.Fatalf("anonymous=true did not skip the saved profile: %s", text)
	}

	e.grant(t, "default", "bob")
	if _, text = callVaultTool(t, bob, "scrape_page", args); !strings.Contains(text, publicMarker) {
		t.Fatalf("another user's profile was used: %s", text)
	}
}

func TestScrapeAnonymousSkipsCookieLogin(t *testing.T) {
	e := newVaultEnv(t)
	e.grant(t, "default", "alice")
	e.seed(t, "default", "alice")
	alice := e.client(t, "default", "alice")
	out, text := callVaultTool(t, alice, "scrape_page", map[string]any{"url": e.site.pageURL(), "anonymous": true})
	if !strings.Contains(text, publicMarker) || out["authenticated"] == true || e.site.sawCookie() {
		t.Fatalf("anonymous=true still used the saved cookies: %s", text)
	}
}

func TestScrapeProfileRawModeRefused(t *testing.T) {
	e := newVaultEnv(t)
	e.seedProfile(t, "default", "alice")
	e.grant(t, "default", "alice")
	alice := e.client(t, "default", "alice")
	res, err := alice.CallTool(context.Background(), &mcp.CallToolParams{Name: "scrape_page", Arguments: map[string]any{"url": e.site.pageURL(), "mode": "raw"}})
	if err != nil {
		t.Fatal(err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !res.IsError || !strings.Contains(text, "anonymous=true") {
		t.Fatalf("raw mode with a profile: isError=%v %s", res.IsError, text)
	}
	if got := e.profiles("default", "alice"); len(got) != 1 {
		t.Errorf("profile count = %d after a refused raw scrape, want 1", len(got))
	}
	if e.site.requestCount() != 0 {
		t.Error("origin was contacted for a refused scrape")
	}
}

func TestSiteLogoutRemovesProfile(t *testing.T) {
	e := newVaultEnv(t)
	e.grant(t, "default", "alice")
	e.seedProfile(t, "default", "alice")
	e.seed(t, "default", "alice")
	alice := e.client(t, "default", "alice")
	out, _ := callVaultTool(t, alice, "site_logout", map[string]any{"domain": vaultTestHost})
	if out["removed"] != true || out["removedProfile"] != true || out["removedCookies"] != true {
		t.Fatalf("logout = %v", out)
	}
	if got := e.profiles("default", "alice"); len(got) != 0 {
		t.Errorf("profile survived logout: %v", got)
	}
	out, _ = callVaultTool(t, alice, "site_logout", map[string]any{"domain": vaultTestHost})
	if out["removed"] != false {
		t.Errorf("second logout = %v, want removed=false", out)
	}
}

// One login per site: a new login of one kind removes the other, so a scrape
// never mixes a profile with copied cookies.
func TestLoginReplacesOtherKind(t *testing.T) {
	e := newVaultEnv(t)
	e.grant(t, "default", "alice")
	subj := cookievault.Subject{TenantID: "default", UserID: "alice"}
	alice := e.client(t, "default", "alice")
	url := "http://" + vaultTestHost + "/login"

	e.seed(t, "default", "alice")
	if out, text := callVaultTool(t, alice, "site_login", map[string]any{"url": url, "use_login_window": true, "replace": true}); out["status"] != "ok" {
		t.Fatal(text)
	}
	if n := len(e.deps.CookieVault.List(context.Background(), subj)); n != 0 {
		t.Errorf("cookie bundle survived a profile login (%d)", n)
	}
	if n := len(e.profiles("default", "alice")); n != 1 {
		t.Fatalf("profiles = %d, want 1", n)
	}

	e.deps.CookieCapturer.WithAttacher(cookieAttacher{canaryCookies()})
	if out, text := callVaultTool(t, alice, "site_login", map[string]any{"url": url, "replace": true}); out["status"] != "ok" || out["kind"] != "cookies" {
		t.Fatal(text)
	}
	if n := len(e.profiles("default", "alice")); n != 0 {
		t.Errorf("profile survived a cookie login (%d)", n)
	}
	if n := len(e.deps.CookieVault.List(context.Background(), subj)); n != 1 {
		t.Errorf("cookie bundles = %d, want 1", n)
	}
}

// cookieAttacher returns fixed cookies as the user's signed-in browser.
type cookieAttacher struct{ cookies []cookievault.Cookie }

func (a cookieAttacher) Attach(context.Context, string) ([]cookievault.Cookie, error) {
	return a.cookies, nil
}

// A sign-in the site did not keep (session cookies only) is refused, not stored.
func TestSiteLoginSessionOnlyCookiesNotSaved(t *testing.T) {
	e := newVaultEnv(t)
	e.grant(t, "default", "alice")
	e.driver.cookies = canaryCookies() // no expiry
	alice := e.client(t, "default", "alice")
	out, text := callVaultTool(t, alice, "site_login", map[string]any{"url": "http://" + vaultTestHost + "/login", "use_login_window": true})
	if out["status"] != "profile_not_saved" || !strings.Contains(text, "remember me") {
		t.Fatalf("session-only sign-in: %s", text)
	}
	if got := e.profiles("default", "alice"); len(got) != 0 {
		t.Errorf("profile saved from session-only cookies: %v", got)
	}
}

func TestProfileAuditAndMetrics(t *testing.T) {
	e := newVaultEnv(t)
	e.grant(t, "default", "alice")
	alice := e.client(t, "default", "alice")
	callVaultTool(t, alice, "site_login", map[string]any{"url": "http://" + vaultTestHost + "/login", "use_login_window": true})
	callVaultTool(t, alice, "scrape_page", map[string]any{"url": e.site.pageURL()})
	callVaultTool(t, alice, "site_logout", map[string]any{"domain": vaultTestDomain})

	e.auditor.mu.Lock()
	var login, logout bool
	for _, ev := range e.auditor.events {
		b, _ := json.Marshal(ev)
		switch {
		case ev.ToolName == "site_login" && ev.Success:
			login = strings.Contains(string(b), `"kind":"profile"`) && strings.Contains(string(b), vaultTestDomain)
		case ev.ToolName == "site_logout" && ev.Success:
			logout = strings.Contains(string(b), `"removedProfile":true`)
		}
		if strings.Contains(string(b), canaryValue) || strings.Contains(string(b), "profile-bytes") {
			t.Errorf("audit event leaks profile content: %s", b)
		}
	}
	e.auditor.mu.Unlock()
	if !login || !logout {
		t.Errorf("audit trail incomplete: login=%v logout=%v", login, logout)
	}

	rec := httptest.NewRecorder()
	e.deps.Metrics.HTTPHandler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`mcp_site_session_events_total{event="login",kind="profile",status="ok"} 1`,
		`mcp_site_session_events_total{event="logout",kind="any",status="ok"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %s", want)
		}
	}
}

func TestLoginHintForProfileNeedsWindow(t *testing.T) {
	deps := setupTestDeps()
	h := loginHintForKind(deps, "profile", "https://www.linkedin.com/feed/")
	if !strings.Contains(h, "use_login_window=true") || !strings.Contains(h, "linkedin.com") {
		t.Errorf("profile hint %q must name the login window and the site", h)
	}
	if !strings.Contains(h, "replace=true") {
		t.Errorf("profile hint %q must say replace=true, or site_login returns already_signed_in", h)
	}
	h = loginHintForKind(deps, "cookies", "https://www.linkedin.com/feed/")
	if !strings.Contains(h, "replace=true") || !strings.Contains(h, "still rejected, add use_login_window=true") {
		t.Errorf("cookie hint %q must say replace=true and offer the window only as a fallback", h)
	}
}

func TestSiteLogoutAcceptsSchemeLessURL(t *testing.T) {
	e := newVaultEnv(t)
	e.grant(t, "default", "alice")
	e.seedProfile(t, "default", "alice")
	alice := e.client(t, "default", "alice")
	out, _ := callVaultTool(t, alice, "site_logout", map[string]any{"domain": vaultTestHost + "/feed?x=1"})
	if out["removedProfile"] != true {
		t.Fatalf("logout with a path = %v, want the profile removed", out)
	}
}

// siteLoginFailure names the saved sites for too_many_profiles and has its own
// status for a full profile store (issue #775).
func TestSiteLoginFailureProfileLimits(t *testing.T) {
	e := newVaultEnv(t)
	e.seedProfile(t, "default", "alice")
	ctx := auth.WithIdentity(context.Background(), "default", "alice")

	res := siteLoginFailure(ctx, e.deps, time.Now(), "profile", cookievault.ErrTooManyProfiles)
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	if out["status"] != "too_many_profiles" || !strings.Contains(out["reason"].(string), vaultTestDomain) {
		t.Errorf("too_many_profiles result = %v, want the saved domain %q named", out, vaultTestDomain)
	}
	// Another user's sites are never listed.
	other := auth.WithIdentity(context.Background(), "default", "bob")
	res = siteLoginFailure(other, e.deps, time.Now(), "profile", cookievault.ErrTooManyProfiles)
	if strings.Contains(res.Content[0].(*mcp.TextContent).Text, vaultTestDomain) {
		t.Error("another user's saved site leaked into the message")
	}

	res = siteLoginFailure(ctx, e.deps, time.Now(), "profile", cookievault.ErrProfileStoreFull)
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, `"profile_store_full"`) || !strings.Contains(text, "COOKIE_VAULT_PROFILE_MAX_TOTAL_MB") {
		t.Errorf("store-full result = %s", text)
	}
}

// A valid saved login makes site_login answer already_signed_in and save
// nothing, until replace=true asks to sign in again.
func TestSiteLoginReportsAlreadySignedIn(t *testing.T) {
	e := newVaultEnv(t)
	e.grant(t, "default", "alice")
	e.grant(t, "default", "bob")
	alice := e.client(t, "default", "alice")
	url := "http://" + vaultTestHost + "/login"

	e.deps.CookieCapturer.WithAttacher(cookieAttacher{canaryCookies()})
	if out, text := callVaultTool(t, alice, "site_login", map[string]any{"url": url}); out["status"] != "ok" || out["kind"] != "cookies" {
		t.Fatal(text)
	}
	out, text := callVaultTool(t, alice, "site_login", map[string]any{"url": url})
	if out["status"] != "already_signed_in" || out["kind"] != "cookies" || out["expiresAt"] == "" || out["domain"] == "" {
		t.Fatalf("second call = %s", text)
	}
	if !strings.Contains(text, "scrape_page") || !strings.Contains(text, "replace=true") {
		t.Errorf("reason does not say what to do: %s", text)
	}
	if out, text := callVaultTool(t, alice, "site_login", map[string]any{"url": url, "replace": true}); out["status"] != "ok" {
		t.Fatalf("replace=true = %s", text)
	}
	// Another user has no saved login, so the check does not report alice's.
	bob := e.client(t, "default", "bob")
	if out, text := callVaultTool(t, bob, "site_login", map[string]any{"url": url}); out["status"] != "ok" {
		t.Fatalf("bob = %s", text)
	}
}

// A saved browser profile also counts as signed in.
func TestSiteLoginReportsAlreadySignedInForProfile(t *testing.T) {
	e := newVaultEnv(t)
	e.grant(t, "default", "alice")
	alice := e.client(t, "default", "alice")
	url := "http://" + vaultTestHost + "/login"

	e.seed(t, "default", "alice")
	if out, text := callVaultTool(t, alice, "site_login", map[string]any{"url": url, "use_login_window": true, "replace": true}); out["status"] != "ok" {
		t.Fatal(text)
	}
	if out, text := callVaultTool(t, alice, "site_login", map[string]any{"url": url, "use_login_window": true}); out["status"] != "already_signed_in" || out["kind"] != "profile" {
		t.Fatalf("second call = %s", text)
	}
}
