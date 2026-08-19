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

// TestExtractClaimEvidenceSpellingVariantsAndNumericWeight is the harness for
// #594: a claim written in American spelling (and/or naming a specific
// number) must not lose Signal to an earlier, less-relevant sentence just
// because the source text uses the British spelling variant and the current
// scoring under-weights the claim's own number relative to repeated proper
// nouns.
func TestExtractClaimEvidenceSpellingVariantsAndNumericWeight(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		claim     string
		wantInSig string // substring the Signal must contain
	}{
		{
			// Mirrors the #594 Eiffel Tower repro: the visitor-count sentence
			// repeats "Eiffel Tower" (2 literal term matches) and appears
			// earlier in the document; the height sentence refers back with
			// "it" and uses the British spelling "metres", so pre-fix it only
			// ties on term count and loses the document-order tie-break.
			name: "meters/metres spelling variant plus numeric weight beats earlier proper-noun sentence",
			text: "Millions of tourists visit the Eiffel Tower every year, making it one of the world's most-visited paid attractions. " +
				"It was designed by Gustave Eiffel's company for the 1889 World's Fair. " +
				"Excluding transmitters, it stands 330 metres (1,083 ft) tall.",
			claim:     "The Eiffel Tower is 330 meters tall",
			wantInSig: "330 metres",
		},
		{
			// Pure spelling-variant case (no numeric term involved): the
			// colour/color mismatch must not cost the sentence its match.
			name: "color/colour spelling variant beats tied proper-noun-only sentences",
			text: "Millions of tourists visit the Golden Gate Bridge every year. " +
				"The Golden Gate Bridge was named after the Golden Gate strait it crosses. " +
				"It is painted a distinctive colour known as International Orange.",
			claim:     "the bridge is painted a bright color",
			wantInSig: "colour",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := ExtractClaimEvidence(tt.text, tt.claim)
			if ev.Signal == "" {
				t.Fatal("expected a signal sentence")
			}
			if !strings.Contains(strings.ToLower(ev.Signal), strings.ToLower(tt.wantInSig)) {
				t.Errorf("expected Signal to contain %q, got: %q", tt.wantInSig, ev.Signal)
			}
		})
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
		t.Error("the negation cue 'did not' should be detected (the bare 'however' connective is intentionally NOT a cue — see #264)")
	}
	if HasContrastCue([]string{"The vaccine reduced infection rates substantially."}) {
		t.Error("a plain supporting sentence should NOT carry a contrast cue")
	}
	if HasContrastCue(nil) {
		t.Error("empty evidence should not signal contrast")
	}
}

// TestHasContrastCueIgnoresDiscourseConnectives is the regression guard for #264:
// bare discourse-contrast connectives (however/although/whereas/in contrast/
// nevertheless/conversely/unlike/rather than) contrast two arbitrary things within a
// sentence WITHOUT opposing the claim, so they must NOT raise a contrast cue.
// Otherwise supporting sources get a false "may refute" heads-up (a trust-suite
// false positive that trains users to ignore the signal).
func TestHasContrastCueIgnoresDiscourseConnectives(t *testing.T) {
	// The exact live-repro sentence from the LeCun/Bengio/Hinton Deep Learning
	// abstract (DOI 10.1038/nature14539) that wrongly tripped contrastSignal.
	lecun := "Deep convolutional nets have brought about breakthroughs in processing images, " +
		"video, speech and audio, whereas recurrent nets have shone light on sequential data such as text and speech."
	if HasContrastCue([]string{lecun}) {
		t.Errorf("the LeCun 'whereas' sentence supports the claim and must NOT trip a contrast cue: %q", lecun)
	}

	// Each bare connective, in an otherwise-supporting sentence, must NOT trip.
	benign := []string{
		"However, the model improves accuracy on every benchmark tested.",
		"Although widely studied, the method remains the standard approach.",
		"In contrast to older systems, this one works reliably.",
		"Nevertheless, the results confirm the original finding.",
		"Conversely, larger models also improve.",
		"Unlike convolutional nets, recurrent nets process sequences — both succeed.",
		"It scales by adding layers rather than widening them.",
	}
	for _, s := range benign {
		if HasContrastCue([]string{s}) {
			t.Errorf("bare discourse connective without negation must NOT trip a contrast cue: %q", s)
		}
	}

	// Genuine refutations MUST still trip — the surviving negation cues catch them
	// even when a discourse connective is also present.
	refutations := []string{
		"However, the drug did not reduce mortality in the trial.",
		"In contrast, no significant effect was found between the groups.",
		"Although promising, the hypothesis was rejected by the data.",
		"The replication failed to reproduce the original effect.",
	}
	for _, s := range refutations {
		if !HasContrastCue([]string{s}) {
			t.Errorf("a genuine refutation must still trip a contrast cue: %q", s)
		}
	}
}

// TestHasContrastCueCatchesRealWorldRefutationLanguage is the regression guard
// for a gap surfaced by a live GEO-defense eval run (2026-07-10, verify_recommendation
// against real search results): sentences using common real-world refutation
// vocabulary — "falsely", "debunked", "hoax", "avoid section" — that the original
// negation-only cue list missed, so corroborateRecommendation's disagreeCount
// under-counted genuine disagreement.
func TestHasContrastCueCatchesRealWorldRefutationLanguage(t *testing.T) {
	refutations := []string{
		"The CDC website now falsely links vaccines and autism.",
		"Health officials debunked the claim within days.",
		"Researchers called the treatment an unfounded hoax with no causal link to recovery.",
		"The report was widely discredited as misinformation.",
		"The study's data were later found to be fabricated.",
		"Experts say the claim is not true and lacks evidence.",
	}
	for _, s := range refutations {
		if !HasContrastCue([]string{s}) {
			t.Errorf("real-world refutation language must trip a contrast cue: %q", s)
		}
	}

	// A bare mention of the word "avoid" outside of a refutation context must NOT
	// trip — "avoid" itself is too context-dependent to add as a standalone cue
	// (see the comment in contrastCues).
	if HasContrastCue([]string{"Patients should avoid taking the drug on an empty stomach."}) {
		t.Error(`a benign dosing instruction with "avoid" must NOT trip a contrast cue`)
	}
}

// TestClaimTermCoverageWindowedExcludesReferencesSection reproduces #522: a
// claim's terms superficially matching the TITLES of a paper's own cited
// works, in its bibliography, must not count as coverage — only the body
// text is measured.
func TestClaimTermCoverageWindowedExcludesReferencesSection(t *testing.T) {
	body := "This work reports nanometre-scale thermometry using NV centers in diamond for measuring temperature in living cells. " +
		"The technique enables precise thermal mapping at the cellular level."
	bibliography := "\nReferences\n" +
		"1. Schroeder, A. et al. Treating metastatic cancer with nanotechnology. Nature Reviews Cancer, 171-176 (2012).\n" +
		"2. Some other cancer nanoparticle ablation study, Cancer Research, 4372-4382 (2012).\n"
	text := body + bibliography

	claim := "This paper proves that CRISPR-Cas9 can cure all forms of cancer."
	matched, total := ClaimTermCoverageWindowed(text, claim, 0)
	if matched != 0 {
		t.Errorf("cancer claim should score 0 coverage against a thermometry paper once the bibliography is excluded, got matched=%d/%d", matched, total)
	}

	ev := ExtractClaimEvidence(text, claim)
	if len(ev.KeySentences) != 0 {
		t.Errorf("expected no claim evidence once the bibliography is excluded, got %v", ev.KeySentences)
	}

	// Precondition: without exclusion, the bibliography's "cancer" mentions would
	// have matched — proving the fix, not a vacuously-true claim setup. Checked
	// via a raw substring scan (not ClaimTermCoverage, which now strips the
	// References section on its own input too).
	if !strings.Contains(strings.ToLower(bibliography), "cancer") {
		t.Fatalf("precondition failed: bibliography text should itself contain claim terms")
	}
}

// TestClaimTermCoverageWindowedExcludesNumberedReferenceListWithoutHeader
// reproduces #522's actual live repro: the arXiv:1304.1068 PDF text has no
// literal "References" header line at all (the scraper's extraction dropped
// it) — only the numbered bibliography entries (1., 2., 3., ...) survive. The
// header-only regex would miss this; the numbered-list detector must catch
// it.
func TestClaimTermCoverageWindowedExcludesNumberedReferenceListWithoutHeader(t *testing.T) {
	body := "This work demonstrates nanoscale thermometry using nitrogen-vacancy color centers in diamond to measure temperature in a living cell. " +
		"Acknowledgements: we thank colleagues for helpful discussions."
	bibliography := "\n1. Yue, Y. and Wang, X. Nanoscale thermal probing. Reviews 3 (2012).\n" +
		"2. Lucchetta, E. et al. Dynamics of embryonic patterning. Nature 434, 1134-1138 (2005).\n" +
		"3. Kumar, S. V. and Wigge, P. A. Nucleosomes and thermosensory response. Cell 140, 136-147 (2010).\n" +
		"6. Schroeder, A. et al. Treating metastatic cancer with nanotechnology. Nature Reviews Cancer 12, 39-50 (2011).\n" +
		"9. Vreugdenburg, T. et al. Elastography and digital infrared thermography for breast cancer screening. (2013).\n"
	text := body + bibliography

	claim := "This paper proves that CRISPR-Cas9 can cure all forms of cancer."
	matched, _ := ClaimTermCoverageWindowed(text, claim, 0)
	if matched != 0 {
		t.Errorf("cancer claim should score 0 coverage once the header-less numbered reference list is excluded, got matched=%d", matched)
	}

	// Sanity: a numbered list that never reaches 1→2→3 in order (just a
	// coincidental "1." in body prose) must NOT be treated as a reference list.
	falsePositive := "Step 1. Mix the reagents. Then move to the next station. The cancer cells were plated."
	if idx := numberedReferenceListStart(strings.Split(falsePositive, "\n")); idx >= 0 {
		t.Errorf("a lone numbered step should not be detected as a reference list, got start index %d", idx)
	}
}

// TestClaimTermsKeepsNumericTokens reproduces part of #523: a claim's
// numeric-only tokens (e.g. a vote count) must survive tokenization even
// though they're shorter than the 3-char minimum applied to words.
func TestClaimTermsKeepsNumericTokens(t *testing.T) {
	terms := claimTerms("The FOMC voted 9-3 with Hammack, Kashkari, and Logan dissenting")
	joined := strings.Join(terms, ",")
	for _, want := range []string{"9", "3"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected numeric term %q to survive claimTerms, got %v", want, terms)
		}
	}
}

// TestClaimTermCoverageWindowedToleratesInflection reproduces #523: a claim
// term and its inflected form in the source ("dissenting" vs. the source's
// "dissented") must be recognized as the same word.
func TestClaimTermCoverageWindowedToleratesInflection(t *testing.T) {
	text := "The Federal Reserve voted 9-3 to raise rates. Hammack, Kashkari, and Logan dissented from the decision."
	claim := "The FOMC voted 9-3 with Hammack, Kashkari, and Logan dissenting in favor of a rate hike"
	matched, total := ClaimTermCoverageWindowed(text, claim, 0)
	if float64(matched)/float64(total) < 0.6 {
		t.Errorf("verbatim FOMC vote claim should clear the addressed threshold, got matched=%d total=%d", matched, total)
	}
}

// TestClaimTermCoverageWindowedStemDoesNotMatchUnrelatedWords is the
// regression guard for a false positive a live wet-test against the real
// arXiv:1304.1068 PDF text surfaced in #523/#549: the initial stemming fix
// matched a claim term's stem as a raw SUBSTRING of the source text, so claim
// term "proves" (stemmed to "prov") wrongly matched unrelated words like
// "improved" and "provide" that merely contain that fragment. Matching must
// be against whole source words (or their stems), never substrings.
func TestClaimTermCoverageWindowedStemDoesNotMatchUnrelatedWords(t *testing.T) {
	text := "The resolution of our method can be improved by increasing the number of centers, " +
		"and this approach may provide a powerful new tool for biological research."
	claim := "This paper proves that CRISPR-Cas9 can cure all forms of cancer."
	matched, _ := ClaimTermCoverageWindowed(text, claim, 0)
	if matched != 0 {
		t.Errorf("stemmed term 'prov' must not substring-match 'improved'/'provide', got matched=%d", matched)
	}

	ev := ExtractClaimEvidence(text, claim)
	if len(ev.KeySentences) != 0 {
		t.Errorf("expected no claim evidence from unrelated substring matches, got %v", ev.KeySentences)
	}
}

// TestClaimTermCoverageWindowedShortDocumentUsesWholeDocCoverage reproduces
// #523's actual live repro: a real, short Federal Reserve FOMC press release
// verbatim-states a 9-3 vote with named dissenters, but the vote-count
// sentence and the dissenter-naming sentence are more than one
// defaultClaimWindow apart, so a fixed sliding window undercounts a claim the
// whole short document genuinely supports.
func TestClaimTermCoverageWindowedShortDocumentUsesWholeDocCoverage(t *testing.T) {
	text := "Federal Reserve issues FOMC statement\n" +
		"For release at 2:00 p.m.\n" +
		"The Federal Open Market Committee approved the following statement for release by a 9 - 3 vote:\n" +
		"The Committee decided to maintain the target range for the federal funds rate at 3-1/2 to 3-3/4 percent, in support of the Federal Reserve's dual mandate.\n" +
		"The Committee is continuing its policy of maintaining ample reserves in the banking system.\n" +
		"Economic activity is expanding at a solid pace despite elevated uncertainty that owes, in part, to the conflict in the Middle East.\n" +
		"Productivity growth and capital investment are strong.\n" +
		"Job gains have kept pace with the workforce, and the unemployment rate has changed little.\n" +
		"Inflation remains elevated relative to the Committee's 2 percent goal, in part reflecting supply shocks that have driven price increases in certain sectors, including energy.\n" +
		"The Committee will deliver price stability.\n" +
		"Voting against the monetary policy action were Beth M. Hammack, Neel Kashkari, and Lorie K. Logan, who preferred to raise the target range for the federal funds rate by 1/4 percentage point at this meeting.\n"

	claim := "The FOMC voted 9-3 with Hammack, Kashkari, and Logan dissenting in favor of a rate hike"
	matched, total := ClaimTermCoverageWindowed(text, claim, 0)
	if float64(matched)/float64(total) < claimAddressedThresholdForTest {
		t.Errorf("verbatim FOMC vote claim against the real short press release should clear the addressed threshold, got matched=%d total=%d", matched, total)
	}
}

// claimAddressedThresholdForTest mirrors internal/tools' claimAddressedThreshold
// (0.6) — duplicated here rather than imported to avoid this package depending
// on internal/tools, which would invert the intended dependency direction.
const claimAddressedThresholdForTest = 0.6

func TestClaimTermsDropsShortNonNumericTokens(t *testing.T) {
	terms := claimTerms("a claim with an id of ab and a number 42")
	joined := strings.Join(terms, ",")
	if strings.Contains(joined, ",ab,") || strings.HasPrefix(joined, "ab,") {
		t.Errorf("short non-numeric token 'ab' should still be dropped, got %v", terms)
	}
	if !strings.Contains(joined, "42") {
		t.Errorf("numeric token '42' should survive, got %v", terms)
	}
}

func TestContainsAny(t *testing.T) {
	needles := []string{"foo", "bar"}
	if !ContainsAny("this has a foo in it", needles) {
		t.Error("expected match on foo")
	}
	if !ContainsAny("this has a bar in it", needles) {
		t.Error("expected match on bar")
	}
	if ContainsAny("no match here", needles) {
		t.Error("expected no match")
	}
	if ContainsAny("", needles) {
		t.Error("empty text should never match")
	}
	if ContainsAny("anything", nil) {
		t.Error("nil needles should never match")
	}
}

func TestCountAny(t *testing.T) {
	needles := []string{"foo", "bar", "baz"}
	if got := CountAny("foo and bar and baz all appear", needles); got != 3 {
		t.Errorf("expected 3 distinct needles, got %d", got)
	}
	if got := CountAny("only foo appears", needles); got != 1 {
		t.Errorf("expected 1 distinct needle, got %d", got)
	}
	if got := CountAny("none of these appear", needles); got != 0 {
		t.Errorf("expected 0 distinct needles, got %d", got)
	}
	// Repeated occurrences of the same needle count once, not per-occurrence.
	if got := CountAny("foo foo foo", needles); got != 1 {
		t.Errorf("repeated needle should count once, got %d", got)
	}
}

// BenchmarkClaimTermCoverageWindowedWithReferenceSection guards against the
// #522 reference-section pre-pass introducing an order-of-magnitude
// regression: it must stay a single linear scan over sentences, not a new
// nested scan (see issue #549, rule 4.1).
func BenchmarkClaimTermCoverageWindowedWithReferenceSection(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		sb.WriteString("The study measured outcomes across multiple cohorts over several years of follow-up. ")
	}
	sb.WriteString("\nReferences\n")
	for i := 0; i < 200; i++ {
		sb.WriteString("Author, A. et al. A related study on cohorts and outcomes. Journal of Studies, 1-10 (2020).\n")
	}
	longDoc := sb.String()
	claim := "the study measured outcomes across multiple cohorts"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ClaimTermCoverageWindowed(longDoc, claim, 0)
	}
}
