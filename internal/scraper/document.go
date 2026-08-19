package scraper

import (
	"bytes"
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

// looksLikeHTML reports whether a document-route response is actually HTML,
// the reverse case of looksLikePDF/isPDFContentType (#631): a URL whose path
// ends in .pdf/.docx/.pptx is a naming convention, not a guarantee — a
// publisher's "download" link can gate the real file behind a session/auth
// redirect that lands on an HTML page instead (nature.com's open-access PDF
// link 303s through an auth handshake onto the full HTML article). Checked
// before any document parse is attempted, so that case gets real HTML
// extraction instead of a doomed PDF/DOCX/PPTX parse.
func looksLikeHTML(contentType string, body []byte) bool {
	if strings.Contains(strings.ToLower(contentType), "text/html") {
		return true
	}
	trimmed := bytes.ToLower(bytes.TrimSpace(body))
	return bytes.HasPrefix(trimmed, []byte("<!doctype html")) || bytes.HasPrefix(trimmed, []byte("<html"))
}

// scrapeBodyAsPDF parses already-downloaded bytes as a PDF document (#206), so
// the stealth and HTML tiers can re-route a PDF response without a second
// round-trip when the URL does not end in .pdf but the Content-Type or magic
// bytes reveal it is one. rawContentType is the server's real Content-Type
// header, threaded through only to stamp rawContentType on the result (raw
// mode's own bytes are the same PDF bytes as full mode — a PDF has no
// separate "unfiltered" representation beyond the file itself — so raw is
// accepted but unused: it exists to keep this call symmetric with the other
// raw-aware tier helpers).
func (p *Pipeline) scrapeBodyAsPDF(rawURL string, body []byte, maxLength int, raw bool, rawContentType string) (*ScrapeResult, error) {
	text, meta, err := documents.Parse(body, "pdf")
	if err != nil {
		return nil, fmt.Errorf("document parse error: %w", err)
	}
	truncated := false
	if len(text) > maxLength {
		text = truncateBytes(text, maxLength)
		truncated = true
	}
	// Raw mode's returned body must respect the caller's maxLength the same
	// way Content does, even though the underlying PDF bytes may be larger.
	rawBody := string(body)
	if len(rawBody) > maxLength {
		rawBody = rawBody[:maxLength]
		truncated = true
	}
	return &ScrapeResult{
		URL:            rawURL,
		Content:        text,
		ContentType:    "pdf",
		Title:          meta.Title,
		Author:         meta.Author,
		Truncated:      truncated,
		Tier:           "document",
		rawBody:        rawBody,
		rawContentType: rawContentType,
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

	respContentType := resp.Header.Get("Content-Type")
	if looksLikeHTML(respContentType, body) {
		return p.parseHTMLBody(ctx, url, body, respContentType, maxLength)
	}

	contentType := detectDocType(url, respContentType)
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
