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
	SearchStep         string `json:"searchStep" jsonschema:"Description of what was done/found in this research step,required"`
	StepNumber         int    `json:"stepNumber" jsonschema:"Current step number (starts at 1),required"`
	NextStepNeeded     bool   `json:"nextStepNeeded" jsonschema:"Whether more research steps are needed,required"`
	TotalStepsEstimate int    `json:"totalStepsEstimate,omitempty" jsonschema:"Estimated total number of steps"`
	SessionID          string `json:"sessionId,omitempty" jsonschema:"Session ID (returned from first call, required for subsequent steps)"`
	IsRevision         bool   `json:"isRevision,omitempty" jsonschema:"Whether this revises a previous step"`
	RevisesStep        int    `json:"revisesStep,omitempty" jsonschema:"Step number being revised"`
	BranchFromStep     int    `json:"branchFromStep,omitempty" jsonschema:"Step to branch from"`
	BranchID           string `json:"branchId,omitempty" jsonschema:"Branch identifier"`
	KnowledgeGap       string `json:"knowledgeGap,omitempty" jsonschema:"Knowledge gap identified in this step"`
}

func registerSequentialSearch(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "sequential_search",
		Description: "Multi-step research tracking with session persistence, branching, and knowledge gap identification. Start with stepNumber=1, set nextStepNeeded=false on the final step.",
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

		return textResult(string(jsonBytes)), nil, nil
	})
}
