package scraper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newGitHubTestPipeline creates a Pipeline whose GitHubRawBase and
// GitHubAPIBase both point at the given test server, and registers a cleanup
// to close it. Production serves raw content and the API from different
// hosts; pointing both at one test server and dispatching on path inside the
// handler keeps the test setup simple without changing the routing logic
// under test.
func newGitHubTestPipeline(t *testing.T, handler http.HandlerFunc) *Pipeline {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewPipeline(PipelineConfig{
		GitHubRawBase:   srv.URL,
		GitHubAPIBase:   srv.URL,
		AllowPrivateIPs: true,
	})
}

func TestIsGitHubContentURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		url  string
		want bool
	}{
		{"https://github.com/golang/go", true},
		{"https://www.github.com/golang/go", true},
		{"https://github.com/golang/go/blob/master/README.md", true},
		{"https://github.com/golang/go/issues/1", true},
		{"https://gist.github.com/octocat/6cad326836d38bd3a7ae", true},
		{"https://gist.github.com/6cad326836d38bd3a7ae", true},
		{"https://evil.com/github.com/golang/go", false},
		{"https://githubusercontent.com/golang/go", false},
		{"https://example.com", false},
	}

	for _, tc := range cases {
		got := isGitHubContentURL(tc.url)
		if got != tc.want {
			t.Errorf("isGitHubContentURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestMatchGitHubPath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		host     string
		segments []string
		wantKind string
	}{
		{"bare repo root", "github.com", []string{"golang", "go"}, "readme"},
		{"blob file", "github.com", []string{"golang", "go", "blob", "master", "README.md"}, "blob"},
		{"nested blob path", "github.com", []string{"golang", "go", "blob", "master", "src", "main.go"}, "blob"},
		{"issues falls through", "github.com", []string{"golang", "go", "issues", "1"}, ""},
		{"pulls falls through", "github.com", []string{"golang", "go", "pull", "1"}, ""},
		{"reserved settings", "github.com", []string{"settings", "profile"}, ""},
		{"reserved security advisories", "github.com", []string{"security", "advisories"}, ""},
		{"reserved topics", "github.com", []string{"topics", "go"}, ""},
		{"reserved marketplace", "github.com", []string{"marketplace", "actions"}, ""},
		{"single segment", "github.com", []string{"golang"}, ""},
		{"reserved owner on blob path", "github.com", []string{"search", "advanced", "blob", "master", "x"}, ""},
		{"gist bare id", "gist.github.com", []string{"6cad326836d38bd3a7ae"}, "gist"},
		{"gist user+id", "gist.github.com", []string{"octocat", "6cad326836d38bd3a7ae"}, "gist"},
		{"gist non-hex id falls through", "gist.github.com", []string{"not-a-gist-id"}, ""},
		{"gist reserved path falls through", "gist.github.com", []string{"discover"}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchGitHubPath(tc.host, tc.segments)
			if got.kind != tc.wantKind {
				t.Errorf("matchGitHubPath(%q, %v).kind = %q, want %q", tc.host, tc.segments, got.kind, tc.wantKind)
			}
		})
	}
}

func TestScrapeGitHubReadmeRawHit(t *testing.T) {
	t.Parallel()

	p := newGitHubTestPipeline(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/golang/go/HEAD/README.md" {
			t.Errorf("unexpected raw path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# The Go Programming Language\n\nGo is great."))
	})

	res, err := p.Scrape(context.Background(), "https://github.com/golang/go", 4096)
	if err != nil {
		t.Fatalf("Scrape() error = %v", err)
	}
	if !strings.Contains(res.Content, "Go Programming Language") {
		t.Errorf("Content = %q, want README text", res.Content)
	}
	if res.Tier != "github:raw" {
		t.Errorf("Tier = %q, want github:raw", res.Tier)
	}
}

func TestScrapeGitHubReadmeFallsBackToContentsAPI(t *testing.T) {
	t.Parallel()

	// The download_url returned by the Contents API must point back at our
	// own test server. httptest.NewServer's URL isn't known until after the
	// server starts, so the handler closes over a pointer set immediately
	// after newGitHubTestPipeline returns, before any request is made.
	var rawBase string
	p := newGitHubTestPipeline(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sindresorhus/awesome/HEAD/README.md":
			// Casing mismatch: repo's real file is lowercase readme.md.
			w.WriteHeader(http.StatusNotFound)
		case "/repos/sindresorhus/awesome/readme":
			if r.Header.Get("Accept") != "application/vnd.github+json" {
				t.Errorf("missing Accept header on API request")
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"download_url":"` + rawBase + `/sindresorhus/awesome/HEAD/readme.md"}`))
		case "/sindresorhus/awesome/HEAD/readme.md":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("# Awesome\n\nA curated list."))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	rawBase = p.githubRawBase()

	res, err := p.Scrape(context.Background(), "https://github.com/sindresorhus/awesome", 4096)
	if err != nil {
		t.Fatalf("Scrape() error = %v", err)
	}
	if !strings.Contains(res.Content, "curated list") {
		t.Errorf("Content = %q, want readme text", res.Content)
	}
	if res.Tier != "github:contents-api" {
		t.Errorf("Tier = %q, want github:contents-api", res.Tier)
	}
}

func TestScrapeGitHubReadmeNotFound(t *testing.T) {
	t.Parallel()

	p := newGitHubTestPipeline(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	_, err := p.Scrape(context.Background(), "https://github.com/nope/nope", 4096)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	se, ok := err.(*ScrapeError)
	if !ok {
		t.Fatalf("error type %T, want *ScrapeError", err)
	}
	if se.Kind != ErrNotFound {
		t.Errorf("Kind = %v, want ErrNotFound", se.Kind)
	}
}

func TestScrapeGitHubBlob(t *testing.T) {
	t.Parallel()

	p := newGitHubTestPipeline(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/golang/go/master/src/main.go" {
			t.Errorf("unexpected raw path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("package main\n\nfunc main() {}\n"))
	})

	res, err := p.Scrape(context.Background(), "https://github.com/golang/go/blob/master/src/main.go", 4096)
	if err != nil {
		t.Fatalf("Scrape() error = %v", err)
	}
	if !strings.Contains(res.Content, "package main") {
		t.Errorf("Content = %q, want file content", res.Content)
	}
	if res.Tier != "github:raw" {
		t.Errorf("Tier = %q, want github:raw", res.Tier)
	}
}

func TestScrapeGitHubBlobRateLimited(t *testing.T) {
	t.Parallel()

	p := newGitHubTestPipeline(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})

	_, err := p.Scrape(context.Background(), "https://github.com/golang/go/blob/master/src/main.go", 4096)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	se, ok := err.(*ScrapeError)
	if !ok {
		t.Fatalf("error type %T, want *ScrapeError", err)
	}
	if se.Kind != ErrRateLimit {
		t.Errorf("Kind = %v, want ErrRateLimit", se.Kind)
	}
}

func TestScrapeGitHubGist(t *testing.T) {
	t.Parallel()

	p := newGitHubTestPipeline(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gists/6cad326836d38bd3a7ae" {
			t.Errorf("unexpected API path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"description": "An example gist",
			"owner": {"login": "octocat"},
			"files": {
				"hello.py": {"filename": "hello.py", "content": "print('hello')"}
			}
		}`))
	})

	res, err := p.Scrape(context.Background(), "https://gist.github.com/octocat/6cad326836d38bd3a7ae", 4096)
	if err != nil {
		t.Fatalf("Scrape() error = %v", err)
	}
	if !strings.Contains(res.Content, "print('hello')") {
		t.Errorf("Content = %q, want gist file content", res.Content)
	}
	if res.Title != "An example gist" {
		t.Errorf("Title = %q, want %q", res.Title, "An example gist")
	}
	if res.Tier != "github:gist-api" {
		t.Errorf("Tier = %q, want github:gist-api", res.Tier)
	}
}

func TestScrapeGitHubGistNotFound(t *testing.T) {
	t.Parallel()

	p := newGitHubTestPipeline(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	_, err := p.Scrape(context.Background(), "https://gist.github.com/octocat/0000000000000000", 4096)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	se, ok := err.(*ScrapeError)
	if !ok {
		t.Fatalf("error type %T, want *ScrapeError", err)
	}
	if se.Kind != ErrNotFound {
		t.Errorf("Kind = %v, want ErrNotFound", se.Kind)
	}
}
