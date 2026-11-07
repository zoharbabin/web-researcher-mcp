package content

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Truncate cuts content to at most maxLength bytes, preferring a paragraph,
// then sentence, then line, then word boundary, and appends a marker.
func Truncate(content string, maxLength int) (string, bool) {
	if len(content) <= maxLength {
		return content, false
	}
	return content[:cutPoint(content, maxLength)] + "\n\n[content truncated]", true
}

// Slice returns the page of text that starts at byte offset and holds at most n
// bytes, cut on the same boundaries as Truncate. next is the offset where the
// following page starts. When more is true the page ends with a continuation
// marker that names next. The marker is extra: the page is up to n bytes of
// text plus the marker. offset at or past len(text) gives an empty page.
//
// An offset inside a multi-byte character moves forward to the next character
// start. Joining text[offset:next] for consecutive pages rebuilds text with no
// gap and no overlap; the marker is the only added text.
func Slice(text string, offset, n int) (page string, next int, more bool) {
	if offset < 0 {
		offset = 0
	}
	for offset < len(text) && !utf8.RuneStart(text[offset]) {
		offset++
	}
	if offset >= len(text) {
		return "", len(text), false
	}
	if n < 1 {
		n = 1
	}
	rest := text[offset:]
	if len(rest) <= n {
		return rest, len(text), false
	}
	end := cutPoint(rest, n)
	if end == 0 {
		// n is smaller than the first character: keep that character so paging
		// always moves forward.
		_, end = utf8.DecodeRuneInString(rest)
	}
	next = offset + end
	return rest[:end] + fmt.Sprintf("\n\n[content truncated, continue with offset=%d]", next), next, true
}

// cutPoint returns the byte index where to cut s (len(s) > maxLength) so the
// kept text is at most maxLength bytes. It prefers a paragraph, sentence, line,
// then word boundary in the second half of the window, and otherwise cuts on a
// character start (which can be 0 when maxLength is smaller than the first character).
func cutPoint(s string, maxLength int) int {
	window := s[:maxLength]

	if idx := strings.LastIndex(window, "\n\n"); idx > maxLength/2 {
		return idx
	}
	if idx := strings.LastIndex(window, ". "); idx > maxLength/2 {
		return idx + 1
	}
	if idx := strings.LastIndex(window, "\n"); idx > maxLength/2 {
		return idx
	}
	if idx := strings.LastIndex(window, " "); idx > maxLength/2 {
		return idx
	}

	end := maxLength
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return end
}

func EstimateTokens(content string) int {
	return len(content) / 4
}

func SizeCategory(length int) string {
	switch {
	case length < 5000:
		return "small"
	case length < 20000:
		return "medium"
	case length < 50000:
		return "large"
	default:
		return "very_large"
	}
}
