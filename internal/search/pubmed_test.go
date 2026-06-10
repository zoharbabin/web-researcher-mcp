package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newPubMedTestProvider(t *testing.T, handler http.HandlerFunc) *PubMedProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewPubMedProvider("", "", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	p.SetBaseURL(srv.URL)
	return p
}

func TestPubMedKeyless(t *testing.T) {
	// PubMed works keyless — it must always construct.
	if p := NewAcademicProviderByName("pubmed", AcademicProviderConfig{}, Deps{}); p == nil {
		t.Error("pubmed should construct without any key")
	}
}

// TestPubMedSearch runs the full esearch→esummary flow against fixtures shaped
// like the real E-utilities JSON, asserting DOI/authors/year/venue/URL mapping.
func TestPubMedSearch(t *testing.T) {
	p := newPubMedTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/esearch.fcgi"):
			if r.URL.Query().Get("term") == "" {
				t.Error("esearch must send a term")
			}
			if r.URL.Query().Get("retmode") != "json" {
				t.Error("retmode=json required")
			}
			w.Write([]byte(`{"esearchresult":{"count":"2","idlist":["42266835","99999999"]}}`))
		case strings.Contains(r.URL.Path, "/esummary.fcgi"):
			if !strings.Contains(r.URL.Query().Get("id"), "42266835") {
				t.Errorf("esummary id missing PMID: %q", r.URL.Query().Get("id"))
			}
			w.Write([]byte(`{"result":{
				"uids":["42266835","99999999"],
				"42266835":{
					"uid":"42266835",
					"title":"CRISPR base editing in practice.",
					"authors":[{"name":"Pattali RK","authtype":"Author"},{"name":"Smith J","authtype":"Author"},{"name":"Editorial Board","authtype":"PublisherName"}],
					"sortpubdate":"2026/06/01 00:00",
					"pubdate":"2026 Jun",
					"source":"Curr Opin Biomed Eng",
					"fulljournalname":"Current opinion in biomedical engineering",
					"articleids":[
						{"idtype":"pubmed","value":"42266835"},
						{"idtype":"doi","value":"10.1016/j.cobme.2026.100654"}
					]
				},
				"99999999":{"uid":"99999999","error":"cannot get document summary"}
			}}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	})

	res, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "CRISPR gene editing", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The bad-PMID record (with an error field) is skipped; one valid record remains.
	if len(res) != 1 {
		t.Fatalf("want 1 record (bad PMID skipped), got %d", len(res))
	}
	r := res[0]
	if r.DOI != "10.1016/j.cobme.2026.100654" {
		t.Errorf("DOI = %q, want the articleids doi", r.DOI)
	}
	if r.Year != 2026 {
		t.Errorf("Year = %d, want 2026", r.Year)
	}
	if r.Journal != "Current opinion in biomedical engineering" {
		t.Errorf("Journal = %q, want full journal name", r.Journal)
	}
	if len(r.Authors) != 2 { // PublisherName is excluded
		t.Errorf("Authors = %v, want 2 (author types only)", r.Authors)
	}
	if r.URL != "https://pubmed.ncbi.nlm.nih.gov/42266835/" {
		t.Errorf("URL = %q", r.URL)
	}
	if r.Source != "pubmed" {
		t.Errorf("Source = %q, want pubmed", r.Source)
	}
}

func TestPubMedDateRange(t *testing.T) {
	p := newPubMedTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/esearch.fcgi") {
			if r.URL.Query().Get("mindate") != "2020" || r.URL.Query().Get("maxdate") != "2021" {
				t.Errorf("date range params wrong: min=%q max=%q", r.URL.Query().Get("mindate"), r.URL.Query().Get("maxdate"))
			}
			if r.URL.Query().Get("datetype") != "pdat" {
				t.Error("datetype=pdat expected")
			}
			w.Write([]byte(`{"esearchresult":{"count":"0","idlist":[]}}`))
			return
		}
		t.Errorf("esummary should not be called for an empty idlist")
	})
	res, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "x", YearFrom: 2020, YearTo: 2021, NumResults: 5})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("empty idlist should yield no results, got %d", len(res))
	}
}

func TestPubMedEmptyQuery(t *testing.T) {
	p := newPubMedTestProvider(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("empty query must not hit the API")
	})
	res, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "   ", NumResults: 5})
	if err != nil || res != nil {
		t.Errorf("empty query should be a no-op, got res=%v err=%v", res, err)
	}
}

func TestPubMedSearchError(t *testing.T) {
	p := newPubMedTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"esearchresult":{"ERROR":"Empty term and query_key - nothing todo"}}`))
	})
	if _, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "x", NumResults: 5}); err == nil {
		t.Error("an esearchresult.ERROR should surface as an error")
	}
}

func TestPubMedAuthParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "k123" {
			t.Errorf("api_key not forwarded: %q", r.URL.Query().Get("api_key"))
		}
		if r.URL.Query().Get("tool") != "web-researcher-mcp" {
			t.Error("tool param should be set")
		}
		w.Write([]byte(`{"esearchresult":{"count":"0","idlist":[]}}`))
	}))
	t.Cleanup(srv.Close)
	p := NewPubMedProvider("k123", "me@example.org", Deps{HTTPClient: srv.Client(), Breaker: circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60})})
	p.SetBaseURL(srv.URL)
	_, _ = p.Scholarly(context.Background(), AcademicSearchParams{Query: "x", NumResults: 1})
}
