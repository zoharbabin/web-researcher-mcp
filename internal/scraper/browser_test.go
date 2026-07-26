package scraper

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/go-rod/rod/lib/launcher/flags"
)

// skipIfNoChrome skips a test when no real Chromium/Chrome binary is
// reachable on the host, mirroring the autodetect behind chromeAvailable() —
// these tests launch a real browser process and cannot run in an environment
// with no browser installed (e.g. a minimal CI image).
func skipIfNoChrome(t *testing.T) {
	t.Helper()
	if !chromeAvailable() {
		t.Skip("no chromium/chrome binary found on this host")
	}
}

// resetPool clears the package-level singleton so each test starts from a
// clean, un-launched state and leaves no browser process behind for the next
// test in the package.
func resetPool(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		closeBrowserPool()
		pool = nil
		poolOnce = sync.Once{}
	})
	pool = nil
	poolOnce = sync.Once{}
}

// TestBrowserPoolCloseIdempotent proves rule 3.5 (issue #407 / #393): closing
// the browser pool twice in succession — e.g. once from a defer and once from
// an explicit shutdown path — must not panic or block forever.
func TestBrowserPoolCloseIdempotent(t *testing.T) {
	skipIfNoChrome(t)
	resetPool(t)

	bp := getBrowserPool("", 1)
	if bp.browser == nil {
		t.Fatalf("browser pool failed to launch: %v", bp.initErr)
	}

	bp.close()
	bp.close() // must not panic on the already-nil browser/launcher
}

// TestBrowserPoolCleanupRemovesUserDataDir proves rule 6.1 (issue #407 /
// #393): closing the pool calls launcher.Cleanup(), which removes the
// browser's UserDataDir (cached cookies/page content) from disk — closing the
// SOC 2 confidentiality gap where a killed-but-not-cleaned-up browser leaves
// session data behind indefinitely.
func TestBrowserPoolCleanupRemovesUserDataDir(t *testing.T) {
	skipIfNoChrome(t)
	resetPool(t)

	bp := getBrowserPool("", 1)
	if bp.browser == nil {
		t.Fatalf("browser pool failed to launch: %v", bp.initErr)
	}
	userDataDir := bp.launcher.Get(flags.UserDataDir)
	if userDataDir == "" {
		t.Fatal("expected launcher to have a non-empty UserDataDir")
	}
	if _, err := os.Stat(userDataDir); err != nil {
		t.Fatalf("expected UserDataDir to exist before close: %v", err)
	}

	bp.close()

	if _, err := os.Stat(userDataDir); !os.IsNotExist(err) {
		t.Errorf("expected UserDataDir %q to be removed after close, stat err = %v", userDataDir, err)
	}
}

// TestScrapeBrowserNotFoundDetected is the regression test for issue #432:
// a genuine HTTP 404 on the main document (e.g. a dead DOI-resolver landing
// page) rendered by the browser tier must be reported as ErrNotFound, not
// returned as if the dead page's HTML were real content.
func TestScrapeBrowserNotFoundDetected(t *testing.T) {
	skipIfNoChrome(t)
	resetPool(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<html><body><h1>Page Not Found</h1><p>The requested article could not be located.</p></body></html>"))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{AllowPrivateIPs: true})
	result, err := p.scrapeBrowser(context.Background(), ts.URL, 10000)
	if err == nil {
		t.Fatalf("expected an error for a 404 main document, got a successful result: %+v", result)
	}
	var se *ScrapeError
	if !errors.As(err, &se) {
		t.Fatalf("expected a *ScrapeError, got %T: %v", err, err)
	}
	if se.Kind != ErrNotFound {
		t.Errorf("Kind = %v, want ErrNotFound", se.Kind)
	}
	if se.Tier != "browser" {
		t.Errorf("Tier = %q, want browser", se.Tier)
	}
}

// TestScrapeBrowserSuccessStillWorks proves the 404-detection subscription
// added for issue #432 does not regress the ordinary 200 path — content is
// still extracted normally.
func TestScrapeBrowserSuccessStillWorks(t *testing.T) {
	skipIfNoChrome(t)
	resetPool(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><article><p>` +
			strings.Repeat("Real article content that is long enough to extract. ", 5) +
			`</p></article></body></html>`))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{AllowPrivateIPs: true})
	result, err := p.scrapeBrowser(context.Background(), ts.URL, 10000)
	if err != nil {
		t.Fatalf("unexpected error for a 200 response: %v", err)
	}
	if result == nil || len(result.Content) < 100 {
		t.Fatalf("expected extracted content, got: %+v", result)
	}
}
