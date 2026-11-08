package content

import (
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

// Passage is a stretch of a cleaned page. Offset is the byte position of its
// first byte in the page text, so a scrape_page call with that offset starts
// at the passage.
type Passage struct {
	Offset int    `json:"offset"`
	Text   string `json:"text"`
}

const (
	// MaxPassageBytes caps one passage. A longer paragraph is cut into pieces.
	MaxPassageBytes = 600
	// minPassageBytes is the size under which a block (a heading, a short
	// line) joins the block that follows it, so it is ranked with its text.
	minPassageBytes = 80
)

// RankPassages returns up to k passages of text that best match query, best
// first (equal scores keep page order). A passage scores the sum of the weights of the distinct
// query terms it contains. A term that appears in few passages weighs more
// than a common one. It is lexical and deterministic: same terms, stemming and
// spelling rules as claim matching, no model call. It returns nil when query
// has no significant terms or nothing matches.
func RankPassages(text, query string, k int) []Passage {
	terms := claimTerms(query)
	if len(terms) == 0 || k < 1 || strings.TrimSpace(text) == "" {
		return nil
	}
	passages := splitPassages(text)
	hits := make([][]bool, len(passages))
	df := make([]int, len(terms))
	for i, p := range passages {
		words := wordSet(strings.ToLower(p.Text))
		hits[i] = make([]bool, len(terms))
		for j, t := range terms {
			if termMatches(words, t) {
				hits[i][j] = true
				df[j]++
			}
		}
	}
	weight := make([]float64, len(terms))
	for j := range terms {
		if df[j] > 0 {
			weight[j] = math.Log(1 + float64(len(passages))/float64(df[j]))
		}
	}
	type scored struct {
		idx   int
		score float64
	}
	var ranked []scored
	for i := range passages {
		var s float64
		for j := range terms {
			if hits[i][j] {
				s += weight[j]
			}
		}
		if s > 0 {
			ranked = append(ranked, scored{i, s})
		}
	}
	if len(ranked) == 0 {
		return nil
	}
	sort.SliceStable(ranked, func(a, b int) bool { return ranked[a].score > ranked[b].score })
	if len(ranked) > k {
		ranked = ranked[:k]
	}
	out := make([]Passage, len(ranked))
	for i, r := range ranked {
		out[i] = passages[r.idx]
	}
	return out
}

// splitPassages cuts text into paragraph blocks. A block under minPassageBytes
// joins the next block, and a block over MaxPassageBytes is cut on the usual
// boundaries. Each passage keeps its byte offset in text.
func splitPassages(text string) []Passage {
	var out []Passage
	pending := -1 // offset of a short block waiting for the next one
	emit := func(start, end int) {
		for start < end && (text[start] == '\n' || text[start] == ' ') {
			start++
		}
		block := strings.TrimRight(text[start:end], " \n")
		for len(block) > MaxPassageBytes {
			cut := cutPoint(block, MaxPassageBytes)
			if cut == 0 {
				_, cut = utf8.DecodeRuneInString(block)
			}
			out = append(out, Passage{Offset: start, Text: strings.TrimRight(block[:cut], " \n")})
			start += cut
			for start < end && (text[start] == '\n' || text[start] == ' ') {
				start++
			}
			block = strings.TrimRight(text[start:end], " \n")
		}
		if block != "" {
			out = append(out, Passage{Offset: start, Text: block})
		}
	}
	for pos := 0; pos < len(text); {
		end := strings.Index(text[pos:], "\n\n")
		next := len(text)
		blockEnd := len(text)
		if end >= 0 {
			blockEnd = pos + end
			next = blockEnd + 2
		}
		if strings.TrimSpace(text[pos:blockEnd]) == "" {
			pos = next
			continue
		}
		if pending < 0 && len(strings.TrimSpace(text[pos:blockEnd])) < minPassageBytes && next < len(text) {
			pending = pos
			pos = next
			continue
		}
		start := pos
		if pending >= 0 {
			start = pending
			pending = -1
		}
		emit(start, blockEnd)
		pos = next
	}
	if pending >= 0 {
		emit(pending, len(text))
	}
	return out
}
