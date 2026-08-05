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
	"strconv"
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

// githubAPIResult is the outcome of a single api.github.com request,
// including the raw "Link" response header (RFC 5988) the contributor/
// release-count page-number trick needs (issue #546 rule 4.2).
type githubAPIResult struct {
	status int
	body   []byte
	link   string
}

// doGitHubAPIRequest is the single shared low-level request path for every
// api.github.com call — README-fallback, gist, and the trust-signal endpoints
// (issue #546) all go through this so every caller inherits the same
// SSRF-safe client, 10s timeout, size cap, and never-logged token handling.
func (p *Pipeline) doGitHubAPIRequest(ctx context.Context, apiURL string) (githubAPIResult, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, "GET", apiURL, nil)
	if err != nil {
		return githubAPIResult{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "web-researcher-mcp/1.0")
	if p.config.GitHubToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.config.GitHubToken) // never logged
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return githubAPIResult{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, githubScrapeMaxBytes))
	if err != nil {
		return githubAPIResult{status: resp.StatusCode}, err
	}
	return githubAPIResult{status: resp.StatusCode, body: body, link: resp.Header.Get("Link")}, nil
}

// fetchGitHubAPI fetches from api.github.com, sending the optional
// GitHubToken (never logged) to raise the unauthenticated core rate limit.
func (p *Pipeline) fetchGitHubAPI(ctx context.Context, apiURL string) (int, []byte, error) {
	res, err := p.doGitHubAPIRequest(ctx, apiURL)
	return res.status, res.body, err
}

// githubAPIRetryBackoffs bounds each trust-signal API call (org/user,
// contributors, community profile, releases) to at most 1+len(...) attempts,
// modeled on linkverify.go's spnRetryBackoffs pattern (issue #546 rule 3.1) —
// a fixed small slice of durations, never an unbounded loop.
var githubAPIRetryBackoffs = []time.Duration{500 * time.Millisecond, 1500 * time.Millisecond}

// fetchGitHubAPIWithRetry calls doGitHubAPIRequest, retrying up to
// len(githubAPIRetryBackoffs) additional times on a transport error or a 5xx
// response — never on 4xx. A 403 (rate limit or forbidden) or 404 will not
// succeed on retry, and retrying a rate-limited response risks exhausting the
// remaining request budget (issue #546 rule 4.3), so it degrades immediately
// instead of retrying.
func (p *Pipeline) fetchGitHubAPIWithRetry(ctx context.Context, apiURL string) (githubAPIResult, error) {
	var res githubAPIResult
	var err error
	for attempt := 0; ; attempt++ {
		res, err = p.doGitHubAPIRequest(ctx, apiURL)
		if err == nil && res.status < 500 {
			return res, err
		}
		if attempt >= len(githubAPIRetryBackoffs) {
			return res, err
		}
		select {
		case <-ctx.Done():
			return res, err
		case <-time.After(githubAPIRetryBackoffs[attempt]):
		}
	}
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
		return p.buildGitHubReadmeResult(ctx, rawURL, owner, repo, string(body), maxLength, "github:raw"), nil
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
	return p.buildGitHubReadmeResult(ctx, rawURL, owner, repo, string(dlBody), maxLength, "github:contents-api"), nil
}

// buildGitHubReadmeResult builds the README ScrapeResult and additively
// attaches the GitHub trust-surface signals (issue #546) for the same
// owner/repo. The trust-signal fetch is best-effort (see
// fetchGitHubTrustSignals's rule 3.2 graceful degradation): a failure there
// never prevents the README content itself from being returned.
func (p *Pipeline) buildGitHubReadmeResult(ctx context.Context, rawURL, owner, repo, body string, maxLength int, tier string) *ScrapeResult {
	truncated := false
	if len(body) > maxLength {
		body = truncateBytes(body, maxLength)
		truncated = true
	}
	res := &ScrapeResult{
		URL:                rawURL,
		Content:            body,
		ContentType:        "github",
		Title:              fmt.Sprintf("%s/%s — README", owner, repo),
		Author:             owner,
		SiteName:           "GitHub",
		Truncated:          truncated,
		GitHubTrustSignals: p.fetchGitHubTrustSignals(ctx, owner, repo),
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

// GitHubTrustSignals is the "GitHub trust surface" for a repo (issue #546):
// repo stats, the owning org/user's profile, contributor count, community
// health, and release presence — surfacing the signals scrape_page's caller
// would otherwise have no way to see (a brand-new repo and a decade-old,
// foundation-backed one both rendered identically as authorityTier: "high").
// Defined once, here, per rule 5.3 — never redefined in internal/content.
// Every field is best-effort: a failed sub-fetch leaves its section nil/zero
// rather than aborting the README scrape (rule 3.2).
type GitHubTrustSignals struct {
	Repo         *GitHubRepoStats `json:"repo,omitempty"`
	Owner        *GitHubOwner     `json:"owner,omitempty"`
	Contributors *int             `json:"contributorCount,omitempty"`
	Community    *GitHubCommunity `json:"community,omitempty"`
	Releases     *int             `json:"releaseCount,omitempty"`
}

// GitHubRepoStats is the subset of GET /repos/{owner}/{repo} that signals a
// repo's trust profile: age, activity, popularity, and license/archival
// status (issue #546 acceptance criteria).
type GitHubRepoStats struct {
	StargazersCount int      `json:"stargazersCount"`
	ForksCount      int      `json:"forksCount"`
	OpenIssuesCount int      `json:"openIssuesCount"`
	CreatedAt       string   `json:"createdAt"`
	PushedAt        string   `json:"pushedAt"`
	Archived        bool     `json:"archived"`
	Disabled        bool     `json:"disabled"`
	Fork            bool     `json:"fork"`
	License         string   `json:"license,omitempty"`
	Topics          []string `json:"topics,omitempty"`
}

// GitHubOwner is the subset of GET /orgs/{login} or /users/{login} that
// signals the credibility of the account behind a repo.
type GitHubOwner struct {
	Login       string `json:"login"`
	Type        string `json:"type"` // "Organization" or "User"
	CreatedAt   string `json:"createdAt"`
	PublicRepos int    `json:"publicRepos"`
	Followers   int    `json:"followers"`
	IsVerified  bool   `json:"isVerified,omitempty"`
}

// GitHubCommunity is the subset of GET /repos/{owner}/{repo}/community/profile
// that signals project governance maturity.
type GitHubCommunity struct {
	HealthPercentage int  `json:"healthPercentage"`
	HasLicense       bool `json:"hasLicense"`
	HasContributing  bool `json:"hasContributing"`
	HasCodeOfConduct bool `json:"hasCodeOfConduct"`
	HasReadme        bool `json:"hasReadme"`
}

// fetchGitHubTrustSignals gathers the full trust surface for owner/repo via
// four independent api.github.com calls (issue #546). Every call is
// best-effort and independent: repo stats fail to nil, owner fetch is skipped
// on unknown owner type, and a failing contributors/community/releases call
// simply leaves that field unset — never propagating an error to the README
// scrape (rule 3.2).
func (p *Pipeline) fetchGitHubTrustSignals(ctx context.Context, owner, repo string) *GitHubTrustSignals {
	signals := &GitHubTrustSignals{}

	ownerType := ""
	if repoStats, ot, ok := p.fetchGitHubRepoStats(ctx, owner, repo); ok {
		signals.Repo = repoStats
		ownerType = ot
	}
	if o, ok := p.fetchGitHubOwner(ctx, owner, ownerType); ok {
		signals.Owner = o
	}
	if n, ok := p.fetchGitHubContributorCount(ctx, owner, repo); ok {
		signals.Contributors = &n
	}
	if c, ok := p.fetchGitHubCommunityProfile(ctx, owner, repo); ok {
		signals.Community = c
	}
	if n, ok := p.fetchGitHubReleaseCount(ctx, owner, repo); ok {
		signals.Releases = &n
	}

	if signals.Repo == nil && signals.Owner == nil && signals.Contributors == nil &&
		signals.Community == nil && signals.Releases == nil {
		return nil
	}
	return signals
}

// fetchGitHubRepoStats calls GET /repos/{owner}/{repo} (issue #546 rules
// 2.1/2.2/2.3/2.4/3.1/3.3). Returns the owner's account type alongside the
// stats so the caller can route the owner fetch to /orgs or /users without a
// second repo call.
func (p *Pipeline) fetchGitHubRepoStats(ctx context.Context, owner, repo string) (*GitHubRepoStats, string, bool) {
	apiURL := fmt.Sprintf("%s/repos/%s/%s", p.githubAPIBase(), url.PathEscape(owner), url.PathEscape(repo))
	res, err := p.fetchGitHubAPIWithRetry(ctx, apiURL)
	if err != nil || res.status != 200 {
		return nil, "", false
	}

	var body struct {
		StargazersCount int    `json:"stargazers_count"`
		ForksCount      int    `json:"forks_count"`
		OpenIssuesCount int    `json:"open_issues_count"`
		CreatedAt       string `json:"created_at"`
		PushedAt        string `json:"pushed_at"`
		Archived        bool   `json:"archived"`
		Disabled        bool   `json:"disabled"`
		Fork            bool   `json:"fork"`
		License         struct {
			SPDXID string `json:"spdx_id"`
		} `json:"license"`
		Topics []string `json:"topics"`
		Owner  struct {
			Type string `json:"type"`
		} `json:"owner"`
	}
	if err := json.Unmarshal(res.body, &body); err != nil {
		return nil, "", false
	}

	return &GitHubRepoStats{
		StargazersCount: body.StargazersCount,
		ForksCount:      body.ForksCount,
		OpenIssuesCount: body.OpenIssuesCount,
		CreatedAt:       body.CreatedAt,
		PushedAt:        body.PushedAt,
		Archived:        body.Archived,
		Disabled:        body.Disabled,
		Fork:            body.Fork,
		License:         body.License.SPDXID,
		Topics:          body.Topics,
	}, body.Owner.Type, true
}

// fetchGitHubOwner calls GET /orgs/{login} when ownerType is "Organization",
// or GET /users/{login} for anything else (including "" — an unknown type
// falls back to the /users endpoint, which also resolves organization logins
// on GitHub's API, just without the org-only fields) (issue #546 rules
// 2.1/2.2/2.3/2.4/3.1/3.3).
func (p *Pipeline) fetchGitHubOwner(ctx context.Context, login, ownerType string) (*GitHubOwner, bool) {
	segment := "users"
	if ownerType == "Organization" {
		segment = "orgs"
	}
	apiURL := fmt.Sprintf("%s/%s/%s", p.githubAPIBase(), segment, url.PathEscape(login))
	res, err := p.fetchGitHubAPIWithRetry(ctx, apiURL)
	if err != nil || res.status != 200 {
		return nil, false
	}

	var body struct {
		Login       string `json:"login"`
		Type        string `json:"type"`
		CreatedAt   string `json:"created_at"`
		PublicRepos int    `json:"public_repos"`
		Followers   int    `json:"followers"`
		IsVerified  bool   `json:"is_verified"`
	}
	if err := json.Unmarshal(res.body, &body); err != nil {
		return nil, false
	}

	typ := body.Type
	if typ == "" {
		typ = ownerType
	}
	return &GitHubOwner{
		Login:       body.Login,
		Type:        typ,
		CreatedAt:   body.CreatedAt,
		PublicRepos: body.PublicRepos,
		Followers:   body.Followers,
		IsVerified:  body.IsVerified,
	}, true
}

// githubLastPageRe extracts the page number from a Link header's
// rel="last" entry, e.g. `<https://api.github.com/...?page=42>; rel="last"`.
// Deriving the count from this single number — rather than paginating through
// every page — is issue #546 rule 4.2's core requirement.
var githubLastPageRe = regexp.MustCompile(`<[^>]*[?&]page=(\d+)[^>]*>;\s*rel="last"`)

// lastPageFromLinkHeader returns the rel="last" page number from an RFC 5988
// Link header, or 0 if absent (meaning: zero or one page — see the two
// call sites, which treat 0 as "no Link header ⇒ derive count from the single
// returned page instead").
func lastPageFromLinkHeader(link string) int {
	m := githubLastPageRe.FindStringSubmatch(link)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// fetchGitHubContributorCount derives the contributor count from the
// rel="last" page number of a per_page=1 request to
// /repos/{owner}/{repo}/contributors — a single HTTP call, never pagination
// (issue #546 rule 4.2). When the Link header is absent, the count is the
// number of items in the single-page body (0 or 1).
func (p *Pipeline) fetchGitHubContributorCount(ctx context.Context, owner, repo string) (int, bool) {
	apiURL := fmt.Sprintf("%s/repos/%s/%s/contributors?per_page=1&anon=1", p.githubAPIBase(), url.PathEscape(owner), url.PathEscape(repo))
	res, err := p.fetchGitHubAPIWithRetry(ctx, apiURL)
	if err != nil || res.status != 200 {
		return 0, false
	}
	if n := lastPageFromLinkHeader(res.link); n > 0 {
		return n, true
	}
	var items []json.RawMessage
	if err := json.Unmarshal(res.body, &items); err != nil {
		return 0, false
	}
	return len(items), true
}

// fetchGitHubReleaseCount derives the release count the same Link-header way
// as fetchGitHubContributorCount, against /repos/{owner}/{repo}/releases
// (issue #546 rule 4.2).
func (p *Pipeline) fetchGitHubReleaseCount(ctx context.Context, owner, repo string) (int, bool) {
	apiURL := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=1", p.githubAPIBase(), url.PathEscape(owner), url.PathEscape(repo))
	res, err := p.fetchGitHubAPIWithRetry(ctx, apiURL)
	if err != nil || res.status != 200 {
		return 0, false
	}
	if n := lastPageFromLinkHeader(res.link); n > 0 {
		return n, true
	}
	var items []json.RawMessage
	if err := json.Unmarshal(res.body, &items); err != nil {
		return 0, false
	}
	return len(items), true
}

// fetchGitHubCommunityProfile calls GET /repos/{owner}/{repo}/community/profile
// (issue #546 rules 2.1/2.2/2.3/2.4/3.1/3.3).
func (p *Pipeline) fetchGitHubCommunityProfile(ctx context.Context, owner, repo string) (*GitHubCommunity, bool) {
	apiURL := fmt.Sprintf("%s/repos/%s/%s/community/profile", p.githubAPIBase(), url.PathEscape(owner), url.PathEscape(repo))
	res, err := p.fetchGitHubAPIWithRetry(ctx, apiURL)
	if err != nil || res.status != 200 {
		return nil, false
	}

	var body struct {
		HealthPercentage int `json:"health_percentage"`
		Files            struct {
			License       json.RawMessage `json:"license"`
			Contributing  json.RawMessage `json:"contributing"`
			CodeOfConduct json.RawMessage `json:"code_of_conduct"`
			Readme        json.RawMessage `json:"readme"`
		} `json:"files"`
	}
	if err := json.Unmarshal(res.body, &body); err != nil {
		return nil, false
	}

	notNull := func(raw json.RawMessage) bool {
		return len(raw) > 0 && string(raw) != "null"
	}
	return &GitHubCommunity{
		HealthPercentage: body.HealthPercentage,
		HasLicense:       notNull(body.Files.License),
		HasContributing:  notNull(body.Files.Contributing),
		HasCodeOfConduct: notNull(body.Files.CodeOfConduct),
		HasReadme:        notNull(body.Files.Readme),
	}, true
}
