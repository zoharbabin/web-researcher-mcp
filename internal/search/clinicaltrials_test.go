package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newClinicalTestProvider(t *testing.T, handler http.HandlerFunc) *ClinicalTrialsProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewClinicalTrialsProvider(Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	p.SetBaseURL(srv.URL)
	return p
}

func TestClinicalTrialsKeyless(t *testing.T) {
	if p := NewTrialProviderByName("clinicaltrials", Deps{}); p == nil {
		t.Error("clinicaltrials should construct without any key")
	}
	if p := NewTrialProviderByName("unknown", Deps{}); p != nil {
		t.Error("unknown trial provider should be nil")
	}
}

func TestClinicalTrialsSearch(t *testing.T) {
	p := newClinicalTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/studies") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("query.cond") != "covid-19" {
			t.Errorf("condition not passed: %q", q.Get("query.cond"))
		}
		if q.Get("format") != "json" {
			t.Error("format=json must be sent")
		}
		if q.Get("fields") == "" {
			t.Error("fields projection must be sent")
		}
		if q.Get("filter.overallStatus") != "RECRUITING" {
			t.Errorf("status should be upper-cased to RECRUITING, got %q", q.Get("filter.overallStatus"))
		}
		w.Write([]byte(`{"studies":[{"protocolSection":{` +
			`"identificationModule":{"nctId":"NCT05047692","briefTitle":"A COVID-19 Vaccine Study"},` +
			`"statusModule":{"overallStatus":"RECRUITING","startDateStruct":{"date":"2021-09-09"}},` +
			`"designModule":{"phases":["PHASE1"]},` +
			`"conditionsModule":{"conditions":["Covid19"]},` +
			`"armsInterventionsModule":{"interventions":[{"name":"AdCLD-CoV19-1"},{"name":""}]},` +
			`"sponsorCollaboratorsModule":{"leadSponsor":{"name":"Cellid Co., Ltd."}}` +
			`},"hasResults":false}],"totalCount":1978}`))
	})
	res, err := p.Trials(context.Background(), TrialSearchParams{Condition: "covid-19", Status: "recruiting", NumResults: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 trial, got %d", len(res))
	}
	tr := res[0]
	if tr.NCTID != "NCT05047692" || tr.Title != "A COVID-19 Vaccine Study" {
		t.Errorf("identification mapping wrong: %+v", tr)
	}
	if tr.Status != "RECRUITING" || tr.StartDate != "2021-09-09" {
		t.Errorf("status/date mapping wrong: %+v", tr)
	}
	if len(tr.Phases) != 1 || tr.Phases[0] != "PHASE1" {
		t.Errorf("phases mapping wrong: %+v", tr.Phases)
	}
	if len(tr.Conditions) != 1 || tr.Conditions[0] != "Covid19" {
		t.Errorf("conditions mapping wrong: %+v", tr.Conditions)
	}
	// The empty-name intervention must be dropped.
	if len(tr.Interventions) != 1 || tr.Interventions[0] != "AdCLD-CoV19-1" {
		t.Errorf("interventions mapping wrong (empty name should drop): %+v", tr.Interventions)
	}
	if tr.Sponsor != "Cellid Co., Ltd." {
		t.Errorf("sponsor mapping wrong: %+v", tr.Sponsor)
	}
	if tr.HasResults {
		t.Error("hasResults should be false")
	}
	if tr.URL != "https://clinicaltrials.gov/study/NCT05047692" {
		t.Errorf("url should be built from NCT id: %s", tr.URL)
	}
	if tr.Source != "clinicaltrials" {
		t.Errorf("source should be clinicaltrials: %s", tr.Source)
	}
}

func TestClinicalTrialsNoMatchEmpty(t *testing.T) {
	p := newClinicalTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"studies":[],"totalCount":0}`))
	})
	res, err := p.Trials(context.Background(), TrialSearchParams{Query: "zzzznomatch"})
	if err != nil {
		t.Fatalf("no-match should not error: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("no-match should be empty, got %+v", res)
	}
}

func TestClinicalTrials404IsEmpty(t *testing.T) {
	p := newClinicalTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`NCT number not found`))
	})
	res, err := p.Trials(context.Background(), TrialSearchParams{Query: "x"})
	if err != nil {
		t.Errorf("404 should map to empty, not error: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("404 should be empty: %+v", res)
	}
}

func TestClinicalTrialsBadRequestErrors(t *testing.T) {
	// The API returns text/plain errors (NOT JSON) on a 400.
	p := newClinicalTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(400)
		w.Write([]byte("Parameter `pageSize` cannot be converted to 32-bit integer"))
	})
	_, err := p.Trials(context.Background(), TrialSearchParams{Query: "x"})
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Errorf("400 should surface as an error, got %v", err)
	}
}

func TestClinicalTrialsInterface(t *testing.T) {
	var _ TrialProvider = (*ClinicalTrialsProvider)(nil)
}

// TestClinicalTrialsExplicitPhaseSetsAggFilter is the regression test for
// #437 fix 1: a structured Phase param must translate to the verified
// aggFilters=phase:N query param (there is no filter.phase).
func TestClinicalTrialsExplicitPhaseSetsAggFilter(t *testing.T) {
	p := newClinicalTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("aggFilters") != "phase:3" {
			t.Errorf("aggFilters = %q, want phase:3", q.Get("aggFilters"))
		}
		if q.Get("query.term") != "diabetes" {
			t.Errorf("query.term should be unmodified when Phase is set explicitly, got %q", q.Get("query.term"))
		}
		w.Write([]byte(`{"studies":[],"totalCount":0}`))
	})
	_, err := p.Trials(context.Background(), TrialSearchParams{Query: "diabetes", Phase: "Phase 3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestClinicalTrialsPhaseInferredFromQuery is the regression test for #437
// fix 2: a phase phrase embedded in free text (the shape an LLM caller that
// hasn't split query into structured fields would produce) is extracted into
// the aggFilters param and stripped from query.term.
func TestClinicalTrialsPhaseInferredFromQuery(t *testing.T) {
	p := newClinicalTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("aggFilters") != "phase:3" {
			t.Errorf("aggFilters = %q, want phase:3 inferred from query", q.Get("aggFilters"))
		}
		if q.Get("query.term") != "type 2 diabetes trial" {
			t.Errorf("query.term = %q, want the phase phrase stripped", q.Get("query.term"))
		}
		w.Write([]byte(`{"studies":[],"totalCount":0}`))
	})
	_, err := p.Trials(context.Background(), TrialSearchParams{Query: "type 2 diabetes phase 3 trial"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestClinicalTrialsExplicitPhaseWinsOverInference proves #434 Rule 2's
// explicit-wins-over-inference precedent holds for clinical_search too.
func TestClinicalTrialsExplicitPhaseWinsOverInference(t *testing.T) {
	p := newClinicalTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("aggFilters") != "phase:1" {
			t.Errorf("aggFilters = %q, want phase:1 (explicit Phase, not the phase 3 in query)", q.Get("aggFilters"))
		}
		if q.Get("query.term") != "diabetes phase 3 trial" {
			t.Errorf("query.term should be unmodified when Phase is set explicitly, got %q", q.Get("query.term"))
		}
		w.Write([]byte(`{"studies":[],"totalCount":0}`))
	})
	_, err := p.Trials(context.Background(), TrialSearchParams{Query: "diabetes phase 3 trial", Phase: "PHASE1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNormalizePhase(t *testing.T) {
	cases := map[string]string{
		"":              "",
		"PHASE3":        "3",
		"Phase 3":       "3",
		"phase_3":       "3",
		"3":             "3",
		"EARLY_PHASE1":  "0",
		"early phase 1": "0",
		"PHASE5":        "", // out of range
		"not a phase":   "",
	}
	for raw, want := range cases {
		if got := normalizePhase(raw); got != want {
			t.Errorf("normalizePhase(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestInferPhaseFromQuery(t *testing.T) {
	cases := []struct {
		query, wantCode, wantRemaining string
	}{
		{"type 2 diabetes phase 3 trial", "3", "type 2 diabetes trial"},
		{"early phase 1 oncology study", "0", "oncology study"},
		{"phase2 lung cancer", "2", "lung cancer"},
		{"remdesivir covid-19", "", "remdesivir covid-19"},
		// "phase 0" and "early phase 2" are not real ClinicalTrials.gov phases
		// (normalizePhase has no mapping for them) — the phrase must be left in
		// place rather than stripped for a filter that never gets applied.
		{"phase 0 dose escalation study", "", "phase 0 dose escalation study"},
		{"early phase 2 oncology study", "", "early phase 2 oncology study"},
	}
	for _, c := range cases {
		gotCode, gotRemaining := inferPhaseFromQuery(c.query)
		if gotCode != c.wantCode || gotRemaining != c.wantRemaining {
			t.Errorf("inferPhaseFromQuery(%q) = (%q, %q), want (%q, %q)", c.query, gotCode, gotRemaining, c.wantCode, c.wantRemaining)
		}
	}
}
