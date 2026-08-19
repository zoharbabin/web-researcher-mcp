package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

func newMonarchTestProvider(t *testing.T, handler http.HandlerFunc) *MonarchAPIProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewMonarchProvider(Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 5, ResetTimeout: 60}),
	})
	p.SetBaseURL(srv.URL)
	return p
}

func TestMonarchKeyless(t *testing.T) {
	if p := NewMonarchProviderByName("monarch", Deps{}); p == nil {
		t.Error("monarch should construct without any key")
	}
	if p := NewMonarchProviderByName("unknown", Deps{}); p != nil {
		t.Error("unknown monarch provider should be nil")
	}
}

func TestMonarchValidCURIE(t *testing.T) {
	cases := map[string]bool{
		"MONDO:0007947":   true,
		"HGNC:3603":       true,
		"HP:0001166":      true,
		"../etc/passwd":   false,
		"no-colon":        false,
		"":                false,
		"MONDO:../../etc": false,
		"a b:c":           false,
	}
	for id, want := range cases {
		if got := ValidCURIE(id); got != want {
			t.Errorf("ValidCURIE(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestMonarchValidSemsimGroup(t *testing.T) {
	valid := []string{"Human Genes", "Mouse Genes", "Rat Genes", "Zebrafish Genes", "C. Elegans Genes", "Human Diseases"}
	for _, g := range valid {
		if !ValidSemsimGroup(g) {
			t.Errorf("ValidSemsimGroup(%q) should be true", g)
		}
	}
	invalid := []string{"Worm Genes", "worm genes", "", "human diseases"}
	for _, g := range invalid {
		if ValidSemsimGroup(g) {
			t.Errorf("ValidSemsimGroup(%q) should be false", g)
		}
	}
}

func TestMonarchSemsimSearch(t *testing.T) {
	p := newMonarchTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/api/semsim/search") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`[{"subject":{"id":"MONDO:0007947","name":"Marfan syndrome","category":"biolink:Disease"},"score":0.85,` +
			`"similarity":{"subject_best_matches":{"HP:0001166":{"similarity":{"ancestor_id":"HP:0001166","ancestor_label":"Arachnodactyly"}}}}}]`))
	})
	res, err := p.Search(context.Background(), MonarchSearchParams{Operation: "semsim", Phenotypes: []string{"HP:0001166", "HP:0001083"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 result, got %d", len(res))
	}
	r0 := res[0]
	if r0.ID != "MONDO:0007947" || r0.Label != "Marfan syndrome" || r0.Score != 0.85 {
		t.Errorf("subject mapping wrong: %+v", r0)
	}
	if r0.AncestorID != "HP:0001166" || r0.AncestorLabel != "Arachnodactyly" {
		t.Errorf("ancestor mapping wrong: %+v", r0)
	}
	if r0.Source != "monarch" {
		t.Errorf("source = %q, want monarch", r0.Source)
	}
}

func TestMonarchSemsimDefaultsToHumanDiseases(t *testing.T) {
	var body string
	p := newMonarchTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 2048)
		n, _ := r.Body.Read(buf)
		body = string(buf[:n])
		w.Write([]byte(`[]`))
	})
	_, err := p.Search(context.Background(), MonarchSearchParams{Operation: "semsim", Phenotypes: []string{"HP:0001166"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(body, `"Human Diseases"`) {
		t.Errorf("expected default group Human Diseases in request body, got %s", body)
	}
}

func TestMonarchEntityByID(t *testing.T) {
	p := newMonarchTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/entity/MONDO:0007947" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{"id":"MONDO:0007947","name":"Marfan syndrome","category":"biolink:Disease","description":"A connective tissue disorder.","xref":["OMIM:154700"]}`))
	})
	res, err := p.Search(context.Background(), MonarchSearchParams{Operation: "entity", EntityID: "MONDO:0007947"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 result, got %d", len(res))
	}
	r0 := res[0]
	if r0.ID != "MONDO:0007947" || r0.Description != "A connective tissue disorder." {
		t.Errorf("entity mapping wrong: %+v", r0)
	}
	if len(r0.CrossReferences) != 1 || r0.CrossReferences[0] != "OMIM:154700" {
		t.Errorf("xref mapping wrong: %+v", r0.CrossReferences)
	}
}

func TestMonarchEntityByIDRejectsPathTraversal(t *testing.T) {
	p := newMonarchTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("must not reach the network with an invalid CURIE")
	})
	_, err := p.Search(context.Background(), MonarchSearchParams{Operation: "entity", EntityID: "../../etc/passwd"})
	if err == nil {
		t.Fatal("expected error for path-traversal entityId")
	}
}

func TestMonarchEntitySearch(t *testing.T) {
	p := newMonarchTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/api/search") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("q") != "Marfan syndrome" {
			t.Errorf("query not passed: %q", r.URL.Query().Get("q"))
		}
		w.Write([]byte(`{"total":1,"items":[{"id":"MONDO:0007947","name":"Marfan syndrome","category":"biolink:Disease"}]}`))
	})
	res, err := p.Search(context.Background(), MonarchSearchParams{Operation: "entity", Query: "Marfan syndrome"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 || res[0].ID != "MONDO:0007947" {
		t.Errorf("search mapping wrong: %+v", res)
	}
}

func TestMonarchAssociations(t *testing.T) {
	p := newMonarchTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/api/association") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("entity") != "MONDO:0007947" {
			t.Errorf("entity not passed: %q", q.Get("entity"))
		}
		if q.Get("category") != "biolink:CausalGeneToDiseaseAssociation" {
			t.Errorf("category not passed: %q", q.Get("category"))
		}
		w.Write([]byte(`{"total":1,"items":[{"subject":"HGNC:3603","subject_label":"FBN1","object":"MONDO:0007947","object_label":"Marfan syndrome","category":"biolink:CausalGeneToDiseaseAssociation","primary_knowledge_source":"infores:omim"}]}`))
	})
	res, err := p.Search(context.Background(), MonarchSearchParams{Operation: "associations", EntityID: "MONDO:0007947", AssocCategory: "biolink:CausalGeneToDiseaseAssociation"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 result, got %d", len(res))
	}
	r0 := res[0]
	if r0.SubjectID != "HGNC:3603" || r0.ObjectID != "MONDO:0007947" || r0.PrimaryKnowledgeSource != "infores:omim" {
		t.Errorf("association mapping wrong: %+v", r0)
	}
}

func TestMonarchAssociationsRejectsQueryInjection(t *testing.T) {
	p := newMonarchTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("must not reach the network with an unsafe association filter value")
	})
	cases := []MonarchSearchParams{
		{Operation: "associations", AssocSubject: "HGNC:3603&extra=1"},
		{Operation: "associations", AssocObject: "MONDO:0007947#frag"},
		{Operation: "associations", AssocCategory: "biolink:X?y=1"},
		{Operation: "associations", EntityID: "MONDO:0007947/../secret"},
	}
	for _, params := range cases {
		if _, err := p.Search(context.Background(), params); err == nil {
			t.Errorf("expected error for unsafe association params: %+v", params)
		}
	}
}

func TestMonarchCompare(t *testing.T) {
	p := newMonarchTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/api/semsim/compare") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{"best_score":0.72,"average_score":0.5,"subject_best_matches":{"HP:0001166":{"similarity":{"ancestor_id":"HP:0001166","ancestor_label":"Arachnodactyly"}}}}`))
	})
	res, err := p.Search(context.Background(), MonarchSearchParams{Operation: "compare", Phenotypes: []string{"HP:0001166"}, CompareTo: []string{"HP:0001083"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 result, got %d", len(res))
	}
	if res[0].Score != 0.72 || res[0].AncestorID != "HP:0001166" {
		t.Errorf("compare mapping wrong: %+v", res[0])
	}
}

func TestMonarchAnnotate(t *testing.T) {
	p := newMonarchTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/api/annotate") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("text") != "patient has long fingers" {
			t.Errorf("text not passed: %q", r.URL.Query().Get("text"))
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`patient has <span class="sciCrunchAnnotation" data-sciGraph="Arachnodactyly,HP:0001166,Phenotype,">long fingers</span>`))
	})
	res, err := p.Search(context.Background(), MonarchSearchParams{Operation: "annotate", Text: "patient has long fingers"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 grounded term, got %d", len(res))
	}
	if res[0].ID != "HP:0001166" || res[0].Label != "Arachnodactyly" || res[0].Text != "long fingers" {
		t.Errorf("annotate mapping wrong: %+v", res[0])
	}
}

// TestMonarchAnnotateFiltersNonHPOAndStopwords is a regression test for #598:
// annotate() must ground only HP:-namespace terms (per its documented "ground
// to HPO terms" contract) and must not report generic clinical-narrative
// stopwords (e.g. "Patient") as grounded terms even if SciGraph's NER matches
// them to some unrelated specific entity.
func TestMonarchAnnotateFiltersNonHPOAndStopwords(t *testing.T) {
	p := newMonarchTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(strings.Join([]string{
			`<span class="sciCrunchAnnotation" data-sciGraph="Some Case Report,LITERATURE:12345,CaseReport,">Patient</span>`,
			`presented with`,
			`<span class="sciCrunchAnnotation" data-sciGraph="Arachnodactyly,HP:0001166,Phenotype,">long fingers</span>`,
			`and`,
			`<span class="sciCrunchAnnotation" data-sciGraph="Abnormal mouse phenotype,MP:0001166,Phenotype,">abnormal gait</span>`,
		}, " ")))
	})
	res, err := p.Search(context.Background(), MonarchSearchParams{Operation: "annotate", Text: "Patient presented with long fingers and abnormal gait"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want exactly 1 grounded HP: term (non-HPO and stopword spans must be dropped), got %d: %+v", len(res), res)
	}
	if res[0].ID != "HP:0001166" || res[0].Label != "Arachnodactyly" || res[0].Text != "long fingers" {
		t.Errorf("annotate mapping wrong: %+v", res[0])
	}
}

func TestMonarchEntity404IsEmpty(t *testing.T) {
	p := newMonarchTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	})
	res, err := p.Search(context.Background(), MonarchSearchParams{Operation: "entity", EntityID: "MONDO:9999999"})
	if err != nil {
		t.Errorf("404 should map to empty, not error: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("404 should be empty: %+v", res)
	}
}

func TestMonarch503Errors(t *testing.T) {
	p := newMonarchTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		w.Write([]byte("service unavailable"))
	})
	_, err := p.Search(context.Background(), MonarchSearchParams{Operation: "entity", EntityID: "MONDO:0007947"})
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("503 should surface as an error, got %v", err)
	}
}

func TestMonarchMalformedJSONErrors(t *testing.T) {
	p := newMonarchTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{not valid json`))
	})
	_, err := p.Search(context.Background(), MonarchSearchParams{Operation: "entity", EntityID: "MONDO:0007947"})
	if err == nil {
		t.Error("malformed JSON should error")
	}
}

func TestMonarchUnknownOperationErrors(t *testing.T) {
	p := newMonarchTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("must not reach the network for an unknown operation")
	})
	_, err := p.Search(context.Background(), MonarchSearchParams{Operation: "bogus"})
	if err == nil {
		t.Error("unknown operation should error")
	}
}

func TestMonarchCircuitBreakerOpensAfterFailures(t *testing.T) {
	t.Parallel()
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	p := NewMonarchProvider(Deps{
		HTTPClient: srv.Client(),
		Breaker:    circuit.New(circuit.Config{FailureThreshold: 2, ResetTimeout: 60}),
	})
	p.SetBaseURL(srv.URL)

	for i := 0; i < 2; i++ {
		if _, err := p.Search(context.Background(), MonarchSearchParams{Operation: "entity", EntityID: "MONDO:0007947"}); err == nil {
			t.Fatalf("call %d: expected error", i)
		}
	}
	hitsAtTrip := hits
	if _, err := p.Search(context.Background(), MonarchSearchParams{Operation: "entity", EntityID: "MONDO:0007947"}); err == nil {
		t.Fatal("expected breaker-open error")
	}
	if hits != hitsAtTrip {
		t.Errorf("breaker did not short-circuit: hits went %d → %d", hitsAtTrip, hits)
	}
}

func TestMonarchInterface(t *testing.T) {
	var _ MonarchProvider = (*MonarchAPIProvider)(nil)
}
