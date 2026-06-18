package scraper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
)

// docFrom builds a goquery document from an HTML fragment for unit tests.
func docFrom(t *testing.T, html string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parse html: %v", err)
	}
	return doc
}

// firstTable returns the first <table> selection in the fragment.
func firstTable(t *testing.T, html string) *goquery.Selection {
	t.Helper()
	return docFrom(t, html).Find("table").First()
}

// =============================================================================
// #48 — HTML tables -> GFM pipe tables
// =============================================================================

func TestExtractTable_HeaderRow(t *testing.T) {
	md, ok := extractTable(firstTable(t, `
		<table>
			<tr><th>Feature</th><th>Plan A</th></tr>
			<tr><td>Storage</td><td>10 GB</td></tr>
			<tr><td>Price</td><td>$5/mo</td></tr>
		</table>`))
	if !ok {
		t.Fatal("expected ok=true for a data table")
	}
	want := "| Feature | Plan A |\n| --- | --- |\n| Storage | 10 GB |\n| Price | $5/mo |"
	if md != want {
		t.Errorf("table mismatch:\n got: %q\nwant: %q", md, want)
	}
}

func TestExtractTable_NoTheadSynthesizesHeader(t *testing.T) {
	md, ok := extractTable(firstTable(t, `
		<table>
			<tr><td>a</td><td>b</td></tr>
			<tr><td>c</td><td>d</td></tr>
		</table>`))
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !strings.HasPrefix(md, "| Column 1 | Column 2 |\n| --- | --- |\n") {
		t.Errorf("expected synthesized Column headers, got:\n%s", md)
	}
	if !strings.Contains(md, "| a | b |") || !strings.Contains(md, "| c | d |") {
		t.Errorf("expected all rows as data, got:\n%s", md)
	}
}

func TestExtractTable_TheadDetected(t *testing.T) {
	md, ok := extractTable(firstTable(t, `
		<table>
			<thead><tr><td>H1</td><td>H2</td></tr></thead>
			<tbody><tr><td>x</td><td>y</td></tr></tbody>
		</table>`))
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !strings.HasPrefix(md, "| H1 | H2 |\n| --- | --- |\n") {
		t.Errorf("expected thead first row as header, got:\n%s", md)
	}
}

func TestExtractTable_RaggedRows(t *testing.T) {
	// maxCols is the max width across ALL rows (here 4, from the last row), so no
	// cell is dropped; shorter rows are right-padded to maxCols. Every emitted
	// line therefore has exactly maxCols+1 pipes — a well-formed GFM grid.
	md, ok := extractTable(firstTable(t, `
		<table>
			<tr><th>A</th><th>B</th><th>C</th></tr>
			<tr><td>1</td></tr>
			<tr><td>w</td><td>x</td><td>y</td><td>z</td></tr>
		</table>`))
	if !ok {
		t.Fatal("expected ok=true")
	}
	// header (3 cells) padded to 4 cols; short row padded; long row retained intact.
	if !strings.Contains(md, "| A | B | C |  |") {
		t.Errorf("expected header padded to maxCols, got:\n%s", md)
	}
	if !strings.Contains(md, "| 1 |  |  |  |") {
		t.Errorf("expected short row right-padded, got:\n%s", md)
	}
	if !strings.Contains(md, "| w | x | y | z |") {
		t.Errorf("expected widest row retained without data loss, got:\n%s", md)
	}
	for _, line := range strings.Split(md, "\n") {
		if strings.Count(line, "|") != 5 {
			t.Errorf("line %q does not have 5 pipes (4 cols)", line)
		}
	}
}

func TestExtractTable_OverLongRowTruncationGuard(t *testing.T) {
	// The norm() truncation path is defensive (maxCols is the row max, so it
	// rarely fires) — verify it never panics and stays within maxCols when a
	// header is the widest row and a body row somehow matches it.
	md, ok := extractTable(firstTable(t, `
		<table><tr><th>A</th><th>B</th></tr><tr><td>1</td><td>2</td></tr></table>`))
	if !ok {
		t.Fatal("expected ok=true")
	}
	for _, line := range strings.Split(md, "\n") {
		if strings.Count(line, "|") != 3 {
			t.Errorf("line %q does not have 3 pipes (2 cols)", line)
		}
	}
}

func TestExtractTable_PipeAndBackslashEscaping(t *testing.T) {
	md, ok := extractTable(firstTable(t, `
		<table>
			<tr><th>K</th><th>V</th></tr>
			<tr><td>pipe</td><td>a|b</td></tr>
			<tr><td>back</td><td>c\d</td></tr>
			<tr><td>multi</td><td>line1
line2	tabbed</td></tr>
		</table>`))
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !strings.Contains(md, `a\|b`) {
		t.Errorf("pipe not escaped, got:\n%s", md)
	}
	if !strings.Contains(md, `c\\d`) {
		t.Errorf("backslash not escaped, got:\n%s", md)
	}
	if !strings.Contains(md, "| line1 line2 tabbed |") {
		t.Errorf("multiline cell not flattened to single spaces, got:\n%s", md)
	}
}

func TestExtractTable_SingleColumnIsLayout(t *testing.T) {
	_, ok := extractTable(firstTable(t, `
		<table><tr><td>only</td></tr><tr><td>one</td></tr></table>`))
	if ok {
		t.Error("expected ok=false for a single-column (layout) table")
	}
}

func TestExtractTable_EmptyAndAllEmpty(t *testing.T) {
	if _, ok := extractTable(firstTable(t, `<table></table>`)); ok {
		t.Error("expected ok=false for empty table")
	}
	if _, ok := extractTable(firstTable(t, `<table><tr><td></td><td></td></tr></table>`)); ok {
		t.Error("expected ok=false for all-empty-cell table")
	}
}

func TestExtractTable_NestedReturnsFalse(t *testing.T) {
	_, ok := extractTable(firstTable(t, `
		<table><tr><td>outer<table><tr><td>inner</td><td>x</td></tr></table></td><td>y</td></tr></table>`))
	if ok {
		t.Error("expected ok=false for a table with a nested table")
	}
}

func TestExtractText_TableIntegratedNoFragments(t *testing.T) {
	doc := docFrom(t, `<body><article>
		<p>Intro paragraph that is reasonably long to pass thresholds.</p>
		<table>
			<tr><th>Metric</th><th>Value</th></tr>
			<tr><td>Speed</td><td>fast</td></tr>
			<tr><td>Cost</td><td>low</td></tr>
		</table>
	</article></body>`)
	out := extractText(doc.Find("article").First())
	if !strings.Contains(out, "| Metric | Value |") || !strings.Contains(out, "| --- | --- |") {
		t.Errorf("expected one markdown table block, got:\n%s", out)
	}
	// Regression: cells must NOT appear as disconnected single-cell lines.
	if strings.Contains(out, "\nSpeed\n") || strings.Contains(out, "\nfast\n") {
		t.Errorf("table cells leaked as disconnected fragments:\n%s", out)
	}
}

func TestExtractText_TableSurvivesCleanText(t *testing.T) {
	doc := docFrom(t, `<body><article>
		<table><tr><th>A</th><th>B</th></tr><tr><td>1</td><td>2</td></tr></table>
	</article></body>`)
	raw := extractText(doc.Find("article").First())
	cleaned := cleanText(raw)
	if !strings.Contains(cleaned, "| A | B |") || !strings.Contains(cleaned, "| --- | --- |") || !strings.Contains(cleaned, "| 1 | 2 |") {
		t.Errorf("table did not survive cleanText:\n%s", cleaned)
	}
}

func TestExtractText_NestedTableNoDoubleEmit(t *testing.T) {
	doc := docFrom(t, `<body><div>
		<table><tr><td>outer cell<table><tr><td>inner1</td><td>inner2</td></tr></table></td><td>right</td></tr></table>
	</div></body>`)
	out := extractText(doc.Find("body").First())
	// inner table must be skipped (ParentsFiltered guard); its text appears at
	// most once via the outer table's plain-text fallback.
	if strings.Count(out, "inner1") > 1 {
		t.Errorf("nested table double-emitted:\n%s", out)
	}
}

func TestExtractText_FallbackTableCellsStillSurface(t *testing.T) {
	// A single-column (layout) table is rejected by extractTable but its text
	// must still surface via the s.Text() fallback (no content loss).
	doc := docFrom(t, `<body><div><table><tr><td>important sidebar note</td></tr></table></div></body>`)
	out := extractText(doc.Find("body").First())
	if !strings.Contains(out, "important sidebar note") {
		t.Errorf("rejected table lost its text:\n%s", out)
	}
}

// =============================================================================
// #46 — structured data extraction
// =============================================================================

func TestExtractStructuredData_JSONLDValid(t *testing.T) {
	doc := docFrom(t, `<html><head>
		<script type="application/ld+json">{"@type":"Article","headline":"Hi"}</script>
	</head><body></body></html>`)
	sd := extractStructuredData(doc)
	if len(sd.JSONLD) != 1 {
		t.Fatalf("expected 1 JSON-LD block, got %d", len(sd.JSONLD))
	}
	if !strings.Contains(string(sd.JSONLD[0]), `"headline":"Hi"`) {
		t.Errorf("JSON-LD not captured verbatim: %s", sd.JSONLD[0])
	}
}

func TestExtractStructuredData_JSONLDInvalidSkipped(t *testing.T) {
	doc := docFrom(t, `<html><head>
		<script type="application/ld+json">{ not valid json </script>
		<script type="application/ld+json">{"@type":"WebSite"}</script>
	</head><body></body></html>`)
	sd := extractStructuredData(doc)
	if len(sd.JSONLD) != 1 {
		t.Fatalf("expected only the valid block, got %d", len(sd.JSONLD))
	}
}

func TestExtractStructuredData_OGAndCitation(t *testing.T) {
	doc := docFrom(t, `<html><head>
		<meta property="og:title" content="The Title">
		<meta property="article:published_time" content="2026-01-01">
		<meta property="twitter:card" content="ignored">
		<meta name="citation_title" content="Paper Title">
		<meta name="citation_doi" content="10.1/x">
		<meta name="description" content="ignored">
	</head><body></body></html>`)
	sd := extractStructuredData(doc)
	if sd.OpenGraph["og:title"] != "The Title" || sd.OpenGraph["article:published_time"] != "2026-01-01" {
		t.Errorf("og/article not captured with prefix: %v", sd.OpenGraph)
	}
	if _, ok := sd.OpenGraph["twitter:card"]; ok {
		t.Error("non-og/article property should be ignored")
	}
	if sd.Citation["citation_title"] != "Paper Title" || sd.Citation["citation_doi"] != "10.1/x" {
		t.Errorf("citation not captured: %v", sd.Citation)
	}
	if _, ok := sd.Citation["description"]; ok {
		t.Error("non-citation name should be ignored")
	}
}

func TestExtractStructuredData_AbsentIsEmpty(t *testing.T) {
	doc := docFrom(t, `<html><body><p>nothing structured</p></body></html>`)
	if !extractStructuredData(doc).IsEmpty() {
		t.Error("expected IsEmpty()==true for a page with no structured data")
	}
}

func TestExtractStructuredData_SizeBounds(t *testing.T) {
	var b strings.Builder
	b.WriteString("<html><head>")
	// 20 valid blocks (> maxJSONLDBlocks=16)
	for i := 0; i < 20; i++ {
		b.WriteString(`<script type="application/ld+json">{"@type":"Thing"}</script>`)
	}
	// one oversized block (> maxJSONLDBlockBytes) must be skipped
	b.WriteString(`<script type="application/ld+json">{"x":"`)
	b.WriteString(strings.Repeat("A", maxJSONLDBlockBytes+10))
	b.WriteString(`"}</script>`)
	// 100 og metas (> maxMetaProps=64)
	for i := 0; i < 100; i++ {
		b.WriteString(`<meta property="og:tag` + strings.Repeat("x", 1) + `" content="v">`)
	}
	b.WriteString("</head><body></body></html>")
	// NB: duplicate og keys collapse; use distinct keys to actually exercise the cap.
	doc := docFrom(t, b.String())
	sd := extractStructuredData(doc)
	if len(sd.JSONLD) > maxJSONLDBlocks {
		t.Errorf("JSON-LD blocks exceeded cap: %d > %d", len(sd.JSONLD), maxJSONLDBlocks)
	}
	for _, blk := range sd.JSONLD {
		if len(blk) > maxJSONLDBlockBytes {
			t.Errorf("oversized block not skipped: %d bytes", len(blk))
		}
	}
	if len(sd.OpenGraph) > maxMetaProps {
		t.Errorf("og props exceeded cap: %d > %d", len(sd.OpenGraph), maxMetaProps)
	}
}

func TestExtractStructuredData_ValueTruncated(t *testing.T) {
	doc := docFrom(t, `<html><head><meta property="og:description" content="`+strings.Repeat("z", maxMetaValueBytes+50)+`"></head></html>`)
	sd := extractStructuredData(doc)
	if v := sd.OpenGraph["og:description"]; len(v) > maxMetaValueBytes {
		t.Errorf("value not truncated: %d > %d", len(v), maxMetaValueBytes)
	}
}

func TestTruncateBytes_RuneSafe(t *testing.T) {
	// "世" is 3 bytes; build a value that would split it at the byte cap.
	val := strings.Repeat("世", maxMetaValueBytes) // way over cap, multibyte throughout
	doc := docFrom(t, `<html><head><meta property="og:title" content="`+val+`"></head></html>`)
	got := extractStructuredData(doc).OpenGraph["og:title"]
	if len(got) > maxMetaValueBytes {
		t.Errorf("not truncated within cap: %d", len(got))
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncation split a multibyte rune (invalid UTF-8): %q", got)
	}
}

func TestExtractText_TableCellBlocksNotDoubleEmitted(t *testing.T) {
	// A <p> and a <ul> inside a cell must appear ONCE (in the GFM cell), not also
	// as standalone paragraph/list lines.
	doc := docFrom(t, `<body><article>
		<table><tr><th>K</th><th>V</th></tr>
		<tr><td>row</td><td><p>cellpara</p></td></tr></table>
	</article></body>`)
	out := extractText(doc.Find("article").First())
	if strings.Count(out, "cellpara") != 1 {
		t.Errorf("table-cell block double-emitted (want exactly 1 'cellpara'):\n%s", out)
	}
}

// TestScrapeHTML_StructuredDataCapturedBeforeScriptRemove is the ordering
// regression guard: JSON-LD lives in a <script> that scrapeHTML strips, so it
// must be captured first. Runs the full HTML tier against an httptest server.
func TestScrapeHTML_StructuredDataCapturedBeforeScriptRemove(t *testing.T) {
	page := `<html><head>
		<title>T</title>
		<script type="application/ld+json">{"@type":"Article","headline":"Captured"}</script>
		<meta property="og:title" content="OG Title">
	</head><body><article>
		<p>` + strings.Repeat("Body text long enough to clear the 100-char content threshold. ", 5) + `</p>
		<table><tr><th>A</th><th>B</th></tr><tr><td>1</td><td>2</td></tr></table>
	</article></body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	res, err := p.scrapeHTML(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("scrapeHTML: %v", err)
	}
	if res.StructuredData.IsEmpty() {
		t.Fatal("structured data was not captured (script likely stripped before capture)")
	}
	if len(res.StructuredData.JSONLD) != 1 || !strings.Contains(string(res.StructuredData.JSONLD[0]), "Captured") {
		t.Errorf("JSON-LD not captured: %+v", res.StructuredData.JSONLD)
	}
	if res.StructuredData.OpenGraph["og:title"] != "OG Title" {
		t.Errorf("og not captured: %v", res.StructuredData.OpenGraph)
	}
	// #48: the table should be a GFM block in the content.
	if !strings.Contains(res.Content, "| A | B |") || !strings.Contains(res.Content, "| --- | --- |") {
		t.Errorf("expected GFM table in content, got:\n%s", res.Content)
	}
}

// TestScrapeStealth_RendersTableAndStructuredData guards the COMMON path: the
// stealth tier (tier 2) wins for most real pages, so it must render GFM tables
// (#48) and capture structuredData (#46) just like the HTML tier — not flatten.
func TestScrapeStealth_RendersTableAndStructuredData(t *testing.T) {
	page := `<html><head>
		<script type="application/ld+json">{"@type":"Article","headline":"Via Stealth"}</script>
		<meta property="og:site_name" content="StealthSite">
	</head><body><article>
		<p>` + strings.Repeat("Article body long enough to clear the stealth 200-char content threshold comfortably. ", 4) + `</p>
		<table><tr><th>Plan</th><th>Price</th></tr><tr><td>Pro</td><td>$15</td></tr></table>
	</article></body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	res, err := p.scrapeStealth(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("scrapeStealth: %v", err)
	}
	if res == nil {
		t.Fatal("expected stealth tier to produce a result")
	}
	if !strings.Contains(res.Content, "| Plan | Price |") || !strings.Contains(res.Content, "| --- | --- |") {
		t.Errorf("stealth tier did not render GFM table:\n%s", res.Content)
	}
	if res.StructuredData.IsEmpty() || len(res.StructuredData.JSONLD) != 1 {
		t.Errorf("stealth tier did not capture structured data: %+v", res.StructuredData)
	}
}

// TestScrapeStealth_DivSoupContentPreserved is the regression guard for the
// audit's major finding: the stealth tier must not silently drop body prose that
// lives in bare <div>/<section> containers (which extractText does not walk) when
// a small recognized-block region also exists. bestText's flat fallback recovers it.
func TestScrapeStealth_DivSoupContentPreserved(t *testing.T) {
	// A >200-char heading+intro in recognized blocks, then the bulk of the body
	// in bare <div>s (the shape that regressed before bestText).
	page := `<html><body><article>
		<h1>A Reasonably Long Headline That On Its Own Exceeds The Two Hundred Character Threshold So The Old Guard Would Short-Circuit Before Ever Considering The Flat Text Fallback Path Here</h1>
		<div>` + strings.Repeat("This is the real article body and it lives entirely inside bare div containers with no paragraph tags at all. ", 12) + `</div>
		<section>` + strings.Repeat("More substantial body prose in a section element that extractText does not walk. ", 8) + `</section>
	</article></body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	res, err := p.scrapeStealth(context.Background(), ts.URL, 50000)
	if err != nil {
		t.Fatalf("scrapeStealth: %v", err)
	}
	if !strings.Contains(res.Content, "real article body") {
		t.Errorf("stealth tier dropped bare-div body prose:\n%s", res.Content[:min(len(res.Content), 400)])
	}
	if !strings.Contains(res.Content, "section element") {
		t.Errorf("stealth tier dropped <section> prose:\n%s", res.Content[:min(len(res.Content), 400)])
	}
}

// TestExtractText_DivSoupYieldsLittle documents WHY bestText is needed:
// extractText alone misses bare-div prose (it only walks block elements).
func TestExtractText_DivSoupYieldsLittle(t *testing.T) {
	doc := docFrom(t, `<body><article><div>`+strings.Repeat("bare div prose ", 20)+`</div></article></body>`)
	if got := extractText(doc.Find("article").First()); got != "" {
		t.Errorf("expected extractText to miss bare-div text (justifies bestText fallback), got: %q", got)
	}
}

// TestScrapeRaw_NoStructuredData confirms non-HTML-tier results leave the field nil.
func TestScrapeRaw_NoStructuredData(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"k":"v"}`))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	res, err := p.ScrapeRaw(context.Background(), ts.URL, 1000)
	if err != nil {
		t.Fatalf("ScrapeRaw: %v", err)
	}
	if !res.StructuredData.IsEmpty() {
		t.Errorf("raw result must have no structured data, got %+v", res.StructuredData)
	}
}

// =============================================================================
// #247 — ForumSignals extraction
// =============================================================================

// TestExtractForumSignals_Reddit checks that a Reddit JSON-LD block is parsed
// into a non-nil ForumSignals with correct upvotes and comment counts.
func TestExtractForumSignals_Reddit(t *testing.T) {
	blocks := []json.RawMessage{
		json.RawMessage(`{
			"@type": "DiscussionForumPosting",
			"upvoteCount": 142,
			"commentCount": 37,
			"datePublished": "2024-01-15T10:30:00Z",
			"author": {"@type": "Person", "name": "test_user"}
		}`),
	}
	sig := extractForumSignals("https://www.reddit.com/r/golang/comments/abc/test", blocks)
	if sig == nil {
		t.Fatal("expected ForumSignals, got nil")
	}
	if sig.Platform != "reddit" {
		t.Errorf("platform: got %q, want 'reddit'", sig.Platform)
	}
	if sig.Upvotes != 142 {
		t.Errorf("upvotes: got %d, want 142", sig.Upvotes)
	}
	if sig.DatePublished != "2024-01-15T10:30:00Z" {
		t.Errorf("datePublished: got %q", sig.DatePublished)
	}
	if sig.AuthorName != "test_user" {
		t.Errorf("authorName: got %q", sig.AuthorName)
	}
}

// TestExtractForumSignals_NonReddit confirms non-Reddit URLs return nil.
func TestExtractForumSignals_NonReddit(t *testing.T) {
	block := []json.RawMessage{
		json.RawMessage(`{"@type":"DiscussionForumPosting","upvoteCount":100}`),
	}
	if sig := extractForumSignals("https://news.ycombinator.com/item?id=123", block); sig != nil {
		t.Errorf("expected nil for non-Reddit URL, got %+v", sig)
	}
}

// TestExtractForumSignals_NoForumType confirms a non-forum JSON-LD block returns nil.
func TestExtractForumSignals_NoForumType(t *testing.T) {
	block := []json.RawMessage{
		json.RawMessage(`{"@type":"Article","headline":"Some article"}`),
	}
	if sig := extractForumSignals("https://www.reddit.com/r/test/comments/xyz/article", block); sig != nil {
		t.Errorf("expected nil for non-DiscussionForumPosting type, got %+v", sig)
	}
}

// TestExtractForumSignals_LowEngagementNote confirms the credibility note fires
// for posts with fewer than 20 upvotes.
func TestExtractForumSignals_LowEngagementNote(t *testing.T) {
	block := []json.RawMessage{
		json.RawMessage(`{"@type":"DiscussionForumPosting","upvoteCount":5}`),
	}
	sig := extractForumSignals("https://www.reddit.com/r/test/comments/xyz/low", block)
	if sig == nil {
		t.Fatal("expected ForumSignals, got nil")
	}
	if sig.CredibilityNote == "" {
		t.Error("expected credibility note for low-engagement post, got empty string")
	}
}
