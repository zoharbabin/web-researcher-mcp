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
