package content

import (
	"strings"
	"testing"
)

func TestExtractClaimEvidenceEmpty(t *testing.T) {
	if ev := ExtractClaimEvidence("", "some claim"); ev.Signal != "" || len(ev.KeySentences) > 0 {
		t.Error("empty text should yield no evidence")
	}
	if ev := ExtractClaimEvidence("some text", ""); ev.Signal != "" || len(ev.KeySentences) > 0 {
		t.Error("empty claim should yield no evidence")
	}
}

func TestExtractClaimEvidenceFindsStanceSentence(t *testing.T) {
	text := "The study enrolled 200 patients. The randomized trial found no significant difference between groups (p=0.7). " +
		"Researchers thanked the funders. The weather was nice that week."
	ev := ExtractClaimEvidence(text, "drug efficacy significant difference")
	if ev.Signal == "" {
		t.Fatal("expected a signal sentence")
	}
	if !strings.Contains(ev.Signal, "no significant difference") {
		t.Errorf("signal should be the stance-bearing sentence, got: %q", ev.Signal)
	}
	// The off-topic weather sentence must not be surfaced.
	for _, s := range ev.KeySentences {
		if strings.Contains(s, "weather") {
			t.Errorf("off-topic sentence surfaced: %q", s)
		}
	}
}

func TestExtractClaimEvidenceRequiresClaimTerm(t *testing.T) {
	// Stance markers present but NO claim term → nothing surfaced.
	text := "However, this is completely unrelated. The result was significant for something else entirely."
	ev := ExtractClaimEvidence(text, "quantum teleportation bandwidth")
	if len(ev.KeySentences) > 0 {
		t.Errorf("sentences without claim terms must not be evidence: %v", ev.KeySentences)
	}
}

func TestExtractClaimEvidenceCapsSentences(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("The transformer model improves accuracy on this benchmark significantly. ")
	}
	ev := ExtractClaimEvidence(b.String(), "transformer model accuracy")
	if len(ev.KeySentences) > maxKeySentences {
		t.Errorf("key sentences not capped: %d > %d", len(ev.KeySentences), maxKeySentences)
	}
}

func TestExtractClaimEvidenceDocumentOrder(t *testing.T) {
	text := "Transformer accuracy is high here. Filler sentence one is here. Transformer accuracy was confirmed by the study showing p<0.01."
	ev := ExtractClaimEvidence(text, "transformer accuracy")
	if len(ev.KeySentences) < 2 {
		t.Fatalf("expected at least 2 key sentences, got %d", len(ev.KeySentences))
	}
	// First key sentence should appear earlier in the text than the second.
	if strings.Index(text, ev.KeySentences[0]) > strings.Index(text, ev.KeySentences[1]) {
		t.Error("key sentences should be in document order")
	}
}

func TestSplitSentences(t *testing.T) {
	got := splitSentences("First sentence here. Second one follows! Is a third question here?\nLine break ends one too.")
	if len(got) != 4 {
		t.Errorf("expected 4 sentences, got %d: %v", len(got), got)
	}
	// "U.S." style mid-sentence dots should not over-split.
	g2 := splitSentences("The U.S. economy grew this quarter substantially.")
	if len(g2) != 1 {
		t.Errorf("abbreviation should not split: got %d: %v", len(g2), g2)
	}
}

func TestClaimTermsDropsStopWords(t *testing.T) {
	terms := claimTerms("the drug was not effective for all patients")
	for _, term := range terms {
		if claimStopWords[term] || len(term) < 3 {
			t.Errorf("stop word / short token leaked: %q", term)
		}
	}
	// significant content words survive
	joined := strings.Join(terms, ",")
	for _, want := range []string{"drug", "effective", "patients"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected term %q in %v", want, terms)
		}
	}
}

func TestClaimTermCoverage(t *testing.T) {
	text := "The randomized trial showed the vaccine reduced infection rates significantly."
	// All three significant terms present.
	if m, total := ClaimTermCoverage(text, "vaccine infection rates"); m != 3 || total != 3 {
		t.Errorf("full coverage: matched=%d total=%d, want 3/3", m, total)
	}
	// None present → 0/total.
	if m, total := ClaimTermCoverage(text, "quantum teleportation bandwidth"); m != 0 || total != 3 {
		t.Errorf("zero coverage: matched=%d total=%d, want 0/3", m, total)
	}
	// Partial.
	if m, total := ClaimTermCoverage(text, "vaccine bandwidth latency"); m != 1 || total != 3 {
		t.Errorf("partial coverage: matched=%d total=%d, want 1/3", m, total)
	}
	// Empty text or claim → 0.
	if m, _ := ClaimTermCoverage("", "vaccine"); m != 0 {
		t.Errorf("empty text should be 0 matched")
	}
	// All-stopword claim → total 0 (no judgment possible).
	if _, total := ClaimTermCoverage(text, "the and for"); total != 0 {
		t.Errorf("all-stopword claim should have total 0, got %d", total)
	}
}

func TestClaimTermCoverageWindowed(t *testing.T) {
	// A genuinely-covered claim: all terms co-occur in one passage → full peak.
	covered := "The randomized trial showed the vaccine reduced infection rates significantly. Methods were standard."
	if m, total := ClaimTermCoverageWindowed(covered, "vaccine infection rates", 0); m != 3 || total != 3 {
		t.Errorf("covered: matched=%d total=%d, want 3/3", m, total)
	}

	// The #177 regression case: a narrow off-topic claim against a long, broad
	// document where the claim's terms are SCATTERED across distant sentences but
	// never co-occur in any passage. Whole-doc coverage over-counts (would score
	// partial); windowed coverage must stay low because no local window holds them.
	var sb strings.Builder
	sb.WriteString("CRISPR is a gene editing technology used in molecular biology. ")
	for i := 0; i < 40; i++ {
		sb.WriteString("Researchers applied the technique to edit genomes in various cell lines. ")
	}
	sb.WriteString("The treaty of Westphalia is unrelated filler appearing here once. ")
	for i := 0; i < 40; i++ {
		sb.WriteString("Gene editing has broad applications in medicine and agriculture. ")
	}
	sb.WriteString("The year 1648 is mentioned in a totally different sentence far away. ")
	longDoc := sb.String()

	// "Westphalia treaty 1648": the three terms appear but in three far-apart
	// sentences. Whole-doc would report 3/3; windowed must report < 3 (they never
	// share a window), so the audit can correctly treat it as not/partially covered.
	wMatched, wTotal := ClaimTermCoverageWindowed(longDoc, "Westphalia treaty 1648", 0)
	dMatched, _ := ClaimTermCoverage(longDoc, "Westphalia treaty 1648")
	if wTotal != 3 {
		t.Fatalf("windowed total=%d, want 3", wTotal)
	}
	if dMatched != 3 {
		t.Fatalf("precondition: whole-doc should find all 3 scattered terms, got %d", dMatched)
	}
	if wMatched >= dMatched {
		t.Errorf("windowed coverage (%d) should be LOWER than diluted whole-doc coverage (%d) for scattered terms", wMatched, dMatched)
	}

	// Short document (fewer sentences than the window) degrades to whole-doc.
	short := "Vaccines reduce infection."
	wm, _ := ClaimTermCoverageWindowed(short, "vaccines infection", 0)
	dm, _ := ClaimTermCoverage(short, "vaccines infection")
	if wm != dm {
		t.Errorf("short doc: windowed=%d should equal whole-doc=%d", wm, dm)
	}

	// Empty / all-stopword guards mirror ClaimTermCoverage.
	if m, _ := ClaimTermCoverageWindowed("", "vaccine", 0); m != 0 {
		t.Error("empty text should be 0 matched")
	}
	if _, total := ClaimTermCoverageWindowed(covered, "the and for", 0); total != 0 {
		t.Error("all-stopword claim should have total 0")
	}
}

func TestHasContrastCue(t *testing.T) {
	if !HasContrastCue([]string{"The drug had no significant effect on mortality."}) {
		t.Error("a sentence with 'no significant' should carry a contrast cue")
	}
	if !HasContrastCue([]string{"Plain sentence.", "However, the result did not replicate."}) {
		t.Error("'did not' / 'however' should be detected")
	}
	if HasContrastCue([]string{"The vaccine reduced infection rates substantially."}) {
		t.Error("a plain supporting sentence should NOT carry a contrast cue")
	}
	if HasContrastCue(nil) {
		t.Error("empty evidence should not signal contrast")
	}
}
