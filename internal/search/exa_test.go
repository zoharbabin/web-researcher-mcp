package search

import (
	"context"
	"encoding/json"
	"errors"
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

func TestExaAnswer(t *testing.T) {
	p, _ := newExaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/answer") {
			t.Errorf("Answer must POST /answer, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"answer":"42",
			"citations":[{"title":"Src","url":"https://s.example","publishedDate":"2026-01-01"}],
			"costDollars":{"total":0.005}
		}`))
	})
	res, err := p.Answer(context.Background(), AnswerParams{Query: "meaning of life"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Answer != "42" || len(res.Citations) != 1 || res.CostUSD != 0.005 {
		t.Errorf("unexpected answer result: %+v", res)
	}
}

func TestExaStructuredSearchWithSchema(t *testing.T) {
	var gotBody map[string]any
	p, _ := newExaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		// summary returned as a JSON STRING conforming to the supplied schema
		_, _ = w.Write([]byte(`{
			"results":[{"title":"Co","url":"https://co.example","summary":"{\"founded\":2021}","entities":[{"type":"company","properties":{"name":"Co"}}]}],
			"costDollars":{"total":0.01}
		}`))
	})
	schema := json.RawMessage(`{"type":"object","properties":{"founded":{"type":"number"}}}`)
	res, err := p.StructuredSearch(context.Background(), StructuredParams{
		Query: "Co", Category: "company", NumResults: 3, Schema: schema,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	contents, _ := gotBody["contents"].(map[string]any)
	summary, _ := contents["summary"].(map[string]any)
	if summary["schema"] == nil {
		t.Errorf("schema must be nested under contents.summary.schema, got %v", contents["summary"])
	}
	if gotBody["category"] != "company" {
		t.Errorf("category should pass through, got %v", gotBody["category"])
	}
	if len(res.Results) != 1 {
		t.Fatalf("want 1 result, got %d", len(res.Results))
	}
	// summary is embedded verbatim as JSON (not double-encoded as a string)
	if string(res.Results[0].Summary) != `{"founded":2021}` {
		t.Errorf("schema-conforming summary should embed as JSON, got %s", res.Results[0].Summary)
	}
	if len(res.Results[0].Entities) == 0 {
		t.Errorf("entities should be surfaced for category=company")
	}
}

func TestExaStructuredSearchNoSchemaPlainSummary(t *testing.T) {
	var gotBody map[string]any
	p, _ := newExaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{"results":[{"title":"T","url":"https://t.example","summary":"plain text"}],"costDollars":{"total":0.01}}`))
	})
	res, err := p.StructuredSearch(context.Background(), StructuredParams{Query: "x", NumResults: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	contents, _ := gotBody["contents"].(map[string]any)
	if contents["summary"] != true {
		t.Errorf("no schema ⇒ contents.summary should be true, got %v", contents["summary"])
	}
	// plain text summary is JSON-encoded as a string
	if string(res.Results[0].Summary) != `"plain text"` {
		t.Errorf("plain summary should be JSON string, got %s", res.Results[0].Summary)
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

func TestExaValidateStructured(t *testing.T) {
	p := NewExaProvider("k", newExaTestDeps())
	cases := []struct {
		name    string
		params  StructuredParams
		wantErr bool
	}{
		{"no category no schema", StructuredParams{Query: "x"}, false},
		{"valid category", StructuredParams{Query: "x", Category: "company"}, false},
		{"bad category", StructuredParams{Query: "x", Category: "tweet"}, true},
		{"valid flat schema", StructuredParams{Query: "x", Schema: json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}}}`)}, false},
		{"non-object root", StructuredParams{Query: "x", Schema: json.RawMessage(`{"type":"array"}`)}, true},
		{"nested object", StructuredParams{Query: "x", Schema: json.RawMessage(`{"type":"object","properties":{"a":{"type":"object"}}}`)}, true},
		{"array of objects", StructuredParams{Query: "x", Schema: json.RawMessage(`{"type":"object","properties":{"a":{"type":"array","items":{"type":"object"}}}}`)}, true},
		{"array of primitives", StructuredParams{Query: "x", Schema: json.RawMessage(`{"type":"object","properties":{"a":{"type":"array","items":{"type":"string"}}}}`)}, false},
		{"invalid json schema", StructuredParams{Query: "x", Schema: json.RawMessage(`{not json`)}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := p.validateStructured(c.params)
			if (msg != "") != c.wantErr {
				t.Errorf("validateStructured = %q, wantErr=%v", msg, c.wantErr)
			}
		})
	}
}

func TestExaStructuredSearchRejectsBadParamsWithoutCall(t *testing.T) {
	called := false
	p, _ := newExaTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{"results":[]}`))
	})
	_, err := p.StructuredSearch(context.Background(), StructuredParams{Query: "x", Category: "bogus"})
	var ipe *InvalidParamsError
	if !errors.As(err, &ipe) {
		t.Fatalf("expected InvalidParamsError, got %v", err)
	}
	if called {
		t.Error("a bad request must NOT reach the network (no paid call)")
	}
}

func TestExaInterfaces(t *testing.T) {
	var _ Provider = (*ExaProvider)(nil)
	var _ AcademicProvider = (*ExaProvider)(nil)
	var _ AnswerProvider = (*ExaProvider)(nil)
	var _ StructuredProvider = (*ExaProvider)(nil)
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

func TestJSONOrString(t *testing.T) {
	cases := []struct {
		in   string
		want string // "" means nil
	}{
		{"", ""},
		{`{"a":1}`, `{"a":1}`},         // object embeds verbatim
		{`[1,2]`, `[1,2]`},             // array embeds verbatim
		{"plain text", `"plain text"`}, // plain → JSON string
		{"null", `"null"`},             // bare scalar → JSON string (not dropped)
		{"123", `"123"`},               // bare number → JSON string (type contract held)
		{"true", `"true"`},             // bare bool → JSON string
		{`  {"a":1}  `, `  {"a":1}  `}, // leading ws still recognized as object
	}
	for _, c := range cases {
		got := jsonOrString(c.in)
		if c.want == "" {
			if got != nil {
				t.Errorf("jsonOrString(%q) = %s, want nil", c.in, got)
			}
			continue
		}
		if string(got) != c.want {
			t.Errorf("jsonOrString(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}
