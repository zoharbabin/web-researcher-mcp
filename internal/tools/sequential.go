package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

type sequentialSearchInput struct {
	SearchStep         string   `json:"searchStep" jsonschema:"Summary of what was researched or discovered in this step. Be descriptive to build a useful research trail.,required"`
	StepNumber         int      `json:"stepNumber" jsonschema:"Current step number (start at 1 for a new session). Must increment sequentially.,required"`
	NextStepNeeded     bool     `json:"nextStepNeeded" jsonschema:"Set true if more research steps will follow; false to mark the session complete.,required"`
	TotalStepsEstimate int      `json:"totalStepsEstimate,omitempty" jsonschema:"Your estimate of total steps needed. Update as scope becomes clearer."`
	SessionID          string   `json:"sessionId,omitempty" jsonschema:"Session ID returned from the first call. Required for steps 2+. Omit to start a new session."`
	IsRevision         bool     `json:"isRevision,omitempty" jsonschema:"Set true if this step revises a previous step's findings."`
	RevisesStep        int      `json:"revisesStep,omitempty" jsonschema:"The step number being revised (required if isRevision is true)."`
	BranchFromStep     int      `json:"branchFromStep,omitempty" jsonschema:"Step number to branch from, for exploring alternative research directions."`
	BranchID           string   `json:"branchId,omitempty" jsonschema:"Identifier for this research branch (e.g. 'technical-approach' vs 'business-angle')."`
	KnowledgeGap       string   `json:"knowledgeGap,omitempty" jsonschema:"A specific gap or unanswered question identified during this step that needs further investigation."`
	ResearchGoal       string   `json:"researchGoal,omitempty" jsonschema:"The question or goal driving this research. Set on step 1; ignored on later steps."`
	Reasoning          string   `json:"reasoning,omitempty" jsonschema:"Why you chose this search direction over alternatives."`
	Confidence         string   `json:"confidence,omitempty" jsonschema:"Confidence in this step's findings: high, medium, or low."`
	RejectedApproaches []string `json:"rejectedApproaches,omitempty" jsonschema:"Approaches considered but rejected, with brief reasons."`
	SessionSummary     string   `json:"sessionSummary,omitempty" jsonschema:"Running summary of research so far. Update periodically for better session recovery."`
	ResponseMode       string   `json:"responseMode,omitempty" jsonschema:"Force response format: full or summary. Default: auto (full for 8 or fewer steps, summary for more)."`
}

func registerSequentialSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "sequential_search",
		Description:  "Keep track of a multi-step research project. Use this alongside web_search or search_and_scrape to record what you've found at each step, note unanswered questions, and explore alternative angles (branching). Start a new session with stepNumber=1, then pass the returned sessionId for each follow-up step. Mark the session complete by setting nextStepNeeded=false. Sessions stay active for 4 hours between steps and persist across restarts. Use get_research_session to recover a session after context loss.",
		Annotations:  readOnlyAnnotations(false, false),
		OutputSchema: sequentialSearchOutputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input sequentialSearchInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		if input.SearchStep == "" {
			return toolError("searchStep is required"), nil, nil
		}

		tenantID := auth.TenantIDFromContext(ctx)

		var idx *session.SessionIndex
		var err error

		if input.SessionID == "" || input.StepNumber == 1 {
			idx, err = deps.Sessions.Create(tenantID)
			if err != nil {
				return toolError(fmt.Sprintf("failed to create session: %v", err)), nil, nil
			}

			goal := input.ResearchGoal
			if goal == "" && len(input.SearchStep) > 0 {
				goal = input.SearchStep
				if len(goal) > 200 {
					goal = goal[:200]
				}
			}
			if goal != "" {
				_ = deps.Sessions.SetResearchGoal(tenantID, idx.ID, goal)
			}
		}

		sessionID := input.SessionID
		if idx != nil {
			sessionID = idx.ID
		}

		step := session.ResearchStep{
			StepNumber:         input.StepNumber,
			Description:        input.SearchStep,
			Reasoning:          input.Reasoning,
			Confidence:         input.Confidence,
			RejectedApproaches: input.RejectedApproaches,
			IsRevision:         input.IsRevision,
			RevisesStep:        input.RevisesStep,
			BranchID:           input.BranchID,
			Timestamp:          time.Now().Format(time.RFC3339),
		}

		var gap *session.KnowledgeGap
		if input.KnowledgeGap != "" {
			gap = &session.KnowledgeGap{
				Description: input.KnowledgeGap,
				FoundInStep: input.StepNumber,
			}
		}

		idx, err = deps.Sessions.AppendStep(tenantID, sessionID, step, gap, input.SessionSummary)
		if err != nil {
			return toolError(fmt.Sprintf("This research session has expired or doesn't exist. Sessions expire after 4 hours of inactivity. Start a new session by setting stepNumber=1 without a sessionId. (error: %v)", err)), nil, nil
		}

		output := buildSequentialResponse(idx, input)

		jsonBytes, _ := json.Marshal(output)
		deps.Metrics.RecordToolCall("sequential_search", time.Since(start), nil, "", false)
		auditToolCall(ctx, deps, "sequential_search", time.Since(start), nil, "")

		return structuredResult(jsonBytes), nil, nil
	})
}

func buildSequentialResponse(idx *session.SessionIndex, input sequentialSearchInput) map[string]any {
	mode := input.ResponseMode
	if mode == "" {
		if idx.StepCount <= 8 {
			mode = "full"
		} else {
			mode = "summary"
		}
	}

	output := map[string]any{
		"sessionId":          idx.ID,
		"responseMode":       mode,
		"researchGoal":       idx.ResearchGoal,
		"currentStep":        input.StepNumber,
		"totalStepsEstimate": input.TotalStepsEstimate,
		"isComplete":         !input.NextStepNeeded,
		"startedAt":          idx.CreatedAt.Format(time.RFC3339),
	}

	if idx.Warning != "" {
		output["warning"] = idx.Warning
		output["isComplete"] = true
	}

	if !input.NextStepNeeded {
		output["completedAt"] = time.Now().Format(time.RFC3339)
	}

	switch mode {
	case "full":
		output["steps"] = idx.StepIndex
		output["lastSteps"] = idx.LastSteps
		output["gaps"] = idx.ActiveGaps
	case "summary":
		output["summary"] = idx.Summary
		output["stepIndex"] = idx.StepIndex
		output["lastSteps"] = idx.LastSteps
		output["gaps"] = idx.ActiveGaps
	}

	return output
}
