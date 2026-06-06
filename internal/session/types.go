package session

import "time"

type Session struct {
	ID              string           `json:"id"`
	TenantID        string           `json:"tenantId"`
	CreatedByUserID string           `json:"createdByUserId,omitempty"`
	ResearchGoal    string           `json:"researchGoal,omitempty"`
	CreatedAt       time.Time        `json:"createdAt"`
	LastUsed        time.Time        `json:"lastUsed"`
	Steps           []ResearchStep   `json:"steps"`
	Sources         []ResearchSource `json:"sources"`
	Gaps            []KnowledgeGap   `json:"gaps"`
	// Outcomes is the bounded per-session record of tool outcomes (provider
	// attempt/success + error kind), feeding the cross-call error-pattern and
	// provider-stats aggregation surfaced in get_research_session (#99). Capped
	// at MaxOutcomes (oldest dropped) to honor the no-unbounded-retention posture.
	Outcomes []OutcomeEvent `json:"outcomes,omitempty"`
}

// MaxOutcomes bounds the per-session outcome log. Aggregates (counts, patterns)
// are derived from this window; older events age out FIFO.
const MaxOutcomes = 200

// OutcomeEvent records one tool call's result against a session: which provider
// answered, whether it succeeded, and (on failure) the typed error kind and the
// URL involved. It is additive telemetry — errors are still returned to the
// caller in full; this only enables the cross-call pattern view.
type OutcomeEvent struct {
	Provider   string `json:"provider,omitempty"`
	Success    bool   `json:"success"`
	ErrorKind  string `json:"errorKind,omitempty"`
	URL        string `json:"url,omitempty"`
	StepNumber int    `json:"stepNumber,omitempty"`
	Timestamp  string `json:"timestamp,omitempty"`
}

// ErrorPatternMinCount is the threshold below which a repeated error kind is NOT
// reported as a session-level pattern — small samples produce false positives
// (roadmap rule, #99).
const ErrorPatternMinCount = 3

// ErrorPattern is an aggregated, cross-call view of one recurring error kind in
// a session. Only surfaced when Count >= ErrorPatternMinCount.
type ErrorPattern struct {
	Kind         string   `json:"kind"`
	Count        int      `json:"count"`
	AffectedURLs []string `json:"affectedUrls,omitempty"`
	Suggestion   string   `json:"suggestion,omitempty"`
	LastSeen     string   `json:"lastSeen,omitempty"`
}

// ProviderStat counts attempts and successes for one provider within a session.
type ProviderStat struct {
	Attempts  int `json:"attempts"`
	Successes int `json:"successes"`
}

type ResearchStep struct {
	StepNumber         int      `json:"stepNumber"`
	Description        string   `json:"description"`
	Reasoning          string   `json:"reasoning,omitempty"`
	Confidence         string   `json:"confidence,omitempty"`
	RejectedApproaches []string `json:"rejectedApproaches,omitempty"`
	IsRevision         bool     `json:"isRevision,omitempty"`
	RevisesStep        int      `json:"revisesStep,omitempty"`
	BranchID           string   `json:"branchId,omitempty"`
	Timestamp          string   `json:"timestamp"`
}

type ResearchSource struct {
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	Relevance   string `json:"relevance,omitempty"`
	FoundInStep int    `json:"foundInStep"`
}

type KnowledgeGap struct {
	Description string `json:"description"`
	FoundInStep int    `json:"foundInStep"`
}

type SessionIndex struct {
	ID              string           `json:"id"`
	TenantID        string           `json:"tenantId"`
	CreatedByUserID string           `json:"createdByUserId,omitempty"`
	ResearchGoal    string           `json:"researchGoal"`
	CreatedAt       time.Time        `json:"createdAt"`
	LastUsed        time.Time        `json:"lastUsed"`
	StepCount       int              `json:"stepCount"`
	Summary         string           `json:"summary"`
	StepIndex       []StepIndexEntry `json:"stepIndex"`
	LastSteps       []ResearchStep   `json:"lastSteps"`
	ActiveGaps      []KnowledgeGap   `json:"activeGaps"`
	Sources         []ResearchSource `json:"sources"`
	Warning         string           `json:"warning,omitempty"`
	// ErrorPatterns surfaces recurring error kinds (count >= ErrorPatternMinCount)
	// across the session; ProviderStats reports per-provider attempt/success
	// counts. Both are derived from Session.Outcomes at index-build time (#99).
	ErrorPatterns []ErrorPattern          `json:"errorPatterns,omitempty"`
	ProviderStats map[string]ProviderStat `json:"providerStats,omitempty"`
}

type StepIndexEntry struct {
	StepNumber int    `json:"stepNumber"`
	BranchID   string `json:"branchId,omitempty"`
	OneLiner   string `json:"oneLiner"`
	Confidence string `json:"confidence,omitempty"`
}
