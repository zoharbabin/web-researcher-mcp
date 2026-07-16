package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHubAPIRequestSendsHeadersAndAuth(t *testing.T) {
	var gotAuth, gotAccept, gotVersion, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotVersion = r.Header.Get("X-GitHub-Api-Version")
		gotUA = r.Header.Get("User-Agent")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	body, err := githubAPIRequest(context.Background(), srv.Client(), srv.URL, "/x", "secret-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q", body)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want Bearer secret-token", gotAuth)
	}
	if gotAccept != "application/vnd.github+json" {
		t.Errorf("Accept = %q", gotAccept)
	}
	if gotVersion != "2022-11-28" {
		t.Errorf("X-GitHub-Api-Version = %q", gotVersion)
	}
	if !strings.HasPrefix(gotUA, "web-researcher-mcp/") {
		t.Errorf("User-Agent = %q, want a descriptive UA", gotUA)
	}
}

func TestGitHubAPIRequestOmitsAuthWhenTokenEmpty(t *testing.T) {
	var gotAuth string
	sawAuth := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		sawAuth = r.Header.Get("Authorization") != ""
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if _, err := githubAPIRequest(context.Background(), srv.Client(), srv.URL, "/x", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sawAuth {
		t.Errorf("Authorization header should be absent when token is empty, got %q", gotAuth)
	}
}

func TestGitHubAPIRequest403RateLimitExceeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := githubAPIRequest(context.Background(), srv.Client(), srv.URL, "/x", "")
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("403 with X-RateLimit-Remaining: 0 should classify as rate limited, got %v", err)
	}
}

func TestGitHubAPIRequest403WithoutRateLimitHeaderIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("some other forbidden reason"))
	}))
	defer srv.Close()

	_, err := githubAPIRequest(context.Background(), srv.Client(), srv.URL, "/x", "")
	if err == nil || strings.Contains(err.Error(), "rate limited") {
		t.Errorf("403 without X-RateLimit-Remaining: 0 should not be classified as rate limited, got %v", err)
	}
}

func TestGitHubAPIRequest429IsRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	_, err := githubAPIRequest(context.Background(), srv.Client(), srv.URL, "/x", "")
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("429 should classify as rate limited, got %v", err)
	}
}

func TestGitHubAPIRequest404IsEmptyNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	body, err := githubAPIRequest(context.Background(), srv.Client(), srv.URL, "/x", "")
	if err != nil {
		t.Errorf("404 should not error: %v", err)
	}
	if body != nil {
		t.Errorf("404 should return nil body, got %q", body)
	}
}

func TestGitHubAPIRequest500IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer srv.Close()

	_, err := githubAPIRequest(context.Background(), srv.Client(), srv.URL, "/x", "")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("500 should surface as an error, got %v", err)
	}
}

// TestGitHubAPIRequestCapsResponseBody proves rule 4.1 (issue #396): an
// oversized response body is truncated to githubMaxResponseBytes rather than
// read into memory unbounded.
func TestGitHubAPIRequestCapsResponseBody(t *testing.T) {
	oversized := githubMaxResponseBytes + 1024
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, oversized))
	}))
	defer srv.Close()

	body, err := githubAPIRequest(context.Background(), srv.Client(), srv.URL, "/x", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(body) != githubMaxResponseBytes {
		t.Errorf("body length = %d, want capped at %d", len(body), githubMaxResponseBytes)
	}
}
