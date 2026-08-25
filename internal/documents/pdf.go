package documents

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/razvandimescu/gopdf/pdf"
)

// Column-detection tuning. These stay well above ordinary word/sentence
// spacing so single-column pages (the overwhelming common case) never trip
// a false column split — see detectColumnBands for the invariants each
// constant protects.
const (
	// columnRowYTolerance mirrors the vendored library's own lineYTolerance
	// (pdf/text.go): how far apart two spans' baselines may sit and still
	// be read as the same physical row. Using the same value means the
	// "rows" analyzed here are exactly the rows BuildLines would otherwise
	// merge into one bogus line across a column boundary.
	columnRowYTolerance = 1.0
	// columnRowGapMinAbs is the minimum horizontal gap (points) within a
	// single row for that gap to be considered as a candidate column
	// gutter, rather than ordinary inter-word spacing (justified text
	// rarely stretches a single gap this wide in body-sized fonts).
	columnRowGapMinAbs = 15.0
	// columnGutterClusterTol is how close two rows' candidate gutter
	// midpoints must be (points) to count as evidence for the same
	// physical gutter.
	columnGutterClusterTol = 12.0
	// columnMinLineCount is the minimum number of rows that must agree on
	// the same gutter position before it is trusted as a real column
	// boundary, and the minimum non-blank spans any resulting band must
	// contain to be kept. A handful of coincidentally wide word-gaps
	// scattered around a single-column page — at inconsistent X
	// positions — must not be able to manufacture a false column split.
	columnMinLineCount = 5
)

func parsePDF(_ io.ReaderAt, size int64, data []byte) (string, Metadata, error) {
	meta := Metadata{
		FileSize: size,
	}

	if len(data) < 4 || string(data[:4]) != "%PDF" {
		return "", meta, fmt.Errorf("not a valid PDF file")
	}

	doc, err := pdf.OpenBytes(data)
	if err != nil {
		return "", meta, fmt.Errorf("failed to parse PDF: %w", err)
	}

	meta.PageCount = doc.NumPages()

	var sb strings.Builder
	for i := 0; i < doc.NumPages(); i++ {
		page := doc.Page(i)
		if page == nil {
			continue
		}
		spans, err := page.TextSpans()
		if err != nil {
			return "", meta, fmt.Errorf("failed to extract page %d text: %w", i, err)
		}
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(pageTextInReadingOrder(spans))
	}

	text := sb.String()
	if text == "" {
		return "", meta, fmt.Errorf("no extractable text in PDF (may be image-based)")
	}

	return text, meta, nil
}

// pageTextInReadingOrder reconstructs a page's text, correcting for
// multi-column layouts. When detectColumnBands finds no clear, well
// supported column gutter (the common single-column case), this produces
// byte-identical output to the library's own Page.Text() — spans are
// passed through to pdf.BuildLines unmodified, in their original order.
func pageTextInReadingOrder(spans []pdf.TextSpan) string {
	bands := detectColumnBands(spans)
	if bands == nil {
		return joinLines(pdf.BuildLines(spans))
	}

	var sb strings.Builder
	for i, band := range bands {
		if i > 0 && sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(joinLines(pdf.BuildLines(band)))
	}
	return sb.String()
}

// joinLines mirrors gopdf's own pdf.Page.Text() line-joining behavior
// exactly, so the single-column fallback path stays byte-identical.
func joinLines(lines []pdf.TextLine) string {
	var sb strings.Builder
	for i, line := range lines {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(line.Text)
	}
	return sb.String()
}

// detectColumnBands partitions a page's text spans into left-to-right
// reading columns (nil = single column, no split needed — the correct
// outcome for the vast majority of PDFs).
//
// It works row by row, not on the page as a whole. For each physical row
// (spans within columnRowYTolerance of the same Y — exactly the grouping
// pdf.BuildLines uses, which is the mechanism that interleaves two
// columns' spans into one bogus line in the first place), it finds that
// row's own single largest internal horizontal gap. A real column gutter
// leaves a strip of the page with nothing printed in it, so on any row
// that has content in both columns, the gutter gap is necessarily the
// widest gap in that row — regardless of how wide the gutter itself is,
// and without needing an absolute width threshold tuned to the specific
// page (columnRowGapMinAbs only filters out ordinary word spacing).
//
// A single wide gap proves nothing on its own — one long single-column
// sentence with uneven spacing can produce one. The real signal is
// consistency: a genuine gutter produces a wide gap, at very nearly the
// same X, on many rows across the page. Gaps from ordinary spacing
// variance land at essentially random X positions and never cluster.
// Requiring columnMinLineCount independent rows to agree on the same
// gutter position (within columnGutterClusterTol) is what tells the two
// apart, and is the guarantee that single-column pages are unaffected.
func detectColumnBands(spans []pdf.TextSpan) [][]pdf.TextSpan {
	rows := groupSpansIntoRows(spans)
	if len(rows) < columnMinLineCount*2 {
		return nil // not enough rows on the page to trust a column split
	}

	type gutterCluster struct {
		x     float64 // running centroid of member rows' gap midpoints
		count int
	}
	var clusters []gutterCluster

	for _, row := range rows {
		nb := make([]pdf.TextSpan, 0, len(row))
		for _, s := range row {
			if strings.TrimSpace(s.Text) != "" {
				nb = append(nb, s)
			}
		}
		if len(nb) < 2 {
			continue // nothing to compare a gap against on this row
		}
		sort.Slice(nb, func(i, j int) bool { return nb[i].X < nb[j].X })

		maxGap, maxMid := -1.0, 0.0
		for i := 1; i < len(nb); i++ {
			end := estimateSpanEnd(nb[i-1])
			if gap := nb[i].X - end; gap > maxGap {
				maxGap, maxMid = gap, (end+nb[i].X)/2
			}
		}
		if maxGap < columnRowGapMinAbs {
			continue
		}

		matched := false
		for i := range clusters {
			if math.Abs(clusters[i].x-maxMid) <= columnGutterClusterTol {
				c := &clusters[i]
				c.x = (c.x*float64(c.count) + maxMid) / float64(c.count+1)
				c.count++
				matched = true
				break
			}
		}
		if !matched {
			clusters = append(clusters, gutterCluster{x: maxMid, count: 1})
		}
	}

	var gutters []float64
	for _, c := range clusters {
		if c.count >= columnMinLineCount {
			gutters = append(gutters, c.x)
		}
	}
	if len(gutters) == 0 {
		return nil // no gutter position consistently supported: single column
	}
	sort.Float64s(gutters)

	bandOf := func(x float64) int {
		band := 0
		for _, g := range gutters {
			if x >= g {
				band++
			}
		}
		return band
	}

	bands := make([][]pdf.TextSpan, len(gutters)+1)
	for _, s := range spans {
		b := bandOf(s.X)
		bands[b] = append(bands[b], s)
	}

	// Verify every resulting band is genuinely well-populated. A sparse
	// band means this split does not represent real reading columns —
	// fall back to the unmodified single-pass behavior rather than risk
	// fracturing prose that only looked like it had a second column.
	for _, b := range bands {
		nonBlankInBand := 0
		for _, s := range b {
			if strings.TrimSpace(s.Text) != "" {
				nonBlankInBand++
			}
		}
		if nonBlankInBand < columnMinLineCount {
			return nil
		}
	}

	return bands
}

// groupSpansIntoRows clusters spans into physical rows by Y coordinate,
// using the same tolerance and top-to-bottom ordering as the vendored
// library's BuildLines, without pdf.BuildLines's cross-column X-merge —
// spans within a row are kept as a plain slice, not joined into text, so
// each row's own internal gaps stay inspectable.
func groupSpansIntoRows(spans []pdf.TextSpan) [][]pdf.TextSpan {
	if len(spans) == 0 {
		return nil
	}
	sorted := make([]pdf.TextSpan, len(spans))
	copy(sorted, spans)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Y > sorted[j].Y })

	var rows [][]pdf.TextSpan
	var current []pdf.TextSpan
	currentY := sorted[0].Y
	for _, s := range sorted {
		if len(current) > 0 && math.Abs(s.Y-currentY) > columnRowYTolerance {
			rows = append(rows, current)
			current = nil
		}
		if len(current) == 0 {
			currentY = s.Y
		}
		current = append(current, s)
	}
	if len(current) > 0 {
		rows = append(rows, current)
	}
	return rows
}

// estimateSpanEnd mirrors the vendored library's private spanEnd fallback
// (pdf/text.go) for spans that did not record an explicit EndX.
func estimateSpanEnd(s pdf.TextSpan) float64 {
	if s.EndX > s.X {
		return s.EndX
	}
	return s.X + float64(utf8.RuneCountInString(s.Text))*s.FontSize*0.5
}
