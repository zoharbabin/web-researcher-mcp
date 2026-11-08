package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// scriptedAcademicProvider returns a fixed result list for every Scholarly call,
// or byQuery[query] when that key exists.
type scriptedAcademicProvider struct {
	name    string
	results []search.AcademicResult
	byQuery map[string][]search.AcademicResult
}

func (p *scriptedAcademicProvider) Name() string { return p.name }
func (p *scriptedAcademicProvider) Metadata() search.ProviderMeta {
	return search.ProviderMeta{Regions: []string{"*"}, RateClass: "free", Description: "scripted academic"}
}
func (p *scriptedAcademicProvider) Scholarly(_ context.Context, params search.AcademicSearchParams) ([]search.AcademicResult, error) {
	if r, ok := p.byQuery[params.Query]; ok {
		return r, nil
	}
	return p.results, nil
}

const attentionRef = "Vaswani et al. 2017, Attention Is All You Need"

// attentionChapter is the #851 false match: same title, other authors, 2025.
func attentionChapter() search.AcademicResult {
	return search.AcademicResult{
		Title: "Attention Is All You Need", URL: "https://doi.org/10.9999/chapter", DOI: "10.9999/chapter",
		Year: 2025, Authors: []string{"Jane Doe", "John Roe"}, Source: "crossref",
	}
}

func attentionPaper() search.AcademicResult {
	return search.AcademicResult{
		Title: "Attention Is All You Need", URL: "https://arxiv.org/abs/1706.03762", DOI: "10.48550/arXiv.1706.03762",
		Year: 2017, Authors: []string{"Ashish Vaswani", "Noam Shazeer", "Niki Parmar"}, Source: "openalex",
	}
}

func depsWithAcademic(providers ...*scriptedAcademicProvider) Dependencies {
	deps := setupTestDeps()
	deps.AcademicProviders = map[string]search.AcademicProvider{}
	for _, p := range providers {
		deps.AcademicProviders[p.name] = p
	}
	deps.Search = nil
	return deps
}

func provenanceHas(out map[string]any, sub string) bool {
	lines, _ := out["provenance"].([]any)
	for _, l := range lines {
		if s, _ := l.(string); strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

const disagreeNote = "year or author in the reference differs from the matched record"

func TestVerifyCitation_ReferenceWrongYearSameTitleNotConfirmed(t *testing.T) {
	ch := attentionChapter()
	ch.Authors = []string{"Ashish Vaswani"} // author agrees, only the year differs
	deps := depsWithAcademic(&scriptedAcademicProvider{name: "openalex", results: []search.AcademicResult{ch}})
	out := callVerify(t, deps, attentionRef)
	if out["exists"] != false || out["verificationStatus"] != verificationUncertain || out["matchConfidence"] != "medium" {
		t.Errorf("want exists=false uncertain medium, got exists=%v status=%v conf=%v", out["exists"], out["verificationStatus"], out["matchConfidence"])
	}
	if _, ok := out["matchedRecord"]; ok {
		t.Error("matchedRecord must not be set")
	}
	if _, ok := out["possibleMatch"]; !ok {
		t.Error("possibleMatch should carry the record")
	}
	if !provenanceHas(out, disagreeNote) {
		t.Errorf("provenance should say %q, got %v", disagreeNote, out["provenance"])
	}
}

func TestVerifyCitation_ReferenceWrongAuthorSameTitleNotConfirmed(t *testing.T) {
	ch := attentionChapter()
	ch.Year = 2017 // year agrees, only the authors differ
	deps := depsWithAcademic(&scriptedAcademicProvider{name: "openalex", results: []search.AcademicResult{ch}})
	out := callVerify(t, deps, attentionRef)
	if out["exists"] != false || out["verificationStatus"] != verificationUncertain || out["matchConfidence"] != "medium" {
		t.Errorf("want exists=false uncertain medium, got exists=%v status=%v conf=%v", out["exists"], out["verificationStatus"], out["matchConfidence"])
	}
	if !provenanceHas(out, disagreeNote) {
		t.Errorf("provenance should say %q, got %v", disagreeNote, out["provenance"])
	}
}

func TestVerifyCitation_ReferenceYearAndAuthorAgreeStaysConfirmed(t *testing.T) {
	deps := depsWithAcademic(&scriptedAcademicProvider{name: "openalex", results: []search.AcademicResult{attentionPaper()}})
	out := callVerify(t, deps, attentionRef)
	if out["exists"] != true || out["verificationStatus"] != verificationConfirmed || out["matchConfidence"] != "high" {
		t.Errorf("want exists=true confirmed high, got exists=%v status=%v conf=%v", out["exists"], out["verificationStatus"], out["matchConfidence"])
	}
	if provenanceHas(out, disagreeNote) {
		t.Error("an agreeing match must not carry the disagreement note")
	}
}

func TestVerifyCitation_ReferenceWithoutYearOrAuthorUnchanged(t *testing.T) {
	// The record is the 2025 chapter by other authors, but the reference names
	// neither a year nor an author, so there is nothing to disagree with.
	deps := depsWithAcademic(&scriptedAcademicProvider{name: "openalex", results: []search.AcademicResult{attentionChapter()}})
	out := callVerify(t, deps, "Attention Is All You Need")
	if out["exists"] != true || out["matchConfidence"] != "high" {
		t.Errorf("want unchanged exists=true high, got exists=%v conf=%v", out["exists"], out["matchConfidence"])
	}
}

func TestVerifyCitation_ReferencePrefersAgreeingRecordFromOtherProvider(t *testing.T) {
	// openalex is the default searcher and its first hit is the 2025 chapter.
	// crossref holds the 2017 paper. The agreeing record must win.
	deps := depsWithAcademic(
		&scriptedAcademicProvider{name: "openalex", results: []search.AcademicResult{attentionChapter()}},
		&scriptedAcademicProvider{name: "crossref", results: []search.AcademicResult{attentionPaper()}},
	)
	out := callVerify(t, deps, attentionRef)
	if out["exists"] != true || out["verificationStatus"] != verificationConfirmed {
		t.Fatalf("want confirmed, got exists=%v status=%v", out["exists"], out["verificationStatus"])
	}
	rec, _ := out["matchedRecord"].(map[string]any)
	if rec["year"] != float64(2017) {
		t.Errorf("matchedRecord.year = %v, want 2017", rec["year"])
	}
}

func TestVerifyCitation_ReferencePrefersAgreeingRecordInSameResultList(t *testing.T) {
	deps := depsWithAcademic(&scriptedAcademicProvider{name: "openalex", results: []search.AcademicResult{attentionChapter(), attentionPaper()}})
	out := callVerify(t, deps, attentionRef)
	rec, _ := out["matchedRecord"].(map[string]any)
	if out["verificationStatus"] != verificationConfirmed || rec["year"] != float64(2017) {
		t.Errorf("want the 2017 record confirmed, got status=%v matched=%v", out["verificationStatus"], out["matchedRecord"])
	}
}

func TestVerifyCitation_ReferenceFindsAgreeingRecordWithBareTitleQuery(t *testing.T) {
	// The full reference only surfaces the 2025 chapter. The bare title also
	// surfaces the 2017 paper.
	p := &scriptedAcademicProvider{
		name:    "openalex",
		results: []search.AcademicResult{attentionChapter()},
		byQuery: map[string][]search.AcademicResult{"Attention Is All You Need": {attentionChapter(), attentionPaper()}},
	}
	out := callVerify(t, depsWithAcademic(p), attentionRef)
	rec, _ := out["matchedRecord"].(map[string]any)
	if out["verificationStatus"] != verificationConfirmed || rec["year"] != float64(2017) {
		t.Errorf("want the 2017 record confirmed, got status=%v matched=%v", out["verificationStatus"], out["matchedRecord"])
	}
}

func TestReferenceTitleQuery(t *testing.T) {
	t.Parallel()
	cases := []struct{ ref, want string }{
		{"Vaswani et al. 2017, Attention Is All You Need", "Attention Is All You Need"},
		{"Vaswani, A., Shazeer, N. (2017). Attention is all you need.", "Attention is all you need."},
		{"Attention Is All You Need", ""},
		{"Smith 2021", ""},
	}
	for _, c := range cases {
		if got := referenceTitleQuery(c.ref); got != c.want {
			t.Errorf("referenceTitleQuery(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
}

func TestReferenceDisagrees(t *testing.T) {
	t.Parallel()
	vaswani := []string{"Ashish Vaswani", "Noam Shazeer"}
	cases := []struct {
		name string
		ref  string
		rec  search.AcademicResult
		want bool
	}{
		{"year differs", "Vaswani 2017, Attention Is All You Need", search.AcademicResult{Title: "Attention Is All You Need", Year: 2025, Authors: vaswani}, true},
		{"year plus one is allowed", "Vaswani 2017, Attention Is All You Need", search.AcademicResult{Title: "Attention Is All You Need", Year: 2018, Authors: vaswani}, false},
		{"year minus one is allowed", "Vaswani 2017, Attention Is All You Need", search.AcademicResult{Title: "Attention Is All You Need", Year: 2016, Authors: vaswani}, false},
		{"year plus two differs", "Vaswani 2017, Attention Is All You Need", search.AcademicResult{Title: "Attention Is All You Need", Year: 2019, Authors: vaswani}, true},
		{"et al other author", "Vaswani et al. 2017", search.AcademicResult{Title: "T", Year: 2017, Authors: []string{"Jane Doe"}}, true},
		{"et al same author", "Vaswani et al. 2017", search.AcademicResult{Title: "T", Year: 2017, Authors: vaswani}, false},
		{"APA surname comma initial", "Vaswani, A., Shazeer, N. (2017). Attention is all you need.", search.AcademicResult{Title: "Attention Is All You Need", Year: 2017, Authors: vaswani}, false},
		{"APA other author", "Smith, J., Jones, K. (2017). Attention is all you need.", search.AcademicResult{Title: "Attention Is All You Need", Year: 2017, Authors: vaswani}, true},
		{"initial then surname", "A. Vaswani, N. Shazeer. Attention is all you need. 2017", search.AcademicResult{Title: "Attention Is All You Need", Year: 2017, Authors: vaswani}, false},
		{"initial then surname other", "J. Smith. Attention is all you need. 2017", search.AcademicResult{Title: "Attention Is All You Need", Year: 2017, Authors: vaswani}, true},
		{"author listed surname-first", "Vaswani et al. 2017", search.AcademicResult{Title: "T", Year: 2017, Authors: []string{"Vaswani, Ashish"}}, false},
		{"diacritics folded", "Müller et al. 2020", search.AcademicResult{Title: "T", Year: 2020, Authors: []string{"Hans Muller"}}, false},
		{"two leading surnames, second matches", "Vaswani and Shazeer 2017", search.AcademicResult{Title: "T", Year: 2017, Authors: []string{"Noam Shazeer"}}, false},
		{"no year, no surname", "Attention Is All You Need", search.AcademicResult{Title: "Attention Is All You Need", Year: 2025, Authors: []string{"Jane Doe"}}, false},
		{"venue is not a surname", "Attention Is All You Need. NeurIPS 2017", search.AcademicResult{Title: "Attention Is All You Need", Year: 2017, Authors: []string{"Jane Doe"}}, false},
		{"record without authors", "Vaswani et al. 2017", search.AcademicResult{Title: "T", Year: 2017}, false},
		{"record without year", "Vaswani et al. 2017", search.AcademicResult{Title: "T", Authors: vaswani}, false},
		{"empty record data", "Vaswani et al. 2017", search.AcademicResult{Title: "T"}, false},
		{"arXiv id is not a year", "Attention Is All You Need arXiv:2001.12345", search.AcademicResult{Title: "Attention Is All You Need", Year: 2017, Authors: vaswani}, false},
		{"DOI digits are not a year", "Attention Is All You Need 10.2017/abc", search.AcademicResult{Title: "Attention Is All You Need", Year: 2024, Authors: vaswani}, false},
		{"year inside the record title is ignored", "World Population Prospects 2022", search.AcademicResult{Title: "World Population Prospects 2022", Year: 2024}, false},
		{"surname inside the record title is ignored", "Fisher et al. 2017", search.AcademicResult{Title: "Fisher exact test", Year: 2017, Authors: []string{"Jane Doe"}}, false},
		{"any matching year is enough", "Vaswani 2017, accessed 2025", search.AcademicResult{Title: "T", Year: 2017, Authors: vaswani}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rec := c.rec
			if got := referenceDisagrees(c.ref, &rec); got != c.want {
				t.Errorf("referenceDisagrees(%q) = %v, want %v", c.ref, got, c.want)
			}
		})
	}
}

func TestReferenceMatchConfidence_CapsOnDisagreementOnly(t *testing.T) {
	t.Parallel()
	title := "Attention Is All You Need"
	agree := &search.AcademicResult{Title: title, Year: 2017, Authors: []string{"Ashish Vaswani"}}
	wrongYear := &search.AcademicResult{Title: title, Year: 2025, Authors: []string{"Ashish Vaswani"}}
	wrongAuthor := &search.AcademicResult{Title: title, Year: 2017, Authors: []string{"Jane Doe"}}
	if got := referenceMatchConfidence(attentionRef, agree); got != "high" {
		t.Errorf("agreeing record = %q, want high", got)
	}
	if got := referenceMatchConfidence(attentionRef, wrongYear); got != "medium" {
		t.Errorf("wrong year = %q, want medium", got)
	}
	if got := referenceMatchConfidence(attentionRef, wrongAuthor); got != "medium" {
		t.Errorf("wrong author = %q, want medium", got)
	}
	if got := referenceMatchConfidence(title, wrongAuthor); got != "high" {
		t.Errorf("title-only reference = %q, want high", got)
	}
}
