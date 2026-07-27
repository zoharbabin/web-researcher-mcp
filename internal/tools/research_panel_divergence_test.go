package tools

import "testing"

func TestAnalyzeDivergence_Consensus(t *testing.T) {
	responses := map[string]string{
		"a/x": "The Eiffel Tower is located in Paris, France. It was completed in 1889.",
		"b/y": "The Eiffel Tower is located in Paris, France, and was finished in 1889.",
		"c/z": "Paris, France is home to the Eiffel Tower, completed in the year 1889.",
	}
	d := AnalyzeDivergence(responses)

	if len(d.ConsensusPoints) == 0 {
		t.Fatalf("expected at least one consensus point, got none: %+v", d)
	}
	if len(d.Contradictions) != 0 {
		t.Errorf("expected zero contradictions for restated agreement, got %d: %+v", len(d.Contradictions), d.Contradictions)
	}
	if d.Confidence != "high" {
		t.Errorf("expected high confidence with no contradictions, got %q (%s)", d.Confidence, d.ConfidenceRationale)
	}
}

func TestAnalyzeDivergence_Contradiction(t *testing.T) {
	responses := map[string]string{
		"a/x": "The drug reduces inflammation significantly in patients.",
		"b/y": "The drug does not reduce inflammation significantly in patients.",
	}
	d := AnalyzeDivergence(responses)

	if len(d.Contradictions) == 0 {
		t.Fatalf("expected at least one contradiction between opposing polarity claims, got none: %+v", d)
	}
	if d.Confidence == "high" {
		t.Errorf("expected confidence below high when a contradiction is detected, got %q", d.Confidence)
	}
}

func TestAnalyzeDivergence_UniqueToModel(t *testing.T) {
	responses := map[string]string{
		"a/x": "The mitochondria is the powerhouse of the cell.",
		"b/y": "Photosynthesis converts sunlight into chemical energy in plants.",
	}
	d := AnalyzeDivergence(responses)

	if len(d.UniqueToModel["a/x"]) == 0 {
		t.Errorf("expected model a/x's unrelated claim to be recorded as unique, got %+v", d.UniqueToModel)
	}
	if len(d.UniqueToModel["b/y"]) == 0 {
		t.Errorf("expected model b/y's unrelated claim to be recorded as unique, got %+v", d.UniqueToModel)
	}
	if len(d.Contradictions) != 0 {
		t.Errorf("unrelated claims on different topics must not be reported as contradictions, got %+v", d.Contradictions)
	}
}

func TestAnalyzeDivergence_SingleModel(t *testing.T) {
	responses := map[string]string{
		"a/x": "This is the only response in the panel this round.",
	}
	d := AnalyzeDivergence(responses)

	if d.Confidence != "low" {
		t.Errorf("expected low confidence when fewer than 2 models succeeded, got %q", d.Confidence)
	}
	if len(d.Contradictions) != 0 {
		t.Errorf("a single response cannot contradict itself, got %+v", d.Contradictions)
	}
}

func TestAnalyzeDivergence_EmptyResponses(t *testing.T) {
	d := AnalyzeDivergence(map[string]string{})

	if d.Confidence != "low" {
		t.Errorf("expected low confidence for zero responses, got %q", d.Confidence)
	}
	if len(d.ConsensusPoints) != 0 || len(d.Contradictions) != 0 {
		t.Errorf("expected no consensus/contradictions for zero responses, got %+v", d)
	}
}

func TestSplitPanelSentences(t *testing.T) {
	text := "First sentence is long enough. Second one too!\nThird one on its own line?"
	got := splitPanelSentences(text)
	if len(got) != 3 {
		t.Fatalf("expected 3 sentences, got %d: %v", len(got), got)
	}

	// Fragments shorter than minSentenceLen are dropped as noise.
	short := splitPanelSentences("Hi. Ok.")
	if len(short) != 0 {
		t.Errorf("expected short fragments to be dropped, got %v", short)
	}
}

func TestAnalyzeDivergence_HighOverlapContradictionNotMisreadAsConsensus(t *testing.T) {
	// Near-identical wording differing only by a negation cue: lexical overlap
	// is very high (>= consensusOverlapThreshold), so a contradiction check
	// gated to run only BELOW that threshold would misfile this as agreement.
	responses := map[string]string{
		"a/x": "The treatment reduces mortality significantly in this trial.",
		"b/y": "The treatment does not reduce mortality significantly in this trial.",
	}
	d := AnalyzeDivergence(responses)
	if len(d.Contradictions) == 0 {
		t.Fatalf("expected a contradiction for near-identical, opposite-polarity claims, got none: %+v", d)
	}
	for _, c := range d.ConsensusPoints {
		if c == responses["a/x"] || c == responses["b/y"] {
			t.Errorf("opposite-polarity claim wrongly counted as consensus: %q", c)
		}
	}
}
