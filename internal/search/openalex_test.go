package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newOpenAlexTestDeps() Deps {
	return Deps{
		HTTPClient: http.DefaultClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	}
}

const testOpenAlexResponse = `{
  "results": [
    {
      "display_name": "Attention Is All You Need",
      "doi": "https://doi.org/10.48550/arXiv.1706.03762",
      "publication_year": 2017,
      "cited_by_count": 95000,
      "authorships": [
        {"author": {"display_name": "Ashish Vaswani"}},
        {"author": {"display_name": "Noam Shazeer"}}
      ],
      "primary_location": {
        "source": {"display_name": "Advances in Neural Information Processing Systems", "is_in_doaj": true}
      },
      "open_access": {
        "is_oa": true,
        "oa_url": "https://arxiv.org/pdf/1706.03762"
      },
      "abstract_inverted_index": {
        "The": [0],
        "dominant": [1],
        "sequence": [2],
        "transduction": [3],
        "models": [4],
        "are": [5],
        "based": [6],
        "on": [7],
        "complex": [8],
        "recurrent": [9],
        "or": [10],
        "convolutional": [11],
        "neural": [12],
        "networks.": [13]
      }
    },
    {
      "display_name": "BERT: Pre-training of Deep Bidirectional Transformers",
      "doi": "https://doi.org/10.18653/v1/N19-1423",
      "publication_year": 2019,
      "cited_by_count": 72000,
      "authorships": [
        {"author": {"display_name": "Jacob Devlin"}}
      ],
      "primary_location": {
        "source": {"display_name": "NAACL-HLT"}
      },
      "open_access": {
        "is_oa": true,
        "oa_url": "https://aclanthology.org/N19-1423.pdf"
      },
      "abstract_inverted_index": null
    }
  ]
}`

const testOpenAlexEmptyResponse = `{"results": []}`

func TestOpenAlexProvider_Scholarly(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/works" {
			w.WriteHeader(404)
			return
		}
		q := r.URL.Query()
		if q.Get("search") == "" {
			w.WriteHeader(400)
			return
		}
		if q.Get("mailto") != "test@example.com" {
			t.Errorf("expected mailto=test@example.com, got %s", q.Get("mailto"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(testOpenAlexResponse))
	}))
	defer srv.Close()

	provider := NewOpenAlexProvider("test@example.com", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)

	results, err := provider.Scholarly(context.Background(), AcademicSearchParams{
		Query:      "transformer attention mechanism",
		NumResults: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	r := results[0]
	if r.Title != "Attention Is All You Need" {
		t.Errorf("unexpected title: %s", r.Title)
	}
	if r.DOI != "10.48550/arXiv.1706.03762" {
		t.Errorf("unexpected DOI: %s", r.DOI)
	}
	if r.Year != 2017 {
		t.Errorf("unexpected year: %d", r.Year)
	}
	if r.CitationCount != 95000 {
		t.Errorf("unexpected citation count: %d", r.CitationCount)
	}
	if len(r.Authors) != 2 || r.Authors[0] != "Ashish Vaswani" {
		t.Errorf("unexpected authors: %v", r.Authors)
	}
	if r.Journal != "Advances in Neural Information Processing Systems" {
		t.Errorf("unexpected journal: %s", r.Journal)
	}
	if !r.OpenAccess {
		t.Error("expected OpenAccess to be true")
	}
	if r.PDFUrl != "https://arxiv.org/pdf/1706.03762" {
		t.Errorf("unexpected PDF URL: %s", r.PDFUrl)
	}
	if r.URL != "https://doi.org/10.48550/arXiv.1706.03762" {
		t.Errorf("unexpected URL: %s", r.URL)
	}
	if r.Source != "openalex" {
		t.Errorf("unexpected source: %s", r.Source)
	}
	if r.Abstract == "" {
		t.Error("expected abstract to be reconstructed from inverted index")
	}
	if !r.IsInDoaj {
		t.Error("expected IsInDoaj to be true")
	}

	// Second result should have no abstract (nil inverted index)
	if results[1].Abstract != "" {
		t.Errorf("expected empty abstract for nil inverted index, got: %s", results[1].Abstract)
	}
	// Second result has no is_in_doaj field in the fixture — must default false.
	if results[1].IsInDoaj {
		t.Error("expected IsInDoaj to be false when absent from response")
	}
}

func TestOpenAlexProvider_Filters(t *testing.T) {
	t.Parallel()

	var capturedFilter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedFilter = r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(testOpenAlexEmptyResponse))
	}))
	defer srv.Close()

	provider := NewOpenAlexProvider("test@example.com", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)

	_, err := provider.Scholarly(context.Background(), AcademicSearchParams{
		Query:      "deep learning",
		YearFrom:   2020,
		YearTo:     2024,
		OpenAccess: true,
		NumResults: 3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedFilter == "" {
		t.Fatal("expected filter to be set")
	}
	expected := "from_publication_date:2020-01-01,to_publication_date:2024-12-31,open_access.is_oa:true"
	if capturedFilter != expected {
		t.Errorf("unexpected filter:\n  got:  %s\n  want: %s", capturedFilter, expected)
	}
}

func TestOpenAlexProvider_RateLimit(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))
	defer srv.Close()

	provider := NewOpenAlexProvider("test@example.com", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)

	_, err := provider.Scholarly(context.Background(), AcademicSearchParams{
		Query: "test",
	})
	if err == nil {
		t.Fatal("expected error for rate limit")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("expected rate limit error, got: %v", err)
	}
}

func TestOpenAlexProvider_ServerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal server error"))
	}))
	defer srv.Close()

	provider := NewOpenAlexProvider("test@example.com", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)

	_, err := provider.Scholarly(context.Background(), AcademicSearchParams{
		Query: "test",
	})
	if err == nil {
		t.Fatal("expected error for server error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 in error, got: %v", err)
	}
}

func TestOpenAlexProvider_Metadata(t *testing.T) {
	t.Parallel()

	provider := NewOpenAlexProvider("test@example.com", Deps{
		HTTPClient: http.DefaultClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	if provider.Name() != "openalex" {
		t.Errorf("unexpected name: %s", provider.Name())
	}

	meta := provider.Metadata()
	if !meta.MatchesRegion("US") {
		t.Error("expected worldwide provider to match US")
	}
	if !meta.HasCapability("search") {
		t.Error("expected search capability")
	}
	if !meta.HasCapability("citations") {
		t.Error("expected citations capability")
	}
}

func TestReconstructAbstract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		index map[string][]int
		want  string
	}{
		{
			name:  "nil index",
			index: nil,
			want:  "",
		},
		{
			name:  "empty index",
			index: map[string][]int{},
			want:  "",
		},
		{
			name: "simple sentence",
			index: map[string][]int{
				"Hello": {0},
				"world": {1},
			},
			want: "Hello world",
		},
		{
			name: "word at multiple positions",
			index: map[string][]int{
				"the":  {0, 4},
				"cat":  {1},
				"sat":  {2},
				"on":   {3},
				"mat.": {5},
			},
			want: "the cat sat on the mat.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reconstructAbstract(tt.index)
			if got != tt.want {
				t.Errorf("reconstructAbstract() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOpenAlexCitationsForward(t *testing.T) {
	// 2 endpoints: resolve seed DOI → work (with id), then cites: filter query.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/works/doi:") {
			_, _ = w.Write([]byte(`{"id":"https://openalex.org/W100","display_name":"Seed","publication_year":2020}`))
			return
		}
		// cites: filter listing
		_, _ = w.Write([]byte(`{"results":[{"id":"https://openalex.org/W200","display_name":"Citing Work","publication_year":2024,"cited_by_count":5}]}`))
	}))
	defer srv.Close()
	p := NewOpenAlexProvider("e@x.com", newOpenAlexTestDeps())
	p.SetBaseURL(srv.URL)

	res, err := p.Citations(context.Background(), "10.1/seed", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 || res[0].Title != "Citing Work" || res[0].Source != "openalex" {
		t.Fatalf("forward edge mapping: %+v", res)
	}
}

func TestOpenAlexReferencesBackward(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/works/doi:") {
			// seed work carries referenced_works
			_, _ = w.Write([]byte(`{"id":"https://openalex.org/W100","display_name":"Seed","referenced_works":["https://openalex.org/W1","https://openalex.org/W2"]}`))
			return
		}
		// openalex_id: batch fetch of referenced works
		_, _ = w.Write([]byte(`{"results":[{"id":"https://openalex.org/W1","display_name":"Ref One","publication_year":2015},{"id":"https://openalex.org/W2","display_name":"Ref Two","publication_year":2016}]}`))
	}))
	defer srv.Close()
	p := NewOpenAlexProvider("e@x.com", newOpenAlexTestDeps())
	p.SetBaseURL(srv.URL)

	res, err := p.References(context.Background(), "10.1/seed", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("backward edges: want 2, got %d (%+v)", len(res), res)
	}
}

func TestShortOpenAlexID(t *testing.T) {
	if shortOpenAlexID("https://openalex.org/W123") != "W123" {
		t.Error("should extract bare ID from URL")
	}
	if shortOpenAlexID("W123") != "W123" {
		t.Error("bare ID should pass through")
	}
}

func TestIsOpenAlexWorkID(t *testing.T) {
	cases := map[string]bool{
		"W2741809807":           true,
		"w123":                  true,
		"W":                     false, // bare letter, no digits
		"Why transformers work": false, // title starting with W
		"Word embeddings":       false,
		"10.1/x":                false,
		"":                      false,
		"W12a":                  false, // non-digit after W
	}
	for in, want := range cases {
		if got := isOpenAlexWorkID(in); got != want {
			t.Errorf("isOpenAlexWorkID(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestOpenAlexTitleSeedStartingWithW guards the routing fix: a title seed that
// begins with "W" must fall through to title search, not the /works/{id} entity
// endpoint (regression for the audit finding).
func TestOpenAlexTitleSeedStartingWithW(t *testing.T) {
	var sawSearch bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("search") != "" {
			sawSearch = true
			_, _ = w.Write([]byte(`{"results":[{"id":"https://openalex.org/W9","display_name":"Why Transformers Work","publication_year":2021}]}`))
			return
		}
		// An entity-endpoint hit for a title would 404 — fail the test if reached.
		w.WriteHeader(404)
	}))
	defer srv.Close()
	p := NewOpenAlexProvider("e@x.com", newOpenAlexTestDeps())
	p.SetBaseURL(srv.URL)

	w, err := p.fetchWork(context.Background(), "Why transformers work")
	if err != nil {
		t.Fatalf("title seed starting with W should resolve via search, got error: %v", err)
	}
	if !sawSearch {
		t.Error("expected title seed to route to /works?search=, not the entity endpoint")
	}
	if w == nil || w.Title != "Why Transformers Work" {
		t.Errorf("unexpected work: %+v", w)
	}
}

func TestOpenAlexCitationInterface(t *testing.T) {
	var _ CitationSearcher = (*OpenAlexProvider)(nil)
}
