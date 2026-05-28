package scraper

import (
	"fmt"
	"strings"
)

type ErrorKind int

const (
	ErrNetwork   ErrorKind = iota // DNS, timeout, connection refused, TLS
	ErrBlocked                    // SSRF, allowlist, HTTP 403, bot detection
	ErrBrowser                    // Chrome not found, launch failed, connect failed
	ErrContent                    // Page loaded but no usable content extracted
	ErrAuth                       // HTTP 401, login redirect
	ErrRateLimit                  // HTTP 429
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
	switch {
	case statusCode == 401:
		return authError(url, tier, statusCode)
	case statusCode == 403:
		return blockedError(url, tier, nil, fmt.Sprintf("HTTP %d", statusCode))
	case statusCode == 429:
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
