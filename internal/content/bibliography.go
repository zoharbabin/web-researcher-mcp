package content

import (
	"sort"
	"strconv"
	"strings"
)

// BibEntry is one source to be formatted into a bibliography. Only URL is
// strictly required; the richer the metadata, the more complete the citation.
// DOI is optional and, when present, is emitted into the formats that carry it
// (BibTeX doi field, RIS DO tag, CSL-JSON DOI) so a reference manager keeps the
// persistent identifier — the backbone of a verifiable citation.
type BibEntry struct {
	URL    string
	Title  string
	Author string
	Site   string
	Date   string
	DOI    string
}

// SupportedBibStyles lists the citation styles FormatBibliography understands.
// apa/mla/bibtex render human-readable citations; ris and csl-json are the
// machine-interchange formats reference managers ingest (Zotero/EndNote/Mendeley
// read RIS; the citation.js/CSL ecosystem reads CSL-JSON).
var SupportedBibStyles = []string{"apa", "mla", "bibtex", "ris", "csl-json"}

// FormatBibliography renders entries into a single bibliography string in the
// given style and returns it alongside the exact number of unique entries
// rendered. Supported styles: "apa"/"mla" (human-readable), "bibtex"/"ris"
// (reference-manager interchange), and "csl-json" (a JSON array). Entries are
// de-duplicated by URL (first occurrence wins) and ordered deterministically:
// APA/MLA alphabetically by the rendered line; BibTeX by (collision-free) cite
// key; RIS and CSL-JSON by the same cite key so the same inputs always produce
// byte-identical output (no timestamps — these formats omit the accessed date so
// they stay reproducible). An unrecognized style falls back to "apa". Entries
// with no URL are skipped. The returned count is authoritative (the caller must
// not re-derive it from the string, since a malformed title could contain a
// blank line and inflate a "\n\n"-based count).
func FormatBibliography(entries []BibEntry, style string) (string, int) {
	style = strings.ToLower(strings.TrimSpace(style))
	switch style {
	case "apa", "mla", "bibtex", "ris", "csl-json":
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

	switch style {
	case "bibtex":
		return formatBibTeXList(deduped), len(deduped)
	case "ris":
		return formatRISList(deduped), len(deduped)
	case "csl-json":
		return formatCSLJSONList(deduped), len(deduped)
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
	return strings.Join(lines, "\n\n"), len(deduped)
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
		entry := withBibTeXDOI(c.Formatted.BibTeX, e.DOI)
		// When a DOI is present the source is almost certainly a scholarly work;
		// upgrade the entry type from @misc to @article and use journal= instead
		// of howpublished= so reference managers classify it correctly.
		if e.DOI != "" {
			entry = strings.Replace(entry, "@misc{"+key+",", "@article{"+key+",", 1)
			entry = strings.Replace(entry, "  howpublished = {", "  journal = {", 1)
		}
		if n := used[key]; n > 0 {
			// Collision: suffix the key (and rewrite the entry's key line) so the
			// generated .bib has no duplicate identifiers. Suffixes run a,b,…,z then
			// fall back to numeric (aa1-style) so >26 collisions stay unique.
			suffixed := key + collisionSuffix(n)
			entryType := "@misc{"
			if e.DOI != "" {
				entryType = "@article{"
			}
			entry = strings.Replace(entry, entryType+key+",", entryType+suffixed+",", 1)
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

// withBibTeXDOI inserts a `doi = {…}` field into a rendered BibTeX entry before
// its closing brace, when a DOI is present. Kept here (not in formatBibTeX) so
// ExtractCitation's signature stays stable for its many other callers; the DOI
// only travels with a BibEntry.
func withBibTeXDOI(entry, doi string) string {
	doi = normalizeBibDOI(doi)
	if doi == "" {
		return entry
	}
	idx := strings.LastIndex(entry, "}")
	if idx < 0 {
		return entry
	}
	return entry[:idx] + "  doi = {" + bibtexEscape(doi) + "},\n" + entry[idx:]
}

// collisionSuffix returns the suffix for the n-th collision (n>=1): "a".."z" for
// the first 26, then "z2","z3",… so keys stay unique without rune overflow.
func collisionSuffix(n int) string {
	if n >= 1 && n <= 26 {
		return string(rune('a' + n - 1))
	}
	return "z" + strconv.Itoa(n-25)
}
