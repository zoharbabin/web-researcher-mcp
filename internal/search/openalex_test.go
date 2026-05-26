package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

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
        "source": {"display_name": "Advances in Neural Information Processing Systems"}
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

	// Second result should have no abstract (nil inverted index)
	if results[1].Abstract != "" {
		t.Errorf("expected empty abstract for nil inverted index, got: %s", results[1].Abstract)
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

