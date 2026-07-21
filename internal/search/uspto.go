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

// USPTOProvider searches the USPTO Patent File Wrapper (PEDS) API.
// Coverage: US patents and published applications.
type USPTOProvider struct {
	apiKey  string
	baseURL string
	deps    Deps
}

func NewUSPTOProvider(apiKey string, deps Deps) *USPTOProvider {
	return &USPTOProvider{
		apiKey:  apiKey,
		baseURL: "https://api.uspto.gov/api/v1/patent/applications/search",
		deps:    deps,
	}
}

func (u *USPTOProvider) Name() string { return "uspto" }

func (u *USPTOProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"US"},
		Capabilities: []string{"search", "biblio"},
		RateClass:    "metered",
		Description:  "US Patent and Trademark Office — US patents and applications",
	}
}

func (u *USPTOProvider) Patents(ctx context.Context, params PatentSearchParams) ([]PatentResult, error) {
	if params.PatentOffice != "" && params.PatentOffice != "all" &&
		!strings.EqualFold(params.PatentOffice, "US") {
		return nil, nil
	}

	var results []PatentResult
	err := u.deps.Breaker.Execute(func() error {
		var e error
		results, e = u.doSearch(ctx, params)
		return e
	})
	return results, err
}

func (u *USPTOProvider) doSearch(ctx context.Context, params PatentSearchParams) ([]PatentResult, error) {
	query := u.buildQuery(params)

	q := url.Values{}
	q.Set("q", query)
	q.Set("rows", strconv.Itoa(clamp(params.NumResults, 1, 10)))

	reqURL := u.baseURL + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-API-KEY", u.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := u.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("uspto: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("uspto: rate limited: %w", circuit.ErrRateLimit)
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("uspto: authentication failed (check USPTO_API_KEY)")
	}
	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("uspto: API error %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("uspto: failed to read response: %w", err)
	}

	var response usptoResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("uspto: failed to parse response: %w", err)
	}

	results := make([]PatentResult, 0, len(response.PatentFileWrapperDataBag))
	for _, item := range response.PatentFileWrapperDataBag {
		meta := item.ApplicationMetaData
		patentNum := meta.PatentNumber
		appNum := item.ApplicationNumberText

		number := ""
		if patentNum != "" {
			number = "US" + patentNum
		} else if appNum != "" {
			number = "US" + appNum
		}

		assignee := meta.FirstApplicantName
		if assignee == "" && len(meta.ApplicantBag) > 0 {
			assignee = meta.ApplicantBag[0].ApplicantNameText
		}
		if assignee == "" {
			assignee = u.assigneeFromAssignments(item.AssignmentBag)
		}

		inventor := meta.FirstInventorName
		if inventor == "" && len(meta.InventorBag) > 0 {
			inv := meta.InventorBag[0]
			inventor = strings.TrimSpace(inv.FirstName + " " + inv.LastName)
		}

		result := PatentResult{
			Title:    meta.InventionTitle,
			Number:   number,
			Abstract: "",
			Assignee: assignee,
			Inventor: inventor,
			Filed:    meta.FilingDate,
			Granted:  meta.GrantDate,
			Status:   meta.ApplicationStatusDescriptionText,
		}
		if result.Number != "" {
			result.URL = "https://patents.google.com/patent/" + result.Number
		}
		results = append(results, result)
	}

	// Defensive cap: the USPTO API can return more rows than requested (the
	// `rows` param is not always honored), so enforce the caller's limit here
	// to match every other provider's contract.
	if n := clamp(params.NumResults, 1, 10); len(results) > n {
		results = results[:n]
	}
	return results, nil
}

func (u *USPTOProvider) assigneeFromAssignments(assignments []usptoAssignment) string {
	if len(assignments) == 0 {
		return ""
	}
	last := assignments[len(assignments)-1]
	if len(last.AssigneeBag) > 0 {
		return last.AssigneeBag[0].AssigneeNameText
	}
	return ""
}

func (u *USPTOProvider) buildQuery(params PatentSearchParams) string {
	// USPTO PEDS API uses simple full-text search. Field-qualified queries
	// (applicationMetaData.inventionTitle:...) and sort parameters are rejected
	// with HTTP 400. Use quoted phrases for precision.
	var parts []string

	if params.Query != "" {
		parts = append(parts, fmt.Sprintf("%q", params.Query))
	}
	if params.Assignee != "" {
		parts = append(parts, fmt.Sprintf("%q", params.Assignee))
	}
	if params.Inventor != "" {
		parts = append(parts, fmt.Sprintf("%q", params.Inventor))
	}

	if len(parts) == 0 {
		return "*"
	}
	return strings.Join(parts, " ")
}

// SetBaseURL overrides the API base URL (used in testing).
func (u *USPTOProvider) SetBaseURL(url string) { u.baseURL = url }

// Response types matching the USPTO Patent File Wrapper (PEDS) API schema.

type usptoResponse struct {
	Count                    int                       `json:"count"`
	PatentFileWrapperDataBag []usptoFileWrapperDataBag `json:"patentFileWrapperDataBag"`
}

type usptoFileWrapperDataBag struct {
	ApplicationNumberText string                   `json:"applicationNumberText"`
	ApplicationMetaData   usptoApplicationMetaData `json:"applicationMetaData"`
	AssignmentBag         []usptoAssignment        `json:"assignmentBag"`
}

type usptoApplicationMetaData struct {
	InventionTitle                   string           `json:"inventionTitle"`
	PatentNumber                     string           `json:"patentNumber"`
	FilingDate                       string           `json:"filingDate"`
	EffectiveFilingDate              string           `json:"effectiveFilingDate"`
	GrantDate                        string           `json:"grantDate"`
	ApplicationStatusDescriptionText string           `json:"applicationStatusDescriptionText"`
	ApplicationStatusCode            int              `json:"applicationStatusCode"`
	FirstApplicantName               string           `json:"firstApplicantName"`
	FirstInventorName                string           `json:"firstInventorName"`
	ApplicationTypeCategory          string           `json:"applicationTypeCategory"`
	GroupArtUnitNumber               string           `json:"groupArtUnitNumber"`
	CPCClassificationBag             []string         `json:"cpcClassificationBag"`
	EarliestPublicationNumber        string           `json:"earliestPublicationNumber"`
	EarliestPublicationDate          string           `json:"earliestPublicationDate"`
	ApplicantBag                     []usptoApplicant `json:"applicantBag"`
	InventorBag                      []usptoInventor  `json:"inventorBag"`
}

type usptoApplicant struct {
	ApplicantNameText string `json:"applicantNameText"`
	FirstName         string `json:"firstName"`
	LastName          string `json:"lastName"`
	CountryCode       string `json:"countryCode"`
}

type usptoInventor struct {
	FirstName        string `json:"firstName"`
	MiddleName       string `json:"middleName"`
	LastName         string `json:"lastName"`
	InventorNameText string `json:"inventorNameText"`
	CountryCode      string `json:"countryCode"`
}

type usptoAssignment struct {
	ConveyanceText string              `json:"conveyanceText"`
	AssignorBag    []usptoAssignor     `json:"assignorBag"`
	AssigneeBag    []usptoAssigneeInfo `json:"assigneeBag"`
}

type usptoAssignor struct {
	AssignorName  string `json:"assignorName"`
	ExecutionDate string `json:"executionDate"`
}

type usptoAssigneeInfo struct {
	AssigneeNameText string `json:"assigneeNameText"`
}
