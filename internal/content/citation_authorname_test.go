package content

import (
	"strings"
	"testing"
)

// TestExtractCitation_APA_AuthorInversion guards #602: APA output must invert
// "First [Middle] Last" names to "Last, F. M." and join multiple authors per
// APA7 (comma between middle authors, "&" before the last). An author string
// already in "Last, First"/"Last, F." form (contains a comma) must be left
// unchanged — not double-inverted.
func TestExtractCitation_APA_AuthorInversion(t *testing.T) {
	tests := []struct {
		name   string
		author string
		want   string
	}{
		{
			name:   "multi-author uninverted",
			author: "Georg Kucsko; Peter C. Maurer",
			want:   "Kucsko, G., & Maurer, P. C.",
		},
		{
			name:   "single-author uninverted",
			author: "Ada Lovelace",
			want:   "Lovelace, A.",
		},
		{
			name:   "already-inverted single author unchanged",
			author: "Smith, J.",
			want:   "Smith, J.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ExtractCitation("https://example.com/x", "Title", tt.author, "Site", "2023")
			if got := c.Formatted.APA; !strings.Contains(got, tt.want) {
				t.Errorf("APA author formatting = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

// TestExtractCitation_MLA_AuthorInversion guards #602: MLA9 inverts only the
// first author ("Last, First Middle"); an already-inverted name is left as-is.
func TestExtractCitation_MLA_AuthorInversion(t *testing.T) {
	tests := []struct {
		name   string
		author string
		want   string
	}{
		{
			name:   "single-author uninverted",
			author: "John Smith",
			want:   "Smith, John",
		},
		{
			name:   "already-inverted single author unchanged",
			author: "Smith, John",
			want:   "Smith, John",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ExtractCitation("https://example.com/x", "Title", tt.author, "Site", "2023")
			if got := c.Formatted.MLA; !strings.Contains(got, tt.want) {
				t.Errorf("MLA author formatting = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}
