package scraper

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

func (p *Pipeline) scrapeMarkdown(ctx context.Context, url string, maxLength int) (*ScrapeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "text/markdown, text/plain;q=0.9")
	req.Header.Set("User-Agent", "web-researcher-mcp/1.0 (content extraction)")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, networkError(url, "markdown", err)
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/markdown") && !strings.Contains(ct, "text/plain") {
		return nil, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxLength)+1024))
	if err != nil {
		return nil, err
	}

	content := string(body)
	if !isMarkdown(content) {
		return nil, nil
	}

	truncated := false
	if len(content) > maxLength {
		content = content[:maxLength]
		truncated = true
	}

	return &ScrapeResult{
		URL:         url,
		Content:     content,
		ContentType: "markdown",
		Truncated:   truncated,
	}, nil
}

func isMarkdown(content string) bool {
	if len(content) < 50 {
		return false
	}

	indicators := 0
	if strings.Contains(content, "# ") {
		indicators++
	}
	if strings.Contains(content, "## ") {
		indicators++
	}
	if strings.Contains(content, "```") {
		indicators++
	}
	if strings.Contains(content, "- ") || strings.Contains(content, "* ") {
		indicators++
	}
	if strings.Contains(content, "[") && strings.Contains(content, "](") {
		indicators++
	}

	return indicators >= 2
}
