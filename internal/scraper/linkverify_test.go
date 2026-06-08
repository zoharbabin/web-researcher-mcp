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
