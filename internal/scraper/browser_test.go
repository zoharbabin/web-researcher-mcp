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
	"time"

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
	result, err := p.scrapeBrowser(context.Background(), ts.URL, 10000, false)
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
	result, err := p.scrapeBrowser(context.Background(), ts.URL, 10000, false)
	if err != nil {
		t.Fatalf("unexpected error for a 200 response: %v", err)
	}
	if result == nil || len(result.Content) < 100 {
		t.Fatalf("expected extracted content, got: %+v", result)
	}
}

// TestScrapeBrowserRecoversFromCrash is the regression test for issue #464:
// getBrowserPool must detect a dead CDP connection (Chromium crashed via OOM,
// a malicious page, or a Chromium bug) and relaunch, rather than handing a
// caller a non-nil *rod.Browser pointing at a dead process. Before this fix,
// scrapeBrowser's own 30s context would eventually expire against the dead
// connection and the call would fail with a context/deadline error; after
// the fix it transparently relaunches and succeeds instead.
//
// It launches the pool, kills the real underlying Chromium OS process (a
// real process kill — not a mock — the actual crash scenario described in
// the issue), then asserts the next scrapeBrowser call succeeds rather than
// erroring out. Wall-clock time is not asserted tightly against the 30s
// figure from the issue: launching a fresh Chromium in this test environment
// (see the multi-second launch times of the sibling tests in this file) can
// itself take longer than 30s in a cold/throttled sandbox, and getBrowserPool
// has no ctx parameter — the liveness check and relaunch are not bounded by
// scrapeBrowser's own timeout. A generous outer bound below still catches a
// genuine infinite hang; the load-bearing assertion is that the call
// succeeds at all instead of returning the old timeout/connection error.
func TestScrapeBrowserRecoversFromCrash(t *testing.T) {
	skipIfNoChrome(t)
	resetPool(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><article><p>` +
			strings.Repeat("Real article content that is long enough to extract. ", 5) +
			`</p></article></body></html>`))
	}))
	defer ts.Close()

	// Prime the pool with a live browser.
	bp := getBrowserPool("", 1)
	if bp.browser == nil {
		t.Fatalf("browser pool failed to launch: %v", bp.initErr)
	}
	pid := bp.launcher.PID()
	if pid == 0 {
		t.Fatal("expected a non-zero launcher PID")
	}

	// Simulate the crash: kill the real Chromium process out from under the
	// pool. bp.browser stays non-nil — this is exactly the state issue #464
	// describes; only a liveness check (not a nil check) can detect it.
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("os.FindProcess(%d) failed: %v", pid, err)
	}
	if err := proc.Kill(); err != nil {
		t.Fatalf("failed to kill browser process %d: %v", pid, err)
	}
	// Give the OS a moment to tear down the process and close its end of the
	// CDP websocket before the next scrape attempt probes liveness.
	time.Sleep(500 * time.Millisecond)

	start := time.Now()
	p := NewPipeline(PipelineConfig{AllowPrivateIPs: true})
	result, scrapeErr := p.scrapeBrowser(context.Background(), ts.URL, 10000, false)
	elapsed := time.Since(start)

	// Smoke bound: catches a genuine infinite/multi-minute hang without being
	// tightly coupled to this sandbox's own Chromium cold-launch overhead.
	const maxBound = 90 * time.Second
	if elapsed > maxBound {
		t.Errorf("scrapeBrowser took %s to recover from a browser crash, want <= %s", elapsed, maxBound)
	}

	if scrapeErr != nil {
		t.Fatalf("expected scrapeBrowser to recover and succeed after a browser crash, got error after %s: %v", elapsed, scrapeErr)
	}
	if result == nil || len(result.Content) < 100 {
		t.Fatalf("expected extracted content after recovery, got: %+v", result)
	}

	// The relaunch must have produced a genuinely new, live browser/launcher
	// (issue #464's fix relaunches rather than reusing dead state).
	newBP := getBrowserPool("", 1)
	if newBP.launcher == nil || newBP.launcher.PID() == pid {
		t.Errorf("expected a new launcher PID after recovery, still have old PID %d", pid)
	}
}
