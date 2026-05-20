package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

type sequentialSearchInput struct {
	SearchStep         string `json:"searchStep" jsonschema:"Summary of what was researched or discovered in this step. Be descriptive to build a useful research trail.,required"`
	StepNumber         int    `json:"stepNumber" jsonschema:"Current step number (start at 1 for a new session). Must increment sequentially.,required"`
	NextStepNeeded     bool   `json:"nextStepNeeded" jsonschema:"Set true if more research steps will follow; false to mark the session complete.,required"`
	TotalStepsEstimate int    `json:"totalStepsEstimate,omitempty" jsonschema:"Your estimate of total steps needed. Update as scope becomes clearer."`
	SessionID          string `json:"sessionId,omitempty" jsonschema:"Session ID returned from the first call. Required for steps 2+. Omit to start a new session."`
	IsRevision         bool   `json:"isRevision,omitempty" jsonschema:"Set true if this step revises a previous step's findings."`
	RevisesStep        int    `json:"revisesStep,omitempty" jsonschema:"The step number being revised (required if isRevision is true)."`
	BranchFromStep     int    `json:"branchFromStep,omitempty" jsonschema:"Step number to branch from, for exploring alternative research directions."`
	BranchID           string `json:"branchId,omitempty" jsonschema:"Identifier for this research branch (e.g. 'technical-approach' vs 'business-angle')."`
	KnowledgeGap       string `json:"knowledgeGap,omitempty" jsonschema:"A specific gap or unanswered question identified during this step that needs further investigation."`
}

func registerSequentialSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "sequential_search",
		Description:  "Track multi-step research progress with persistent sessions, branching, and knowledge gap tracking. This is a state tracker, not a search tool — pair with web_search or search_and_scrape for actual searches. Call with stepNumber=1 (omit sessionId) to start a new session. Returns JSON with fields: sessionId, currentStep, totalStepsEstimate, isComplete, steps (array of {stepNumber, description, isRevision, revisesStep, branchId, timestamp}), sources, gaps (array of {description, foundInStep}), startedAt, completedAt (only when isComplete=true). Pass sessionId for steps 2+; each call returns the full accumulated session state. Set nextStepNeeded=false on final step to mark complete. Sessions expire after 30 min of inactivity (returns 'session expired or not found' error); max 50 concurrent sessions per tenant. Use branchFromStep + branchId to explore alternative directions without losing the main thread. Not cached.",
		Annotations:  readOnlyAnnotations(false, false),
		OutputSchema: sequentialSearchOutputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input sequentialSearchInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		if input.SearchStep == "" {
			return toolError("searchStep is required"), nil, nil
		}

		tenantID := "default"

		var sess *session.Session
		var err error

		if input.SessionID == "" || input.StepNumber == 1 {
			sess, err = deps.Sessions.Create(tenantID)
			if err != nil {
				return toolError(fmt.Sprintf("failed to create session: %v", err)), nil, nil
			}
		} else {
			var ok bool
			sess, ok = deps.Sessions.Get(tenantID, input.SessionID)
			if !ok {
				return toolError("session expired or not found"), nil, nil
			}
		}

		step := session.ResearchStep{
			StepNumber:  input.StepNumber,
			Description: input.SearchStep,
			IsRevision:  input.IsRevision,
			RevisesStep: input.RevisesStep,
			BranchID:    input.BranchID,
			Timestamp:   time.Now().Format(time.RFC3339),
		}
		sess.Steps = append(sess.Steps, step)

		if input.KnowledgeGap != "" {
			sess.Gaps = append(sess.Gaps, session.KnowledgeGap{
				Description: input.KnowledgeGap,
				FoundInStep: input.StepNumber,
			})
		}

		deps.Sessions.Update(tenantID, sess)

		output := map[string]any{
			"sessionId":          sess.ID,
			"currentStep":        input.StepNumber,
			"totalStepsEstimate": input.TotalStepsEstimate,
			"isComplete":         !input.NextStepNeeded,
			"steps":              sess.Steps,
			"sources":            sess.Sources,
			"gaps":               sess.Gaps,
			"startedAt":          sess.CreatedAt.Format(time.RFC3339),
		}

		if !input.NextStepNeeded {
			output["completedAt"] = time.Now().Format(time.RFC3339)
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Metrics.RecordToolCall("sequential_search", time.Since(start), nil, "", false)
		auditToolCall(deps, "sequential_search", time.Since(start), nil, "")

		return structuredResult(jsonBytes), nil, nil
	})
}
