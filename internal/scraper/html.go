package scraper

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// maxHTMLRead bounds the decompressed body fed into goquery's in-memory DOM in
// the tier-3 HTML scraper, mirroring the caps the other tiers already enforce
// (stealth.go 1MB, patents.go 3MB). Without it a large or decompression-bomb
// page could exhaust memory (OWASP Agentic ASI06 resource exhaustion). 3MB is
// generous for an article — this tier is only reached when markdown/stealth
// yield too little, and content is truncated to maxLength afterward anyway.
const maxHTMLRead = 3 * 1024 * 1024

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

	body, err := io.ReadAll(io.LimitReader(reader, maxHTMLRead))
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	doc.Find("script, style, nav, footer, aside, header, noscript, iframe, form").Remove()
	doc.Find("[role='navigation'], [role='banner'], [role='complementary']").Remove()
	doc.Find(".ad, .ads, .advertisement, .sidebar, .nav, .footer, .header, .menu").Remove()

	meta := extractHTMLMetadata(doc)
	content := extractMainContent(doc)

	content = cleanText(content)
	truncated := false
	if len(content) > maxLength {
		content = content[:maxLength]
		truncated = true
	}

	return &ScrapeResult{
		URL:         url,
		Content:     content,
		ContentType: "html",
		Title:       meta.title,
		Author:      meta.author,
		SiteName:    meta.siteName,
		PublishDate: meta.publishDate,
		Truncated:   truncated,
	}, nil
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
	sel.Find("p, h1, h2, h3, h4, h5, h6, li, blockquote, pre, td, th").Each(func(_ int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if text != "" {
			tag := goquery.NodeName(s)
			switch {
			case strings.HasPrefix(tag, "h"):
				parts = append(parts, "\n## "+text+"\n")
			case tag == "li":
				parts = append(parts, "- "+text)
			case tag == "blockquote":
				parts = append(parts, "> "+text)
			case tag == "pre":
				parts = append(parts, "```\n"+text+"\n```")
			default:
				parts = append(parts, text)
			}
		}
	})
	return strings.Join(parts, "\n\n")
}

func cleanText(s string) string {
	lines := strings.Split(s, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}

	result := strings.Join(cleaned, "\n")

	// Collapse multiple newlines
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}

	return strings.TrimSpace(result)
}
