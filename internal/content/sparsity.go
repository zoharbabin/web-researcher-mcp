package content

import "fmt"

// SparseWordThreshold is the word count below which content is flagged as thin.
// 150 words is roughly one short paragraph: below a real article's lede, above
// the placeholder text a bot-wall or stub page typically ships.
const SparseWordThreshold = 150

// SparsityWarning returns the word count of text and, when that count is below
// SparseWordThreshold, a warning for the caller. The warning is "" otherwise.
// Call it on the content that is delivered, so the count matches what the
// caller receives.
func SparsityWarning(text string) (words int, warning string) {
	words = WordCount(text)
	if words < SparseWordThreshold {
		warning = fmt.Sprintf("Extracted content is thin (%d words); claim checks against this source may be unreliable.", words)
	}
	return words, warning
}
