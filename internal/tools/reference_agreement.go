package tools

import (
	"regexp"
	"strings"

	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// Reference-versus-record agreement (#851). A title match alone must not confirm
// a reference when the reference also names a year or an author and the matched
// record disagrees. "No data" is never disagreement: a reference without a year
// or a surname, and a record without a year or authors, are left unchanged.

// refYearTolerance allows a preprint-to-publication gap.
const refYearTolerance = 1

// refSurnameWord is one capitalized name word, with optional apostrophe or hyphen.
const refSurnameWord = `\p{Lu}[\p{L}'’\-]+`

var (
	refYearRe = regexp.MustCompile(`\b(?:19|20)\d{2}\b`)
	// refNoiseRe removes text whose digits look like a year but are not one:
	// URLs, arXiv ids (2001.12345) and DOIs.
	refNoiseRe = regexp.MustCompile(`(?i)https?://\S+|\b\d{4}\.\d{4,5}(?:v\d+)?\b|\b10\.\d{4,9}/\S+`)

	// Surname patterns. Each one needs an author-position cue, so a capitalized
	// word in the title or venue (for example "Nature") is never read as a surname.
	refSurnameEtAlRe     = regexp.MustCompile(`(` + refSurnameWord + `),?\s+et\s+al\b`)
	refSurnameInitialRe  = regexp.MustCompile(`(` + refSurnameWord + `),\s*\p{Lu}\.`)
	refInitialSurnameRe  = regexp.MustCompile(`\p{Lu}\.\s*(?:\p{Lu}\.\s*)*(` + refSurnameWord + `)`)
	refLeadSurnameYearRe = regexp.MustCompile(`^\s*(` + refSurnameWord + `)(?:\s+(?:and|&)\s+(` + refSurnameWord + `))?\s*[,(]?\s*\(?(?:19|20)\d{2}`)
)

// referenceDisagrees reports whether the reference text names a year or an
// author that the matched record contradicts.
func referenceDisagrees(ref string, rec *search.AcademicResult) bool {
	if rec == nil {
		return false
	}
	return referenceYearDisagrees(ref, rec) || referenceSurnameDisagrees(ref, rec)
}

// referenceYearDisagrees is true when the reference holds at least one year, the
// record has a year, and no reference year is within refYearTolerance of it.
// Years that are part of the record title (for example "Prospects 2022") are
// ignored.
func referenceYearDisagrees(ref string, rec *search.AcademicResult) bool {
	if rec.Year <= 0 {
		return false
	}
	years := refYearRe.FindAllString(refNoiseRe.ReplaceAllString(ref, " "), -1)
	seen := false
	for _, ys := range years {
		if strings.Contains(rec.Title, ys) {
			continue
		}
		y := 0
		for _, c := range ys {
			y = y*10 + int(c-'0')
		}
		seen = true
		if d := y - rec.Year; d >= -refYearTolerance && d <= refYearTolerance {
			return false
		}
	}
	return seen
}

// referenceSurnameDisagrees is true when the reference names at least one
// surname (in an author position), the record lists authors, and none of the
// named surnames is a name word of any record author. Surnames that also appear
// in the record title are ignored.
func referenceSurnameDisagrees(ref string, rec *search.AcademicResult) bool {
	if len(rec.Authors) == 0 {
		return false
	}
	named := referenceSurnames(ref, rec.Title)
	if len(named) == 0 {
		return false
	}
	authorWords := map[string]bool{}
	for _, a := range rec.Authors {
		for _, w := range strings.Fields(normalizeTitleText(a)) {
			authorWords[w] = true
		}
	}
	if len(authorWords) == 0 {
		return false
	}
	for _, s := range named {
		if authorWords[s] {
			return false
		}
	}
	return true
}

// referenceSurnames returns the normalized surname words the reference names in
// an author position, minus words that appear in the record title.
func referenceSurnames(ref, recTitle string) []string {
	titleWords := map[string]bool{}
	for _, w := range strings.Fields(normalizeTitleText(recTitle)) {
		titleWords[w] = true
	}
	var out []string
	add := func(raw string) {
		for _, w := range strings.Fields(normalizeTitleText(raw)) {
			if len(w) < 2 || w == "et" || w == "al" || titleWords[w] {
				continue
			}
			out = append(out, w)
		}
	}
	for _, re := range []*regexp.Regexp{refSurnameEtAlRe, refSurnameInitialRe, refInitialSurnameRe} {
		for _, m := range re.FindAllStringSubmatch(ref, -1) {
			add(m[1])
		}
	}
	if m := refLeadSurnameYearRe.FindStringSubmatch(ref); m != nil {
		add(m[1])
		add(m[2])
	}
	return out
}

// refInitialTokenRe matches a lone initial such as "A." in a reference.
var refInitialTokenRe = regexp.MustCompile(`^\p{Lu}\.$`)

// referenceTitleQuery strips the author and year cues from a reference and
// returns what is left, normally the title. Providers rank a bare title far
// better than "Vaswani et al. 2017, Title", so it is a second search query when
// the first results disagree with the reference. Returns "" when nothing was
// stripped or fewer than two words are left.
func referenceTitleQuery(ref string) string {
	drop := map[string]bool{"et": true, "al": true}
	for _, s := range referenceSurnames(ref, "") {
		drop[s] = true
	}
	var kept []string
	for _, tok := range strings.Fields(refNoiseRe.ReplaceAllString(ref, " ")) {
		trimmed := strings.Trim(tok, ",;:()[]")
		bare := strings.Trim(tok, ",;:()[].")
		if trimmed == "" || refInitialTokenRe.MatchString(trimmed) || refYearRe.FindString(bare) == bare {
			continue
		}
		if drop[normalizeTitleText(trimmed)] {
			continue
		}
		kept = append(kept, strings.Trim(tok, ",;:"))
	}
	q := strings.TrimSpace(strings.Join(kept, " "))
	if len(kept) < 2 || q == strings.TrimSpace(ref) {
		return ""
	}
	return q
}
