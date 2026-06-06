package content

import (
	"sort"
	"strconv"
	"strings"
)

// BibEntry is one source to be formatted into a bibliography. Only URL is
// strictly required; the richer the metadata, the more complete the citation.
type BibEntry struct {
	URL    string
	Title  string
	Author string
	Site   string
	Date   string
}

// SupportedBibStyles lists the citation styles FormatBibliography understands.
var SupportedBibStyles = []string{"apa", "mla", "bibtex"}

// FormatBibliography renders entries into a single bibliography string in the
// given style ("apa", "mla", or "bibtex"). Entries are de-duplicated by URL
// (first occurrence wins), each is formatted via ExtractCitation, and the list
// is ordered deterministically: APA/MLA alphabetically by the rendered line,
// BibTeX by cite key. BibTeX cite keys are made unique within the list by
// appending a/b/c… on collision so the output compiles. An unrecognized style
// falls back to "apa". Entries with no URL are skipped.
func FormatBibliography(entries []BibEntry, style string) string {
	style = strings.ToLower(strings.TrimSpace(style))
	switch style {
	case "apa", "mla", "bibtex":
	default:
		style = "apa"
	}

	seen := make(map[string]bool, len(entries))
	deduped := make([]BibEntry, 0, len(entries))
	for _, e := range entries {
		url := strings.TrimSpace(e.URL)
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		deduped = append(deduped, e)
	}

	if style == "bibtex" {
		return formatBibTeXList(deduped)
	}

	lines := make([]string, 0, len(deduped))
	for _, e := range deduped {
		c := ExtractCitation(e.URL, e.Title, e.Author, e.Site, e.Date)
		if style == "mla" {
			lines = append(lines, c.Formatted.MLA)
		} else {
			lines = append(lines, c.Formatted.APA)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n\n")
}

// formatBibTeXList renders entries as a BibTeX bibliography with collision-free
// cite keys, ordered by key.
func formatBibTeXList(entries []BibEntry) string {
	type rendered struct {
		key   string
		entry string
	}
	used := make(map[string]int, len(entries))
	out := make([]rendered, 0, len(entries))

	for _, e := range entries {
		c := ExtractCitation(e.URL, e.Title, e.Author, e.Site, e.Date)
		key := BibTeXKey(e.Author, e.Date, e.Title)
		entry := c.Formatted.BibTeX
		if n := used[key]; n > 0 {
			// Collision: suffix the key (and rewrite the entry's key line) so the
			// generated .bib has no duplicate identifiers. Suffixes run a,b,…,z then
			// fall back to numeric (aa1-style) so >26 collisions stay unique.
			suffixed := key + collisionSuffix(n)
			entry = strings.Replace(entry, "@misc{"+key+",", "@misc{"+suffixed+",", 1)
			used[key] = n + 1
			out = append(out, rendered{key: suffixed, entry: entry})
		} else {
			used[key] = 1
			out = append(out, rendered{key: key, entry: entry})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	lines := make([]string, len(out))
	for i, r := range out {
		lines[i] = r.entry
	}
	return strings.Join(lines, "\n\n")
}

// collisionSuffix returns the suffix for the n-th collision (n>=1): "a".."z" for
// the first 26, then "z2","z3",… so keys stay unique without rune overflow.
func collisionSuffix(n int) string {
	if n >= 1 && n <= 26 {
		return string(rune('a' + n - 1))
	}
	return "z" + strconv.Itoa(n-25)
}
