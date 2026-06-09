package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newRetractionResolver(t *testing.T, handler http.HandlerFunc) *CrossrefRetractionResolver {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	r := NewCrossrefRetractionResolver("test@example.com", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	r.SetBaseURL(srv.URL)
	return r
}

func TestRetraction_Retracted(t *testing.T) {
	t.Parallel()
	r := newRetractionResolver(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Shape verified against the live Wakefield-paper response.
		_, _ = w.Write([]byte(`{"message":{"updated-by":[
			{"DOI":"10.1016/s0140-6736(10)60175-4","type":"retraction","label":"Retraction","source":"retraction-watch","updated":{"date-parts":[[2010,2,6]],"date-time":"2010-02-06T00:00:00Z"}}
		]}}`))
	})
	st, found, err := r.Resolve(context.Background(), "10.1016/S0140-6736(97)11096-0")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if st == nil || !st.Retracted || st.Kind != RetractionKindRetraction {
		t.Fatalf("status=%+v, want retracted/retraction", st)
	}
	if st.Date != "2010-02-06" || st.NoticeDOI != "10.1016/s0140-6736(10)60175-4" || st.Source != "retraction-watch" {
		t.Errorf("fields=%+v", st)
	}
}

func TestRetraction_ExpressionOfConcernNotRetracted(t *testing.T) {
	t.Parallel()
	r := newRetractionResolver(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"updated-by":[
			{"DOI":"10.1/eoc","type":"expression_of_concern","source":"publisher","updated":{"date-parts":[[2021]]}}
		]}}`))
	})
	st, _, _ := r.Resolve(context.Background(), "10.1/x")
	if st == nil || st.Retracted || st.Kind != RetractionKindConcern {
		t.Fatalf("EoC must not be 'retracted': %+v", st)
	}
	if st.Date != "2021" {
		t.Errorf("year-only date = %q, want 2021", st.Date)
	}
}

func TestRetraction_MostSevereWins(t *testing.T) {
	t.Parallel()
	// A work with both a correction and a retraction → retraction must win.
	r := newRetractionResolver(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"updated-by":[
			{"DOI":"10.1/corr","type":"correction","source":"publisher","updated":{"date-parts":[[2019,1,1]]}},
			{"DOI":"10.1/retr","type":"retraction","source":"retraction-watch","updated":{"date-parts":[[2020,5,5]]}}
		]}}`))
	})
	st, _, _ := r.Resolve(context.Background(), "10.1/x")
	if st == nil || !st.Retracted || st.NoticeDOI != "10.1/retr" {
		t.Fatalf("most-severe (retraction) should win: %+v", st)
	}
}

func TestRetraction_CleanWorkNoStatus(t *testing.T) {
	t.Parallel()
	r := newRetractionResolver(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"title":["A fine paper"]}}`)) // no updated-by key
	})
	st, found, err := r.Resolve(context.Background(), "10.1/clean")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if st != nil {
		t.Errorf("clean work must yield nil status, got %+v", st)
	}
}

func TestRetraction_404IsNoop(t *testing.T) {
	t.Parallel()
	r := newRetractionResolver(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})
	st, found, err := r.Resolve(context.Background(), "10.1/unknown")
	if err != nil {
		t.Fatalf("404 must be a no-op (nil err), got %v", err)
	}
	if found || st != nil {
		t.Errorf("404 should yield no status: found=%v st=%+v", found, st)
	}
}

func TestRetraction_429IsError(t *testing.T) {
	t.Parallel()
	r := newRetractionResolver(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
	})
	if _, _, err := r.Resolve(context.Background(), "10.1/x"); err == nil {
		t.Error("429 should surface an error (best-effort caller skips it)")
	}
}

func TestRetraction_IgnoresVersionUpdates(t *testing.T) {
	t.Parallel()
	r := newRetractionResolver(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"updated-by":[
			{"DOI":"10.1/v2","type":"new_version","source":"publisher","updated":{"date-parts":[[2022,1,1]]}}
		]}}`))
	})
	st, found, _ := r.Resolve(context.Background(), "10.1/x")
	if !found || st != nil {
		t.Errorf("new_version is not an integrity notice: st=%+v", st)
	}
}

func TestEnrichRetraction_NilSafeAndBestEffort(t *testing.T) {
	t.Parallel()
	results := []AcademicResult{{DOI: "10.1/x"}, {Title: "no doi"}}
	// nil resolver → unchanged.
	out := EnrichRetraction(context.Background(), nil, results)
	if out[0].Retraction != nil {
		t.Error("nil resolver must be a no-op")
	}
	// resolver that errors → leaves results un-flagged, no panic.
	r := newRetractionResolver(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) })
	out = EnrichRetraction(context.Background(), r, results)
	for _, x := range out {
		if x.Retraction != nil {
			t.Error("errored resolve must leave result un-flagged")
		}
	}
}

func TestNormalizeDOI(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"https://doi.org/10.1/x": "10.1/x",
		"doi:10.1/x":             "10.1/x",
		"  10.1/x  ":             "10.1/x",
		"":                       "",
	} {
		if got := normalizeDOI(in); got != want {
			t.Errorf("normalizeDOI(%q)=%q want %q", in, got, want)
		}
	}
}

func TestCrossrefEscapeDOI(t *testing.T) {
	t.Parallel()
	// The DOI's own slash is a real path separator and is preserved; a simple
	// alphanumeric DOI is unchanged.
	if got := crossrefEscapeDOI("10.1038/nature12373"); got != "10.1038/nature12373" {
		t.Errorf("simple DOI must be unchanged: %q", got)
	}
	// A DOI with sub-delim chars (parens) keeps its slash structure; the parens
	// may be percent-encoded — Crossref decodes them, and the live polite-pool
	// call still resolves (asserted in the integration tests). The guarantee here
	// is that the slash count (path depth) is preserved and the host can't change.
	got := crossrefEscapeDOI("10.1016/S0140-6736(97)11096-0")
	if strings.Count(got, "/") != 1 {
		t.Errorf("slash structure (path depth) must be preserved: %q", got)
	}
	// Unsafe chars within a segment are escaped (defense in depth), so a "../"
	// segment can only 404 — it can't climb the path or change the host.
	if got := crossrefEscapeDOI("10.1/a b"); got != "10.1/a%20b" {
		t.Errorf("space not escaped: %q", got)
	}
}
