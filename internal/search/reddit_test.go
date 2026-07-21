package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newRedditTestProvider(t *testing.T, handler http.HandlerFunc) *RedditProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &RedditProvider{client: srv.Client(), baseURL: srv.URL}
}

const redditSampleFeed = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
	<entry>
		<title>Go 1.22 released</title>
		<link rel="alternate" href="https://www.reddit.com/r/golang/comments/abc123/go_122_released/"/>
		<author><name>gopher42</name></author>
		<published>2024-02-06T00:00:00+00:00</published>
		<updated>2024-02-06T00:00:00+00:00</updated>
		<category term="golang" label="r/golang"/>
		<content type="html">&lt;p&gt;ignored&lt;/p&gt;</content>
	</entry>
	<entry>
		<title>Ask r/programming: best practices</title>
		<link rel="alternate" href="https://www.reddit.com/r/programming/comments/def456/best_practices/"/>
		<author><name>coder99</name></author>
		<published>2024-02-05T00:00:00+00:00</published>
		<updated>2024-02-05T00:00:00+00:00</updated>
		<category term="programming" label="r/programming"/>
		<content type="html">&lt;p&gt;ignored&lt;/p&gt;</content>
	</entry>
</feed>`

func TestRedditProviderName(t *testing.T) {
	t.Parallel()
	p := &RedditProvider{client: http.DefaultClient, baseURL: "x"}
	if p.Name() != "reddit" {
		t.Errorf("Name() = %q, want %q", p.Name(), "reddit")
	}
}

func TestRedditProviderWeb(t *testing.T) {
	t.Parallel()
	p := newRedditTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		w.Write([]byte(redditSampleFeed))
	})
	results, err := p.Web(context.Background(), WebSearchParams{Query: "golang", NumResults: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if results[0].URL != "https://www.reddit.com/r/golang/comments/abc123/go_122_released/" {
		t.Errorf("results[0].URL = %q, want alternate link href", results[0].URL)
	}
	if results[0].Title != "Go 1.22 released" {
		t.Errorf("results[0].Title = %q, want %q", results[0].Title, "Go 1.22 released")
	}
	if results[0].DisplayLink != "reddit.com/r/golang" {
		t.Errorf("results[0].DisplayLink = %q, want reddit.com/r/golang", results[0].DisplayLink)
	}
	if !strings.Contains(results[0].Snippet, "u/gopher42") {
		t.Errorf("results[0].Snippet should contain author, got %q", results[0].Snippet)
	}
	if !strings.Contains(results[0].Snippet, "2024-02-06") {
		t.Errorf("results[0].Snippet should contain date, got %q", results[0].Snippet)
	}
}

func TestRedditProviderWebQueryParams(t *testing.T) {
	t.Parallel()
	var gotQuery, gotSort, gotT string
	p := newRedditTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		gotQuery = q.Get("q")
		gotSort = q.Get("sort")
		gotT = q.Get("t")
		w.Header().Set("Content-Type", "application/atom+xml")
		w.Write([]byte(`<feed xmlns="http://www.w3.org/2005/Atom"></feed>`))
	})
	_, err := p.Web(context.Background(), WebSearchParams{Query: "kubernetes", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotQuery != "kubernetes" {
		t.Errorf("q = %q, want kubernetes", gotQuery)
	}
	if gotSort != "relevance" {
		t.Errorf("sort = %q, want relevance", gotSort)
	}
	if gotT == "" {
		t.Error("t param should be set")
	}
}

func TestRedditProviderWebZeroHits(t *testing.T) {
	t.Parallel()
	p := newRedditTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		w.Write([]byte(`<feed xmlns="http://www.w3.org/2005/Atom"></feed>`))
	})
	results, err := p.Web(context.Background(), WebSearchParams{Query: "xyzzy123", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error on zero hits: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("want 0 results, got %d", len(results))
	}
}

func TestRedditProviderImages(t *testing.T) {
	t.Parallel()
	p := newRedditTestProvider(t, func(w http.ResponseWriter, r *http.Request) {})
	imgs, err := p.Images(context.Background(), ImageSearchParams{Query: "test", NumResults: 5})
	if err != nil {
		t.Errorf("Images() should return nil error, got %v", err)
	}
	if imgs != nil {
		t.Errorf("Images() should return nil slice, got %v", imgs)
	}
}

func TestRedditProviderNews(t *testing.T) {
	t.Parallel()
	p := newRedditTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		w.Write([]byte(redditSampleFeed))
	})
	news, err := p.News(context.Background(), NewsSearchParams{Query: "golang", NumResults: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(news) != 2 {
		t.Fatalf("want 2 results, got %d", len(news))
	}
	if news[0].Source != "reddit" {
		t.Errorf("news[0].Source = %q, want reddit", news[0].Source)
	}
}

func TestRedditProvider429(t *testing.T) {
	t.Parallel()
	p := newRedditTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := p.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err == nil {
		t.Fatal("expected error on HTTP 429, got nil")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("error message should mention rate limit, got %q", err.Error())
	}
}

func TestRedditProviderNon200(t *testing.T) {
	t.Parallel()
	p := newRedditTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, err := p.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err == nil {
		t.Fatal("expected error on HTTP 503, got nil")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error message should mention 503, got %q", err.Error())
	}
}

func TestRedditProviderBadXML(t *testing.T) {
	t.Parallel()
	p := newRedditTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not xml at all"))
	})
	_, err := p.Web(context.Background(), WebSearchParams{Query: "test", NumResults: 5})
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "parse feed") {
		t.Errorf("error message should mention parse feed, got %q", err.Error())
	}
}

func TestRedditProviderInterface(t *testing.T) {
	t.Parallel()
	var _ Provider = (*RedditProvider)(nil)
}

func TestRedditTimeRangeMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"hour", "hour"},
		{"day", "day"},
		{"week", "week"},
		{"month", "month"},
		{"year", "year"},
		{"", "month"},
		{"all", "month"},
	}
	for _, c := range cases {
		if got := mapRedditTimeRange(c.in); got != c.want {
			t.Errorf("mapRedditTimeRange(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRedditEntryLinkSelection(t *testing.T) {
	t.Parallel()

	t.Run("alternate wins over no-rel", func(t *testing.T) {
		links := []redditAtomLink{
			{Rel: "", Href: "https://example.com/self"},
			{Rel: "alternate", Href: "https://example.com/alt"},
		}
		if got := entryLink(links); got != "https://example.com/alt" {
			t.Errorf("entryLink() = %q, want alternate href", got)
		}
	})

	t.Run("fallback to any href when no alternate", func(t *testing.T) {
		links := []redditAtomLink{
			{Rel: "self", Href: "https://example.com/self"},
		}
		if got := entryLink(links); got != "https://example.com/self" {
			t.Errorf("entryLink() = %q, want fallback href", got)
		}
	})

	t.Run("returns empty when no links", func(t *testing.T) {
		if got := entryLink(nil); got != "" {
			t.Errorf("entryLink(nil) = %q, want empty string", got)
		}
	})
}
