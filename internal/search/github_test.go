package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newGHTestProvider(t *testing.T, handler http.HandlerFunc) *GitHubProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewGitHubProvider("", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	p.SetBaseURL(srv.URL)
	return p
}

func TestGitHubProviderName(t *testing.T) {
	t.Parallel()
	p := NewGitHubProvider("", Deps{})
	if p.Name() != "github" {
		t.Errorf("Name() = %q, want %q", p.Name(), "github")
	}
}

func TestGitHubProviderInterface(t *testing.T) {
	t.Parallel()
	var _ Provider = (*GitHubProvider)(nil)
}

func TestGitHubProviderImages(t *testing.T) {
	t.Parallel()
	p := NewGitHubProvider("", Deps{})
	res, err := p.Images(context.Background(), ImageSearchParams{})
	if err != nil {
		t.Errorf("Images() error = %v, want nil", err)
	}
	if res != nil {
		t.Errorf("Images() = %v, want nil", res)
	}
}

const ghIssueResponse = `{"total_count":1,"items":[{
	"number": 42,
	"title": "Something broke",
	"html_url": "https://github.com/o/r/issues/42",
	"state": "open",
	"user": {"login": "alice"},
	"created_at": "2024-03-01T12:00:00Z",
	"reactions": {"total_count": 5},
	"comments": 3,
	"pull_request": null
}]}`

func TestGitHubProviderWeb(t *testing.T) {
	t.Parallel()
	p := newGHTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(ghIssueResponse))
	})

	res, err := p.Web(context.Background(), WebSearchParams{Query: "something broke"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 result, got %d", len(res))
	}
	r := res[0]
	if !strings.Contains(r.Snippet, "#42") {
		t.Errorf("snippet missing issue number: %q", r.Snippet)
	}
	if !strings.Contains(r.Snippet, "[open]") {
		t.Errorf("snippet missing state: %q", r.Snippet)
	}
	if !strings.Contains(r.Snippet, "issue") {
		t.Errorf("snippet missing kind: %q", r.Snippet)
	}
	if !strings.Contains(r.Snippet, "5 reactions") {
		t.Errorf("snippet missing reaction count: %q", r.Snippet)
	}
	if !strings.Contains(r.Snippet, "3 comments") {
		t.Errorf("snippet missing comment count: %q", r.Snippet)
	}
	if !strings.Contains(r.Snippet, "alice") {
		t.Errorf("snippet missing author: %q", r.Snippet)
	}
	if !strings.Contains(r.Snippet, "2024-03-01") {
		t.Errorf("snippet missing date in YYYY-MM-DD: %q", r.Snippet)
	}
	if r.DisplayLink != "github.com" {
		t.Errorf("DisplayLink = %q, want github.com", r.DisplayLink)
	}
}

const ghPRResponse = `{"total_count":1,"items":[{
	"number": 7,
	"title": "Fix the thing",
	"html_url": "https://github.com/o/r/pull/7",
	"state": "open",
	"user": {"login": "bob"},
	"created_at": "2024-05-01T00:00:00Z",
	"reactions": {"total_count": 1},
	"comments": 0,
	"pull_request": {}
}]}`

func TestGitHubProviderPR(t *testing.T) {
	t.Parallel()
	p := newGHTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(ghPRResponse))
	})

	res, err := p.Web(context.Background(), WebSearchParams{Query: "fix the thing"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 result, got %d", len(res))
	}
	if !strings.Contains(res[0].Snippet, "pr") {
		t.Errorf("PR snippet should contain %q, got %q", "pr", res[0].Snippet)
	}
	if strings.Contains(res[0].Snippet, "issue") {
		t.Errorf("PR snippet should not contain %q, got %q", "issue", res[0].Snippet)
	}
}

func TestGitHubProvider429(t *testing.T) {
	t.Parallel()
	p := newGHTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := p.doSearch(context.Background(), WebSearchParams{Query: "x"})
	if err == nil {
		t.Error("429 should surface as an error")
	}
}

func TestGitHubProvider403(t *testing.T) {
	t.Parallel()
	p := newGHTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	})
	_, err := p.doSearch(context.Background(), WebSearchParams{Query: "x"})
	if err == nil {
		t.Error("403 secondary rate limit should surface as an error")
	}
}

func TestGitHubProviderHTTPError(t *testing.T) {
	t.Parallel()
	p := newGHTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	})
	_, err := p.doSearch(context.Background(), WebSearchParams{Query: "x"})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("500 should surface as an error, got %v", err)
	}
}

func TestGitHubProviderZeroHits(t *testing.T) {
	t.Parallel()
	p := newGHTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"total_count":0,"items":[]}`))
	})
	res, err := p.Web(context.Background(), WebSearchParams{Query: "zzzznomatch"})
	if err != nil {
		t.Fatalf("zero-hits should not error: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("want empty result, got %+v", res)
	}
}

func TestGitHubProviderNews(t *testing.T) {
	t.Parallel()
	p := newGHTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(ghIssueResponse))
	})
	res, err := p.News(context.Background(), NewsSearchParams{Query: "something broke"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 result, got %d", len(res))
	}
	if res[0].Source != "github" {
		t.Errorf("Source = %q, want github", res[0].Source)
	}
}

func TestGitHubProviderTimeRange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in        string
		wantEmpty bool
	}{
		{"day", false},
		{"week", false},
		{"month", false},
		{"year", false},
		{"", true},
		{"invalid", true},
	}
	for _, tt := range tests {
		got := mapGHTimeRange(tt.in)
		if tt.wantEmpty {
			if got != "" {
				t.Errorf("mapGHTimeRange(%q) = %q, want empty", tt.in, got)
			}
			continue
		}
		if !strings.HasPrefix(got, "created:>") {
			t.Errorf("mapGHTimeRange(%q) = %q, want prefix %q", tt.in, got, "created:>")
		}
	}
}

// TestGitHubProviderSendsTokenWhenConfigured verifies GITHUB_TOKEN forwards
// to the Authorization header (#282), mirroring the same optional-key pattern
// as ECOSYSTEMS_API_KEY / BRANDFETCH_API_KEY.
func TestGitHubProviderSendsTokenWhenConfigured(t *testing.T) {
	t.Parallel()
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"total_count":0,"items":[]}`))
	}))
	defer srv.Close()

	p := NewGitHubProvider("secret-token", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	p.SetBaseURL(srv.URL)

	if _, err := p.Web(context.Background(), WebSearchParams{Query: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want Bearer secret-token", gotAuth)
	}
}

// TestMultiInstanceGitHubProviderIsolation proves rule 1 (issue #396): two
// GitHubProvider instances constructed with different tokens in the same
// process never leak state across instances.
func TestMultiInstanceGitHubProviderIsolation(t *testing.T) {
	t.Parallel()
	var gotAuthA, gotAuthB string
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthA = r.Header.Get("Authorization")
		w.Write([]byte(`{"total_count":0,"items":[]}`))
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthB = r.Header.Get("Authorization")
		w.Write([]byte(`{"total_count":0,"items":[]}`))
	}))
	defer srvB.Close()

	pa := NewGitHubProvider("token-a", Deps{HTTPClient: srvA.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60})})
	pa.SetBaseURL(srvA.URL)
	pb := NewGitHubProvider("token-b", Deps{HTTPClient: srvB.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60})})
	pb.SetBaseURL(srvB.URL)

	if _, err := pa.Web(context.Background(), WebSearchParams{Query: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := pb.Web(context.Background(), WebSearchParams{Query: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotAuthA != "Bearer token-a" {
		t.Errorf("instance A leaked/lost its own token: got %q", gotAuthA)
	}
	if gotAuthB != "Bearer token-b" {
		t.Errorf("instance B leaked/lost its own token: got %q", gotAuthB)
	}
	if pa.token == pb.token {
		t.Error("two instances constructed with different tokens must not share field values")
	}
}
