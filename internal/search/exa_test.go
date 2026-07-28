package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newExaTestDeps() Deps {
	return Deps{
		HTTPClient: http.DefaultClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	}
}

// newExaTestServer returns an Exa provider wired to a test server that asserts
// the x-api-key header and returns the given status+body for any endpoint.
func newExaTestServer(t *testing.T, handler http.HandlerFunc) (*ExaProvider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewExaProvider("test-key", newExaTestDeps())
	p.SetBaseURL(srv.URL)
	return p, srv
}

func TestExaWebSearch(t *testing.T) {
	var gotBody map[string]any
	p, _ := newExaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("missing/wrong x-api-key header: %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("must NOT send Authorization header (Exa uses x-api-key)")
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{
			"requestId":"abc",
			"results":[
				{"title":"First","url":"https://a.example/x","highlights":["a highlight"]},
				{"title":"","url":"https://b.example/y","publishedDate":null,"summary":"fallback summary"}
			],
			"costDollars":{"total":0.007}
		}`))
	})

	results, err := p.Web(context.Background(), WebSearchParams{Query: "golang", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	// content MUST nest under "contents" (top-level would 400 on the real API)
	contents, ok := gotBody["contents"].(map[string]any)
	if !ok || contents["highlights"] != true {
		t.Errorf("request must nest highlights under contents; got %v", gotBody["contents"])
	}
	if gotBody["type"] != "auto" {
		t.Errorf("default type should be auto, got %v", gotBody["type"])
	}
	// snippet from highlights[0]
	if results[0].Snippet != "a highlight" {
		t.Errorf("snippet should come from highlights[0], got %q", results[0].Snippet)
	}
	// empty title passes through; snippet falls back to summary
	if results[1].Title != "" {
		t.Errorf("empty title should pass through, got %q", results[1].Title)
	}
	if results[1].Snippet != "fallback summary" {
		t.Errorf("snippet should fall back to summary, got %q", results[1].Snippet)
	}
	if results[1].DisplayLink != "b.example" {
		t.Errorf("displayLink should be host, got %q", results[1].DisplayLink)
	}
}

func TestExaWebSearchClampAndSite(t *testing.T) {
	var gotBody map[string]any
	p, _ := newExaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{"results":[],"costDollars":{"total":0}}`))
	})
	_, err := p.Web(context.Background(), WebSearchParams{Query: "x", NumResults: 999, Site: "example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n, _ := gotBody["numResults"].(float64); int(n) != 10 {
		t.Errorf("numResults should clamp to 10, got %v", gotBody["numResults"])
	}
	if q, _ := gotBody["query"].(string); !strings.Contains(q, "site:example.com") {
		t.Errorf("site operator should be appended to query, got %q", q)
	}
}

// TestExaWebSearch_PublishedAt (#356): a result's publishedDate must populate
// SearchResult.PublishedAt, normalized to RFC3339.
func TestExaWebSearch_PublishedAt(t *testing.T) {
	p, _ := newExaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[
			{"title":"Dated","url":"https://a.example/x","publishedDate":"2026-05-01T12:00:00.000Z","highlights":["h"]},
			{"title":"Undated","url":"https://b.example/y","highlights":["h"]}
		]}`))
	})

	results, err := p.Web(context.Background(), WebSearchParams{Query: "golang", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if results[0].PublishedAt != "2026-05-01T12:00:00Z" {
		t.Errorf("expected normalized PublishedAt, got %q", results[0].PublishedAt)
	}
	if results[1].PublishedAt != "" {
		t.Errorf("expected empty PublishedAt when absent, got %q", results[1].PublishedAt)
	}
}

// TestExaWebSearch_Engagement (#281): a result's score must populate
// SearchResult.Engagement; a zero/absent score must leave Engagement nil.
func TestExaWebSearch_Engagement(t *testing.T) {
	p, _ := newExaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[
			{"title":"Scored","url":"https://a.example/x","highlights":["h"],"score":0.87},
			{"title":"Unscored","url":"https://b.example/y","highlights":["h"]}
		]}`))
	})

	results, err := p.Web(context.Background(), WebSearchParams{Query: "golang", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if results[0].Engagement == nil || results[0].Engagement.Score != 0.87 {
		t.Errorf("results[0].Engagement = %+v, want Score=0.87", results[0].Engagement)
	}
	if results[1].Engagement != nil {
		t.Errorf("results[1].Engagement should be nil when score is absent/zero, got %+v", results[1].Engagement)
	}
}

func TestExaNewsSearch(t *testing.T) {
	var gotBody map[string]any
	p, _ := newExaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{
			"results":[{"title":"News","url":"https://news.example/z","publishedDate":"2026-06-01T00:00:00.000Z","highlights":["h"],"score":0.55}],
			"costDollars":{"total":0.007}
		}`))
	})
	results, err := p.News(context.Background(), NewsSearchParams{Query: "ai", NumResults: 3, Freshness: "week"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["category"] != "news" {
		t.Errorf("news must set category=news, got %v", gotBody["category"])
	}
	if _, ok := gotBody["startPublishedDate"].(string); !ok {
		t.Errorf("freshness should map to startPublishedDate, got %v", gotBody["startPublishedDate"])
	}
	if len(results) != 1 || results[0].PublishedAt == "" {
		t.Fatalf("want 1 dated news result, got %+v", results)
	}
	if results[0].Source != "news.example" {
		t.Errorf("source should be host, got %q", results[0].Source)
	}
	if results[0].Engagement == nil || results[0].Engagement.Score != 0.55 {
		t.Errorf("results[0].Engagement = %+v, want Score=0.55 (#281)", results[0].Engagement)
	}
}

func TestExaImagesEmptyNoRequest(t *testing.T) {
	called := false
	p, _ := newExaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{}`))
	})
	results, err := p.Images(context.Background(), ImageSearchParams{Query: "cats"})
	if err != nil {
		t.Fatalf("Images must not error, got %v", err)
	}
	if results != nil {
		t.Errorf("Images must return nil, got %v", results)
	}
	if called {
		t.Error("Images must make NO HTTP request (would trip the breaker)")
	}
}

func TestExaScholarly(t *testing.T) {
	var gotBody map[string]any
	p, _ := newExaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{
			"results":[
				{"title":"Attention Is All You Need","url":"https://arxiv.org/abs/1706.03762","author":"Vaswani","publishedDate":"2017-06-12T00:00:00.000Z","highlights":["transformer"]},
				{"title":"","url":"https://x.example/skip"}
			],
			"costDollars":{"total":0.007}
		}`))
	})
	results, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "transformers", NumResults: 5, YearFrom: 2017, YearTo: 2018})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["category"] != "research paper" {
		t.Errorf("academic must set category=research paper, got %v", gotBody["category"])
	}
	if gotBody["startPublishedDate"] == nil || gotBody["endPublishedDate"] == nil {
		t.Errorf("year window should map to start/endPublishedDate")
	}
	if len(results) != 1 { // empty-title result is skipped
		t.Fatalf("want 1 result (empty-title skipped), got %d", len(results))
	}
	if results[0].Year != 2017 || len(results[0].Authors) != 1 || results[0].Source != "exa" {
		t.Errorf("unexpected mapping: %+v", results[0])
	}
}

func TestExaRateLimitClassified(t *testing.T) {
	p, _ := newExaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":"too many"}`))
	})
	_, err := p.Web(context.Background(), WebSearchParams{Query: "x", NumResults: 3})
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("429 must produce an error containing 'rate limited', got %v", err)
	}
}

func TestExaUpstreamError(t *testing.T) {
	p, _ := newExaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(402)
		_, _ = w.Write([]byte(`{"error":"NO_MORE_CREDITS","tag":"NO_MORE_CREDITS"}`))
	})
	_, err := p.Web(context.Background(), WebSearchParams{Query: "x", NumResults: 3})
	if err == nil || !strings.Contains(err.Error(), "402") {
		t.Errorf("402 should surface a descriptive upstream error, got %v", err)
	}
}

func TestExaInterfaces(t *testing.T) {
	var _ Provider = (*ExaProvider)(nil)
	var _ AcademicProvider = (*ExaProvider)(nil)
}

func TestFreshnessToStartDate(t *testing.T) {
	if freshnessToStartDate("bogus") != "" {
		t.Error("unknown freshness should map to empty (no filter)")
	}
	for _, f := range []string{"hour", "day", "week", "month", "year"} {
		if freshnessToStartDate(f) == "" {
			t.Errorf("freshness %q should map to a date", f)
		}
	}
}

func TestPublishYear(t *testing.T) {
	cases := map[string]int{
		"2017-06-12T00:00:00.000Z": 2017, // full RFC3339
		"2017-06-12":               2017, // date-only (Exa research-paper case)
		"2017":                     2017, // bare year
		"":                         0,
		"not-a-date":               0,
		"99":                       0, // too short / implausible
	}
	for in, want := range cases {
		if got := publishYear(in); got != want {
			t.Errorf("publishYear(%q) = %d, want %d", in, got, want)
		}
	}
}
