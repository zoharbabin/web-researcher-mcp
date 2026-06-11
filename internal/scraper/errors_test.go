package scraper

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testCtx() context.Context { return context.Background() }

// newBotWallServer returns a server that responds 200 with a Cloudflare-style
// "Checking your browser…" interstitial on every tier path.
func newBotWallServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Just a moment...</title></head><body><h1>Checking your browser before accessing the site.</h1><p>Please verify you are a human by completing the security check. Enable JavaScript and cookies to continue.</p></body></html>`))
	}))
}

// TestLooksLikeBotWall: short interstitial content is detected; a long real
// article that merely mentions a marker word is NOT (size-bounded guard).
func TestLooksLikeBotWall(t *testing.T) {
	t.Parallel()
	botWalls := []string{
		"Checking your browser before accessing example.com",
		"Please verify you are a human by completing the security check.",
		"Just a moment... cf-browser-verification",
		"Enable JavaScript and cookies to continue",
	}
	for _, c := range botWalls {
		if !looksLikeBotWall(c) {
			t.Errorf("expected bot-wall detection for %q", c)
		}
	}
	// A long article that happens to contain "captcha" must NOT be flagged.
	long := "This paper studies CAPTCHA usability. " + strings.Repeat("Real article body discussing the methodology and results in depth. ", 30)
	if looksLikeBotWall(long) {
		t.Error("a long article mentioning captcha must not be classified as a bot-wall")
	}
	// Empty / ordinary short content is not a bot-wall.
	if looksLikeBotWall("Welcome to my homepage.") {
		t.Error("ordinary short content must not be a bot-wall")
	}
}

// TestClassifyHTTPStatus_NotFound: 404 and 410 are ErrNotFound (definite dead
// link), not ErrNetwork (which would imply a retry).
func TestClassifyHTTPStatus_NotFound(t *testing.T) {
	t.Parallel()
	for _, code := range []int{404, 410} {
		se := classifyHTTPStatus(code, "https://example.com/gone", "html")
		if se.Kind != ErrNotFound {
			t.Errorf("HTTP %d → kind %v, want ErrNotFound", code, se.Kind)
		}
	}
}

// TestClassifyRawError_NotFound: a composite multi-tier error mentioning 404 maps
// to ErrNotFound, not the generic network bucket.
func TestClassifyRawError_NotFound(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("no content extracted from https://x/y (markdown: HTTP 404, html: HTTP 404)")
	se := classifyRawError(err, "https://x/y")
	if se.Kind != ErrNotFound {
		t.Errorf("raw 404 error → kind %v, want ErrNotFound", se.Kind)
	}
}

// TestPipeline_BotWallTreatedAsBlocked: a 200 response whose body is a bot/JS-wall
// interstitial must surface as ErrBlocked, not as a successful low-quality scrape.
func TestPipeline_BotWallTreatedAsBlocked(t *testing.T) {
	ts := newBotWallServer()
	defer ts.Close()

	orig := statFile
	statFile = func(path string) (any, error) { return nil, fmt.Errorf("not found") }
	defer func() { statFile = orig }()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	_, err := p.Scrape(testCtx(), ts.URL, 50000)
	if err == nil {
		t.Fatal("expected an error for a bot-wall interstitial, got success")
	}
	se, ok := err.(*ScrapeError)
	if !ok || se.Kind != ErrBlocked {
		t.Errorf("bot-wall should be ErrBlocked, got %T kind=%v", err, err)
	}
}
