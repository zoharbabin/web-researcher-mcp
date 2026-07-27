package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newScholarAPITestProvider(t *testing.T, handler http.HandlerFunc) *ScholarAPIProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewScholarAPIProvider("test-key", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	p.SetBaseURL(srv.URL)
	return p
}

func TestScholarAPIRequiresKey(t *testing.T) {
	if p := NewAcademicProviderByName("scholarapi", AcademicProviderConfig{}, Deps{}); p != nil {
		t.Error("scholarapi should not construct without SCHOLAR_API_KEY")
	}
	if p := NewAcademicProviderByName("scholarapi", AcademicProviderConfig{ScholarAPIKey: "k"}, Deps{}); p == nil {
		t.Error("scholarapi should construct once a key is set")
	}
}

func TestScholarAPIExplicitOnly(t *testing.T) {
	for _, name := range SupportedAcademicProviders {
		if name == "scholarapi" {
			t.Fatal("scholarapi must not appear in SupportedAcademicProviders — it is explicit-only")
		}
	}
	found := false
	for _, name := range AcademicProvidersExplicitOnly {
		if name == "scholarapi" {
			found = true
		}
	}
	if !found {
		t.Fatal("scholarapi must be listed in AcademicProvidersExplicitOnly")
	}
}

func TestScholarAPISearch(t *testing.T) {
	p := newScholarAPITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/search") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("X-API-Key") != "test-key" {
			t.Errorf("X-API-Key = %q, want test-key", r.Header.Get("X-API-Key"))
		}
		if r.URL.Query().Get("q") == "" {
			t.Error("q param must be set")
		}
		w.Write([]byte(`{"papers":[
			{"id":"p1","title":"Base editing at scale","url":"https://example.org/p1","doi":"10.1/abc","authors":["A. One","B. Two"],"journal":"Nature","published_date":"2024-03-15","abstract":"short abstract","has_text":true,"has_pdf":false},
			{"id":"p2","title":"","doi":"10.1/skip"}
		]}`))
	})

	res, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "base editing", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 result (empty-title record skipped), got %d", len(res))
	}
	r := res[0]
	if r.Title != "Base editing at scale" {
		t.Errorf("Title = %q", r.Title)
	}
	if r.DOI != "10.1/abc" {
		t.Errorf("DOI = %q", r.DOI)
	}
	if r.Year != 2024 {
		t.Errorf("Year = %d, want 2024", r.Year)
	}
	if r.Journal != "Nature" {
		t.Errorf("Journal = %q", r.Journal)
	}
	if len(r.Authors) != 2 {
		t.Errorf("Authors = %v, want 2", r.Authors)
	}
	if !r.HasText || r.HasPDF {
		t.Errorf("HasText=%v HasPDF=%v, want true/false", r.HasText, r.HasPDF)
	}
	if r.Source != "scholarapi" {
		t.Errorf("Source = %q, want scholarapi", r.Source)
	}
}

func TestScholarAPISearchURLFallsBackToDOI(t *testing.T) {
	p := newScholarAPITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"papers":[{"id":"p1","title":"No URL paper","doi":"10.1/nourl"}]}`))
	})
	res, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "x", NumResults: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 result, got %d", len(res))
	}
	if res[0].URL != "https://doi.org/10.1/nourl" {
		t.Errorf("URL = %q, want doi.org fallback", res[0].URL)
	}
}

func TestScholarAPIResolveByDOIMatch(t *testing.T) {
	p := newScholarAPITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"papers":[{"id":"p1","title":"Exact match paper","doi":"10.1/Exact"}]}`))
	})
	r, err := p.ResolveByDOI(context.Background(), "10.1/exact")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil || r.Title != "Exact match paper" {
		t.Fatalf("want matched result, got %+v", r)
	}
}

func TestScholarAPIResolveByDOIMismatch(t *testing.T) {
	p := newScholarAPITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"papers":[{"id":"p1","title":"Wrong paper","doi":"10.1/other"}]}`))
	})
	r, err := p.ResolveByDOI(context.Background(), "10.1/wanted")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r != nil {
		t.Fatalf("mismatched DOI must return nil, got %+v", r)
	}
}

func TestScholarAPIResolveByDOIEmpty(t *testing.T) {
	p := newScholarAPITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("must not call the API for an empty DOI")
	})
	r, err := p.ResolveByDOI(context.Background(), "")
	if err != nil || r != nil {
		t.Fatalf("empty DOI should short-circuit to (nil, nil), got %+v, %v", r, err)
	}
}

func TestScholarAPIFetchText(t *testing.T) {
	p := newScholarAPITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/text/p1") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{"text":"full text body"}`))
	})
	text, err := p.FetchText(context.Background(), "p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "full text body" {
		t.Errorf("text = %q", text)
	}
}

func TestScholarAPIFetchTextNotFound(t *testing.T) {
	p := newScholarAPITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	text, err := p.FetchText(context.Background(), "missing")
	if err != nil {
		t.Fatalf("404 should be a soft miss, not an error: %v", err)
	}
	if text != "" {
		t.Errorf("text = %q, want empty", text)
	}
}

func TestScholarAPIFetchTexts(t *testing.T) {
	p := newScholarAPITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/texts/p1,p2") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{"texts":{"p1":"text one","p2":""}}`))
	})
	texts, err := p.FetchTexts(context.Background(), []string{"p1", "p2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if texts["p1"] != "text one" {
		t.Errorf("p1 = %q", texts["p1"])
	}
	if _, ok := texts["p2"]; ok {
		t.Error("empty-text id p2 should be omitted from the map")
	}
}

func TestScholarAPIFetchTextsEmptyInput(t *testing.T) {
	p := newScholarAPITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("must not call the API for an all-blank id list")
	})
	texts, err := p.FetchTexts(context.Background(), []string{"", "  "})
	if err != nil || len(texts) != 0 {
		t.Fatalf("want empty map, nil error, got %+v, %v", texts, err)
	}
}

// TestScholarAPI402DoesNotTripBreaker proves the core resiliency requirement
// from issue #266: repeated 402s (credits exhausted) must never open the
// circuit breaker, since that's a billing state a human must resolve, not a
// transient fault the breaker should protect against.
func TestScholarAPI402DoesNotTripBreaker(t *testing.T) {
	p := newScholarAPITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
	})
	for i := 0; i < 10; i++ {
		if _, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "x", NumResults: 1}); err == nil {
			t.Fatal("expected an error surfaced to the caller on 402")
		}
	}
	if got := p.deps.Breaker.State(); got != circuit.StateClosed {
		t.Errorf("breaker state = %v after repeated 402s, want StateClosed", got)
	}
}

func TestScholarAPI429OpensBreakerImmediately(t *testing.T) {
	p := newScholarAPITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	if _, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "x", NumResults: 1}); err == nil {
		t.Fatal("expected an error on 429")
	}
	if got := p.deps.Breaker.State(); got != circuit.StateOpen {
		t.Errorf("breaker state = %v after one 429, want StateOpen", got)
	}
}

func TestScholarAPI401Unauthorized(t *testing.T) {
	p := newScholarAPITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "x", NumResults: 1})
	if err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("err = %v, want authentication failed", err)
	}
}

func TestScholarAPIEmptyQuery(t *testing.T) {
	p := newScholarAPITestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("must not call the API for an empty query")
	})
	res, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "   "})
	if err != nil || res != nil {
		t.Fatalf("want (nil, nil) for blank query, got %+v, %v", res, err)
	}
}

func TestScholarAPIYearParsing(t *testing.T) {
	cases := map[string]int{
		"2024-03-15": 2024,
		"2024":       2024,
		"":           0,
		"abcd":       0,
		"20":         0,
	}
	for input, want := range cases {
		if got := scholarAPIYear(input); got != want {
			t.Errorf("scholarAPIYear(%q) = %d, want %d", input, got, want)
		}
	}
}
