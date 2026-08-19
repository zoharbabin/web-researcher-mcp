package content

import (
	"regexp"
	"sort"
	"strconv"
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

	sentences := splitSentences(stripReferencesSection(text))

	type scored struct {
		idx   int
		text  string
		score float64
	}
	var hits []scored
	for i, s := range sentences {
		lower := strings.ToLower(s)
		words := wordSet(lower)
		matched := 0
		for _, t := range terms {
			if termMatches(words, t) {
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
		if claimHasMatchedNumber(terms, words) {
			// A sentence naming the claim's own specific number is stronger
			// evidence for that claim than one merely repeating proper nouns
			// from it (#594) — weight it at least as high as the stance bonus.
			score += 1.5
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
	words := wordSet(strings.ToLower(stripReferencesSection(text)))
	for _, t := range terms {
		if termMatches(words, t) {
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

	sentences := splitSentences(stripReferencesSection(text))
	if len(sentences) == 0 {
		// No sentence boundaries (e.g. one long line) — fall back to whole-text.
		return ClaimTermCoverage(text, claim)
	}

	// Per-sentence word sets over the claim terms, computed once.
	sentenceWords := make([]map[string]struct{}, len(sentences))
	for i, s := range sentences {
		sentenceWords[i] = wordSet(strings.ToLower(s))
	}

	// A short, single-topic document (e.g. a press release stating a vote count
	// near the top and naming the dissenters near the bottom) has no "scattered
	// stray hits across an unrelated long page" risk — that's what windowing
	// guards against (#177). Splitting it into small windows instead
	// undercounts genuinely-supported claims whose terms are spread across the
	// whole short body, which is exactly what #523's live repro (a real FOMC
	// press release) surfaced: the vote count and the named dissenters are both
	// true of the same document but more than one window apart. Below the
	// threshold, whole-document coverage is the right measure.
	if len(sentences) <= shortDocSentenceThreshold {
		merged := make(map[string]struct{})
		for _, sw := range sentenceWords {
			for w := range sw {
				merged[w] = struct{}{}
			}
		}
		matched = 0
		for _, t := range terms {
			if termMatches(merged, t) {
				matched++
			}
		}
		return matched, total
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
				if termMatches(sentenceWords[w], t) {
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

// shortDocSentenceThreshold is the sentence count below which
// ClaimTermCoverageWindowed measures whole-document coverage instead of a
// sliding window (#523). A short document (press release, abstract, brief
// article) is inherently single-topic — the dilution risk windowing guards
// against (#177) only shows up on long, broad pages — so windowing it can
// only undercount a genuinely well-supported claim whose terms happen to
// fall in different sentences (e.g. a vote count near the top of a press
// release and the named dissenters near the bottom). Sized comfortably above
// defaultClaimWindow so it covers documents a few times the window's length,
// not just ones already smaller than a single window.
const shortDocSentenceThreshold = 20

// defaultClaimWindow is the sentence-window size for ClaimTermCoverageWindowed.
// Sized so a claim's terms can co-occur within a focused passage (a few adjacent
// sentences / a paragraph) without spanning an entire long article.
const defaultClaimWindow = 4

// SignificantTerms tokenizes free text into distinct, lowercased significant
// terms, dropping stop words and very short tokens. Exported for callers
// outside this package that need the same term-extraction used for claim
// matching — e.g. sequential_search's refinement-query reformulation (#511),
// which needs the DISTINCT content words of a research goal / knowledge gap
// rather than the raw strings, so it can vary phrasing instead of
// concatenating them verbatim.
func SignificantTerms(text string) []string {
	return claimTerms(text)
}

// claimTerms tokenizes a claim into distinct, lowercased significant terms,
// dropping stop words and very short tokens so matching is meaningful. Purely
// numeric tokens (e.g. a "9-3" vote count) are kept regardless of length: a
// short number is often the single most claim-critical differentiator (#523),
// unlike a short word, which is usually noise.
func claimTerms(claim string) []string {
	fields := strings.FieldsFunc(strings.ToLower(claim), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	seen := make(map[string]struct{}, len(fields))
	var terms []string
	for _, f := range fields {
		numeric := isNumericToken(f)
		if !numeric && (len(f) < 3 || claimStopWords[f]) {
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

// isNumericToken reports whether f consists entirely of digits.
func isNumericToken(f string) bool {
	if f == "" {
		return false
	}
	for _, r := range f {
		if !unicode.IsNumber(r) {
			return false
		}
	}
	return true
}

// claimHasMatchedNumber reports whether any of the claim's numeric terms
// (e.g. "330" from "330 meters tall") appears in words — i.e. this sentence
// names the specific number the claim is about, not just a proper noun the
// claim happens to mention (#594).
func claimHasMatchedNumber(terms []string, words map[string]struct{}) bool {
	for _, t := range terms {
		if isNumericToken(t) && termMatches(words, t) {
			return true
		}
	}
	return false
}

// termSuffixes are inflectional endings stripped by stemTerm so a claim term
// and its inflected form in the source text (e.g. claim "voted" vs. source
// "vote") are recognized as the same word (#523). Ordered longest first so
// "es"/"'s" don't shadow a longer suffix on the same token.
var termSuffixes = []string{"'s", "ing", "ed", "es", "s"}

// stemTerm strips a single trailing inflectional suffix from a lowercased
// word, leaving numeric tokens untouched (stripping digits would corrupt
// them). Best-effort and deliberately shallow — no real stemming, just enough
// to bridge simple verb/noun inflection, which is what #523's repro needed.
func stemTerm(w string) string {
	if isNumericToken(w) {
		return w
	}
	for _, suf := range termSuffixes {
		if strings.HasSuffix(w, suf) && len(w) > len(suf)+2 {
			return strings.TrimSuffix(w, suf)
		}
	}
	return w
}

// britishToAmericanSpelling maps a British spelling to its American
// equivalent (#594), so a claim term and its cross-variant spelling in
// source text (e.g. claim "meters" vs. a Wikipedia article's "metres") are
// recognized as the same word instead of undercounting one of them. Small,
// explicit, and one-directional by design: normalizeSpelling always
// canonicalizes toward the American form, so American-spelled input passes
// through unchanged and needs no reverse entries.
var britishToAmericanSpelling = map[string]string{
	"metre": "meter", "kilometre": "kilometer", "centimetre": "centimeter",
	"millimetre": "millimeter", "litre": "liter",
	"colour": "color", "favour": "favor", "flavour": "flavor",
	"honour": "honor", "humour": "humor", "labour": "labor",
	"neighbour": "neighbor", "rumour": "rumor", "armour": "armor",
	"behaviour": "behavior", "endeavour": "endeavor",
}

// normalizeSpelling canonicalizes a lowercased word toward its American
// spelling so British/American variants compare equal (#594). Falls back to
// a productive suffix rule for the common "-ise" -> "-ize" verb pattern
// (organise/organize, realise/realize) not covered by the explicit map.
// Returns w unchanged when no variant applies — safe as a no-op identity for
// already-American or unrelated words.
func normalizeSpelling(w string) string {
	if american, ok := britishToAmericanSpelling[w]; ok {
		return american
	}
	if strings.HasSuffix(w, "ise") && len(w) > 5 {
		return strings.TrimSuffix(w, "ise") + "ize"
	}
	return w
}

// wordSet tokenizes lowerText into a set of whole words plus each word's stem
// and spelling-normalized form, for exact (never substring) claim-term
// matching. A raw substring match on a stemmed term (e.g. claim "proves"
// stemmed to "prov") would wrongly match unrelated words that merely CONTAIN
// that fragment — "improved", "provide" — which is exactly the false-positive
// a live wet-test against the real arXiv:1304.1068 PDF text surfaced after
// the initial #523 fix. Matching against whole tokens (and their stems)
// instead of raw substrings closes that hole while still tolerating simple
// inflection.
func wordSet(lowerText string) map[string]struct{} {
	fields := strings.FieldsFunc(lowerText, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	set := make(map[string]struct{}, len(fields)*2)
	for _, f := range fields {
		set[f] = struct{}{}
		stemmed := stemTerm(f)
		if stemmed != f {
			set[stemmed] = struct{}{}
		}
		if normalized := normalizeSpelling(f); normalized != f {
			set[normalized] = struct{}{}
		}
		if normalizedStem := normalizeSpelling(stemmed); normalizedStem != stemmed {
			set[normalizedStem] = struct{}{}
		}
	}
	return set
}

// termMatches reports whether claim term t appears as a whole word (or its
// stem, or a British/American spelling variant of either — #594) in the set
// of words produced by wordSet, tolerating simple inflection and spelling
// variation in either direction without falling back to substring
// containment.
func termMatches(words map[string]struct{}, t string) bool {
	if _, ok := words[t]; ok {
		return true
	}
	if normalized := normalizeSpelling(t); normalized != t {
		if _, ok := words[normalized]; ok {
			return true
		}
	}
	stemmed := stemTerm(t)
	if stemmed != t {
		if _, ok := words[stemmed]; ok {
			return true
		}
		if normalizedStem := normalizeSpelling(stemmed); normalizedStem != stemmed {
			if _, ok := words[normalizedStem]; ok {
				return true
			}
		}
	}
	return false
}

// referenceSectionHeader matches a line that starts an academic
// bibliography/reference list, so that section can be excluded from claim-
// coverage scoring — otherwise a claim's terms can spuriously match the
// TITLES of a paper's own cited works rather than its actual body text
// (#522), e.g. a "cancer" claim matching a cited cancer-nanotechnology paper's
// title in the bibliography of a paper that is actually about something else
// entirely.
var referenceSectionHeader = regexp.MustCompile(`(?i)^\s*(references|bibliography|works cited|literature cited)\s*$`)

// numberedReferenceLine matches a line opening a numbered bibliography entry
// ("1. Smith, J. et al. ..."). Academic PDF text extraction routinely drops
// the "References" header itself (confirmed against the arXiv:1304.1068 PDF
// text underlying #522) while leaving the numbered entries intact, so the
// header regex alone misses real-world cases.
var numberedReferenceLine = regexp.MustCompile(`^\s*(\d{1,3})\.\s+\S`)

// numberedReferenceListStart returns the line index where a numbered
// bibliography (1., 2., 3., ... in order) begins, or -1 if none is found.
// Requires the sequence 1→2→3 to appear IN ORDER (not necessarily on
// consecutive lines, since a wrapped citation spans several) before
// concluding it found a reference list rather than a coincidental "1." in
// body prose — a bare "1." alone is far too common to trust on its own.
func numberedReferenceListStart(lines []string) int {
	startIdx := -1
	expected := 1
	for i, line := range lines {
		m := numberedReferenceLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		switch n {
		case expected:
			if expected == 1 {
				startIdx = i
			}
			expected++
			if expected > 3 {
				return startIdx
			}
		case 1:
			startIdx = i
			expected = 2
		default:
			startIdx = -1
			expected = 1
		}
	}
	return -1
}

// stripReferencesSection truncates text at the start of its
// References/Bibliography section, if one is found, so downstream claim-
// coverage scoring never scans citation-list text (#522). Detection is a
// single linear pre-pass over lines — a header-line scan first (deliberately
// not confused with an in-body mention like "see the references below,"
// which does not stand alone on its own line the way a real section header
// does), falling back to the numbered-reference-list shape when no explicit
// header line survived extraction.
func stripReferencesSection(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if referenceSectionHeader.MatchString(line) {
			return strings.Join(lines[:i], "\n")
		}
	}
	if idx := numberedReferenceListStart(lines); idx >= 0 {
		return strings.Join(lines[:idx], "\n")
	}
	return text
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
