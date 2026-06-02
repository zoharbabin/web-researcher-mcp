package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/consent"
)

type getMyAnalyticsInput struct {
	// No inputs: a user can only ever see THEIR OWN analytics. The subject is
	// taken from the authenticated context, never from a parameter, so one user
	// can never request another's data.
	_ struct{} `json:"-"`
}

// registerGetMyAnalytics registers the read-only per-user analytics view (#92).
// Registered only when a non-Noop recorder is present (USER_ANALYTICS_ENABLED).
// Returns the caller's own usage summary, and only when they have consented.
func registerGetMyAnalytics(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "get_my_analytics",
		Description:  "Return YOUR OWN usage analytics (which tools you used such as web_search or sequential_search, counts, first/last seen) for this tenant. Opt-in and consent-gated: returns data only if user-level analytics is enabled and you have consented to the 'analytics' purpose; otherwise returns a disabled/no-consent status. Shows only your own data — never another user's.",
		Annotations:  readOnlyAnnotations(true, false),
		OutputSchema: getMyAnalyticsOutputSchema,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ getMyAnalyticsInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		tenantID := auth.TenantIDFromContext(ctx)
		userID := auth.UserIDFromContext(ctx)

		if userID == "" || userID == "anonymous" {
			auditToolDenial(ctx, deps, "get_my_analytics", time.Since(start), "unauthenticated")
			return structuredResult(mustJSON(map[string]any{
				"status": "unavailable",
				"reason": "user-level analytics requires an authenticated user",
			})), nil, nil
		}
		if deps.Consent == nil || !deps.Consent.HasConsent(ctx, consent.PurposeAnalytics) {
			auditToolDenial(ctx, deps, "get_my_analytics", time.Since(start), "no_consent")
			return structuredResult(mustJSON(map[string]any{
				"status": "no_consent",
				"reason": "no recorded consent for the 'analytics' purpose; nothing is collected",
			})), nil, nil
		}

		summary, ok := deps.UserAnalytics.Get(ctx, tenantID, userID)
		if !ok {
			deps.Metrics.RecordToolCall("get_my_analytics", time.Since(start), nil, "", false)
			auditToolCall(ctx, deps, "get_my_analytics", time.Since(start), nil, "")
			return structuredResult(mustJSON(map[string]any{"status": "empty"})), nil, nil
		}
		out := map[string]any{"status": "ok", "analytics": summary}
		deps.Metrics.RecordToolCall("get_my_analytics", time.Since(start), nil, "", false)
		auditToolCall(ctx, deps, "get_my_analytics", time.Since(start), nil, "")
		return structuredResult(mustJSON(out)), nil, nil
	})
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
