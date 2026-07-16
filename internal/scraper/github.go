package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// githubRawBaseURL serves raw file bytes with no API rate-limit bucket at all
// (verified live 2026-07-15) — the cheapest, fastest path for README/file
// content when the ref/path is already known or derivable from the URL.
const githubRawBaseURL = "https://raw.githubusercontent.com"

// githubAPIBaseURL is GitHub's REST API, used only as a fallback (README
// filename/branch resolution) and for gists (no raw-CDN equivalent).
const githubAPIBaseURL = "https://api.github.com"

// githubScrapeMaxBytes bounds every GitHub content fetch (raw file, README,
// gist). Generous enough for real READMEs/source files while still bounding
// memory on an unexpectedly large response (issue #396 rule 4.1).
const githubScrapeMaxBytes = 2 * 1024 * 1024

// githubGistIDRe matches a gist ID: GitHub gist IDs are lowercase hex strings.
var githubGistIDRe = regexp.MustCompile(`^[0-9a-f]{6,40}$`)

// reservedGitHubPaths are github.com top-level paths that are never a real
// username/org (navigation, settings, marketplace, etc.). Without this guard
// a two-segment path like "github.com/settings/profile" would be misread as
// {owner}/{repo} and routed to a README fetch instead of falling through to
// the generic scraper.
var reservedGitHubPaths = map[string]bool{
	"about": true, "account": true, "apps": true, "blog": true, "business": true,
	"codespaces": true, "collections": true, "contact": true, "customer-stories": true,
	"dashboard": true, "developer": true, "enterprise": true, "explore": true,
	"features": true, "help": true, "home": true, "issues": true, "join": true,
	"login": true, "logout": true, "marketplace": true, "new": true, "notifications": true,
	"open-source": true, "orgs": true, "organizations": true, "plans": true, "pricing": true,
	"pulls": true, "search": true, "security": true, "settings": true, "site": true,
	"sitemap": true, "sponsors": true, "stars": true, "styleguide": true, "support": true,
	"team": true, "teams": true, "topics": true, "trending": true, "watching": true,
}

// isGitHubContentURL reports whether rawURL is on github.com or
// gist.github.com. Path-shape matching (README/blob/gist vs. everything else)
// happens in scrapeGitHubContent, mirroring isHNURL/scrapeHN.
func isGitHubContentURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	return host == "github.com" || host == "gist.github.com"
}

func (p *Pipeline) githubRawBase() string {
	if p.config.GitHubRawBase != "" {
		return p.config.GitHubRawBase
	}
	return githubRawBaseURL
}

func (p *Pipeline) githubAPIBase() string {
	if p.config.GitHubAPIBase != "" {
		return p.config.GitHubAPIBase
	}
	return githubAPIBaseURL
}

func splitGitHubPath(p string) []string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

// githubPathMatch is the pure routing decision for a github.com/gist.github.com
// URL, factored out of scrapeGitHubContent so it can be unit-tested without a
// network round trip through scrapeWithTieredFallback.
type githubPathMatch struct {
	kind   string // "readme", "blob", "gist", or "" (no match — fall through)
	owner  string
	repo   string
	ref    string
	path   string
	gistID string
}

// matchGitHubPath decides how (or whether) a github.com/gist.github.com path
// maps to a native content route. Returns a zero-value match (kind == "")
// for anything that should fall through to the generic tiered scraper:
// issues, PRs, wikis, reserved top-level navigation paths, and gist paths
// that aren't a bare ID.
func matchGitHubPath(host string, segments []string) githubPathMatch {
	if host == "gist.github.com" {
		var id string
		switch len(segments) {
		case 1:
			id = segments[0]
		case 2:
			id = segments[1]
		}
		if id != "" && githubGistIDRe.MatchString(id) {
			return githubPathMatch{kind: "gist", gistID: id}
		}
		return githubPathMatch{}
	}

	switch {
	case len(segments) == 2 && !reservedGitHubPaths[segments[0]]:
		return githubPathMatch{kind: "readme", owner: segments[0], repo: segments[1]}
	case len(segments) >= 5 && segments[2] == "blob" && !reservedGitHubPaths[segments[0]]:
		return githubPathMatch{
			kind:  "blob",
			owner: segments[0],
			repo:  segments[1],
			ref:   segments[3],
			path:  strings.Join(segments[4:], "/"),
		}
	default:
		return githubPathMatch{}
	}
}

// scrapeGitHubContent routes a github.com/gist.github.com URL to the README,
// blob, or gist handler based on path shape; anything else (issues, PRs,
// wikis, reserved top-level paths, unrecognized gist paths) falls through to
// the generic tiered HTML scraper.
func (p *Pipeline) scrapeGitHubContent(ctx context.Context, rawURL string, maxLength int) (*ScrapeResult, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, validationError(rawURL, "github", err, err.Error())
	}
	host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	segments := splitGitHubPath(u.Path)

	m := matchGitHubPath(host, segments)
	switch m.kind {
	case "readme":
		return p.scrapeGitHubReadme(ctx, rawURL, m.owner, m.repo, maxLength)
	case "blob":
		return p.scrapeGitHubBlob(ctx, rawURL, m.owner, m.repo, m.ref, m.path, maxLength)
	case "gist":
		return p.scrapeGitHubGist(ctx, rawURL, m.gistID, maxLength)
	default:
		return p.scrapeWithTieredFallback(ctx, rawURL, maxLength)
	}
}

// fetchGitHubRaw fetches from raw.githubusercontent.com — no auth, no API
// rate-limit bucket.
func (p *Pipeline) fetchGitHubRaw(ctx context.Context, rawContentURL string) (int, []byte, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, "GET", rawContentURL, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", "web-researcher-mcp/1.0")

	resp, err := p.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, githubScrapeMaxBytes))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

// fetchGitHubAPI fetches from api.github.com, sending the optional
// GitHubToken (never logged) to raise the unauthenticated core rate limit.
func (p *Pipeline) fetchGitHubAPI(ctx context.Context, apiURL string) (int, []byte, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, "GET", apiURL, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "web-researcher-mcp/1.0")
	if p.config.GitHubToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.config.GitHubToken) // never logged
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, githubScrapeMaxBytes))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

// scrapeGitHubReadme fetches a repo's README, preferring the zero-budget raw
// CDN path (HEAD resolves to the default branch) and falling back to the
// Contents API only when that 404s — e.g. a non-standard README filename or
// casing (readme.md, README.rst) that raw.githubusercontent.com's literal
// "README.md" path won't match.
func (p *Pipeline) scrapeGitHubReadme(ctx context.Context, rawURL, owner, repo string, maxLength int) (*ScrapeResult, error) {
	rawReadmeURL := fmt.Sprintf("%s/%s/%s/HEAD/README.md", p.githubRawBase(), url.PathEscape(owner), url.PathEscape(repo))
	status, body, err := p.fetchGitHubRaw(ctx, rawReadmeURL)
	if err != nil {
		return nil, networkError(rawURL, "github", err)
	}
	if status == 200 && len(body) > 0 {
		return p.buildGitHubReadmeResult(rawURL, owner, repo, string(body), maxLength, "github:raw"), nil
	}
	if status != 404 {
		return nil, classifyHTTPStatus(status, rawURL, "github")
	}

	apiURL := fmt.Sprintf("%s/repos/%s/%s/readme", p.githubAPIBase(), url.PathEscape(owner), url.PathEscape(repo))
	apiStatus, apiBody, err := p.fetchGitHubAPI(ctx, apiURL)
	if err != nil {
		return nil, networkError(rawURL, "github", err)
	}
	if apiStatus == 404 {
		return nil, notFoundError(rawURL, "github", 404)
	}
	if apiStatus != 200 {
		return nil, classifyHTTPStatus(apiStatus, rawURL, "github")
	}

	var meta struct {
		DownloadURL string `json:"download_url"`
	}
	if err := json.Unmarshal(apiBody, &meta); err != nil || meta.DownloadURL == "" {
		return nil, contentError(rawURL, "github: readme metadata missing download_url")
	}

	dlStatus, dlBody, err := p.fetchGitHubRaw(ctx, meta.DownloadURL)
	if err != nil {
		return nil, networkError(rawURL, "github", err)
	}
	if dlStatus != 200 {
		return nil, classifyHTTPStatus(dlStatus, rawURL, "github")
	}
	return p.buildGitHubReadmeResult(rawURL, owner, repo, string(dlBody), maxLength, "github:contents-api"), nil
}

func (p *Pipeline) buildGitHubReadmeResult(rawURL, owner, repo, body string, maxLength int, tier string) *ScrapeResult {
	truncated := false
	if len(body) > maxLength {
		body = truncateBytes(body, maxLength)
		truncated = true
	}
	res := &ScrapeResult{
		URL:         rawURL,
		Content:     body,
		ContentType: "github",
		Title:       fmt.Sprintf("%s/%s — README", owner, repo),
		Author:      owner,
		SiteName:    "GitHub",
		Truncated:   truncated,
	}
	return stampTier(res, tier)
}

// escapeGitHubPathSegments PathEscapes each "/"-separated segment of path
// independently, so a decoded segment containing a raw "#", "?", or "%" (from
// url.Parse's automatic percent-decoding) can't be misread as a URL fragment,
// query string, or escape sequence when reassembled into an outbound request
// (issue #396 rule 2.4). Re-joining with "/" preserves the path's shape.
func escapeGitHubPathSegments(path string) string {
	segments := strings.Split(path, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/")
}

// scrapeGitHubBlob fetches a specific file at a ref/path directly from the
// raw CDN — the ref and path are already known from the URL, so no API call
// is needed at all.
func (p *Pipeline) scrapeGitHubBlob(ctx context.Context, rawURL, owner, repo, ref, path string, maxLength int) (*ScrapeResult, error) {
	rawFileURL := fmt.Sprintf("%s/%s/%s/%s/%s", p.githubRawBase(), url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(ref), escapeGitHubPathSegments(path))
	status, body, err := p.fetchGitHubRaw(ctx, rawFileURL)
	if err != nil {
		return nil, networkError(rawURL, "github", err)
	}
	if status != 200 {
		return nil, classifyHTTPStatus(status, rawURL, "github")
	}

	content := string(body)
	truncated := false
	if len(content) > maxLength {
		content = truncateBytes(content, maxLength)
		truncated = true
	}

	res := &ScrapeResult{
		URL:         rawURL,
		Content:     content,
		ContentType: "github",
		Title:       fmt.Sprintf("%s/%s/%s", owner, repo, path),
		Author:      owner,
		SiteName:    "GitHub",
		Truncated:   truncated,
	}
	return stampTier(res, "github:raw"), nil
}

// scrapeGitHubGist fetches a gist via the Gist API and concatenates each
// file's content — the API returns raw file content directly with no
// HTML-chrome loss (gist.github.com renders a stripped-down code view).
func (p *Pipeline) scrapeGitHubGist(ctx context.Context, rawURL, gistID string, maxLength int) (*ScrapeResult, error) {
	apiURL := fmt.Sprintf("%s/gists/%s", p.githubAPIBase(), gistID)
	status, body, err := p.fetchGitHubAPI(ctx, apiURL)
	if err != nil {
		return nil, networkError(rawURL, "github", err)
	}
	if status == 404 {
		return nil, notFoundError(rawURL, "github", 404)
	}
	if status != 200 {
		return nil, classifyHTTPStatus(status, rawURL, "github")
	}

	var gist struct {
		Description string `json:"description"`
		Owner       struct {
			Login string `json:"login"`
		} `json:"owner"`
		Files map[string]struct {
			Filename string `json:"filename"`
			Content  string `json:"content"`
		} `json:"files"`
	}
	if err := json.Unmarshal(body, &gist); err != nil {
		return nil, contentError(rawURL, "github: gist parse error: "+err.Error())
	}
	if len(gist.Files) == 0 {
		return nil, notFoundError(rawURL, "github", 404)
	}

	names := make([]string, 0, len(gist.Files))
	for name := range gist.Files {
		names = append(names, name)
	}
	sort.Strings(names)

	var sb strings.Builder
	for _, name := range names {
		f := gist.Files[name]
		fmt.Fprintf(&sb, "## %s\n\n```\n%s\n```\n\n", f.Filename, f.Content)
	}

	content := sb.String()
	truncated := false
	if len(content) > maxLength {
		content = truncateBytes(content, maxLength)
		truncated = true
	}

	title := gist.Description
	if title == "" {
		title = "Gist by " + gist.Owner.Login
	}

	res := &ScrapeResult{
		URL:         rawURL,
		Content:     content,
		ContentType: "github",
		Title:       title,
		Author:      gist.Owner.Login,
		SiteName:    "GitHub Gist",
		Truncated:   truncated,
	}
	return stampTier(res, "github:gist-api"), nil
}
