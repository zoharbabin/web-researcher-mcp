package content

import "strings"

// A paragraph can be removed as a repeat only when it is at least this long.
// Shorter repeats are data values (IDs, "N/A", "Active"), not boilerplate, and
// removing them leaves labels with no value next to them.
const (
	dedupMinBytes = 80
	dedupMinWords = 8
)

// Dedup drops repeated paragraphs, keeping the first copy. Only paragraphs of
// at least dedupMinBytes bytes and dedupMinWords words are eligible. Shorter
// paragraphs always pass through.
func Dedup(paragraphs []string) []string {
	seen := make(map[uint64]bool)
	var result []string

	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		if len(p) >= dedupMinBytes && WordCount(p) >= dedupMinWords {
			h := djb2(p)
			if seen[h] {
				continue
			}
			seen[h] = true
		}
		result = append(result, p)
	}
	return result
}

func DedupContent(content string) string {
	paragraphs := strings.Split(content, "\n\n")
	deduped := Dedup(paragraphs)
	return strings.Join(deduped, "\n\n")
}

func djb2(s string) uint64 {
	var hash uint64 = 5381
	for i := 0; i < len(s); i++ {
		hash = ((hash << 5) + hash) + uint64(s[i])
	}
	return hash
}
