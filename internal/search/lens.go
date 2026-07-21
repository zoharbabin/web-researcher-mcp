package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/zoharbabin/web-researcher-mcp/internal/circuit"
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
		return nil, fmt.Errorf("lens: rate limited: %w", circuit.ErrRateLimit)
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
			Title:    doc.title(),
			Number:   doc.patentNumber(),
			Abstract: doc.abstract(),
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
			"match_phrase": map[string]any{"applicant.name": params.Assignee},
		})
	}
	if params.Inventor != "" {
		must = append(must, map[string]any{
			"match_phrase": map[string]any{"inventor.name": params.Inventor},
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
	LensID      string          `json:"lens_id"`
	Country     string          `json:"jurisdiction"`
	DocNumber   json.RawMessage `json:"doc_number"`
	Kind        string          `json:"kind"`
	RawAbstract json.RawMessage `json:"abstract"`
	FilingDate  string          `json:"date_published"`
	Biblio      lensBiblio      `json:"biblio"`
	LegalStatus lensLegal       `json:"legal_status"`
}

type lensBiblio struct {
	InventionTitle json.RawMessage `json:"invention_title"`
	Parties        lensParties     `json:"parties"`
}

type lensParties struct {
	Applicants []lensPartyEntry `json:"applicants"`
	Inventors  []lensPartyEntry `json:"inventors"`
}

type lensPartyEntry struct {
	ExtractedName lensExtractedName `json:"extracted_name"`
}

type lensExtractedName struct {
	Value string `json:"value"`
}

func (d *lensDoc) abstract() string {
	if len(d.RawAbstract) == 0 {
		return ""
	}
	// Try array of objects: [{"text": "...", "lang": "..."}]
	var arrObj []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(d.RawAbstract, &arrObj) == nil && len(arrObj) > 0 && arrObj[0].Text != "" {
		parts := make([]string, 0, len(arrObj))
		for _, o := range arrObj {
			if o.Text != "" {
				parts = append(parts, o.Text)
			}
		}
		return strings.Join(parts, " ")
	}
	// Try single object: {"text": "...", "lang": "..."}
	var obj struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(d.RawAbstract, &obj) == nil && obj.Text != "" {
		return obj.Text
	}
	// Try plain string
	var s string
	if json.Unmarshal(d.RawAbstract, &s) == nil && s != "" {
		return s
	}
	// Try array of strings
	var arr []string
	if json.Unmarshal(d.RawAbstract, &arr) == nil && len(arr) > 0 {
		return strings.Join(arr, " ")
	}
	return ""
}

type lensLegal struct {
	Granted   bool   `json:"granted"`
	GrantDate string `json:"grant_date"`
}

func (d *lensDoc) title() string {
	if len(d.Biblio.InventionTitle) == 0 {
		return ""
	}
	// Try object: {"text": "...", "lang": "..."}
	var obj struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(d.Biblio.InventionTitle, &obj) == nil && obj.Text != "" {
		return obj.Text
	}
	// Try array: [{"text": "...", "lang": "..."}]
	var arr []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(d.Biblio.InventionTitle, &arr) == nil && len(arr) > 0 {
		return arr[0].Text
	}
	// Try plain string
	var s string
	if json.Unmarshal(d.Biblio.InventionTitle, &s) == nil {
		return s
	}
	return ""
}

func (d *lensDoc) docNumber() string {
	if len(d.DocNumber) == 0 {
		return ""
	}
	// API returns doc_number as either string or number
	var s string
	if json.Unmarshal(d.DocNumber, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(d.DocNumber, &n) == nil {
		return n.String()
	}
	return ""
}

func (d *lensDoc) patentNumber() string {
	num := d.docNumber()
	if num == "" {
		return ""
	}
	country := d.Country
	if country == "" {
		return num
	}
	return strings.ToUpper(country) + num
}

func (d *lensDoc) firstApplicant() string {
	if len(d.Biblio.Parties.Applicants) > 0 {
		return d.Biblio.Parties.Applicants[0].ExtractedName.Value
	}
	return ""
}

func (d *lensDoc) firstInventor() string {
	if len(d.Biblio.Parties.Inventors) > 0 {
		return d.Biblio.Parties.Inventors[0].ExtractedName.Value
	}
	return ""
}

func (d *lensDoc) grantDate() string {
	if d.LegalStatus.Granted && d.LegalStatus.GrantDate != "" {
		return d.LegalStatus.GrantDate
	}
	return ""
}
