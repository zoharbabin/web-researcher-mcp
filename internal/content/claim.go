package content

import (
	"sort"
	"strings"
	"unicode"
)

// ClaimEvidence is the per-source output of claim-relationship analysis (#66).
// We deliberately surface the EVIDENCE, not a verdict: the caller's LLM decides
// whether a source supports, contradicts, or merely mentions the claim. No
// server-side model call — pure text extraction over already-fetched content.
type ClaimEvidence struct {
	// Signal is the single most indicative sentence (highest-scoring), or "".
	Signal string `json:"signal,omitempty"`
	// KeySentences are the top claim-relevant sentences in document order.
	KeySentences []string `json:"keySentences,omitempty"`
}

// maxKeySentences caps how many sentences are returned, keeping the payload
// bounded and the signal high.
const maxKeySentences = 5

// stanceMarkers are terms that indicate a sentence takes a position on a claim
// (negation, contrast, hedging, statistical findings) — they boost a sentence's
// relevance beyond mere keyword overlap, surfacing the sentences most useful for
// the LLM's supports/contradicts judgment.
var stanceMarkers = []string{
	"no significant", "not significant", "significant", "however", "although",
	"contrary", "contradict", "dispute", "refute", "disprove", "failed to",
	"did not", "does not", "cannot", "no evidence", "no association", "no difference",
	"in contrast", "whereas", "but ", "yet ", "nevertheless", "conversely",
	"supports", "consistent with", "in line with", "confirms", "demonstrates",
	"found that", "showed that", "concluded", "p =", "p=", "p <", "p<", "p >", "p>",
	"95% ci", "confidence interval", "odds ratio", "relative risk", "meta-analysis",
	"randomized", "rct", "compared with", "compared to",
}

// contrastCues are NEGATION / REFUTATION terms that oppose a claim's stance: a
// matched evidence sentence carrying one of these may REFUTE the claim even though
// it shares the claim's terms — the lexical "false-addressed" hole. We surface this
// as a neutral "read this sentence yourself" signal, never as a refutes verdict.
//
// CRITICAL — this list holds ONLY cues that encode opposition (an explicit negation
// or a verb of contradiction). It deliberately EXCLUDES bare discourse-contrast
// connectives ("however", "although", "whereas", "in contrast", "nevertheless",
// "conversely", "unlike", "rather than"): those merely contrast two arbitrary things
// within a sentence and do NOT oppose the claim, so they fire on supporting sources
// (e.g. the LeCun et al. Deep Learning abstract's "…breakthroughs in processing
// images … whereas recurrent nets …"), producing trust-suite false positives (#264).
// A genuine refutation almost always carries an explicit negation alongside any such
// connective ("However, the drug DID NOT reduce mortality" → "did not"; "In contrast,
// NO SIGNIFICANT effect" → "no significant"), so the bare connectives add no recall.
// Do NOT re-add them; they belong in stanceMarkers (relevance scoring), not here.
var contrastCues = []string{
	"not significant", "no significant", "contrary", "contrary to",
	"contradict", "dispute", "refute", "disprove", "failed to", "did not",
	"does not", "do not", "no evidence", "no association", "no difference",
	"rejected", "no effect", "not associated", "not supported",
	// Added after a live GEO-defense eval run (2026-07-10) surfaced real refutation
	// sentences these missed, e.g. "ivermectin failed to treat COVID-19" was caught
	// by "failed to" above, but "CDC website now falsely links vaccines and autism"
	// and "we've also added LastPass ... to the avoid section" were not. Unlike
	// "avoid" (too context-dependent — "avoid this side effect" doesn't oppose a
	// claim), each of these is an unambiguous negation/refutation word regardless
	// of surrounding context, so they carry the same low false-positive risk as the
	// terms above.
	"falsely", "debunk", "debunked", "hoax", "unfounded", "baseless",
	"discredited", "misinformation", "fabricated", "no causal link",
	"no link between", "not true", "untrue", "lacks evidence",
	"unsupported by evidence",
}

// ContainsAny reports whether lowerText (already lowercased by the caller)
// contains any of needles. Shared substring-match primitive for the lexical,
// English-keyword-heuristic signals in this package (HasContrastCue, the
// stance-marker boost in ExtractClaimEvidence) and in classify.go
// (DetectConflictOfInterest) and brand_research.go (looksLikeBrandPage) — the
// matching mechanism is common across all of them; the word lists themselves
// stay domain-specific and are never merged (see issue #390).
func ContainsAny(lowerText string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(lowerText, n) {
			return true
		}
	}
	return false
}

// CountAny reports how many distinct needles appear anywhere in lowerText
// (already lowercased by the caller). Companion to ContainsAny for callers
// that need a co-occurrence count rather than a short-circuiting boolean
// (e.g. brand_research.go's weak-signal threshold check).
func CountAny(lowerText string, needles []string) int {
	count := 0
	for _, n := range needles {
		if strings.Contains(lowerText, n) {
			count++
		}
	}
	return count
}

// HasContrastCue reports whether any sentence in evidence contains a
// negation/contrast cue — i.e. a matched-on-terms sentence that may oppose the
// claim. Used by audit_bibliography (#174) to raise a neutral contrastSignal so a
// source that lexically "addresses" a claim while refuting it isn't read as
// reassurance. Evidence, not a verdict: it flags "read this", never "refutes".
//
// English-keyword heuristic (#390): an evidence sentence in another language
// carrying a genuine refutation will not match this list and returns false —
// that means "the heuristic didn't fire," not "confirmed no contrast."
func HasContrastCue(sentences []string) bool {
	for _, s := range sentences {
		if ContainsAny(strings.ToLower(s), contrastCues) {
			return true
		}
	}
	return false
}

// ExtractClaimEvidence finds the sentences in content most relevant to claim and
// returns them (plus the single strongest as Signal). Returns a zero ClaimEvidence
// when claim or content is empty, or when nothing relevant is found.
//
// Scoring per sentence = (# distinct claim terms present) + (stance-marker bonus).
// A sentence must contain at least one claim term to qualify, so unrelated
// stance-bearing sentences are never surfaced.
func ExtractClaimEvidence(text, claim string) ClaimEvidence {
	claim = strings.TrimSpace(claim)
	if claim == "" || strings.TrimSpace(text) == "" {
		return ClaimEvidence{}
	}

	terms := claimTerms(claim)
	if len(terms) == 0 {
		return ClaimEvidence{}
	}

	sentences := splitSentences(text)

	type scored struct {
		idx   int
		text  string
		score float64
	}
	var hits []scored
	for i, s := range sentences {
		lower := strings.ToLower(s)
		matched := 0
		for _, t := range terms {
			if strings.Contains(lower, t) {
				matched++
			}
		}
		if matched == 0 {
			continue // must mention the claim to be evidence
		}
		score := float64(matched)
		if ContainsAny(lower, stanceMarkers) {
			score += 1.5 // a stance-bearing sentence is worth more than a bare mention
		}
		hits = append(hits, scored{idx: i, text: strings.TrimSpace(s), score: score})
	}
	if len(hits) == 0 {
		return ClaimEvidence{}
	}

	// Rank by score desc (then document order) to pick the top sentences.
	sort.SliceStable(hits, func(a, b int) bool {
		if hits[a].score != hits[b].score {
			return hits[a].score > hits[b].score
		}
		return hits[a].idx < hits[b].idx
	})

	signal := hits[0].text
	top := hits
	if len(top) > maxKeySentences {
		top = top[:maxKeySentences]
	}
	// Present key sentences in original document order for readability.
	sort.SliceStable(top, func(a, b int) bool { return top[a].idx < top[b].idx })
	out := make([]string, 0, len(top))
	for _, h := range top {
		out = append(out, h.text)
	}
	return ClaimEvidence{Signal: signal, KeySentences: out}
}

// ClaimTermCoverage reports how many of a claim's distinct significant terms
// appear anywhere in text (matched) out of the total distinct significant terms
// in the claim. It is the transparent, dependency-free measure of how much a
// source actually overlaps a claim's topic — used by audit_bibliography (#174)
// to distinguish a source that addresses a claim from one that's simply the
// wrong source. total==0 means the claim had no significant terms to match.
func ClaimTermCoverage(text, claim string) (matched, total int) {
	terms := claimTerms(claim)
	total = len(terms)
	if total == 0 || strings.TrimSpace(text) == "" {
		return 0, total
	}
	lower := strings.ToLower(text)
	for _, t := range terms {
		if strings.Contains(lower, t) {
			matched++
		}
	}
	return matched, total
}

// ClaimTermCoverageWindowed reports the PEAK claim-term coverage found within any
// contiguous sentence window of the source, rather than across the whole document
// (#177). Whole-document coverage dilutes on long, broad sources: an unrelated
// claim can pick up stray term hits scattered across a 50KB page and score
// "partially_addressed" when no single passage actually discusses it. Measuring
// the best-matching local window instead asks the sharper question — "does some
// focused passage cover most of the claim's terms?" — so a genuinely off-topic
// claim against a long page correctly scores zero local coverage.
//
// matched is the maximum number of distinct claim terms co-occurring in any
// window of up to windowSize sentences; total is the claim's distinct term count.
// Deterministic and lexical (no dependency): a single linear scan with a sliding
// window over the already-split sentences. windowSize<=0 uses defaultClaimWindow.
// A document with fewer sentences than the window is measured as one window (i.e.
// degrades to whole-document coverage), so short sources are unaffected.
func ClaimTermCoverageWindowed(text, claim string, windowSize int) (matched, total int) {
	terms := claimTerms(claim)
	total = len(terms)
	if total == 0 || strings.TrimSpace(text) == "" {
		return 0, total
	}
	if windowSize <= 0 {
		windowSize = defaultClaimWindow
	}

	sentences := splitSentences(text)
	if len(sentences) == 0 {
		// No sentence boundaries (e.g. one long line) — fall back to whole-text.
		return ClaimTermCoverage(text, claim)
	}

	// Per-sentence presence bitsets over the claim terms, computed once.
	lowerSentences := make([]string, len(sentences))
	for i, s := range sentences {
		lowerSentences[i] = strings.ToLower(s)
	}

	best := 0
	for start := 0; start < len(sentences); start++ {
		end := start + windowSize
		if end > len(sentences) {
			end = len(sentences)
		}
		seen := 0
		for ti := range terms {
			t := terms[ti]
			for w := start; w < end; w++ {
				if strings.Contains(lowerSentences[w], t) {
					seen++
					break
				}
			}
		}
		if seen > best {
			best = seen
			if best == total {
				break // can't do better than full coverage
			}
		}
		// Once the window reaches the document end, sliding further only shrinks it.
		if end == len(sentences) {
			break
		}
	}
	return best, total
}

// defaultClaimWindow is the sentence-window size for ClaimTermCoverageWindowed.
// Sized so a claim's terms can co-occur within a focused passage (a few adjacent
// sentences / a paragraph) without spanning an entire long article.
const defaultClaimWindow = 4

// claimTerms tokenizes a claim into distinct, lowercased significant terms,
// dropping stop words and very short tokens so matching is meaningful.
func claimTerms(claim string) []string {
	fields := strings.FieldsFunc(strings.ToLower(claim), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	seen := make(map[string]struct{}, len(fields))
	var terms []string
	for _, f := range fields {
		if len(f) < 3 || claimStopWords[f] {
			continue
		}
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		terms = append(terms, f)
	}
	return terms
}

// splitSentences breaks text into sentences on ., !, ? boundaries while keeping
// the split lightweight (no NLP dependency). Newlines also terminate a sentence.
// Fragments shorter than a few chars are dropped.
func splitSentences(text string) []string {
	var sentences []string
	var b strings.Builder
	flush := func() {
		s := strings.TrimSpace(b.String())
		if len(s) >= 12 { // drop trivial fragments
			sentences = append(sentences, s)
		}
		b.Reset()
	}
	runes := []rune(text)
	for i, r := range runes {
		b.WriteRune(r)
		switch r {
		case '\n':
			flush()
		case '.', '!', '?':
			// Terminate only when followed by whitespace/EOF, so "U.S." or "3.5"
			// mid-sentence don't over-split.
			if i+1 >= len(runes) || unicode.IsSpace(runes[i+1]) {
				flush()
			}
		}
	}
	flush()
	return sentences
}

// claimStopWords are common words excluded from claim term matching.
var claimStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "was": true, "were": true,
	"that": true, "this": true, "with": true, "from": true, "have": true, "has": true,
	"had": true, "not": true, "but": true, "all": true, "any": true, "can": true,
	"will": true, "would": true, "should": true, "could": true, "does": true,
	"did": true, "what": true, "when": true, "where": true, "which": true, "who": true,
	"why": true, "how": true, "their": true, "there": true, "they": true, "than": true,
	"then": true, "into": true, "over": true, "such": true, "more": true, "most": true,
	"some": true, "been": true, "being": true, "about": true, "between": true,
}
