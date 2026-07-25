package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// JinaReaderURL is Jina Reader's content-extraction endpoint: a target URL is
// appended to the path and clean extracted markdown comes back. It sits
// between the stealth and html tiers in the pipeline (#270) because it is a
// cloud proxy that resolves Cloudflare/JS-heavy pages the local HTTP tiers
// cannot, without paying for the browser tier. Keyless free tier; an optional
// JinaAPIKey raises the rate limit only.
//
// Exported (unlike exaContentsURL) and a package var rather than a
// PipelineConfig field: the Jina tier is unconditional — unlike Exa (gated on
// an API key) or the browser tier (gated on Chrome availability), every
// pipeline test everywhere reaches it, including dozens of pre-existing
// internal/tools tests that construct their own scraper.Pipeline against
// local httptest servers. A single exported var lets any package redirect it
// with one TestMain, instead of threading a new config field through every
// existing call site.
var JinaReaderURL = "https://r.jina.ai/"

// scrapeJina extracts page content via Jina Reader. It runs unconditionally
// (no API key required) as the pipeline's "tier 2.5", after stealth and before
// the html tier.
//
// The same SSRF/allowlist guards as every other tier already ran in Scrape
// before this tier is reached; this method only performs the outbound request
// to the fixed, trusted r.jina.ai host, not a direct fetch of the user URL.
//
// JinaDisabled is the tier's kill switch (JINA_READER_DISABLED env var),
// mirroring ChromePath=disabled for the browser tier: unlike Exa (opt-in via
// an API key), Jina runs unconditionally, so a deployment or test context
// that wants zero dependency on this third-party proxy needs an explicit way
// to turn it off. It also keeps e2e tests deterministic: e2e spawns a real
// subprocess against local httptest servers, so JinaReaderURL cannot be
// stubbed there the way unit tests do via TestMain — without a kill switch,
// the tier makes a genuine call to production r.jina.ai during those tests.
func (p *Pipeline) scrapeJina(ctx context.Context, pageURL string, maxLength int) (*ScrapeResult, error) {
	if p.config.JinaDisabled {
		return nil, contentError(pageURL, "jina tier disabled")
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	endpoint := JinaReaderURL + url.QueryEscape(pageURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, networkError(pageURL, "jina", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Return-Format", "markdown")
	if p.config.JinaAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.config.JinaAPIKey) // never logged
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, networkError(pageURL, "jina", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, rateLimitError(pageURL, "jina")
	}
	if resp.StatusCode >= 400 {
		return nil, classifyHTTPStatus(resp.StatusCode, pageURL, "jina")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, networkError(pageURL, "jina", err)
	}

	var jr jinaResponse
	if err := json.Unmarshal(body, &jr); err != nil {
		return nil, contentError(pageURL, fmt.Sprintf("jina: parse response: %v", err))
	}
	if jr.Data.Content == "" {
		return nil, contentError(pageURL, "jina returned no extractable content")
	}

	content := jr.Data.Content
	truncated := false
	if maxLength > 0 && len(content) > maxLength {
		content = truncateBytes(content, maxLength)
		truncated = true
	}

	return &ScrapeResult{
		URL:         pageURL,
		Content:     content,
		ContentType: "text/markdown",
		Title:       jr.Data.Title,
		Tier:        "jina",
		Truncated:   truncated,
	}, nil
}

type jinaResponse struct {
	Code   int      `json:"code"`
	Status int      `json:"status"`
	Data   jinaData `json:"data"`
}

type jinaData struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}
