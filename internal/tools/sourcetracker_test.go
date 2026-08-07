package tools

import (
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/scraper"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// TestAcademicResultsToSourcesCarriesAuthorDateDOI proves the #532 fix:
// academicResultsToSources must carry Author/Date/DOI onto the session source
// instead of dropping them, since format_bibliography reads a session's
// sources directly and previously produced author/year/DOI-less citations
// for any source that arrived via session auto-tracking rather than an
// explicit sources list.
func TestAcademicResultsToSourcesCarriesAuthorDateDOI(t *testing.T) {
	results := []search.AcademicResult{
		{
			Title:   "Attention Is All You Need",
			URL:     "https://doi.org/10.1/x",
			DOI:     "10.1/x",
			Authors: []string{"Vaswani, A.", "Shazeer, N."},
			Year:    2017,
			Source:  "openalex",
		},
	}
	sources := academicResultsToSources(results)
	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
	s := sources[0]
	if s.DOI != "10.1/x" {
		t.Errorf("DOI = %q, want 10.1/x", s.DOI)
	}
	if s.Author != "Vaswani, A.; Shazeer, N." {
		t.Errorf("Author = %q, want %q", s.Author, "Vaswani, A.; Shazeer, N.")
	}
	if s.Date != "2017" {
		t.Errorf("Date = %q, want 2017", s.Date)
	}
	if s.Relevance != "academic" {
		t.Errorf("Relevance = %q, want academic", s.Relevance)
	}
}

// TestAcademicResultsToSourcesOmitsMissingFields proves a result with no
// authors/year/DOI produces a source with those fields empty, not "0" or a
// stray separator — the omitempty json tags on ResearchSource depend on this.
func TestAcademicResultsToSourcesOmitsMissingFields(t *testing.T) {
	results := []search.AcademicResult{{Title: "No Metadata", URL: "https://example.com/a", Source: "openalex"}}
	sources := academicResultsToSources(results)
	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
	s := sources[0]
	if s.DOI != "" || s.Author != "" || s.Date != "" {
		t.Errorf("expected empty Author/Date/DOI, got %+v", s)
	}
}

// TestPatentResultsToSourcesCarriesAuthorDate proves the #532 fix for patents:
// inventor and filing date survive onto the session source (mapped to
// Author/Date respectively) instead of being dropped.
func TestPatentResultsToSourcesCarriesAuthorDate(t *testing.T) {
	results := []scraper.PatentResult{
		{Title: "Video Codec", URL: "https://patents.google.com/patent/US1", Inventor: "Jane Doe", Filed: "2020-03-15"},
	}
	sources := patentResultsToSources(results)
	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
	s := sources[0]
	if s.Author != "Jane Doe" {
		t.Errorf("Author = %q, want Jane Doe", s.Author)
	}
	if s.Date != "2020-03-15" {
		t.Errorf("Date = %q, want 2020-03-15", s.Date)
	}
	if s.Relevance != "patent" {
		t.Errorf("Relevance = %q, want patent", s.Relevance)
	}
}
