package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newBlueskyTestProvider(t *testing.T, handler http.HandlerFunc) *BlueskyProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &BlueskyProvider{client: srv.Client(), baseURL: srv.URL}
}

const bskySampleResponse = `{"posts":[
	{"uri":"at://did:plc:abc123/app.bsky.feed.post/xyz456","author":{"handle":"user.bsky.social","displayName":"Alice"},"record":{"text":"Hello Bluesky world","createdAt":"2024-06-01T12:00:00Z"},"likeCount":10,"repostCount":2,"replyCount":5,"indexedAt":"2024-06-01T12:00:00Z"},
	{"uri":"at://did:plc:def456/app.bsky.feed.post/abc789","author":{"handle":"anon.bsky.social","displayName":""},"record":{"text":"Another post","createdAt":"2024-06-02T00:00:00Z"},"likeCount":0,"repostCount":0,"replyCount":0,"indexedAt":"2024-06-02T00:00:00Z"}
]}`

func TestBlueskyProviderName(t *testing.T) {
	t.Parallel()
	p := &BlueskyProvider{client: http.DefaultClient, baseURL: "x"}
	if p.Name() != "bluesky" {
		t.Errorf("Name() = %q, want %q", p.Name(), "bluesky")
	}
}

func TestBlueskyProviderWeb(t *testing.T) {
	t.Parallel()
	p := newBlueskyTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(bskySampleResponse))
	})
	results, err := p.Web(context.Background(), WebSearchParams{Query: "golang", NumResults: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if results[0].URL != "https://bsky.app/profile/did:plc:abc123/post/xyz456" {
		t.Errorf("results[0].URL = %q, want converted AT URI", results[0].URL)
	}
	if results[0].DisplayLink != "bsky.app" {
		t.Errorf("results[0].DisplayLink = %q, want bsky.app", results[0].DisplayLink)
	}
	if !strings.Contains(results[0].Snippet, "10 likes") {
		t.Errorf("results[0].Snippet should contain like count, got %q", results[0].Snippet)
	}
	if results[0].Engagement == nil || results[0].Engagement.LikeCount != 10 {
		t.Errorf("results[0].Engagement = %+v, want LikeCount=10", results[0].Engagement)
	}
	if results[1].Engagement != nil {
		t.Errorf("results[1].Engagement should be nil when all counts are zero, got %+v", results[1].Engagement)
	}
}

func TestBlueskyProviderWeb_DisplayNameFallback(t *testing.T) {
	t.Parallel()
	p := newBlueskyTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(bskySampleResponse))
	})
	results, err := p.Web(context.Background(), WebSearchParams{Query: "golang", NumResults: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(results[0].Snippet, "Alice (@user.bsky.social)") {
		t.Errorf("results[0].Snippet should contain displayName+handle, got %q", results[0].Snippet)
	}
	if !strings.Contains(results[1].Snippet, "anon.bsky.social") {
		t.Errorf("results[1].Snippet should contain handle, got %q", results[1].Snippet)
	}
	if strings.Contains(results[1].Snippet, "(@anon.bsky.social)") {
		t.Errorf("results[1].Snippet should not wrap bare handle in displayName format, got %q", results[1].Snippet)
	}
}

func TestBlueskyProviderImages(t *testing.T) {
	t.Parallel()
	called := false
	p := newBlueskyTestProvider(t, func(w http.ResponseWriter, r *http.Request) { called = true })
	imgs, err := p.Images(context.Background(), ImageSearchParams{Query: "test", NumResults: 5})
	if err != nil {
		t.Errorf("Images() should return nil error, got %v", err)
	}
	if imgs != nil {
		t.Errorf("Images() should return nil slice, got %v", imgs)
	}
	if called {
		t.Error("Images() should not make an HTTP call")
	}
}

func TestBlueskyProviderNews(t *testing.T) {
	t.Parallel()
	called := false
	p := newBlueskyTestProvider(t, func(w http.ResponseWriter, r *http.Request) { called = true })
	news, err := p.News(context.Background(), NewsSearchParams{Query: "test", NumResults: 5})
	if err != nil {
		t.Errorf("News() should return nil error, got %v", err)
	}
	if news != nil {
		t.Errorf("News() should return nil slice, got %v", news)
	}
	if called {
		t.Error("News() should not make an HTTP call")
	}
}

func TestBluesky429(t *testing.T) {
	t.Parallel()
	p := newBlueskyTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := p.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err == nil {
		t.Fatal("expected error on HTTP 429, got nil")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("error message should mention rate limit, got %q", err.Error())
	}
}

func TestBlueskyProviderNonOK(t *testing.T) {
	t.Parallel()
	p := newBlueskyTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, err := p.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err == nil {
		t.Fatal("expected error on HTTP 503, got nil")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error message should mention 503, got %q", err.Error())
	}
}

func TestBlueskyProviderZeroHits(t *testing.T) {
	t.Parallel()
	p := newBlueskyTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"posts":[]}`))
	})
	results, err := p.Web(context.Background(), WebSearchParams{Query: "xyzzy123", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error on zero hits: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("want 0 results, got %d", len(results))
	}
}

func TestBlueskyProviderNumResultsClamp(t *testing.T) {
	t.Parallel()
	cases := []struct {
		numResults int
		wantLimit  string
	}{
		{0, "10"},
		{5, "5"},
		{200, "10"},
	}
	for _, c := range cases {
		var gotLimit string
		p := newBlueskyTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
			gotLimit = r.URL.Query().Get("limit")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"posts":[]}`))
		})
		_, err := p.Web(context.Background(), WebSearchParams{Query: "test", NumResults: c.numResults})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotLimit != c.wantLimit {
			t.Errorf("NumResults=%d: limit param = %q, want %q", c.numResults, gotLimit, c.wantLimit)
		}
	}
}

func TestBlueskyProviderInterface(t *testing.T) {
	t.Parallel()
	var _ Provider = (*BlueskyProvider)(nil)
}

func TestBlueskyATURIToHTTPS(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"valid AT URI", "at://did:plc:abc123/app.bsky.feed.post/xyz456", "https://bsky.app/profile/did:plc:abc123/post/xyz456"},
		{"non-at passthrough", "https://example.com/foo", "https://example.com/foo"},
		{"no slash passthrough", "at://did:plc:abc123", "at://did:plc:abc123"},
		{"non-post collection passthrough", "at://did:plc:abc123/app.bsky.feed.like/xyz456", "at://did:plc:abc123/app.bsky.feed.like/xyz456"},
		{"empty string passthrough", "", ""},
		{"empty rkey passthrough", "at://did:plc:abc123/app.bsky.feed.post/", "at://did:plc:abc123/app.bsky.feed.post/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := atURIToHTTPS(c.in); got != c.want {
				t.Errorf("atURIToHTTPS(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
