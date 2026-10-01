package scraper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestVerifier builds a verifier whose SSRF client allows private IPs (the
// httptest server binds 127.0.0.1) and points Wayback at a stub.
func newTestVerifier(t *testing.T, wayback string) *LinkVerifier {
	t.Helper()
	// Short per-URL timeout so the network-failure case doesn't wait on DNS.
	v := NewLinkVerifier(LinkVerifierConfig{AllowPrivateIPs: true, MaxConcurrency: 4, PerURLTimeout: 2 * time.Second})
	v.SetWaybackBase(wayback)
	return v
}

func TestLinkVerifier_LiveAndDead(t *testing.T) {
	t.Parallel()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(200)
		case "/gone":
			w.WriteHeader(404)
		case "/nohead":
			if r.Method == http.MethodHead {
				w.WriteHeader(405)
				return
			}
			w.WriteHeader(200) // GET works
		default:
			w.WriteHeader(500)
		}
	}))
	defer origin.Close()

	// Wayback stub: only the dead URL has a snapshot.
	wb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "gone") {
			_, _ = w.Write([]byte(`{"archived_snapshots":{"closest":{"available":true,"url":"http://web.archive.org/snap/gone","status":"200"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"archived_snapshots":{}}`))
	}))
	defer wb.Close()

	v := newTestVerifier(t, wb.URL)
	got := v.VerifyAll(context.Background(), []string{
		origin.URL + "/ok",
		origin.URL + "/gone",
		origin.URL + "/nohead",
		"",
	})

	if len(got) != 4 {
		t.Fatalf("want 4 statuses, got %d", len(got))
	}
	// /ok → live, no archive
	if !got[0].Live || got[0].HTTPStatus != 200 || got[0].ArchivedURL != "" {
		t.Errorf("ok: %+v", got[0])
	}
	// /gone → dead, archive attached
	if got[1].Live || got[1].HTTPStatus != 404 || got[1].ArchivedURL != "http://web.archive.org/snap/gone" {
		t.Errorf("gone: %+v", got[1])
	}
	// /nohead → HEAD 405 then GET 200 → live
	if !got[2].Live || got[2].HTTPStatus != 200 {
		t.Errorf("nohead (HEAD→GET fallback): %+v", got[2])
	}
	// "" → empty input, not live, no panic
	if got[3].Live || got[3].URL != "" {
		t.Errorf("empty url: %+v", got[3])
	}
}

// TestLinkVerifier_BotWallIsBlockedNotLive: 403/429/503 are "blocked": neither
// live (a refusal is not evidence the page exists) nor dead. No Wayback lookup.
func TestLinkVerifier_BotWallIsBlockedNotLive(t *testing.T) {
	t.Parallel()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/forbidden":
			w.WriteHeader(403)
		case "/ratelimited":
			w.WriteHeader(429)
		case "/unavailable":
			w.WriteHeader(503)
		default:
			w.WriteHeader(500)
		}
	}))
	defer origin.Close()

	// waybackCalled tracks whether the Wayback stub was ever hit, it must NOT be.
	var waybackCalled atomic.Bool
	wb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		waybackCalled.Store(true)
		_, _ = w.Write([]byte(`{"archived_snapshots":{"closest":{"available":true,"url":"http://web.archive.org/snap/blocked","status":"200"}}}`))
	}))
	defer wb.Close()

	v := newTestVerifier(t, wb.URL)
	got := v.VerifyAll(context.Background(), []string{
		origin.URL + "/forbidden",
		origin.URL + "/ratelimited",
		origin.URL + "/unavailable",
	})

	cases := []struct {
		name   string
		status int
		idx    int
	}{
		{"403 forbidden", 403, 0},
		{"429 rate-limited", 429, 1},
		{"503 unavailable", 503, 2},
	}
	for _, c := range cases {
		st := got[c.idx]
		if st.Live {
			t.Errorf("%s: a blocked URL must not be Live (%+v)", c.name, st)
		}
		if st.Dead() {
			t.Errorf("%s: a blocked URL must not be Dead (%+v)", c.name, st)
		}
		if !st.Blocked || st.Outcome != LinkOutcomeBlocked {
			t.Errorf("%s: want Blocked outcome, got %+v", c.name, st)
		}
		if st.HTTPStatus != c.status {
			t.Errorf("%s: want HTTPStatus=%d, got %d", c.name, c.status, st.HTTPStatus)
		}
		if st.ArchivedURL != "" {
			t.Errorf("%s: ArchivedURL must be empty for bot-wall, got %q", c.name, st.ArchivedURL)
		}
	}
	if waybackCalled.Load() {
		t.Error("Wayback must NOT be queried for blocked URLs")
	}
}

// TestLinkVerifier_FailureCauses: status 0 is split into distinct causes so a
// caller can tell a dead host (dns_failure) from a flaky one.
func TestLinkVerifier_FailureCauses(t *testing.T) {
	t.Parallel()
	wb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"archived_snapshots":{}}`))
	}))
	t.Cleanup(wb.Close)

	// Server that accepts a connection and drops it without answering.
	reset := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	t.Cleanup(reset.Close)

	// Server that never answers within the verifier's timeout.
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	t.Cleanup(slow.Close)
	t.Cleanup(func() { close(release) })

	// Closed port: connection refused.
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	cases := []struct {
		name     string
		url      string
		wantFail string
		wantDead bool
		wantOut  LinkOutcome
		verifier func() *LinkVerifier
	}{
		{"dns failure", "http://nonexistent-host-zz9.invalid/x", LinkFailureDNS, true, LinkOutcomeDead, nil},
		{"connection reset", reset.URL + "/x", LinkFailureConnectionReset, false, LinkOutcomeUnreachable, nil},
		{"connection refused", closedURL + "/x", LinkFailureConnectionRefused, false, LinkOutcomeUnreachable, nil},
		{"timeout", slow.URL + "/x", LinkFailureTimeout, false, LinkOutcomeUnreachable, func() *LinkVerifier {
			v := NewLinkVerifier(LinkVerifierConfig{AllowPrivateIPs: true, PerURLTimeout: 300 * time.Millisecond})
			v.SetWaybackBase(wb.URL)
			return v
		}},
		{"ssrf policy", "http://127.0.0.1:1/x", LinkFailureSSRFBlocked, false, LinkOutcomeUnreachable, func() *LinkVerifier {
			v := NewLinkVerifier(LinkVerifierConfig{AllowPrivateIPs: false, PerURLTimeout: time.Second})
			v.SetWaybackBase(wb.URL)
			return v
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			v := newTestVerifier(t, wb.URL)
			if c.verifier != nil {
				v = c.verifier()
			}
			st := v.VerifyAll(context.Background(), []string{c.url})[0]
			if st.HTTPStatus != 0 || st.Live {
				t.Fatalf("want status 0 and not live, got %+v", st)
			}
			if st.FailureCause != c.wantFail {
				t.Errorf("FailureCause = %q, want %q (%+v)", st.FailureCause, c.wantFail, st)
			}
			if st.Dead() != c.wantDead {
				t.Errorf("Dead() = %v, want %v", st.Dead(), c.wantDead)
			}
			if st.Outcome != c.wantOut {
				t.Errorf("Outcome = %q, want %q", st.Outcome, c.wantOut)
			}
		})
	}
}

// TestLinkVerifier_RedirectToRoot: a deep URL that ends at a host root (or a
// generic landing page) is not "live"; a redirect between two deep pages is.
func TestLinkVerifier_RedirectToRoot(t *testing.T) {
	t.Parallel()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	t.Cleanup(other.Close)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(200)
		case "/old/article":
			http.Redirect(w, r, "/", http.StatusFound)
		case "/old/other":
			http.Redirect(w, r, "/index.html", http.StatusMovedPermanently)
		case "/index.html":
			w.WriteHeader(200)
		case "/moved":
			http.Redirect(w, r, "/new/article", http.StatusMovedPermanently)
		case "/new/article":
			w.WriteHeader(200)
		case "/to-other-host-root":
			http.Redirect(w, r, other.URL+"/", http.StatusFound)
		case "/plain-root-redirect-is-fine":
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(origin.Close)

	wb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"archived_snapshots":{"closest":{"available":true,"url":"http://web.archive.org/snap/x","status":"200"}}}`))
	}))
	t.Cleanup(wb.Close)
	v := newTestVerifier(t, wb.URL)

	cases := []struct {
		name      string
		url       string
		wantLive  bool
		wantRoot  bool
		wantFinal string
	}{
		{"deep to root", origin.URL + "/old/article", false, true, origin.URL + "/"},
		{"deep to index.html", origin.URL + "/old/other", false, true, origin.URL + "/index.html"},
		{"deep to deep", origin.URL + "/moved", true, false, origin.URL + "/new/article"},
		{"root itself", origin.URL + "/", true, false, origin.URL + "/"},
		{"no redirect", origin.URL + "/plain-root-redirect-is-fine", true, false, origin.URL + "/plain-root-redirect-is-fine"},
		{"to other host root", origin.URL + "/to-other-host-root", false, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := v.VerifyAll(context.Background(), []string{c.url})[0]
			if st.Live != c.wantLive {
				t.Errorf("Live = %v, want %v (%+v)", st.Live, c.wantLive, st)
			}
			if st.RedirectedToRoot != c.wantRoot {
				t.Errorf("RedirectedToRoot = %v, want %v (%+v)", st.RedirectedToRoot, c.wantRoot, st)
			}
			if c.wantFinal != "" && st.FinalURL != c.wantFinal {
				t.Errorf("FinalURL = %q, want %q", st.FinalURL, c.wantFinal)
			}
			if c.wantRoot {
				if st.Outcome != LinkOutcomeRedirectedToRoot || st.Dead() {
					t.Errorf("want redirected_to_root outcome, not dead: %+v", st)
				}
				if st.HTTPStatus != 200 {
					t.Errorf("HTTPStatus should keep the final status 200, got %d", st.HTTPStatus)
				}
				if st.ArchivedURL == "" {
					t.Error("a root-redirected URL should still get its Wayback snapshot attached")
				}
			}
		})
	}
}

func TestLinkVerifier_NetworkFailureNoArchive(t *testing.T) {
	t.Parallel()
	// Wayback stub returns no snapshot.
	wb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"archived_snapshots":{}}`))
	}))
	defer wb.Close()
	v := newTestVerifier(t, wb.URL)
	// An unresolvable host → status 0, not live, no archive.
	got := v.VerifyAll(context.Background(), []string{"http://nonexistent.invalid.example.test.local/x"})
	if got[0].Live || got[0].HTTPStatus != 0 || got[0].ArchivedURL != "" {
		t.Errorf("network failure: %+v", got[0])
	}
}

// --- Save Page Now / Archive (#196) ---

// TestArchive_CapturedViaContentLocation: SPN responds (no redirect) with a
// Content-Location pointing at a /web/ snapshot → Captured=true, SnapshotURL set.
func TestArchive_CapturedViaContentLocation(t *testing.T) {
	t.Parallel()
	var sawAuth string
	spn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Location", "/web/20260101000000/https://example.com/x")
		w.WriteHeader(200)
	}))
	defer spn.Close()

	v := NewLinkVerifier(LinkVerifierConfig{AllowPrivateIPs: true})
	v.SetSaveBase(spn.URL + "/save/")
	res := v.Archive(context.Background(), "https://example.com/x")
	if !res.Captured || res.SnapshotURL != "https://web.archive.org/web/20260101000000/https://example.com/x" {
		t.Fatalf("expected captured snapshot, got %+v", res)
	}
	if res.Timestamp == "" {
		t.Error("captured snapshot should carry a timestamp")
	}
	if sawAuth != "" {
		t.Errorf("no Authorization header expected without keys, got %q", sawAuth)
	}
}

// TestArchive_AuthHeaderWhenKeysSet: both IA keys → Authorization: LOW access:secret.
func TestArchive_AuthHeaderWhenKeysSet(t *testing.T) {
	t.Parallel()
	var sawAuth string
	spn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Location", "/web/20260101000000/https://example.com/x")
		w.WriteHeader(200)
	}))
	defer spn.Close()

	v := NewLinkVerifier(LinkVerifierConfig{AllowPrivateIPs: true, IAAccessKey: "AK", IASecretKey: "SK"})
	v.SetSaveBase(spn.URL + "/save/")
	_ = v.Archive(context.Background(), "https://example.com/x")
	if sawAuth != "LOW AK:SK" {
		t.Errorf("Authorization = %q, want LOW AK:SK", sawAuth)
	}
}

// TestArchive_FallbackToExisting: SPN fails to produce a /web/ URL, but the
// availability API has an existing snapshot → Captured=false, SnapshotURL from fallback.
func TestArchive_FallbackToExisting(t *testing.T) {
	t.Parallel()
	spn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429) // throttled, no snapshot
	}))
	defer spn.Close()
	wb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"archived_snapshots":{"closest":{"available":true,"url":"https://web.archive.org/web/2019/https://example.com/x","status":"200"}}}`))
	}))
	defer wb.Close()

	v := NewLinkVerifier(LinkVerifierConfig{AllowPrivateIPs: true})
	v.SetSaveBase(spn.URL + "/save/")
	v.SetWaybackBase(wb.URL)
	res := v.Archive(context.Background(), "https://example.com/x")
	if res.Captured {
		t.Error("a throttled SPN must not report Captured=true")
	}
	if res.SnapshotURL != "https://web.archive.org/web/2019/https://example.com/x" {
		t.Errorf("expected fallback snapshot, got %q", res.SnapshotURL)
	}
	if res.HTTPStatus != 429 {
		t.Errorf("HTTPStatus = %d, want 429", res.HTTPStatus)
	}
}

// TestArchive_NothingAvailable: SPN fails and no existing snapshot → empty SnapshotURL, no panic.
func TestArchive_NothingAvailable(t *testing.T) {
	t.Parallel()
	spn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer spn.Close()
	wb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"archived_snapshots":{}}`))
	}))
	defer wb.Close()

	v := NewLinkVerifier(LinkVerifierConfig{AllowPrivateIPs: true})
	v.SetSaveBase(spn.URL + "/save/")
	v.SetWaybackBase(wb.URL)
	res := v.Archive(context.Background(), "https://example.com/x")
	if res.Captured || res.SnapshotURL != "" {
		t.Errorf("expected no snapshot, got %+v", res)
	}
}

// TestArchive_EmptyURL: empty input → zero result, no panic.
func TestArchive_EmptyURL(t *testing.T) {
	t.Parallel()
	v := NewLinkVerifier(LinkVerifierConfig{AllowPrivateIPs: true})
	res := v.Archive(context.Background(), "")
	if res.Captured || res.SnapshotURL != "" {
		t.Errorf("empty url should yield zero result, got %+v", res)
	}
}
