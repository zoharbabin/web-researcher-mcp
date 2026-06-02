package tools

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/consent"
	"github.com/zoharbabin/web-researcher-mcp/internal/workspace"
)

type workspaceContributeInput struct {
	WorkspaceID string   `json:"workspace_id" jsonschema:"The shared workspace to contribute to. You must be a member (membership is managed by your host app's admin).,required"`
	Note        string   `json:"note" jsonschema:"The finding/conclusion to share into the workspace.,required"`
	URL         string   `json:"url,omitempty" jsonschema:"Optional source URL."`
	Tags        []string `json:"tags,omitempty" jsonschema:"Optional tags."`
}

type workspaceReadInput struct {
	WorkspaceID string `json:"workspace_id" jsonschema:"The shared workspace to read. You must be a member.,required"`
}

// caller derives the workspace Member from VALIDATED context identity only —
// never from a tool parameter or the workspace id (per MCP auth guidance:
// authorization binds to the token, not a session/resource id).
func callerMember(ctx context.Context) workspace.Member {
	return workspace.Member{TenantID: auth.TenantIDFromContext(ctx), UserID: auth.UserIDFromContext(ctx)}
}

// registerWorkspaceContribute registers the WRITE tool that copies a finding
// into a shared workspace with provenance (#96). Consent-gated on the
// "workspace" purpose; membership is enforced by the store (non-member → error).
func registerWorkspaceContribute(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "workspace_contribute",
		Description:  "Share a research finding into a shared team workspace (a COPY is stored with your attribution — never a live link to your private data). Opt-in and consent-gated: requires the 'workspace' purpose consent and workspace membership (managed by your host app). Read shared findings back with workspace_read. Use memory_save for your own private cross-session memory instead.",
		Annotations:  writeAnnotations(false),
		OutputSchema: workspaceContributeOutputSchema,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input workspaceContributeInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		if input.WorkspaceID == "" || input.Note == "" {
			return toolError("workspace_id and note are required"), nil, nil
		}
		caller := callerMember(ctx)
		if caller.UserID == "" || caller.UserID == "anonymous" {
			auditToolDenial(ctx, deps, "workspace_contribute", time.Since(start), "unauthenticated")
			return structuredResult(mustJSON(map[string]any{"status": "unavailable", "reason": "shared workspaces require an authenticated user"})), nil, nil
		}
		if deps.Consent == nil || !deps.Consent.HasConsent(ctx, consent.PurposeWorkspace) {
			auditToolDenial(ctx, deps, "workspace_contribute", time.Since(start), "no_consent")
			return structuredResult(mustJSON(map[string]any{"status": "no_consent", "reason": "no recorded consent for the 'workspace' purpose"})), nil, nil
		}
		saved, err := deps.Workspaces.Contribute(ctx, input.WorkspaceID, caller, workspace.Contribution{
			Note: input.Note, URL: input.URL, Tags: input.Tags,
		})
		if errors.Is(err, workspace.ErrNotMember) {
			auditToolDenial(ctx, deps, "workspace_contribute", time.Since(start), "not_member")
			return structuredResult(mustJSON(map[string]any{"status": "not_member", "reason": "you are not a member of this workspace"})), nil, nil
		}
		if err != nil {
			deps.Metrics.RecordToolCall("workspace_contribute", time.Since(start), err, "upstream_error", false)
			return upstreamErrorResponse("workspace_contribute", err), nil, nil
		}
		deps.Metrics.RecordToolCall("workspace_contribute", time.Since(start), nil, "", false)
		auditToolCallQuery(ctx, deps, "workspace_contribute", time.Since(start), nil, "", "", map[string]any{"event": "workspace.contribute", "workspace_id": input.WorkspaceID})
		return structuredResult(mustJSON(map[string]any{"status": "ok", "id": saved.ID})), nil, nil
	})
}

// registerWorkspaceRead registers the read tool that returns a workspace's
// shared contributions — only for members (non-member → zero bytes).
func registerWorkspaceRead(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "workspace_read",
		Description:  "Read the shared findings in a team workspace you belong to (contributed by members via workspace_contribute, each with attribution). Opt-in and consent-gated: requires 'workspace' consent and membership. Non-members receive nothing. Use web_search or search_and_scrape to gather new findings to contribute.",
		Annotations:  readOnlyAnnotations(true, false),
		OutputSchema: workspaceReadOutputSchema,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input workspaceReadInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		if input.WorkspaceID == "" {
			return toolError("workspace_id is required"), nil, nil
		}
		caller := callerMember(ctx)
		if caller.UserID == "" || caller.UserID == "anonymous" {
			auditToolDenial(ctx, deps, "workspace_read", time.Since(start), "unauthenticated")
			return structuredResult(mustJSON(map[string]any{"status": "unavailable", "reason": "shared workspaces require an authenticated user"})), nil, nil
		}
		if deps.Consent == nil || !deps.Consent.HasConsent(ctx, consent.PurposeWorkspace) {
			auditToolDenial(ctx, deps, "workspace_read", time.Since(start), "no_consent")
			return structuredResult(mustJSON(map[string]any{"status": "no_consent", "reason": "no recorded consent for the 'workspace' purpose"})), nil, nil
		}
		items, err := deps.Workspaces.Read(ctx, input.WorkspaceID, caller)
		if errors.Is(err, workspace.ErrNotMember) {
			// Non-member gets zero bytes — release-gating invariant.
			auditToolDenial(ctx, deps, "workspace_read", time.Since(start), "not_member")
			return structuredResult(mustJSON(map[string]any{"status": "not_member", "contributions": []any{}})), nil, nil
		}
		if err != nil {
			deps.Metrics.RecordToolCall("workspace_read", time.Since(start), err, "upstream_error", false)
			return upstreamErrorResponse("workspace_read", err), nil, nil
		}
		deps.Metrics.RecordToolCall("workspace_read", time.Since(start), nil, "", false)
		auditToolCallQuery(ctx, deps, "workspace_read", time.Since(start), nil, "", "", map[string]any{"event": "workspace.read", "workspace_id": input.WorkspaceID})
		// Contributions may come from OTHER members (the highest-risk poisoning
		// vector: cross-principal, persisted). Mark the payload untrusted so the
		// host treats it as data — this does NOT restrict who may read it
		// (membership-gated above; every member still sees all contributions).
		return structuredResult(mustJSON(map[string]any{"status": "ok", "count": len(items), "contributions": items, "trust": untrustedContentTrust})), nil, nil
	})
}
