package cookievault

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/launcher"
)

// TestRodDriverReadsCookiesAndSignalsClose drives a real headless Chromium.
// Skipped when no Chrome is installed.
func TestRodDriverReadsCookiesAndSignalsClose(t *testing.T) {
	path, ok := launcher.LookPath()
	if !ok {
		t.Skip("chrome not available")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc", Path: "/", HttpOnly: true})
		http.SetCookie(w, &http.Cookie{Name: "keep", Value: "def", Path: "/", Expires: time.Now().Add(time.Hour)})
		_, _ = w.Write([]byte("<html><body>login</body></html>"))
	}))
	defer srv.Close()

	// Not t.TempDir: Chrome helpers may still write after Close, which would
	// fail TempDir's single-shot cleanup. removeDir retries.
	dir, err := os.MkdirTemp("", "rod-driver-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeDir(dir) })
	d := &RodDriver{chromePath: path, headless: true}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	sess, err := d.Open(ctx, srv.URL, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	var text string
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline) && text == ""; time.Sleep(200 * time.Millisecond) {
		text, _ = sess.PageText(ctx)
	}
	if text != "login" {
		t.Errorf("PageText = %q, want %q", text, "login")
	}

	var got map[string]Cookie
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		cookies, err := sess.Cookies(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got = map[string]Cookie{}
		for _, c := range cookies {
			got[c.Name] = c
		}
		if len(got) == 2 {
			break
		}
	}
	sid, keep := got["sid"], got["keep"]
	if sid.Value != "abc" || !sid.HTTPOnly || !sid.HostOnly || !sid.Expires.IsZero() {
		t.Errorf("session cookie wrong: %+v", sid)
	}
	if keep.Value != "def" || keep.Expires.IsZero() {
		t.Errorf("persistent cookie wrong: %+v", keep)
	}

	select {
	case <-sess.Done():
		t.Fatal("Done closed while the window was open")
	default:
	}
	_ = sess.Close()
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Error("Done not closed after Close")
	}
	_ = sess.Close() // idempotent
	if _, err := os.Stat(dir); err == nil {
		entries, _ := os.ReadDir(dir)
		if len(entries) != 0 {
			t.Logf("profile dir has %d leftovers (Capturer removes the dir itself)", len(entries))
		}
	}
}

// TestRodDriverBrowserBin: an explicit path wins, else the installed browser,
// else "" (rod then downloads its own Chromium).
func TestRodDriverBrowserBin(t *testing.T) {
	orig := lookPath
	t.Cleanup(func() { lookPath = orig })

	lookPath = func() (string, bool) { return "/installed/chrome", true }
	if got := (&RodDriver{chromePath: "/explicit/chromium"}).BrowserBin(); got != "/explicit/chromium" {
		t.Fatalf("explicit path = %q", got)
	}
	if got := NewRodDriver("").BrowserBin(); got != "/installed/chrome" {
		t.Fatalf("installed browser = %q", got)
	}
	lookPath = func() (string, bool) { return "", false }
	if got := NewRodDriver("").BrowserBin(); got != "" {
		t.Fatalf("no browser = %q", got)
	}
}
