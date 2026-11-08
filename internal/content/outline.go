package content

import "strings"

// OutlineEntry is one section heading of a cleaned page. Offset is the byte
// position of the heading line in the text, so a scrape_page call with that
// offset starts at the section.
type OutlineEntry struct {
	Heading string `json:"heading"`
	Level   int    `json:"level"`
	Offset  int    `json:"offset"`
}

const (
	// MinOutlineHeadings is the fewest headings worth reporting. Below it a
	// page has no structure a reader could jump through.
	MinOutlineHeadings = 3
	// MaxOutlineEntries caps the outline so it stays a small part of the result.
	MaxOutlineEntries = 60
	// maxOutlineHeadingLen caps one heading so a long one cannot fill the budget.
	maxOutlineHeadingLen = 120
)

// Outline lists the Markdown (ATX) headings of text with their byte offsets,
// skipping lines inside fenced code blocks. It returns nil when the page has
// fewer than MinOutlineHeadings headings. Past MaxOutlineEntries it keeps the
// shallowest levels first, so the top of the structure always fits.
func Outline(text string) []OutlineEntry {
	var all []OutlineEntry
	inFence := false
	for pos := 0; pos < len(text); {
		end := strings.IndexByte(text[pos:], '\n')
		line := text[pos:]
		next := len(text)
		if end >= 0 {
			line = text[pos : pos+end]
			next = pos + end + 1
		}
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			inFence = !inFence
		} else if !inFence {
			if e, ok := parseHeading(line, pos); ok {
				all = append(all, e)
			}
		}
		pos = next
	}
	if len(all) < MinOutlineHeadings {
		return nil
	}
	if len(all) <= MaxOutlineEntries {
		return all
	}
	// Drop the deepest level until the list fits, keeping document order.
	for level := 6; level >= 1 && len(all) > MaxOutlineEntries; level-- {
		kept := all[:0:0]
		for _, e := range all {
			if e.Level < level {
				kept = append(kept, e)
			}
		}
		if len(kept) >= MinOutlineHeadings {
			all = kept
		}
	}
	if len(all) > MaxOutlineEntries {
		all = all[:MaxOutlineEntries]
	}
	return all
}

// parseHeading reads "# Title" to "###### Title".
func parseHeading(line string, offset int) (OutlineEntry, bool) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || len(line) <= level || line[level] != ' ' {
		return OutlineEntry{}, false
	}
	title := strings.TrimSpace(line[level+1:])
	if title == "" {
		return OutlineEntry{}, false
	}
	if len(title) > maxOutlineHeadingLen {
		title = strings.ToValidUTF8(title[:maxOutlineHeadingLen], "") + "..."
	}
	return OutlineEntry{Heading: title, Level: level, Offset: offset}, true
}
