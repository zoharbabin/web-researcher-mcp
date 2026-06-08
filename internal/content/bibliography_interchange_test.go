package content

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFormatRISBasic(t *testing.T) {
	entries := []BibEntry{
		{URL: "https://example.com/a", Title: "Attention Is All You Need", Author: "Vaswani, Ashish; Shazeer, Noam", Site: "NeurIPS", Date: "2017", DOI: "10.5555/3295222"},
	}
	out, n := FormatBibliography(entries, "ris")
	if n != 1 {
		t.Fatalf("entry count = %d, want 1", n)
	}
	for _, want := range []string{"TY  - ELEC", "TI  - Attention Is All You Need", "AU  - Vaswani, Ashish", "AU  - Shazeer, Noam", "PY  - 2017", "T2  - NeurIPS", "DO  - 10.5555/3295222", "UR  - https://example.com/a", "ER  - "} {
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
	if it["type"] != "webpage" {
		t.Errorf("type = %v, want webpage", it["type"])
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
