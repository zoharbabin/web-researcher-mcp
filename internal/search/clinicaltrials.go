package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
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
//     &filter.overallStatus=&aggFilters=phase:{0-4}&pageSize=&fields=&format=json
//     → {studies:[{protocolSection{…}, hasResults}], nextPageToken, totalCount}
//   - aggFilters=phase:N is the phase filter (0=early phase 1, 1-4=phase 1-4);
//     there is no filter.phase param.
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

	// #437: an explicit Phase always wins; only fall back to extracting a
	// phase-intent phrase from free text when the caller didn't set one.
	phaseCode := normalizePhase(params.Phase)
	queryTerm := params.Query
	if phaseCode == "" && queryTerm != "" {
		if code, remaining := inferPhaseFromQuery(queryTerm); code != "" {
			phaseCode = code
			queryTerm = remaining
		}
	}

	q := url.Values{}
	q.Set("format", "json")
	q.Set("pageSize", strconv.Itoa(num))
	q.Set("fields", clinicalTrialFields)
	if queryTerm != "" {
		q.Set("query.term", queryTerm)
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
	if phaseCode != "" {
		// Verified against the live API (2026-08-18): aggFilters=phase:{0-4}
		// is the v2 phase filter; filter.phase does not exist.
		q.Set("aggFilters", "phase:"+phaseCode)
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

// clinicalPhaseCodes maps a normalized (upper-case, spaces/underscores
// stripped) phase string to ClinicalTrials.gov v2's aggFilters phase code —
// verified against the live API (2026-08-18): aggFilters=phase:{0-4}, where 0
// is "early phase 1". There is no filter.phase param, despite that being the
// name suggested in issue #437 as one possibility.
var clinicalPhaseCodes = map[string]string{
	"EARLYPHASE1": "0",
	"PHASE1":      "1",
	"PHASE2":      "2",
	"PHASE3":      "3",
	"PHASE4":      "4",
	"0":           "0",
	"1":           "1",
	"2":           "2",
	"3":           "3",
	"4":           "4",
}

// normalizePhase resolves free-form phase text (e.g. "PHASE3", "Phase 3",
// "phase_3", "early phase 1", "3") to its aggFilters code, or "" if
// unrecognized (including an empty string).
func normalizePhase(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, " ", "")
	return clinicalPhaseCodes[s]
}

// clinicalPhaseTokenPattern matches a phase-intent phrase embedded in free
// text: "phase 3", "phase3", "early phase 1", "phase 0". The optional
// "early" prefix is captured as part of the SAME match — rather than as a
// separate top-level alternative — so a bare "phase\s*[0-9]" branch can
// never match starting past an "early " prefix and leave it dangling in the
// remaining query text. Not every matched phrase is a real
// ClinicalTrials.gov phase (e.g. "phase 0", "early phase 2" have no
// clinicalPhaseCodes entry); inferPhaseFromQuery below rejects those via
// normalizePhase rather than stripping a phrase that maps to no actual
// filter. Roman numerals ("phase III") are not matched here — rare in
// LLM-generated queries; a caller needing that precision sets Phase
// explicitly.
var clinicalPhaseTokenPattern = regexp.MustCompile(`(?i)\b(?:early\s*)?phase\s*[0-9]+\b`)

// inferPhaseFromQuery extracts a phase-intent phrase from free text (#437
// fix 2, mirroring the form_type inference added for filing_search in #434).
// It returns the normalized aggFilters code plus the query with that phrase
// removed, so the literal words "phase 3" don't dilute query.term's full-text
// relevance — the same rationale as EDGAR's queryStopWords stripping. A
// matched phrase normalizePhase doesn't recognize (e.g. "phase 0", "early
// phase 2" — not real ClinicalTrials.gov phases) is left in place: no code is
// returned and the query comes back unmodified, rather than silently
// dropping text that would apply no actual filter.
func inferPhaseFromQuery(query string) (code, remaining string) {
	loc := clinicalPhaseTokenPattern.FindStringIndex(query)
	if loc == nil {
		return "", query
	}
	code = normalizePhase(query[loc[0]:loc[1]])
	if code == "" {
		return "", query
	}
	remaining = strings.Join(strings.Fields(query[:loc[0]]+" "+query[loc[1]:]), " ")
	return code, remaining
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
