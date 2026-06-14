package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newEDGARTestProvider(t *testing.T, handler http.HandlerFunc) *EDGARProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewEDGARProvider("web-researcher-mcp/test (test@example.com)", Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	// All three bases point at the one test server; routing is by path.
	p.SetBaseURLs(srv.URL, srv.URL, srv.URL)
	return p
}

func TestEDGARRequiresUserAgent(t *testing.T) {
	// Factory must skip EDGAR when no contact UA is configured.
	if p := NewFilingProviderByName("edgar", FilingProviderConfig{}, Deps{}); p != nil {
		t.Error("edgar must be nil without a User-Agent")
	}
	if p := NewFilingProviderByName("edgar", FilingProviderConfig{EDGARUserAgent: "x/1 (a@b.c)"}, Deps{}); p == nil {
		t.Error("edgar should construct with a User-Agent")
	}
}

func TestEDGARSetsUserAgent(t *testing.T) {
	var gotUA string
	p := newEDGARTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Write([]byte(`{"hits":{"hits":[]}}`))
	})
	_, _ = p.Filings(context.Background(), FilingSearchParams{Query: "climate risk disclosure"})
	if !strings.Contains(gotUA, "test@example.com") {
		t.Errorf("EDGAR must send a contact User-Agent, got %q", gotUA)
	}
}

func TestEDGARFullTextSearch(t *testing.T) {
	p := newEDGARTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		// A non-ticker query first probes the ticker map (no match → empty), then
		// falls through to EFTS full-text search.
		if strings.Contains(r.URL.Path, "company_tickers.json") {
			w.Write([]byte(`{}`))
			return
		}
		if !strings.Contains(r.URL.Path, "search-index") {
			t.Errorf("free-text query should hit EFTS, got %s", r.URL.Path)
		}
		w.Write([]byte(`{"hits":{"hits":[{"_id":"0000035527-22-000119:doc.htm","_source":{"adsh":"0000035527-22-000119","form":"10-K","file_date":"2022-02-25","period_ending":"2021-12-31","display_names":["FIFTH THIRD BANCORP (FITB)"],"ciks":["0000035527"]}}]}}`))
	})
	res, err := p.Filings(context.Background(), FilingSearchParams{Query: "climate risk disclosure", FormType: "10-K", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 filing, got %d", len(res))
	}
	r := res[0]
	if r.FormType != "10-K" || r.Accession != "0000035527-22-000119" || r.Source != "edgar" {
		t.Errorf("unexpected mapping: %+v", r)
	}
	if !strings.HasPrefix(r.URL, "https://www.sec.gov/Archives/edgar/data/35527/") {
		t.Errorf("document URL wrong: %s", r.URL)
	}
}

func TestEDGARCompanySubmissionsByTicker(t *testing.T) {
	p := newEDGARTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "company_tickers.json"):
			w.Write([]byte(`{"0":{"cik_str":320193,"ticker":"AAPL","title":"Apple Inc."}}`))
		case strings.Contains(r.URL.Path, "submissions/CIK0000320193.json"):
			w.Write([]byte(`{"name":"Apple Inc.","filings":{"recent":{"accessionNumber":["0000320193-24-000001","0000320193-24-000002"],"form":["10-K","8-K"],"filingDate":["2024-11-01","2024-08-01"],"reportDate":["2024-09-28","2024-08-01"],"primaryDocument":["aapl-10k.htm","aapl-8k.htm"],"primaryDocDescription":["10-K","8-K"]}}}`))
		default:
			w.WriteHeader(404)
		}
	})
	res, err := p.Filings(context.Background(), FilingSearchParams{Ticker: "AAPL", FormType: "10-K", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("form_type filter should yield 1 (the 10-K), got %d", len(res))
	}
	if res[0].Company != "Apple Inc." || res[0].FormType != "10-K" {
		t.Errorf("unexpected: %+v", res[0])
	}
}

func TestEDGARCompanyFacts(t *testing.T) {
	p := newEDGARTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "company_tickers.json"):
			w.Write([]byte(`{"0":{"cik_str":320193,"ticker":"AAPL","title":"Apple Inc."}}`))
		case strings.Contains(r.URL.Path, "companyfacts/CIK0000320193.json"):
			w.Write([]byte(`{"entityName":"Apple Inc.","facts":{"us-gaap":{"NetIncomeLoss":{"label":"Net Income","units":{"USD":[{"end":"2022-09-24","val":99803000000,"form":"10-K","fy":2022},{"end":"2023-09-30","val":96995000000,"form":"10-K","fy":2023}]}}}}}`))
		default:
			w.WriteHeader(404)
		}
	})
	res, err := p.Filings(context.Background(), FilingSearchParams{Ticker: "AAPL", Facts: true, NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 fact, got %d", len(res))
	}
	if res[0].Concept != "NetIncomeLoss" || res[0].Unit != "USD" || res[0].Value != 96995000000 {
		t.Errorf("facts must pass through latest value verbatim: %+v", res[0])
	}
}

// TestEDGARRaggedSubmissions guards against a panic when SEC returns parallel
// arrays of unequal length (untrusted external data). Regression for the audit
// finding that r.Form[i] was direct-indexed.
func TestEDGARRaggedSubmissions(t *testing.T) {
	p := newEDGARTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "company_tickers.json"):
			w.Write([]byte(`{"0":{"cik_str":1,"ticker":"X","title":"X Corp"}}`))
		case strings.Contains(r.URL.Path, "submissions/"):
			// form array shorter than accessionNumber — must not panic.
			w.Write([]byte(`{"name":"X Corp","filings":{"recent":{"accessionNumber":["a-1","a-2"],"form":["10-K"],"filingDate":["2024-01-01","2024-02-01"]}}}`))
		default:
			w.WriteHeader(404)
		}
	})
	// With a form filter, the loop reaches i=1 where form[1] is out of range.
	res, err := p.Filings(context.Background(), FilingSearchParams{Ticker: "X", FormType: "10-K", NumResults: 5})
	if err != nil {
		t.Fatalf("ragged arrays must not error/panic: %v", err)
	}
	// Only the first (i=0, form present) row qualifies; i=1 is skipped safely.
	if len(res) != 1 {
		t.Errorf("want 1 result from the well-formed row, got %d", len(res))
	}
}

func TestEDGARRateLimit(t *testing.T) {
	p := newEDGARTestProvider(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) })
	_, err := p.Filings(context.Background(), FilingSearchParams{Query: "anything full text"})
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("expected rate-limit error, got %v", err)
	}
}

func TestEDGARHelpers(t *testing.T) {
	if padCIK("320193") != "0000320193" {
		t.Error("padCIK")
	}
	if !isAllDigits("12345") || isAllDigits("AAPL") {
		t.Error("isAllDigits")
	}
	if !dateInRange("2022-05-01", "2022-01-01", "2022-12-31") || dateInRange("2021-01-01", "2022-01-01", "") {
		t.Error("dateInRange")
	}
	if filingURL("0000320193", "0000320193-24-000001", "doc.htm") != "https://www.sec.gov/Archives/edgar/data/320193/000032019324000001/doc.htm" {
		t.Errorf("filingURL: %s", filingURL("0000320193", "0000320193-24-000001", "doc.htm"))
	}
}

// TestLatestFactPrefers10K guards issue #207: latestFact must return the most-recent
// 10-K annual entry over a more-recent 10-Q quarterly entry to avoid mixing stale
// annual Revenues with current-quarter NetIncomeLoss in the same snapshot.
func TestLatestFactPrefers10K(t *testing.T) {
	t.Parallel()
	type dp = struct {
		End  string  `json:"end"`
		Val  float64 `json:"val"`
		Form string  `json:"form"`
		FY   int     `json:"fy"`
	}
	units := map[string][]dp{
		"USD": {
			{End: "2018-12-31", Val: 100, Form: "10-K", FY: 2018},
			{End: "2024-03-31", Val: 200, Form: "10-Q", FY: 2024}, // more recent but quarterly
			{End: "2023-12-31", Val: 150, Form: "10-K", FY: 2023}, // most-recent annual
		},
	}
	unit, best := latestFact(units)
	if unit != "USD" {
		t.Fatalf("unit = %q, want USD", unit)
	}
	if best.Form != "10-K" {
		t.Errorf("form = %q, want 10-K (annual should beat more-recent quarterly)", best.Form)
	}
	if best.Val != 150 {
		t.Errorf("val = %v, want 150 (most-recent 10-K FY2023)", best.Val)
	}
}

// TestLatestFactFallsBackWhenNo10K verifies that when there is no 10-K entry at all,
// latestFact returns the most-recent entry of any form type (honest fallback).
func TestLatestFactFallsBackWhenNo10K(t *testing.T) {
	t.Parallel()
	type dp = struct {
		End  string  `json:"end"`
		Val  float64 `json:"val"`
		Form string  `json:"form"`
		FY   int     `json:"fy"`
	}
	units := map[string][]dp{
		"USD": {
			{End: "2023-06-30", Val: 50, Form: "10-Q", FY: 2023},
			{End: "2024-03-31", Val: 75, Form: "10-Q", FY: 2024},
		},
	}
	unit, best := latestFact(units)
	if unit != "USD" {
		t.Fatalf("unit = %q, want USD", unit)
	}
	if best.Val != 75 || best.End != "2024-03-31" {
		t.Errorf("fallback should pick latest-end 10-Q: got val=%v end=%q", best.Val, best.End)
	}
}

func TestEDGARInterface(t *testing.T) {
	var _ FilingProvider = (*EDGARProvider)(nil)
}

// TestEDGARCompanyNameResolution validates the stop-word stripping + nameMap lookup
// that fixes #238 (free-text queries like "Apple annual report 10-K" must resolve
// to the correct CIK without falling through to the EFTS full-text search).
func TestEDGARCompanyNameResolution(t *testing.T) {
	t.Parallel()

	// nameMap fixture: keys are upper-cased SEC company titles.
	nameMap := map[string]string{
		"APPLE INC":          "0000320193",
		"NVIDIA CORP":        "0001045810",
		"META PLATFORMS INC": "0001326801",
	}

	p := &EDGARProvider{
		nameMap:   nameMap,
		tickerMap: map[string]string{},
	}

	tests := []struct {
		name    string
		query   string
		wantCIK string
		wantHit bool
	}{
		{"exact company title", "Apple Inc", "0000320193", true},
		{"stop-word stripping", "Apple annual report 10-K", "0000320193", true},
		{"single token prefix scan", "Apple", "0000320193", true},
		{"single token case insensitive", "nvidia", "0001045810", true},
		// Two name tokens remain after stripping ("Meta Platforms") but the nameMap
		// key is "META PLATFORMS INC" — no exact match and no prefix scan (prefix
		// scan is single-token only to avoid false positives). Falls to EFTS. This
		// is correct behavior: the EFTS full-text search handles multi-token company
		// names better than a partial nameMap lookup.
		{"multi-token no exact match falls through", "Meta Platforms annual filing", "", false},
		{"all stop-words → no match", "annual report 10-K for the company", "", false},
		{"unknown company → no match", "BogusWidgetCo", "", false},
		{"ambiguous multi-token → no prefix scan", "Apple Meta annual", "", false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cik, _ := p.resolveByCompanyName(tc.query)
			if tc.wantHit && cik != tc.wantCIK {
				t.Errorf("resolveByCompanyName(%q) = %q, want %q", tc.query, cik, tc.wantCIK)
			}
			if !tc.wantHit && cik != "" {
				t.Errorf("resolveByCompanyName(%q) should return empty, got %q", tc.query, cik)
			}
		})
	}
}

// TestEDGARQueryRoutesViaNameMap verifies the end-to-end path: a natural-language
// query that contains a known company name hits the submissions API (not EFTS).
func TestEDGARQueryRoutesViaNameMap(t *testing.T) {
	t.Parallel()

	eftsCalled := false
	p := newEDGARTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "company_tickers.json"):
			// Ticker: AAPL, Title: "APPLE INC" — company name present in the map.
			w.Write([]byte(`{"0":{"cik_str":320193,"ticker":"AAPL","title":"APPLE INC"}}`))
		case strings.Contains(r.URL.Path, "submissions/CIK0000320193.json"):
			w.Write([]byte(`{"name":"Apple Inc.","filings":{"recent":{"accessionNumber":["0000320193-24-000001"],"form":["10-K"],"filingDate":["2024-11-01"],"reportDate":["2024-09-28"],"primaryDocument":["aapl-10k.htm"],"primaryDocDescription":["10-K"]}}}`))
		case strings.Contains(r.URL.Path, "search-index"):
			eftsCalled = true
			w.Write([]byte(`{"hits":{"hits":[]}}`))
		default:
			w.WriteHeader(404)
		}
	})

	res, err := p.Filings(context.Background(), FilingSearchParams{Query: "Apple annual report 10-K", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if eftsCalled {
		t.Error("EFTS full-text search must NOT be called when the company name resolves via nameMap")
	}
	if len(res) == 0 {
		t.Error("expected at least 1 submission result via nameMap resolution")
	}
}
