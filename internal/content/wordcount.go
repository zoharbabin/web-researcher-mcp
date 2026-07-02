package content

import "unicode"

// WordCount is a language-agnostic proxy for how much prose text contains.
// Plain whitespace splitting (strings.Fields) undercounts scripts that don't
// separate words with spaces — CJK, Thai, Lao, Khmer, Myanmar — where a full,
// complete article collapses to a handful of whitespace-delimited chunks
// (often just one). For runes in those scripts, each rune counts as its own
// word instead of relying on surrounding whitespace; everything else is
// counted by contiguous non-space runs, same as strings.Fields.
func WordCount(text string) int {
	count := 0
	inWord := false
	for _, r := range text {
		if isDenseScriptRune(r) {
			count++
			inWord = false
			continue
		}
		if unicode.IsSpace(r) {
			inWord = false
			continue
		}
		if !inWord {
			count++
			inWord = true
		}
	}
	return count
}

// isDenseScriptRune reports whether r belongs to a script that does not use
// inter-word spaces. Hangul (Korean) is deliberately excluded — Korean text
// does space-separate words, so it is already counted correctly by the
// whitespace-run branch above.
func isDenseScriptRune(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Thai, r) ||
		unicode.Is(unicode.Lao, r) ||
		unicode.Is(unicode.Khmer, r) ||
		unicode.Is(unicode.Myanmar, r)
}
