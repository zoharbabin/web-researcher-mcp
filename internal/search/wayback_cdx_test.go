package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newWaybackTestServer(t *testing.T, handler http.HandlerFunc) *WaybackCDXResolver {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	r := NewWaybackCDXResolver(Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	r.SetBaseURL(srv.URL)
	return r
}

func TestWaybackCDX_ParsesHeaderAndFiltersStatus(t *testing.T) {
	var gotQuery string
	r := newWaybackTestServer(t, func(w http.ResponseWriter, req *http.Request) {
		gotQuery = req.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			["original","timestamp","statuscode","mimetype"],
			["https://acme.com/login","20260101000000","200","text/html"],
			["https://acme.com/dead","20250101000000","404","text/html"],
			["https://acme.com/api/status","20260201000000","301","application/json"]
		]`))
	})
	entries, err := r.Lookup(context.Background(), "acme.com", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 { // the 404 row must be filtered out
		t.Fatalf("want 2 entries (404 filtered), got %d: %+v", len(entries), entries)
	}
	if entries[0].Category != "login" {
		t.Errorf("expected /login to categorize as 'login', got %q", entries[0].Category)
	}
	if entries[1].Category != "api" {
		t.Errorf("expected /api/status to categorize as 'api', got %q", entries[1].Category)
	}
	if !strings.Contains(gotQuery, "url=acme.com%2F%2A") {
		t.Errorf("query should search url=<domain>/* , got %q", gotQuery)
	}
}

func TestWaybackCDX_ColumnReorderTolerant(t *testing.T) {
	// Header columns in a different order than the fl= we request — the parser
	// must use the header's declared indices, not assume positional order.
	r := newWaybackTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			["statuscode","original","timestamp"],
			["200","https://acme.com/","20260101000000"]
		]`))
	})
	entries, err := r.Lookup(context.Background(), "acme.com", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 || entries[0].URL != "https://acme.com/" {
		t.Fatalf("reordered columns misparsed: %+v", entries)
	}
}

func TestWaybackCDX_EmptyBodyIsZeroEntries(t *testing.T) {
	r := newWaybackTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	entries, err := r.Lookup(context.Background(), "acme.com", 0)
	if err != nil {
		t.Fatalf("empty array must not be an error, got %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("want zero entries, got %d", len(entries))
	}
}

func TestWaybackCDX_HeaderOnlyIsZeroEntries(t *testing.T) {
	r := newWaybackTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[["original","timestamp","statuscode","mimetype"]]`))
	})
	entries, err := r.Lookup(context.Background(), "acme.com", 0)
	if err != nil {
		t.Fatalf("header-only response must not be an error, got %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("want zero entries, got %d", len(entries))
	}
}

func TestWaybackCDX_RateLimitClassified(t *testing.T) {
	r := newWaybackTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
	})
	_, err := r.Lookup(context.Background(), "acme.com", 0)
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("429 must produce an error containing 'rate limited', got %v", err)
	}
}

func TestWaybackCDX_InvalidDomainRejectedWithoutRequest(t *testing.T) {
	called := false
	r := newWaybackTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
	})
	_, err := r.Lookup(context.Background(), "not a domain", 0)
	if err == nil {
		t.Fatal("invalid domain should be rejected")
	}
	if called {
		t.Error("invalid domain must never reach the HTTP client")
	}
}

func TestCategorizeArchiveURL(t *testing.T) {
	cases := map[string]string{
		"https://acme.com/login":          "login",
		"https://acme.com/api/v1/users":   "api",
		"https://acme.com/wp-admin/":      "admin",
		"https://acme.com/whitepaper.pdf": "doc",
		"https://acme.com/app.js":         "asset",
		"https://acme.com/about":          "other",
		// #596: content-site buckets, added because the original 5 (+ catch-all
		// "other") gave near-zero triage signal on marketing/content-heavy
		// domains where most archived paths are blog/docs/legal pages.
		"https://acme.com/blog/2026-roadmap":    "blog",
		"https://acme.com/news/press-release":   "blog",
		"https://acme.com/press/announcement":   "blog",
		"https://acme.com/docs/getting-started": "docs",
		"https://acme.com/help/faq":             "docs",
		"https://acme.com/support/ticket":       "docs",
		"https://acme.com/kb/article-1":         "docs",
		"https://acme.com/privacy":              "legal",
		"https://acme.com/terms":                "legal",
		"https://acme.com/legal/cookies":        "legal",
	}
	for url, want := range cases {
		if got := categorizeArchiveURL(url); got != want {
			t.Errorf("categorizeArchiveURL(%q) = %q, want %q", url, got, want)
		}
	}
}

// TestWaybackCDX_PercentEncodedURLDecoded is the regression test for issue
// #438: a percent-encoded capture (e.g. "%2Flogin" instead of "/login") used
// to reach categorizeArchiveURL unescaped, both categorizing as "other" noise
// and surfacing a garbled URL in the result — decodeArchiveURL now runs first.
func TestWaybackCDX_PercentEncodedURLDecoded(t *testing.T) {
	r := newWaybackTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			["original","timestamp","statuscode","mimetype"],
			["https://acme.com/account%2Flogin","20260101000000","200","text/html"]
		]`))
	})
	entries, err := r.Lookup(context.Background(), "acme.com", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d: %+v", len(entries), entries)
	}
	if entries[0].URL != "https://acme.com/account/login" {
		t.Errorf("URL = %q, want percent-decoded https://acme.com/account/login", entries[0].URL)
	}
	if entries[0].Category != "login" {
		t.Errorf("Category = %q, want login (decoding should let categorization match /login)", entries[0].Category)
	}
}

func TestDecodeArchiveURL(t *testing.T) {
	cases := map[string]string{
		"https://acme.com/account%2Flogin":             "https://acme.com/account/login",
		"https://acme.com/path+with+plus":              "https://acme.com/path+with+plus",            // '+' is literal, not a space, outside a query string
		"https://acme.com/not%a-valid%escape":          "https://acme.com/not%a-valid%escape",        // malformed escape falls back to raw
		"https://acme.com/account%2Flogin?a=1%26b=2":   "https://acme.com/account/login?a=1%26b=2",   // query-string "%26" (encoded '&') must NOT be decoded — that would change the query's meaning
		"https://acme.com/account%2Flogin#frag%2Fpart": "https://acme.com/account/login#frag%2Fpart", // fragment escapes are likewise left untouched
	}
	for raw, want := range cases {
		if got := decodeArchiveURL(raw); got != want {
			t.Errorf("decodeArchiveURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestWaybackCDX_Interface(t *testing.T) {
	var _ ArchiveResolver = (*WaybackCDXResolver)(nil)
}
