package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// CourtListenerProvider implements CaseSearcher over the CourtListener (Free Law
// Project) v4 API: 11M+ federal and state opinions with Bluebook citations.
// Works without a token at a lower shared rate; a token raises the limit.
//
// Live-verified (2026-06-06):
//
//	GET https://www.courtlistener.com/api/rest/v4/search/?q=&type=o&court=&filed_after=&filed_before=
//	→ {count, results:[{caseName,citation:[…],court,court_id,dateFiled,
//	    docketNumber,citeCount,absolute_url}]}
type CourtListenerProvider struct {
	token   string
	baseURL string
	deps    Deps
}

// NewCourtListenerProvider creates the provider. token may be "" (keyless).
func NewCourtListenerProvider(token string, deps Deps) *CourtListenerProvider {
	return &CourtListenerProvider{
		token:   token,
		baseURL: "https://www.courtlistener.com/api/rest/v4",
		deps:    deps,
	}
}

func (c *CourtListenerProvider) Name() string { return "courtlistener" }

func (c *CourtListenerProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"US"},
		Capabilities: []string{"search", "caselaw", "citations"},
		RateClass:    "free",
		Description:  "CourtListener (Free Law Project) — 11M+ US federal and state court opinions with Bluebook citations",
	}
}

// SetBaseURL overrides the API base URL (testing).
func (c *CourtListenerProvider) SetBaseURL(base string) { c.baseURL = base }

func (c *CourtListenerProvider) Cases(ctx context.Context, params CaseSearchParams) ([]CaseResult, error) {
	var results []CaseResult
	err := c.deps.Breaker.Execute(func() error {
		var er error
		results, er = c.doSearch(ctx, params)
		return er
	})
	return results, err
}

func (c *CourtListenerProvider) doSearch(ctx context.Context, params CaseSearchParams) ([]CaseResult, error) {
	num := clamp(params.NumResults, 1, 20)

	q := url.Values{}
	q.Set("q", params.Query)
	q.Set("type", "o") // opinions
	if params.Jurisdiction != "" {
		q.Set("court", params.Jurisdiction)
	}
	if params.DateFrom != "" {
		q.Set("filed_after", params.DateFrom)
	}
	if params.DateTo != "" {
		q.Set("filed_before", params.DateTo)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/search/?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Token "+c.token) // never logged
	}

	resp, err := c.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("courtlistener: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("courtlistener: rate limited")
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("courtlistener: authentication failed (check COURTLISTENER_API_TOKEN)")
	}
	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("courtlistener: API error %d: %s", resp.StatusCode, string(b))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("courtlistener: read: %w", err)
	}

	var parsed struct {
		Results []struct {
			CaseName     string   `json:"caseName"`
			Citation     []string `json:"citation"`
			Court        string   `json:"court"`
			CourtID      string   `json:"court_id"`
			DateFiled    string   `json:"dateFiled"`
			DocketNumber string   `json:"docketNumber"`
			CiteCount    int      `json:"citeCount"`
			AbsoluteURL  string   `json:"absolute_url"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("courtlistener: parse: %w", err)
	}

	out := make([]CaseResult, 0, num)
	for _, r := range parsed.Results {
		out = append(out, CaseResult{
			CaseName:      r.CaseName,
			Citation:      strings.Join(r.Citation, "; "),
			Court:         r.Court,
			CourtID:       r.CourtID,
			DateFiled:     r.DateFiled,
			DocketNumber:  r.DocketNumber,
			CitationCount: r.CiteCount,
			URL:           courtListenerURL(r.AbsoluteURL),
			Source:        "courtlistener",
		})
		if len(out) >= num {
			break
		}
	}
	return out, nil
}

// courtListenerURL absolutizes the API's relative absolute_url ("/opinion/…").
func courtListenerURL(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "http") {
		return path
	}
	return "https://www.courtlistener.com" + path
}

var _ CaseProvider = (*CourtListenerProvider)(nil)
