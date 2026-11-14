package scraper

import (
	"context"
	"errors"
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

// TestClassifyRawError_DNSNotFound: a DNS NXDOMAIN ("no such host") is a
// permanent failure and must map to ErrNotFound (non-retryable), not the
// generic ErrNetwork bucket (which implies a retry will help). Regression
// guard for GitHub issue #674.
func TestClassifyRawError_DNSNotFound(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("Get \"https://this-domain-should-not-exist-zzz12345.com\": dial tcp: lookup this-domain-should-not-exist-zzz12345.com: no such host")
	se := classifyRawError(err, "https://this-domain-should-not-exist-zzz12345.com")
	if se.Kind != ErrNotFound {
		t.Errorf("NXDOMAIN error → kind %v, want ErrNotFound", se.Kind)
	}
}

// TestClassifyRawError_TransientNetworkStillRetryable is the regression guard
// alongside TestClassifyRawError_DNSNotFound: a genuinely transient network
// fault (connection refused, timeout, temporary DNS server failure — NOT "no
// such host") must still classify as ErrNetwork so the NXDOMAIN fix doesn't
// accidentally make ALL network errors non-retryable.
func TestClassifyRawError_TransientNetworkStillRetryable(t *testing.T) {
	t.Parallel()
	cases := []string{
		"dial tcp 127.0.0.1:1: connect: connection refused",
		"context deadline exceeded",
		"dial tcp: i/o timeout",
	}
	for _, msg := range cases {
		se := classifyRawError(errors.New(msg), "https://example.com")
		if se.Kind != ErrNetwork {
			t.Errorf("%q → kind %v, want ErrNetwork", msg, se.Kind)
		}
	}
}

// TestPipeline_SoftGateTreatedAsBlocked: end-to-end regression guard for issue
// #661 — an HTTP 200 response whose entire body is a short Crunchbase/SimilarWeb-
// style signup gate must surface as ErrBlocked, not a successful low-content scrape.
func TestPipeline_SoftGateTreatedAsBlocked(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Acme Corp - Company Profile</title></head><body>` +
			`<h1>Acme Corp</h1><p>Create a free account to see this company's full profile, including funding rounds and key people.</p>` +
			`</body></html>`))
	}))
	defer ts.Close()

	orig := statFile
	statFile = func(path string) (any, error) { return nil, fmt.Errorf("not found") }
	defer func() { statFile = orig }()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ChromePath: chromeDisabled})
	_, err := p.Scrape(testCtx(), ts.URL, 50000)
	if err == nil {
		t.Fatal("expected an error for a soft signup-gate page, got success")
	}
	se, ok := err.(*ScrapeError)
	if !ok || se.Kind != ErrBlocked {
		t.Errorf("soft signup gate should be ErrBlocked, got %T kind=%v", err, err)
	}
}

// TestPipeline_LongArticleWithFooterCTA_NotBlocked: negative counterpart to
// TestPipeline_SoftGateTreatedAsBlocked — a long, real article that happens to
// contain a soft-gate phrase in a trailing footer sentence must scrape
// successfully, never be misclassified as blocked.
func TestPipeline_LongArticleWithFooterCTA_NotBlocked(t *testing.T) {
	body := strings.Repeat(
		"This in-depth analysis covers the company's market position, funding history, and competitive landscape in substantial detail. ", 30,
	) + "Sign up to continue receiving our newsletter with more analysis like this."

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>Deep Dive Article</title></head><body><article>%s</article></body></html>`, body)
	}))
	defer ts.Close()

	orig := statFile
	statFile = func(path string) (any, error) { return nil, fmt.Errorf("not found") }
	defer func() { statFile = orig }()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true, ChromePath: chromeDisabled})
	result, err := p.Scrape(testCtx(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("expected a successful scrape for a long real article, got error: %v", err)
	}
	if !strings.Contains(result.Content, "in-depth analysis") {
		t.Errorf("expected real article content in scrape result, got: %s", result.Content[:min(200, len(result.Content))])
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
