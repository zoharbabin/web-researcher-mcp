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
