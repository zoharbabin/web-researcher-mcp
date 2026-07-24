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

// bskyMaxReplies bounds how many top-level replies scrapeBskyPost includes in
// the formatted content, mirroring hnMaxComments' role for HN items.
const bskyMaxReplies = 5

// bskyRkeyRe validates the AT Protocol record key segment of a post URL.
var bskyRkeyRe = regexp.MustCompile(`^[a-zA-Z0-9._~-]{1,512}$`)

// bskyHandleRe validates a Bluesky handle (domain-shaped) or a DID identifier.
var bskyHandleRe = regexp.MustCompile(`^([a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}$`)
var bskyDIDRe = regexp.MustCompile(`^did:[a-zA-Z0-9]+:[a-zA-Z0-9._%-]{1,256}$`)

type bskyEmbedRecord struct {
	Images []struct {
		Alt string `json:"alt"`
	} `json:"images,omitempty"`
	External *struct {
		URI         string `json:"uri"`
		Title       string `json:"title"`
		Description string `json:"description"`
	} `json:"external,omitempty"`
}

type bskyPostNode struct {
	Author struct {
		Handle      string `json:"handle"`
		DisplayName string `json:"displayName"`
	} `json:"author"`
	Record struct {
		Text      string           `json:"text"`
		CreatedAt string           `json:"createdAt"`
		Embed     *bskyEmbedRecord `json:"embed,omitempty"`
	} `json:"record"`
	LikeCount   int `json:"likeCount"`
	RepostCount int `json:"repostCount"`
	ReplyCount  int `json:"replyCount"`
}

type bskyThreadResponse struct {
	Thread struct {
		Post    bskyPostNode `json:"post"`
		Replies []struct {
			Post bskyPostNode `json:"post"`
		} `json:"replies"`
	} `json:"thread"`
}

type bskyProfileResponse struct {
	Handle         string `json:"handle"`
	DisplayName    string `json:"displayName"`
	Description    string `json:"description"`
	FollowersCount int    `json:"followersCount"`
	FollowsCount   int    `json:"followsCount"`
	PostsCount     int    `json:"postsCount"`
	CreatedAt      string `json:"createdAt"`
}

// bskyAPIBase returns the AT Protocol public API base URL, or the test
// override from PipelineConfig.BskyAPIBase.
func (p *Pipeline) bskyAPIBase() string {
	if p.config.BskyAPIBase != "" {
		return p.config.BskyAPIBase
	}
	return "https://public.api.bsky.app/xrpc"
}

// isBskyURL reports whether rawURL is a Bluesky URL.
func isBskyURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	return host == "bsky.app"
}

// bskyPostURLToATURI parses a bsky.app post URL path of the shape
// /profile/{handle}/post/{rkey} and returns its components.
func bskyPostURLToATURI(rawURL string) (handle, rkey string, ok bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", false
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) != 4 || segments[0] != "profile" || segments[2] != "post" {
		return "", "", false
	}
	if segments[1] == "" || segments[3] == "" {
		return "", "", false
	}
	return segments[1], segments[3], true
}

// bskyProfileURL parses a bsky.app profile URL path of the shape
// /profile/{handle} with no further segments.
func bskyProfileURL(rawURL string) (handle string, ok bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) != 2 || segments[0] != "profile" || segments[1] == "" {
		return "", false
	}
	return segments[1], true
}

// scrapeBsky routes a bsky.app URL to the native post or profile handler, or
// falls through to the tiered HTML pipeline for unrecognized paths.
func (p *Pipeline) scrapeBsky(ctx context.Context, rawURL string, maxLength int) (*ScrapeResult, error) {
	if handle, rkey, ok := bskyPostURLToATURI(rawURL); ok {
		return p.scrapeBskyPost(ctx, rawURL, handle, rkey, maxLength)
	}
	if handle, ok := bskyProfileURL(rawURL); ok {
		return p.scrapeBskyProfile(ctx, rawURL, handle, maxLength)
	}
	return p.scrapeWithTieredFallback(ctx, rawURL, maxLength)
}

// bskyGet performs a GET against the AT Protocol public API and returns the
// size-bounded response body, classifying non-200 statuses into ScrapeError.
func (p *Pipeline) bskyGet(ctx context.Context, endpoint string, query url.Values) ([]byte, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	reqURL := p.bskyAPIBase() + "/" + endpoint + "?" + query.Encode()

	req, err := http.NewRequestWithContext(fetchCtx, "GET", reqURL, nil)
	if err != nil {
		return nil, networkError(reqURL, "bluesky", err)
	}
	req.Header.Set("User-Agent", "web-researcher-mcp/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, networkError(reqURL, "bluesky", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, rateLimitError(reqURL, "bluesky")
	}
	if resp.StatusCode == 400 || resp.StatusCode == 404 {
		return nil, notFoundError(reqURL, "bluesky", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return nil, classifyHTTPStatus(resp.StatusCode, reqURL, "bluesky")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return nil, networkError(reqURL, "bluesky", err)
	}
	return body, nil
}

// scrapeBskyPost fetches a post thread via app.bsky.feed.getPostThread and
// formats the post text, embed previews, engagement counts, and up to
// bskyMaxReplies top-level replies.
func (p *Pipeline) scrapeBskyPost(ctx context.Context, rawURL, handle, rkey string, maxLength int) (*ScrapeResult, error) {
	if !bskyRkeyRe.MatchString(rkey) {
		return nil, validationError(rawURL, "bluesky", nil, "rkey must match ^[a-zA-Z0-9._~-]{1,512}$")
	}
	if !bskyHandleRe.MatchString(handle) && !bskyDIDRe.MatchString(handle) {
		return nil, validationError(rawURL, "bluesky", nil, "handle must be a valid Bluesky handle or DID")
	}

	atURI := fmt.Sprintf("at://%s/app.bsky.feed.post/%s", handle, rkey)
	query := url.Values{}
	query.Set("uri", atURI)

	body, err := p.bskyGet(ctx, "app.bsky.feed.getPostThread", query)
	if err != nil {
		return nil, err
	}

	var thread bskyThreadResponse
	if err := json.Unmarshal(body, &thread); err != nil {
		return nil, networkError(rawURL, "bluesky", err)
	}

	post := thread.Thread.Post
	if post.Author.Handle == "" {
		return nil, notFoundError(rawURL, "bluesky", 404)
	}

	author := post.Author.Handle
	if post.Author.DisplayName != "" {
		author = fmt.Sprintf("%s (@%s)", post.Author.DisplayName, post.Author.Handle)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# Post by %s\n\n%s\n\n", author, post.Record.Text)
	fmt.Fprintf(&sb, "**Likes:** %d | **Reposts:** %d | **Replies:** %d | **Posted:** %s\n",
		post.LikeCount, post.RepostCount, post.ReplyCount, post.Record.CreatedAt)

	if embed := post.Record.Embed; embed != nil {
		for _, img := range embed.Images {
			if img.Alt != "" {
				fmt.Fprintf(&sb, "\n**Image:** %s\n", img.Alt)
			}
		}
		if embed.External != nil {
			fmt.Fprintf(&sb, "\n**Link:** %s\n%s\n%s\n", embed.External.Title, embed.External.URI, embed.External.Description)
		}
	}

	replies := thread.Thread.Replies
	take := len(replies)
	if take > bskyMaxReplies {
		take = bskyMaxReplies
	}
	if take > 0 {
		sb.WriteString("\n## Top Replies\n")
		for _, r := range replies[:take] {
			rAuthor := r.Post.Author.Handle
			if r.Post.Author.DisplayName != "" {
				rAuthor = fmt.Sprintf("%s (@%s)", r.Post.Author.DisplayName, r.Post.Author.Handle)
			}
			fmt.Fprintf(&sb, "\n### %s\n\n%s\n\n---", rAuthor, r.Post.Record.Text)
		}
	}

	res := &ScrapeResult{
		URL:         rawURL,
		Content:     truncateBytes(sb.String(), maxLength),
		ContentType: "bluesky",
		Title:       fmt.Sprintf("Bluesky post by %s", author),
		Author:      author,
		SiteName:    "Bluesky",
		PublishDate: post.Record.CreatedAt,
		Truncated:   len(replies) > bskyMaxReplies,
		ForumSignals: &ForumSignals{
			Platform:   "bluesky",
			Upvotes:    post.LikeCount,
			Comments:   post.ReplyCount,
			AuthorName: author,
		},
	}
	return stampTier(res, "bluesky:api"), nil
}

// scrapeBskyProfile fetches a user profile via app.bsky.actor.getProfile and
// formats display name, bio, and follower/follows/posts counts.
func (p *Pipeline) scrapeBskyProfile(ctx context.Context, rawURL, handle string, maxLength int) (*ScrapeResult, error) {
	if !bskyHandleRe.MatchString(handle) && !bskyDIDRe.MatchString(handle) {
		return nil, validationError(rawURL, "bluesky", nil, "handle must be a valid Bluesky handle or DID")
	}

	query := url.Values{}
	query.Set("actor", handle)

	body, err := p.bskyGet(ctx, "app.bsky.actor.getProfile", query)
	if err != nil {
		return nil, err
	}

	var profile bskyProfileResponse
	if err := json.Unmarshal(body, &profile); err != nil {
		return nil, networkError(rawURL, "bluesky", err)
	}
	if profile.Handle == "" {
		return nil, notFoundError(rawURL, "bluesky", 404)
	}

	name := profile.Handle
	if profile.DisplayName != "" {
		name = fmt.Sprintf("%s (@%s)", profile.DisplayName, profile.Handle)
	}

	content := fmt.Sprintf("# %s\n\n%s\n\n**Followers:** %d | **Following:** %d | **Posts:** %d | **Joined:** %s",
		name, profile.Description, profile.FollowersCount, profile.FollowsCount, profile.PostsCount, profile.CreatedAt)

	res := &ScrapeResult{
		URL:         rawURL,
		Content:     truncateBytes(content, maxLength),
		ContentType: "bluesky",
		Title:       name,
		Author:      name,
		SiteName:    "Bluesky",
		PublishDate: profile.CreatedAt,
	}
	return stampTier(res, "bluesky:api"), nil
}
