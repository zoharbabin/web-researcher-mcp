package scraper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/documents"
)

// isPDFContentType reports whether the Content-Type header value indicates PDF.
func isPDFContentType(ct string) bool {
	return strings.Contains(strings.ToLower(ct), "application/pdf")
}

// looksLikePDF reports whether body starts with the %PDF magic bytes, covering
// servers that serve PDFs with an incorrect or absent Content-Type header.
func looksLikePDF(body []byte) bool {
	return len(body) >= 4 && body[0] == '%' && body[1] == 'P' && body[2] == 'D' && body[3] == 'F'
}

// scrapeBodyAsPDF parses already-downloaded bytes as a PDF document (#206), so
// the stealth and HTML tiers can re-route a PDF response without a second
// round-trip when the URL does not end in .pdf but the Content-Type or magic
// bytes reveal it is one.
func (p *Pipeline) scrapeBodyAsPDF(rawURL string, body []byte, maxLength int) (*ScrapeResult, error) {
	text, meta, err := documents.Parse(body, "pdf")
	if err != nil {
		return nil, fmt.Errorf("document parse error: %w", err)
	}
	truncated := false
	if len(text) > maxLength {
		text = truncateBytes(text, maxLength)
		truncated = true
	}
	return &ScrapeResult{
		URL:         rawURL,
		Content:     text,
		ContentType: "pdf",
		Title:       meta.Title,
		Author:      meta.Author,
		Truncated:   truncated,
		Tier:        "document",
	}, nil
}

func (p *Pipeline) scrapeDocument(ctx context.Context, url string, maxLength int) (*ScrapeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "web-researcher-mcp/1.0")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, networkError(url, "document", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, classifyHTTPStatus(resp.StatusCode, url, "document")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(p.config.MaxDocumentBytes)))
	if err != nil {
		return nil, err
	}

	contentType := detectDocType(url, resp.Header.Get("Content-Type"))
	text, meta, err := documents.Parse(body, contentType)
	if err != nil {
		if len(body) >= p.config.MaxDocumentBytes {
			return nil, fmt.Errorf("document parse error (body hit %d-byte read cap — raise MAX_DOCUMENT_BYTES): %w", p.config.MaxDocumentBytes, err)
		}
		return nil, fmt.Errorf("document parse error: %w", err)
	}

	truncated := false
	if len(text) > maxLength {
		text = truncateBytes(text, maxLength)
		truncated = true
	}

	result := &ScrapeResult{
		URL:         url,
		Content:     text,
		ContentType: contentType,
		Title:       meta.Title,
		Author:      meta.Author,
		Truncated:   truncated,
	}

	return result, nil
}

func detectDocType(url, contentType string) string {
	lower := strings.ToLower(url)
	switch {
	case strings.HasSuffix(lower, ".pdf") || strings.Contains(contentType, "application/pdf"):
		return "pdf"
	case strings.HasSuffix(lower, ".docx") || strings.Contains(contentType, "openxmlformats-officedocument.wordprocessingml"):
		return "docx"
	case strings.HasSuffix(lower, ".pptx") || strings.Contains(contentType, "openxmlformats-officedocument.presentationml"):
		return "pptx"
	default:
		return "unknown"
	}
}
