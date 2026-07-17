package scraper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
)

// Structured-data extraction caps (#46). structuredData is UNTRUSTED external
// data that content.Process never sanitizes/truncates, so extractStructuredData
// self-bounds before returning to protect the response budget (mirroring the
// p.config.MaxHTMLBytes input cap). Worst-case added response size is
// deterministically <= maxStructuredDataBytes (~64KB), a small fraction of the
// 50KB default content budget and bounded regardless of the HTML input size.
// Hitting a cap stops collection silently — partial enrichment is fine, never an error.
const (
	maxJSONLDBlocks        = 16        // keep at most 16 valid ld+json blocks (real pages carry 2-4)
	maxJSONLDBlockBytes    = 32 * 1024 // skip any single ld+json block larger than 32KB before parsing
	maxStructuredDataBytes = 64 * 1024 // running total across all stored jsonLd + og + citation bytes
	maxMetaProps           = 64        // cap entry count per meta map (og/article and citation independently)
	maxMetaValueBytes      = 2 * 1024  // truncate any single meta content value beyond 2KB
)

// maxIframeCandidates bounds how many cross-origin <iframe src> URLs are kept
// per page (issue #399) — real wrapper pages (HuggingFace Spaces, CodeSandbox,
// Stripe Checkout) carry 1-2, so this is generous headroom, not a real limit.
const maxIframeCandidates = 2

func (p *Pipeline) scrapeHTML(ctx context.Context, url string, maxLength int) (*ScrapeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; web-researcher-mcp/1.0)")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, networkError(url, "html", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, classifyHTTPStatus(resp.StatusCode, url, "html")
	}

	reader, err := decompressBody(resp)
	if err != nil {
		return nil, err
	}
	if closer, ok := reader.(io.Closer); ok && closer != resp.Body {
		defer closer.Close()
	}

	body, err := io.ReadAll(io.LimitReader(reader, int64(p.config.MaxHTMLBytes)))
	if err != nil {
		return nil, err
	}

	// Same PDF detection as the stealth tier — HTML tier is the third pass,
	// still before the browser; same re-route logic applies (#206).
	if isPDFContentType(resp.Header.Get("Content-Type")) || looksLikePDF(body) {
		return p.scrapeBodyAsPDF(url, body, maxLength)
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// MUST precede the script Remove() below (#46): JSON-LD lives in <script
	// type="application/ld+json">, which the Remove() call strips. Capture first.
	sd := extractStructuredData(doc)
	// MUST precede the iframe Remove() below (#399): a cross-origin <iframe src>
	// carrying the real content (e.g. a HuggingFace Space wrapper) is stripped
	// by that same call. Capture first.
	iframes := extractIframeCandidates(doc, url)

	doc.Find("script, style, nav, footer, aside, header, noscript, iframe, form").Remove()
	doc.Find("[role='navigation'], [role='banner'], [role='complementary']").Remove()
	doc.Find(".ad, .ads, .advertisement, .sidebar, .nav, .footer, .header, .menu").Remove()

	meta := extractHTMLMetadata(doc)
	content := extractMainContent(doc)

	content = cleanText(content)
	truncated := false
	if len(content) > maxLength {
		content = truncateBytes(content, maxLength)
		truncated = true
	}

	res := &ScrapeResult{
		URL:         url,
		Content:     content,
		ContentType: "html",
		Title:       meta.title,
		Author:      meta.author,
		SiteName:    meta.siteName,
		PublishDate: meta.publishDate,
		Truncated:   truncated,
		// Surface the decompressed HTML size so the pipeline can detect a
		// JS-rendered SPA shell (large HTML, little extracted text) and keep
		// escalating to the browser tier (see looksLikePartialShell).
		rawHTMLBytes:     len(body),
		iframeCandidates: iframes,
	}
	// Attach structured data only when something was captured, so ordinary pages
	// leave the pointer nil (the clean "absent" signal).
	if !sd.IsEmpty() {
		res.StructuredData = sd
	}
	// Extract forum signals (Reddit upvotes, comments, etc.) from JSON-LD
	if res.StructuredData != nil && len(res.StructuredData.JSONLD) > 0 {
		res.ForumSignals = extractForumSignals(url, res.StructuredData.JSONLD)
	}
	return res, nil
}

type htmlMetadata struct {
	title       string
	author      string
	siteName    string
	publishDate string
}

func extractHTMLMetadata(doc *goquery.Document) htmlMetadata {
	title := doc.Find("title").First().Text()
	if ogTitle, exists := doc.Find(`meta[property="og:title"]`).Attr("content"); exists {
		title = ogTitle
	}

	var author, siteName, publishDate string
	if a, exists := doc.Find(`meta[name="author"]`).Attr("content"); exists {
		author = a
	}
	if s, exists := doc.Find(`meta[property="og:site_name"]`).Attr("content"); exists {
		siteName = s
	}
	if d, exists := doc.Find(`meta[property="article:published_time"]`).Attr("content"); exists {
		publishDate = d
	} else if d, exists := doc.Find(`meta[name="date"]`).Attr("content"); exists {
		publishDate = d
	}

	return htmlMetadata{
		title:       strings.TrimSpace(title),
		author:      author,
		siteName:    siteName,
		publishDate: publishDate,
	}
}

func extractMainContent(doc *goquery.Document) string {
	selectors := []string{"article", "main", "[role='main']", ".content", ".post-content", "#content", "body"}
	for _, sel := range selectors {
		node := doc.Find(sel).First()
		if node.Length() > 0 {
			content := extractText(node)
			if len(content) > 100 {
				return content
			}
		}
	}
	return extractText(doc.Find("body"))
}

func extractText(sel *goquery.Selection) string {
	var parts []string
	// "table" is walked here and "td, th" are deliberately NOT — table cells are
	// emitted ONLY through their owning <table> (as a GFM pipe table), which
	// eliminates the old bug where a data table became disconnected cell
	// fragments (#48).
	sel.Find("p, h1, h2, h3, h4, h5, h6, li, blockquote, pre, table, figcaption, dt, dd").Each(func(_ int, s *goquery.Selection) {
		tag := goquery.NodeName(s)
		if tag == "table" {
			// Only the OUTERMOST table emits: a table nested inside another is
			// skipped here (its text surfaces via the outer table's fallback),
			// so nested tables never double-emit.
			if s.ParentsFiltered("table").Length() > 0 {
				return
			}
			if md, ok := extractTable(s); ok {
				parts = append(parts, md)
			} else if t := strings.TrimSpace(s.Text()); t != "" {
				// Layout/malformed table: degrade to plain text (today's behavior).
				parts = append(parts, t)
			}
			return
		}
		// A block element living inside a <table> cell is already rendered as
		// part of that table's GFM grid, so skip it here to avoid emitting its
		// text a second time as a standalone paragraph/list item.
		if s.ParentsFiltered("table").Length() > 0 {
			return
		}
		text := strings.TrimSpace(s.Text())
		if text != "" {
			switch {
			case strings.HasPrefix(tag, "h") && len(tag) == 2 && tag[1] >= '1' && tag[1] <= '6':
				level := int(tag[1] - '0')
				parts = append(parts, "\n"+strings.Repeat("#", level)+" "+text+"\n")
			case tag == "li":
				parts = append(parts, "- "+text)
			case tag == "blockquote":
				parts = append(parts, "> "+text)
			case tag == "pre":
				parts = append(parts, "```\n"+text+"\n```")
			case tag == "dt":
				parts = append(parts, text+":")
			case tag == "dd", tag == "figcaption":
				parts = append(parts, text)
			default:
				parts = append(parts, text)
			}
		}
	})
	return strings.Join(parts, "\n\n")
}

// extractTable renders an HTML <table> as a GitHub-flavored markdown pipe table
// (#48). It returns (markdown, true) for a data table, or ("", false) for a
// layout/empty/nested table so the caller can fall back to plain text. Pure and
// reentrant; never panics (goquery yields empty selections, not nil derefs).
func extractTable(table *goquery.Selection) (string, bool) {
	// Nested/layout table: a table containing a descendant <table> is rejected
	// so it degrades to plain text rather than producing a garbled grid.
	if table.Find("table").Length() > 0 {
		return "", false
	}

	var grid [][]string
	firstRowAllTh := false
	table.Find("tr").Each(func(ri int, tr *goquery.Selection) {
		var cells []string
		allTh := true
		// Children().Filter (not Find) so we never descend into nested structures.
		tr.Children().Filter("td, th").Each(func(_ int, cell *goquery.Selection) {
			cells = append(cells, escapeCell(strings.TrimSpace(cell.Text())))
			if goquery.NodeName(cell) != "th" {
				allTh = false
			}
		})
		if len(cells) == 0 {
			return // skip a row that produced no cells
		}
		if len(grid) == 0 {
			firstRowAllTh = allTh
		}
		grid = append(grid, cells)
	})

	if len(grid) == 0 {
		return "", false
	}

	maxCols := 0
	allEmpty := true
	for _, row := range grid {
		if len(row) > maxCols {
			maxCols = len(row)
		}
		for _, c := range row {
			if c != "" {
				allEmpty = false
			}
		}
	}
	if maxCols <= 1 || allEmpty {
		return "", false // single-column or content-free => treat as layout
	}

	// Header detection: an all-<th> first row, or a <thead>, is the header;
	// otherwise synthesize "Column N" headers and treat every row as data.
	var header []string
	var bodyRows [][]string
	switch {
	case firstRowAllTh:
		header, bodyRows = grid[0], grid[1:]
	case table.Find("thead th, thead td").Length() > 0:
		header, bodyRows = grid[0], grid[1:]
	default:
		header = make([]string, maxCols)
		for i := range header {
			header[i] = fmt.Sprintf("Column %d", i+1)
		}
		bodyRows = grid
	}

	norm := func(row []string) []string {
		out := make([]string, maxCols)
		for i := 0; i < maxCols; i++ {
			if i < len(row) {
				out[i] = row[i]
			}
		}
		return out
	}

	sep := make([]string, maxCols)
	for i := range sep {
		sep[i] = "---"
	}

	var lines []string
	lines = append(lines, "| "+strings.Join(norm(header), " | ")+" |")
	lines = append(lines, "| "+strings.Join(sep, " | ")+" |")
	for _, row := range bodyRows {
		lines = append(lines, "| "+strings.Join(norm(row), " | ")+" |")
	}
	return strings.Join(lines, "\n"), true
}

// escapeCell makes a table cell safe for a single GFM table row: it flattens all
// internal whitespace to single spaces (so a multi-line cell stays on one row)
// and escapes backslashes (first) then pipes (so a literal | never breaks the
// column layout).
func escapeCell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "|", `\|`)
	return s
}

// extractStructuredData lifts JSON-LD, Open Graph/article, and Highwire
// citation_* metadata from the parsed document (#46). It MUST be called BEFORE
// the <script> Remove() in scrapeHTML (JSON-LD lives in a stripped <script>).
// Pure, reentrant, never panics. Self-bounds to the size caps above so the
// response budget is protected (content.Process never sees this data). Always
// returns a non-nil pointer; callers use IsEmpty() to decide whether to surface it.
func extractStructuredData(doc *goquery.Document) *StructuredData {
	sd := &StructuredData{}
	total := 0

	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, s *goquery.Selection) {
		if len(sd.JSONLD) >= maxJSONLDBlocks {
			return
		}
		raw := strings.TrimSpace(s.Text())
		if raw == "" || len(raw) > maxJSONLDBlockBytes {
			return
		}
		if total+len(raw) > maxStructuredDataBytes {
			return
		}
		if !json.Valid([]byte(raw)) {
			return // skip invalid JSON-LD without failing the scrape
		}
		sd.JSONLD = append(sd.JSONLD, json.RawMessage(raw))
		total += len(raw)
	})

	doc.Find("meta[property]").Each(func(_ int, s *goquery.Selection) {
		prop, _ := s.Attr("property")
		if !strings.HasPrefix(prop, "og:") && !strings.HasPrefix(prop, "article:") {
			return
		}
		if len(sd.OpenGraph) >= maxMetaProps {
			return
		}
		v := truncateBytes(strings.TrimSpace(mustAttr(s, "content")), maxMetaValueBytes)
		if v == "" || total+len(v) > maxStructuredDataBytes {
			return
		}
		if sd.OpenGraph == nil {
			sd.OpenGraph = map[string]string{}
		}
		sd.OpenGraph[prop] = v
		total += len(v)
	})

	doc.Find("meta[name]").Each(func(_ int, s *goquery.Selection) {
		name, _ := s.Attr("name")
		if !strings.HasPrefix(name, "citation_") {
			return
		}
		if len(sd.Citation) >= maxMetaProps {
			return
		}
		v := truncateBytes(strings.TrimSpace(mustAttr(s, "content")), maxMetaValueBytes)
		if v == "" || total+len(v) > maxStructuredDataBytes {
			return
		}
		if sd.Citation == nil {
			sd.Citation = map[string]string{}
		}
		sd.Citation[name] = v
		total += len(v)
	})

	return sd
}

// extractIframeCandidates lifts up to maxIframeCandidates absolute http(s)
// URLs from <iframe src="..."> in the parsed document (#399). MUST be called
// BEFORE the iframe-stripping Remove() in scrapeHTML/extractArticleContent —
// same precedent as extractStructuredData for JSON-LD. Relative src values
// are resolved against baseURL. data:/about:/javascript:/blob:/mailto:/empty
// are dropped. Pure, reentrant, never panics. Returns nil when nothing usable.
func extractIframeCandidates(doc *goquery.Document, baseURL string) []string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}

	var candidates []string
	seen := map[string]bool{}
	doc.Find("iframe[src]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if len(candidates) >= maxIframeCandidates {
			return false
		}
		src := strings.TrimSpace(mustAttr(s, "src"))
		if src == "" {
			return true
		}
		ref, err := url.Parse(src)
		if err != nil {
			return true
		}
		resolved := base.ResolveReference(ref)
		scheme := strings.ToLower(resolved.Scheme)
		if scheme != "http" && scheme != "https" {
			return true
		}
		if resolved.Hostname() == "" {
			return true
		}
		abs := resolved.String()
		if seen[abs] {
			return true
		}
		seen[abs] = true
		candidates = append(candidates, abs)
		return true
	})
	return candidates
}

// mustAttr returns the attribute value or "" when absent (goquery returns
// ("", false) on a missing attr; this keeps call sites terse).
func mustAttr(s *goquery.Selection, attr string) string {
	v, _ := s.Attr(attr)
	return v
}

// truncateBytes caps a string to at most n bytes, trimming back to a UTF-8 rune
// boundary so a multibyte character is never split (which would yield invalid
// UTF-8 in the JSON output).
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func cleanText(s string) string {
	lines := strings.Split(s, "\n")
	var cleaned []string
	prevBlank := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if !prevBlank {
				cleaned = append(cleaned, "")
			}
			prevBlank = true
		} else {
			cleaned = append(cleaned, trimmed)
			prevBlank = false
		}
	}
	for len(cleaned) > 0 && cleaned[0] == "" {
		cleaned = cleaned[1:]
	}
	for len(cleaned) > 0 && cleaned[len(cleaned)-1] == "" {
		cleaned = cleaned[:len(cleaned)-1]
	}
	return strings.Join(cleaned, "\n")
}

// extractForumSignals parses JSON-LD blocks from a Reddit page and returns
// ForumSignals. Returns nil for non-Reddit URLs or when no forum schema found.
func extractForumSignals(rawURL string, jsonldBlocks []json.RawMessage) *ForumSignals {
	if !strings.Contains(rawURL, "reddit.com") {
		return nil
	}

	for _, raw := range jsonldBlocks {
		var block map[string]interface{}
		if err := json.Unmarshal(raw, &block); err != nil {
			continue
		}

		t, ok := block["@type"].(string)
		if !ok || t != "DiscussionForumPosting" {
			continue
		}

		sig := &ForumSignals{Platform: "reddit", Upvotes: -1, Comments: -1}

		// Extract upvotes from upvoteCount field
		if upvotes, ok := block["upvoteCount"]; ok {
			if flt, ok := upvotes.(float64); ok {
				sig.Upvotes = int(flt)
			}
		}

		// Extract comments and upvotes from interactionStatistic array
		if stats, ok := block["interactionStatistic"].([]interface{}); ok {
			for _, stat := range stats {
				statMap, ok := stat.(map[string]interface{})
				if !ok {
					continue
				}
				itype, ok := statMap["interactionType"].(string)
				if !ok {
					continue
				}
				count, ok := statMap["userInteractionCount"].(float64)
				if !ok {
					continue
				}
				if strings.Contains(itype, "VoteAction") && sig.Upvotes == -1 {
					sig.Upvotes = int(count)
				} else if strings.Contains(itype, "CommentAction") {
					sig.Comments = int(count)
				}
			}
		}

		// Extract datePublished
		if published, ok := block["datePublished"].(string); ok {
			sig.DatePublished = published
		}

		// Extract author name
		if author, ok := block["author"].(map[string]interface{}); ok {
			if name, ok := author["name"].(string); ok {
				sig.AuthorName = name
			}
		}

		// Set credibility note for low engagement
		if sig.Upvotes >= 0 && sig.Upvotes < 20 {
			sig.CredibilityNote = "Low engagement: this post has fewer than 20 upvotes. Community validation is minimal."
		}

		return sig
	}

	return nil
}
