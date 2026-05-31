package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	twitterTweetRegex   = regexp.MustCompile(`^https?://(x\.com|twitter\.com)/([^/]+)/status/(\d+)`)
	twitterProfileRegex = regexp.MustCompile(`^https?://(x\.com|twitter\.com)/([^/?#]+)/?$`)
)

// fxTwitterBaseURL is the default FXTwitter API endpoint.
const fxTwitterBaseURL = "https://api.fxtwitter.com"

func isTwitterURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	switch host {
	case "x.com", "twitter.com", "mobile.twitter.com", "mobile.x.com":
		return true
	}
	return false
}

func (p *Pipeline) scrapeTwitter(ctx context.Context, url string, maxLength int) (*ScrapeResult, error) {
	// Try FXTwitter API first
	result, err := p.scrapeViaFxTwitter(ctx, url, maxLength)
	if err == nil && result != nil {
		return result, nil
	}

	// Fallback: rewrite URL to XCancel and use normal scraper tiers
	result, err = p.scrapeViaXCancel(ctx, url, maxLength)
	if err == nil && result != nil {
		return result, nil
	}

	return nil, &ScrapeError{
		Kind:    ErrBlocked,
		Message: fmt.Sprintf("twitter scrape failed for %s: FXTwitter and XCancel both unavailable", url),
		URL:     url,
		Tier:    "twitter",
	}
}

func (p *Pipeline) scrapeViaFxTwitter(ctx context.Context, rawURL string, maxLength int) (*ScrapeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	apiURL := buildFxTwitterAPIURL(rawURL)
	if apiURL == "" {
		return nil, fmt.Errorf("unsupported twitter URL format: %s", rawURL)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "web-researcher-mcp/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fxtwitter returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return nil, err
	}

	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("fxtwitter JSON parse error: %w", err)
	}

	// Determine if this is a tweet or profile response
	if tweetData, ok := envelope["tweet"].(map[string]any); ok {
		return formatTweetResult(rawURL, tweetData, maxLength), nil
	}
	if userData, ok := envelope["user"].(map[string]any); ok {
		return formatProfileResult(rawURL, userData, maxLength), nil
	}

	return nil, fmt.Errorf("fxtwitter response missing tweet or user data")
}

func buildFxTwitterAPIURL(rawURL string) string {
	if m := twitterTweetRegex.FindStringSubmatch(rawURL); len(m) >= 4 {
		user := m[2]
		statusID := m[3]
		return fmt.Sprintf("%s/%s/status/%s", fxTwitterBaseURL, user, statusID)
	}
	if m := twitterProfileRegex.FindStringSubmatch(rawURL); len(m) >= 3 {
		user := m[2]
		// Skip non-profile paths
		if user == "search" || user == "explore" || user == "home" || user == "notifications" || user == "messages" || user == "i" {
			return ""
		}
		return fmt.Sprintf("%s/%s", fxTwitterBaseURL, user)
	}
	return ""
}

func formatTweetResult(rawURL string, tweet map[string]any, maxLength int) *ScrapeResult {
	author, _ := tweet["author"].(map[string]any)
	username, _ := author["screen_name"].(string)
	displayName, _ := author["name"].(string)
	text, _ := tweet["text"].(string)

	likes := jsonNumber(tweet["likes"])
	retweets := jsonNumber(tweet["retweets"])
	views := jsonNumber(tweet["views"])
	createdAt, _ := tweet["created_at"].(string)

	content := fmt.Sprintf("@%s (%s)\n%s\n\n%s likes · %s retweets · %s views\nPosted: %s",
		username, displayName, text, likes, retweets, views, createdAt)

	if len(content) > maxLength {
		content = content[:maxLength]
	}

	return &ScrapeResult{
		URL:         rawURL,
		Content:     content,
		ContentType: "twitter",
		Title:       fmt.Sprintf("Tweet by @%s", username),
		Author:      displayName,
	}
}

func formatProfileResult(rawURL string, user map[string]any, maxLength int) *ScrapeResult {
	username, _ := user["screen_name"].(string)
	displayName, _ := user["name"].(string)
	bio, _ := user["description"].(string)

	followers := jsonNumber(user["followers"])
	following := jsonNumber(user["following"])
	tweetsCount := jsonNumber(user["tweets"])
	joined, _ := user["joined"].(string)

	content := fmt.Sprintf("%s (@%s)\n%s\n\n%s followers · %s following · %s tweets\nJoined: %s",
		displayName, username, bio, followers, following, tweetsCount, joined)

	if len(content) > maxLength {
		content = content[:maxLength]
	}

	return &ScrapeResult{
		URL:         rawURL,
		Content:     content,
		ContentType: "twitter",
		Title:       fmt.Sprintf("%s (@%s)", displayName, username),
		Author:      displayName,
	}
}

func (p *Pipeline) scrapeViaXCancel(ctx context.Context, rawURL string, maxLength int) (*ScrapeResult, error) {
	// Rewrite x.com or twitter.com to xcancel.com
	rewritten := rawURL
	rewritten = strings.Replace(rewritten, "://x.com/", "://xcancel.com/", 1)
	rewritten = strings.Replace(rewritten, "://twitter.com/", "://xcancel.com/", 1)
	rewritten = strings.Replace(rewritten, "://www.x.com/", "://xcancel.com/", 1)
	rewritten = strings.Replace(rewritten, "://www.twitter.com/", "://xcancel.com/", 1)

	if rewritten == rawURL {
		return nil, fmt.Errorf("failed to rewrite twitter URL to xcancel: %s", rawURL)
	}

	result, err := p.scrapeWithTieredFallback(ctx, rewritten, maxLength)
	if err != nil {
		return nil, err
	}

	// Restore original URL in result
	result.URL = rawURL
	if result.ContentType == "" || result.ContentType == "html" {
		result.ContentType = "twitter"
	}
	return result, nil
}

// jsonNumber extracts a number from a JSON value and formats it as a string.
func jsonNumber(v any) string {
	switch n := v.(type) {
	case float64:
		if n >= 1_000_000 {
			return fmt.Sprintf("%.1fM", n/1_000_000)
		}
		if n >= 1_000 {
			return fmt.Sprintf("%.1fK", n/1_000)
		}
		return fmt.Sprintf("%d", int64(n))
	case json.Number:
		return string(n)
	case nil:
		return "0"
	default:
		return fmt.Sprintf("%v", v)
	}
}
