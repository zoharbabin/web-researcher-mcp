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

func TestPubMedPMCID(t *testing.T) {
	tests := []struct {
		name string
		ids  []pubmedArticleID
		want string
	}{
		{"present", []pubmedArticleID{{IDType: "pmc", Value: "PMC3539452"}}, "PMC3539452"},
		{"mixed types", []pubmedArticleID{{IDType: "pubmed", Value: "42266835"}, {IDType: "doi", Value: "10.1/x"}, {IDType: "pmc", Value: "PMC123"}}, "PMC123"},
		{"absent", []pubmedArticleID{{IDType: "pubmed", Value: "42266835"}}, ""},
		{"nil", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pubmedPMCID(tt.ids); got != tt.want {
				t.Errorf("pubmedPMCID(%v) = %q, want %q", tt.ids, got, tt.want)
			}
		})
	}
}

const jatsFixture = `<?xml version="1.0"?>
<pmc-articleset><article>
<front><article-meta><abstract><p>This is the abstract.</p></abstract></article-meta></front>
<body><sec><p>This is body paragraph one.</p></sec><sec><p>This is body paragraph two.</p></sec></body>
</article></pmc-articleset>`

func TestPubMedFetchFullTextSuccess(t *testing.T) {
	p := newPubMedTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/efetch.fcgi") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("id") != "PMC3539452" {
			t.Errorf("id = %q, want PMC3539452", r.URL.Query().Get("id"))
		}
		if r.URL.Query().Get("db") != "pmc" {
			t.Errorf("db = %q, want pmc", r.URL.Query().Get("db"))
		}
		w.Write([]byte(jatsFixture))
	})
	text, err := p.FetchFullText(context.Background(), "PMC3539452")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(text, "This is the abstract.") {
		t.Errorf("text missing abstract: %q", text)
	}
	if !strings.Contains(text, "This is body paragraph one.") || !strings.Contains(text, "This is body paragraph two.") {
		t.Errorf("text missing body paragraphs: %q", text)
	}
}

const jatsFixtureLeadInAndNested = `<?xml version="1.0"?>
<pmc-articleset><article>
<front><article-meta><abstract><p>This is the abstract.</p></abstract></article-meta></front>
<body><p>Lead-in paragraph directly under body.</p><sec><p>Top-level section paragraph.</p><sec><p>Nested subsection paragraph.</p></sec></sec></body>
</article></pmc-articleset>`

// TestPubMedFetchFullTextLeadInAndNestedSections is a regression test: real
// PMC JATS XML commonly has a lead-in <p> directly under <body> before any
// <sec>, and <sec> can nest arbitrarily deep. Both must be captured, not just
// paragraphs one level under a single <sec>.
func TestPubMedFetchFullTextLeadInAndNestedSections(t *testing.T) {
	p := newPubMedTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(jatsFixtureLeadInAndNested))
	})
	text, err := p.FetchFullText(context.Background(), "PMC1111111")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(text, "Lead-in paragraph directly under body.") {
		t.Errorf("text missing lead-in paragraph: %q", text)
	}
	if !strings.Contains(text, "Top-level section paragraph.") {
		t.Errorf("text missing top-level section paragraph: %q", text)
	}
	if !strings.Contains(text, "Nested subsection paragraph.") {
		t.Errorf("text missing nested subsection paragraph: %q", text)
	}
}

func TestPubMedFetchFullTextEmpty(t *testing.T) {
	p := newPubMedTestProvider(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("empty PMCID must not make an HTTP call")
	})
	text, err := p.FetchFullText(context.Background(), "")
	if err != nil || text != "" {
		t.Errorf("empty PMCID should be a no-op, got text=%q err=%v", text, err)
	}
}

func TestPubMedFetchFullTextHTTPError(t *testing.T) {
	p := newPubMedTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	text, err := p.FetchFullText(context.Background(), "PMC0000000")
	if err != nil {
		t.Errorf("HTTP error should be a soft failure (nil err), got %v", err)
	}
	if text != "" {
		t.Errorf("text = %q, want empty on HTTP error", text)
	}
}

func TestPubMedFetchFullTextBadXML(t *testing.T) {
	p := newPubMedTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<<<not xml>>>`))
	})
	text, err := p.FetchFullText(context.Background(), "PMC1234567")
	if err != nil {
		t.Errorf("malformed XML should be a soft failure (nil err), got %v", err)
	}
	if text != "" {
		t.Errorf("text = %q, want empty on malformed XML", text)
	}
}

func pubmedFullTextMux(t *testing.T, efetchCalled *bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/esearch.fcgi"):
			w.Write([]byte(`{"esearchresult":{"count":"1","idlist":["42266835"]}}`))
		case strings.Contains(r.URL.Path, "/esummary.fcgi"):
			w.Write([]byte(`{"result":{
				"uids":["42266835"],
				"42266835":{
					"uid":"42266835",
					"title":"CRISPR base editing in practice.",
					"authors":[{"name":"Smith J","authtype":"Author"}],
					"sortpubdate":"2026/06/01 00:00",
					"source":"Curr Opin Biomed Eng",
					"articleids":[
						{"idtype":"pubmed","value":"42266835"},
						{"idtype":"pmc","value":"PMC3539452"}
					]
				}
			}}`))
		case strings.Contains(r.URL.Path, "/efetch.fcgi"):
			*efetchCalled = true
			w.Write([]byte(jatsFixture))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}
}

func TestPubMedSearchFullTextSkippedWhenFalse(t *testing.T) {
	var efetchCalled bool
	p := newPubMedTestProvider(t, pubmedFullTextMux(t, &efetchCalled))
	res, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "CRISPR", NumResults: 5, FullText: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if efetchCalled {
		t.Error("efetch should not be called when FullText is false")
	}
	if len(res) != 1 || res[0].FullText != "" {
		t.Errorf("FullText should be empty when not requested, got %q", res[0].FullText)
	}
}

func TestPubMedSearchFullTextCalledWhenTrue(t *testing.T) {
	var efetchCalled bool
	p := newPubMedTestProvider(t, pubmedFullTextMux(t, &efetchCalled))
	res, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "CRISPR", NumResults: 5, FullText: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !efetchCalled {
		t.Error("efetch should be called when FullText is true and a PMCID is present")
	}
	if len(res) != 1 || res[0].FullText == "" {
		t.Fatalf("FullText should be populated, got %q", res[0].FullText)
	}
	if !strings.Contains(res[0].FullText, "This is the abstract.") {
		t.Errorf("FullText missing abstract: %q", res[0].FullText)
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
