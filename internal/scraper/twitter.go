package scraper

import (
	"context"
	"encoding/json"
	"errors"
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
	result, fxErr := p.scrapeViaFxTwitter(ctx, url, maxLength)
	if fxErr == nil && result != nil {
		return result, nil
	}

	result, xcErr := p.scrapeViaXCancel(ctx, url, maxLength)
	if xcErr == nil && result != nil {
		return result, nil
	}

	// Propagate the MOST informative error (e.g. ErrNotFound for a deleted tweet)
	// rather than the first ScrapeError found or always masking with ErrBlocked.
	// scrapeKindPriority ranks definitiveness so an authoritative 404 from one
	// tier wins over a transient ErrNetwork timeout from the other.
	var best *ScrapeError
	for _, candidate := range []error{fxErr, xcErr} {
		var se *ScrapeError
		if candidate != nil && errors.As(candidate, &se) {
			if best == nil || scrapeKindPriority(se.Kind) > scrapeKindPriority(best.Kind) {
				best = se
			}
		}
	}
	if best != nil {
		return nil, best
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
		return nil, networkError(rawURL, "fxtwitter", err)
	}
	req.Header.Set("User-Agent", "web-researcher-mcp/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, networkError(rawURL, "fxtwitter", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, classifyHTTPStatus(resp.StatusCode, rawURL, "fxtwitter")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return nil, networkError(rawURL, "fxtwitter", err)
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

	// An X Article (long-form post) carries an empty top-level `text` — the visible
	// tweet body is just a t.co link — while the real content lives in an `article`
	// object. Reconstruct the full body so it isn't silently lost.
	if article, ok := tweet["article"].(map[string]any); ok {
		title, _ := article["title"].(string)
		var parts []string
		if t := strings.TrimSpace(title); t != "" {
			parts = append(parts, t)
		}
		// Prefer the full body reconstructed from the Draft.js content blocks; fall
		// back to the short preview_text when the blocks are absent.
		if body := extractArticleBody(article); body != "" {
			parts = append(parts, body)
		} else if preview, _ := article["preview_text"].(string); strings.TrimSpace(preview) != "" {
			parts = append(parts, strings.TrimSpace(preview))
		}
		if articleBody := strings.Join(parts, "\n\n"); articleBody != "" {
			if strings.TrimSpace(text) == "" {
				text = articleBody
			} else {
				text = text + "\n\n" + articleBody
			}
		}
	}

	// When `text` is still empty (e.g. a media-only or link-only tweet), fall back
	// to the unshortened raw_text so the result is never blank.
	if strings.TrimSpace(text) == "" {
		if rawText, ok := tweet["raw_text"].(map[string]any); ok {
			if rt, _ := rawText["text"].(string); strings.TrimSpace(rt) != "" {
				text = rt
			}
		}
	}

	likes := jsonNumber(tweet["likes"])
	retweets := jsonNumber(tweet["retweets"])
	views := jsonNumber(tweet["views"])
	createdAt, _ := tweet["created_at"].(string)

	content := fmt.Sprintf("@%s (%s)\n%s\n\n%s likes · %s retweets · %s views\nPosted: %s",
		username, displayName, text, likes, retweets, views, createdAt)

	if len(content) > maxLength {
		content = truncateBytes(content, maxLength)
	}

	return &ScrapeResult{
		URL:         rawURL,
		Content:     content,
		ContentType: "twitter",
		Title:       fmt.Sprintf("Tweet by @%s", username),
		Author:      displayName,
	}
}

// extractArticleBody reconstructs the full text of an X Article from the
// Draft.js content blocks FXTwitter returns under article.content.blocks[].
// Each block carries a .text field; block .type controls light markdown shaping
// (headers, list items). Returns "" when no content blocks are present.
func extractArticleBody(article map[string]any) string {
	content, ok := article["content"].(map[string]any)
	if !ok {
		return ""
	}
	blocks, ok := content["blocks"].([]any)
	if !ok {
		return ""
	}

	var lines []string
	for _, b := range blocks {
		block, ok := b.(map[string]any)
		if !ok {
			continue
		}
		text, _ := block["text"].(string)
		text = strings.TrimSpace(text)
		if text == "" {
			continue // skip empty / atomic (media-only) blocks
		}
		switch blockType, _ := block["type"].(string); blockType {
		case "header-one":
			lines = append(lines, "# "+text)
		case "header-two":
			lines = append(lines, "## "+text)
		case "header-three":
			lines = append(lines, "### "+text)
		case "unordered-list-item":
			lines = append(lines, "- "+text)
		case "ordered-list-item":
			lines = append(lines, "1. "+text)
		case "blockquote":
			lines = append(lines, "> "+text)
		default:
			lines = append(lines, text)
		}
	}
	return strings.Join(lines, "\n\n")
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
		content = truncateBytes(content, maxLength)
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
