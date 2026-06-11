package content

import (
	"fmt"
	"strings"
	"time"
)

type Citation struct {
	URL          string           `json:"url"`
	AccessedDate string           `json:"accessedDate"`
	Metadata     CitationMetadata `json:"metadata"`
	Formatted    CitationFormats  `json:"formatted"`
}

type CitationMetadata struct {
	Title  string `json:"title,omitempty"`
	Author string `json:"author,omitempty"`
	Site   string `json:"site,omitempty"`
	Date   string `json:"date,omitempty"`
}

type CitationFormats struct {
	APA    string `json:"apa"`
	MLA    string `json:"mla"`
	BibTeX string `json:"bibtex"`
}

func ExtractCitation(url, title, author, siteName, pubDate string) Citation {
	accessed := time.Now().Format("2006-01-02")

	c := Citation{
		URL:          url,
		AccessedDate: accessed,
		Metadata: CitationMetadata{
			Title:  title,
			Author: author,
			Site:   siteName,
			Date:   pubDate,
		},
	}

	c.Formatted = CitationFormats{
		APA:    formatAPA(title, author, siteName, pubDate, url, accessed),
		MLA:    formatMLA(title, author, siteName, pubDate, url, accessed),
		BibTeX: formatBibTeX(title, author, siteName, pubDate, url, accessed),
	}

	return c
}

// ensureTerminalPeriod appends a period unless the string already ends with one,
// so an author name ending in an initial ("Hassabis, D.") doesn't become a
// doubled period ("Hassabis, D..") in APA/MLA output.
func ensureTerminalPeriod(s string) string {
	if strings.HasSuffix(s, ".") {
		return s
	}
	return s + "."
}

func formatAPA(title, author, site, date, url, accessed string) string {
	parts := []string{}

	if author != "" {
		// Avoid a doubled period when the author string already ends in one
		// (e.g. an initial like "Hassabis, D." or "Gordon, L. I.").
		parts = append(parts, ensureTerminalPeriod(author))
	}

	if date != "" {
		parts = append(parts, fmt.Sprintf("(%s).", date))
	} else {
		parts = append(parts, "(n.d.).")
	}

	if title != "" {
		parts = append(parts, title+".")
	}

	if site != "" {
		parts = append(parts, site+".")
	}

	parts = append(parts, fmt.Sprintf("Retrieved %s, from %s", accessed, url))

	return strings.Join(parts, " ")
}

func formatMLA(title, author, site, date, url, accessed string) string {
	parts := []string{}

	if author != "" {
		parts = append(parts, ensureTerminalPeriod(author))
	}

	if title != "" {
		parts = append(parts, fmt.Sprintf("\"%s.\"", title))
	}

	if site != "" {
		parts = append(parts, site+",")
	}

	if date != "" {
		parts = append(parts, date+",")
	}

	parts = append(parts, url+".")
	parts = append(parts, fmt.Sprintf("Accessed %s.", accessed))

	return strings.Join(parts, " ")
}

// formatBibTeX renders a BibTeX @misc/@article entry. A web source becomes @misc
// (with urldate); an entry with an author + year is still @misc since we cannot
// reliably tell journal articles from web pages here — callers needing @article
// can post-edit. The cite key is BibTeXKey(author,date,title). Special BibTeX
// characters are minimally escaped.
func formatBibTeX(title, author, site, date, url, accessed string) string {
	key := BibTeXKey(author, date, title)
	year := bibtexYear(date)

	var b strings.Builder
	fmt.Fprintf(&b, "@misc{%s,\n", key)
	if author != "" {
		fmt.Fprintf(&b, "  author = {%s},\n", bibtexEscape(normalizeBibTeXAuthor(author)))
	}
	if title != "" {
		fmt.Fprintf(&b, "  title = {%s},\n", bibtexEscape(title))
	}
	if site != "" {
		fmt.Fprintf(&b, "  howpublished = {%s},\n", bibtexEscape(site))
	}
	if year != "" {
		fmt.Fprintf(&b, "  year = {%s},\n", year)
	}
	if url != "" {
		fmt.Fprintf(&b, "  url = {%s},\n", bibtexEscape(url))
	}
	if accessed != "" {
		fmt.Fprintf(&b, "  urldate = {%s},\n", accessed)
	}
	b.WriteString("}")
	return b.String()
}

// BibTeXKey builds a deterministic, collision-resistant citation key from the
// first author surname (or host), the year, and the first significant title
// word — e.g. "smith2024attention". Exported so the bibliography tool can
// de-duplicate and order entries consistently.
func BibTeXKey(author, date, title string) string {
	surname := firstAlnumToken(author)
	if surname == "" {
		surname = "anon"
	}
	year := bibtexYear(date)
	titleWord := firstAlnumToken(title)
	key := strings.ToLower(surname + year + titleWord)
	if key == "" {
		return "ref"
	}
	return key
}

// firstAlnumToken returns the first run of letters/digits in s (lowercased by
// callers as needed), skipping commas/initials — e.g. "Smith, J." → "Smith".
func firstAlnumToken(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			break
		}
	}
	return b.String()
}

// bibtexYear extracts a 4-digit year (starting 1 or 2) from a free-form date
// string, or "". The run must be digit-bounded — not flanked by another digit —
// so a page range ("pp. 1990-1995" → "1990" is fine, but "12345" or a DOI's
// "10.1234" do not false-match a 4-of-5+ digit substring).
func bibtexYear(date string) string {
	for i := 0; i+4 <= len(date); i++ {
		if i > 0 && date[i-1] >= '0' && date[i-1] <= '9' {
			continue // left-flanked by a digit → part of a longer number
		}
		if i+4 < len(date) && date[i+4] >= '0' && date[i+4] <= '9' {
			continue // right-flanked by a digit → part of a longer number
		}
		sub := date[i : i+4]
		if sub[0] >= '1' && sub[0] <= '2' && allDigits(sub) {
			return sub
		}
	}
	return ""
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

// normalizeBibTeXAuthor converts semicolon-separated author lists to the
// BibTeX " and "-separated form. BibTeX requires " and " as the author
// separator (e.g. "Smith, J. and Doe, A."); semicolons are accepted at the
// input boundary (bibliography tool, format_bibliography) but are invalid
// BibTeX syntax.
func normalizeBibTeXAuthor(author string) string {
	parts := strings.Split(author, ";")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return strings.Join(parts, " and ")
}

// bibtexEscape escapes the BibTeX-significant characters so a value can't break
// out of its {…} field.
func bibtexEscape(s string) string {
	r := strings.NewReplacer("{", "\\{", "}", "\\}", "\\", "\\\\", "$", "\\$", "%", "\\%", "&", "\\&", "#", "\\#")
	return r.Replace(s)
}
