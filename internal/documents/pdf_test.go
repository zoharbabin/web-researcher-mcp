package documents

import (
	"strings"
	"testing"

	"github.com/razvandimescu/gopdf/pdf"
)

// placedLine positions one line of text at an exact (x, y) point, mirroring
// the vendored library's own creator_test.go fixture convention
// (pdf.NewCreator + PageBuilder.DrawText draws one text span per call).
type placedLine struct {
	x, y float64
	text string
}

func buildPlacedTextPDF(t *testing.T, width, height, fontSize float64, lines []placedLine) []byte {
	t.Helper()
	c := pdf.NewCreator()
	page := c.NewPage(width, height)
	page.SetFont("Helvetica", fontSize)
	for _, l := range lines {
		page.DrawText(l.x, l.y, l.text)
	}
	data, err := c.Build()
	if err != nil {
		t.Fatalf("building fixture PDF: %v", err)
	}
	return data
}

// TestParsePDF_SingleColumn_ByteIdenticalToLibraryText is the critical
// regression guard for #678: single-column PDFs (the common case for most
// non-academic documents) must extract exactly as before this fix. The
// reference is the vendored library's own whole-document Text() method —
// precisely what the old parsePDF called directly, with no column logic
// in front of it.
func TestParsePDF_SingleColumn_ByteIdenticalToLibraryText(t *testing.T) {
	lines := []placedLine{
		{x: 72, y: 750, text: "This report summarizes quarterly results across all regions."},
		{x: 72, y: 734, text: "Revenue grew steadily while operating costs remained flat."},
		{x: 72, y: 718, text: "The following sections break down performance by segment."},
		{x: 72, y: 702, text: "Marketing spend increased in Q3 ahead of the product launch [1]."},
		{x: 72, y: 686, text: "Customer retention improved year over year across all cohorts."},
		{x: 72, y: 670, text: "See appendix A for the full breakdown by region and channel."},
	}
	data := buildPlacedTextPDF(t, 612, 792, 12, lines)

	refDoc, err := pdf.OpenBytes(data)
	if err != nil {
		t.Fatalf("opening fixture (reference path): %v", err)
	}
	want, err := refDoc.Text()
	if err != nil {
		t.Fatalf("reference doc.Text(): %v", err)
	}
	if want == "" {
		t.Fatal("reference extraction produced empty text; fixture is broken")
	}

	got, _, err := Parse(data, "pdf")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got != want {
		t.Errorf("single-column extraction regressed:\n got:  %q\nwant: %q", got, want)
	}
}

// TestParsePDF_SingleColumn_MultiPage_ByteIdenticalToLibraryText extends the
// byte-identical guarantee to multi-page single-column documents.
func TestParsePDF_SingleColumn_MultiPage_ByteIdenticalToLibraryText(t *testing.T) {
	c := pdf.NewCreator()
	for _, pageLines := range [][]string{
		{"Page one, first line.", "Page one, second line."},
		{"Page two, first line.", "Page two, second line [7]."},
	} {
		page := c.NewPage(612, 792)
		page.SetFont("Helvetica", 12)
		y := 750.0
		for _, l := range pageLines {
			page.DrawText(72, y, l)
			y -= 16
		}
	}
	data, err := c.Build()
	if err != nil {
		t.Fatalf("building fixture PDF: %v", err)
	}

	refDoc, err := pdf.OpenBytes(data)
	if err != nil {
		t.Fatalf("opening fixture (reference path): %v", err)
	}
	want, err := refDoc.Text()
	if err != nil {
		t.Fatalf("reference doc.Text(): %v", err)
	}

	got, meta, err := Parse(data, "pdf")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if meta.PageCount != 2 {
		t.Errorf("PageCount = %d, want 2", meta.PageCount)
	}
	if got != want {
		t.Errorf("multi-page single-column extraction regressed:\n got:  %q\nwant: %q", got, want)
	}
}

// TestParsePDF_TwoColumn_ReadingOrderReconstructed reproduces the exact bug
// shape from #678: two columns of academic-style prose sharing the same Y
// coordinates. The pre-fix behavior (naive Y-band merge across the whole
// page width) splices column 1's tail directly onto column 2's head — e.g.
// "[38]. Hybrid" — instead of keeping each column's prose intact and
// emitting column 1 in full before column 2.
func TestParsePDF_TwoColumn_ReadingOrderReconstructed(t *testing.T) {
	col1 := []string{
		"Retrieval-augmented models often cite prior work [38].",
		"combining parametric and non-parametric memory",
		"helps improve factual accuracy in generated text.",
		"Approaches like REALM [12] and ORQA [15] differ",
		"in how retriever and reader components interact.",
		"with the surrounding generation pipeline overall.",
		"Earlier systems relied on a fixed retrieval step",
		"performed once before generation ever began.",
		"Later work explored iterative retrieval instead,",
		"interleaving lookups with partial generation.",
		"Our approach builds on these lessons directly by",
		"jointly training the retriever end to end.",
	}
	col2 := []string{
		"Hybrid methods add dense passage retrieval",
		"combined with sparse lexical matching to",
		"balance precision and recall broadly.",
		"This method trains retriever and generator",
		"jointly end to end for better results [42].",
		"We evaluate on open-domain question answering",
		"as well as fact verification benchmarks widely",
		"used throughout the community for comparison.",
		"Results show consistent gains over baselines",
		"that use retrieval or generation alone, not both.",
		"Ablations confirm each component contributes",
		"measurably to the final reported accuracy.",
	}

	const (
		col1X    = 50.0
		col2X    = 450.0
		fontSize = 9.0
		pageW    = 800.0
		pageH    = 792.0
	)

	var lines []placedLine
	y := 750.0
	for i := range col1 {
		lines = append(lines, placedLine{x: col1X, y: y, text: col1[i]})
		lines = append(lines, placedLine{x: col2X, y: y, text: col2[i]})
		y -= 16
	}
	data := buildPlacedTextPDF(t, pageW, pageH, fontSize, lines)

	got, meta, err := Parse(data, "pdf")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if meta.PageCount != 1 {
		t.Errorf("PageCount = %d, want 1", meta.PageCount)
	}

	// The reported bug shape: a Y-band merge puts one column-1 line and its
	// same-row column-2 line on a single output line (joined by the
	// proportional-space gap gopdf inserts between same-line spans). No
	// output line may contain text from both columns.
	for _, outLine := range strings.Split(got, "\n") {
		hasCol1 := false
		hasCol2 := false
		for _, l := range col1 {
			if strings.Contains(outLine, l) {
				hasCol1 = true
			}
		}
		for _, l := range col2 {
			if strings.Contains(outLine, l) {
				hasCol2 = true
			}
		}
		if hasCol1 && hasCol2 {
			t.Errorf("output line mixes column 1 and column 2 text — reading order not reconstructed: %q", outLine)
		}
	}

	// Column-major reading order: every column 1 line precedes every
	// column 2 line.
	positions := make(map[string]int, len(col1)+len(col2))
	for _, l := range append(append([]string{}, col1...), col2...) {
		idx := strings.Index(got, l)
		if idx < 0 {
			t.Fatalf("expected line %q not found verbatim in output:\n%s", l, got)
		}
		positions[l] = idx
	}

	lastCol1 := positions[col1[len(col1)-1]]
	firstCol2 := positions[col2[0]]
	for _, l := range col1 {
		if positions[l] > lastCol1 {
			t.Errorf("column 1 line %q appears after the last column 1 line", l)
		}
	}
	if lastCol1 >= firstCol2 {
		t.Errorf("column 1 (last line at %d) does not fully precede column 2 (first line at %d):\n%s", lastCol1, firstCol2, got)
	}
	for _, l := range col2 {
		if positions[l] < firstCol2 {
			t.Errorf("column 2 line %q appears before the first column 2 line", l)
		}
	}
}

// TestDetectColumnBands_SingleColumn_ReturnsNil is a focused unit test on
// the detector itself: overlapping same-left-margin spans (the ordinary
// single-column shape, whatever their individual widths) must never be
// split into bands.
func TestDetectColumnBands_SingleColumn_ReturnsNil(t *testing.T) {
	spans := []pdf.TextSpan{
		{X: 72, EndX: 300, Y: 750, FontSize: 12, Text: "line one of a single column page"},
		{X: 72, EndX: 250, Y: 734, FontSize: 12, Text: "line two, shorter"},
		{X: 72, EndX: 400, Y: 718, FontSize: 12, Text: "line three, considerably longer than the others"},
		{X: 72, EndX: 200, Y: 702, FontSize: 12, Text: "line four"},
		{X: 72, EndX: 350, Y: 686, FontSize: 12, Text: "line five, medium length text here"},
		{X: 72, EndX: 280, Y: 670, FontSize: 12, Text: "line six of the page"},
	}
	if bands := detectColumnBands(spans); bands != nil {
		t.Errorf("expected nil (no column split) for single-column spans, got %d bands", len(bands))
	}
}

// TestDetectColumnBands_SparseSecondBand_FallsBack guards against a stray
// isolated span (e.g. a lone page number) being mistaken for a second
// column.
func TestDetectColumnBands_SparseSecondBand_FallsBack(t *testing.T) {
	spans := []pdf.TextSpan{
		{X: 72, EndX: 300, Y: 750, FontSize: 12, Text: "line one of a single column page"},
		{X: 72, EndX: 250, Y: 734, FontSize: 12, Text: "line two, shorter"},
		{X: 72, EndX: 400, Y: 718, FontSize: 12, Text: "line three, considerably longer than the others"},
		{X: 72, EndX: 200, Y: 702, FontSize: 12, Text: "line four"},
		{X: 72, EndX: 350, Y: 686, FontSize: 12, Text: "line five, medium length text here"},
		{X: 72, EndX: 280, Y: 670, FontSize: 12, Text: "line six of the page"},
		{X: 72, EndX: 320, Y: 654, FontSize: 12, Text: "line seven of the page"},
		{X: 72, EndX: 260, Y: 638, FontSize: 12, Text: "line eight of the page"},
		{X: 550, EndX: 570, Y: 40, FontSize: 10, Text: "12"}, // lone page number, far right
	}
	if bands := detectColumnBands(spans); bands != nil {
		t.Errorf("expected nil (fall back) when a candidate band is too sparse to trust, got %d bands", len(bands))
	}
}
