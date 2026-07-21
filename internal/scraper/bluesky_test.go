package scraper

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newBskyTestPipeline creates a Pipeline whose BskyAPIBase points at the
// given test server, and registers a cleanup to close it.
func newBskyTestPipeline(t *testing.T, handler http.HandlerFunc) *Pipeline {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewPipeline(PipelineConfig{BskyAPIBase: srv.URL, AllowPrivateIPs: true})
}

// TestMultiInstancePipelineIsolation proves rule 1.3 (issue #407): two
// Pipeline instances constructed with different BskyAPIBase values in the
// same process route to their own configured base independently — no shared
// package-level state.
func TestMultiInstancePipelineIsolation(t *testing.T) {
	t.Parallel()

	var gotPathA, gotPathB string
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPathA = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"handle": "a.bsky.social"}`)
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPathB = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"handle": "b.bsky.social"}`)
	}))
	defer srvB.Close()

	pa := NewPipeline(PipelineConfig{BskyAPIBase: srvA.URL, AllowPrivateIPs: true})
	pb := NewPipeline(PipelineConfig{BskyAPIBase: srvB.URL, AllowPrivateIPs: true})

	resA, err := pa.Scrape(context.Background(), "https://bsky.app/profile/a.bsky.social", 4096)
	if err != nil {
		t.Fatalf("pa.Scrape: unexpected error: %v", err)
	}
	resB, err := pb.Scrape(context.Background(), "https://bsky.app/profile/b.bsky.social", 4096)
	if err != nil {
		t.Fatalf("pb.Scrape: unexpected error: %v", err)
	}

	if pa.bskyAPIBase() == pb.bskyAPIBase() {
		t.Errorf("pa and pb share the same bskyAPIBase %q — instance state leaked", pa.bskyAPIBase())
	}
	if gotPathA == "" || gotPathB == "" {
		t.Fatal("expected both test servers to receive a request")
	}
	if !strings.Contains(resA.Content, "a.bsky.social") {
		t.Errorf("resA.Content = %q, want to contain %q", resA.Content, "a.bsky.social")
	}
	if !strings.Contains(resB.Content, "b.bsky.social") {
		t.Errorf("resB.Content = %q, want to contain %q", resB.Content, "b.bsky.social")
	}
}

// TestIsBskyURL verifies that isBskyURL accepts real bsky.app hostnames and
// rejects look-alikes.
func TestIsBskyURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		url  string
		want bool
	}{
		{"https://bsky.app/profile/user.bsky.social/post/abc123", true},
		{"https://www.bsky.app/profile/user.bsky.social", true},
		{"https://evil.com/bsky.app", false},
		{"https://bsky.social", false},
		{"https://twitter.com", false},
	}

	for _, tc := range cases {
		got := isBskyURL(tc.url)
		if got != tc.want {
			t.Errorf("isBskyURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

// TestBskyPostURLToATURI verifies post URL path parsing.
func TestBskyPostURLToATURI(t *testing.T) {
	t.Parallel()

	cases := []struct {
		url        string
		wantHandle string
		wantRkey   string
		wantOK     bool
	}{
		{"https://bsky.app/profile/user.bsky.social/post/3k2abc", "user.bsky.social", "3k2abc", true},
		{"https://bsky.app/profile/did:plc:abc123/post/3k2abc", "did:plc:abc123", "3k2abc", true},
		{"https://bsky.app/profile/user.bsky.social", "", "", false},
		{"https://bsky.app/search", "", "", false},
		{"https://bsky.app/profile/user.bsky.social/post/", "", "", false},
	}

	for _, tc := range cases {
		handle, rkey, ok := bskyPostURLToATURI(tc.url)
		if ok != tc.wantOK || handle != tc.wantHandle || rkey != tc.wantRkey {
			t.Errorf("bskyPostURLToATURI(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.url, handle, rkey, ok, tc.wantHandle, tc.wantRkey, tc.wantOK)
		}
	}
}

// TestScrapeBskyPost verifies a post thread is fetched and formatted with
// engagement counts in ForumSignals.
func TestScrapeBskyPost(t *testing.T) {
	t.Parallel()

	p := newBskyTestPipeline(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"thread": {
				"post": {
					"author": {"handle": "user.bsky.social", "displayName": "Test User"},
					"record": {"text": "hello world", "createdAt": "2026-06-11T14:35:34Z"},
					"likeCount": 10,
					"repostCount": 2,
					"replyCount": 5
				},
				"replies": []
			}
		}`)
	})

	res, err := p.Scrape(context.Background(), "https://bsky.app/profile/user.bsky.social/post/abc123", 4096)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ContentType != "bluesky" {
		t.Errorf("ContentType = %q, want %q", res.ContentType, "bluesky")
	}
	if res.Tier != "bluesky:api" {
		t.Errorf("Tier = %q, want %q", res.Tier, "bluesky:api")
	}
	if res.ForumSignals == nil {
		t.Fatal("expected ForumSignals, got nil")
	}
	if res.ForumSignals.Platform != "bluesky" {
		t.Errorf("ForumSignals.Platform = %q, want %q", res.ForumSignals.Platform, "bluesky")
	}
	if res.ForumSignals.Upvotes != 10 {
		t.Errorf("ForumSignals.Upvotes = %d, want 10", res.ForumSignals.Upvotes)
	}
	if res.ForumSignals.Comments != 5 {
		t.Errorf("ForumSignals.Comments = %d, want 5", res.ForumSignals.Comments)
	}
	if !strings.Contains(res.Content, "hello world") {
		t.Errorf("post text missing from content: %q", res.Content)
	}
}

// TestScrapeBskyPost_Replies verifies only bskyMaxReplies replies are included.
func TestScrapeBskyPost_Replies(t *testing.T) {
	t.Parallel()

	var repliesJSON strings.Builder
	for i := 0; i < 7; i++ {
		if i > 0 {
			repliesJSON.WriteString(",")
		}
		fmt.Fprintf(&repliesJSON, `{"post": {"author": {"handle": "replier%d.bsky.social"}, "record": {"text": "reply %d"}}}`, i, i)
	}

	p := newBskyTestPipeline(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"thread": {
				"post": {
					"author": {"handle": "user.bsky.social"},
					"record": {"text": "hello world"}
				},
				"replies": [%s]
			}
		}`, repliesJSON.String())
	})

	res, err := p.Scrape(context.Background(), "https://bsky.app/profile/user.bsky.social/post/abc123", 8192)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	count := strings.Count(res.Content, "reply ")
	if count != bskyMaxReplies {
		t.Errorf("reply count in content = %d, want %d", count, bskyMaxReplies)
	}
	if !res.Truncated {
		t.Error("expected Truncated=true when replies exceed bskyMaxReplies")
	}
}

// TestScrapeBskyPost_NotFound verifies a missing post yields ErrNotFound.
func TestScrapeBskyPost_NotFound(t *testing.T) {
	t.Parallel()

	p := newBskyTestPipeline(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	_, err := p.Scrape(context.Background(), "https://bsky.app/profile/user.bsky.social/post/abc123", 4096)
	se, ok := err.(*ScrapeError)
	if !ok {
		t.Fatalf("error type %T, want *ScrapeError", err)
	}
	if se.Kind != ErrNotFound {
		t.Errorf("Kind = %v, want ErrNotFound", se.Kind)
	}
}

// TestScrapeBskyPost_RateLimit verifies a 429 yields ErrRateLimit.
func TestScrapeBskyPost_RateLimit(t *testing.T) {
	t.Parallel()

	p := newBskyTestPipeline(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})

	_, err := p.Scrape(context.Background(), "https://bsky.app/profile/user.bsky.social/post/abc123", 4096)
	se, ok := err.(*ScrapeError)
	if !ok {
		t.Fatalf("error type %T, want *ScrapeError", err)
	}
	if se.Kind != ErrRateLimit {
		t.Errorf("Kind = %v, want ErrRateLimit", se.Kind)
	}
}

// TestScrapeBskyPost_InvalidRkey verifies a malformed rkey is rejected before
// any request reaches the server.
func TestScrapeBskyPost_InvalidRkey(t *testing.T) {
	t.Parallel()

	p := newBskyTestPipeline(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be called for validation-rejected URL: %s", r.URL)
	})

	badURLs := []string{
		"https://bsky.app/profile/user.bsky.social/post/" + strings.Repeat("a", 513),
	}

	for _, rawURL := range badURLs {
		_, err := p.Scrape(context.Background(), rawURL, 4096)
		se, ok := err.(*ScrapeError)
		if !ok {
			t.Fatalf("Scrape(%q): error type %T, want *ScrapeError", rawURL, err)
		}
		if se.Kind != ErrValidation {
			t.Errorf("Scrape(%q): Kind = %v, want ErrValidation", rawURL, se.Kind)
		}
	}
}

// TestScrapeBskyPost_WithImageEmbed verifies image alt text appears in content.
func TestScrapeBskyPost_WithImageEmbed(t *testing.T) {
	t.Parallel()

	p := newBskyTestPipeline(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"thread": {
				"post": {
					"author": {"handle": "user.bsky.social"},
					"record": {
						"text": "check this out",
						"embed": {"images": [{"alt": "a scenic mountain view"}]}
					}
				},
				"replies": []
			}
		}`)
	})

	res, err := p.Scrape(context.Background(), "https://bsky.app/profile/user.bsky.social/post/abc123", 4096)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Content, "a scenic mountain view") {
		t.Errorf("image alt text missing from content: %q", res.Content)
	}
}

// TestScrapeBskyPost_WithExternalEmbed verifies external link embeds appear in content.
func TestScrapeBskyPost_WithExternalEmbed(t *testing.T) {
	t.Parallel()

	p := newBskyTestPipeline(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"thread": {
				"post": {
					"author": {"handle": "user.bsky.social"},
					"record": {
						"text": "read this",
						"embed": {"external": {"uri": "https://example.com/article", "title": "Example Article", "description": "A description"}}
					}
				},
				"replies": []
			}
		}`)
	})

	res, err := p.Scrape(context.Background(), "https://bsky.app/profile/user.bsky.social/post/abc123", 4096)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Content, "https://example.com/article") {
		t.Errorf("external embed URI missing from content: %q", res.Content)
	}
	if !strings.Contains(res.Content, "Example Article") {
		t.Errorf("external embed title missing from content: %q", res.Content)
	}
}

// TestScrapeBskyProfile verifies a user profile is fetched and formatted.
func TestScrapeBskyProfile(t *testing.T) {
	t.Parallel()

	p := newBskyTestPipeline(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"handle": "user.bsky.social",
			"displayName": "Test User",
			"description": "A bio here",
			"followersCount": 100,
			"followsCount": 50,
			"postsCount": 25,
			"createdAt": "2024-01-01T00:00:00Z"
		}`)
	})

	res, err := p.Scrape(context.Background(), "https://bsky.app/profile/user.bsky.social", 4096)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ContentType != "bluesky" {
		t.Errorf("ContentType = %q, want %q", res.ContentType, "bluesky")
	}
	if res.Tier != "bluesky:api" {
		t.Errorf("Tier = %q, want %q", res.Tier, "bluesky:api")
	}
	if !strings.Contains(res.Content, "100") {
		t.Errorf("follower count missing from content: %q", res.Content)
	}
}

// TestScrapeBskyProfile_NotFound verifies a missing profile yields ErrNotFound.
func TestScrapeBskyProfile_NotFound(t *testing.T) {
	t.Parallel()

	p := newBskyTestPipeline(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	_, err := p.Scrape(context.Background(), "https://bsky.app/profile/user.bsky.social", 4096)
	se, ok := err.(*ScrapeError)
	if !ok {
		t.Fatalf("error type %T, want *ScrapeError", err)
	}
	if se.Kind != ErrNotFound {
		t.Errorf("Kind = %v, want ErrNotFound", se.Kind)
	}
}

// TestScrapeBskyUnknownPath verifies an unrecognized bsky.app path falls
// through to the tiered HTML pipeline rather than calling the AT Protocol API.
func TestScrapeBskyUnknownPath(t *testing.T) {
	t.Parallel()

	apiCalled := false
	p := newBskyTestPipeline(t, func(w http.ResponseWriter, r *http.Request) {
		apiCalled = true
		w.WriteHeader(http.StatusOK)
	})

	// The fallback pipeline will attempt real network access to bsky.app,
	// which is expected to fail in this sandboxed test environment; only the
	// apiCalled assertion (that scrapeBsky did not treat this as a post/profile
	// path) matters here.
	_, _ = p.Scrape(context.Background(), "https://bsky.app/search?q=test", 4096)

	if apiCalled {
		t.Error("AT Protocol API should not be called for an unrecognized bsky.app path")
	}
}
