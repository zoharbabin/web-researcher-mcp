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

// ClinicalTrialsProvider implements TrialSearcher over the ClinicalTrials.gov v2
// API: the US NIH registry of 400K+ clinical studies (status, phase, sponsor,
// conditions, interventions, results availability). Keyless and free — extends
// the "free coverage of paid verticals" set into evidence-based medicine.
//
// Verified contract (API v2.0.5, 2026):
//   - search:  /api/v2/studies?query.term=&query.cond=&query.intr=&query.spons=
//     &filter.overallStatus=&pageSize=&fields=&format=json
//     → {studies:[{protocolSection{…}, hasResults}], nextPageToken, totalCount}
//   - fields= projects a subset (PascalCase API names, '|'-separated); the server
//     returns the full nested path to each requested leaf.
//   - errors are HTTP 400/404 with a text/plain body (NOT JSON); no-match is a
//     200 with an empty studies array.
type ClinicalTrialsProvider struct {
	baseURL string
	deps    Deps
}

// NewClinicalTrialsProvider creates the provider. No key required.
func NewClinicalTrialsProvider(deps Deps) *ClinicalTrialsProvider {
	return &ClinicalTrialsProvider{
		baseURL: "https://clinicaltrials.gov/api/v2",
		deps:    deps,
	}
}

func (c *ClinicalTrialsProvider) Name() string { return "clinicaltrials" }

func (c *ClinicalTrialsProvider) Metadata() ProviderMeta {
	return ProviderMeta{
		Regions:      []string{"*"},
		Capabilities: []string{"search", "clinical", "trials"},
		RateClass:    "free",
		Description:  "ClinicalTrials.gov (NIH) — 400K+ clinical studies with status, phase, sponsor, and results availability",
	}
}

// SetBaseURL overrides the API base URL (testing).
func (c *ClinicalTrialsProvider) SetBaseURL(base string) { c.baseURL = base }

// clinicalTrialFields is the field projection — only the leaves the TrialResult
// needs, keeping the response small. PascalCase API names, '|'-separated.
const clinicalTrialFields = "NCTId|BriefTitle|OverallStatus|Phase|Condition|InterventionName|LeadSponsorName|StartDate|HasResults"

func (c *ClinicalTrialsProvider) Trials(ctx context.Context, params TrialSearchParams) ([]TrialResult, error) {
	var results []TrialResult
	err := c.deps.Breaker.Execute(func() error {
		var er error
		results, er = c.doSearch(ctx, params)
		return er
	})
	return results, err
}

func (c *ClinicalTrialsProvider) doSearch(ctx context.Context, params TrialSearchParams) ([]TrialResult, error) {
	num := clamp(params.NumResults, 1, 100)

	q := url.Values{}
	q.Set("format", "json")
	q.Set("pageSize", strconv.Itoa(num))
	q.Set("fields", clinicalTrialFields)
	if params.Query != "" {
		q.Set("query.term", params.Query)
	}
	if params.Condition != "" {
		q.Set("query.cond", params.Condition)
	}
	if params.Intervention != "" {
		q.Set("query.intr", params.Intervention)
	}
	if params.Sponsor != "" {
		q.Set("query.spons", params.Sponsor)
	}
	if params.Status != "" {
		// Registry vocabulary is upper-case (RECRUITING, COMPLETED, …).
		q.Set("filter.overallStatus", strings.ToUpper(params.Status))
	}

	body, err := c.get(ctx, "/studies?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, nil // 404 / no body → empty, not an error
	}

	var parsed struct {
		Studies []struct {
			ProtocolSection struct {
				IdentificationModule struct {
					NCTID      string `json:"nctId"`
					BriefTitle string `json:"briefTitle"`
				} `json:"identificationModule"`
				StatusModule struct {
					OverallStatus   string `json:"overallStatus"`
					StartDateStruct struct {
						Date string `json:"date"`
					} `json:"startDateStruct"`
				} `json:"statusModule"`
				DesignModule struct {
					Phases []string `json:"phases"`
				} `json:"designModule"`
				ConditionsModule struct {
					Conditions []string `json:"conditions"`
				} `json:"conditionsModule"`
				ArmsInterventionsModule struct {
					Interventions []struct {
						Name string `json:"name"`
					} `json:"interventions"`
				} `json:"armsInterventionsModule"`
				SponsorCollaboratorsModule struct {
					LeadSponsor struct {
						Name string `json:"name"`
					} `json:"leadSponsor"`
				} `json:"sponsorCollaboratorsModule"`
			} `json:"protocolSection"`
			HasResults bool `json:"hasResults"`
		} `json:"studies"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("clinicaltrials: parse: %w", err)
	}

	out := make([]TrialResult, 0, len(parsed.Studies))
	for _, s := range parsed.Studies {
		ps := s.ProtocolSection
		interventions := make([]string, 0, len(ps.ArmsInterventionsModule.Interventions))
		for _, iv := range ps.ArmsInterventionsModule.Interventions {
			if iv.Name != "" {
				interventions = append(interventions, iv.Name)
			}
		}
		out = append(out, TrialResult{
			NCTID:         ps.IdentificationModule.NCTID,
			Title:         ps.IdentificationModule.BriefTitle,
			Status:        ps.StatusModule.OverallStatus,
			Phases:        ps.DesignModule.Phases,
			Conditions:    ps.ConditionsModule.Conditions,
			Interventions: interventions,
			Sponsor:       ps.SponsorCollaboratorsModule.LeadSponsor.Name,
			StartDate:     ps.StatusModule.StartDateStruct.Date,
			HasResults:    s.HasResults,
			URL:           clinicalTrialURL(ps.IdentificationModule.NCTID),
			Source:        "clinicaltrials",
		})
	}
	return out, nil
}

// clinicalTrialURL builds the human-facing study page from an NCT ID.
func clinicalTrialURL(nctID string) string {
	if nctID == "" {
		return ""
	}
	return "https://clinicaltrials.gov/study/" + nctID
}

func (c *ClinicalTrialsProvider) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.deps.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("clinicaltrials: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("clinicaltrials: rate limited: %w", circuit.ErrRateLimit)
	}
	if resp.StatusCode == 404 {
		return nil, nil // not found → empty, not an error
	}
	if resp.StatusCode >= 400 {
		// Error bodies are text/plain, not JSON — surface a trimmed snippet.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("clinicaltrials: API error %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
}

var _ TrialProvider = (*ClinicalTrialsProvider)(nil)
