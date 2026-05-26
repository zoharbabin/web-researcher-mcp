package search

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// EPOProvider searches the European Patent Office Open Patent Services (OPS) API.
// Coverage: Worldwide (100M+ patent documents across all major offices).
type EPOProvider struct {
	consumerKey    string
	consumerSecret string
	baseURL        string
	tokenURL       string
	deps           Deps
	tokens         *epoTokenStore
}

func NewEPOProvider(consumerKey, consumerSecret string, deps Deps) *EPOProvider {
	return &EPOProvider{
		consumerKey:    consumerKey,
		consumerSecret: consumerSecret,
		baseURL:        "https://ops.epo.org/3.2/rest-services",
		tokenURL:       "https://ops.epo.org/3.2/auth/accesstoken",
		deps:           deps,
		tokens:         &epoTokenStore{},
	}
}

func (e *EPOProvider) Name() string { return "epo" }

func (e *EPOProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"*"},
		Capabilities: []string{"search", "biblio", "family", "citations"},
		RateClass:    "free",
		Description:  "European Patent Office OPS — worldwide patent data (free, rate-limited)",
	}
}

func (e *EPOProvider) Patents(ctx context.Context, params PatentSearchParams) ([]PatentResult, error) {
	var results []PatentResult
	err := e.deps.Breaker.Execute(func() error {
		var er error
		results, er = e.doSearch(ctx, params)
		return er
	})
	return results, err
}

func (e *EPOProvider) doSearch(ctx context.Context, params PatentSearchParams) ([]PatentResult, error) {
	token, err := e.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("epo: authentication failed: %w", err)
	}

	cql := e.buildCQL(params)
	num := clamp(params.NumResults, 1, 25)

	searchURL := e.baseURL + "/published-data/search/biblio?q=" + url.QueryEscape(cql)
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("X-OPS-Range", fmt.Sprintf("1-%d", num))

	resp, err := e.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("epo: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		e.tokens.Invalidate()
		token2, err2 := e.refreshToken(ctx)
		if err2 != nil {
			return nil, fmt.Errorf("epo: token refresh failed: %w", err2)
		}
		req2, _ := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
		req2.Header.Set("Authorization", "Bearer "+token2)
		req2.Header.Set("Accept", "application/xml")
		req2.Header.Set("X-OPS-Range", fmt.Sprintf("1-%d", num))
		resp, err = e.deps.HTTPClient.Do(req2)
		if err != nil {
			return nil, fmt.Errorf("epo: retry request failed: %w", err)
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode == 403 {
		reason := resp.Header.Get("X-Rejection-Reason")
		if reason != "" {
			return nil, fmt.Errorf("epo: quota exceeded (%s)", reason)
		}
		return nil, fmt.Errorf("epo: rate limited (403)")
	}
	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("epo: rate limited")
	}
	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("epo: API error %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("epo: failed to read response: %w", err)
	}

	return parseEPOResponse(body)
}

func (e *EPOProvider) buildCQL(params PatentSearchParams) string {
	var clauses []string

	if params.Query != "" {
		clauses = append(clauses, fmt.Sprintf("txt=%q", params.Query))
	}
	if params.Assignee != "" {
		clauses = append(clauses, fmt.Sprintf("pa=%q", params.Assignee))
	}
	if params.Inventor != "" {
		clauses = append(clauses, fmt.Sprintf("in=%q", params.Inventor))
	}
	if params.PatentOffice != "" && params.PatentOffice != "all" {
		clauses = append(clauses, fmt.Sprintf("pn=%s", params.PatentOffice))
	}
	if params.YearFrom > 0 {
		clauses = append(clauses, fmt.Sprintf("pd>=%d", params.YearFrom))
	}
	if params.YearTo > 0 {
		clauses = append(clauses, fmt.Sprintf("pd<=%d", params.YearTo))
	}

	if len(clauses) == 0 {
		return "txt=patent"
	}
	return strings.Join(clauses, " AND ")
}

func (e *EPOProvider) getToken(ctx context.Context) (string, error) {
	if token, valid := e.tokens.Get(); valid {
		return token, nil
	}
	return e.refreshToken(ctx)
}

func (e *EPOProvider) refreshToken(ctx context.Context) (string, error) {
	e.tokens.mu.Lock()
	defer e.tokens.mu.Unlock()

	// Double-check after acquiring write lock
	if e.tokens.token != "" && time.Now().Before(e.tokens.expires) {
		return e.tokens.token, nil
	}

	data := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, "POST", e.tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(e.consumerKey, e.consumerSecret)

	resp, err := e.deps.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}

	// EPO returns: {"access_token":"...","token_type":"BearerToken","expires_in":1200}
	type tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	var tr tokenResp
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("failed to parse token response: %w", err)
	}

	expiry := time.Duration(tr.ExpiresIn) * time.Second
	if expiry > 60*time.Second {
		expiry -= 60 * time.Second // refresh buffer
	}

	e.tokens.token = tr.AccessToken
	e.tokens.expires = time.Now().Add(expiry)
	return tr.AccessToken, nil
}

// SetBaseURL overrides API base URL (testing).
func (e *EPOProvider) SetBaseURL(base string) { e.baseURL = base }

// SetTokenURL overrides token endpoint URL (testing).
func (e *EPOProvider) SetTokenURL(u string) { e.tokenURL = u }

// Thread-safe token storage
type epoTokenStore struct {
	mu      sync.RWMutex
	token   string
	expires time.Time
}

func (s *epoTokenStore) Get() (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.token != "" && time.Now().Before(s.expires) {
		return s.token, true
	}
	return "", false
}

func (s *epoTokenStore) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = ""
	s.expires = time.Time{}
}

// XML response parsing for EPO OPS bibliographic data

type opsWorldPatentData struct {
	XMLName  xml.Name        `xml:"world-patent-data"`
	Search   opsSearchResult `xml:"biblio-search"`
}

type opsSearchResult struct {
	TotalCount int                `xml:"total-result-count,attr"`
	Documents  []opsExchangeDoc   `xml:"search-result>exchange-documents>exchange-document"`
}

type opsExchangeDoc struct {
	Country  string          `xml:"country,attr"`
	DocNum   string          `xml:"doc-number,attr"`
	Kind     string          `xml:"kind,attr"`
	Biblio   opsBiblioData   `xml:"bibliographic-data"`
	Abstract []opsAbstract   `xml:"abstract"`
}

type opsBiblioData struct {
	Title      []opsTitle     `xml:"invention-title"`
	Applicants []opsApplicant `xml:"parties>applicants>applicant"`
	Inventors  []opsInventor  `xml:"parties>inventors>inventor"`
	AppRef     opsDocRef      `xml:"application-reference>document-id"`
	PubRef     []opsDocRef    `xml:"publication-reference>document-id"`
}

type opsTitle struct {
	Lang  string `xml:"lang,attr"`
	Value string `xml:",chardata"`
}

type opsApplicant struct {
	Name string `xml:"applicant-name>name"`
}

type opsInventor struct {
	Name string `xml:"inventor-name>name"`
}

type opsDocRef struct {
	Country string `xml:"country"`
	DocNum  string `xml:"doc-number"`
	Kind    string `xml:"kind"`
	Date    string `xml:"date"`
}

type opsAbstract struct {
	Lang string `xml:"lang,attr"`
	Text string `xml:"p"`
}

func parseEPOResponse(data []byte) ([]PatentResult, error) {
	var world opsWorldPatentData
	if err := xml.Unmarshal(data, &world); err != nil {
		return nil, fmt.Errorf("epo: failed to parse XML: %w", err)
	}

	results := make([]PatentResult, 0, len(world.Search.Documents))
	for _, doc := range world.Search.Documents {
		result := PatentResult{
			Number: doc.Country + doc.DocNum,
		}

		// Title: prefer English
		for _, t := range doc.Biblio.Title {
			if result.Title == "" || t.Lang == "en" {
				result.Title = strings.TrimSpace(t.Value)
			}
		}

		// Assignee: first applicant
		if len(doc.Biblio.Applicants) > 0 {
			result.Assignee = strings.TrimSpace(doc.Biblio.Applicants[0].Name)
		}

		// Inventor: first inventor
		if len(doc.Biblio.Inventors) > 0 {
			result.Inventor = strings.TrimSpace(doc.Biblio.Inventors[0].Name)
		}

		// Filing date from application reference
		if doc.Biblio.AppRef.Date != "" {
			result.Filed = formatEPODate(doc.Biblio.AppRef.Date)
		}

		// Publication date (as granted proxy)
		for _, ref := range doc.Biblio.PubRef {
			if ref.Date != "" {
				result.Granted = formatEPODate(ref.Date)
				break
			}
		}

		// Abstract: prefer English
		for _, abs := range doc.Abstract {
			if result.Abstract == "" || abs.Lang == "en" {
				result.Abstract = strings.TrimSpace(abs.Text)
			}
		}
		if len(result.Abstract) > 500 {
			result.Abstract = result.Abstract[:500] + "..."
		}

		result.URL = "https://patents.google.com/patent/" + result.Number
		results = append(results, result)
	}

	return results, nil
}

// formatEPODate converts YYYYMMDD to YYYY-MM-DD
func formatEPODate(d string) string {
	d = strings.TrimSpace(d)
	if len(d) == 8 {
		return d[:4] + "-" + d[4:6] + "-" + d[6:8]
	}
	return d
}

