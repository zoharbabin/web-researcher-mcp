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
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d fetching document", resp.StatusCode)
	}

	// Limit download to 10MB
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, err
	}

	contentType := detectDocType(url, resp.Header.Get("Content-Type"))
	text, meta, err := documents.Parse(body, contentType)
	if err != nil {
		return nil, fmt.Errorf("document parse error: %w", err)
	}

	truncated := false
	if len(text) > maxLength {
		text = text[:maxLength]
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
