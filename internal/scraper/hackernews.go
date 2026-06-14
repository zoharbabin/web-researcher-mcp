package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	hnMaxComments  = 10
	hnMaxListItems = 20
)

var hnStripTagRe = regexp.MustCompile(`<[^>]+>`)

type hnItem struct {
	ID          int    `json:"id"`
	Type        string `json:"type"`
	By          string `json:"by"`
	Time        int64  `json:"time"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Text        string `json:"text"`
	Score       int    `json:"score"`
	Descendants int    `json:"descendants"`
	Kids        []int  `json:"kids"`
	Dead        bool   `json:"dead"`
	Deleted     bool   `json:"deleted"`
}

type hnUser struct {
	ID      string `json:"id"`
	Created int64  `json:"created"`
	Karma   int    `json:"karma"`
	About   string `json:"about"`
}

func (p *Pipeline) hnFirebaseBase() string {
	if p.config.HNFirebaseBase != "" {
		return p.config.HNFirebaseBase
	}
	return "https://hacker-news.firebaseio.com/v0"
}

// isHNURL reports whether rawURL is a Hacker News URL.
func isHNURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	return host == "news.ycombinator.com"
}

// scrapeHN routes a HN URL to the appropriate handler.
func (p *Pipeline) scrapeHN(ctx context.Context, rawURL string, maxLength int) (*ScrapeResult, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, validationError(rawURL, "hackernews", err, err.Error())
	}

	path := strings.TrimRight(u.Path, "/")

	switch {
	case path == "/item" && u.Query().Get("id") != "":
		return p.scrapeHNItem(ctx, rawURL, u, maxLength)
	case path == "/user" && u.Query().Get("id") != "":
		return p.scrapeHNUser(ctx, rawURL, u, maxLength)
	case path == "/newest":
		return p.scrapeHNList(ctx, rawURL, "newstories.json", "Newest", maxLength)
	case path == "/best":
		return p.scrapeHNList(ctx, rawURL, "beststories.json", "Best", maxLength)
	case path == "/ask":
		return p.scrapeHNList(ctx, rawURL, "askstories.json", "Ask HN", maxLength)
	case path == "/show":
		return p.scrapeHNList(ctx, rawURL, "showstories.json", "Show HN", maxLength)
	case path == "/jobs":
		return p.scrapeHNList(ctx, rawURL, "jobstories.json", "Jobs", maxLength)
	case path == "" || path == "/":
		return p.scrapeHNList(ctx, rawURL, "topstories.json", "Top Stories", maxLength)
	default:
		return p.scrapeWithTieredFallback(ctx, rawURL, maxLength)
	}
}

// fetchHNItem fetches a single HN item by ID from the Firebase API.
func (p *Pipeline) fetchHNItem(ctx context.Context, id int64) (*hnItem, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	itemURL := p.hnFirebaseBase() + "/item/" + strconv.FormatInt(id, 10) + ".json"

	req, err := http.NewRequestWithContext(fetchCtx, "GET", itemURL, nil)
	if err != nil {
		return nil, networkError(itemURL, "hackernews", err)
	}
	req.Header.Set("User-Agent", "web-researcher-mcp/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, networkError(itemURL, "hackernews", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, rateLimitError(itemURL, "hackernews")
	}
	if resp.StatusCode != 200 {
		return nil, classifyHTTPStatus(resp.StatusCode, itemURL, "hackernews")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return nil, networkError(itemURL, "hackernews", err)
	}

	if strings.TrimSpace(string(body)) == "null" {
		return nil, nil
	}

	var item hnItem
	if err := json.Unmarshal(body, &item); err != nil {
		return nil, networkError(itemURL, "hackernews", err)
	}
	if item.ID == 0 {
		return nil, nil
	}
	return &item, nil
}

// scrapeHNItem fetches and formats a HN item (story + top comments).
func (p *Pipeline) scrapeHNItem(ctx context.Context, rawURL string, u *url.URL, maxLength int) (*ScrapeResult, error) {
	// 1. Validate and parse the id param.
	idStr := u.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id < 1 || id > 99999999999 {
		return nil, validationError(rawURL, "hackernews", nil, "id must be a positive integer")
	}

	// 2. Fetch the item.
	item, err := p.fetchHNItem(ctx, id)
	if err != nil {
		return nil, err
	}
	if item == nil || item.Dead || item.Deleted {
		return nil, notFoundError(rawURL, "hackernews", 404)
	}

	// 3. Parallel-fetch top comments.
	take := len(item.Kids)
	if take > hnMaxComments {
		take = hnMaxComments
	}

	type result struct {
		idx  int
		item *hnItem
	}

	var comments []*hnItem
	if take > 0 {
		ch := make(chan result, take)
		sem := make(chan struct{}, 5)

		for i := 0; i < take; i++ {
			i, kidID := i, item.Kids[i]
			go func() {
				sem <- struct{}{}
				defer func() { <-sem }()

				fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()

				kid, _ := p.fetchHNItem(fetchCtx, int64(kidID))
				ch <- result{i, kid}
			}()
		}

		ordered := make([]*hnItem, take)
		for range take {
			r := <-ch
			ordered[r.idx] = r.item
		}

		for _, c := range ordered {
			if c == nil || c.Dead || c.Deleted || c.Type != "comment" {
				continue
			}
			comments = append(comments, c)
		}
	}

	// 4. Build content.
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n\n", item.Title)
	fmt.Fprintf(&sb, "**Score:** %d | **Comments:** %d | **By:** %s | **Posted:** %s\n",
		item.Score, item.Descendants, item.By,
		time.Unix(item.Time, 0).UTC().Format(time.RFC822))
	if item.URL != "" {
		fmt.Fprintf(&sb, "**Link:** %s\n", item.URL)
	}
	fmt.Fprintf(&sb, "**HN Discussion:** https://news.ycombinator.com/item?id=%s\n", strconv.FormatInt(id, 10))

	if len(comments) > 0 {
		sb.WriteString("\n## Top Comments\n")
		for _, c := range comments {
			fmt.Fprintf(&sb, "\n### %s · %s\n\n%s\n\n---",
				c.By,
				time.Unix(c.Time, 0).UTC().Format(time.RFC822),
				stripHNHTML(c.Text))
		}
	}

	// 5. Build result.
	res := &ScrapeResult{
		URL:         rawURL,
		Content:     truncateBytes(sb.String(), maxLength),
		ContentType: "hackernews",
		Title:       item.Title,
		Author:      item.By,
		SiteName:    "Hacker News",
		PublishDate: time.Unix(item.Time, 0).UTC().Format(time.RFC822),
		Truncated:   len(item.Kids) > hnMaxComments,
	}
	return stampTier(res, "hackernews:api"), nil
}

// scrapeHNUser fetches and formats a HN user profile.
func (p *Pipeline) scrapeHNUser(ctx context.Context, rawURL string, u *url.URL, maxLength int) (*ScrapeResult, error) {
	// 1. Validate username.
	username := u.Query().Get("id")
	validUsername := regexp.MustCompile(`^[a-zA-Z0-9_-]{1,25}$`)
	if !validUsername.MatchString(username) {
		return nil, validationError(rawURL, "hackernews", nil, "username must be alphanumeric/hyphen/underscore, max 25 chars")
	}

	// 2. Fetch user.
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	userURL := p.hnFirebaseBase() + "/user/" + url.PathEscape(username) + ".json"

	req, err := http.NewRequestWithContext(fetchCtx, "GET", userURL, nil)
	if err != nil {
		return nil, networkError(userURL, "hackernews", err)
	}
	req.Header.Set("User-Agent", "web-researcher-mcp/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, networkError(userURL, "hackernews", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, rateLimitError(userURL, "hackernews")
	}
	if resp.StatusCode != 200 {
		return nil, classifyHTTPStatus(resp.StatusCode, userURL, "hackernews")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, networkError(userURL, "hackernews", err)
	}

	if strings.TrimSpace(string(body)) == "null" {
		return nil, notFoundError(rawURL, "hackernews", 404)
	}

	var usr hnUser
	if err := json.Unmarshal(body, &usr); err != nil {
		return nil, networkError(userURL, "hackernews", err)
	}
	if usr.ID == "" {
		return nil, notFoundError(rawURL, "hackernews", 404)
	}

	// 3. Format.
	content := fmt.Sprintf("# HN User: %s\n\n**Karma:** %d | **Member since:** %s\n\n## About\n\n%s",
		usr.ID,
		usr.Karma,
		time.Unix(usr.Created, 0).UTC().Format(time.RFC822),
		stripHNHTML(usr.About))

	res := &ScrapeResult{
		URL:         rawURL,
		Content:     truncateBytes(content, maxLength),
		ContentType: "hackernews",
		Title:       "HN User: " + usr.ID,
		Author:      usr.ID,
		SiteName:    "Hacker News",
		PublishDate: time.Unix(usr.Created, 0).UTC().Format(time.RFC822),
	}
	return stampTier(res, "hackernews:api"), nil
}

// scrapeHNList fetches and formats a HN story list (top/new/best/ask/show/jobs).
func (p *Pipeline) scrapeHNList(ctx context.Context, rawURL, listEndpoint, listName string, maxLength int) (*ScrapeResult, error) {
	// 1. Fetch story ID list.
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	listURL := p.hnFirebaseBase() + "/" + listEndpoint

	req, err := http.NewRequestWithContext(fetchCtx, "GET", listURL, nil)
	if err != nil {
		return nil, networkError(listURL, "hackernews", err)
	}
	req.Header.Set("User-Agent", "web-researcher-mcp/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, networkError(listURL, "hackernews", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, rateLimitError(listURL, "hackernews")
	}
	if resp.StatusCode != 200 {
		return nil, classifyHTTPStatus(resp.StatusCode, listURL, "hackernews")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return nil, networkError(listURL, "hackernews", err)
	}

	var ids []int
	if err := json.Unmarshal(body, &ids); err != nil {
		return nil, networkError(listURL, "hackernews", err)
	}

	// 2. Take first N IDs.
	take := len(ids)
	if take > hnMaxListItems {
		take = hnMaxListItems
	}
	truncated := len(ids) > hnMaxListItems

	// 3. Parallel-fetch items.
	type result struct {
		idx  int
		item *hnItem
	}

	ch := make(chan result, take)
	sem := make(chan struct{}, 5)

	for i := 0; i < take; i++ {
		i, storyID := i, ids[i]
		go func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			itemCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			item, _ := p.fetchHNItem(itemCtx, int64(storyID))
			ch <- result{i, item}
		}()
	}

	ordered := make([]*hnItem, take)
	for range take {
		r := <-ch
		ordered[r.idx] = r.item
	}

	// 4. Format content.
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Hacker News — %s\n", listName)

	counter := 1
	for _, item := range ordered {
		if item == nil || item.Dead || item.Deleted {
			continue
		}
		link := item.URL
		if link == "" {
			link = "https://news.ycombinator.com/item?id=" + strconv.Itoa(item.ID)
		}
		fmt.Fprintf(&sb, "\n%d. **%s** (%d pts, %d comments)\n",
			counter, item.Title, item.Score, item.Descendants)
		fmt.Fprintf(&sb, "   %s\n", link)
		fmt.Fprintf(&sb, "   By %s · %s",
			item.By,
			time.Unix(item.Time, 0).UTC().Format(time.RFC822))
		counter++
	}

	res := &ScrapeResult{
		URL:         rawURL,
		Content:     truncateBytes(sb.String(), maxLength),
		ContentType: "hackernews",
		Title:       "Hacker News — " + listName,
		SiteName:    "Hacker News",
		Truncated:   truncated,
	}
	return stampTier(res, "hackernews:api"), nil
}

// stripHNHTML strips HTML tags from HN text fields and unescapes HTML entities.
func stripHNHTML(s string) string {
	s = hnStripTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s)
}
