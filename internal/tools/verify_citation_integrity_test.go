package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// fakeRetractionResolver is a RetractionResolver stub with a fixed answer.
type fakeRetractionResolver struct {
	status *search.RetractionStatus
	found  bool
	err    error
}

func (f fakeRetractionResolver) Resolve(context.Context, string) (*search.RetractionStatus, bool, error) {
	return f.status, f.found, f.err
}
func (fakeRetractionResolver) Name() string { return "fake" }

// titledRecordProvider resolves 10.1234/x to a record with a configurable title.
type titledRecordProvider struct {
	*mockAcademicProvider
	title string
}

func (p titledRecordProvider) ResolveByDOI(_ context.Context, doi string) (*search.AcademicResult, error) {
	if doi != "10.1234/x" {
		return nil, nil
	}
	return &search.AcademicResult{Title: p.title, URL: "https://doi.org/10.1234/x", DOI: "10.1234/x", Year: 2024, Source: "openalex"}, nil
}

func depsWithRecordTitle(title string) Dependencies {
	deps := setupTestDeps()
	p := titledRecordProvider{mockAcademicProvider: &mockAcademicProvider{}, title: title}
	deps.AcademicProviders = map[string]search.AcademicProvider{p.Name(): p}
	return deps
}

// TestVerifyCitation_RetractionCheck: retractionCheck must separate "looked and
// clean" from "could not look" (unchecked must never read as clear).
func TestVerifyCitation_RetractionCheck(t *testing.T) {
	t.Parallel()
	retracted := &search.RetractionStatus{Retracted: true, Kind: "retraction"}
	concern := &search.RetractionStatus{Retracted: false, Kind: "expression_of_concern"}
	cases := []struct {
		name       string
		citation   string
		resolver   search.RetractionResolver
		wantCheck  string
		wantReason string
		wantStatus bool
	}{
		{"clean doi", "10.1234/x", fakeRetractionResolver{found: true}, "clear", "", false},
		{"retracted doi", "10.1234/x", fakeRetractionResolver{found: true, status: retracted}, "retracted", "", true},
		{"concern is flagged not clear", "10.1234/x", fakeRetractionResolver{found: true, status: concern}, "flagged", "", true},
		{"lookup error", "10.1234/x", fakeRetractionResolver{err: errors.New("boom")}, "unchecked", "lookup_error", false},
		{"not in crossref", "10.1234/x", fakeRetractionResolver{found: false}, "unchecked", "not_in_crossref", false},
		{"no resolver", "10.1234/x", nil, "unchecked", "resolver_unavailable", false},
		{"free text, matched record has DOI, clean", "Mock Paper, 2024", fakeRetractionResolver{found: true}, "clear", "", false},
		{"free text, no resolver", "Mock Paper, 2024", nil, "unchecked", "resolver_unavailable", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			deps := setupTestDeps()
			if c.resolver != nil {
				deps.RetractionResolver = c.resolver
			}
			out := callVerify(t, deps, c.citation)
			if got := out["retractionCheck"]; got != c.wantCheck {
				t.Errorf("retractionCheck = %v, want %q (out=%v)", got, c.wantCheck, out)
			}
			reason, hasReason := out["retractionCheckReason"]
			if c.wantReason == "" && hasReason {
				t.Errorf("retractionCheckReason = %v, want absent", reason)
			}
			if c.wantReason != "" && reason != c.wantReason {
				t.Errorf("retractionCheckReason = %v, want %q", reason, c.wantReason)
			}
			if _, has := out["retractionStatus"]; has != c.wantStatus {
				t.Errorf("retractionStatus present = %v, want %v", has, c.wantStatus)
			}
		})
	}
}

// TestVerifyCitation_URLRetractionCheckNotADOI: a plain URL has no DOI to check.
func TestVerifyCitation_URLRetractionCheckNotADOI(t *testing.T) {
	t.Parallel()
	out := callVerify(t, verifyClaimDeps(t), "https://example.invalid/not-a-paper")
	if out["retractionCheck"] != "unchecked" || out["retractionCheckReason"] != "not_a_doi" {
		t.Errorf("got retractionCheck=%v reason=%v, want unchecked/not_a_doi", out["retractionCheck"], out["retractionCheckReason"])
	}
}

// TestVerifyCitation_TitleMismatchDowngrades: a DOI that exists but is cited under
// a different title is evidence of a misattributed citation, not a confirmation.
func TestVerifyCitation_TitleMismatchDowngrades(t *testing.T) {
	t.Parallel()
	const realTitle = "Highly accurate protein structure prediction with AlphaFold"
	cases := []struct {
		name       string
		citation   string
		wantMatch  string
		wantStatus string
		wantReason string
	}{
		{"wrong title", "10.1234/x Quantum entanglement teleportation bandwidth", "mismatch", "uncertain", "title_mismatch"},
		{"exact title", "10.1234/x Highly accurate protein structure prediction with AlphaFold", "match", "confirmed", ""},
		{"lowercase, no punctuation", "10.1234/x highly accurate protein structure prediction with alphafold", "match", "confirmed", ""},
		{"missing subtitle", "10.1234/x Highly accurate protein structure prediction", "match", "confirmed", ""},
		{"with authors and journal", "10.1234/x Jumper J, et al. Highly accurate protein structure prediction with AlphaFold. Nature. 2021", "match", "confirmed", ""},
		{"bare doi", "10.1234/x", "not_checked", "confirmed", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out := callVerify(t, depsWithRecordTitle(realTitle), c.citation)
			if out["titleMatch"] != c.wantMatch {
				t.Errorf("titleMatch = %v, want %v", out["titleMatch"], c.wantMatch)
			}
			if out["verificationStatus"] != c.wantStatus {
				t.Errorf("verificationStatus = %v, want %v", out["verificationStatus"], c.wantStatus)
			}
			if c.wantReason == "" {
				if _, has := out["verificationReason"]; has {
					t.Errorf("verificationReason = %v, want absent", out["verificationReason"])
				}
			} else if out["verificationReason"] != c.wantReason {
				t.Errorf("verificationReason = %v, want %v", out["verificationReason"], c.wantReason)
			}
			// The DOI itself resolves and the record is still shown: only the
			// headline state is downgraded.
			if out["exists"] != true {
				t.Errorf("exists = %v, want true", out["exists"])
			}
			if _, ok := out["matchedRecord"]; !ok {
				t.Error("matchedRecord must stay attached")
			}
		})
	}
}

// TestComputeTitleMatch_FalsePositiveGuards: real titles that differ only in
// case, punctuation, diacritics, subtitle or trailing context must never be
// called a mismatch.
func TestComputeTitleMatch_FalsePositiveGuards(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		supplied string
		record   string
		want     string
	}{
		{"case", "DEEP RESIDUAL LEARNING FOR IMAGE RECOGNITION", "Deep Residual Learning for Image Recognition", "match"},
		{"punctuation", "COVID-19: a systematic review, of outcomes", "COVID-19 - A Systematic Review of Outcomes", "match"},
		{"colon subtitle dropped", "Attention mechanisms", "Attention mechanisms: a survey of neural architectures", "match"},
		{"subtitle added by caller", "Graph neural networks: a review of methods and applications", "Graph neural networks", "match"},
		{"diacritics", "Uber die Elektrodynamik bewegter Korper", "Über die Elektrodynamik bewegter Körper", "match"},
		{"accents in record only", "Recherche sur les principes mathematiques de la theorie des richesses", "Recherches sur les principes mathématiques de la théorie des richesses", "match"},
		{"html entity remnants", "Cats &amp; dogs: behaviour and cognition", "Cats & Dogs: Behaviour and Cognition", "match"},
		{"short exact title", "Garbage", "Garbage", "match"},
		{"empty", "", "Anything at all", "not_checked"},
		{"invented title", "Quantum entanglement teleportation bandwidth", "Highly accurate protein structure prediction with AlphaFold", "mismatch"},
		{"translated title with no shared words reads as mismatch", "Apprentissage résiduel profond pour la reconnaissance visuelle", "Deep Residual Learning for Image Recognition", "mismatch"},
		{"translated title sharing cognates still matches", "Reconocimiento residual profundo de imagenes", "Deep Residual Learning for Image Recognition", "match"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := computeTitleMatch(c.supplied, &search.AcademicResult{Title: c.record}); got != c.want {
				t.Errorf("computeTitleMatch(%q, %q) = %q, want %q", c.supplied, c.record, got, c.want)
			}
		})
	}
}
