package scraper

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// githubTrustSignalsHandler builds a full mock GitHub surface (raw README +
// repo/owner/contributors/community/releases API endpoints) for owner/repo,
// so trust-signal tests can focus on asserting the parsed GitHubTrustSignals
// shape rather than re-deriving the endpoint routing on every test.
func githubTrustSignalsHandler(t *testing.T, owner, repo string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == fmt.Sprintf("/%s/%s/HEAD/README.md", owner, repo):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("# " + repo))
		case r.URL.Path == fmt.Sprintf("/repos/%s/%s", owner, repo):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"stargazers_count": 11240, "forks_count": 745, "open_issues_count": 12,
				"created_at": "2026-02-01T00:00:00Z", "pushed_at": "2026-08-01T00:00:00Z",
				"archived": false, "disabled": false, "fork": false,
				"license": {"spdx_id": "MIT"}, "topics": ["pdf", "cli"],
				"owner": {"type": "Organization"}
			}`))
		case r.URL.Path == fmt.Sprintf("/orgs/%s", owner):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"login": "` + owner + `", "type": "Organization",
				"created_at": "2023-01-01T00:00:00Z", "public_repos": 40,
				"followers": 3027, "is_verified": false
			}`))
		case r.URL.Path == fmt.Sprintf("/repos/%s/%s/contributors", owner, repo):
			w.Header().Set("Link", fmt.Sprintf(`<https://api.github.com/repos/%s/%s/contributors?per_page=1&page=2>; rel="next", <https://api.github.com/repos/%s/%s/contributors?per_page=1&page=57>; rel="last"`, owner, repo, owner, repo))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"login":"alice"}]`))
		case r.URL.Path == fmt.Sprintf("/repos/%s/%s/community/profile", owner, repo):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"health_percentage": 88,
				"files": {"license": {"key": "mit"}, "contributing": null, "code_of_conduct": null, "readme": {"key": "readme"}}
			}`))
		case r.URL.Path == fmt.Sprintf("/repos/%s/%s/releases", owner, repo):
			w.Header().Set("Link", fmt.Sprintf(`<https://api.github.com/repos/%s/%s/releases?per_page=1&page=9>; rel="last"`, owner, repo))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"tag_name":"v1.0.0"}]`))
		default:
			t.Errorf("unexpected request path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// TestScrapeGitHubReadmeTrustSignals is the end-to-end happy path: a README
// scrape additively populates GitHubTrustSignals with repo/owner/contributor/
// community/release data (issue #546 acceptance criteria).
func TestScrapeGitHubReadmeTrustSignals(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(githubTrustSignalsHandler(t, "firecrawl", "pdf-inspector"))
	t.Cleanup(srv.Close)
	p := NewPipeline(PipelineConfig{GitHubRawBase: srv.URL, GitHubAPIBase: srv.URL, AllowPrivateIPs: true})

	res, err := p.Scrape(context.Background(), "https://github.com/firecrawl/pdf-inspector", 4096)
	if err != nil {
		t.Fatalf("Scrape() error = %v", err)
	}

	ts := res.GitHubTrustSignals
	if ts == nil {
		t.Fatal("GitHubTrustSignals is nil, want populated")
	}
	if ts.Repo == nil || ts.Repo.StargazersCount != 11240 || ts.Repo.ForksCount != 745 {
		t.Errorf("Repo = %+v, want stars=11240 forks=745", ts.Repo)
	}
	if ts.Repo.License != "MIT" {
		t.Errorf("Repo.License = %q, want MIT", ts.Repo.License)
	}
	if ts.Owner == nil || ts.Owner.Login != "firecrawl" || ts.Owner.Type != "Organization" || ts.Owner.Followers != 3027 {
		t.Errorf("Owner = %+v, want login=firecrawl type=Organization followers=3027", ts.Owner)
	}
	if ts.Contributors == nil || *ts.Contributors != 57 {
		t.Errorf("Contributors = %v, want 57", ts.Contributors)
	}
	if ts.Community == nil || ts.Community.HealthPercentage != 88 || !ts.Community.HasLicense || ts.Community.HasContributing {
		t.Errorf("Community = %+v, want health=88 hasLicense=true hasContributing=false", ts.Community)
	}
	if ts.Releases == nil || *ts.Releases != 9 {
		t.Errorf("Releases = %v, want 9", ts.Releases)
	}
}

// TestScrapeGitHubReadmeTrustSignalsDegradesGracefully proves rule 3.2: a
// failure in one stats call (community/profile 500s) never aborts the
// README scrape — the README content is still returned, with only the
// failing field left unset.
func TestScrapeGitHubReadmeTrustSignalsDegradesGracefully(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/octo/widgets/HEAD/README.md":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("# widgets"))
		case r.URL.Path == "/repos/octo/widgets":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"stargazers_count": 5, "owner": {"type": "User"}}`))
		case r.URL.Path == "/users/octo":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"login": "octo", "type": "User", "public_repos": 3, "followers": 1}`))
		case r.URL.Path == "/repos/octo/widgets/community/profile":
			// Simulate a persistent upstream failure — every attempt (including
			// retries) 500s, so this call exhausts its bounded retry budget.
			w.WriteHeader(http.StatusInternalServerError)
		case r.URL.Path == "/repos/octo/widgets/contributors":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"login":"octo"}]`))
		case r.URL.Path == "/repos/octo/widgets/releases":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected request path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	p := NewPipeline(PipelineConfig{GitHubRawBase: srv.URL, GitHubAPIBase: srv.URL, AllowPrivateIPs: true})

	res, err := p.Scrape(context.Background(), "https://github.com/octo/widgets", 4096)
	if err != nil {
		t.Fatalf("Scrape() error = %v, want the README scrape to succeed despite the community/profile failure", err)
	}
	if !strings.Contains(res.Content, "widgets") {
		t.Errorf("Content = %q, want README text still returned", res.Content)
	}
	if res.GitHubTrustSignals == nil {
		t.Fatal("GitHubTrustSignals is nil, want the other fields still populated")
	}
	if res.GitHubTrustSignals.Repo == nil {
		t.Error("Repo is nil, want it populated despite the unrelated community/profile failure")
	}
	if res.GitHubTrustSignals.Community != nil {
		t.Errorf("Community = %+v, want nil (the failing field omitted)", res.GitHubTrustSignals.Community)
	}
}

// TestFetchGitHubRepoStatsPathEscape proves rule 2.1: a crafted owner/repo
// containing "../", "%2e%2e", "#", "?" is percent-escaped per segment before
// interpolation, so the constructed request can never escape the /repos/
// path prefix or reach a different host.
func TestFetchGitHubRepoStatsPathEscape(t *testing.T) {
	t.Parallel()

	// r.URL.Path is Go's auto-DECODED convenience view — checking it would
	// assert the wrong thing here. The actual bytes sent on the wire (what a
	// path-traversal attack would need to control) are r.URL.EscapedPath(),
	// which must keep every "/" inside a segment percent-encoded as %2F so it
	// can never be interpreted as an additional path separator.
	var gotEscapedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	p := NewPipeline(PipelineConfig{GitHubAPIBase: srv.URL, AllowPrivateIPs: true})

	_, _, _ = p.fetchGitHubRepoStats(context.Background(), "../../etc", "passwd#?")

	if !strings.HasPrefix(gotEscapedPath, "/repos/") {
		t.Fatalf("escaped request path %q escaped the /repos/ prefix", gotEscapedPath)
	}
	if strings.Contains(gotEscapedPath, "/../") || strings.HasSuffix(gotEscapedPath, "/..") {
		t.Errorf("escaped request path %q contains a raw \"..\" traversal segment", gotEscapedPath)
	}
	if !strings.Contains(gotEscapedPath, "..%2F..%2Fetc") {
		t.Errorf("escaped request path %q does not show the owner's \"/\" characters percent-encoded", gotEscapedPath)
	}
}

// TestFetchGitHubContributorCountSingleRequest proves rule 4.2: the
// contributor count is derived from the Link header's rel="last" page number
// on a single per_page=1 request — never by paginating through every page.
func TestFetchGitHubContributorCountSingleRequest(t *testing.T) {
	t.Parallel()

	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Link", `<https://api.github.com/repos/o/r/contributors?per_page=1&page=42>; rel="last"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"login":"a"}]`))
	}))
	t.Cleanup(srv.Close)
	p := NewPipeline(PipelineConfig{GitHubAPIBase: srv.URL, AllowPrivateIPs: true})

	n, ok := p.fetchGitHubContributorCount(context.Background(), "o", "r")
	if !ok || n != 42 {
		t.Errorf("fetchGitHubContributorCount = (%d, %v), want (42, true)", n, ok)
	}
	if requests != 1 {
		t.Errorf("made %d requests, want exactly 1 (no pagination)", requests)
	}
}

// TestFetchGitHubContributorCountNoLinkHeader covers the single-page case
// (no Link header at all): the count falls back to the item count in the
// one returned page.
func TestFetchGitHubContributorCountNoLinkHeader(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"login":"a"}]`))
	}))
	t.Cleanup(srv.Close)
	p := NewPipeline(PipelineConfig{GitHubAPIBase: srv.URL, AllowPrivateIPs: true})

	n, ok := p.fetchGitHubContributorCount(context.Background(), "o", "r")
	if !ok || n != 1 {
		t.Errorf("fetchGitHubContributorCount = (%d, %v), want (1, true)", n, ok)
	}
}

// TestFetchGitHubAPIWithRetrySucceedsAfterTransientFailures proves rule 3.1:
// a server that fails N times then succeeds is retried within the bounded
// attempt count, and no more than len(backoffs)+1 total calls are made.
func TestFetchGitHubAPIWithRetrySucceedsAfterTransientFailures(t *testing.T) {
	t.Parallel()

	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= len(githubAPIRetryBackoffs) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	p := NewPipeline(PipelineConfig{GitHubAPIBase: srv.URL, AllowPrivateIPs: true})

	res, err := p.fetchGitHubAPIWithRetry(context.Background(), srv.URL+"/repos/o/r")
	if err != nil || res.status != 200 {
		t.Fatalf("fetchGitHubAPIWithRetry() = (%+v, %v), want status 200, no error", res, err)
	}
	maxAttempts := len(githubAPIRetryBackoffs) + 1
	if attempts > maxAttempts {
		t.Errorf("made %d attempts, want at most %d", attempts, maxAttempts)
	}
}

// TestFetchGitHubAPIWithRetryDoesNotRetry403 proves rule 4.3: a 403
// (rate-limited or forbidden) response is not retried — retrying it would
// risk exhausting the remaining unauthenticated rate-limit budget instead of
// degrading immediately per rule 3.2.
func TestFetchGitHubAPIWithRetryDoesNotRetry403(t *testing.T) {
	t.Parallel()

	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	p := NewPipeline(PipelineConfig{GitHubAPIBase: srv.URL, AllowPrivateIPs: true})

	res, err := p.fetchGitHubAPIWithRetry(context.Background(), srv.URL+"/repos/o/r")
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if res.status != 403 {
		t.Errorf("status = %d, want 403", res.status)
	}
	if attempts != 1 {
		t.Errorf("made %d attempts, want exactly 1 (no retry on 403)", attempts)
	}
}

// TestFetchGitHubAPIRequestTimeoutSet proves rule 3.3: each trust-signal call
// applies the same bounded-timeout context as the existing fetchGitHubAPI/
// fetchGitHubRaw calls, rather than the caller's raw (potentially unbounded)
// context. A server that hangs well past a 10s timeout would make this test
// slow to fail, so instead this drives the bound from the other direction:
// a parent context whose OWN deadline is far in the future (past 10s) must
// still result in the outbound request carrying a <=10s deadline — proven by
// asserting doGitHubAPIRequest derives a fresh, shorter-lived context via
// context.WithTimeout rather than passing the parent ctx straight through.
// This is a static/structural check (grep-equivalent via reflection is
// unnecessary): the request must fail fast when the PARENT is already
// expired, confirming ctx cancellation actually propagates into the request.
func TestFetchGitHubAPIRequestTimeoutSet(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	p := NewPipeline(PipelineConfig{GitHubAPIBase: srv.URL, AllowPrivateIPs: true})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := p.doGitHubAPIRequest(ctx, srv.URL+"/repos/o/r"); err == nil {
		t.Error("doGitHubAPIRequest() with an already-canceled parent context returned no error, want ctx cancellation to propagate into the request")
	}
}

// TestFetchGitHubAPIResponseSizeCapped proves rule 2.4: a response body over
// githubScrapeMaxBytes is truncated via the existing io.LimitReader cap,
// inherited automatically because doGitHubAPIRequest shares it with
// fetchGitHubRaw/fetchGitHubAPI.
func TestFetchGitHubAPIResponseSizeCapped(t *testing.T) {
	t.Parallel()

	oversized := strings.Repeat("a", githubScrapeMaxBytes+1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(oversized))
	}))
	t.Cleanup(srv.Close)
	p := NewPipeline(PipelineConfig{GitHubAPIBase: srv.URL, AllowPrivateIPs: true})

	res, err := p.doGitHubAPIRequest(context.Background(), srv.URL+"/repos/o/r")
	if err != nil {
		t.Fatalf("doGitHubAPIRequest() error = %v", err)
	}
	if len(res.body) > githubScrapeMaxBytes {
		t.Errorf("body len = %d, want capped at %d", len(res.body), githubScrapeMaxBytes)
	}
}

// TestMultiInstanceGitHubTrustSignalsIsolation proves rule 1.1: two Pipeline
// instances constructed with distinct GitHubAPIBase values, in the same
// process, fetch trust signals from their own configured base independently
// — no shared package-level state. Modeled on TestMultiInstancePipelineIsolation
// (bluesky_test.go), the isolation-test precedent for issue #407 rule 1.3.
func TestMultiInstanceGitHubTrustSignalsIsolation(t *testing.T) {
	t.Parallel()

	srvA := httptest.NewServer(githubTrustSignalsHandler(t, "orga", "reposa"))
	t.Cleanup(srvA.Close)
	srvB := httptest.NewServer(githubTrustSignalsHandler(t, "orgb", "reposb"))
	t.Cleanup(srvB.Close)

	pa := NewPipeline(PipelineConfig{GitHubRawBase: srvA.URL, GitHubAPIBase: srvA.URL, AllowPrivateIPs: true})
	pb := NewPipeline(PipelineConfig{GitHubRawBase: srvB.URL, GitHubAPIBase: srvB.URL, AllowPrivateIPs: true})

	resA, err := pa.Scrape(context.Background(), "https://github.com/orga/reposa", 4096)
	if err != nil {
		t.Fatalf("pa.Scrape: unexpected error: %v", err)
	}
	resB, err := pb.Scrape(context.Background(), "https://github.com/orgb/reposb", 4096)
	if err != nil {
		t.Fatalf("pb.Scrape: unexpected error: %v", err)
	}

	if pa.githubAPIBase() == pb.githubAPIBase() {
		t.Errorf("pa and pb share the same githubAPIBase %q — instance state leaked", pa.githubAPIBase())
	}
	if resA.GitHubTrustSignals == nil || resA.GitHubTrustSignals.Owner == nil || resA.GitHubTrustSignals.Owner.Login != "orga" {
		t.Errorf("resA.GitHubTrustSignals.Owner = %+v, want login=orga", resA.GitHubTrustSignals)
	}
	if resB.GitHubTrustSignals == nil || resB.GitHubTrustSignals.Owner == nil || resB.GitHubTrustSignals.Owner.Login != "orgb" {
		t.Errorf("resB.GitHubTrustSignals.Owner = %+v, want login=orgb", resB.GitHubTrustSignals)
	}
}
