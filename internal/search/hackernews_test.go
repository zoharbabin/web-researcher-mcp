package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newHNTestProvider(t *testing.T, handler http.HandlerFunc) *HNProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &HNProvider{client: srv.Client(), baseURL: srv.URL}
}

func TestHNProviderName(t *testing.T) {
	t.Parallel()
	p := &HNProvider{client: http.DefaultClient, baseURL: "x"}
	if p.Name() != "hackernews" {
		t.Errorf("Name() = %q, want %q", p.Name(), "hackernews")
	}
}

func TestHNProviderWeb(t *testing.T) {
	t.Parallel()
	const body = `{"hits":[
		{"objectID":"34179426","story_id":34179426,"author":"user1","title":"Go 1.22","url":"https://go.dev/blog/go1.22","points":500,"num_comments":100,"created_at":"2024-02-06T00:00:00Z"},
		{"objectID":"34179427","story_id":34179427,"author":"user2","title":"Ask HN: Tools","url":"","points":200,"num_comments":50,"created_at":"2024-02-05T00:00:00Z"}
	],"nbHits":2}`
	p := newHNTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	})
	results, err := p.Web(context.Background(), WebSearchParams{Query: "golang", NumResults: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if results[0].URL != "https://go.dev/blog/go1.22" {
		t.Errorf("results[0].URL = %q, want https://go.dev/blog/go1.22", results[0].URL)
	}
	if results[1].URL != "https://news.ycombinator.com/item?id=34179427" {
		t.Errorf("results[1].URL = %q, want HN item URL (empty url field)", results[1].URL)
	}
	if !strings.Contains(results[0].Snippet, "500 pts") {
		t.Errorf("results[0].Snippet should contain point count, got %q", results[0].Snippet)
	}
	if results[0].DisplayLink != "news.ycombinator.com" {
		t.Errorf("results[0].DisplayLink = %q, want news.ycombinator.com", results[0].DisplayLink)
	}
}

// TestHNProviderPublishedAtNotInSnippet (#356): created_at must populate
// PublishedAt (normalized to RFC3339) and no longer be embedded in Snippet.
func TestHNProviderPublishedAtNotInSnippet(t *testing.T) {
	t.Parallel()
	const body = `{"hits":[
		{"objectID":"34179426","story_id":34179426,"author":"user1","title":"Go 1.22","url":"https://go.dev/blog/go1.22","points":500,"num_comments":100,"created_at":"2024-02-06T00:00:00Z"}
	],"nbHits":1}`
	p := newHNTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	})
	results, err := p.Web(context.Background(), WebSearchParams{Query: "golang", NumResults: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].PublishedAt != "2024-02-06T00:00:00Z" {
		t.Errorf("PublishedAt = %q, want normalized created_at", results[0].PublishedAt)
	}
	if strings.Contains(results[0].Snippet, "2024-02-06") {
		t.Errorf("Snippet must no longer embed the date, got %q", results[0].Snippet)
	}
}

func TestHNProviderImages(t *testing.T) {
	t.Parallel()
	// newHNTestProvider requires a non-nil handler; use a no-op — Images never calls the server.
	p := newHNTestProvider(t, func(w http.ResponseWriter, r *http.Request) {})
	imgs, err := p.Images(context.Background(), ImageSearchParams{Query: "test", NumResults: 5})
	if err != nil {
		t.Errorf("Images() should return nil error, got %v", err)
	}
	if imgs != nil {
		t.Errorf("Images() should return nil slice, got %v", imgs)
	}
}

func TestHNProviderNews(t *testing.T) {
	t.Parallel()
	const body = `{"hits":[
		{"objectID":"34179426","story_id":34179426,"author":"user1","title":"Go 1.22","url":"https://go.dev/blog/go1.22","points":500,"num_comments":100,"created_at":"2024-02-06T00:00:00Z"},
		{"objectID":"34179427","story_id":34179427,"author":"user2","title":"Ask HN: Tools","url":"","points":200,"num_comments":50,"created_at":"2024-02-05T00:00:00Z"}
	],"nbHits":2}`
	p := newHNTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	})
	results, err := p.News(context.Background(), NewsSearchParams{Query: "golang", NumResults: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("News() should delegate to Web and return 2 results, got %d", len(results))
	}
	if results[0].Source != "hackernews" {
		t.Errorf("results[0].Source = %q, want hackernews", results[0].Source)
	}
	if results[0].PublishedAt != "2024-02-06T00:00:00Z" {
		t.Errorf("News() must propagate PublishedAt from Web(), got %q", results[0].PublishedAt)
	}
}

func TestHNProvider429(t *testing.T) {
	t.Parallel()
	p := newHNTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := p.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err == nil {
		t.Fatal("expected error on HTTP 429, got nil")
	}
	if !strings.Contains(err.Error(), "rate limited") && !strings.Contains(err.Error(), "429") {
		t.Errorf("error message should mention rate limit or 429, got %q", err.Error())
	}
}

func TestHNProviderZeroHits(t *testing.T) {
	t.Parallel()
	p := newHNTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"hits":[],"nbHits":0}`))
	})
	results, err := p.Web(context.Background(), WebSearchParams{Query: "xyzzy123", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error on zero hits: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("want 0 results, got %d", len(results))
	}
}

func TestHNProviderInterface(t *testing.T) {
	t.Parallel()
	// compile-time assertion — if this line compiles the interface is satisfied.
	var _ Provider = (*HNProvider)(nil)
}
