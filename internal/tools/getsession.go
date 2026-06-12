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
		Description:  "Recover a sequential_search research session after context loss. Returns the session summary, a one-liner step index covering every step, and the last 3 steps in full detail (the `lastSteps` sliding window). For full details of any earlier step, pass its stepId. A source's `foundInStep` is the 1-indexed step that surfaced it, omitted when the source was not tied to a numbered step (e.g. added via a web_search carrying only a sessionId) — there is no step 0. Sessions persist for 4 hours from last activity and survive server restarts.",
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
				recordToolCall(deps, "get_research_session", time.Since(start), err, "upstream_error", false)
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
			recordToolCall(deps, "get_research_session", time.Since(start), nil, "", false)
			auditToolCall(ctx, deps, "get_research_session", time.Since(start), nil, "")
			return structuredResult(jsonBytes), nil, nil
		}

		idx, ok := deps.Sessions.GetIndex(tenantID, userID, input.SessionID)
		if !ok {
			recordToolCall(deps, "get_research_session", time.Since(start), nil, "upstream_error", false)
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

		// Cross-call error patterns + provider stats (#99): additive metadata,
		// present only when there's something to report (patterns gate on count
		// >= 3 in the session layer).
		if len(idx.ErrorPatterns) > 0 {
			output["errorPatterns"] = idx.ErrorPatterns
		}
		if len(idx.ProviderStats) > 0 {
			output["providerStats"] = idx.ProviderStats
		}

		jsonBytes, _ := json.Marshal(output)
		recordToolCall(deps, "get_research_session", time.Since(start), nil, "", false)
		auditToolCall(ctx, deps, "get_research_session", time.Since(start), nil, "")
		return structuredResult(jsonBytes), nil, nil
	})
}
