package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newHNTestPipeline creates a Pipeline whose HNFirebaseBase and HNAlgoliaBase
// both point at the given test server, and registers a cleanup to close it.
func newHNTestPipeline(t *testing.T, handler http.HandlerFunc) *Pipeline {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewPipeline(PipelineConfig{HNFirebaseBase: srv.URL, HNAlgoliaBase: srv.URL, AllowPrivateIPs: true})
	return p
}

// TestIsHNURL verifies that isHNURL accepts real HN hostnames and rejects look-alikes.
func TestIsHNURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		url  string
		want bool
	}{
		{"https://news.ycombinator.com/item?id=1", true},
		{"https://news.ycombinator.com/", true},
		{"https://www.news.ycombinator.com/newest", true},
		{"https://evil.com/news.ycombinator.com", false},
		{"https://ycombinator.com/", false},
		{"https://example.com", false},
	}

	for _, tc := range cases {
		got := isHNURL(tc.url)
		if got != tc.want {
			t.Errorf("isHNURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

// TestScrapeHNItemValidation confirms that malformed item IDs produce ErrValidation
// without ever reaching the server.
func TestScrapeHNItemValidation(t *testing.T) {
	t.Parallel()

	badURLs := []string{
		"https://news.ycombinator.com/item?id=0",
		"https://news.ycombinator.com/item?id=-1",
		"https://news.ycombinator.com/item?id=abc",
		// missing id param — the scrapeHN router falls through to scrapeWithTieredFallback,
		// which will hit the server; test the explicit path via scrapeHNItem directly instead
	}

	p := newHNTestPipeline(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be called for validation-rejected URL: %s", r.URL)
	}))

	ctx := context.Background()
	for _, rawURL := range badURLs {
		_, err := p.Scrape(ctx, rawURL, 4096)
		if err == nil {
			t.Errorf("Scrape(%q): expected error, got nil", rawURL)
			continue
		}
		se, ok := err.(*ScrapeError)
		if !ok {
			t.Errorf("Scrape(%q): error type %T, want *ScrapeError", rawURL, err)
			continue
		}
		if se.Kind != ErrValidation {
			t.Errorf("Scrape(%q): Kind=%v, want ErrValidation", rawURL, se.Kind)
		}
	}
}

// TestScrapeHNUserValidation confirms that invalid usernames produce ErrValidation.
func TestScrapeHNUserValidation(t *testing.T) {
	t.Parallel()

	badUserURLs := []string{
		"https://news.ycombinator.com/user?id=u/evil",
		"https://news.ycombinator.com/user?id=" + strings.Repeat("a", 26),
		// empty id falls through to the default case (scrapeWithTieredFallback),
		// so test the explicit path via scrapeHNUser for an empty string separately.
	}

	p := newHNTestPipeline(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be called for validation-rejected user URL: %s", r.URL)
	}))

	ctx := context.Background()
	for _, rawURL := range badUserURLs {
		_, err := p.Scrape(ctx, rawURL, 4096)
		if err == nil {
			t.Errorf("Scrape(%q): expected error, got nil", rawURL)
			continue
		}
		se, ok := err.(*ScrapeError)
		if !ok {
			t.Errorf("Scrape(%q): error type %T, want *ScrapeError", rawURL, err)
			continue
		}
		if se.Kind != ErrValidation {
			t.Errorf("Scrape(%q): Kind=%v, want ErrValidation", rawURL, se.Kind)
		}
	}
}

// TestScrapeHNItemNullResponse verifies that a "null" Firebase response yields ErrNotFound.
func TestScrapeHNItemNullResponse(t *testing.T) {
	t.Parallel()

	p := newHNTestPipeline(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "null")
	}))

	ctx := context.Background()
	_, err := p.Scrape(ctx, "https://news.ycombinator.com/item?id=8863", 4096)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	se, ok := err.(*ScrapeError)
	if !ok {
		t.Fatalf("error type %T, want *ScrapeError", err)
	}
	if se.Kind != ErrNotFound {
		t.Errorf("Kind=%v, want ErrNotFound", se.Kind)
	}
}

// TestScrapeHNItem verifies a story with two comments is fetched and formatted correctly.
func TestScrapeHNItem(t *testing.T) {
	t.Parallel()

	items := map[string]string{
		"/item/8863.json": `{"id":8863,"type":"story","by":"dhouston","time":1175714200,"title":"My Dropbox","url":"http://example.com","score":104,"descendants":2,"kids":[1,2]}`,
		"/item/1.json":    `{"id":1,"type":"comment","by":"alice","time":1175714300,"text":"Great stuff","dead":false,"deleted":false}`,
		"/item/2.json":    `{"id":2,"type":"comment","by":"bob","time":1175714400,"text":"Cool project","dead":false,"deleted":false}`,
	}

	p := newHNTestPipeline(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := items[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))

	ctx := context.Background()
	result, err := p.Scrape(ctx, "https://news.ycombinator.com/item?id=8863", 4096)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Title != "My Dropbox" {
		t.Errorf("Title=%q, want %q", result.Title, "My Dropbox")
	}
	if result.ContentType != "hackernews" {
		t.Errorf("ContentType=%q, want %q", result.ContentType, "hackernews")
	}
	if result.Tier != "hackernews:api" {
		t.Errorf("Tier=%q, want %q", result.Tier, "hackernews:api")
	}
	if !strings.Contains(result.Content, "alice") {
		t.Errorf("Content missing comment by alice: %q", result.Content)
	}
	if !strings.Contains(result.Content, "bob") {
		t.Errorf("Content missing comment by bob: %q", result.Content)
	}
}

// TestScrapeHNItemDeadCommentSkipped confirms dead comments are omitted from output.
func TestScrapeHNItemDeadCommentSkipped(t *testing.T) {
	t.Parallel()

	items := map[string]string{
		"/item/9999.json": `{"id":9999,"type":"story","by":"host","time":1175714200,"title":"Test Story","score":10,"descendants":2,"kids":[3,4]}`,
		"/item/3.json":    `{"id":3,"type":"comment","by":"ghost","dead":true,"text":"bad comment"}`,
		"/item/4.json":    `{"id":4,"type":"comment","by":"carol","text":"Good point","dead":false,"deleted":false}`,
	}

	p := newHNTestPipeline(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := items[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))

	ctx := context.Background()
	result, err := p.Scrape(ctx, "https://news.ycombinator.com/item?id=9999", 4096)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Content, "ghost") {
		t.Errorf("Content contains dead comment author 'ghost': %q", result.Content)
	}
	if !strings.Contains(result.Content, "carol") {
		t.Errorf("Content missing live comment by carol: %q", result.Content)
	}
}

// TestScrapeHNUser verifies user profile is fetched and HTML stripped from the About field.
func TestScrapeHNUser(t *testing.T) {
	t.Parallel()

	p := newHNTestPipeline(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/pg.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"pg","created":1170000000,"karma":155000,"about":"<p>Y Combinator"}`)
	}))

	ctx := context.Background()
	result, err := p.Scrape(ctx, "https://news.ycombinator.com/user?id=pg", 4096)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "155000") {
		t.Errorf("Content missing karma 155000: %q", result.Content)
	}
	if strings.Contains(result.Content, "<p>") {
		t.Errorf("Content contains raw HTML tag <p>: %q", result.Content)
	}
}

// TestScrapeHNList verifies that a list endpoint returns up to hnMaxListItems stories
// and marks the result as Truncated when the server returns more.
func TestScrapeHNList(t *testing.T) {
	t.Parallel()

	// Build 25 sequential IDs [101..125].
	ids := make([]int, 25)
	for i := range ids {
		ids[i] = 101 + i
	}
	idJSON, _ := json.Marshal(ids)

	p := newHNTestPipeline(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/topstories.json" {
			w.Write(idJSON)
			return
		}
		// Individual item requests: /item/<N>.json
		if strings.HasPrefix(r.URL.Path, "/item/") {
			idStr := strings.TrimPrefix(r.URL.Path, "/item/")
			idStr = strings.TrimSuffix(idStr, ".json")
			fmt.Fprintf(w, `{"id":%s,"type":"story","by":"u","title":"Story %s","score":10,"descendants":5,"time":1175714200}`, idStr, idStr)
			return
		}
		http.NotFound(w, r)
	}))

	ctx := context.Background()
	result, err := p.Scrape(ctx, "https://news.ycombinator.com/", 1<<20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Truncated {
		t.Error("expected Truncated==true when list has 25 items > hnMaxListItems(20)")
	}
	count := strings.Count(result.Content, "Story")
	if count != hnMaxListItems {
		t.Errorf("Content contains %d 'Story' occurrences, want %d", count, hnMaxListItems)
	}
}

// TestScrapeHNFirebase429 verifies that a 429 from the Firebase API yields ErrRateLimit.
func TestScrapeHNFirebase429(t *testing.T) {
	t.Parallel()

	p := newHNTestPipeline(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))

	ctx := context.Background()
	_, err := p.Scrape(ctx, "https://news.ycombinator.com/item?id=1", 4096)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	se, ok := err.(*ScrapeError)
	if !ok {
		t.Fatalf("error type %T, want *ScrapeError", err)
	}
	if se.Kind != ErrRateLimit {
		t.Errorf("Kind=%v, want ErrRateLimit", se.Kind)
	}
}

// TestScrapeHNUnknownPath verifies that an unrecognised HN path (e.g. /submit)
// does NOT hit Firebase API endpoints — it falls through to scrapeWithTieredFallback.
func TestScrapeHNUnknownPath(t *testing.T) {
	t.Parallel()

	firebaseCalled := false
	p := newHNTestPipeline(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/item/") || strings.HasSuffix(r.URL.Path, "stories.json") {
			firebaseCalled = true
		}
		// Respond with something that won't produce a valid scrape result so the
		// tiered fallback simply errors out — we only care about firebaseCalled.
		http.Error(w, "not found", http.StatusNotFound)
	}))

	ctx := context.Background()
	// Result may be an error (expected — test server returns 404) or a partial
	// fallback result. Either is fine; the important assertion is firebaseCalled.
	p.Scrape(ctx, "https://news.ycombinator.com/submit", 4096) //nolint:errcheck

	if firebaseCalled {
		t.Error("Firebase API endpoints were called for an unknown HN path; expected fall-through to tiered scraper")
	}
}
