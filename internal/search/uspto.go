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
		if params.CPCCode != "" && !matchesCPCCode(meta.CPCClassificationBag, params.CPCCode) {
			continue
		}
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

	// USPTO's PEDS API `q` full-text search has no native filing-date range
	// parameter (unlike EPO/Lens/searchapi), so year_from/year_to is enforced
	// client-side on the Filed date here instead (#528).
	results = filterByFiledYear(results, params.YearFrom, params.YearTo)

	// Defensive cap: the USPTO API can return more rows than requested (the
	// `rows` param is not always honored), so enforce the caller's limit here
	// to match every other provider's contract.
	if n := clamp(params.NumResults, 1, 10); len(results) > n {
		results = results[:n]
	}
	return results, nil
}

// filterByFiledYear drops results whose Filed date falls outside
// [yearFrom, yearTo] (either bound may be zero/unset). A result whose Filed
// date is missing or unparseable is kept rather than dropped — there is no
// evidence it's out of range, so excluding it would be a false negative, not
// a correctness fix.
func filterByFiledYear(results []PatentResult, yearFrom, yearTo int) []PatentResult {
	if yearFrom == 0 && yearTo == 0 {
		return results
	}
	out := results[:0]
	for _, r := range results {
		if year, ok := parseFiledYear(r.Filed); ok {
			if yearFrom > 0 && year < yearFrom {
				continue
			}
			if yearTo > 0 && year > yearTo {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// parseFiledYear extracts the leading 4-digit year from a Filed date string
// (USPTO's filingDate is "YYYY-MM-DD").
func parseFiledYear(filed string) (int, bool) {
	if len(filed) < 4 {
		return 0, false
	}
	year, err := strconv.Atoi(filed[:4])
	if err != nil {
		return 0, false
	}
	return year, true
}

// matchesCPCCode reports whether any of a result's CPC classification symbols
// starts with the requested code (e.g. requesting "G06F" matches the
// symbol "G06F17/30"). USPTO's PEDS API `q` full-text search has no CPC
// query parameter (unlike EPO's cpc= and Lens's class_cpc.symbol), so
// cpc_code is enforced client-side here, the same pattern as #528's
// year-range filter (#530).
func matchesCPCCode(symbols []string, code string) bool {
	code = strings.ToUpper(code)
	for _, s := range symbols {
		if strings.HasPrefix(strings.ToUpper(s), code) {
			return true
		}
	}
	return false
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
