package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

func registerSequentialSearch(srv *server.MCPServer, deps Dependencies) {
	tool := mcp.NewTool("sequential_search",
		mcp.WithDescription("Multi-step research tracking with session persistence, branching, and knowledge gap identification. Start with stepNumber=1, set nextStepNeeded=false on the final step."),
		mcp.WithString("searchStep", mcp.Required(), mcp.Description("Description of what was done/found in this research step")),
		mcp.WithNumber("stepNumber", mcp.Required(), mcp.Description("Current step number (starts at 1)")),
		mcp.WithNumber("totalStepsEstimate", mcp.Description("Estimated total number of steps")),
		mcp.WithBoolean("nextStepNeeded", mcp.Required(), mcp.Description("Whether more research steps are needed")),
		mcp.WithString("sessionId", mcp.Description("Session ID (returned from first call, required for subsequent steps)")),
		mcp.WithBoolean("isRevision", mcp.Description("Whether this revises a previous step")),
		mcp.WithNumber("revisesStep", mcp.Description("Step number being revised")),
		mcp.WithNumber("branchFromStep", mcp.Description("Step to branch from")),
		mcp.WithString("branchId", mcp.Description("Branch identifier")),
		mcp.WithString("knowledgeGap", mcp.Description("Knowledge gap identified in this step")),
	)

	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		searchStep, _ := req.GetArguments()["searchStep"].(string)
		if searchStep == "" {
			return toolError("searchStep is required"), nil
		}

		stepNumber := intParam(req.GetArguments(), "stepNumber", 1)
		totalSteps := intParam(req.GetArguments(), "totalStepsEstimate", 0)
		nextStepNeeded := boolParam(req.GetArguments(), "nextStepNeeded", true)
		sessionID, _ := req.GetArguments()["sessionId"].(string)
		isRevision := boolParam(req.GetArguments(), "isRevision", false)
		revisesStep := intParam(req.GetArguments(), "revisesStep", 0)
		branchID, _ := req.GetArguments()["branchId"].(string)
		knowledgeGap, _ := req.GetArguments()["knowledgeGap"].(string)

		tenantID := "default"

		var sess *session.Session
		var err error

		if sessionID == "" || stepNumber == 1 {
			// Create new session
			sess, err = deps.Sessions.Create(tenantID)
			if err != nil {
				return toolError(fmt.Sprintf("failed to create session: %v", err)), nil
			}
		} else {
			// Resume existing session
			var ok bool
			sess, ok = deps.Sessions.Get(tenantID, sessionID)
			if !ok {
				return toolError("session expired or not found"), nil
			}
		}

		// Add step
		step := session.ResearchStep{
			StepNumber:  stepNumber,
			Description: searchStep,
			IsRevision:  isRevision,
			RevisesStep: revisesStep,
			BranchID:    branchID,
			Timestamp:   time.Now().Format(time.RFC3339),
		}
		sess.Steps = append(sess.Steps, step)

		// Track knowledge gap
		if knowledgeGap != "" {
			sess.Gaps = append(sess.Gaps, session.KnowledgeGap{
				Description: knowledgeGap,
				FoundInStep: stepNumber,
			})
		}

		// Save session
		deps.Sessions.Update(tenantID, sess)

		// Build output
		output := map[string]any{
			"sessionId":          sess.ID,
			"currentStep":        stepNumber,
			"totalStepsEstimate": totalSteps,
			"isComplete":         !nextStepNeeded,
			"steps":              sess.Steps,
			"sources":            sess.Sources,
			"gaps":               sess.Gaps,
			"startedAt":          sess.CreatedAt.Format(time.RFC3339),
		}

		if !nextStepNeeded {
			output["completedAt"] = time.Now().Format(time.RFC3339)
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Metrics.RecordToolCall("sequential_search", time.Since(start), nil, "", false)
		auditToolCall(deps, "sequential_search", time.Since(start), nil, "")

		return mcp.NewToolResultText(string(jsonBytes)), nil
	})
}
