package content

import (
	"reflect"
	"strings"
	"testing"
)

const (
	orgWiki = "Contributors to Wikimedia projects"
	orgGAO  = "U.S. Government Accountability Office"
)

// TestIsOrganizationName guards #848: organizations are detected by keyword,
// connector word or leading "The"; people are not.
func TestIsOrganizationName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want bool
	}{
		{orgWiki, true},
		{orgGAO, true},
		{"World Health Organization", true},
		{"Acme Corp, Inc.", true},
		{"Bank of America", true},
		{"Center for Disease Control", true},
		{"The Economist", true},
		{"Department of Health and Human Services", true},
		{"Smith and the Others", true},
		{"Doctors Without Borders", true},
		{"Reporters Without Borders", true},
		{"Amnesty International", true},
		{"Human Rights Watch", true},
		{"Google Research", true},
		{"Alicia Court", false},
		{"Mary Church", false},
		{"Jordan Bank", false},
		{"Alicia Malone", false},
		{"Georg Kucsko", false},
		{"Peter C. Maurer", false},
		{"Smith, J.", false},
		{"Hassabis, D.", false},
		{"Plato", false},
		{"The", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isOrganizationName(tt.name); got != tt.want {
				t.Errorf("isOrganizationName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// TestExtractCitation_OrganizationAuthor guards #848: a multi-word organization
// author stays whole in APA, MLA and BibTeX, and people keep today's output.
func TestExtractCitation_OrganizationAuthor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		author    string
		wantAPA   string
		wantMLA   string
		wantBibTX string
	}{
		{
			name:      "wikimedia contributors",
			author:    orgWiki,
			wantAPA:   "Contributors to Wikimedia projects. (2024).",
			wantMLA:   "Contributors to Wikimedia projects. \"Title.\"",
			wantBibTX: "author = {{Contributors to Wikimedia projects}},",
		},
		{
			name:      "government office",
			author:    orgGAO,
			wantAPA:   "U.S. Government Accountability Office. (2024).",
			wantMLA:   "U.S. Government Accountability Office. \"Title.\"",
			wantBibTX: "author = {{U.S. Government Accountability Office}},",
		},
		{
			name:      "person unchanged",
			author:    "Alicia Malone",
			wantAPA:   "Malone, A. (2024).",
			wantMLA:   "Malone, Alicia. \"Title.\"",
			wantBibTX: "author = {Alicia Malone},",
		},
		{
			name:      "two people",
			author:    "Georg Kucsko and Peter C. Maurer",
			wantAPA:   "Kucsko, G., & Maurer, P. C. (2024).",
			wantMLA:   "Kucsko, Georg, and Peter C. Maurer. \"Title.\"",
			wantBibTX: "author = {Georg Kucsko and Peter C. Maurer},",
		},
		{
			name:      "person plus organization",
			author:    "Alicia Malone; " + orgGAO,
			wantAPA:   "Malone, A., & U.S. Government Accountability Office. (2024).",
			wantMLA:   "Malone, Alicia, and U.S. Government Accountability Office. \"Title.\"",
			wantBibTX: "author = {Alicia Malone and {U.S. Government Accountability Office}},",
		},
		{
			name:      "person plus organization joined by and",
			author:    "Alicia Malone and " + orgGAO,
			wantAPA:   "Malone, A., & U.S. Government Accountability Office. (2024).",
			wantMLA:   "Malone, Alicia, and U.S. Government Accountability Office. \"Title.\"",
			wantBibTX: "author = {Alicia Malone and {U.S. Government Accountability Office}},",
		},
		{
			name:      "organization with internal and",
			author:    "Department of Health and Human Services",
			wantAPA:   "Department of Health and Human Services. (2024).",
			wantMLA:   "Department of Health and Human Services. \"Title.\"",
			wantBibTX: "author = {{Department of Health and Human Services}},",
		},
		{
			name:      "pre-inverted name unchanged",
			author:    "Smith, J.",
			wantAPA:   "Smith, J. (2024).",
			wantMLA:   "Smith, J. \"Title.\"",
			wantBibTX: "author = {Smith, J.},",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := ExtractCitation("https://example.com/x", "Title", tt.author, "Site", "2024")
			if !strings.HasPrefix(c.Formatted.APA, tt.wantAPA) {
				t.Errorf("APA = %q, want prefix %q", c.Formatted.APA, tt.wantAPA)
			}
			if !strings.HasPrefix(c.Formatted.MLA, tt.wantMLA) {
				t.Errorf("MLA = %q, want prefix %q", c.Formatted.MLA, tt.wantMLA)
			}
			if !strings.Contains(c.Formatted.BibTeX, tt.wantBibTX) {
				t.Errorf("BibTeX = %q, want it to contain %q", c.Formatted.BibTeX, tt.wantBibTX)
			}
		})
	}
}

// TestSplitAuthors_Organizations guards #848: an organization keeps its
// internal "and", and a person next to an organization still splits.
func TestSplitAuthors_Organizations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		author string
		want   []string
	}{
		{"org with and", "Department of Health and Human Services", []string{"Department of Health and Human Services"}},
		{"bill and melinda", "Bill and Melinda Gates Foundation", []string{"Bill and Melinda Gates Foundation"}},
		{"org with comma", "University of Michigan, Ann Arbor", []string{"University of Michigan, Ann Arbor"}},
		{"person and org", "Alicia Malone and " + orgGAO, []string{"Alicia Malone", orgGAO}},
		{"person and org with and", "Alicia Malone and Department of Health and Human Services", []string{"Alicia Malone", "Department of Health and Human Services"}},
		{"two people", "Georg Kucsko and Peter C. Maurer", []string{"Georg Kucsko", "Peter C. Maurer"}},
		{"semicolons with org", "Georg Kucsko; " + orgWiki, []string{"Georg Kucsko", orgWiki}},
		{"bare comma people", "Yoshua Bengio, Aaron Courville", []string{"Yoshua Bengio", "Aaron Courville"}},
		{"pre-inverted pair", "Smith, J.", []string{"Smith, J."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := splitAuthors(tt.author); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitAuthors(%q) = %q, want %q", tt.author, got, tt.want)
			}
		})
	}
}

// TestSplitPersonalName_Organization guards #848: organizations are literal,
// people still split into family and given.
func TestSplitPersonalName_Organization(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		in          string
		wantFamily  string
		wantGiven   string
		wantLiteral bool
	}{
		{"org", orgWiki, orgWiki, "", true},
		{"org with comma", "Acme, Inc.", "Acme, Inc.", "", true},
		{"person", "Alicia Malone", "Malone", "Alicia", false},
		{"pre-inverted", "Malone, Alicia", "Malone", "Alicia", false},
		{"single token", "UNESCO", "UNESCO", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, g, lit := splitPersonalName(tt.in)
			if f != tt.wantFamily || g != tt.wantGiven || lit != tt.wantLiteral {
				t.Errorf("splitPersonalName(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.in, f, g, lit, tt.wantFamily, tt.wantGiven, tt.wantLiteral)
			}
		})
	}
}

// TestCSLAuthors_Organization guards #848: CSL-JSON gives an organization as a
// "literal" name and people as family/given.
func TestCSLAuthors_Organization(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		author string
		want   string
	}{
		{"org", orgGAO, `[{"literal": "U.S. Government Accountability Office"}]`},
		{"person", "Alicia Malone", `[{"family": "Malone", "given": "Alicia"}]`},
		{"person and org", "Alicia Malone; " + orgWiki, `[{"family": "Malone", "given": "Alicia"}, {"literal": "Contributors to Wikimedia projects"}]`},
		{"pre-inverted", "Malone, Alicia", `[{"family": "Malone", "given": "Alicia"}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := cslAuthors(tt.author); got != tt.want {
				t.Errorf("cslAuthors(%q) = %s, want %s", tt.author, got, tt.want)
			}
		})
	}
}

// TestBibTeXKey_Organization guards #848: the cite key of an organization
// author uses its first word, not the last token.
func TestBibTeXKey_Organization(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		author string
		want   string
	}{
		{"wikimedia", orgWiki, "contributors2024page"},
		{"world health", "World Health Organization", "world2024page"},
		{"person", "Alicia Malone", "malone2024page"},
		{"pre-inverted", "Malone, Alicia", "malone2024page"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := BibTeXKey(tt.author, "2024", "Page"); got != tt.want {
				t.Errorf("BibTeXKey(%q) = %q, want %q", tt.author, got, tt.want)
			}
		})
	}
}

// TestBibTeXOrganizationRoundTrip guards #848: the writer's double-brace
// organization author reads back as the same single name.
func TestBibTeXOrganizationRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		author string
		want   string
	}{
		{"org", orgWiki, orgWiki},
		{"org with and", "Bill and Melinda Gates Foundation", "Bill and Melinda Gates Foundation"},
		{"person and org", "Alicia Malone and " + orgGAO, "Alicia Malone; " + orgGAO},
		{"two people", "Georg Kucsko and Peter C. Maurer", "Georg Kucsko; Peter C. Maurer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := ExtractCitation("https://example.com/x", "Title", tt.author, "Site", "2024")
			e, ok := parseBibTeXEntry(c.Formatted.BibTeX)
			if !ok {
				t.Fatalf("parseBibTeXEntry failed on %q", c.Formatted.BibTeX)
			}
			if e.Author != tt.want {
				t.Errorf("round-trip author = %q, want %q", e.Author, tt.want)
			}
		})
	}
}
