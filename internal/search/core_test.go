package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newCORETestProvider(t *testing.T, apiKey string, handler http.HandlerFunc) *COREProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewCOREProvider(apiKey, Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	p.SetBaseURL(srv.URL)
	return p
}

func TestCOREProvider_Scholarly_Basic(t *testing.T) {
	p := newCORETestProvider(t, "", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/search/works") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{"results":[
			{"title":"Deep Learning for OA Discovery","abstract":"` + strings.Repeat("a", 600) + `","doi":"10.1234/abc","authors":[{"name":"Jane Doe"},{"name":"John Roe"}],"yearPublished":2024,"downloadUrl":"https://core.ac.uk/download/1.pdf"}
		]}`))
	})

	res, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "machine learning", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 result, got %d", len(res))
	}
	r := res[0]
	if !r.OpenAccess {
		t.Error("OpenAccess must be true for every CORE result")
	}
	if r.URL != "https://doi.org/10.1234/abc" {
		t.Errorf("URL = %q, want DOI-derived URL", r.URL)
	}
	if len([]rune(r.Abstract)) > 503 { // 500 + "..."
		t.Errorf("abstract not truncated: %d runes", len([]rune(r.Abstract)))
	}
	if len(r.Authors) != 2 {
		t.Errorf("Authors = %v, want 2", r.Authors)
	}
	if r.Source != "core" {
		t.Errorf("Source = %q, want core", r.Source)
	}
}

func TestCOREProvider_Scholarly_NoKey(t *testing.T) {
	p := newCORETestProvider(t, "", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("no Authorization header expected, got %q", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{"results":[]}`))
	})
	if _, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "x", NumResults: 5}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCOREProvider_Scholarly_WithKey(t *testing.T) {
	p := newCORETestProvider(t, "test-key", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", got)
		}
		w.Write([]byte(`{"results":[]}`))
	})
	if _, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "x", NumResults: 5}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCOREProvider_Scholarly_YearFilter(t *testing.T) {
	p := newCORETestProvider(t, "", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if !strings.Contains(q, "yearPublished>=2020") || !strings.Contains(q, "yearPublished<=2023") {
			t.Errorf("q = %q, want year-range clauses", q)
		}
		w.Write([]byte(`{"results":[]}`))
	})
	_, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "x", YearFrom: 2020, YearTo: 2023, NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCOREProvider_Scholarly_SortByDate(t *testing.T) {
	p := newCORETestProvider(t, "", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("sort") != "yearPublished:desc" {
			t.Errorf("sort = %q, want yearPublished:desc", r.URL.Query().Get("sort"))
		}
		w.Write([]byte(`{"results":[]}`))
	})
	_, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "x", SortBy: "date", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCOREProvider_Scholarly_FallbackURL(t *testing.T) {
	p := newCORETestProvider(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[{"title":"No DOI Work","downloadUrl":"https://core.ac.uk/download/2.pdf"}]}`))
	})
	res, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "x", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 || res[0].URL != "https://core.ac.uk/download/2.pdf" {
		t.Fatalf("want downloadUrl fallback, got %+v", res)
	}
}

func TestCOREProvider_Scholarly_AbstractFallback(t *testing.T) {
	longText := strings.Repeat("full text body ", 60)
	p := newCORETestProvider(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[{"title":"Full Text Only","doi":"10.1/xyz","fullText":"` + longText + `"}]}`))
	})
	res, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "x", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 || res[0].Abstract == "" {
		t.Fatalf("want abstract derived from fullText, got %+v", res)
	}
	if len([]rune(res[0].Abstract)) > 503 {
		t.Errorf("fullText-derived abstract not truncated: %d runes", len([]rune(res[0].Abstract)))
	}
}

func TestCOREProvider_Scholarly_SkipsEmptyURL(t *testing.T) {
	p := newCORETestProvider(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[{"title":"No DOI No Download"},{"title":"Has DOI","doi":"10.1/ok"}]}`))
	})
	res, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "x", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want the empty-URL work excluded, got %d results", len(res))
	}
}

func TestCOREProvider_Scholarly_NonOKStatus(t *testing.T) {
	p := newCORETestProvider(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("rate limited"))
	})
	if _, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "x", NumResults: 5}); err == nil {
		t.Error("want error on HTTP 429")
	}
}

func TestCOREProvider_Scholarly_MalformedJSON(t *testing.T) {
	p := newCORETestProvider(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{broken json`))
	})
	if _, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "x", NumResults: 5}); err == nil {
		t.Error("want error on malformed JSON")
	}
}

func TestCOREProvider_Scholarly_EmptyQuery(t *testing.T) {
	p := newCORETestProvider(t, "", func(w http.ResponseWriter, r *http.Request) {
		t.Error("empty query must not hit the API")
	})
	res, err := p.Scholarly(context.Background(), AcademicSearchParams{Query: "   ", NumResults: 5})
	if err != nil || res != nil {
		t.Errorf("empty query should be a no-op, got res=%v err=%v", res, err)
	}
}

func TestCOREProvider_Name(t *testing.T) {
	p := NewCOREProvider("", Deps{})
	if p.Name() != "core" {
		t.Errorf("Name() = %q, want core", p.Name())
	}
}

func TestCOREProvider_Metadata(t *testing.T) {
	p := NewCOREProvider("", Deps{})
	if !p.Metadata().HasCapability("fulltext") {
		t.Error("Metadata capabilities should include fulltext")
	}
}

func TestCOREKeyless(t *testing.T) {
	if p := NewAcademicProviderByName("core", AcademicProviderConfig{}, Deps{}); p == nil {
		t.Error("core should construct without any key")
	}
}
