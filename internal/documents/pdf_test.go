package documents

import (
	"strings"
	"testing"
	"time"

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

// TestDetectColumnBands_SparseSecondBand_FallsBack guards the final
// per-band population check: a gutter can accumulate columnMinLineCount+
// supporting rows (and so get trusted as a real column boundary) via rows
// that are each kept intact — because they only support that one gutter,
// not every established gutter — while genuinely-split, fully-partitioned
// rows contributing actual content to the resulting band stay under
// columnMinLineCount. This constructs exactly that shape with two trusted
// gutters (g1≈200, g2≈400) fed mostly by "kept intact" rows, plus only 2
// rows that are split across all three bands: band 1 and band 2 each end
// up with only 2 real spans, well under columnMinLineCount, so the split
// must be rejected even though both gutters were independently trusted.
//
// This is deliberately not just a small/degenerate page (that would only
// exercise the earlier len(rows) < columnMinLineCount*2 bailout, before
// this check is ever reached) — there are 14 rows here, comfortably past
// that gate, and both gutters clear columnMinLineCount on their own.
func TestDetectColumnBands_SparseSecondBand_FallsBack(t *testing.T) {
	var spans []pdf.TextSpan
	y := 750.0
	// 6 rows supporting only g1≈200 (colA + colBC merged, no gap near 400):
	// kept intact, but their gap still trusts g1.
	for i := 0; i < 6; i++ {
		spans = append(spans,
			pdf.TextSpan{X: 0, EndX: 150, Y: y, FontSize: 10, Text: "colA"},
			pdf.TextSpan{X: 250, EndX: 500, Y: y, FontSize: 10, Text: "colBC"},
		)
		y -= 14
	}
	// 6 rows supporting only g2≈400 (colAB merged + colC, no gap near 200):
	// kept intact, but their gap still trusts g2.
	for i := 0; i < 6; i++ {
		spans = append(spans,
			pdf.TextSpan{X: 0, EndX: 350, Y: y, FontSize: 10, Text: "colAB"},
			pdf.TextSpan{X: 450, EndX: 500, Y: y, FontSize: 10, Text: "colC"},
		)
		y -= 14
	}
	// Only 2 rows that genuinely support both gutters and get split into
	// all three bands — too few to populate bands 1 and 2 above threshold.
	for i := 0; i < 2; i++ {
		spans = append(spans,
			pdf.TextSpan{X: 0, EndX: 150, Y: y, FontSize: 10, Text: "colA"},
			pdf.TextSpan{X: 250, EndX: 350, Y: y, FontSize: 10, Text: "colB"},
			pdf.TextSpan{X: 450, EndX: 500, Y: y, FontSize: 10, Text: "colC"},
		)
		y -= 14
	}

	if bands := detectColumnBands(spans); bands != nil {
		t.Errorf("expected nil (fall back) when a trusted gutter's resulting band is too sparse, got %d bands", len(bands))
	}
}

// TestDetectColumnBands_FullWidthRow_KeptIntact guards against tearing a
// title/header row apart at a column gutter established by the rows around
// it. The title's own internal gaps are all well under
// columnRowGapMinAbs, so it has no supporting gap near the gutter — it
// must be kept in a single band, even though the gutter position (285)
// falls inside one of its words' own span.
func TestDetectColumnBands_FullWidthRow_KeptIntact(t *testing.T) {
	var spans []pdf.TextSpan
	y := 750.0
	for i := 0; i < 9; i++ {
		spans = append(spans,
			pdf.TextSpan{X: 72, EndX: 250, Y: y, FontSize: 10, Text: "left column body text"},
			pdf.TextSpan{X: 320, EndX: 500, Y: y, FontSize: 10, Text: "right column body text"},
		)
		y -= 14
	}
	spans = append(spans,
		pdf.TextSpan{X: 72, EndX: 110, Y: y, FontSize: 10, Text: "Experimental"},
		pdf.TextSpan{X: 115, EndX: 170, Y: y, FontSize: 10, Text: "Results"},
		pdf.TextSpan{X: 175, EndX: 230, Y: y, FontSize: 10, Text: "and"},
		pdf.TextSpan{X: 235, EndX: 310, Y: y, FontSize: 10, Text: "Discussion"},
		pdf.TextSpan{X: 315, EndX: 390, Y: y, FontSize: 10, Text: "Section"},
	)

	bands := detectColumnBands(spans)
	if len(bands) != 2 {
		t.Fatalf("expected 2 bands, got %d", len(bands))
	}

	titleWords := []string{"Experimental", "Results", "and", "Discussion", "Section"}
	bandOfWord := map[string]int{}
	for bi, band := range bands {
		for _, s := range band {
			for _, w := range titleWords {
				if s.Text == w {
					bandOfWord[w] = bi
				}
			}
		}
	}
	if len(bandOfWord) != len(titleWords) {
		t.Fatalf("expected all %d title words present in output bands, found %d: %v", len(titleWords), len(bandOfWord), bandOfWord)
	}
	first := bandOfWord[titleWords[0]]
	for _, w := range titleWords {
		if b := bandOfWord[w]; b != first {
			t.Errorf("title row torn across bands: %q landed in band %d, %q landed in band %d", titleWords[0], first, w, b)
		}
	}
}

// TestDetectColumnBands_ThreeColumns_Detected guards the multi-gap-per-row
// capture: a 3-column row has two real gutters, and both must accumulate
// independent cluster evidence for a 3+-column page to be split correctly.
func TestDetectColumnBands_ThreeColumns_Detected(t *testing.T) {
	var spans []pdf.TextSpan
	y := 750.0
	for i := 0; i < 10; i++ {
		spans = append(spans,
			pdf.TextSpan{X: 72, EndX: 150, Y: y, FontSize: 10, Text: "col1 text"},
			pdf.TextSpan{X: 200, EndX: 280, Y: y, FontSize: 10, Text: "col2 text"},
			pdf.TextSpan{X: 330, EndX: 420, Y: y, FontSize: 10, Text: "col3 text"},
		)
		y -= 14
	}

	bands := detectColumnBands(spans)
	if len(bands) != 3 {
		t.Fatalf("expected 3 bands for a 3-column page, got %d", len(bands))
	}
	wantText := []string{"col1 text", "col2 text", "col3 text"}
	for bi, band := range bands {
		if len(band) != 10 {
			t.Errorf("band %d: got %d spans, want 10", bi, len(band))
		}
		for _, s := range band {
			if s.Text != wantText[bi] {
				t.Errorf("band %d contains %q, want only %q", bi, s.Text, wantText[bi])
			}
		}
	}
}

// TestDetectColumnBands_BoundedDrift_RejectsUnrelatedGutter constructs a
// page where each row's gap midpoint sits just inside columnGutterClusterTol
// of the previous rows' running mean, always in the same direction — the
// chained-small-steps drift a running mean is vulnerable to. By row 33 the
// mean has walked far enough that the next candidate is still within
// tolerance of the (already drifted) mean but more than
// columnGutterClusterMaxDrift from the anchor that started the cluster, so
// it must be rejected into a second, independently-trusted gutter rather
// than folded into the first. Without that bound, all 36 rows merge into
// one over-averaged gutter and the page reports one column split instead
// of two.
func TestDetectColumnBands_BoundedDrift_RejectsUnrelatedGutter(t *testing.T) {
	mids := []float64{
		100.0, 111.99, 117.985, 121.982, 124.979, 127.377, 129.375, 131.088, 132.587,
		133.919, 135.118, 136.208, 137.207, 138.13, 138.986, 139.786, 140.535, 141.24,
		141.906, 142.537, 143.137, 143.708, 144.253, 144.774, 145.274, 145.753, 146.214,
		146.659, 147.087, 147.5, 147.9, 148.287, 160.277, 166.272, 170.268, 173.266,
	}

	var spans []pdf.TextSpan
	y := 750.0
	for _, mid := range mids {
		spans = append(spans,
			pdf.TextSpan{X: mid - 120, EndX: mid - 20, Y: y, FontSize: 10, Text: "L"},
			pdf.TextSpan{X: mid + 20, EndX: mid + 120, Y: y, FontSize: 10, Text: "R"},
		)
		y -= 12
	}

	bands := detectColumnBands(spans)
	if len(bands) != 3 {
		t.Fatalf("expected the drifted sequence to split into 2 gutters (3 bands), got %d bands — bounded drift not enforced", len(bands))
	}
	wantCounts := []int{38, 19, 15}
	for i, want := range wantCounts {
		if got := len(bands[i]); got != want {
			t.Errorf("band %d: got %d spans, want %d", i, got, want)
		}
	}
}

// TestDetectColumnBands_AdversarialGaps_BoundedCost guards against
// unbounded CPU cost on adversarial input. documents.Parse runs on
// untrusted, remotely fetched PDF bytes (up to MaxDocumentBytes, default
// 50MB) with no timeout wrapping the parse itself, so an unbounded
// algorithmic-complexity blowup inside detectColumnBands is a real
// resource-exhaustion risk. Without columnMaxClusters, every gap that
// doesn't match an existing cluster grows the cluster list, and each new
// gap is matched against every existing cluster — a page of spans placed
// so no two gaps ever cluster (as constructed here) makes that scan
// O(rows^2). This builds exactly that adversarial shape at a scale where
// the uncapped version measurably exceeds the budget below (confirmed by
// reverting the cap and rerunning: it times out), and asserts the capped
// version finishes well within it. n and the timeout are both sized with
// generous headroom over local measurements (capped cost finishes in
// ~0.25s under -race) so the assertion holds on slower/shared CI runners
// too, not just on the machine that wrote it — a tight wall-clock budget
// here would make the test flaky rather than a real cost guard.
func TestDetectColumnBands_AdversarialGaps_BoundedCost(t *testing.T) {
	const n = 150000
	spans := make([]pdf.TextSpan, 0, n*2)
	y := 750000.0
	for i := 0; i < n; i++ {
		mid := float64(i) * 1000.0 // every gap far from every other -> never clusters
		spans = append(spans,
			pdf.TextSpan{X: mid - 500, EndX: mid - 100, Y: y, FontSize: 10, Text: "L"},
			pdf.TextSpan{X: mid + 100, EndX: mid + 500, Y: y, FontSize: 10, Text: "R"},
		)
		y -= 12
	}

	done := make(chan struct{})
	go func() {
		detectColumnBands(spans)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("detectColumnBands took longer than 20s on adversarial non-clustering gaps — columnMaxClusters cap not bounding cost")
	}
}
