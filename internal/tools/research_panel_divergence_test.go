package tools

import (
	"strings"
	"testing"
)

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

// TestAnalyzeDivergence_DegenerateResponseNotCountedAsContributing is the
// #667 regression guard: a panelist whose response is empty or shorter than
// minSentenceLen (e.g. a filtered/degenerate goai.GenerateText completion,
// err==nil but no usable text) contributes no claims and must not be counted
// toward the "N models compared" figure that panelConfidence uses — that
// figure must reflect models with a comparable claim, not len(responses).
func TestAnalyzeDivergence_DegenerateResponseNotCountedAsContributing(t *testing.T) {
	responses := map[string]string{
		"a/x": "This is a substantive claim long enough to be a candidate sentence.",
		"b/y": "", // degenerate completion: no usable claim sentence
	}
	d := AnalyzeDivergence(responses)

	if d.Confidence != "low" {
		t.Errorf("only 1 model produced a comparable claim; expected low confidence, got %q (rationale: %q)", d.Confidence, d.ConfidenceRationale)
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

func TestSplitPanelSentences_DropsMarkdownHeadings(t *testing.T) {
	text := "# Intermittent Fasting and Human Lifespan\nIntermittent fasting has not been proven to extend lifespan in humans.\n## Evidence\nMost evidence comes from animal studies, not human trials."
	got := splitPanelSentences(text)
	for _, s := range got {
		if isMarkdownHeading(s) {
			t.Errorf("markdown heading leaked into candidate sentences: %q", s)
		}
	}
	if len(got) != 2 {
		t.Fatalf("expected the 2 non-heading sentences only, got %d: %v", len(got), got)
	}
}

// TestSplitPanelSentences_DropsLowSignificantTermFragments reproduces the
// exact E2E-observed leak: a short transition phrase ("Specifically:") and a
// bold-emphasis pseudo-heading ("**The Update**") both clear minSentenceLen's
// raw character count but carry only one real content word each, so they
// must still be dropped as candidate sentences.
func TestSplitPanelSentences_DropsLowSignificantTermFragments(t *testing.T) {
	text := "Specifically:\n**The Update**\nThe central bank raised interest rates by half a percentage point."
	got := splitPanelSentences(text)
	for _, s := range got {
		if s == "Specifically:" || s == "**The Update**" {
			t.Errorf("low-content fragment leaked into candidate sentences: %q", s)
		}
	}
	if len(got) != 1 {
		t.Fatalf("expected only the one substantive sentence, got %d: %v", len(got), got)
	}
}

// TestAnalyzeDivergence_TransitionPhraseNotMisreadAsConsensus is the direct
// consensus_points-level regression for the same bug: two models both echo
// "Specifically:" and "**The Update**" near-verbatim (boilerplate), which
// trivially maxes out lexical overlap and used to win the consensus
// threshold ahead of the actual, differently-phrased substantive claim.
func TestAnalyzeDivergence_TransitionPhraseNotMisreadAsConsensus(t *testing.T) {
	responses := map[string]string{
		"a/x": "Specifically:\n**The Update**\nThe central bank raised interest rates by half a percentage point this quarter.",
		"b/y": "Specifically:\n**The Update**\nPolicymakers increased the benchmark interest rate by fifty basis points this quarter.",
	}
	d := AnalyzeDivergence(responses)

	for _, c := range d.ConsensusPoints {
		if c == "Specifically:" || c == "**The Update**" {
			t.Errorf("boilerplate transition phrase wrongly reported as a consensus point: %q", c)
		}
	}
}

func TestIsMarkdownHeading(t *testing.T) {
	cases := map[string]bool{
		"# Title":                     true,
		"## Subtitle":                 true,
		"###### Deep heading":         true,
		"####### Too many hashes":     false,
		"#no-space-tag":               false,
		"":                            false,
		"Regular sentence about #ai.": false,
	}
	for in, want := range cases {
		if got := isMarkdownHeading(in); got != want {
			t.Errorf("isMarkdownHeading(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestAnalyzeDivergence_HeadingNotMisreadAsConsensus is the #632 regression:
// two models answering the same question both echo it as an identical
// markdown heading (near-universal LLM behavior) while phrasing their actual
// shared substantive claim differently. Before the fix, the heading's
// near-1.0 lexical overlap won the consensus threshold and the real claim
// (paraphrased, lower overlap) did not.
func TestAnalyzeDivergence_HeadingNotMisreadAsConsensus(t *testing.T) {
	responses := map[string]string{
		"a/x": "# Intermittent Fasting and Human Lifespan\nIntermittent fasting has not been proven to extend lifespan in humans, and the supporting evidence is largely limited to animal studies.",
		"b/y": "# Intermittent Fasting and Human Lifespan\nThere is no proof that intermittent fasting extends human lifespan; most of the supporting evidence comes from studies in animals.",
	}
	d := AnalyzeDivergence(responses)

	for _, c := range d.ConsensusPoints {
		if isMarkdownHeading(c) {
			t.Errorf("markdown heading wrongly reported as a consensus point: %q", c)
		}
	}
	if len(d.ConsensusPoints) == 0 {
		t.Fatalf("expected the shared substantive claim to be reported as consensus, got none: %+v", d)
	}
}

// TestAnalyzeDivergence_NoConsensusNoContradiction_RationaleHonest is the #677
// regression: two models substantively agree (hybrid work stabilizing) but
// phrase it differently enough that no sentence pair crosses the strict 0.8
// consensus-overlap threshold, and neither uses a negation cue, so no
// contradiction fires either. Before the fix, panelConfidence's
// "contradictionCount == 0" branch fired regardless of consensusCount,
// producing confidence:"high" and agreement language ("agreed on core
// claims") that consensus_points:[] didn't back up.
func TestAnalyzeDivergence_NoConsensusNoContradiction_RationaleHonest(t *testing.T) {
	responses := map[string]string{
		"a/x": "Hybrid work arrangements are likely to stabilize as the dominant model for most large employers over the next few years.",
		"b/y": "Most big companies will probably settle into a steady hybrid pattern rather than shifting fully remote or fully back to offices.",
	}
	d := AnalyzeDivergence(responses)

	if len(d.ConsensusPoints) != 0 {
		t.Fatalf("expected no consensus points for paraphrased agreement below the overlap threshold, got %+v", d.ConsensusPoints)
	}
	if len(d.Contradictions) != 0 {
		t.Fatalf("expected no contradictions, got %+v", d.Contradictions)
	}
	if d.Confidence != "medium" {
		t.Errorf("expected medium confidence when neither consensus nor contradiction fires, got %q (%s)", d.Confidence, d.ConfidenceRationale)
	}
	if strings.Contains(d.ConfidenceRationale, "agreed") {
		t.Errorf("confidence_rationale must not claim agreement when consensus_points is empty, got %q", d.ConfidenceRationale)
	}
}

// TestAnalyzeDivergence_YesNoFramingNotMisreadAsConsensus reproduces a
// real E2E finding: two panelists answer a yes/no question with directly
// opposing positions phrased with a modal negation ("will not") rather than
// "does not"/"did not"/"do not". Before contrastCues covered modal negation
// forms, HasContrastCue saw no cue on either sentence, polarityDiffers was
// false, and the two opposite answers were reported as a consensus point.
func TestAnalyzeDivergence_YesNoFramingNotMisreadAsConsensus(t *testing.T) {
	responses := map[string]string{
		"a/x": "Yes, the Federal Reserve will cut interest rates in December this year.",
		"b/y": "No, the Federal Reserve will not cut interest rates in December this year.",
	}
	d := AnalyzeDivergence(responses)

	if len(d.ConsensusPoints) != 0 {
		t.Fatalf("expected no consensus for opposite-polarity Yes/No answers, got %+v", d.ConsensusPoints)
	}
	if len(d.Contradictions) == 0 {
		t.Fatalf("expected the Yes/No disagreement to be reported as a contradiction, got none: %+v", d)
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
