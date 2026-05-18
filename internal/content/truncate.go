package content

import "strings"

func Truncate(content string, maxLength int) (string, bool) {
	if len(content) <= maxLength {
		return content, false
	}

	truncated := content[:maxLength]

	// Try to break at paragraph boundary
	if idx := strings.LastIndex(truncated, "\n\n"); idx > maxLength/2 {
		return truncated[:idx] + "\n\n[content truncated]", true
	}

	// Try sentence boundary
	if idx := strings.LastIndex(truncated, ". "); idx > maxLength/2 {
		return truncated[:idx+1] + "\n\n[content truncated]", true
	}

	// Try newline
	if idx := strings.LastIndex(truncated, "\n"); idx > maxLength/2 {
		return truncated[:idx] + "\n\n[content truncated]", true
	}

	// Hard cut at word boundary
	if idx := strings.LastIndex(truncated, " "); idx > maxLength/2 {
		return truncated[:idx] + "\n\n[content truncated]", true
	}

	return truncated + "\n\n[content truncated]", true
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
