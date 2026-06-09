package content

import (
	"encoding/json"
	"strings"
)

// This file is the inverse of bibliography_interchange.go (#168): it reads a
// bibliography document back into []BibEntry so the corpus can be audited
// (existence / retraction / dead-link) entry by entry. It parses the three
// machine-interchange formats the writers emit — CSL-JSON, RIS, and BibTeX —
// using only the stdlib (encoding/json + string scanning); no third-party
// bibliography library. Parsing is lenient: unknown fields/tags are ignored and
// a malformed record is skipped rather than failing the whole document, so a
// real-world export (which often carries extra fields) still audits cleanly.

// SupportedBibParseFormats lists the input formats ParseBibliography accepts.
// "auto" detects the format from the content. These are the machine formats the
// matching writers produce; the prose styles (apa/mla) are not round-trippable.
var SupportedBibParseFormats = []string{"auto", "csl-json", "ris", "bibtex"}

// ParseBibliography parses a bibliography document in the given format into
// entries. format "auto" (or "") sniffs the format from the content. It returns
// the parsed entries and the format it actually used. Lenient by design: it
// never returns an error — an unrecognizable document yields zero entries (the
// caller reports "nothing to audit"), and individual malformed records are
// skipped. Entries are returned in document order (no dedup here; the auditor
// dedups by its own key).
func ParseBibliography(doc, format string) ([]BibEntry, string) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" || format == "auto" {
		format = detectBibFormat(doc)
	}
	switch format {
	case "csl-json":
		return parseCSLJSON(doc), "csl-json"
	case "ris":
		return parseRIS(doc), "ris"
	case "bibtex":
		return parseBibTeX(doc), "bibtex"
	}
	return nil, format
}

// detectBibFormat sniffs the format from the document's shape: a leading [ or {
// is CSL-JSON; an "@type{" entry is BibTeX; "TY  - " (or any "XX  - " tag) is
// RIS. Falls back to "" (unknown) when nothing matches.
func detectBibFormat(doc string) string {
	t := strings.TrimSpace(doc)
	if t == "" {
		return ""
	}
	switch t[0] {
	case '[', '{':
		return "csl-json"
	case '@':
		return "bibtex"
	}
	if strings.Contains(t, "TY  - ") || risTagLine(t) {
		return "ris"
	}
	// A bare @ entry not at position 0 (leading comment) is still BibTeX.
	if strings.Contains(t, "@") && strings.Contains(t, "{") {
		return "bibtex"
	}
	return ""
}

// risTagLine reports whether the first non-empty line looks like a RIS tag
// ("XX  - …"): two chars, two spaces, a dash, a space.
func risTagLine(doc string) bool {
	for _, line := range strings.Split(doc, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		return len(line) >= 6 && line[2] == ' ' && line[3] == ' ' && line[4] == '-' && line[5] == ' '
	}
	return false
}

// ─────────────────────────────── CSL-JSON ──────────────────────────────────

// parseCSLJSON parses a CSL-JSON array (or a single object) into entries.
func parseCSLJSON(doc string) []BibEntry {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return nil
	}
	// Accept either a top-level array or a single item object.
	var items []cslItem
	if doc[0] == '{' {
		var one cslItem
		if json.Unmarshal([]byte(doc), &one) != nil {
			return nil
		}
		items = []cslItem{one}
	} else if json.Unmarshal([]byte(doc), &items) != nil {
		return nil
	}

	out := make([]BibEntry, 0, len(items))
	for _, it := range items {
		e := BibEntry{
			Title: it.Title,
			Site:  it.ContainerTitle,
			DOI:   normalizeBibDOI(it.DOI),
			URL:   firstNonEmpty(it.URL, doiURL(it.DOI)),
		}
		e.Author = joinCSLAuthors(it.Author)
		e.Date = it.Issued.year()
		if hasAnyBibField(e) {
			out = append(out, e)
		}
	}
	return out
}

type cslItem struct {
	Title          string    `json:"title"`
	ContainerTitle string    `json:"container-title"`
	DOI            string    `json:"DOI"`
	URL            string    `json:"URL"`
	Author         []cslName `json:"author"`
	Issued         cslDate   `json:"issued"`
}

type cslName struct {
	Family  string `json:"family"`
	Given   string `json:"given"`
	Literal string `json:"literal"`
}

func (n cslName) display() string {
	if n.Literal != "" {
		return n.Literal
	}
	switch {
	case n.Family != "" && n.Given != "":
		return n.Family + ", " + n.Given
	case n.Family != "":
		return n.Family
	default:
		return strings.TrimSpace(n.Given)
	}
}

func joinCSLAuthors(names []cslName) string {
	parts := make([]string, 0, len(names))
	for _, n := range names {
		if d := n.display(); d != "" {
			parts = append(parts, d)
		}
	}
	return strings.Join(parts, "; ")
}

// cslDate models the CSL "issued" field, whose date-parts is [[year, month?,
// day?]] (ints), tolerating string years too.
type cslDate struct {
	DateParts [][]json.Number `json:"date-parts"`
}

func (d cslDate) year() string {
	if len(d.DateParts) > 0 && len(d.DateParts[0]) > 0 {
		return d.DateParts[0][0].String()
	}
	return ""
}

// ────────────────────────────────── RIS ────────────────────────────────────

// parseRIS parses RIS records (tag lines "XX  - value", records delimited by
// "ER  - "). Repeated AU tags accumulate into a "; "-joined author string.
func parseRIS(doc string) []BibEntry {
	var out []BibEntry
	var cur BibEntry
	var authors []string
	started := false

	flush := func() {
		if started {
			cur.Author = strings.Join(authors, "; ")
			cur.DOI = normalizeBibDOI(cur.DOI)
			if cur.URL == "" {
				cur.URL = doiURL(cur.DOI)
			}
			if hasAnyBibField(cur) {
				out = append(out, cur)
			}
		}
		cur = BibEntry{}
		authors = nil
		started = false
	}

	for _, raw := range strings.Split(doc, "\n") {
		line := strings.TrimRight(raw, "\r")
		tag, val, ok := risParseLine(line)
		if !ok {
			continue
		}
		started = true
		switch tag {
		case "TY":
			// New record marker — but the writer never re-uses TY mid-record, so a
			// second TY without an intervening ER still starts fresh defensively.
			if cur.Title != "" || len(authors) > 0 || cur.URL != "" {
				flush()
				started = true
			}
		case "TI", "T1":
			cur.Title = val
		case "AU", "A1":
			if val != "" {
				authors = append(authors, val)
			}
		case "PY", "Y1", "DA":
			if cur.Date == "" {
				cur.Date = val
			}
		case "T2", "JO", "JF":
			if cur.Site == "" {
				cur.Site = val
			}
		case "DO":
			cur.DOI = val
		case "UR", "L1":
			if cur.URL == "" {
				cur.URL = val
			}
		case "ER":
			flush()
		}
	}
	flush() // trailing record without an explicit ER
	return out
}

// risParseLine splits a RIS line "XX  - value" into (tag, value). ok=false for
// blank/continuation lines that aren't tag lines.
func risParseLine(line string) (tag, val string, ok bool) {
	if len(line) < 6 || line[2] != ' ' || line[3] != ' ' || line[4] != '-' || line[5] != ' ' {
		return "", "", false
	}
	tag = strings.TrimSpace(line[:2])
	val = strings.TrimSpace(line[6:])
	if tag == "" {
		return "", "", false
	}
	return tag, val, true
}

// ───────────────────────────────── BibTeX ──────────────────────────────────

// parseBibTeX parses BibTeX entries (@type{key, field = {value}, …}). Brace- and
// quote-delimited values are both accepted; nested braces are balanced.
func parseBibTeX(doc string) []BibEntry {
	var out []BibEntry
	for _, raw := range splitBibTeXEntries(doc) {
		if e, ok := parseBibTeXEntry(raw); ok {
			out = append(out, e)
		}
	}
	return out
}

// splitBibTeXEntries returns each "@type{ … }" block, balancing braces so a
// value containing braces doesn't split the entry early.
func splitBibTeXEntries(doc string) []string {
	var entries []string
	i := 0
	for {
		at := strings.IndexByte(doc[i:], '@')
		if at < 0 {
			break
		}
		start := i + at
		open := strings.IndexByte(doc[start:], '{')
		if open < 0 {
			break
		}
		// Balance braces from the first '{'.
		depth := 0
		j := start + open
		for ; j < len(doc); j++ {
			switch doc[j] {
			case '{':
				depth++
			case '}':
				depth--
			}
			if depth == 0 {
				break
			}
		}
		if depth != 0 {
			break // unbalanced — stop
		}
		entries = append(entries, doc[start:j+1])
		i = j + 1
	}
	return entries
}

// parseBibTeXEntry extracts the citation-identity fields from one @type{…}
// block. The writer's `urldate` (access timestamp) is intentionally NOT read
// back — it isn't an identity field and keeping it out preserves determinism.
func parseBibTeXEntry(entry string) (BibEntry, bool) {
	open := strings.IndexByte(entry, '{')
	if open < 0 {
		return BibEntry{}, false
	}
	body := entry[open+1 : len(entry)-1] // strip the outer @type{ … }
	fields := parseBibTeXFields(body)

	e := BibEntry{
		Title:  bibtexUnescape(fields["title"]),
		Author: normalizeBibTeXAuthors(bibtexUnescape(fields["author"])),
		Site:   bibtexUnescape(firstNonEmpty(fields["journal"], fields["howpublished"], fields["booktitle"])),
		Date:   firstNonEmpty(fields["year"], fields["date"]),
		DOI:    normalizeBibDOI(bibtexUnescape(fields["doi"])),
		URL:    bibtexUnescape(fields["url"]),
	}
	if e.URL == "" {
		e.URL = doiURL(e.DOI)
	}
	if !hasAnyBibField(e) {
		return BibEntry{}, false
	}
	return e, true
}

// parseBibTeXFields scans "name = {value}" / "name = \"value\"" pairs from an
// entry body, balancing braces in values. The leading cite-key token (before the
// first comma) carries no '=', so it's naturally skipped.
func parseBibTeXFields(body string) map[string]string {
	fields := map[string]string{}
	i := 0
	n := len(body)
	for i < n {
		// Find a field name = the run of letters before '='.
		eq := strings.IndexByte(body[i:], '=')
		if eq < 0 {
			break
		}
		eqPos := i + eq
		name := strings.ToLower(strings.TrimSpace(lastToken(body[i:eqPos])))
		// Skip whitespace after '='.
		j := eqPos + 1
		for j < n && (body[j] == ' ' || body[j] == '\t' || body[j] == '\n' || body[j] == '\r') {
			j++
		}
		if j >= n {
			break
		}
		var val string
		switch body[j] {
		case '{':
			val, j = readBraced(body, j)
		case '"':
			val, j = readQuoted(body, j)
		default:
			val, j = readBare(body, j)
		}
		if name != "" {
			fields[name] = strings.TrimSpace(val)
		}
		// Advance past a trailing comma.
		for j < n && (body[j] == ',' || body[j] == ' ' || body[j] == '\t' || body[j] == '\n' || body[j] == '\r') {
			j++
		}
		i = j
	}
	return fields
}

// lastToken returns the last whitespace/comma-separated token in s (the field
// name immediately left of '=').
func lastToken(s string) string {
	s = strings.TrimRight(s, " \t\n\r")
	if idx := strings.LastIndexAny(s, " \t\n\r,"); idx >= 0 {
		return s[idx+1:]
	}
	return s
}

// readBraced reads a {…} value starting at body[start]=='{', balancing nesting,
// and returns the inner text and the index just past the closing brace.
func readBraced(body string, start int) (string, int) {
	depth := 0
	for j := start; j < len(body); j++ {
		switch body[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return body[start+1 : j], j + 1
			}
		}
	}
	return body[start+1:], len(body)
}

// readQuoted reads a "…" value starting at body[start]=='"'.
func readQuoted(body string, start int) (string, int) {
	for j := start + 1; j < len(body); j++ {
		if body[j] == '"' {
			return body[start+1 : j], j + 1
		}
	}
	return body[start+1:], len(body)
}

// readBare reads an unquoted value (a number/macro) up to the next comma or
// closing context.
func readBare(body string, start int) (string, int) {
	for j := start; j < len(body); j++ {
		if body[j] == ',' || body[j] == '\n' || body[j] == '}' {
			return body[start:j], j
		}
	}
	return body[start:], len(body)
}

// normalizeBibTeXAuthors turns BibTeX " and "-separated authors into the
// "; "-joined form the rest of the pipeline uses.
func normalizeBibTeXAuthors(author string) string {
	if author == "" {
		return ""
	}
	return strings.Join(splitAuthors(author), "; ")
}

// bibtexUnescape reverses bibtexEscape for the field values we read back.
func bibtexUnescape(s string) string {
	if s == "" {
		return ""
	}
	r := strings.NewReplacer(
		"\\&", "&", "\\%", "%", "\\$", "$", "\\#", "#",
		"\\{", "{", "\\}", "}", "\\\\", "\\",
	)
	return strings.TrimSpace(r.Replace(s))
}

// ────────────────────────────────── shared ─────────────────────────────────

// hasAnyBibField reports whether an entry carries at least one identifying field
// worth auditing (so empty/garbage records are dropped).
func hasAnyBibField(e BibEntry) bool {
	return e.Title != "" || e.URL != "" || e.DOI != "" || e.Author != ""
}

// doiURL builds the canonical resolver URL for a bare DOI, or "" when none.
func doiURL(doi string) string {
	d := normalizeBibDOI(doi)
	if d == "" {
		return ""
	}
	return "https://doi.org/" + d
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
