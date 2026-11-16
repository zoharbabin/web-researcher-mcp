package scraper

import (
	"testing"
	"unicode/utf8"
)

// TestScrapeBodyAsPDF_RawCutNeverSplitsCharacter: the raw body cut must not
// end inside a multi-byte character. The CJK tail sits after the PDF, and
// maxLength ends one byte into its first character.
func TestScrapeBodyAsPDF_RawCutNeverSplitsCharacter(t *testing.T) {
	t.Parallel()
	pdf := minimalPDF(t)
	body := append(append([]byte{}, pdf...), []byte("\n日本語")...)

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	res, err := p.scrapeBodyAsPDF("https://example.com/a.pdf", body, len(pdf)+2, true, "application/pdf")
	if err != nil {
		t.Fatalf("scrapeBodyAsPDF failed: %v", err)
	}
	if !res.rawTruncated {
		t.Fatal("expected the raw body to be truncated")
	}
	if !utf8.ValidString(res.rawBody[len(pdf):]) {
		t.Errorf("raw body ends inside a character: %q", res.rawBody[len(pdf):])
	}
}
