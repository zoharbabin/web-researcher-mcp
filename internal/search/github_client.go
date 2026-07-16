package search

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// githubAPIBaseURL is the production GitHub REST API base URL, shared by
// every GitHub-backed search-layer capability (the awesome-list topic
// fallback in ecosystems_awesome.go and the GitHubProvider search.Provider).
const githubAPIBaseURL = "https://api.github.com"

// githubMaxResponseBytes bounds every GitHub API response body this package
// reads. A page of 30 search results is a few KB; 1 MiB is generous headroom
// while still bounding memory on an unexpectedly large response (see issue
// #396 rule 4.1).
const githubMaxResponseBytes = 1024 * 1024

// githubAPIRequest issues one GET request against the GitHub REST API and
// returns the raw response body. It is the single place that constructs
// GitHub API headers (Accept, X-GitHub-Api-Version, optional Authorization)
// and classifies GitHub's error responses — shared by every search-layer
// GitHub capability so header/auth/error-handling logic exists in exactly one
// place (issue #396 rule 5.1). token may be "" (unauthenticated, subject to
// GitHub's lower unauth rate limits); never logged.
//
// GitHub's Search API returns 403 (not 429) with X-RateLimit-Remaining: 0 on
// rate-limit exceeded — verified live 2026-07-15. That case is classified
// here as a rate-limit error whose message contains the literal substring
// "rate limited" so it hooks internal/tools/search.go's isRateLimitError()
// without any change to that function.
func githubAPIRequest(ctx context.Context, client *http.Client, baseURL, path, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("github: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "web-researcher-mcp/1.0")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token) // never logged
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return nil, fmt.Errorf("github: rate limited")
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // not found -> empty, not an error
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("github: rate limited")
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github: API error %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	return io.ReadAll(io.LimitReader(resp.Body, githubMaxResponseBytes))
}
