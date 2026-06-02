package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
)

type getSessionInput struct {
	SessionID string `json:"sessionId" jsonschema:"The session ID to recover.,required"`
	StepID    int    `json:"stepId,omitempty" jsonschema:"Retrieve full details for a specific step number. Omit to get session overview."`
}

func registerGetSession(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "get_research_session",
		Description:  "Recover a sequential_search research session after context loss. Returns the session summary, step index, and most recent steps. Use stepId to retrieve full details of a specific earlier step. Sessions persist for 4 hours from last activity and survive server restarts.",
		Annotations:  readOnlyAnnotations(true, false),
		OutputSchema: getSessionOutputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest, input getSessionInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()

		if input.SessionID == "" {
			return toolError("sessionId is required"), nil, nil
		}

		tenantID := auth.TenantIDFromContext(ctx)
		userID := auth.UserIDFromContext(ctx)

		if input.StepID > 0 {
			step, err := deps.Sessions.GetStep(tenantID, userID, input.SessionID, input.StepID)
			if err != nil {
				deps.Metrics.RecordToolCall("get_research_session", time.Since(start), err, "upstream_error", false)
				auditToolCall(ctx, deps, "get_research_session", time.Since(start), err, "upstream_error")
				return toolError("Session not found or expired. Sessions last 4 hours from last activity."), nil, nil
			}

			output := map[string]any{
				"sessionId":    input.SessionID,
				"responseMode": "step",
				"step":         step,
				"trust":        untrustedContentTrust,
			}
			jsonBytes, _ := json.Marshal(output)
			deps.Metrics.RecordToolCall("get_research_session", time.Since(start), nil, "", false)
			auditToolCall(ctx, deps, "get_research_session", time.Since(start), nil, "")
			return structuredResult(jsonBytes), nil, nil
		}

		idx, ok := deps.Sessions.GetIndex(tenantID, userID, input.SessionID)
		if !ok {
			deps.Metrics.RecordToolCall("get_research_session", time.Since(start), nil, "upstream_error", false)
			auditToolCall(ctx, deps, "get_research_session", time.Since(start), nil, "upstream_error")
			return toolError("Session not found or expired. Sessions last 4 hours from last activity."), nil, nil
		}

		output := map[string]any{
			"sessionId":    idx.ID,
			"responseMode": "summary",
			"researchGoal": idx.ResearchGoal,
			"stepCount":    idx.StepCount,
			"summary":      idx.Summary,
			"stepIndex":    idx.StepIndex,
			"lastSteps":    idx.LastSteps,
			"gaps":         idx.ActiveGaps,
			"sources":      idx.Sources,
			"startedAt":    idx.CreatedAt.Format(time.RFC3339),
			"trust":        untrustedContentTrust,
		}

		jsonBytes, _ := json.Marshal(output)
		deps.Metrics.RecordToolCall("get_research_session", time.Since(start), nil, "", false)
		auditToolCall(ctx, deps, "get_research_session", time.Since(start), nil, "")
		return structuredResult(jsonBytes), nil, nil
	})
}
