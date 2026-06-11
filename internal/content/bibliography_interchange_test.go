package content

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFormatRISBasic(t *testing.T) {
	// Entry has a DOI → expect TY = JOUR (journal article).
	entries := []BibEntry{
		{URL: "https://example.com/a", Title: "Attention Is All You Need", Author: "Vaswani, Ashish; Shazeer, Noam", Site: "NeurIPS", Date: "2017", DOI: "10.5555/3295222"},
	}
	out, n := FormatBibliography(entries, "ris")
	if n != 1 {
		t.Fatalf("entry count = %d, want 1", n)
	}
	for _, want := range []string{"TY  - JOUR", "TI  - Attention Is All You Need", "AU  - Vaswani, Ashish", "AU  - Shazeer, Noam", "PY  - 2017", "T2  - NeurIPS", "DO  - 10.5555/3295222", "UR  - https://example.com/a", "ER  - "} {
		if !strings.Contains(out, want) {
			t.Errorf("RIS missing %q:\n%s", want, out)
		}
	}
	// TY must be first, ER last (RIS record structure).
	if !strings.HasPrefix(out, "TY  - ") {
		t.Errorf("RIS must start with TY:\n%s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "ER  -") {
		t.Errorf("RIS record must end with ER:\n%s", out)
	}
}

func TestFormatRISDOINormalized(t *testing.T) {
	entries := []BibEntry{{URL: "https://x/a", Title: "T", DOI: "https://doi.org/10.1038/nature12373"}}
	out, _ := FormatBibliography(entries, "ris")
	if !strings.Contains(out, "DO  - 10.1038/nature12373") {
		t.Errorf("DOI URL prefix should be stripped to bare DOI:\n%s", out)
	}
	if strings.Contains(out, "doi.org") {
		t.Errorf("RIS DO should carry the bare DOI, not the URL:\n%s", out)
	}
}

func TestFormatRISInjectionSafe(t *testing.T) {
	// A newline in the title must not break the RIS line structure (inject a tag).
	entries := []BibEntry{{URL: "https://x/a", Title: "Evil\nAB  - injected"}}
	out, _ := FormatBibliography(entries, "ris")
	if strings.Contains(out, "\nAB  - injected") {
		t.Errorf("title newline must be stripped to prevent tag injection:\n%s", out)
	}
}

func TestFormatRISStripsUnicodeLineTerminators(t *testing.T) {
	// Defense in depth: NEL/LS/PS and form-feed/vertical-tab must also be
	// neutralized so a Unicode-aware RIS reader can't see an injected tag line.
	for _, sep := range []string{"\u0085", "\u2028", "\u2029", "\f", "\v"} {
		entries := []BibEntry{{URL: "https://x/a", Title: "Evil" + sep + "AB  - injected"}}
		out, _ := FormatBibliography(entries, "ris")
		if strings.Contains(out, sep) {
			t.Errorf("separator %q must be stripped from RIS values:\n%q", sep, out)
		}
	}
}

func TestFormatCSLJSONValidAndComplete(t *testing.T) {
	entries := []BibEntry{
		{URL: "https://example.com/a", Title: "Deep Learning", Author: "LeCun, Yann; Bengio, Yoshua", Site: "Nature", Date: "2015", DOI: "10.1038/nature14539"},
	}
	out, n := FormatBibliography(entries, "csl-json")
	if n != 1 {
		t.Fatalf("entry count = %d, want 1", n)
	}
	// Must be a valid JSON array of CSL items.
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("CSL-JSON is not valid JSON: %v\n%s", err, out)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 CSL item, got %d", len(items))
	}
	it := items[0]
	// Entry has a DOI → expect article-journal.
	if it["type"] != "article-journal" {
		t.Errorf("type = %v, want article-journal", it["type"])
	}
	if it["title"] != "Deep Learning" {
		t.Errorf("title = %v", it["title"])
	}
	if it["DOI"] != "10.1038/nature14539" {
		t.Errorf("DOI = %v", it["DOI"])
	}
	if it["container-title"] != "Nature" {
		t.Errorf("container-title = %v", it["container-title"])
	}
	authors, ok := it["author"].([]any)
	if !ok || len(authors) != 2 {
		t.Fatalf("expected 2 authors, got %v", it["author"])
	}
	first, _ := authors[0].(map[string]any)
	if first["literal"] != "LeCun, Yann" {
		t.Errorf("first author literal = %v", first["literal"])
	}
	issued, _ := it["issued"].(map[string]any)
	if issued == nil {
		t.Fatalf("missing issued date-parts")
	}
}

func TestFormatCSLJSONEmptyIsValidArray(t *testing.T) {
	out, n := FormatBibliography(nil, "csl-json")
	if n != 0 || out != "[]" {
		t.Errorf("empty CSL-JSON should be [] with count 0, got %q (n=%d)", out, n)
	}
}

func TestFormatCSLJSONEscapesSpecials(t *testing.T) {
	entries := []BibEntry{{URL: "https://x/a", Title: `Quote " and back\slash and <tag>`}}
	out, _ := FormatBibliography(entries, "csl-json")
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("special chars broke JSON validity: %v\n%s", err, out)
	}
	if items[0]["title"] != `Quote " and back\slash and <tag>` {
		t.Errorf("title round-trip failed: %v", items[0]["title"])
	}
}

func TestInterchangeDeterministicAndDeduped(t *testing.T) {
	entries := []BibEntry{
		{URL: "https://example.com/b", Title: "Beta", Author: "Zeta, Z.", Date: "2020"},
		{URL: "https://example.com/a", Title: "Alpha", Author: "Adams, A.", Date: "2019"},
		{URL: "https://example.com/b", Title: "dup", Author: "Zeta, Z.", Date: "2020"}, // dup URL
	}
	for _, style := range []string{"ris", "csl-json"} {
		out1, n1 := FormatBibliography(entries, style)
		out2, n2 := FormatBibliography(entries, style)
		if out1 != out2 {
			t.Errorf("%s output not deterministic", style)
		}
		if n1 != 2 || n2 != 2 {
			t.Errorf("%s: dedup-by-URL failed, count=%d", style, n1)
		}
		// Cite-key order: adams2019alpha precedes zeta2020beta.
		if strings.Index(out1, "Adams") > strings.Index(out1, "Zeta") && strings.Contains(out1, "Adams") {
			t.Errorf("%s not ordered by cite key:\n%s", style, out1)
		}
	}
}

func TestBibTeXCarriesDOI(t *testing.T) {
	entries := []BibEntry{{URL: "https://x/a", Title: "T", Author: "Doe, J.", Date: "2020", DOI: "10.1/abc"}}
	out, _ := FormatBibliography(entries, "bibtex")
	if !strings.Contains(out, "doi = {10.1/abc}") {
		t.Errorf("BibTeX should carry the doi field:\n%s", out)
	}
}

func TestFormatBibTeX_ArticleWhenDOI(t *testing.T) {
	t.Parallel()
	entries := []BibEntry{
		{URL: "https://example.com/a", Title: "Deep Learning", Author: "LeCun, Yann", Site: "Nature", Date: "2015", DOI: "10.1038/nature14539"},
	}
	out, _ := FormatBibliography(entries, "bibtex")
	if !strings.HasPrefix(out, "@article{") {
		t.Errorf("BibTeX with DOI should start with @article, got:\n%s", out)
	}
	if strings.Contains(out, "@misc{") {
		t.Errorf("BibTeX with DOI must not use @misc:\n%s", out)
	}
	if !strings.Contains(out, "journal = {Nature}") {
		t.Errorf("BibTeX @article should have journal field, got:\n%s", out)
	}
	if strings.Contains(out, "howpublished") {
		t.Errorf("BibTeX @article must not have howpublished field:\n%s", out)
	}
	if !strings.Contains(out, "doi = {10.1038/nature14539}") {
		t.Errorf("BibTeX @article should carry doi field:\n%s", out)
	}
}

func TestFormatBibTeX_MiscWhenNoDOI(t *testing.T) {
	t.Parallel()
	entries := []BibEntry{
		{URL: "https://example.com/b", Title: "A Blog Post", Author: "Smith, J.", Site: "ExampleBlog", Date: "2022"},
	}
	out, _ := FormatBibliography(entries, "bibtex")
	if !strings.HasPrefix(out, "@misc{") {
		t.Errorf("BibTeX without DOI should use @misc, got:\n%s", out)
	}
	if strings.Contains(out, "@article{") {
		t.Errorf("BibTeX without DOI must not use @article:\n%s", out)
	}
	if !strings.Contains(out, "howpublished = {ExampleBlog}") {
		t.Errorf("BibTeX @misc should have howpublished field:\n%s", out)
	}
}

func TestFormatRIS_JOURWhenDOI(t *testing.T) {
	t.Parallel()
	entries := []BibEntry{
		{URL: "https://example.com/a", Title: "Some Study", Author: "Doe, J.", Site: "Science", Date: "2021", DOI: "10.1126/science.abc1234"},
	}
	out, _ := FormatBibliography(entries, "ris")
	if !strings.Contains(out, "TY  - JOUR") {
		t.Errorf("RIS with DOI should use TY = JOUR:\n%s", out)
	}
	if strings.Contains(out, "TY  - ELEC") {
		t.Errorf("RIS with DOI must not use TY = ELEC:\n%s", out)
	}
	// Verify the rest of the record is still present.
	if !strings.Contains(out, "TI  - Some Study") {
		t.Errorf("RIS missing title:\n%s", out)
	}
	if !strings.Contains(out, "DO  - 10.1126/science.abc1234") {
		t.Errorf("RIS missing DOI:\n%s", out)
	}
	if !strings.Contains(out, "ER  - ") {
		t.Errorf("RIS missing ER terminator:\n%s", out)
	}
}

func TestFormatCSLJSON_ArticleJournalWhenDOI(t *testing.T) {
	t.Parallel()
	entries := []BibEntry{
		{URL: "https://example.com/a", Title: "Genomics Paper", Author: "Kim, S.", Site: "Cell", Date: "2020", DOI: "10.1016/j.cell.2020.01.001"},
	}
	out, _ := FormatBibliography(entries, "csl-json")
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("CSL-JSON is not valid JSON: %v\n%s", err, out)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 CSL item, got %d", len(items))
	}
	it := items[0]
	if it["type"] != "article-journal" {
		t.Errorf("type = %v, want article-journal", it["type"])
	}
	if it["container-title"] != "Cell" {
		t.Errorf("container-title = %v, want Cell", it["container-title"])
	}
	if it["DOI"] != "10.1016/j.cell.2020.01.001" {
		t.Errorf("DOI = %v", it["DOI"])
	}
	// No-DOI variant should remain webpage.
	noDOI := []BibEntry{
		{URL: "https://example.com/b", Title: "Blog Post", Site: "SomeBlog", Date: "2020"},
	}
	out2, _ := FormatBibliography(noDOI, "csl-json")
	var items2 []map[string]any
	if err := json.Unmarshal([]byte(out2), &items2); err != nil {
		t.Fatalf("CSL-JSON (no-DOI) is not valid JSON: %v", err)
	}
	if items2[0]["type"] != "webpage" {
		t.Errorf("no-DOI type = %v, want webpage", items2[0]["type"])
	}
}

func TestSplitAuthors(t *testing.T) {
	cases := map[string]int{
		"":                      0,
		"Smith, J.":             1,
		"Smith, J.; Doe, A.":    2,
		"Smith, J. and Doe, A.": 2,
		"A; B; C":               3,
	}
	for in, want := range cases {
		if got := len(splitAuthors(in)); got != want {
			t.Errorf("splitAuthors(%q) = %d names, want %d", in, got, want)
		}
	}
}
