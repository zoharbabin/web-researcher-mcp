package scraper

import (
	"fmt"
	"strings"
)

type ErrorKind int

const (
	ErrNetwork    ErrorKind = iota // DNS, timeout, connection refused, TLS
	ErrBlocked                     // remote bot detection / HTTP 403 (a real site refusing us)
	ErrBrowser                     // Chrome not found, launch failed, connect failed
	ErrContent                     // Page loaded but no usable content extracted
	ErrAuth                        // HTTP 401, login redirect
	ErrRateLimit                   // HTTP 429
	ErrValidation                  // permanent client/security rejection: bad scheme, empty host, SSRF / private-IP / blocked-hostname denial. NOT retryable.
	ErrNotFound                    // HTTP 404/410 — the resource does not exist. Definite, NOT retryable (a dead link, not a transient fault).
)

// scrapeKindPriority ranks error kinds by how DEFINITIVE they are, so the
// composite-error aggregator (scrapeWithTieredFallback) can pick the most
// authoritative diagnosis when tiers disagree — independent of tier order. A
// security/validation denial is the most definitive (permanent, never retry);
// a 404/410 not-found and the explicit HTTP rejections (blocked/auth/rate-limit)
// are definite remote answers; a browser-launch/eval failure and a generic
// content-empty are the weakest (a tier-local hiccup or "page loaded but nothing
// extracted") and must never mask a stronger sibling signal. ErrNetwork is
// handled separately by the aggregator's allNetwork path and is not ranked here.
func scrapeKindPriority(k ErrorKind) int {
	switch k {
	case ErrValidation:
		return 6
	case ErrNotFound:
		return 5
	case ErrAuth:
		return 4
	case ErrRateLimit:
		return 3
	case ErrBlocked:
		return 2
	case ErrBrowser:
		return 1
	case ErrContent:
		return 0
	default:
		return 0
	}
}

type ScrapeError struct {
	Kind    ErrorKind
	Message string
	Cause   error
	URL     string
	Tier    string
}

func (e *ScrapeError) Error() string { return e.Message }
func (e *ScrapeError) Unwrap() error { return e.Cause }

func newScrapeError(kind ErrorKind, url, tier string, cause error, msg string) *ScrapeError {
	return &ScrapeError{Kind: kind, Message: msg, Cause: cause, URL: url, Tier: tier}
}

func networkError(url, tier string, cause error) *ScrapeError {
	return newScrapeError(ErrNetwork, url, tier, cause, fmt.Sprintf("network error: %v", cause))
}

func blockedError(url, tier string, cause error, detail string) *ScrapeError {
	return newScrapeError(ErrBlocked, url, tier, cause, fmt.Sprintf("access blocked: %s", detail))
}

// validationError marks a permanent client/security rejection (unsupported
// scheme, empty host, SSRF / private-IP / blocked-hostname denial). These are
// never retryable and must not be reported as transient network errors or as
// remote "bot detection".
func validationError(url, tier string, cause error, detail string) *ScrapeError {
	return newScrapeError(ErrValidation, url, tier, cause, detail)
}

// isSSRFDenial reports whether an error string is an SSRF / private-IP /
// blocked-hostname denial raised by the SSRF-safe HTTP client.
func isSSRFDenial(s string) bool {
	return containsAny(s, "ssrf:", "request blocked (private ip", "blocked hostname", "private ip or blocked")
}

func browserError(url string, cause error, detail string) *ScrapeError {
	return newScrapeError(ErrBrowser, url, "browser", cause, detail)
}

func contentError(url string, detail string) *ScrapeError {
	return newScrapeError(ErrContent, url, "", nil, detail)
}

func authError(url, tier string, statusCode int) *ScrapeError {
	return newScrapeError(ErrAuth, url, tier, nil, fmt.Sprintf("HTTP %d: authentication required", statusCode))
}

func rateLimitError(url, tier string) *ScrapeError {
	return newScrapeError(ErrRateLimit, url, tier, nil, "HTTP 429: rate limited")
}

func notFoundError(url, tier string, statusCode int) *ScrapeError {
	return newScrapeError(ErrNotFound, url, tier, nil, fmt.Sprintf("HTTP %d: page not found", statusCode))
}

func classifyHTTPStatus(statusCode int, url, tier string) *ScrapeError {
	switch statusCode {
	case 401:
		return authError(url, tier, statusCode)
	case 403:
		return blockedError(url, tier, nil, fmt.Sprintf("HTTP %d", statusCode))
	case 404, 410:
		// Definite not-found / gone — a dead link, not a transient fault. Never
		// retryable; the user must fix the URL, not retry or file a bug.
		return notFoundError(url, tier, statusCode)
	case 429:
		return rateLimitError(url, tier)
	default:
		return newScrapeError(ErrNetwork, url, tier, nil, fmt.Sprintf("HTTP %d", statusCode))
	}
}

func classifyRawError(err error, url string) *ScrapeError {
	if se, ok := err.(*ScrapeError); ok {
		return se
	}

	s := err.Error()
	switch {
	case isSSRFDenial(s):
		// Security denial — permanent, never retryable. Checked before the
		// generic network/blocked buckets because an SSRF denial can co-occur
		// with a sibling tier's timeout text.
		return validationError(url, "", err, s)
	case containsAny(s, "404", "410", "not found", "page not found"):
		// A definite not-found across tiers — a dead link, not transient. Checked
		// before the generic network bucket so it isn't reported as retryable.
		return newScrapeError(ErrNotFound, url, "", err, s)
	case containsAny(s, "no such host", "connection refused", "timeout", "deadline exceeded", "network", "navigation failed"):
		return networkError(url, "", err)
	case containsAny(s, "blocked", "403", "forbidden"):
		return blockedError(url, "", err, s)
	case containsAny(s, "429", "rate limit", "quota"):
		return rateLimitError(url, "")
	case containsAny(s, "401", "unauthorized"):
		return authError(url, "", 401)
	default:
		return newScrapeError(ErrContent, url, "", err, s)
	}
}

// botWallMaxBytes bounds how large extracted content can be and still be judged a
// bot-wall interstitial. A real article is far larger; an interstitial ("Checking
// your browser…", a CAPTCHA shell, or an Anubis PoW gate) is small. Set to 2048
// to cover Anubis (~1075 B) while remaining well below any real article body.
// The claim-fetch cap is 50KB so there is no risk of false-positives on real content.
const botWallMaxBytes = 2048

// botWallMarkers are phrases that, in SHORT extracted content, indicate a bot/JS
// interstitial rather than the page itself. Covers Cloudflare, CAPTCHA shells, and
// Anubis/PoW proof-of-work gates (github.com/TecharoHQ/anubis) which return HTTP 200.
var botWallMarkers = []string{
	"checking your browser",
	"enable javascript and cookies to continue",
	"verify you are human",
	"verifying you are human",
	"please verify you are a human",
	"verify that you're not a robot", // CourtListener / Free Law Project interstitial
	"verify that you are not a robot",
	"are you a robot",
	"complete the security check",
	"ddos protection by",
	"attention required",
	"just a moment", // Cloudflare interstitial title
	"cf-browser-verification",
	"please turn javascript on",
	"javascript is disabled", // bot/JS-wall shell that renders no real content
	"please enable javascript to view",
	// Anubis / PoW anti-AI-scraping gates (github.com/TecharoHQ/anubis).
	// These return HTTP 200 with a ~1075-byte interstitial — not detectable by
	// status code alone. All three phrases appear in the Anubis default template.
	"making sure you're not a bot",
	"protect the server against the scourge of",
	"anubis uses a proof-of-work scheme",
	"this is a placeholder solution",
}

// looksLikeBotWall reports whether short extracted content is a bot/JS-wall
// interstitial that was returned with a 200 status. Such pages must be treated as
// blocked (ErrBlocked), not as a successful low-quality scrape — otherwise the
// placeholder text masquerades as the page's content. Only short content is
// considered, so a legitimate long article is never flagged.
func looksLikeBotWall(content string) bool {
	if len(content) > botWallMaxBytes {
		return false
	}
	lower := strings.ToLower(content)
	for _, m := range botWallMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func containsAny(s string, substrs ...string) bool {
	lower := strings.ToLower(s)
	for _, sub := range substrs {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}
