package content

import (
	"testing"
)

func findByTitle(entries []BibEntry, title string) *BibEntry {
	for i := range entries {
		if entries[i].Title == title {
			return &entries[i]
		}
	}
	return nil
}

// The strongest correctness guarantee: what the writers emit, the parsers read
// back faithfully (DOI/title/authors/site/year/url survive the round trip).
func TestRoundTripAllFormats(t *testing.T) {
	src := []BibEntry{
		{URL: "https://www.nature.com/articles/nature14539", Title: "Deep Learning", Author: "LeCun, Yann; Bengio, Yoshua", Site: "Nature", Date: "2015", DOI: "10.1038/nature14539"},
		{URL: "https://example.com/b", Title: "Attention Is All You Need", Author: "Vaswani, Ashish", Site: "NeurIPS", Date: "2017", DOI: "10.5555/3295222"},
	}
	for _, format := range []string{"csl-json", "ris", "bibtex"} {
		doc, n := FormatBibliography(src, format)
		if n != 2 {
			t.Fatalf("%s: writer rendered %d entries, want 2", format, n)
		}
		parsed, used := ParseBibliography(doc, "auto")
		if used != format {
			t.Errorf("%s: auto-detect returned %q", format, used)
		}
		if len(parsed) != 2 {
			t.Fatalf("%s: parsed %d entries, want 2\n%s", format, len(parsed), doc)
		}
		got := findByTitle(parsed, "Deep Learning")
		if got == nil {
			t.Fatalf("%s: lost the Deep Learning entry:\n%s", format, doc)
		}
		if got.DOI != "10.1038/nature14539" {
			t.Errorf("%s: DOI round-trip failed: %q", format, got.DOI)
		}
		if got.Date != "2015" {
			t.Errorf("%s: year round-trip failed: %q", format, got.Date)
		}
		if got.Site != "Nature" {
			t.Errorf("%s: site round-trip failed: %q", format, got.Site)
		}
		// First author must survive (BibTeX keeps only what we render; CSL/RIS keep all).
		if got.Author == "" {
			t.Errorf("%s: author lost", format)
		}
	}
}

func TestParseCSLJSONSingleObject(t *testing.T) {
	doc := `{"id":"x","type":"webpage","title":"Solo","DOI":"10.1/x","author":[{"literal":"Doe, J."}],"issued":{"date-parts":[[2021]]}}`
	parsed, used := ParseBibliography(doc, "auto")
	if used != "csl-json" || len(parsed) != 1 {
		t.Fatalf("single object should parse as 1 csl-json entry, got %d (%s)", len(parsed), used)
	}
	if parsed[0].DOI != "10.1/x" || parsed[0].Date != "2021" || parsed[0].Author != "Doe, J." {
		t.Errorf("field mapping wrong: %+v", parsed[0])
	}
}

func TestParseCSLJSONFamilyGiven(t *testing.T) {
	doc := `[{"title":"T","author":[{"family":"Smith","given":"Jane"},{"family":"Roe"}]}]`
	parsed, _ := ParseBibliography(doc, "csl-json")
	if len(parsed) != 1 || parsed[0].Author != "Smith, Jane; Roe" {
		t.Errorf("family/given join wrong: %q", parsed[0].Author)
	}
}

func TestParseCSLJSONDOIBecomesURL(t *testing.T) {
	doc := `[{"title":"T","DOI":"10.1/x"}]`
	parsed, _ := ParseBibliography(doc, "csl-json")
	if parsed[0].URL != "https://doi.org/10.1/x" {
		t.Errorf("DOI should yield a resolver URL when no URL present: %q", parsed[0].URL)
	}
}

func TestParseRISMultiRecord(t *testing.T) {
	doc := "TY  - ELEC\nTI  - First\nAU  - A, B\nAU  - C, D\nPY  - 2020\nDO  - 10.1/a\nUR  - https://x/a\nER  - \n\nTY  - ELEC\nTI  - Second\nPY  - 2019\nER  - "
	parsed, used := ParseBibliography(doc, "auto")
	if used != "ris" {
		t.Fatalf("auto-detect = %q, want ris", used)
	}
	if len(parsed) != 2 {
		t.Fatalf("want 2 records, got %d", len(parsed))
	}
	first := findByTitle(parsed, "First")
	if first.Author != "A, B; C, D" {
		t.Errorf("repeated AU should join: %q", first.Author)
	}
	if first.DOI != "10.1/a" || first.URL != "https://x/a" {
		t.Errorf("DOI/URL mapping: %+v", *first)
	}
}

func TestParseRISTrailingRecordNoER(t *testing.T) {
	doc := "TY  - ELEC\nTI  - Only\nPY  - 2022"
	parsed, _ := ParseBibliography(doc, "ris")
	if len(parsed) != 1 || parsed[0].Title != "Only" {
		t.Errorf("a trailing record without ER must still parse: %+v", parsed)
	}
}

func TestParseBibTeXBracedAndNested(t *testing.T) {
	doc := "@misc{smith2020learning,\n  author = {Smith, Alice and Doe, Bob},\n  title = {Nested {Braces} Survive},\n  howpublished = {Some Site},\n  year = {2020},\n  doi = {10.1/abc},\n  url = {https://x/a},\n}"
	parsed, used := ParseBibliography(doc, "auto")
	if used != "bibtex" {
		t.Fatalf("auto-detect = %q, want bibtex", used)
	}
	if len(parsed) != 1 {
		t.Fatalf("want 1 entry, got %d", len(parsed))
	}
	e := parsed[0]
	if e.Title != "Nested {Braces} Survive" {
		t.Errorf("nested braces not preserved: %q", e.Title)
	}
	if e.Author != "Smith, Alice; Doe, Bob" {
		t.Errorf("\" and \" authors should become \"; \": %q", e.Author)
	}
	if e.DOI != "10.1/abc" || e.URL != "https://x/a" || e.Date != "2020" || e.Site != "Some Site" {
		t.Errorf("field mapping: %+v", e)
	}
}

func TestParseBibTeXEscapesReversed(t *testing.T) {
	doc := `@misc{k, title = {Cost 50\% \& {rising}}, url = {http://x/a} }`
	parsed, _ := ParseBibliography(doc, "bibtex")
	if len(parsed) != 1 {
		t.Fatalf("want 1, got %d", len(parsed))
	}
	if parsed[0].Title != "Cost 50% & {rising}" {
		t.Errorf("escapes not reversed: %q", parsed[0].Title)
	}
}

func TestParseBibTeXQuotedValues(t *testing.T) {
	doc := `@article{k, title = "Quoted Title", year = "2018", doi = "10.2/y"}`
	parsed, _ := ParseBibliography(doc, "bibtex")
	if len(parsed) != 1 || parsed[0].Title != "Quoted Title" || parsed[0].Date != "2018" || parsed[0].DOI != "10.2/y" {
		t.Errorf("quoted-value parse failed: %+v", parsed)
	}
}

func TestParseBibTeXMultipleEntries(t *testing.T) {
	doc := "@misc{a, title = {One}, year = {2020}}\n\n@misc{b, title = {Two}, year = {2021}}"
	parsed, _ := ParseBibliography(doc, "bibtex")
	if len(parsed) != 2 {
		t.Fatalf("want 2 entries, got %d", len(parsed))
	}
}

func TestParseEmptyAndGarbage(t *testing.T) {
	for _, doc := range []string{"", "   ", "not a bibliography at all", "%%%%%"} {
		parsed, _ := ParseBibliography(doc, "auto")
		if len(parsed) != 0 {
			t.Errorf("garbage %q should yield no entries, got %d", doc, len(parsed))
		}
	}
}

func TestParseMalformedJSONIsEmpty(t *testing.T) {
	parsed, used := ParseBibliography(`[{"title": "broken"`, "auto")
	if used != "csl-json" {
		t.Errorf("leading [ should detect csl-json, got %q", used)
	}
	if len(parsed) != 0 {
		t.Errorf("malformed JSON should yield no entries (lenient), got %d", len(parsed))
	}
}

func TestDetectBibFormat(t *testing.T) {
	cases := map[string]string{
		`[{"title":"x"}]`:     "csl-json",
		`{"title":"x"}`:       "csl-json",
		"@misc{k, title={x}}": "bibtex",
		"TY  - ELEC\nER  - ":  "ris",
		"AU  - Smith\nER  - ": "ris",
		"":                    "",
		"random text":         "",
	}
	for doc, want := range cases {
		if got := detectBibFormat(doc); got != want {
			t.Errorf("detectBibFormat(%q) = %q, want %q", doc, got, want)
		}
	}
}
