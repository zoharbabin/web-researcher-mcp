package scraper

import (
	"compress/flate"
	"compress/gzip"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

func (p *Pipeline) scrapeStealth(ctx context.Context, url string, maxLength int) (*ScrapeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	client := newStealthClient(p.config.AllowPrivateIPs)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	applyBrowserHeaders(req)

	resp, err := client.Do(req)
	if err != nil {
		return nil, networkError(url, "stealth", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, classifyHTTPStatus(resp.StatusCode, url, "stealth")
	}

	reader, err := decompressBody(resp)
	if err != nil {
		return nil, err
	}
	if closer, ok := reader.(io.Closer); ok && closer != resp.Body {
		defer closer.Close()
	}

	// Read up to 1MB of raw HTML to ensure we reach the article content,
	// regardless of the desired output maxLength.
	const maxHTMLRead = 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(reader, maxHTMLRead))
	if err != nil {
		return nil, err
	}

	// Re-route to the document parser when the Content-Type header or %PDF
	// magic bytes reveal this is a PDF (#206). isDocumentURL only checks the
	// URL path suffix, so PDFs served at HTML-path URLs (e.g. PLoS printable
	// views, journal download links) slip through to this tier where binary
	// bytes fed into goquery's HTML parser produce empty or garbled output.
	if isPDFContentType(resp.Header.Get("Content-Type")) || looksLikePDF(body) {
		return p.scrapeBodyAsPDF(url, body, maxLength)
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}

	// Capture structured data (#46) BEFORE extractArticleContent strips <script>
	// (JSON-LD lives in a stripped <script>). The stealth tier wins for most
	// real pages, so structuredData is populated here too, not only the HTML tier.
	sd := extractStructuredData(doc)

	content := extractArticleContent(doc)
	if len(content) < 100 {
		return nil, nil
	}

	title := doc.Find("title").First().Text()
	title = strings.TrimSpace(title)

	truncated := false
	if len(content) > maxLength {
		content = content[:maxLength]
		truncated = true
	}

	res := &ScrapeResult{
		URL:         url,
		Content:     content,
		ContentType: "html",
		Title:       title,
		Truncated:   truncated,
	}
	if !sd.IsEmpty() {
		res.StructuredData = sd
	}
	return res, nil
}

func newStealthClient(allowPrivateIPs bool) *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: false,
		},
		ForceAttemptHTTP2: true,
	}

	if !allowPrivateIPs {
		transport.DialContext = newSSRFSafeDialer().DialContext
	}

	return &http.Client{
		Transport: transport,
		Timeout:   20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return http.ErrUseLastResponse
			}
			applyBrowserHeaders(req)
			return nil
		},
	}
}

func applyBrowserHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	req.Header.Set("Sec-Ch-Ua", `"Chromium";v="131", "Not_A Brand";v="24"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"macOS"`)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Cache-Control", "max-age=0")
}

func extractArticleContent(doc *goquery.Document) string {
	// Remove noise
	doc.Find("script, style, nav, footer, header, aside, .sidebar, .menu, .ad, .advertisement, .cookie-banner, .popup").Remove()

	// Try structured article selectors first
	selectors := []string{
		"article",
		"[role='main']",
		"main",
		".post-content",
		".article-content",
		".entry-content",
		"#content",
		".content",
	}

	for _, sel := range selectors {
		el := doc.Find(sel).First()
		if el.Length() > 0 {
			if text := bestText(el); len(text) > 200 {
				return text
			}
		}
	}

	// Fall back to body with the same structured-vs-flat reconciliation.
	return bestText(doc.Find("body"))
}

// bestText renders a selection's content, preferring extractText (which emits
// GFM tables #48, headings, and lists) but falling back to flat .Text() when the
// flat rendering captures materially more — i.e. the prose lives in bare
// div/span/section containers that extractText does not walk. extractText adds
// markup characters, so for ordinary p/list/table content it is at least as long
// as flat and is kept; flat only wins on genuine div-soup pages, restoring the
// stealth tier's pre-#48 completeness (this tier wins for most pages, so this
// prevents silent body-text loss).
func bestText(sel *goquery.Selection) string {
	structured := cleanText(extractText(sel))
	if flat := cleanText(sel.Text()); len(flat) > len(structured)*3/2 {
		return flat
	}
	return structured
}

func newSSRFSafeDialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
}

func decompressBody(resp *http.Response) (io.Reader, error) {
	switch strings.ToLower(resp.Header.Get("Content-Encoding")) {
	case "gzip":
		return gzip.NewReader(resp.Body)
	case "deflate":
		return flate.NewReader(resp.Body), nil
	default:
		return resp.Body, nil
	}
}
