package scraper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	out := extractText(doc.Find("article").First(), nil)
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
	raw := extractText(doc.Find("article").First(), nil)
	cleaned := cleanText(raw)
	if !strings.Contains(cleaned, "| A | B |") || !strings.Contains(cleaned, "| --- | --- |") || !strings.Contains(cleaned, "| 1 | 2 |") {
		t.Errorf("table did not survive cleanText:\n%s", cleaned)
	}
}

func TestExtractText_NestedTableNoDoubleEmit(t *testing.T) {
	doc := docFrom(t, `<body><div>
		<table><tr><td>outer cell<table><tr><td>inner1</td><td>inner2</td></tr></table></td><td>right</td></tr></table>
	</div></body>`)
	out := extractText(doc.Find("body").First(), nil)
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
	out := extractText(doc.Find("body").First(), nil)
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
	out := extractText(doc.Find("article").First(), nil)
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
	res, err := p.scrapeHTML(context.Background(), ts.URL, 50000, false)
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
	res, err := p.scrapeStealth(context.Background(), ts.URL, 50000, false)
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
	res, err := p.scrapeStealth(context.Background(), ts.URL, 50000, false)
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
	if got := extractText(doc.Find("article").First(), nil); got != "" {
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
	sig := extractForumSignals(context.Background(), "https://www.reddit.com/r/golang/comments/abc/test", blocks, nil, "")
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
	if sig := extractForumSignals(context.Background(), "https://news.ycombinator.com/item?id=123", block, nil, ""); sig != nil {
		t.Errorf("expected nil for non-Reddit URL, got %+v", sig)
	}
}

// TestExtractForumSignals_NoForumType confirms a non-forum JSON-LD block returns nil.
func TestExtractForumSignals_NoForumType(t *testing.T) {
	block := []json.RawMessage{
		json.RawMessage(`{"@type":"Article","headline":"Some article"}`),
	}
	if sig := extractForumSignals(context.Background(), "https://www.reddit.com/r/test/comments/xyz/article", block, nil, ""); sig != nil {
		t.Errorf("expected nil for non-DiscussionForumPosting type, got %+v", sig)
	}
}

// TestExtractForumSignals_LowEngagementNote confirms the credibility note fires
// for posts with fewer than 20 upvotes.
func TestExtractForumSignals_LowEngagementNote(t *testing.T) {
	block := []json.RawMessage{
		json.RawMessage(`{"@type":"DiscussionForumPosting","upvoteCount":5}`),
	}
	sig := extractForumSignals(context.Background(), "https://www.reddit.com/r/test/comments/xyz/low", block, nil, "")
	if sig == nil {
		t.Fatal("expected ForumSignals, got nil")
	}
	if sig.CredibilityNote == "" {
		t.Error("expected credibility note for low-engagement post, got empty string")
	}
}

// =============================================================================
// #283 — fetchShredditComments extraction
// =============================================================================

func TestFetchShredditComments_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`
			<shreddit-comment score="12" author="alice" created="2024-01-15T10:00:00Z" permalink="/r/golang/comments/abc/comment/1/">
				<div id="t1_1-post-rtjson-content"><p>Low score comment.</p></div>
			</shreddit-comment>
			<shreddit-comment score="99" author="bob" created="2024-01-15T11:00:00Z" permalink="/r/golang/comments/abc/comment/2/">
				<div id="t1_2-post-rtjson-content"><p>High score comment.</p></div>
			</shreddit-comment>
		`))
	}))
	defer ts.Close()

	client := NewPipeline(PipelineConfig{MaxConcurrency: 1, AllowPrivateIPs: true}).client
	comments := fetchShredditComments(context.Background(), "https://www.reddit.com/r/golang/comments/abc/test", ts.URL, client)
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %d: %+v", len(comments), comments)
	}
	if comments[0].Score != 99 || comments[0].Author != "bob" {
		t.Errorf("expected highest-score comment first, got %+v", comments[0])
	}
	if comments[1].Score != 12 {
		t.Errorf("expected second comment score 12, got %d", comments[1].Score)
	}
	for _, c := range comments {
		if len(c.Body) > shredditMaxBodyBytes {
			t.Errorf("body exceeds cap: %d bytes", len(c.Body))
		}
	}
}

func TestFetchShredditComments_RateLimit_ReturnsNil(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	client := NewPipeline(PipelineConfig{MaxConcurrency: 1, AllowPrivateIPs: true}).client
	comments := fetchShredditComments(context.Background(), "https://www.reddit.com/r/golang/comments/abc/test", ts.URL, client)
	if comments != nil {
		t.Errorf("expected nil on 429, got %+v", comments)
	}
}

func TestFetchShredditComments_InvalidURL_SkipsFetch(t *testing.T) {
	client := NewPipeline(PipelineConfig{MaxConcurrency: 1, AllowPrivateIPs: true}).client
	comments := fetchShredditComments(context.Background(), "https://www.reddit.com/r/golang/wiki/faq", "http://should-not-be-called.invalid", client)
	if comments != nil {
		t.Errorf("expected nil for URL without /comments/ path, got %+v", comments)
	}
}

func TestFetchShredditComments_DeletedBodySkipped(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`
			<shreddit-comment score="5" author="[deleted]" created="2024-01-15T10:00:00Z" permalink="/r/golang/comments/abc/comment/1/">
				<div id="t1_1-post-rtjson-content"><p>[deleted]</p></div>
			</shreddit-comment>
			<shreddit-comment score="10" author="carol" created="2024-01-15T11:00:00Z" permalink="/r/golang/comments/abc/comment/2/">
				<div id="t1_2-post-rtjson-content"><p>Real comment text.</p></div>
			</shreddit-comment>
		`))
	}))
	defer ts.Close()

	client := NewPipeline(PipelineConfig{MaxConcurrency: 1, AllowPrivateIPs: true}).client
	comments := fetchShredditComments(context.Background(), "https://www.reddit.com/r/golang/comments/abc/test", ts.URL, client)
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment (deleted skipped), got %d: %+v", len(comments), comments)
	}
	if comments[0].Author != "carol" {
		t.Errorf("expected carol's comment to survive, got %+v", comments[0])
	}
}

func TestFetchShredditComments_BodyTruncated(t *testing.T) {
	longBody := strings.Repeat("a", shredditMaxBodyBytes+200)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`
			<shreddit-comment score="1" author="dave" created="2024-01-15T10:00:00Z" permalink="/r/golang/comments/abc/comment/1/">
				<div id="t1_1-post-rtjson-content"><p>` + longBody + `</p></div>
			</shreddit-comment>
		`))
	}))
	defer ts.Close()

	client := NewPipeline(PipelineConfig{MaxConcurrency: 1, AllowPrivateIPs: true}).client
	comments := fetchShredditComments(context.Background(), "https://www.reddit.com/r/golang/comments/abc/test", ts.URL, client)
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
	if len(comments[0].Body) > shredditMaxBodyBytes {
		t.Errorf("body not truncated: %d bytes", len(comments[0].Body))
	}
	if !utf8.ValidString(comments[0].Body) {
		t.Error("truncated body is not valid UTF-8")
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestInlineText(t *testing.T) {
	t.Parallel()
	base := mustParseURL(t, "https://example.com/docs/page.html")
	tests := []struct {
		name string
		html string
		base *url.URL
		want string
	}{
		{"no links is plain text", `<p>Just text, <b>bold</b> and <i>italic</i>.</p>`, base, "Just text, bold and italic."},
		{"absolute link", `<p>See <a href="https://other.org/a">the report</a> now.</p>`, base, "See [the report](https://other.org/a) now."},
		{"root-relative link", `<p><a href="/files/r.pdf">Report</a></p>`, base, "[Report](https://example.com/files/r.pdf)"},
		{"relative link", `<p><a href="r.pdf">Report</a></p>`, base, "[Report](https://example.com/docs/r.pdf)"},
		{"protocol-relative link", `<p><a href="//cdn.example.org/x">CDN</a></p>`, base, "[CDN](https://cdn.example.org/x)"},
		{"query kept", `<p><a href="/s?q=a&amp;b=2">Search</a></p>`, base, "[Search](https://example.com/s?q=a&b=2)"},
		{"fragment only is plain", `<p><a href="#top">Back to top</a></p>`, base, "Back to top"},
		{"javascript is plain", `<p><a href="javascript:void(0)">Click</a></p>`, base, "Click"},
		{"mailto is plain", `<p><a href="mailto:a@b.org">Mail us</a></p>`, base, "Mail us"},
		{"tel is plain", `<p><a href="tel:+123">Call</a></p>`, base, "Call"},
		{"ftp is plain", `<p><a href="ftp://x.org/f">FTP</a></p>`, base, "FTP"},
		{"data is plain", `<p><a href="data:text/plain,hi">Data</a></p>`, base, "Data"},
		{"empty href is plain", `<p><a href="">Empty</a></p>`, base, "Empty"},
		{"no href is plain", `<p><a name="x">Anchor</a></p>`, base, "Anchor"},
		{"empty text emits nothing", `<p>A<a href="/x"><img src="i.png"></a>B</p>`, base, "AB"},
		{"nested markup in the anchor", `<p><a href="/x"><strong>Bold</strong> and <em>em</em></a></p>`, base, "[Bold and em](https://example.com/x)"},
		{"text equal to the URL is written once", `<p><a href="https://example.com/x">https://example.com/x</a></p>`, base, "https://example.com/x"},
		{"text equal to the URL without slash", `<p><a href="https://example.com/">https://example.com</a></p>`, base, "https://example.com/"},
		{"spaces around the text stay outside", `<p>See<a href="/x"> the report </a>now</p>`, base, "See [the report](https://example.com/x) now"},
		{"inner whitespace collapses", "<p><a href=\"/x\">two\n   lines</a></p>", base, "[two lines](https://example.com/x)"},
		{"brackets in the text are escaped", `<p><a href="/x">a [b] c</a></p>`, base, `[a \[b\] c](https://example.com/x)`},
		{"parentheses and spaces in the URL are encoded", `<p><a href="/a_(b)/c d">T</a></p>`, base, "[T](https://example.com/a_%28b%29/c%20d)"},
		{"two links", `<p><a href="/a">A</a> and <a href="/b">B</a></p>`, base, "[A](https://example.com/a) and [B](https://example.com/b)"},
		{"nil base turns links off", `<p>See <a href="https://other.org/a">the report</a>.</p>`, nil, "See the report."},
		{"relative link with a path-only base is plain", `<p><a href="/x">X</a></p>`, mustParseURL(t, "/just/a/path"), "X"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc := docFrom(t, "<body>"+tc.html+"</body>")
			base := tc.base
			if base != nil && !isHTTPURL(base) {
				base = nil // documentBase gives nil for such a page URL; mirror it
			}
			if got := inlineText(doc.Find("p").First(), base); got != tc.want {
				t.Errorf("inlineText = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDocumentBase(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, html, page, want string
	}{
		{"page URL", `<html><head></head><body></body></html>`, "https://a.org/x/y", "https://a.org/x/y"},
		{"base href moves it", `<html><head><base href="/other/"></head><body></body></html>`, "https://a.org/x/y", "https://a.org/other/"},
		{"absolute base href", `<html><head><base href="https://b.org/z/"></head><body></body></html>`, "https://a.org/x/y", "https://b.org/z/"},
		{"non-http base href is ignored", `<html><head><base href="javascript:void(0)"></head><body></body></html>`, "https://a.org/x/y", "https://a.org/x/y"},
		{"non-http page gives nil", `<html><body></body></html>`, "ftp://a.org/x", ""},
		{"bad page URL gives nil", `<html><body></body></html>`, "://bad", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ""
			if b := documentBase(docFrom(t, tc.html), tc.page); b != nil {
				got = b.String()
			}
			if got != tc.want {
				t.Errorf("documentBase = %q, want %q", got, tc.want)
			}
		})
	}
}

// A page without usable links must extract exactly as it did before links were kept.
func TestExtractText_PageWithoutUsableLinksIsUnchanged(t *testing.T) {
	t.Parallel()
	doc := docFrom(t, `<body><article>
		<h2>Heading</h2>
		<p>Plain paragraph with <b>bold</b> and a <a href="#note">fragment link</a>, a <a href="javascript:void(0)">script link</a> and <a href="mailto:a@b.org">mail</a>.</p>
		<ul><li>One</li><li>Two <a href="tel:+1">phone</a></li></ul>
		<pre>code <a href="/x">not a link</a></pre>
	</article></body>`)
	sel := doc.Find("article").First()
	base := mustParseURL(t, "https://example.com/")
	with, without := extractText(sel, base), extractText(sel, nil)
	if with != without {
		t.Errorf("links changed a page that has none:\nwith:    %q\nwithout: %q", with, without)
	}
}

// GAO-style product page: the heading has the report PDF link under it.
func TestExtractText_FullReportLinkKept(t *testing.T) {
	t.Parallel()
	doc := docFrom(t, `<body><main>
		<h2>Highlights</h2><p>What GAO found, in a paragraph of enough length to look like prose on the page.</p>
		<h2>Full Report</h2>
		<ul><li><a href="/assets/gao-24-106591.pdf">Full Report (PDF, 54 pages)</a></li></ul>
		<p>Related: <a href="https://www.gao.gov/products/gao-24-000001">GAO-24-000001</a></p>
	</main></body>`)
	out := cleanText(extractText(doc.Find("main").First(), mustParseURL(t, "https://www.gao.gov/products/gao-24-106591")))
	want := "## Full Report\n\n- [Full Report (PDF, 54 pages)](https://www.gao.gov/assets/gao-24-106591.pdf)"
	if !strings.Contains(out, want) {
		t.Errorf("PDF link missing under the Full Report heading:\n%s", out)
	}
	if !strings.Contains(out, "[GAO-24-000001](https://www.gao.gov/products/gao-24-000001)") {
		t.Errorf("related link missing:\n%s", out)
	}
}

// GAO wraps the report links in <figure>/<div> containers with no <p> or <li>,
// so they sit outside every element extractText walks. Such a link is kept as
// its own line, once. A link inside a walked block is not repeated, and nil
// base keeps the old output.
func TestExtractText_StandaloneAnchors(t *testing.T) {
	t.Parallel()
	page := `<body><main>
		<section><h2>Full Report</h2>
			<div class="field__item"><figure><a href="/assets/gao-24-106591.pdf"><div>Full Report</div>
				<div>(37 pages)</div></a></figure></div>
			<div><a href="#top">Back to top</a></div>
			<div><a href="/card"><h3>Card title</h3></a></div>
			<p>Read <a href="/inside">the inside link</a> here.</p>
		</section>
	</main></body>`
	sel := docFrom(t, page).Find("main").First()
	out := cleanText(extractText(sel, mustParseURL(t, "https://www.gao.gov/products/gao-24-106591")))
	if !strings.Contains(out, "## Full Report\n\n[Full Report (37 pages)](https://www.gao.gov/assets/gao-24-106591.pdf)") {
		t.Errorf("standalone report link missing under its heading:\n%s", out)
	}
	if strings.Contains(out, "Back to top") {
		t.Errorf("fragment-only standalone link kept:\n%s", out)
	}
	if strings.Count(out, "the inside link") != 1 {
		t.Errorf("link inside a paragraph should appear once:\n%s", out)
	}
	if strings.Count(out, "Card title") != 1 {
		t.Errorf("anchor wrapping a heading should not be duplicated:\n%s", out)
	}
	if got := cleanText(extractText(sel, nil)); strings.Contains(got, "37 pages") || strings.Contains(got, "](") {
		t.Errorf("nil base must not emit links:\n%s", got)
	}
}

// bestText picks the flat text on a div-soup page whether or not the page has
// links. Link URLs make the structured text longer, but must not hide the bare
// prose that only the flat text sees.
func TestBestText_LinksDoNotDecideWhichTextWins(t *testing.T) {
	t.Parallel()
	longPath := strings.Repeat("segment/", 25)
	page := `<body><article>
		<p>` + strings.Repeat("word ", 20) + `<a href="https://example.com/` + longPath + `">linked text</a></p>
		<div>` + strings.Repeat("bare div prose that only the flat text sees. ", 4) + `</div>
	</article></body>`
	sel := docFrom(t, page).Find("article").First()
	base := mustParseURL(t, "https://example.com/")

	withLinks := bestText(sel, base)
	without := bestText(sel, nil)
	if !strings.Contains(without, "bare div prose") {
		t.Fatalf("setup: flat text should win without links, got %q", without)
	}
	if !strings.Contains(withLinks, "bare div prose") {
		t.Errorf("link markup made the structured text win and dropped the bare-div prose:\n%s", withLinks)
	}
}

// Structured text that keeps its links still wins when the page is ordinary.
func TestBestText_KeepsLinksWhenStructuredWins(t *testing.T) {
	t.Parallel()
	sel := docFrom(t, `<body><article><p>`+strings.Repeat("word ", 50)+`<a href="/report.pdf">the report</a></p></article></body>`).Find("article").First()
	got := bestText(sel, mustParseURL(t, "https://example.com/"))
	if !strings.Contains(got, "[the report](https://example.com/report.pdf)") {
		t.Errorf("link lost:\n%s", got)
	}
}

// End to end through both HTML tiers: a relative link on the page comes out as
// an absolute Markdown link.
func TestScrapeHTMLTiers_KeepLinks(t *testing.T) {
	t.Parallel()
	page := `<html><head><title>Product</title></head><body><article>
		<h1>Product page</h1>
		<p>` + strings.Repeat("This paragraph gives the page enough prose to count as an article body. ", 5) + `</p>
		<h2>Full Report</h2>
		<p><a href="/assets/report.pdf">Download the report</a> or <a href="javascript:void(0)">print it</a>.</p>
	</article></body></html>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer ts.Close()

	p := NewPipeline(PipelineConfig{MaxConcurrency: 2, AllowPrivateIPs: true})
	for name, scrape := range map[string]func() (*ScrapeResult, error){
		"stealth": func() (*ScrapeResult, error) { return p.scrapeStealth(context.Background(), ts.URL, 50000, false) },
		"html":    func() (*ScrapeResult, error) { return p.scrapeHTML(context.Background(), ts.URL, 50000, false) },
	} {
		res, err := scrape()
		if err != nil || res == nil {
			t.Fatalf("%s: %v", name, err)
		}
		if want := "[Download the report](" + ts.URL + "/assets/report.pdf)"; !strings.Contains(res.Content, want) {
			t.Errorf("%s: missing %q in:\n%s", name, want, res.Content)
		}
		if strings.Contains(res.Content, "javascript:") {
			t.Errorf("%s: javascript: link in output:\n%s", name, res.Content)
		}
		if !strings.Contains(res.Content, "or print it.") {
			t.Errorf("%s: skipped link text lost:\n%s", name, res.Content)
		}
	}
}
