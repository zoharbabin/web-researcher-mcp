package scraper

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// exaContentsURL is Exa's content-extraction endpoint: a URL goes in, clean
// extracted text comes out. It is the paid final fallback tier of the pipeline,
// reached only when the free tiers (markdown→stealth→html→browser) all fail.
// A var (not const) solely so tests can point it at an httptest server, matching
// the statFile test-hook precedent in pipeline.go.
var exaContentsURL = "https://api.exa.ai/contents"

// scrapeExa extracts page text via Exa's /contents API. It is the pipeline's
// last-resort tier (configured by PipelineConfig.ExaAPIKey): a neural extractor
// that often succeeds on bot-blocked or JS-heavy pages the local tiers cannot.
//
// Provenance: Exa reports per-URL whether it served a cached copy or freshly
// crawled the page (statuses[].source). That is recorded in the result Tier as
// "exa:cached" or "exa:crawled" so the tool layer can surface honest provenance.
//
// The same SSRF/allowlist guards as every other tier already ran in Scrape
// before this tier is reached; this method only performs the outbound Exa API
// call (a fixed, trusted host), not a fetch of the user URL itself.
func (p *Pipeline) scrapeExa(ctx context.Context, pageURL string, maxLength int) (*ScrapeResult, error) {
	if p.config.ExaAPIKey == "" {
		return nil, contentError(pageURL, "exa tier not configured")
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	maxChars := maxLength
	if maxChars <= 0 {
		maxChars = 50000
	}
	payload := map[string]any{
		"urls": []string{pageURL},
		"text": map[string]any{"maxCharacters": maxChars},
	}
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return nil, contentError(pageURL, "exa: marshal request: "+err.Error())
	}

	endpoint := exaContentsURL
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, networkError(pageURL, "exa", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", p.config.ExaAPIKey) // never logged

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, networkError(pageURL, "exa", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, rateLimitError(pageURL, "exa")
	}
	if resp.StatusCode >= 400 {
		// The status code drives classification; the Exa error body is not read
		// or surfaced (it can echo the key-bearing request context).
		return nil, classifyHTTPStatus(resp.StatusCode, pageURL, "exa")
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, networkError(pageURL, "exa", err)
	}

	var er exaContentsResponse
	if err := json.Unmarshal(data, &er); err != nil {
		return nil, contentError(pageURL, "exa: parse response: "+err.Error())
	}
	if len(er.Results) == 0 || er.Results[0].Text == "" {
		return nil, contentError(pageURL, "exa returned no extractable content")
	}

	r := er.Results[0]
	result := &ScrapeResult{
		URL:         pageURL,
		Content:     r.Text,
		ContentType: "text/plain",
		Title:       r.Title,
		Author:      r.Author,
		Tier:        "exa:" + er.provenance(),
	}
	return result, nil
}

type exaContentsResponse struct {
	Results []struct {
		Title  string `json:"title"`
		URL    string `json:"url"`
		Author string `json:"author"`
		Text   string `json:"text"`
	} `json:"results"`
	Statuses []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Source string `json:"source"` // "cached" | "crawled"
	} `json:"statuses"`
}

// provenance returns the per-URL source Exa reported ("cached" or "crawled"),
// or "ok" when the status array is absent. Single-URL request ⇒ statuses[0].
func (e exaContentsResponse) provenance() string {
	if len(e.Statuses) > 0 && e.Statuses[0].Source != "" {
		return e.Statuses[0].Source
	}
	return "ok"
}
