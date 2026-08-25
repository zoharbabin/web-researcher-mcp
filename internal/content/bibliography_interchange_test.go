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
	if first["family"] != "LeCun" || first["given"] != "Yann" {
		t.Errorf("first author family/given = %v/%v, want LeCun/Yann", first["family"], first["given"])
	}
	issued, _ := it["issued"].(map[string]any)
	if issued == nil {
		t.Fatalf("missing issued date-parts")
	}
}

// TestFormatBibliographyBareCommaPreInvertedPairsAcrossFormats proves #680's
// success criteria end-to-end: a bare comma-separated pre-inverted author
// list ("Smith, John, Doe, Jane") renders as two distinct authors in every
// bibliography format, not one mangled author.
func TestFormatBibliographyBareCommaPreInvertedPairsAcrossFormats(t *testing.T) {
	entries := []BibEntry{{
		URL:    "https://example.com/a",
		Title:  "Widgets at Scale",
		Author: "Smith, John, Doe, Jane",
		Date:   "2020",
	}}

	bibtexOut, _ := FormatBibliography(entries, "bibtex")
	if !strings.Contains(bibtexOut, "author = {Smith, John and Doe, Jane}") {
		t.Errorf("BibTeX should render 2 authors joined by \" and \", not 1 merged author:\n%s", bibtexOut)
	}

	risOut, _ := FormatBibliography(entries, "ris")
	auCount := strings.Count(risOut, "AU  - ")
	if auCount != 2 {
		t.Errorf("RIS should have 2 AU lines, got %d:\n%s", auCount, risOut)
	}
	if !strings.Contains(risOut, "AU  - Smith, John") || !strings.Contains(risOut, "AU  - Doe, Jane") {
		t.Errorf("RIS AU lines should be Smith, John and Doe, Jane:\n%s", risOut)
	}

	cslOut, _ := FormatBibliography(entries, "csl-json")
	var items []map[string]any
	if err := json.Unmarshal([]byte(cslOut), &items); err != nil {
		t.Fatalf("CSL-JSON is not valid JSON: %v\n%s", err, cslOut)
	}
	authors, ok := items[0]["author"].([]any)
	if !ok || len(authors) != 2 {
		t.Fatalf("expected 2 CSL-JSON authors, got %v", items[0]["author"])
	}
	first, _ := authors[0].(map[string]any)
	second, _ := authors[1].(map[string]any)
	if first["family"] != "Smith" || first["given"] != "John" {
		t.Errorf("first author family/given = %v/%v, want Smith/John", first["family"], first["given"])
	}
	if second["family"] != "Doe" || second["given"] != "Jane" {
		t.Errorf("second author family/given = %v/%v, want Doe/Jane", second["family"], second["given"])
	}

	apaOut, _ := FormatBibliography(entries, "apa")
	if !strings.Contains(apaOut, "Smith, John, & Doe, Jane") {
		t.Errorf("APA should render 2 authors, not 1 merged author:\n%s", apaOut)
	}

	mlaOut, _ := FormatBibliography(entries, "mla")
	if !strings.Contains(mlaOut, "Smith, John, and Doe, Jane") {
		t.Errorf("MLA should render 2 authors, not 1 merged author:\n%s", mlaOut)
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

// TestFormatCSLJSONCollisionKeys proves the #531 fix: two entries whose
// BibTeXKey collides (same author+year+first-title-word, different URLs) must
// get distinct "id" values in the CSL-JSON array — the same collisionSuffix
// scheme formatBibTeXList already applies — instead of silently sharing an id
// that would shadow one entry when a reference manager indexes by id.
func TestFormatCSLJSONCollisionKeys(t *testing.T) {
	entries := []BibEntry{
		{URL: "https://example.com/1", Title: "Learning models", Author: "Smith, A.", Date: "2020"},
		{URL: "https://example.com/2", Title: "Learning systems", Author: "Smith, A.", Date: "2020"},
	}
	out, n := FormatBibliography(entries, "csl-json")
	if n != 2 {
		t.Fatalf("entry count = %d, want 2", n)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("CSL-JSON is not valid JSON: %v\n%s", err, out)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 CSL items, got %d", len(items))
	}
	ids := map[string]bool{}
	for _, it := range items {
		id, _ := it["id"].(string)
		if id == "" {
			t.Fatalf("item missing id: %v", it)
		}
		if ids[id] {
			t.Fatalf("duplicate id %q across CSL-JSON items:\n%s", id, out)
		}
		ids[id] = true
	}
	if !ids["smith2020learning"] {
		t.Errorf("base key missing among ids: %v", ids)
	}
	if !ids["smith2020learninga"] {
		t.Errorf("collision-suffixed key missing among ids: %v", ids)
	}
}

func TestSplitAuthors(t *testing.T) {
	cases := map[string]int{
		"":                               0,
		"Smith, J.":                      1,
		"Smith, J.; Doe, A.":             2,
		"Smith, J. and Doe, A.":          2,
		"A; B; C":                        3,
		"Yoshua Bengio, Aaron Courville": 2,
		"Yoshua Bengio, Aaron Courville, Geoffrey Hinton": 3,
		"Smith, John Michael and Doe, Jane Anne":          2,
		"Smith, John, Doe, Jane":                          2,
		"Smith, John, Doe, Jane, Lee, Kim":                3,
	}
	for in, want := range cases {
		if got := len(splitAuthors(in)); got != want {
			t.Errorf("splitAuthors(%q) = %d names, want %d", in, got, want)
		}
	}
}

// TestSplitAuthorsBareCommaFullNames proves the #639 fix: a bare
// comma-separated list of full "First Last" names (no ";"/" and") is split
// into individual authors, not misparsed as one pre-inverted "Family, Given"
// name.
func TestSplitAuthorsBareCommaFullNames(t *testing.T) {
	names := splitAuthors("Yoshua Bengio, Aaron Courville")
	if len(names) != 2 || names[0] != "Yoshua Bengio" || names[1] != "Aaron Courville" {
		t.Fatalf("splitAuthors(bare comma) = %v, want [Yoshua Bengio, Aaron Courville]", names)
	}

	family, given, isLiteral := splitPersonalName(names[0])
	if isLiteral || family != "Bengio" || given != "Yoshua" {
		t.Errorf("first author split = (%q, %q, %v), want (Bengio, Yoshua, false)", family, given, isLiteral)
	}
}

// TestSplitAuthorsPreInvertedSingleAuthorNotSplit proves a genuine single
// pre-inverted "Family, Given" author (both sides a single token, as in
// "Bengio, Yoshua" or "Smith, J.") is never mistaken for a bare-comma
// multi-author list.
func TestSplitAuthorsPreInvertedSingleAuthorNotSplit(t *testing.T) {
	cases := []string{"Bengio, Yoshua", "Smith, J.", "de la Cruz, Maria"}
	for _, in := range cases {
		names := splitAuthors(in)
		if len(names) != 1 || names[0] != in {
			t.Errorf("splitAuthors(%q) = %v, want single unsplit author %q", in, names, in)
		}
	}
}

// TestSplitAuthorsBareCommaPreInvertedPairs proves the #680 fix: a bare
// comma-separated list of pre-inverted "Family, Given" pairs (all
// single-token comma segments, even count) is paired back up into individual
// "Family, Given" authors, not merged into one mangled author.
func TestSplitAuthorsBareCommaPreInvertedPairs(t *testing.T) {
	names := splitAuthors("Smith, John, Doe, Jane")
	want := []string{"Smith, John", "Doe, Jane"}
	if len(names) != len(want) {
		t.Fatalf("splitAuthors(bare comma pre-inverted pairs) = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("splitAuthors(bare comma pre-inverted pairs)[%d] = %q, want %q", i, names[i], want[i])
		}
	}

	names3 := splitAuthors("Smith, John, Doe, Jane, Lee, Kim")
	want3 := []string{"Smith, John", "Doe, Jane", "Lee, Kim"}
	if len(names3) != len(want3) {
		t.Fatalf("splitAuthors(3 pre-inverted pairs) = %v, want %v", names3, want3)
	}
	for i := range want3 {
		if names3[i] != want3[i] {
			t.Errorf("splitAuthors(3 pre-inverted pairs)[%d] = %q, want %q", i, names3[i], want3[i])
		}
	}
}

// TestSplitPersonalName proves the shared family/given parser (#621) matches
// the last-token-is-surname convention invertNameAPA/invertNameMLA already
// use, plus the pre-inverted-comma and unsplittable-single-token cases.
func TestSplitPersonalName(t *testing.T) {
	cases := []struct {
		name          string
		family, given string
		wantLiteral   bool
	}{
		{"James Watson", "Watson", "James", false},
		{"Francis Harry Compton Crick", "Crick", "Francis Harry Compton", false},
		{"Watson, James", "Watson", "James", false},
		{"Smith, J.", "Smith", "J.", false},
		{"NASA", "NASA", "", true},
		{"Prince", "Prince", "", true},
		{"", "", "", true},
	}
	for _, c := range cases {
		family, given, isLiteral := splitPersonalName(c.name)
		if family != c.family || given != c.given || isLiteral != c.wantLiteral {
			t.Errorf("splitPersonalName(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.name, family, given, isLiteral, c.family, c.given, c.wantLiteral)
		}
	}
}

// TestCSLAuthorsFamilyGivenSplit proves the #621 fix: an ordinary "Given
// Family" personal name becomes {"family", "given"} in CSL-JSON's author
// array, not {"literal": "James Watson"} — literal is reserved for names that
// genuinely can't be split (a single-token surname or organization).
func TestCSLAuthorsFamilyGivenSplit(t *testing.T) {
	entries := []BibEntry{
		{URL: "https://example.com/dna", Title: "Molecular Structure of Nucleic Acids", Author: "James Watson and Francis Crick", Date: "1953"},
	}
	out, _ := FormatBibliography(entries, "csl-json")
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("CSL-JSON is not valid JSON: %v\n%s", err, out)
	}
	authors, ok := items[0]["author"].([]any)
	if !ok || len(authors) != 2 {
		t.Fatalf("expected 2 authors, got %v", items[0]["author"])
	}
	watson, _ := authors[0].(map[string]any)
	if watson["family"] != "Watson" || watson["given"] != "James" {
		t.Errorf("first author = %v, want family=Watson given=James", watson)
	}
	if _, hasLiteral := watson["literal"]; hasLiteral {
		t.Errorf("first author should not carry literal once split: %v", watson)
	}
	crick, _ := authors[1].(map[string]any)
	if crick["family"] != "Crick" || crick["given"] != "Francis" {
		t.Errorf("second author = %v, want family=Crick given=Francis", crick)
	}
	// The id/cite-key must key on the surname too — same root cause, same fix.
	if items[0]["id"] != "watson1953molecular" {
		t.Errorf("id = %v, want watson1953molecular", items[0]["id"])
	}
}

// TestCSLAuthorsOrgNameStaysLiteral proves a single-token name (an
// organization, or a bare surname with no given name to split out) still
// falls back to {"literal": …} rather than being force-split.
func TestCSLAuthorsOrgNameStaysLiteral(t *testing.T) {
	entries := []BibEntry{{URL: "https://example.com/x", Title: "Report", Author: "NASA", Date: "2020"}}
	out, _ := FormatBibliography(entries, "csl-json")
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("CSL-JSON is not valid JSON: %v\n%s", err, out)
	}
	authors, ok := items[0]["author"].([]any)
	if !ok || len(authors) != 1 {
		t.Fatalf("expected 1 author, got %v", items[0]["author"])
	}
	org, _ := authors[0].(map[string]any)
	if org["literal"] != "NASA" {
		t.Errorf("org author = %v, want literal=NASA", org)
	}
}
