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

	bp := getBrowserPool("", 1, 0)
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

	bp := getBrowserPool("", 1, 0)
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

// TestBrowserPoolIdleTimeoutCloses proves #460's fix: a browser pool armed
// with a short idle timeout closes itself — killing the Chromium process —
// after no getBrowserPool call touches it within that window, instead of
// staying open for the full server lifetime.
func TestBrowserPoolIdleTimeoutCloses(t *testing.T) {
	skipIfNoChrome(t)
	resetPool(t)

	const idleTimeout = 150 * time.Millisecond
	bp := getBrowserPool("", 1, idleTimeout)
	if bp.browser == nil {
		t.Fatalf("browser pool failed to launch: %v", bp.initErr)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		bp.mu.Lock()
		closed := bp.browser == nil
		bp.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("browser pool did not auto-close within the deadline after going idle")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestBrowserPoolIdleTimeoutResetsOnUse proves the idle timer is re-armed by
// every getBrowserPool call, not just the first: a pool touched more often
// than its idle timeout must stay open across that whole span, not close
// partway through because the ORIGINAL timer (from launch) fired.
func TestBrowserPoolIdleTimeoutResetsOnUse(t *testing.T) {
	skipIfNoChrome(t)
	resetPool(t)

	// A generous idleTimeout with a small sleep fraction (rather than half)
	// keeps this robust on loaded CI runners: a sleep intended to be well
	// inside the window must not be able to overshoot it under scheduling
	// delay, which would make the test intermittently fail even when the
	// implementation is correct.
	const idleTimeout = 1 * time.Second
	bp := getBrowserPool("", 1, idleTimeout)
	if bp.browser == nil {
		t.Fatalf("browser pool failed to launch: %v", bp.initErr)
	}

	// Touch well inside the idle window, several times, for longer than the
	// idle timeout itself would allow if it were not being reset.
	for i := 0; i < 4; i++ {
		time.Sleep(idleTimeout / 5)
		bp2 := getBrowserPool("", 1, idleTimeout)
		bp2.mu.Lock()
		alive := bp2.browser != nil
		bp2.mu.Unlock()
		if !alive {
			t.Fatalf("browser pool closed after repeated touches within the idle window (iteration %d)", i)
		}
	}
}

// TestBrowserPoolIdleTimeoutDoesNotFireDuringActiveUse proves the #460
// follow-up fix: acquire() disarms the idle timer for the duration of an
// in-progress operation, so an idle timeout shorter than that operation
// cannot close the browser out from under it. Without the fix, the timer
// armed by getBrowserPool would fire mid-"scrape" and close bp.browser while
// acquire() still holds it checked out.
func TestBrowserPoolIdleTimeoutDoesNotFireDuringActiveUse(t *testing.T) {
	skipIfNoChrome(t)
	resetPool(t)

	const idleTimeout = 50 * time.Millisecond
	bp := getBrowserPool("", 1, idleTimeout)
	if bp.browser == nil {
		t.Fatalf("browser pool failed to launch: %v", bp.initErr)
	}

	browser := bp.acquire()
	if browser == nil {
		t.Fatal("acquire() returned nil browser")
	}

	// Longer than idleTimeout: if the timer were still armed, it would have
	// fired and closed the pool well before this returns.
	time.Sleep(idleTimeout * 4)

	bp.mu.Lock()
	stillOpen := bp.browser != nil
	bp.mu.Unlock()
	if !stillOpen {
		t.Fatal("browser pool closed while acquire() held it checked out")
	}

	bp.release()

	deadline := time.Now().Add(5 * time.Second)
	for {
		bp.mu.Lock()
		closed := bp.browser == nil
		bp.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("browser pool did not auto-close within the deadline after release()")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestBrowserPoolIdleTimeoutZeroDisabled proves idleTimeout=0 (the
// PipelineConfig zero value, and BROWSER_IDLE_TIMEOUT=0 at the config layer)
// disables the auto-close timer entirely — the pre-#460 behavior every other
// test in this file relies on.
func TestBrowserPoolIdleTimeoutZeroDisabled(t *testing.T) {
	skipIfNoChrome(t)
	resetPool(t)

	bp := getBrowserPool("", 1, 0)
	if bp.browser == nil {
		t.Fatalf("browser pool failed to launch: %v", bp.initErr)
	}

	time.Sleep(300 * time.Millisecond)

	bp.mu.Lock()
	alive := bp.browser != nil
	bp.mu.Unlock()
	if !alive {
		t.Error("expected browser pool to stay open indefinitely with idleTimeout=0, but it closed")
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
	bp := getBrowserPool("", 1, 0)
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
	// Wait for the kill to actually take effect — i.e. for the liveness probe
	// itself to start observing a dead connection — instead of a fixed sleep,
	// which can race the OS/websocket teardown and make this test flaky.
	const teardownPollTimeout = 5 * time.Second
	teardownDeadline := time.Now().Add(teardownPollTimeout)
	for {
		bp.mu.Lock()
		dead := !bp.connectedLocked()
		bp.mu.Unlock()
		if dead {
			break
		}
		if time.Now().After(teardownDeadline) {
			t.Fatalf("browser connection still reports alive %s after killing pid %d", teardownPollTimeout, pid)
		}
		time.Sleep(20 * time.Millisecond)
	}

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
	newBP := getBrowserPool("", 1, 0)
	if newBP.launcher == nil || newBP.launcher.PID() == pid {
		t.Errorf("expected a new launcher PID after recovery, still have old PID %d", pid)
	}
}
