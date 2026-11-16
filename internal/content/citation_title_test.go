package content

import (
	"strings"
	"testing"
)

// TestExtractCitation_EmptyTitleFallsBackToHost guards #854: an empty title
// uses the URL host in APA, MLA and BibTeX, and Metadata.Title stays raw.
func TestExtractCitation_EmptyTitleFallsBackToHost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		url       string
		title     string
		wantTitle string // raw Metadata.Title
		wantAPA   string
		wantMLA   string
		wantBibTX string
	}{
		{
			name:      "empty title uses host",
			url:       "https://example.org/papers/x.pdf",
			title:     "",
			wantTitle: "",
			wantAPA:   "Doe, J. (2024). example.org. Site. Retrieved",
			wantMLA:   "Doe, John. \"example.org.\" Site,",
			wantBibTX: "title = {example.org},",
		},
		{
			name:      "port is dropped from host",
			url:       "http://127.0.0.1:8080/x",
			title:     "",
			wantTitle: "",
			wantAPA:   "(2024). 127.0.0.1. Site. Retrieved",
			wantMLA:   "\"127.0.0.1.\"",
			wantBibTX: "title = {127.0.0.1},",
		},
		{
			name:      "real title is kept",
			url:       "https://example.org/x",
			title:     "A Real Title",
			wantTitle: "A Real Title",
			wantAPA:   "(2024). A Real Title. Site.",
			wantMLA:   "\"A Real Title.\"",
			wantBibTX: "title = {A Real Title},",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := ExtractCitation(tt.url, tt.title, "John Doe", "Site", "2024")
			if c.Metadata.Title != tt.wantTitle {
				t.Errorf("Metadata.Title = %q, want %q", c.Metadata.Title, tt.wantTitle)
			}
			if !strings.Contains(c.Formatted.APA, tt.wantAPA) {
				t.Errorf("APA = %q, want it to contain %q", c.Formatted.APA, tt.wantAPA)
			}
			if !strings.Contains(c.Formatted.MLA, tt.wantMLA) {
				t.Errorf("MLA = %q, want it to contain %q", c.Formatted.MLA, tt.wantMLA)
			}
			if !strings.Contains(c.Formatted.BibTeX, tt.wantBibTX) {
				t.Errorf("BibTeX = %q, want it to contain %q", c.Formatted.BibTeX, tt.wantBibTX)
			}
		})
	}
}

// TestExtractCitation_EmptyTitleNoURL guards #854: with no usable host the
// title slot stays empty instead of printing junk.
func TestExtractCitation_EmptyTitleNoURL(t *testing.T) {
	t.Parallel()
	for _, u := range []string{"", "not a url", "%zz"} {
		c := ExtractCitation(u, "", "", "", "")
		if strings.Contains(c.Formatted.BibTeX, "title =") {
			t.Errorf("url %q: BibTeX has a title field: %q", u, c.Formatted.BibTeX)
		}
	}
}
