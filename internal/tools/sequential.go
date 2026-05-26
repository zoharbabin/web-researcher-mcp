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
		Description:  "Keep track of a multi-step research project. Use this alongside web_search or search_and_scrape to record what you've found at each step, note unanswered questions, and explore alternative angles (branching). Start a new session with stepNumber=1, then pass the returned sessionId for each follow-up step. Mark the session complete by setting nextStepNeeded=false. Sessions stay active for 30 minutes between steps. Useful for complex investigations that require several rounds of searching.",
		Annotations:  readOnlyAnnotations(false, false),
		OutputSchema: sequentialSearchOutputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input sequentialSearchInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		if input.SearchStep == "" {
			return toolError("searchStep is required"), nil, nil
		}

		tenantID := auth.TenantIDFromContext(ctx)

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
				return toolError("This research session has expired or doesn't exist. Sessions expire after 30 minutes of inactivity. Start a new session by setting stepNumber=1 without a sessionId."), nil, nil
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
		auditToolCall(ctx, deps, "sequential_search", time.Since(start), nil, "")

		return structuredResult(jsonBytes), nil, nil
	})
}
