package scraper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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
