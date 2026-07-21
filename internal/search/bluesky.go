package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

var _ Provider = (*BlueskyProvider)(nil)

// bskyFallbackBaseURL is the Bluesky AppView's uncached host. Same backend as
// public.api.bsky.app (both documented at
// https://docs.bsky.app/docs/advanced-guides/api-directory), reached only
// when the cached host 403s searchPosts — observed in production as
// deliberate, intermittent load-shedding on that one endpoint (see
// https://github.com/bluesky-social/bsky-docs/issues/332), not an outage of
// the AppView itself. Var, not const, so tests can point it at an httptest
// server instead of the real network.
var bskyFallbackBaseURL = "https://api.bsky.app"

// BlueskyProvider searches Bluesky posts via the public AT Protocol AppView
// API. No API key required — works as a zero-config provider. Circuit
// breaking is handled at the Router layer (see internal/search/router.go),
// not in-provider.
type BlueskyProvider struct {
	client  *http.Client
	baseURL string
}

type bskySearchResponse struct {
	Posts []bskyPost `json:"posts"`
}

type bskyPost struct {
	URI         string     `json:"uri"`
	Author      bskyAuthor `json:"author"`
	Record      bskyRecord `json:"record"`
	ReplyCount  int        `json:"replyCount"`
	RepostCount int        `json:"repostCount"`
	LikeCount   int        `json:"likeCount"`
	IndexedAt   string     `json:"indexedAt"`
}

type bskyAuthor struct {
	Handle      string `json:"handle"`
	DisplayName string `json:"displayName"`
}

type bskyRecord struct {
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt"`
}

func NewBlueskyProvider(deps Deps) *BlueskyProvider {
	return &BlueskyProvider{client: deps.HTTPClient, baseURL: "https://public.api.bsky.app"}
}

func (p *BlueskyProvider) Name() string { return "bluesky" }

// isBskyForbidden reports whether err is the HTTP 403 response wrapped by
// searchPosts — the signal that the cached AppView host is load-shedding
// this endpoint and the uncached fallback host should be tried.
func isBskyForbidden(err error) bool {
	return strings.Contains(err.Error(), "HTTP 403")
}

// searchPosts calls app.bsky.feed.searchPosts against base and returns the
// size-bounded response body.
func (p *BlueskyProvider) searchPosts(ctx context.Context, base string, qp url.Values) ([]byte, error) {
	reqURL := base + "/xrpc/app.bsky.feed.searchPosts?" + qp.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("bluesky: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bluesky: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("bluesky: rate limited: %w", circuit.ErrRateLimit)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bluesky: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("bluesky: %w", err)
	}
	return body, nil
}

func (p *BlueskyProvider) Web(ctx context.Context, params WebSearchParams) ([]SearchResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	n := params.NumResults
	if n <= 0 || n > 100 {
		n = 10
	}

	qp := url.Values{}
	qp.Set("q", params.Query)
	qp.Set("limit", strconv.Itoa(n))

	body, err := p.searchPosts(ctx, p.baseURL, qp)
	if err != nil && p.baseURL != bskyFallbackBaseURL && isBskyForbidden(err) {
		// The cached AppView host load-sheds searchPosts by 403'ing it while
		// every other endpoint keeps working — retry once against the same
		// backend's uncached host before giving up.
		body, err = p.searchPosts(ctx, bskyFallbackBaseURL, qp)
	}
	if err != nil {
		return nil, err
	}

	var searchResp bskySearchResponse
	if err := json.Unmarshal(body, &searchResp); err != nil {
		return nil, fmt.Errorf("bluesky: %w", err)
	}

	now := time.Now()
	results := make([]SearchResult, 0, len(searchResp.Posts))
	for _, post := range searchResp.Posts {
		author := post.Author.Handle
		if post.Author.DisplayName != "" {
			author = fmt.Sprintf("%s (@%s)", post.Author.DisplayName, post.Author.Handle)
		}
		var eng *EngagementSignals
		if post.LikeCount > 0 || post.RepostCount > 0 || post.ReplyCount > 0 {
			eng = &EngagementSignals{
				LikeCount:   post.LikeCount,
				RepostCount: post.RepostCount,
				ReplyCount:  post.ReplyCount,
			}
		}
		results = append(results, SearchResult{
			Title: truncateText(post.Record.Text, 120),
			URL:   atURIToHTTPS(post.URI),
			Snippet: fmt.Sprintf("%d likes · %d reposts · %d replies · by %s · %s",
				post.LikeCount, post.RepostCount, post.ReplyCount, author, publishedDate(post.Record.CreatedAt)),
			DisplayLink: "bsky.app",
			PublishedAt: normalizePublishedAt(post.Record.CreatedAt, now),
			Engagement:  eng,
		})
	}
	return results, nil
}

func (p *BlueskyProvider) Images(_ context.Context, _ ImageSearchParams) ([]ImageResult, error) {
	return nil, nil
}

func (p *BlueskyProvider) News(_ context.Context, _ NewsSearchParams) ([]NewsResult, error) {
	return nil, nil
}

// atURIToHTTPS converts an AT Protocol post URI
// (at://did:plc:<did>/app.bsky.feed.post/<rkey>) to a canonical
// https://bsky.app/profile/<did>/post/<rkey> URL. Malformed or non-post URIs
// pass through unchanged.
func atURIToHTTPS(uri string) string {
	const prefix = "at://"
	if !strings.HasPrefix(uri, prefix) {
		return uri
	}
	rest := uri[len(prefix):]
	did, collectionAndRkey, ok := strings.Cut(rest, "/")
	if !ok || did == "" {
		return uri
	}
	const postCollection = "app.bsky.feed.post/"
	if !strings.HasPrefix(collectionAndRkey, postCollection) {
		return uri
	}
	rkey := collectionAndRkey[len(postCollection):]
	if rkey == "" {
		return uri
	}
	return fmt.Sprintf("https://bsky.app/profile/%s/post/%s", did, rkey)
}
