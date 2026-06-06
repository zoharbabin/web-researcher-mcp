package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newS2TestServer(t *testing.T, handler http.HandlerFunc) *SemanticScholarProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewSemanticScholarProvider("", Deps{
		HTTPClient: http.DefaultClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	p.SetBaseURL(srv.URL)
	return p
}

func TestSemanticScholarSearch(t *testing.T) {
	var gotPath string
	p := newS2TestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		_, _ = w.Write([]byte(`{"total":1,"data":[
			{"paperId":"abc","externalIds":{"DOI":"10.1/x","ArXiv":"2112.0"},"title":"Deep Nets","venue":"NeurIPS","year":2024,"citationCount":42,"isOpenAccess":true,"openAccessPdf":{"url":"https://x/p.pdf"},"tldr":{"text":"A one-liner."},"authors":[{"name":"A. Smith"}],"abstract":"Long abstract."},
			{"paperId":"def","title":"","year":2023}
		]}`))
	})
	res, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "deep nets", NumResults: 5, YearFrom: 2020, YearTo: 2024, OpenAccess: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 { // empty-title paper skipped
		t.Fatalf("want 1 result (empty-title skipped), got %d", len(res))
	}
	r := res[0]
	if r.Title != "Deep Nets" || r.DOI != "10.1/x" || r.Year != 2024 || r.CitationCount != 42 {
		t.Errorf("bad mapping: %+v", r)
	}
	if r.TLDR != "A one-liner." {
		t.Errorf("tldr not mapped: %q", r.TLDR)
	}
	if r.PDFUrl != "https://x/p.pdf" || !r.OpenAccess {
		t.Errorf("OA fields not mapped: %+v", r)
	}
	if r.URL != "https://doi.org/10.1/x" {
		t.Errorf("URL should prefer DOI, got %q", r.URL)
	}
	if r.Source != "semanticscholar" || len(r.Authors) != 1 {
		t.Errorf("source/authors: %+v", r)
	}
	// query construction: fields mask, year range, OA restriction
	for _, want := range []string{"fields=", "year=2020-2024", "openAccessPdf", "query=deep+nets", "limit=5"} {
		if !strings.Contains(gotPath, want) {
			t.Errorf("request path missing %q; got %s", want, gotPath)
		}
	}
}

func TestSemanticScholarCitationsForwardWithIntent(t *testing.T) {
	p := newS2TestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/citations") {
			t.Errorf("expected /citations path, got %s", r.URL.Path)
		}
		if !strings.HasPrefix(r.URL.Path, "/paper/DOI:") {
			t.Errorf("DOI seed should map to /paper/DOI:..., got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[
			{"isInfluential":true,"intents":["methodology","background"],"citingPaper":{"paperId":"c1","title":"Cites It","year":2025,"externalIds":{"DOI":"10.2/y"},"authors":[{"name":"B. Lee"}]}},
			{"isInfluential":false,"intents":[],"citingPaper":{"title":"","year":2025}}
		]}`))
	})
	res, err := p.Citations(context.Background(), "10.18653/v1/N19-1423", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 { // empty-title edge skipped
		t.Fatalf("want 1 edge, got %d", len(res))
	}
	if !res[0].IsInfluential || len(res[0].CitationIntents) != 2 {
		t.Errorf("intent/influence not mapped: %+v", res[0])
	}
	if res[0].Title != "Cites It" {
		t.Errorf("citing paper title: %q", res[0].Title)
	}
}

func TestSemanticScholarReferencesBackward(t *testing.T) {
	p := newS2TestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/references") {
			t.Errorf("expected /references path, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[
			{"isInfluential":false,"intents":["background"],"citedPaper":{"paperId":"r1","title":"Foundational Work","year":2017}}
		]}`))
	})
	res, err := p.References(context.Background(), "somepaperid", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 || res[0].Title != "Foundational Work" {
		t.Fatalf("backward edge mapping: %+v", res)
	}
}

func TestSemanticScholarRateLimitClassified(t *testing.T) {
	p := newS2TestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	})
	_, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "x", NumResults: 3})
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("429 must produce an error containing 'rate limited', got %v", err)
	}
}

func TestSemanticScholarNotFound(t *testing.T) {
	p := newS2TestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	})
	_, err := p.Citations(context.Background(), "10.0/missing", 5)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("404 should surface 'not found', got %v", err)
	}
}

func TestS2Helpers(t *testing.T) {
	if s2PaperPath("10.1/abc") != "/paper/DOI:10.1/abc" {
		t.Errorf("DOI path: %s", s2PaperPath("10.1/abc"))
	}
	if s2PaperPath("plainid") != "/paper/plainid" {
		t.Errorf("plain id path: %s", s2PaperPath("plainid"))
	}
	cases := map[[2]int]string{{2020, 2024}: "2020-2024", {2020, 0}: "2020-", {0, 2024}: "-2024", {0, 0}: ""}
	for in, want := range cases {
		if got := s2YearRange(in[0], in[1]); got != want {
			t.Errorf("s2YearRange(%v)=%q want %q", in, got, want)
		}
	}
	if !isDOI("10.1/x") || isDOI("notadoi") || isDOI("10.nodash") {
		t.Error("isDOI misclassified")
	}
}

func TestSemanticScholarInterface(t *testing.T) {
	var _ AcademicProvider = (*SemanticScholarProvider)(nil)
}
