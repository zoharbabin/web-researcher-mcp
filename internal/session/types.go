package session

import "time"

type Session struct {
	ID              string `json:"id"`
	TenantID        string `json:"tenantId"`
	CreatedByUserID string `json:"createdByUserId,omitempty"`
	ResearchGoal    string `json:"researchGoal,omitempty"`
	// TotalStepsEstimate is the caller's latest estimate of total steps needed,
	// set via sequential_search's totalStepsEstimate field and persisted across
	// steps so it survives even when a later step omits it (#525).
	TotalStepsEstimate int       `json:"totalStepsEstimate,omitempty"`
	CreatedAt          time.Time `json:"createdAt"`
	LastUsed           time.Time `json:"lastUsed"`
	// Completed is set by MarkComplete when the caller submits a step with
	// nextStepNeeded:false. It exists solely so ActiveCount can exclude finished
	// sessions from the "live" gauge (#622) — a completed session is not deleted
	// and stays readable via GetFull/GetIndex until its TTL expires.
	Completed bool             `json:"completed,omitempty"`
	Steps     []ResearchStep   `json:"steps"`
	Sources   []ResearchSource `json:"sources"`
	Gaps      []KnowledgeGap   `json:"gaps"`
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
	// SupersededBy is a derived, read-time-only discoverability signal (#512):
	// the step number of the LATEST later step that revises this one (isRevision
	// + revisesStep == this step's number), or omitted when no later step revises
	// it. It is never persisted — this struct's on-disk/stored form never carries
	// it; callers that read a step (get_research_session, research_export) compute
	// it fresh via SupersededMap and attach it to the copy they return. Revision
	// is additive/audit-trail by design: nothing about the superseded step is
	// mutated or removed, this only makes the forward link discoverable backward.
	SupersededBy int `json:"supersededBy,omitempty"`
}

type ResearchSource struct {
	URL       string `json:"url"`
	Title     string `json:"title,omitempty"`
	Relevance string `json:"relevance,omitempty"`
	// Author/Date/DOI carry bibliographic metadata from the originating search
	// result (e.g. search.AcademicResult) when the tool that auto-tracks this
	// source has it available, so format_bibliography on a session produces
	// citation-complete entries without requiring the caller to pass sources
	// explicitly (#532). Omitted when the source type has no such metadata.
	Author string `json:"author,omitempty"`
	Date   string `json:"date,omitempty"`
	DOI    string `json:"doi,omitempty"`
	// FoundInStep is the 1-indexed sequential_search step that surfaced this
	// source, or omitted entirely when the source was not tied to a numbered step
	// (e.g. added via a web_search carrying only a sessionId). Steps are 1-indexed,
	// so a literal 0 would read as a real-but-nonexistent step; omitempty drops it
	// instead, giving "no step" an unambiguous absent representation (#235).
	FoundInStep int `json:"foundInStep,omitempty"`
	// Link-liveness provenance (#157), populated only when verification is
	// requested (opt-in verify_links on research_export / search_and_scrape).
	// Omitted entirely when unverified, so an unverified source is unchanged.
	HTTPStatus  int    `json:"httpStatus,omitempty"`  // last observed status (0 = network failure)
	Verified    *bool  `json:"verified,omitempty"`    // true = resolved 2xx/3xx; pointer so "unverified" ≠ "verified:false"
	ArchivedURL string `json:"archivedUrl,omitempty"` // Wayback snapshot when the live URL is dead
	VerifiedAt  string `json:"verifiedAt,omitempty"`  // RFC3339 timestamp of the check
}

type KnowledgeGap struct {
	Description string `json:"description"`
	// FoundInStep is the 1-indexed step that recorded the gap; omitted when 0
	// (unattributed) for the same reason as ResearchSource.FoundInStep (#235).
	FoundInStep int `json:"foundInStep,omitempty"`
}

type SessionIndex struct {
	ID              string `json:"id"`
	TenantID        string `json:"tenantId"`
	CreatedByUserID string `json:"createdByUserId,omitempty"`
	ResearchGoal    string `json:"researchGoal"`
	// TotalStepsEstimate mirrors Session.TotalStepsEstimate (#525) — the latest
	// caller-supplied estimate, persisted so it survives steps that omit it.
	TotalStepsEstimate int       `json:"totalStepsEstimate,omitempty"`
	CreatedAt          time.Time `json:"createdAt"`
	LastUsed           time.Time `json:"lastUsed"`
	// Completed mirrors Session.Completed (#622) — surfaced on the index so
	// get_research_session and ActiveCount agree on completion state without
	// loading the full session.
	Completed  bool             `json:"completed,omitempty"`
	StepCount  int              `json:"stepCount"`
	Summary    string           `json:"summary"`
	StepIndex  []StepIndexEntry `json:"stepIndex"`
	LastSteps  []ResearchStep   `json:"lastSteps"`
	ActiveGaps []KnowledgeGap   `json:"activeGaps"`
	Sources    []ResearchSource `json:"sources"`
	Warning    string           `json:"warning,omitempty"`
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
	// SupersededBy mirrors ResearchStep.SupersededBy (#512) — derived at
	// index-build time, present on every entry so the one-liner index (which
	// covers EVERY step, unlike the LastSteps sliding window) always surfaces
	// the signal regardless of how far back the revised step is.
	SupersededBy int `json:"supersededBy,omitempty"`
}
