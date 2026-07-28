package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
)

// ScholarAPI (scholarapi.net) is a paid academic search API whose differentiator
// is full-text retrieval: /text/{id} returns clean, pre-extracted plain text for
// a paper with no scraping and no publisher bot-wall (#266). has_text/has_pdf on
// each search result signal availability before a caller commits to a fetch —
// signals absent from every other provider in this stack.
//
// Deliberately excluded from SupportedAcademicProviders (see domain.go): it is
// metered at 10 credits/search call, so auto-routing would burn credits on every
// fallback pass. Reachable only via explicit provider=scholarapi.
//
// Auth is the X-API-Key header (capitalized — unlike Semantic Scholar's lowercase
// x-api-key). Rate limiting uses Retry-After on 429. Credits-exhausted is 402 — a
// billing state, not a service failure, so it must never trip the circuit breaker
// (isPaymentRequired below).
const scholarAPIBaseURL = "https://scholarapi.net/api"

// ScholarAPIProvider talks to the ScholarAPI REST API. Immutable after
// construction — apiKey and baseURL are never mutated, so no locking is needed
// for concurrent use.
type ScholarAPIProvider struct {
	apiKey  string
	baseURL string
	deps    Deps
}

// NewScholarAPIProvider creates a ScholarAPI provider. The key is sent as the
// X-API-Key header on every request and is never logged.
func NewScholarAPIProvider(apiKey string, deps Deps) *ScholarAPIProvider {
	return &ScholarAPIProvider{apiKey: apiKey, baseURL: scholarAPIBaseURL, deps: deps}
}

// SetBaseURL overrides the API base URL (used in testing).
func (p *ScholarAPIProvider) SetBaseURL(base string) { p.baseURL = base }

func (p *ScholarAPIProvider) Name() string { return "scholarapi" }

func (p *ScholarAPIProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"*"},
		Capabilities: []string{"search", "scholarly", "fulltext"},
		RateClass:    "metered",
		Description:  "ScholarAPI — paid academic search with full-text retrieval (scholarapi.net); no retraction signal, abstract coverage intermittent",
	}
}

// Scholarly runs a relevance-ranked keyword/boolean search via GET /search.
func (p *ScholarAPIProvider) Scholarly(ctx context.Context, params AcademicSearchParams) ([]AcademicResult, error) {
	var results []AcademicResult
	err := p.deps.Breaker.Execute(func() error {
		var er error
		results, er = p.doSearch(ctx, params)
		return er
	})
	return results, err
}

func (p *ScholarAPIProvider) doSearch(ctx context.Context, params AcademicSearchParams) ([]AcademicResult, error) {
	query := strings.TrimSpace(params.Query)
	if query == "" {
		return nil, nil
	}
	q := url.Values{}
	q.Set("q", query)
	q.Set("limit", strconv.Itoa(clamp(params.NumResults, 1, 20)))

	var resp scholarAPISearchResponse
	if err := p.doRequest(ctx, "/search?"+q.Encode(), &resp); err != nil {
		return nil, err
	}

	results := make([]AcademicResult, 0, len(resp.Papers))
	for _, paper := range resp.Papers {
		if paper.Title == "" {
			continue
		}
		results = append(results, paper.toAcademicResult())
	}
	return results, nil
}

// ResolveByDOI implements DOIResolver via a fuzzy /search?q={doi}&limit=1 —
// ScholarAPI has no exact-entity DOI lookup endpoint, so the top hit's DOI is
// validated to match the queried DOI (case-insensitively) before being
// returned. A mismatch returns (nil, nil): the caller (verify_citation) must
// never be shown a different paper's record as if it belonged to this DOI.
func (p *ScholarAPIProvider) ResolveByDOI(ctx context.Context, doi string) (*AcademicResult, error) {
	wantDOI := strings.TrimSpace(doi)
	if wantDOI == "" {
		return nil, nil
	}
	var out *AcademicResult
	err := p.deps.Breaker.Execute(func() error {
		results, er := p.doSearch(ctx, AcademicSearchParams{Query: wantDOI, NumResults: 1})
		if er != nil || len(results) == 0 {
			return er
		}
		if !strings.EqualFold(strings.TrimSpace(results[0].DOI), wantDOI) {
			return nil // mismatch — not this DOI
		}
		r := results[0]
		out = &r
		return nil
	})
	return out, err
}

// FetchText implements FullTextFetcher via GET /text/{id}. Returns ("", nil)
// when the ID has no full text on record (HTTP 404) — a soft miss, not an error.
func (p *ScholarAPIProvider) FetchText(ctx context.Context, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", nil
	}
	var text string
	err := p.deps.Breaker.Execute(func() error {
		var resp scholarAPITextResponse
		er := p.doRequest(ctx, "/text/"+url.PathEscape(id), &resp)
		if er != nil {
			if strings.Contains(er.Error(), "not found") {
				return nil
			}
			return er
		}
		text = resp.Text
		return nil
	})
	return text, err
}

// FetchTexts implements FullTextFetcher via GET /texts/{ids} (batch, up to 100
// IDs). IDs with no full text are simply absent from the returned map rather
// than failing the whole batch.
func (p *ScholarAPIProvider) FetchTexts(ctx context.Context, ids []string) (map[string]string, error) {
	clean := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			clean = append(clean, id)
		}
	}
	if len(clean) == 0 {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(clean))
	err := p.deps.Breaker.Execute(func() error {
		var resp scholarAPITextsResponse
		if er := p.doRequest(ctx, "/texts/"+url.PathEscape(strings.Join(clean, ",")), &resp); er != nil {
			return er
		}
		for id, text := range resp.Texts {
			if text != "" {
				out[id] = text
			}
		}
		return nil
	})
	return out, err
}

// --- Shared HTTP core -------------------------------------------------------

// isPaymentRequired reports whether statusCode is ScholarAPI's credits-exhausted
// response. A 402 is a billing state, not a service-health signal, so it must
// never count toward the circuit breaker's failure threshold.
func isPaymentRequired(statusCode int) bool { return statusCode == http.StatusPaymentRequired }

// doRequest GETs a ScholarAPI endpoint with X-API-Key auth and decodes the JSON
// response into out. Error classification: 429 → wrapped circuit.ErrRateLimit
// (opens the breaker immediately); 402 → wrapped circuit.ErrNonTripping (a
// billing/credits-exhausted state, never counted as a breaker failure — see
// isPaymentRequired); 401 → "authentication failed"; any other >=400 → a
// descriptive upstream error.
func (p *ScholarAPIProvider) doRequest(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Key", p.apiKey) // never logged

	resp, err := p.deps.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("scholarapi: not found")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("scholarapi: rate limited: %w", circuit.ErrRateLimit)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("scholarapi: authentication failed — check SCHOLAR_API_KEY")
	}
	if isPaymentRequired(resp.StatusCode) {
		return fmt.Errorf("scholarapi: credits exhausted (402) — see https://scholarapi.net for billing: %w", circuit.ErrNonTripping)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("scholarapi: API error %d: %s", resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return fmt.Errorf("scholarapi: failed to read response: %w", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("scholarapi: failed to parse response: %w", err)
	}
	return nil
}

// --- Response shapes ---------------------------------------------------------

type scholarAPISearchResponse struct {
	Papers []scholarAPIPaper `json:"papers"`
}

type scholarAPIPaper struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	DOI           string   `json:"doi"`
	Authors       []string `json:"authors"`
	Journal       string   `json:"journal"`
	PublishedDate string   `json:"published_date"`
	Abstract      string   `json:"abstract"`
	HasText       bool     `json:"has_text"`
	HasPDF        bool     `json:"has_pdf"`
}

type scholarAPITextResponse struct {
	Text string `json:"text"`
}

type scholarAPITextsResponse struct {
	Texts map[string]string `json:"texts"`
}

// toAcademicResult maps a ScholarAPI paper to the shared AcademicResult shape.
// CitationCount/OpenAccess/PDFUrl are left at their zero values: ScholarAPI
// supplies none of them (OpenAccess/PDFUrl are filled post-search by the
// existing Unpaywall enrichment when a DOI is present, exactly as for every
// other provider — nothing provider-specific needed here).
func (paper scholarAPIPaper) toAcademicResult() AcademicResult {
	r := AcademicResult{
		Title:    strings.TrimSpace(paper.Title),
		URL:      paper.URL,
		DOI:      strings.TrimSpace(paper.DOI),
		Authors:  paper.Authors,
		Journal:  paper.Journal,
		Year:     scholarAPIYear(paper.PublishedDate),
		Abstract: truncateText(paper.Abstract, 500),
		Source:   "scholarapi",
		HasText:  paper.HasText,
		HasPDF:   paper.HasPDF,
	}
	if r.URL == "" && r.DOI != "" {
		r.URL = "https://doi.org/" + r.DOI
	}
	return r
}

// scholarAPIYear parses the leading 4-digit year out of an ISO-8601-ish
// published_date string ("2024-03-15"). Returns 0 when absent/unparseable.
func scholarAPIYear(date string) int {
	date = strings.TrimSpace(date)
	if len(date) >= 4 {
		if y, err := strconv.Atoi(date[:4]); err == nil && y > 1000 {
			return y
		}
	}
	return 0
}

var (
	_ AcademicProvider = (*ScholarAPIProvider)(nil)
	_ DOIResolver      = (*ScholarAPIProvider)(nil)
	_ FullTextFetcher  = (*ScholarAPIProvider)(nil)
)
