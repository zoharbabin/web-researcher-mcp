package scraper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckArchiveTarget(t *testing.T) {
	refused := []string{
		"http://127.0.0.1/x",
		"http://10.0.0.5/x",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/x",
		"http://localhost/admin",
		"http://wiki/page",
		"http://app.internal/x",
		"http://printer.local/x",
		"http://nas.lan/x",
		"http://metadata.google.internal/computeMetadata/v1/",
		"not a url",
		"http://127.0.0.1./x",
		"http://wiki./x",
		"http://app.internal./x",
		"http://127.0.0.1。/x",
		"http://0177.0.0.1/x",
		"http://0x7f.0.0.1/x",
		"http://127.1/x",
		"http://2130706433/x",
	}
	for _, u := range refused {
		if err := checkArchiveTarget(context.Background(), u); err == nil {
			t.Errorf("checkArchiveTarget(%q) = nil, want a refusal", u)
		}
	}
	// A public IP literal needs no DNS and must pass.
	if err := checkArchiveTarget(context.Background(), "http://93.184.216.34/x"); err != nil {
		t.Errorf("public literal refused: %v", err)
	}
}

// TestArchiveAndWayback_NeverSendInternalTargets: the archive requests dial
// web.archive.org, so the SSRF-safe client cannot see the target host. Internal
// targets must not reach the archive at all, from either path.
func TestArchiveAndWayback_NeverSendInternalTargets(t *testing.T) {
	var archiveHits atomic.Int32
	archive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		archiveHits.Add(1)
		_, _ = w.Write([]byte(`{"archived_snapshots":{"closest":{"available":true,"url":"https://web.archive.org/web/1/x","status":"200"}}}`))
	}))
	defer archive.Close()
	dead := httptest.NewServer(http.NotFoundHandler()) // loopback target that answers 404
	defer dead.Close()

	newVerifier := func() *LinkVerifier {
		v := NewLinkVerifier(LinkVerifierConfig{AllowPrivateIPs: true, PerURLTimeout: time.Second})
		v.SetWaybackBase(archive.URL)
		v.SetSaveBase(archive.URL + "/save/")
		return v
	}
	internal := []string{dead.URL + "/page", "http://wiki/page", "http://app.internal/x"}

	prev := GuardArchiveTarget
	t.Cleanup(func() { GuardArchiveTarget = prev })

	// Control: with the guard off, the same calls do reach the archive, so the
	// assertions below can fail.
	GuardArchiveTarget = func(context.Context, string) error { return nil }
	if st := newVerifier().VerifyAll(context.Background(), []string{dead.URL + "/page"})[0]; st.ArchivedURL == "" {
		t.Fatal("control: dead loopback link should get a Wayback lookup when the guard is off")
	}
	archiveHits.Store(0)

	GuardArchiveTarget = checkArchiveTarget
	v := newVerifier()
	for _, u := range internal {
		if res := v.Archive(context.Background(), u); res.SnapshotURL != "" || res.PollURL != "" || res.Captured {
			t.Errorf("Archive(%q) = %+v, want an empty result", u, res)
		}
		if got := v.wayback(context.Background(), u); got != "" {
			t.Errorf("wayback(%q) = %q, want empty", u, got)
		}
	}
	for _, st := range v.VerifyAll(context.Background(), internal) {
		if st.ArchivedURL != "" {
			t.Errorf("VerifyAll archived %q", st.URL)
		}
	}
	if n := archiveHits.Load(); n != 0 {
		t.Errorf("archive received %d requests for internal targets, want 0", n)
	}
}
