package scraper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

// redditPostRe parses subreddit and post ID from any reddit.com post URL.
var redditPostRe = regexp.MustCompile(`/r/([^/]+)/comments/([A-Za-z0-9]+)`)

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

func (p *Pipeline) scrapeHTML(ctx context.Context, url string, maxLength int, raw bool) (*ScrapeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; web-researcher-mcp/1.0)")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	client, err := clientWithCookies(ctx, p.client)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
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
	ct := resp.Header.Get("Content-Type")
	if isPDFContentType(ct) || looksLikePDF(body) {
		return p.scrapeBodyAsPDF(url, body, maxLength, raw, ct)
	}

	return p.parseHTMLBody(ctx, url, body, ct, maxLength)
}

// parseHTMLBody parses already-fetched bytes as HTML, extracting the article
// content, metadata, and structured data. Factored out of scrapeHTML (mirroring
// scrapeBodyAsPDF's existing split for the same reason, #206) so scrapeDocument
// can reuse it: a document-typed URL (.pdf/.docx/.pptx) can redirect through a
// session/access-gate onto an HTML landing page instead of the claimed document
// bytes (#631 — nature.com's open-access "download PDF" link 303s through an
// auth handshake onto the full HTML article), and that HTML deserves real
// extraction rather than a doomed document parse.
func (p *Pipeline) parseHTMLBody(ctx context.Context, url string, body []byte, ct string, maxLength int) (*ScrapeResult, error) {
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
	// MUST precede the Remove() calls below: JSON-LD, <header> and byline nodes
	// are stripped by them.
	meta := extractHTMLMetadata(doc)

	doc.Find("script, style, nav, footer, aside, header, noscript, iframe, form").Remove()
	doc.Find("[role='navigation'], [role='banner'], [role='complementary']").Remove()
	doc.Find(".ad, .ads, .advertisement, .sidebar, .nav, .footer, .header, .menu").Remove()

	content := extractMainContent(doc, documentBase(doc, url))

	content = cleanText(content)
	truncated := false
	if len(content) > maxLength {
		content = truncateBytes(content, maxLength)
		truncated = true
	}

	// Raw mode's returned body must respect the caller's maxLength the same
	// way Content does — body itself is only bounded by MaxHTMLBytes, which is
	// deliberately larger so extraction reaches the real article text.
	// The raw cut sets rawTruncated only: the flag for full mode describes the
	// returned text, not these bytes (#823).
	rawBody := string(body)
	rawTruncated := false
	if len(rawBody) > maxLength {
		rawBody = truncateBytes(rawBody, maxLength)
		rawTruncated = true
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
		// The undecoded HTML body this tier already fetched, before
		// extractMainContent stripped it down to Content — raw mode returns
		// this instead of Content once tieredFallback picks a winner.
		rawBody:        rawBody,
		rawTruncated:   rawTruncated,
		rawContentType: ct,
	}
	// Attach structured data only when something was captured, so ordinary pages
	// leave the pointer nil (the clean "absent" signal).
	if !sd.IsEmpty() {
		res.StructuredData = sd
	}
	// Extract forum signals (Reddit upvotes, comments, etc.) from JSON-LD
	if res.StructuredData != nil && len(res.StructuredData.JSONLD) > 0 {
		res.ForumSignals = extractForumSignals(ctx, url, res.StructuredData.JSONLD, p.client, p.config.ShredditBase)
	}
	return res, nil
}

type htmlMetadata struct {
	title       string
	author      string
	siteName    string
	publishDate string
}

// extractHTMLMetadata is the one metadata extractor shared by every HTML tier
// (html, stealth). It MUST run before anything removes elements: the JSON-LD
// <script>, <header> and byline nodes it reads are all stripped later.
//
// For author and publish date the first source that yields a value wins, in the
// order listed on dateFromDoc and authorFromDoc. Publish dates are normalized to
// ISO YYYY-MM-DD (or YYYY / YYYY-MM when the page gives no more). A value that
// cannot be parsed is skipped, never guessed.
func extractHTMLMetadata(doc *goquery.Document) htmlMetadata {
	title := doc.Find("title").First().Text()
	if ogTitle, exists := doc.Find(`meta[property="og:title"]`).Attr("content"); exists {
		title = ogTitle
	}

	var siteName string
	if s, exists := doc.Find(`meta[property="og:site_name"]`).Attr("content"); exists {
		siteName = s
	}

	metas := collectMetaTags(doc)
	jsonld := parseJSONLDObjects(doc)

	return htmlMetadata{
		title:       strings.TrimSpace(title),
		author:      authorFromDoc(doc, metas, jsonld),
		siteName:    siteName,
		publishDate: dateFromDoc(doc, metas, jsonld),
	}
}

// collectMetaTags maps each lower-cased meta name/property to its first
// non-empty content value.
func collectMetaTags(doc *goquery.Document) map[string]string {
	metas := map[string]string{}
	doc.Find("meta").Each(func(_ int, s *goquery.Selection) {
		content := strings.TrimSpace(mustAttr(s, "content"))
		if content == "" {
			return
		}
		for _, attr := range []string{"name", "property"} {
			if k := strings.ToLower(strings.TrimSpace(mustAttr(s, attr))); k != "" {
				if _, seen := metas[k]; !seen {
					metas[k] = content
				}
			}
		}
	})
	return metas
}

// parseJSONLDObjects returns every JSON-LD object in the page, flattening
// top-level arrays and @graph lists. Invalid blocks are skipped.
func parseJSONLDObjects(doc *goquery.Document) []map[string]any {
	var out []map[string]any
	var walk func(v any, depth int)
	walk = func(v any, depth int) {
		if depth > 3 {
			return
		}
		switch t := v.(type) {
		case []any:
			for _, e := range t {
				walk(e, depth+1)
			}
		case map[string]any:
			out = append(out, t)
			if g, ok := t["@graph"]; ok {
				walk(g, depth+1)
			}
		}
	}
	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, s *goquery.Selection) {
		raw := strings.TrimSpace(s.Text())
		if raw == "" || len(raw) > maxJSONLDBlockBytes {
			return
		}
		var v any
		if json.Unmarshal([]byte(raw), &v) != nil {
			return
		}
		walk(v, 0)
	})
	return out
}

// dateFromDoc returns the publish date, first match wins: article:published_time,
// og:article:published_time, meta name=date, DC.date, dcterms.created,
// citation_publication_date, JSON-LD datePublished, <time datetime> inside
// article or header, [itemprop=datePublished], then the byline pattern.
func dateFromDoc(doc *goquery.Document, metas map[string]string, jsonld []map[string]any) string {
	for _, k := range []string{
		"article:published_time", "og:article:published_time", "date",
		"dc.date", "dcterms.created", "citation_publication_date",
	} {
		if d := normalizeDate(metas[k]); d != "" {
			return d
		}
	}
	for _, obj := range jsonld {
		if v, ok := obj["datePublished"].(string); ok {
			if d := normalizeDate(v); d != "" {
				return d
			}
		}
	}
	var found string
	doc.Find("article time[datetime], header time[datetime]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		found = normalizeDate(mustAttr(s, "datetime"))
		return found == ""
	})
	if found != "" {
		return found
	}
	doc.Find(`[itemprop="datePublished"]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		for _, v := range []string{mustAttr(s, "datetime"), mustAttr(s, "content"), s.Text()} {
			if found = normalizeDate(v); found != "" {
				break
			}
		}
		return found == ""
	})
	if found != "" {
		return found
	}
	return dateFromByline(doc)
}

// maxBylineTextBytes bounds the byline text scanned for a date, so a byline
// container wrapping the whole page cannot match a date from the body.
const maxBylineTextBytes = 400

var (
	// bylineOnDateRe matches "on 14 Sep, 2026" (day, abbreviated or full month).
	bylineOnDateRe = regexp.MustCompile(`\bon (\d{1,2} [A-Z][a-z]{2,8},? \d{4})\b`)
	// bylineMonthDateRe matches "September 14, 2026".
	bylineMonthDateRe = regexp.MustCompile(`\b([A-Z][a-z]{2,8} \d{1,2}, \d{4})\b`)
)

// dateFromByline reads a visible byline such as "Alicia Malone on 14 Sep, 2026".
// It only looks inside a byline container (a[rel=author], .author-link,
// [class*=byline]) and its parent. A date that does not parse yields no date.
func dateFromByline(doc *goquery.Document) string {
	var found string
	doc.Find(`a[rel="author"], .author-link, [class*="byline"]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		for _, text := range []string{s.Text(), s.Parent().Text()} {
			text = truncateBytes(collapseSpace(text), maxBylineTextBytes)
			for _, re := range []*regexp.Regexp{bylineOnDateRe, bylineMonthDateRe} {
				if m := re.FindStringSubmatch(text); m != nil {
					if found = normalizeDate(m[1]); found != "" {
						return false
					}
				}
			}
		}
		return true
	})
	return found
}

// authorFromDoc returns the author, first match wins: meta name=author,
// article:author, JSON-LD author name, [itemprop=author], a[rel=author] text.
func authorFromDoc(doc *goquery.Document, metas map[string]string, jsonld []map[string]any) string {
	for _, k := range []string{"author", "article:author"} {
		if a := cleanAuthor(metas[k]); a != "" {
			return a
		}
	}
	for _, obj := range jsonld {
		if a := cleanAuthor(jsonLDAuthorNames(obj["author"])); a != "" {
			return a
		}
	}
	var found string
	doc.Find(`[itemprop="author"]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		text := s.Find(`[itemprop="name"]`).First().Text()
		if strings.TrimSpace(text) == "" {
			text = mustAttr(s, "content")
		}
		if strings.TrimSpace(text) == "" {
			text = s.Text()
		}
		found = cleanAuthor(text)
		return found == ""
	})
	if found != "" {
		return found
	}
	doc.Find(`a[rel="author"]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		found = cleanAuthor(s.Text())
		return found == ""
	})
	return found
}

// jsonLDAuthorNames reads the name(s) from a JSON-LD author value: a string, an
// object with name, or a list of either. Several names are joined with ", ".
func jsonLDAuthorNames(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		name, _ := t["name"].(string)
		return name
	case []any:
		var names []string
		for _, e := range t {
			if n := cleanAuthor(jsonLDAuthorNames(e)); n != "" {
				names = append(names, n)
			}
		}
		return strings.Join(names, ", ")
	}
	return ""
}

// maxAuthorBytes caps an extracted author so a mis-matched container cannot
// return a paragraph.
const maxAuthorBytes = 200

// cleanAuthor trims and collapses whitespace. A URL (some sites put a profile
// link in article:author) or an over-long value is not a name and yields "".
func cleanAuthor(s string) string {
	s = collapseSpace(s)
	if s == "" || len(s) > maxAuthorBytes {
		return ""
	}
	if l := strings.ToLower(s); strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") {
		return ""
	}
	return s
}

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

var (
	partialDateRe = regexp.MustCompile(`^\d{4}(-\d{2})?$`)
	// dateLayouts are tried in order. The result keeps the date as written on
	// the page (no time zone shift).
	dateLayouts = []string{
		time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05",
		"2006-01-02", "2006/01/02", "2006.01.02",
		time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822,
		"January 2, 2006", "Jan 2, 2006", "2 January 2006", "2 Jan 2006",
		"2 Jan, 2006", "2 January, 2006",
	}
)

// normalizeDate turns a page date into ISO YYYY-MM-DD. A bare year or year and
// month is kept as given (no day is invented). Anything else that does not
// parse returns "".
func normalizeDate(s string) string {
	s = collapseSpace(s)
	if s == "" {
		return ""
	}
	if partialDateRe.MatchString(s) {
		if validPartialDate(s) {
			return s
		}
		return ""
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return ""
}

// validPartialDate reports whether s ("YYYY" or "YYYY-MM") is a real year or
// month. A year below 1000 is treated as a page error, not a date.
func validPartialDate(s string) bool {
	if len(s) == 4 {
		return s >= "1000"
	}
	_, err := time.Parse("2006-01", s)
	return err == nil && s >= "1000"
}

func extractMainContent(doc *goquery.Document, base *url.URL) string {
	selectors := []string{"article", "main", "[role='main']", ".content", ".post-content", "#content", "body"}
	for _, sel := range selectors {
		node := doc.Find(sel).First()
		if node.Length() > 0 {
			content := extractText(node, base)
			if len(content) > 100 {
				return content
			}
		}
	}
	return extractText(doc.Find("body"), base)
}

// textBlockSelector lists the elements extractText renders as blocks.
const textBlockSelector = "p, h1, h2, h3, h4, h5, h6, li, blockquote, pre, table, figcaption, dt, dd"

// extractText renders the block elements under sel as Markdown-ish text. base is
// the page URL that relative links resolve against (#827): in-page links with an
// http(s) target are kept as [text](url). A nil base turns links off, so the
// text is exactly what the elements hold.
func extractText(sel *goquery.Selection, base *url.URL) string {
	var parts []string
	// "table" is walked here and "td, th" are deliberately NOT — table cells are
	// emitted ONLY through their owning <table> (as a GFM pipe table), which
	// eliminates the old bug where a data table became disconnected cell
	// fragments (#48).
	//
	// "a" is walked too, but only for a link that sits outside every other walked
	// element and holds none (a bare <a> in a <div> or <figure>, as report
	// download links often are), which would otherwise be dropped (#827).
	// base == nil turns links off, so then no "a" is visited.
	selector := textBlockSelector
	if base != nil {
		selector += ", a"
	}
	sel.Find(selector).Each(func(_ int, s *goquery.Selection) {
		tag := goquery.NodeName(s)
		if tag == "a" {
			if s.ParentsFiltered(textBlockSelector).Length() > 0 || s.Find(textBlockSelector).Length() > 0 {
				return
			}
			var link strings.Builder
			if writeInlineText(&link, s.Nodes[0], base) && strings.TrimSpace(link.String()) != "" {
				parts = append(parts, strings.TrimSpace(link.String()))
			}
			return
		}
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
		// <pre> is literal text (code): links are not rewritten there.
		var text string
		if tag == "pre" {
			text = strings.TrimSpace(s.Text())
		} else {
			text = strings.TrimSpace(inlineText(s, base))
		}
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

// documentBase is the URL that relative links on the page resolve against: the
// page URL, moved by a <base href> if the page has one. It returns nil when the
// page URL is not an absolute http(s) URL, which turns links off.
func documentBase(doc *goquery.Document, pageURL string) *url.URL {
	base, err := url.Parse(pageURL)
	if err != nil || !isHTTPURL(base) {
		return nil
	}
	if href, ok := doc.Find("base[href]").First().Attr("href"); ok {
		if ref, err := url.Parse(strings.TrimSpace(href)); err == nil {
			if b := base.ResolveReference(ref); isHTTPURL(b) {
				return b
			}
		}
	}
	return base
}

func isHTTPURL(u *url.URL) bool {
	return u != nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}

// linkTextEscaper escapes the characters that would end a Markdown link text.
var linkTextEscaper = strings.NewReplacer(`[`, `\[`, `]`, `\]`)

// linkURLEscaper escapes the characters that would end a Markdown link target.
var linkURLEscaper = strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29", "\n", "%0A", "\t", "%09", "\r", "%0D")

// inlineText returns the text under s with every usable link written as
// [text](absolute-url). It is s.Text() for a page without usable links, byte for
// byte. A link is usable when its href resolves against base to an http(s) URL
// and its text is not empty. Fragment-only, javascript:, mailto:, tel: and other
// schemes are dropped to plain text. A link whose text is the URL is written as
// the URL alone. A nil base turns links off. Link text and URLs are untrusted
// page content, like the rest of the text. Nothing is fetched.
func inlineText(s *goquery.Selection, base *url.URL) string {
	var b strings.Builder
	for _, n := range s.Nodes {
		writeInlineText(&b, n, base)
	}
	return b.String()
}

// writeInlineText writes the text under n to b. It reports whether n itself was
// written as a link.
func writeInlineText(b *strings.Builder, n *html.Node, base *url.URL) bool {
	switch n.Type {
	case html.TextNode:
		b.WriteString(n.Data)
		return false
	case html.ElementNode:
		if n.Data == "a" && base != nil {
			if target, ok := linkTarget(n, base); ok {
				var inner strings.Builder
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					writeInlineText(&inner, c, nil) // no links inside a link
				}
				return writeLink(b, inner.String(), target)
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		writeInlineText(b, c, base)
	}
	return false
}

// linkTarget resolves the href of anchor a against base. ok is false when the
// anchor is not a usable link.
func linkTarget(a *html.Node, base *url.URL) (string, bool) {
	var href string
	for _, attr := range a.Attr {
		if attr.Key == "href" {
			href = strings.TrimSpace(attr.Val)
			break
		}
	}
	if href == "" || strings.HasPrefix(href, "#") {
		return "", false
	}
	ref, err := url.Parse(href)
	if err != nil {
		return "", false
	}
	target := base.ResolveReference(ref)
	if !isHTTPURL(target) {
		return "", false
	}
	return target.String(), true
}

// writeLink writes the link text as [text](target) and reports true. Spaces
// around the text stay outside the brackets, and inner whitespace is collapsed
// so the link stays on one line. With no text it writes raw as is and reports
// false.
func writeLink(b *strings.Builder, raw, target string) bool {
	text := collapseSpace(raw)
	if text == "" {
		b.WriteString(raw)
		return false
	}
	lead, trail := "", ""
	if raw != strings.TrimLeftFunc(raw, unicode.IsSpace) {
		lead = " "
	}
	if raw != strings.TrimRightFunc(raw, unicode.IsSpace) {
		trail = " "
	}
	b.WriteString(lead)
	if text == target || text == strings.TrimSuffix(target, "/") {
		b.WriteString(linkURLEscaper.Replace(target))
	} else {
		b.WriteString("[" + linkTextEscaper.Replace(text) + "](" + linkURLEscaper.Replace(target) + ")")
	}
	b.WriteString(trail)
	return true
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
// When client is non-nil, it also best-effort fetches top comments from
// Reddit's unauthenticated shreddit endpoint; any failure there is swallowed
// and never affects the post-level signals returned.
func extractForumSignals(ctx context.Context, rawURL string, jsonldBlocks []json.RawMessage, client *http.Client, shredditBase string) *ForumSignals {
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

		if client != nil {
			sig.TopComments = fetchShredditComments(ctx, rawURL, shredditBase, client)
		}

		return sig
	}

	return nil
}

const (
	shredditMaxComments  = 5
	shredditMaxBodyBytes = 500
	shredditBodyLimit    = 512 * 1024 // 512 KB cap on shreddit HTML response
)

// fetchShredditComments best-effort fetches the top comments for a Reddit
// post from the unauthenticated shreddit server-rendered HTML endpoint. Any
// failure (invalid URL, timeout, non-200, parse error) returns nil — callers
// must never treat a nil result as an error.
func fetchShredditComments(ctx context.Context, postURL, shredditBase string, client *http.Client) []ForumComment {
	m := redditPostRe.FindStringSubmatch(postURL)
	if len(m) != 3 {
		return nil
	}
	sub, id := m[1], m[2]

	base := shredditBase
	if base == "" {
		base = "https://www.reddit.com"
	}
	fetchURL := fmt.Sprintf("%s/svc/shreddit/comments/r/%s/t3_%s?sort=top", base, sub, id)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", fetchURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; web-researcher-mcp/1.0)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	doc, err := goquery.NewDocumentFromReader(io.LimitReader(resp.Body, shredditBodyLimit))
	if err != nil {
		return nil
	}

	var comments []ForumComment
	doc.Find("shreddit-comment").Each(func(_ int, s *goquery.Selection) {
		body := ""
		if bodyNode := s.Find("[id$='post-rtjson-content']").First(); bodyNode.Length() > 0 {
			body = strings.TrimSpace(bodyNode.Text())
		} else if p := s.Find("p").First(); p.Length() > 0 {
			body = strings.TrimSpace(p.Text())
		}
		if body == "" || body == "[deleted]" || body == "[removed]" {
			return
		}
		body = truncateBytes(cleanText(body), shredditMaxBodyBytes)

		score, _ := strconv.Atoi(mustAttr(s, "score"))

		comments = append(comments, ForumComment{
			Author:    mustAttr(s, "author"),
			Score:     score,
			Body:      body,
			Permalink: mustAttr(s, "permalink"),
			Created:   mustAttr(s, "created"),
		})
	})

	if len(comments) == 0 {
		return nil
	}

	sort.Slice(comments, func(i, j int) bool { return comments[i].Score > comments[j].Score })
	if len(comments) > shredditMaxComments {
		comments = comments[:shredditMaxComments]
	}
	return comments
}
