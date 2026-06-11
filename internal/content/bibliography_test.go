package content

import (
	"strings"
	"testing"
)

func TestFormatBibTeXKeyAndYear(t *testing.T) {
	c := ExtractCitation("https://example.com/x", "Attention Is All You Need", "Vaswani, Ashish", "NeurIPS", "2017-06-12")
	if !strings.HasPrefix(c.Formatted.BibTeX, "@misc{vaswani2017attention,") {
		t.Errorf("unexpected cite key: %s", c.Formatted.BibTeX)
	}
	if !strings.Contains(c.Formatted.BibTeX, "year = {2017}") {
		t.Errorf("year not extracted: %s", c.Formatted.BibTeX)
	}
	if !strings.Contains(c.Formatted.BibTeX, "urldate = {") {
		t.Errorf("missing urldate: %s", c.Formatted.BibTeX)
	}
}

func TestBibtexYear(t *testing.T) {
	cases := map[string]string{
		"2017-06-12":    "2017",
		"June 2019":     "2019",
		"pp. 1990-1995": "1990", // page range: first 4-digit token is a real year here
		"12345":         "",     // 5-digit run, not a year (digit-flanked)
		"123456789":     "",     // long digit run, no bounded 4-token starting 1/2
		"no year here":  "",
		"":              "",
		"1999":          "1999",
	}
	for in, want := range cases {
		if got := bibtexYear(in); got != want {
			t.Errorf("bibtexYear(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeBibTeXAuthor(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Smith, J.", "Smith, J."},                                           // single author, no change
		{"Smith, J.; Doe, A.", "Smith, J. and Doe, A."},                      // semicolon → " and "
		{"Smith, J.; Doe, A.; Lee, B.", "Smith, J. and Doe, A. and Lee, B."}, // three authors
		{"Smith, J. and Doe, A.", "Smith, J. and Doe, A."},                   // already " and ", no double-and
		{" Smith, J. ; Doe, A. ", "Smith, J. and Doe, A."},                   // whitespace trimmed
		{"", ""}, // empty passthrough
	}
	for _, c := range cases {
		got := normalizeBibTeXAuthor(c.in)
		if got != c.want {
			t.Errorf("normalizeBibTeXAuthor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestBibTeXAuthorSeparator guards the live bug: semicolon-separated authors must
// produce valid BibTeX " and "-separated output, not a raw semicolon.
func TestBibTeXAuthorSeparator(t *testing.T) {
	c := ExtractCitation("https://example.com/x", "Paper", "Smith, J.; Doe, A.; Lee, B.", "Journal", "2020")
	if strings.Contains(c.Formatted.BibTeX, ";") {
		t.Errorf("BibTeX author field must not contain semicolons: %s", c.Formatted.BibTeX)
	}
	if !strings.Contains(c.Formatted.BibTeX, "Smith, J. and Doe, A. and Lee, B.") {
		t.Errorf("BibTeX author field must use ' and ' separator: %s", c.Formatted.BibTeX)
	}
}

func TestBibTeXKeyFallback(t *testing.T) {
	if got := BibTeXKey("", "", ""); got != "anon" {
		t.Errorf("empty fallback = %q, want anon", got)
	}
	if got := BibTeXKey("", "2020", "Deep Learning"); got != "anon2020deep" {
		t.Errorf("anon key = %q", got)
	}
}

func TestBibTeXEscape(t *testing.T) {
	c := ExtractCitation("https://example.com/x", "Cost is 50% & {rising}", "Doe, J.", "Site", "2020")
	if strings.Contains(c.Formatted.BibTeX, "50% ") || !strings.Contains(c.Formatted.BibTeX, "\\%") {
		t.Errorf("percent not escaped: %s", c.Formatted.BibTeX)
	}
	if !strings.Contains(c.Formatted.BibTeX, "\\&") {
		t.Errorf("ampersand not escaped: %s", c.Formatted.BibTeX)
	}
	if !strings.Contains(c.Formatted.BibTeX, "\\{rising\\}") {
		t.Errorf("braces not escaped: %s", c.Formatted.BibTeX)
	}
}

// TestBibTeXURLEscaped guards against field break-out via a malicious source URL
// (a `}` in the URL must not close the url={…} field). Regression for the audit
// finding that url was emitted unescaped.
func TestBibTeXURLEscaped(t *testing.T) {
	evil := "http://x/}\ninjected = {pwned"
	c := ExtractCitation(evil, "Title", "Doe, J.", "Site", "2020")
	if strings.Contains(c.Formatted.BibTeX, "}\ninjected = {") {
		t.Errorf("URL was not escaped — field break-out possible:\n%s", c.Formatted.BibTeX)
	}
	if !strings.Contains(c.Formatted.BibTeX, "\\}") {
		t.Errorf("expected escaped brace in url field:\n%s", c.Formatted.BibTeX)
	}
}

func TestFormatBibliographyDedupAndOrder(t *testing.T) {
	entries := []BibEntry{
		{URL: "https://example.com/b", Title: "Beta", Author: "Zeta, Z.", Date: "2020"},
		{URL: "https://example.com/a", Title: "Alpha", Author: "Adams, A.", Date: "2019"},
		{URL: "https://example.com/b", Title: "Beta dup", Author: "Zeta, Z.", Date: "2020"},
	}
	out, n := FormatBibliography(entries, "apa")
	if n != 2 {
		t.Errorf("expected 2 unique entries after dedup, got count %d", n)
	}
	if strings.Count(out, "\n\n") != 1 {
		t.Errorf("expected 2 entries (1 blank-line separator) after dedup:\n%s", out)
	}
	// APA sorts alphabetically by rendered line; "Adams" precedes "Zeta".
	if strings.Index(out, "Adams") > strings.Index(out, "Zeta") {
		t.Errorf("entries not alphabetically ordered:\n%s", out)
	}
}

// TestFormatBibliographyCountIgnoresEmbeddedBlankLines guards the count fix: a
// title containing a blank line must NOT inflate the returned entry count (which
// a naive strings.Count("\n\n") would do).
func TestFormatBibliographyCountWithBlankLineInTitle(t *testing.T) {
	entries := []BibEntry{
		{URL: "https://example.com/a", Title: "Line one\n\nLine two", Author: "Doe, J.", Date: "2020"},
		{URL: "https://example.com/b", Title: "Normal", Author: "Roe, R.", Date: "2021"},
	}
	_, n := FormatBibliography(entries, "apa")
	if n != 2 {
		t.Errorf("count must reflect unique entries (2), not blank-line separators, got %d", n)
	}
}

func TestFormatBibliographyBibTeXCollisionKeys(t *testing.T) {
	// Same author+year+first-title-word but different URLs → unique cite keys.
	entries := []BibEntry{
		{URL: "https://example.com/1", Title: "Learning models", Author: "Smith, A.", Date: "2020"},
		{URL: "https://example.com/2", Title: "Learning systems", Author: "Smith, A.", Date: "2020"},
	}
	out, _ := FormatBibliography(entries, "bibtex")
	if !strings.Contains(out, "@misc{smith2020learning,") {
		t.Errorf("base key missing:\n%s", out)
	}
	if !strings.Contains(out, "@misc{smith2020learninga,") {
		t.Errorf("collision-suffixed key missing:\n%s", out)
	}
}

func TestFormatBibliographyUnknownStyleFallsBackToAPA(t *testing.T) {
	entries := []BibEntry{{URL: "https://example.com/a", Title: "X", Author: "Doe, J.", Date: "2020"}}
	out, _ := FormatBibliography(entries, "chicago")
	if !strings.Contains(out, "Retrieved") { // APA marker
		t.Errorf("unknown style should fall back to APA:\n%s", out)
	}
}

func TestFormatBibliographySkipsNoURL(t *testing.T) {
	entries := []BibEntry{{Title: "No URL"}, {URL: "https://example.com/a", Title: "Has URL"}}
	out, _ := FormatBibliography(entries, "apa")
	if strings.Contains(out, "No URL") {
		t.Errorf("entry without URL should be skipped:\n%s", out)
	}
}
