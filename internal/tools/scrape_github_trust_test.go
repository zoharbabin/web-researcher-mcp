package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
)

// githubTrustSignalsToolTestHandler mocks the raw README + trust-surface API
// endpoints for a single owner/repo, mirroring the fixture used in
// internal/scraper/github_trust_signals_test.go so this tool-layer test can
// assert on the end-to-end scrape_page output shape (#546) without depending
// on that unexported scraper-package helper.
func githubTrustSignalsToolTestHandler(owner, repo string) http.HandlerFunc {
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
			w.Header().Set("Link", fmt.Sprintf(`<https://api.github.com/repos/%s/%s/contributors?per_page=1&page=57>; rel="last"`, owner, repo))
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
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// TestScrapePageGitHubTrustSignalsPresent is the tool-layer end-to-end guard
// for issue #546: scraping a github.com repo README additively surfaces
// githubTrustSignals in the scrape_page output.
func TestScrapePageGitHubTrustSignalsPresent(t *testing.T) {
	srv := httptest.NewServer(githubTrustSignalsToolTestHandler("firecrawl", "pdf-inspector"))
	defer srv.Close()

	deps := scrapeTestDeps()
	deps.Scraper = scraper.NewPipeline(scraper.PipelineConfig{
		MaxConcurrency:  2,
		AllowPrivateIPs: true,
		GitHubRawBase:   srv.URL,
		GitHubAPIBase:   srv.URL,
	})

	ctx := context.Background()
	toolSrv := createTestServer(deps)
	client := connectTestClient(ctx, t, toolSrv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scrape_page",
		Arguments: map[string]any{"url": "https://github.com/firecrawl/pdf-inspector"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}

	ts, ok := out["githubTrustSignals"].(map[string]any)
	if !ok {
		t.Fatalf("expected githubTrustSignals object, got %v", out["githubTrustSignals"])
	}
	repo, ok := ts["repo"].(map[string]any)
	if !ok || repo["stargazersCount"] != float64(11240) {
		t.Errorf("repo = %v, want stargazersCount=11240", ts["repo"])
	}
	owner, ok := ts["owner"].(map[string]any)
	if !ok || owner["login"] != "firecrawl" {
		t.Errorf("owner = %v, want login=firecrawl", ts["owner"])
	}
	if ts["contributorCount"] != float64(57) {
		t.Errorf("contributorCount = %v, want 57", ts["contributorCount"])
	}
	if ts["releaseCount"] != float64(9) {
		t.Errorf("releaseCount = %v, want 9", ts["releaseCount"])
	}
}

// TestScrapePageGitHubTrustSignalsAbsentForNonGitHub confirms the field is
// omitted (never present as null) for a regular non-GitHub URL (#546).
func TestScrapePageGitHubTrustSignalsAbsentForNonGitHub(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body><article><p>" +
			"Regular web content, nothing GitHub-related here at all in this page. " +
			"</p></article></body></html>"))
	}))
	defer ts.Close()

	ctx := context.Background()
	srv := createTestServer(scrapeTestDeps())
	client := connectTestClient(ctx, t, srv)
	defer client.Close()

	res, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "scrape_page", Arguments: map[string]any{"url": ts.URL}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, present := out["githubTrustSignals"]; present {
		t.Errorf("githubTrustSignals must be absent for non-GitHub URL, got %v", out["githubTrustSignals"])
	}
}
