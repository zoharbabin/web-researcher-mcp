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
)

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

func classifyHTTPStatus(statusCode int, url, tier string) *ScrapeError {
	switch statusCode {
	case 401:
		return authError(url, tier, statusCode)
	case 403:
		return blockedError(url, tier, nil, fmt.Sprintf("HTTP %d", statusCode))
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

func containsAny(s string, substrs ...string) bool {
	lower := strings.ToLower(s)
	for _, sub := range substrs {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}
