package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

const testCrossRefResponse = `{
  "message": {
    "items": [
      {
        "DOI": "10.1038/s41586-021-03819-2",
        "title": ["Highly accurate protein structure prediction with AlphaFold"],
        "author": [
          {"given": "John", "family": "Jumper"},
          {"given": "Richard", "family": "Evans"},
          {"name": "DeepMind Team"}
        ],
        "container-title": ["Nature"],
        "published": {
          "date-parts": [[2021, 7, 15]]
        },
        "is-referenced-by-count": 18000,
        "link": [
          {"URL": "https://www.nature.com/articles/s41586-021-03819-2.pdf", "content-type": "application/pdf"}
        ],
        "abstract": "<jats:p>Proteins are essential to life, and understanding their structure can facilitate a mechanistic understanding of their function.</jats:p>"
      },
      {
        "DOI": "10.1126/science.abj8754",
        "title": ["Accurate prediction of protein structures and interactions using a three-track neural network"],
        "author": [
          {"given": "Minkyung", "family": "Baek"}
        ],
        "container-title": ["Science"],
        "issued": {
          "date-parts": [[2021, 8, 20]]
        },
        "is-referenced-by-count": 3500,
        "link": [],
        "abstract": ""
      },
      {
        "DOI": "10.1234/empty-title",
        "title": [""],
        "author": []
      }
    ]
  }
}`

const testCrossRefEmptyResponse = `{"message": {"items": []}}`

func TestCrossRefProvider_Scholarly(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/works" {
			w.WriteHeader(404)
			return
		}
		q := r.URL.Query()
		if q.Get("query") == "" {
			w.WriteHeader(400)
			return
		}
		if q.Get("mailto") != "test@example.com" {
			t.Errorf("expected mailto=test@example.com, got %s", q.Get("mailto"))
		}
		ua := r.Header.Get("User-Agent")
		if !strings.Contains(ua, "web-researcher-mcp") {
			t.Errorf("expected User-Agent to contain web-researcher-mcp, got %s", ua)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(testCrossRefResponse))
	}))
	defer srv.Close()

	provider := NewCrossRefProvider("test@example.com", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)

	results, err := provider.Scholarly(context.Background(), AcademicSearchParams{
		Query:      "protein structure prediction",
		NumResults: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Third item has empty title, should be skipped
	if len(results) != 2 {
		t.Fatalf("expected 2 results (empty title skipped), got %d", len(results))
	}

	r := results[0]
	if r.Title != "Highly accurate protein structure prediction with AlphaFold" {
		t.Errorf("unexpected title: %s", r.Title)
	}
	if r.DOI != "10.1038/s41586-021-03819-2" {
		t.Errorf("unexpected DOI: %s", r.DOI)
	}
	if r.Year != 2021 {
		t.Errorf("unexpected year: %d", r.Year)
	}
	if r.CitationCount != 18000 {
		t.Errorf("unexpected citation count: %d", r.CitationCount)
	}
	if len(r.Authors) != 3 {
		t.Fatalf("expected 3 authors, got %d", len(r.Authors))
	}
	if r.Authors[0] != "John Jumper" {
		t.Errorf("unexpected first author: %s", r.Authors[0])
	}
	if r.Authors[2] != "DeepMind Team" {
		t.Errorf("expected org author 'DeepMind Team', got: %s", r.Authors[2])
	}
	if r.Journal != "Nature" {
		t.Errorf("unexpected journal: %s", r.Journal)
	}
	if r.URL != "https://doi.org/10.1038/s41586-021-03819-2" {
		t.Errorf("unexpected URL: %s", r.URL)
	}
	if r.PDFUrl != "https://www.nature.com/articles/s41586-021-03819-2.pdf" {
		t.Errorf("unexpected PDF URL: %s", r.PDFUrl)
	}
	if r.Source != "crossref" {
		t.Errorf("unexpected source: %s", r.Source)
	}
	if r.Abstract == "" {
		t.Error("expected abstract to be cleaned from JATS XML")
	}
	if strings.Contains(r.Abstract, "<jats:p>") {
		t.Error("expected JATS XML tags to be stripped")
	}

	// Second result uses "issued" instead of "published"
	r2 := results[1]
	if r2.Year != 2021 {
		t.Errorf("expected year from issued date, got: %d", r2.Year)
	}
	if r2.PDFUrl != "" {
		t.Errorf("expected no PDF URL for empty links, got: %s", r2.PDFUrl)
	}
}

func TestCrossRefProvider_Filters(t *testing.T) {
	t.Parallel()

	var capturedFilter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedFilter = r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(testCrossRefEmptyResponse))
	}))
	defer srv.Close()

	provider := NewCrossRefProvider("test@example.com", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	provider.SetBaseURL(srv.URL)

	_, err := provider.Scholarly(context.Background(), AcademicSearchParams{
		Query:    "machine learning",
		YearFrom: 2020,
		YearTo:   2024,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedFilter == "" {
		t.Fatal("expected filter to be set")
	}
	if !strings.Contains(capturedFilter, "from-pub-date:2020") {
		t.Errorf("expected from-pub-date filter, got: %s", capturedFilter)
	}
	if !strings.Contains(capturedFilter, "until-pub-date:2024") {
		t.Errorf("expected until-pub-date filter, got: %s", capturedFilter)
	}
}

func TestCrossRefProvider_RateLimit(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))
	defer srv.Close()

	provider := NewCrossRefProvider("test@example.com", Deps{
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

func TestCrossRefProvider_ServerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		w.Write([]byte("service unavailable"))
	}))
	defer srv.Close()

	provider := NewCrossRefProvider("test@example.com", Deps{
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
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("expected 503 in error, got: %v", err)
	}
}

func TestCrossRefProvider_Metadata(t *testing.T) {
	t.Parallel()

	provider := NewCrossRefProvider("test@example.com", Deps{
		HTTPClient: http.DefaultClient,
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})

	if provider.Name() != "crossref" {
		t.Errorf("unexpected name: %s", provider.Name())
	}

	meta := provider.Metadata()
	if !meta.MatchesRegion("") {
		t.Error("expected worldwide provider to match empty region")
	}
	if !meta.HasCapability("biblio") {
		t.Error("expected biblio capability")
	}
	if !meta.HasCapability("citations") {
		t.Error("expected citations capability")
	}
}

func TestCleanCrossRefAbstract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain text",
			input: "Simple abstract text",
			want:  "Simple abstract text",
		},
		{
			name:  "JATS paragraph tags",
			input: "<jats:p>First paragraph.</jats:p><jats:p>Second paragraph.</jats:p>",
			want:  "First paragraph. Second paragraph.",
		},
		{
			name:  "JATS with formatting",
			input: "<jats:p>This is <jats:bold>bold</jats:bold> and <jats:italic>italic</jats:italic> text.</jats:p>",
			want:  "This is bold and italic text.",
		},
		{
			name:  "JATS with sub/sup",
			input: "<jats:p>H<jats:sub>2</jats:sub>O and E=mc<jats:sup>2</jats:sup></jats:p>",
			want:  "H2O and E=mc2",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanCrossRefAbstract(tt.input)
			if got != tt.want {
				t.Errorf("cleanCrossRefAbstract() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatCrossRefAuthor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		author crossRefAuthor
		want   string
	}{
		{
			name:   "given and family",
			author: crossRefAuthor{Given: "John", Family: "Doe"},
			want:   "John Doe",
		},
		{
			name:   "org name only",
			author: crossRefAuthor{Name: "World Health Organization"},
			want:   "World Health Organization",
		},
		{
			name:   "family only",
			author: crossRefAuthor{Family: "Smith"},
			want:   "Smith",
		},
		{
			name:   "empty",
			author: crossRefAuthor{},
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatCrossRefAuthor(tt.author)
			if got != tt.want {
				t.Errorf("formatCrossRefAuthor() = %q, want %q", got, tt.want)
			}
		})
	}
}
