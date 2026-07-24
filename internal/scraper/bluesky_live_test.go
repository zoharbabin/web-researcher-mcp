//go:build live

// Live external-API integration test — excluded from the default suite
// (non-deterministic external dependency, real network calls to
// public.api.bsky.app). Run with:
//
//	go test -tags=live -run TestScrapeBsky.*Live ./internal/scraper/...
//
// Proves the native Bluesky post/profile scraper route (#285) actually
// reaches Bluesky's real, unauthenticated public AppView API.
package scraper

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// discoverBskyLivePostURL fetches @bsky.app's own recent posts (a long-lived
// public account) and returns a live bsky.app post URL for TestScrapeBskyPostLive
// — hardcoding a specific rkey would break the moment that post is deleted.
func discoverBskyLivePostURL(t *testing.T) string {
	t.Helper()

	q := url.Values{}
	q.Set("actor", "bsky.app")
	q.Set("limit", "1")
	q.Set("filter", "posts_no_replies")
	reqURL := "https://public.api.bsky.app/xrpc/app.bsky.feed.getAuthorFeed?" + q.Encode()

	resp, err := http.Get(reqURL)
	skipIfNetworkUnreachableScraper(t, err)
	if err != nil {
		t.Fatalf("discoverBskyLivePostURL: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("discoverBskyLivePostURL: read body: %v", err)
	}

	var feed struct {
		Feed []struct {
			Post struct {
				URI    string `json:"uri"`
				Author struct {
					Handle string `json:"handle"`
				} `json:"author"`
			} `json:"post"`
		} `json:"feed"`
	}
	if err := json.Unmarshal(body, &feed); err != nil {
		t.Fatalf("discoverBskyLivePostURL: unmarshal: %v", err)
	}
	if len(feed.Feed) == 0 {
		t.Skip("bsky.app author feed returned no posts")
	}

	post := feed.Feed[0].Post
	segments := strings.Split(post.URI, "/")
	rkey := segments[len(segments)-1]
	return "https://bsky.app/profile/" + post.Author.Handle + "/post/" + rkey
}

func TestScrapeBskyProfileLive(t *testing.T) {
	p := NewPipeline(PipelineConfig{})

	res, err := p.Scrape(context.Background(), "https://bsky.app/profile/bsky.app", 4096)
	skipIfNetworkUnreachableScraper(t, err)
	if err != nil {
		t.Fatalf("Scrape() error: %v", err)
	}
	if res.Tier != "bluesky:api" {
		t.Errorf("Tier = %q, want bluesky:api", res.Tier)
	}
	if !strings.Contains(res.Content, "bsky.app") {
		t.Errorf("Content = %q, want it to mention bsky.app", res.Content)
	}
	t.Logf("tier=%s title=%q len(content)=%d", res.Tier, res.Title, len(res.Content))
}

func TestScrapeBskyPostLive(t *testing.T) {
	postURL := discoverBskyLivePostURL(t)

	p := NewPipeline(PipelineConfig{})
	res, err := p.Scrape(context.Background(), postURL, 4096)
	skipIfNetworkUnreachableScraper(t, err)
	if err != nil {
		t.Fatalf("Scrape(%q) error: %v", postURL, err)
	}
	if res.Tier != "bluesky:api" {
		t.Errorf("Tier = %q, want bluesky:api", res.Tier)
	}
	if res.ForumSignals == nil {
		t.Fatal("expected ForumSignals on a live post scrape, got nil")
	}
	if res.ForumSignals.Platform != "bluesky" {
		t.Errorf("ForumSignals.Platform = %q, want bluesky", res.ForumSignals.Platform)
	}
	t.Logf("tier=%s title=%q upvotes=%d comments=%d", res.Tier, res.Title, res.ForumSignals.Upvotes, res.ForumSignals.Comments)
}
