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
		for _, m := range stanceMarkers {
			if strings.Contains(lower, m) {
				score += 1.5 // a stance-bearing sentence is worth more than a bare mention
				break
			}
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
