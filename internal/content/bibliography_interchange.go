package content

import (
	"sort"
	"strings"
)

// This file adds the reference-manager interchange formats (#163): RIS (the tag
// format Zotero/EndNote/Mendeley import) and CSL-JSON (the citation.js / CSL
// ecosystem format). Both are deterministic — same inputs → byte-identical bytes
// — so they omit the accessed-date stamp and order by the same collision-free
// cite key the BibTeX list uses. They carry the DOI when present so a downstream
// manager keeps the persistent identifier.

// formatRISList renders entries as a single RIS document. Records are ordered by
// cite key for determinism and separated by a blank line. Entries with a DOI are
// typed TY = JOUR; others fall back to TY = ELEC. ER terminates each record.
func formatRISList(entries []BibEntry) string {
	ordered := orderByCiteKey(entries)
	records := make([]string, 0, len(ordered))
	for _, e := range ordered {
		records = append(records, formatRIS(e))
	}
	return strings.Join(records, "\n\n")
}

// formatRIS renders one RIS record. RIS is line-oriented: each line is a 2-letter
// tag, "  - ", then the value. TY must be first and ER last. When a DOI is
// present the source is almost certainly a journal article (TY = JOUR); otherwise
// we fall back to ELEC (electronic/web) for web-discovered sources.
func formatRIS(e BibEntry) string {
	var b strings.Builder
	if e.DOI != "" {
		b.WriteString("TY  - JOUR\n")
	} else {
		b.WriteString("TY  - ELEC\n")
	}
	if e.Title != "" {
		b.WriteString("TI  - " + risValue(e.Title) + "\n")
	}
	// AU is repeated per author; split on common delimiters so "Smith, J.; Doe, A."
	// becomes two AU lines (RIS readers key on repeated AU tags).
	for _, a := range splitAuthors(e.Author) {
		b.WriteString("AU  - " + risValue(a) + "\n")
	}
	if year := bibtexYear(e.Date); year != "" {
		b.WriteString("PY  - " + year + "\n")
	}
	if e.Date != "" {
		b.WriteString("DA  - " + risValue(e.Date) + "\n")
	}
	if e.Site != "" {
		// T2 (secondary title) is where reference managers read the journal/site.
		b.WriteString("T2  - " + risValue(e.Site) + "\n")
	}
	if e.DOI != "" {
		b.WriteString("DO  - " + risValue(normalizeBibDOI(e.DOI)) + "\n")
	}
	if e.URL != "" {
		b.WriteString("UR  - " + risValue(e.URL) + "\n")
	}
	b.WriteString("ER  - ")
	return b.String()
}

// risValue strips line breaks from a value so it can't inject extra RIS tag lines
// (a newline in a title would otherwise break the record's line structure). It
// neutralizes ASCII breaks AND the Unicode line terminators (NEL U+0085, LS
// U+2028, PS U+2029) plus form-feed/vertical-tab, since some non-ASCII-aware RIS
// readers treat those as line boundaries too — defense in depth against tag
// injection regardless of the consuming parser.
func risValue(s string) string {
	r := strings.NewReplacer(
		"\r\n", " ", "\n", " ", "\r", " ",
		"\f", " ", "\v", " ",
		"\u0085", " ", "\u2028", " ", "\u2029", " ",
	)
	return strings.TrimSpace(r.Replace(s))
}

// formatCSLJSONList renders entries as a CSL-JSON array (the format citation.js,
// Zotero's "Better CSL JSON", and pandoc consume). Ordered by cite key; the
// "id" is that key so the array is stable and each item is addressable.
func formatCSLJSONList(entries []BibEntry) string {
	ordered := orderByCiteKey(entries)
	items := make([]string, 0, len(ordered))
	for _, e := range ordered {
		items = append(items, formatCSLJSON(e))
	}
	if len(items) == 0 {
		return "[]"
	}
	return "[\n" + strings.Join(items, ",\n") + "\n]"
}

// formatCSLJSON renders one CSL-JSON item object (indented two spaces to sit in
// the array). Fields follow the CSL schema: type "webpage" for generic web
// sources or "article-journal" when a DOI is present (DOIs are primarily assigned
// to scholarly works), a title, an author array of {family,given|literal}, an
// "issued" date-parts year, container-title for the site/journal, DOI, and URL.
// Values are JSON-escaped.
func formatCSLJSON(e BibEntry) string {
	var fields []string
	add := func(s string) { fields = append(fields, "    "+s) }

	add(`"id": ` + jsonString(BibTeXKey(e.Author, e.Date, e.Title)))
	if e.DOI != "" {
		add(`"type": "article-journal"`)
	} else {
		add(`"type": "webpage"`)
	}
	if e.Title != "" {
		add(`"title": ` + jsonString(e.Title))
	}
	if authors := cslAuthors(e.Author); authors != "" {
		add(`"author": ` + authors)
	}
	if year := bibtexYear(e.Date); year != "" {
		add(`"issued": {"date-parts": [[` + year + `]]}`)
	}
	if e.Site != "" {
		add(`"container-title": ` + jsonString(e.Site))
	}
	if e.DOI != "" {
		add(`"DOI": ` + jsonString(normalizeBibDOI(e.DOI)))
	}
	if e.URL != "" {
		add(`"URL": ` + jsonString(e.URL))
	}
	return "  {\n" + strings.Join(fields, ",\n") + "\n  }"
}

// cslAuthors renders the CSL author array. Each author becomes a {"literal": …}
// object — we don't reliably know the family/given split for web sources, and
// "literal" is the CSL-sanctioned way to give a name verbatim. Returns "" when
// there are no authors.
func cslAuthors(author string) string {
	names := splitAuthors(author)
	if len(names) == 0 {
		return ""
	}
	objs := make([]string, 0, len(names))
	for _, n := range names {
		objs = append(objs, `{"literal": `+jsonString(n)+`}`)
	}
	return "[" + strings.Join(objs, ", ") + "]"
}

// splitAuthors splits a free-form author string into individual names on the
// delimiters bibliographies commonly use (";" and " and "), trimming blanks.
// A single "Smith, J." (one comma, no delimiter) stays one author.
func splitAuthors(author string) []string {
	author = strings.TrimSpace(author)
	if author == "" {
		return nil
	}
	// Normalize " and " (BibTeX-style) to ";" then split.
	normalized := strings.ReplaceAll(author, " and ", ";")
	parts := strings.Split(normalized, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// normalizeBibDOI strips a doi.org URL prefix so the DOI field carries the bare
// identifier (10.xxxx/yyyy), which is what RIS DO and CSL DOI expect.
func normalizeBibDOI(doi string) string {
	d := strings.TrimSpace(doi)
	for _, p := range []string{"https://doi.org/", "http://doi.org/", "https://dx.doi.org/", "http://dx.doi.org/", "doi:"} {
		if strings.HasPrefix(strings.ToLower(d), p) {
			return d[len(p):]
		}
	}
	return d
}

// jsonString returns a minimally-escaped JSON string literal for a value. Used
// instead of json.Marshal so the interchange formatters have no marshaling
// surprises (HTML escaping of &, <, >) and stay byte-deterministic.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			if r < 0x20 {
				// Control characters must be \u-escaped to stay valid JSON.
				const hex = "0123456789abcdef"
				b.WriteString("\\u00")
				b.WriteByte(hex[r>>4])
				b.WriteByte(hex[r&0xf])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// orderByCiteKey returns entries sorted by their collision-free cite key — the
// same ordering the BibTeX list uses — so all interchange formats agree on order
// and the output is deterministic. The cite key suffixing (a/b/… on collision)
// only matters for BibTeX identifiers, so here we order by the base key then by
// URL to break ties stably.
func orderByCiteKey(entries []BibEntry) []BibEntry {
	ordered := make([]BibEntry, len(entries))
	copy(ordered, entries)
	sort.SliceStable(ordered, func(i, j int) bool {
		ki := BibTeXKey(ordered[i].Author, ordered[i].Date, ordered[i].Title)
		kj := BibTeXKey(ordered[j].Author, ordered[j].Date, ordered[j].Title)
		if ki != kj {
			return ki < kj
		}
		return ordered[i].URL < ordered[j].URL
	})
	return ordered
}
