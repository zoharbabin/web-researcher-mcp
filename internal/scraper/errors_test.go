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
		"Please verify that you're not a robot to continue.", // CourtListener-style
		"JavaScript is disabled in your browser.",
		// Frontify brand-portal login wall (returned with HTTP 200 when go-rod
		// is fingerprinted as a bot on public portals).
		"Please enter your viewer credentials or request access to the Brand Owner.",
		"request access to the brand owner",
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

// TestLooksLikeBotWall_Anubis: regression guard for GitHub issue #263.
// Anubis (github.com/TecharoHQ/anubis) returns HTTP 200 with a ~1075-byte PoW
// interstitial. Before the fix, its phrases were absent from botWallMarkers AND
// botWallMaxBytes=600 was smaller than the Anubis body, so both guards failed.
func TestLooksLikeBotWall_Anubis(t *testing.T) {
	t.Parallel()

	// Representative Anubis interstitial body (~1075 bytes in production).
	// Contains the three canonical Anubis template phrases.
	anubisBody := `Making sure you're not a bot!
Anubis is checking to make sure that you are actually a human,
and to protect the server against the scourge of AI companies
that scrape sites without regard for the wishes of the site owners.
Anubis uses a Proof-of-Work scheme in the vein of Hashcash,
a proposed proof-of-work scheme for reducing email spam.
This is a placeholder solution so that more time can be spent
on building better solutions to this problem.`

	if !looksLikeBotWall(anubisBody) {
		t.Error("Anubis PoW interstitial must be detected as a bot-wall")
	}

	// Each Anubis marker must trigger independently on a minimal short string.
	anubisMarkers := []string{
		"Making sure you're not a bot!",
		"protect the server against the scourge of AI companies",
		"Anubis uses a proof-of-work scheme in the vein of Hashcash",
		"This is a placeholder solution so that more time can be spent",
	}
	for _, m := range anubisMarkers {
		if !looksLikeBotWall(m) {
			t.Errorf("Anubis marker must be detected as bot-wall: %q", m)
		}
	}

	// A legitimate academic article ABOUT proof-of-work / anti-scraping that is
	// long enough (> botWallMaxBytes) must NOT be flagged — size gate must hold.
	longPoWArticle := "We study proof-of-work schemes for spam prevention. " +
		"Anubis uses a proof-of-work scheme in the vein of Hashcash. " +
		strings.Repeat("The methodology examines computational hardness assumptions and the trade-off between verifier cost and prover work in distributed systems. ", 20)
	if looksLikeBotWall(longPoWArticle) {
		t.Errorf("a long article about PoW (len=%d) must not be flagged as a bot-wall", len(longPoWArticle))
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

// TestPipeline_NotFoundSurvivesAggregation is the regression guard for the
// v1.27.1 404-classification gap: classifyHTTPStatus already returned ErrNotFound
// per-tier, but the composite-error aggregator in scrapeWithTieredFallback did not
// promote ErrNotFound into highestKind, so a real 404 surfaced as content_empty.
// A 404 from every reachable tier must surface as ErrNotFound end-to-end.
func TestPipeline_NotFoundSurvivesAggregation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("404 page not found"))
	}))
	defer ts.Close()

	// Force the browser tier off so the test is deterministic and exercises the
	// HTTP-tier path that returns ErrNotFound (a real local Chrome would only add
	// a weaker ErrBrowser outcome, which priority-selection must already ignore).
	orig := statFile
	statFile = func(path string) (any, error) { return nil, fmt.Errorf("not found") }
	defer func() { statFile = orig }()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ChromePath: chromeDisabled})
	_, err := p.Scrape(testCtx(), ts.URL, 50000)
	if err == nil {
		t.Fatal("expected an error for a 404, got success")
	}
	se, ok := err.(*ScrapeError)
	if !ok || se.Kind != ErrNotFound {
		t.Errorf("404 should aggregate to ErrNotFound, got %T kind=%v", err, err)
	}
}

// TestScrapeKindPriority pins the definitiveness ordering the aggregator relies
// on: a definite remote answer (not-found / blocked / auth) must outrank a
// tier-local browser or content-empty failure, and a validation/security denial
// outranks everything — so the strongest sibling signal wins regardless of which
// tier produced it (and in which order).
func TestScrapeKindPriority(t *testing.T) {
	t.Parallel()
	stronger := [][2]ErrorKind{
		{ErrValidation, ErrNotFound},
		{ErrNotFound, ErrBlocked},
		{ErrNotFound, ErrBrowser},
		{ErrNotFound, ErrContent},
		{ErrAuth, ErrContent},
		{ErrBlocked, ErrBrowser},
		{ErrBrowser, ErrContent},
	}
	for _, p := range stronger {
		if scrapeKindPriority(p[0]) <= scrapeKindPriority(p[1]) {
			t.Errorf("expected %v to outrank %v", p[0], p[1])
		}
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

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ChromePath: chromeDisabled})
	_, err := p.Scrape(testCtx(), ts.URL, 50000)
	if err == nil {
		t.Fatal("expected an error for a bot-wall interstitial, got success")
	}
	se, ok := err.(*ScrapeError)
	if !ok || se.Kind != ErrBlocked {
		t.Errorf("bot-wall should be ErrBlocked, got %T kind=%v", err, err)
	}
}

// TestPipeline_AnubisBotWallTreatedAsBlocked: regression guard for GitHub issue #263.
// An HTTP-200 Anubis PoW interstitial (~1075 bytes) must surface as ErrBlocked so
// its placeholder text is never fed into the claim-coverage pipeline as real evidence.
func TestPipeline_AnubisBotWallTreatedAsBlocked(t *testing.T) {
	anubisHTML := `<!DOCTYPE html><html><head><title>Making sure you're not a bot!</title></head><body>` +
		`<p>Anubis is checking to make sure that you are actually a human, and to protect the server against the scourge of AI companies that scrape sites without regard for the wishes of the site owners.</p>` +
		`<p>Anubis uses a Proof-of-Work scheme in the vein of Hashcash, a proposed proof-of-work scheme for reducing email spam.</p>` +
		`<p>This is a placeholder solution so that more time can be spent on building better solutions to this problem.</p>` +
		`</body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(anubisHTML))
	}))
	defer ts.Close()

	orig := statFile
	statFile = func(path string) (any, error) { return nil, fmt.Errorf("not found") }
	defer func() { statFile = orig }()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ChromePath: chromeDisabled})
	_, err := p.Scrape(testCtx(), ts.URL, 50000)
	if err == nil {
		t.Fatal("Anubis PoW interstitial returned HTTP 200: expected ErrBlocked, got success")
	}
	se, ok := err.(*ScrapeError)
	if !ok || se.Kind != ErrBlocked {
		t.Errorf("Anubis PoW interstitial should be ErrBlocked, got %T kind=%v", err, err)
	}
}
