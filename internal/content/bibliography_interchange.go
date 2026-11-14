package content

import (
	"regexp"
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
// "id" is that key, suffixed on collision (a/b/…, same as formatBibTeXList) so
// two entries never share an id and silently shadow one another in a tool that
// indexes CSL-JSON by id (#531).
func formatCSLJSONList(entries []BibEntry) string {
	ordered := orderByCiteKey(entries)
	used := make(map[string]int, len(ordered))
	items := make([]string, 0, len(ordered))
	for _, e := range ordered {
		key := BibTeXKey(e.Author, e.Date, e.Title)
		if n := used[key]; n > 0 {
			used[key] = n + 1
			key += collisionSuffix(n)
		} else {
			used[key] = 1
		}
		items = append(items, formatCSLJSON(e, key))
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
// Values are JSON-escaped. id is the caller-resolved (collision-free) cite key,
// not recomputed here, so formatCSLJSONList's suffixing is the single source of
// truth for it (#531).
func formatCSLJSON(e BibEntry, id string) string {
	var fields []string
	add := func(s string) { fields = append(fields, "    "+s) }

	add(`"id": ` + jsonString(id))
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

// cslAuthors renders the CSL author array. Each name is split via
// splitPersonalName into {"family", "given"} — the CSL-standard shape a
// reference manager actually indexes authors by (#621). A name that can't be
// split (a single token — a bare surname or an organization) falls back to
// {"literal": …}, CSL's sanctioned way to give a name verbatim. Returns "" when
// there are no authors.
func cslAuthors(author string) string {
	names := splitAuthors(author)
	if len(names) == 0 {
		return ""
	}
	objs := make([]string, 0, len(names))
	for _, n := range names {
		family, given, isLiteral := splitPersonalName(n)
		if isLiteral {
			objs = append(objs, `{"literal": `+jsonString(n)+`}`)
			continue
		}
		obj := `{"family": ` + jsonString(family)
		if given != "" {
			obj += `, "given": ` + jsonString(given)
		}
		obj += `}`
		objs = append(objs, obj)
	}
	return "[" + strings.Join(objs, ", ") + "]"
}

// etAlTail matches a trailing "et al." that stands alone (not the end of a name).
var etAlTail = regexp.MustCompile(`(?i)(?:^|[\s,])et al\.?$`)

// SplitAuthorList splits a joined author string ("A B, C D, et al.") into one
// name per item. A trailing "et al." is dropped: it is not a name. Returns nil
// for an empty string.
func SplitAuthorList(author string) []string {
	author = strings.TrimSpace(author)
	if m := etAlTail.FindStringIndex(author); m != nil {
		author = strings.TrimRight(author[:m[0]], ", ")
	}
	return splitAuthors(author)
}

// splitAuthors splits a free-form author string into individual names on the
// delimiters bibliographies commonly use (";" and " and "), trimming blanks.
// A single "Smith, J." (one comma, no delimiter) stays one author. A bare
// comma-separated list of full names ("First Last, First Last", common in
// APA/MLA-style citations with no ";"/"and") is also detected and split
// (#639) via splitBareCommaNames.
func splitAuthors(author string) []string {
	author = strings.TrimSpace(author)
	if author == "" {
		return nil
	}
	parts := strings.Split(author, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, splitAndSegment(t)...)
		}
	}
	return out
}

// splitAndSegment splits one ";"-free segment on " and " (BibTeX-style). A
// segment that is an organization name as a whole keeps its internal "and"
// ("Department of Health and Human Services"), except where a person-like
// name (2+ words, not an organization) is followed by " and " and an
// organization ("Alicia Malone and U.S. Government Accountability Office"), or
// an organization ending in a corporate suffix is followed by a person
// ("Acme Inc and Bob Smith") (#848). A comma list inside an organization segment is not split.
func splitAndSegment(seg string) []string {
	const sep = " and "
	if !isOrganizationName(seg) {
		var out []string
		for _, p := range strings.Split(seg, sep) {
			if t := strings.TrimSpace(p); t != "" {
				out = append(out, splitBareCommaNames(t)...)
			}
		}
		return out
	}
	for from := 0; ; {
		i := strings.Index(seg[from:], sep)
		if i < 0 {
			return []string{seg}
		}
		i += from
		left, right := strings.TrimSpace(seg[:i]), strings.TrimSpace(seg[i+len(sep):])
		if left != "" && right != "" && len(strings.Fields(right)) >= 2 {
			// A person before an organization needs a long organization name
			// (3+ words) and no comma in the person part. Shorter splits cut
			// agencies such as "Occupational Safety and Health Administration".
			personThenOrg := len(strings.Fields(left)) >= 2 && !strings.Contains(left, ",") &&
				len(strings.Fields(right)) >= 3 && !isOrganizationName(left) && isOrganizationName(right)
			orgThenPerson := endsWithCorporateSuffix(left) && !isOrganizationName(right)
			if personThenOrg || orgThenPerson {
				return append(splitAndSegment(left), splitAndSegment(right)...)
			}
		}
		from = i + len(sep)
	}
}

// corporateSuffixes end a company name ("Acme Inc"). Only these words, not any
// organization keyword, mark where a company name stops.
var corporateSuffixes = map[string]bool{
	"inc": true, "ltd": true, "llc": true, "gmbh": true, "corp": true, "co": true,
}

// endsWithCorporateSuffix reports whether name's last word is a corporate
// suffix such as "Inc." or "Ltd".
func endsWithCorporateSuffix(name string) bool {
	tokens := strings.Fields(name)
	if len(tokens) < 2 {
		return false
	}
	last := strings.ToLower(strings.Trim(tokens[len(tokens)-1], ".,;:()"))
	return corporateSuffixes[last]
}

// splitBareCommaNames detects a bare comma-separated list of names within a
// single ";"/" and "-delimited segment and splits it into individual names.
// Two input shapes are handled:
//
//  1. Full names (#639): every comma-delimited segment itself looks like a
//     full name (2+ whitespace tokens) — e.g. "Yoshua Bengio, Aaron
//     Courville". Split on every comma.
//  2. Pre-inverted pairs (#680): every segment is a SINGLE token and the
//     segment count is even — e.g. "Smith, John, Doe, Jane" (segments
//     "Smith"|"John"|"Doe"|"Jane"). Adjacent segments are paired back into
//     "Family, Given" form: "Smith, John" and "Doe, Jane". This subsumes the
//     single-pre-inverted-author case ("Smith, J." -> 2 single-token
//     segments -> 1 pair -> 1 author), so no separate single-pair branch is
//     needed.
//
// Anything else — odd segment counts, or a mix of single- and multi-token
// segments (e.g. "de la Cruz, Maria") — is genuinely ambiguous and is
// returned unsplit, as a single pre-inverted "Family, Given" name.
func splitBareCommaNames(s string) []string {
	if !strings.Contains(s, ",") {
		return []string{s}
	}
	segments := strings.Split(s, ",")

	allMultiToken := true
	allSingleToken := true
	trimmed := make([]string, len(segments))
	for i, seg := range segments {
		t := strings.TrimSpace(seg)
		trimmed[i] = t
		switch len(strings.Fields(t)) {
		case 0:
			// An empty segment (stray/doubled comma) is neither a full name
			// nor a single-token pre-inverted half — disqualify both shapes
			// so this falls through to the safe unsplit fallback instead of
			// silently pairing unrelated names around the gap.
			allMultiToken = false
			allSingleToken = false
		case 1:
			allMultiToken = false
		default:
			allSingleToken = false
		}
	}

	if allMultiToken {
		return trimmed
	}

	if allSingleToken && len(trimmed)%2 == 0 {
		pairs := make([]string, 0, len(trimmed)/2)
		for i := 0; i < len(trimmed); i += 2 {
			pairs = append(pairs, trimmed[i]+", "+trimmed[i+1])
		}
		return pairs
	}

	return []string{s}
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
