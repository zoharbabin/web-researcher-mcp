package session

import "time"

type Session struct {
	ID           string           `json:"id"`
	TenantID     string           `json:"tenantId"`
	ResearchGoal string           `json:"researchGoal,omitempty"`
	CreatedAt    time.Time        `json:"createdAt"`
	LastUsed     time.Time        `json:"lastUsed"`
	Steps        []ResearchStep   `json:"steps"`
	Sources      []ResearchSource `json:"sources"`
	Gaps         []KnowledgeGap   `json:"gaps"`
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
	ID           string           `json:"id"`
	TenantID     string           `json:"tenantId"`
	ResearchGoal string           `json:"researchGoal"`
	CreatedAt    time.Time        `json:"createdAt"`
	LastUsed     time.Time        `json:"lastUsed"`
	StepCount    int              `json:"stepCount"`
	Summary      string           `json:"summary"`
	StepIndex    []StepIndexEntry `json:"stepIndex"`
	LastSteps    []ResearchStep   `json:"lastSteps"`
	ActiveGaps   []KnowledgeGap   `json:"activeGaps"`
	Sources      []ResearchSource `json:"sources"`
	Warning      string           `json:"warning,omitempty"`
}

type StepIndexEntry struct {
	StepNumber int    `json:"stepNumber"`
	BranchID   string `json:"branchId,omitempty"`
	OneLiner   string `json:"oneLiner"`
	Confidence string `json:"confidence,omitempty"`
}
