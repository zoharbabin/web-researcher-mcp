package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// LensProvider searches The Lens patent database (lens.org).
// Coverage: Worldwide (100+ jurisdictions), connects patents to scholarly works.
type LensProvider struct {
	apiToken string
	baseURL  string
	deps     Deps
}

func NewLensProvider(apiToken string, deps Deps) *LensProvider {
	return &LensProvider{
		apiToken: apiToken,
		baseURL:  "https://api.lens.org",
		deps:     deps,
	}
}

func (l *LensProvider) Name() string { return "lens" }

func (l *LensProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"*"},
		Capabilities: []string{"search", "biblio", "citations", "scholarly"},
		RateClass:    "metered",
		Description:  "The Lens — worldwide patents with scholarly connections (100M+ documents)",
	}
}

func (l *LensProvider) Patents(ctx context.Context, params PatentSearchParams) ([]PatentResult, error) {
	var results []PatentResult
	err := l.deps.Breaker.Execute(func() error {
		var e error
		results, e = l.doSearch(ctx, params)
		return e
	})
	return results, err
}

func (l *LensProvider) doSearch(ctx context.Context, params PatentSearchParams) ([]PatentResult, error) {
	body := l.buildQuery(params)

	reqURL := l.baseURL + "/patent/search"
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+l.apiToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := l.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lens: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("lens: rate limited")
	}
	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("lens: authentication failed (check LENS_API_TOKEN)")
	}
	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("lens: API error %d: %s", resp.StatusCode, string(respBody))
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("lens: failed to read response: %w", err)
	}

	var response lensResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("lens: failed to parse response: %w", err)
	}

	results := make([]PatentResult, 0, len(response.Data))
	for _, doc := range response.Data {
		result := PatentResult{
			Title:    doc.Title,
			Number:   doc.patentNumber(),
			Abstract: doc.Abstract,
			Assignee: doc.firstApplicant(),
			Inventor: doc.firstInventor(),
			Filed:    doc.FilingDate,
			Granted:  doc.grantDate(),
		}
		if len(result.Abstract) > 500 {
			result.Abstract = result.Abstract[:500] + "..."
		}
		if result.Number != "" {
			result.URL = "https://patents.google.com/patent/" + result.Number
		}
		results = append(results, result)
	}
	return results, nil
}

func (l *LensProvider) buildQuery(params PatentSearchParams) []byte {
	var must []any

	if params.Query != "" {
		must = append(must, map[string]any{
			"match": map[string]any{"title": params.Query},
		})
	}
	if params.Assignee != "" {
		must = append(must, map[string]any{
			"match": map[string]any{"applicant.name": params.Assignee},
		})
	}
	if params.Inventor != "" {
		must = append(must, map[string]any{
			"match": map[string]any{"inventor.name": params.Inventor},
		})
	}
	if params.PatentOffice != "" && params.PatentOffice != "all" {
		must = append(must, map[string]any{
			"term": map[string]any{"jurisdiction": strings.ToUpper(params.PatentOffice)},
		})
	}

	dateRange := map[string]any{}
	if params.YearFrom > 0 {
		dateRange["gte"] = fmt.Sprintf("%d-01-01", params.YearFrom)
	}
	if params.YearTo > 0 {
		dateRange["lte"] = fmt.Sprintf("%d-12-31", params.YearTo)
	}
	if len(dateRange) > 0 {
		must = append(must, map[string]any{
			"range": map[string]any{"date_published": dateRange},
		})
	}

	if len(must) == 0 {
		must = append(must, map[string]any{
			"match_all": map[string]any{},
		})
	}

	query := map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"must": must,
			},
		},
		"size": clamp(params.NumResults, 1, 10),
		"from": 0,
	}

	data, _ := json.Marshal(query)
	return data
}

// SetBaseURL overrides the API base URL (testing).
func (l *LensProvider) SetBaseURL(url string) { l.baseURL = url }

// Response types

type lensResponse struct {
	Total int       `json:"total"`
	Data  []lensDoc `json:"data"`
}

type lensDoc struct {
	LensID      string        `json:"lens_id"`
	Country     string        `json:"jurisdiction"`
	DocNumber   string        `json:"doc_number"`
	Kind        string        `json:"kind"`
	Title       string        `json:"title"`
	Abstract    string        `json:"abstract"`
	FilingDate  string        `json:"date_published"`
	Applicants  []lensParty   `json:"applicants"`
	Inventors   []lensParty   `json:"inventors"`
	LegalStatus lensLegal     `json:"legal_status"`
}

type lensParty struct {
	Name string `json:"name"`
}

type lensLegal struct {
	Granted   bool   `json:"granted"`
	GrantDate string `json:"grant_date"`
}

func (d *lensDoc) patentNumber() string {
	if d.DocNumber == "" {
		return ""
	}
	country := d.Country
	if country == "" {
		return d.DocNumber
	}
	return strings.ToUpper(country) + d.DocNumber
}

func (d *lensDoc) firstApplicant() string {
	if len(d.Applicants) > 0 {
		return d.Applicants[0].Name
	}
	return ""
}

func (d *lensDoc) firstInventor() string {
	if len(d.Inventors) > 0 {
		return d.Inventors[0].Name
	}
	return ""
}

func (d *lensDoc) grantDate() string {
	if d.LegalStatus.Granted && d.LegalStatus.GrantDate != "" {
		return d.LegalStatus.GrantDate
	}
	return ""
}
