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
	// columnGutterClusterMaxDrift caps how far a cluster's running-mean
	// gutter position may end up from the anchor point (the first row's
	// midpoint) that created it. Matching still happens against the
	// running mean, not the anchor, so the cluster can absorb the ordinary
	// row-to-row jitter that estimateSpanEnd's line-length-based
	// approximation introduces (different text per row estimates a
	// slightly different gap midpoint even at a fixed physical gutter).
	// Without this cap, a chain of small steps that each individually
	// clears columnGutterClusterTol could walk the mean arbitrarily far
	// from where the cluster actually started.
	columnGutterClusterMaxDrift = columnGutterClusterTol * 4
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
// columns' spans into one bogus line in the first place), it collects
// every internal horizontal gap wide enough to plausibly be a gutter
// (columnRowGapMinAbs — well above ordinary word spacing, so this filters
// out justified-text stretch, not just tunes to one page). A real column
// gutter leaves a strip of the page with nothing printed in it, so a row
// with content in N columns has (at least) N-1 such gaps; capturing all of
// them, not just the widest, is what lets a 3+-column row register
// evidence for every one of its gutters instead of just one.
//
// A single wide gap proves nothing on its own — one long single-column
// sentence with uneven spacing can produce one. The real signal is
// consistency: a genuine gutter produces a wide gap, at very nearly the
// same X, on many rows across the page. Gaps from ordinary spacing
// variance land at essentially random X positions and never cluster.
// Requiring columnMinLineCount independent rows to agree on the same
// gutter position is what tells the two apart, and is the guarantee that
// single-column pages are unaffected. A cluster's reported gutter position
// is a running mean of its member rows' midpoints — needed because
// estimateSpanEnd's line-length-based approximation puts the apparent
// midpoint at a slightly different X per row even at one fixed physical
// gutter — but columnGutterClusterMaxDrift bounds how far that mean may
// end up from the row that first created the cluster, so this row-to-row
// jitter tolerance can't be chained into unbounded drift.
//
// A row is only split across the resulting bands if it has its own
// supporting gap at every established gutter; a row that has content
// spanning across a gutter with no gap there (a title, header, footer, or
// table row crossing the column boundary) is kept intact in a single band
// instead, so partitioning never tears a full-width element apart
// mid-sentence.
func detectColumnBands(spans []pdf.TextSpan) [][]pdf.TextSpan {
	rows := groupSpansIntoRows(spans)
	if len(rows) < columnMinLineCount*2 {
		return nil // not enough rows on the page to trust a column split
	}

	type gutterCluster struct {
		anchor float64 // first row's gap midpoint that created this cluster — see columnGutterClusterMaxDrift
		x      float64 // running centroid of member rows' gap midpoints, reported as the final gutter position
		count  int
	}
	var clusters []gutterCluster

	// rowGapMids[i] holds every gutter-sized gap midpoint found on rows[i].
	// Capturing all of them, not just the widest, is what makes 3+-column
	// detection possible: a 3-column row has two real gutters, and both
	// must be able to accumulate cluster evidence. It's also reused below,
	// once gutters are finalized, to tell a genuine per-column row apart
	// from a full-width row that happens to have its own wide gaps (e.g. a
	// justified table row) but not at the established gutter positions.
	rowGapMids := make([][]float64, len(rows))

	for ri, row := range rows {
		nb := nonBlankSortedByX(row)
		if len(nb) < 2 {
			continue // nothing to compare a gap against on this row
		}

		var mids []float64
		for i := 1; i < len(nb); i++ {
			end := estimateSpanEnd(nb[i-1])
			if gap := nb[i].X - end; gap >= columnRowGapMinAbs {
				mids = append(mids, (end+nb[i].X)/2)
			}
		}
		rowGapMids[ri] = mids

		for _, mid := range mids {
			matched := false
			for i := range clusters {
				c := &clusters[i]
				if math.Abs(c.x-mid) <= columnGutterClusterTol && math.Abs(c.anchor-mid) <= columnGutterClusterMaxDrift {
					c.x = (c.x*float64(c.count) + mid) / float64(c.count+1)
					c.count++
					matched = true
					break
				}
			}
			if !matched {
				clusters = append(clusters, gutterCluster{anchor: mid, x: mid, count: 1})
			}
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

	// rowSpansAllGutters reports whether row ri has its own supporting gap
	// near every established gutter. A genuine per-column row does; a
	// full-width row (title, header, footer, table row) spanning across a
	// column boundary does not, since nothing separates its content there.
	// Uses columnGutterClusterMaxDrift, not columnGutterClusterTol: a row
	// whose own raw midpoint sits at the jittery edge of the cluster (close
	// enough to have been absorbed by the running mean earlier, or later,
	// in the same chain) must still count as supporting the gutter it
	// helped establish, even if it happens to be more than one tolerance
	// window away from the cluster's final reported mean.
	rowSpansAllGutters := func(ri int) bool {
		for _, g := range gutters {
			found := false
			for _, mid := range rowGapMids[ri] {
				if math.Abs(mid-g) <= columnGutterClusterMaxDrift {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}

	bands := make([][]pdf.TextSpan, len(gutters)+1)
	for ri, row := range rows {
		if rowSpansAllGutters(ri) {
			for _, s := range row {
				b := bandOf(s.X)
				bands[b] = append(bands[b], s)
			}
			continue
		}
		// Splitting this row by individual span X would tear a full-width
		// element apart mid-sentence across bands, so keep it intact in a
		// single band instead of partitioning it like a genuine per-column
		// row.
		b := 0
		if nb := nonBlankSortedByX(row); len(nb) > 0 {
			b = bandOf(nb[0].X)
		}
		bands[b] = append(bands[b], row...)
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

// nonBlankSortedByX returns row's non-blank spans, sorted left to right.
func nonBlankSortedByX(row []pdf.TextSpan) []pdf.TextSpan {
	nb := make([]pdf.TextSpan, 0, len(row))
	for _, s := range row {
		if strings.TrimSpace(s.Text) != "" {
			nb = append(nb, s)
		}
	}
	sort.Slice(nb, func(i, j int) bool { return nb[i].X < nb[j].X })
	return nb
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
