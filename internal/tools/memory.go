package tools

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/consent"
	"github.com/zoharbabin/web-researcher-mcp/internal/memory"
)

type memorySaveInput struct {
	Note  string   `json:"note" jsonschema:"The finding or conclusion to remember for future sessions.,required"`
	Topic string   `json:"topic,omitempty" jsonschema:"Optional topic label to group and later recall related memories."`
	URL   string   `json:"url,omitempty" jsonschema:"Optional source URL this memory refers to."`
	Tags  []string `json:"tags,omitempty" jsonschema:"Optional tags for organization."`
}

type memoryRecallInput struct {
	Topic string `json:"topic,omitempty" jsonschema:"Optional topic to filter recalled memories. Omit to recall the most recent across all topics."`
	Limit int    `json:"limit,omitempty" jsonschema:"Max memories to return (default 20)."`
}

// registerMemorySave registers the WRITE tool that persists a cross-session
// memory (#88). It is consent-gated on the "memory" purpose and requires an
// authenticated user; without either it refuses (no silent persistence).
func registerMemorySave(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "memory_save",
		Description:  "Save a research finding to YOUR long-term memory so it can be recalled in future sessions (unlike sequential_search sessions, which expire after 4 hours). Opt-in and consent-gated: persists only if long-term memory is enabled and you have consented to the 'memory' purpose. Stored per-user, encrypted, retention-bounded, and erasable via the data-subject endpoint. Recall with memory_recall.",
		Annotations:  writeAnnotations(false),
		OutputSchema: memorySaveOutputSchema,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input memorySaveInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		if input.Note == "" {
			return toolError("note is required"), nil, nil
		}
		userID := auth.UserIDFromContext(ctx)
		if userID == "" || userID == "anonymous" {
			return structuredResult(mustJSON(map[string]any{"status": "unavailable", "reason": "long-term memory requires an authenticated user"})), nil, nil
		}
		if deps.Consent == nil || !deps.Consent.HasConsent(ctx, consent.PurposeMemory) {
			return structuredResult(mustJSON(map[string]any{"status": "no_consent", "reason": "no recorded consent for the 'memory' purpose; nothing is stored"})), nil, nil
		}
		tenantID := auth.TenantIDFromContext(ctx)
		saved, err := deps.Memory.Save(ctx, memory.Entry{
			TenantID: tenantID, UserID: userID,
			Topic: input.Topic, Note: input.Note, URL: input.URL, Tags: input.Tags,
		})
		if err != nil {
			return upstreamErrorResponse("memory_save", err), nil, nil
		}
		auditToolCall(ctx, deps, "memory_save", time.Since(start), nil, "")
		return structuredResult(mustJSON(map[string]any{"status": "ok", "id": saved.ID, "createdAt": saved.CreatedAt})), nil, nil
	})
}

// registerMemoryRecall registers the read-only tool that returns the caller's
// own remembered entries (#88). Consent-gated like memory_save.
func registerMemoryRecall(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "memory_recall",
		Description:  "Recall findings YOU previously saved with memory_save, across sessions (optionally filtered by topic). Opt-in and consent-gated: returns data only if long-term memory is enabled and you have consented to the 'memory' purpose. Shows only your own memories — never another user's. Use sequential_search for within-session research tracking.",
		Annotations:  readOnlyAnnotations(true, false),
		OutputSchema: memoryRecallOutputSchema,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input memoryRecallInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		userID := auth.UserIDFromContext(ctx)
		if userID == "" || userID == "anonymous" {
			return structuredResult(mustJSON(map[string]any{"status": "unavailable", "reason": "long-term memory requires an authenticated user"})), nil, nil
		}
		if deps.Consent == nil || !deps.Consent.HasConsent(ctx, consent.PurposeMemory) {
			return structuredResult(mustJSON(map[string]any{"status": "no_consent", "reason": "no recorded consent for the 'memory' purpose"})), nil, nil
		}
		entries, err := deps.Memory.Recall(ctx, auth.TenantIDFromContext(ctx), userID, input.Topic, input.Limit)
		if err != nil {
			return upstreamErrorResponse("memory_recall", err), nil, nil
		}
		auditToolCall(ctx, deps, "memory_recall", time.Since(start), nil, "")
		return structuredResult(mustJSON(map[string]any{"status": "ok", "count": len(entries), "memories": entries})), nil, nil
	})
}
