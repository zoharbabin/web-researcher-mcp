package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/consent"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

const (
	monitorKeyPrefix   = "monitor:"
	monitorIndexPrefix = "monitor:index:"
	monitorTTLDefault  = 30 * 24 * time.Hour
	// monitorMaxPerUser bounds unbounded accumulation (OWASP Agentic ASI06): a
	// caller cannot grow their monitor set past this without deleting old ones.
	monitorMaxPerUser  = 100
	monitorMaxQueryLen = 500 // reuse web_search's query length cap
	monitorMaxTTLDays  = 90
)

// monitorRecord is the persisted state of one saved query monitor. SeenURLs is
// the union of every result URL observed so far — monitor_query_check diffs
// against it and then grows it, so a URL is reported as "new" only once.
type monitorRecord struct {
	Query      string    `json:"query"`
	Provider   string    `json:"provider,omitempty"`
	SeenURLs   []string  `json:"seenUrls"`
	SavedAt    time.Time `json:"savedAt"`
	LastRunAt  time.Time `json:"lastRunAt,omitempty"`
	TTLSeconds int64     `json:"ttlSeconds"`
}

// monitorStoreKey derives a bounded, deterministic persist.Store key from the
// (userID, query, provider) tuple. SHA-256 ensures fixed key length regardless
// of query text; 32 hex chars of the digest are sufficient for uniqueness.
func monitorStoreKey(userID, query, provider string) string {
	h := sha256.New()
	h.Write([]byte(userID))
	h.Write([]byte("|"))
	h.Write([]byte(strings.ToLower(strings.TrimSpace(query))))
	h.Write([]byte("|"))
	h.Write([]byte(provider))
	return monitorKeyPrefix + hex.EncodeToString(h.Sum(nil))[:32]
}

// monitorIndexKey holds the set of monitor store keys owned by one user, used
// only to enforce monitorMaxPerUser — never to answer a lookup (the lookup key
// is always recomputed from query+provider).
func monitorIndexKey(userID string) string {
	h := sha256.New()
	h.Write([]byte(userID))
	return monitorIndexPrefix + hex.EncodeToString(h.Sum(nil))[:32]
}

type monitorQuerySaveInput struct {
	Query    string `json:"query" jsonschema:"The search query to monitor (1-500 chars),required"`
	Provider string `json:"provider,omitempty" jsonschema:"Search provider (e.g. google, brave, duckduckgo). Must match what you pass to monitor_query_check. Leave empty for the configured default."`
	TTLDays  int    `json:"ttl_days,omitempty" jsonschema:"Days to retain this monitor (1-90, default 30). After expiry it is silently dropped."`
}

type monitorQueryCheckInput struct {
	Query    string `json:"query" jsonschema:"The query to check — must match the query passed to monitor_query_save,required"`
	Provider string `json:"provider,omitempty" jsonschema:"Provider to use — must match the provider used in monitor_query_save (or both empty for the default)."`
}

// loadMonitorIndex returns the set of store keys owned by userID (best-effort;
// a missing/corrupt index degrades to empty rather than failing the call).
func loadMonitorIndex(ctx context.Context, store interface {
	Get(ctx context.Context, key string) ([]byte, bool)
}, userID string) []string {
	raw, ok := store.Get(ctx, monitorIndexKey(userID))
	if !ok {
		return nil
	}
	var keys []string
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil
	}
	return keys
}

// registerMonitorQuerySave registers the WRITE tool that seeds a monitor
// baseline (#273): it runs the query once via the configured search provider
// and stores the resulting URLs as "already seen" so a later
// monitor_query_check reports only genuinely new results.
func registerMonitorQuerySave(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "monitor_query_save",
		Description:  "Save a search query to monitor for new results over time. Runs the query once now and records the results as the baseline — nothing is reported as \"new\" until you call monitor_query_check later. Opt-in and consent-gated: persists only if query monitoring is enabled and you have consented to the 'monitoring' purpose. Bounded to 100 monitors per user and a max 90-day retention. There are no background jobs — you must call monitor_query_check yourself to see what changed.",
		Annotations:  writeAnnotations(false),
		OutputSchema: monitorQuerySaveOutputSchema,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input monitorQuerySaveInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		input.Query = strings.TrimSpace(input.Query)
		if input.Query == "" {
			return toolError("query is required"), nil, nil
		}
		if len(input.Query) > monitorMaxQueryLen {
			return toolError(fmt.Sprintf("query must be %d characters or less", monitorMaxQueryLen)), nil, nil
		}
		if input.TTLDays < 0 || input.TTLDays > monitorMaxTTLDays {
			return toolError(fmt.Sprintf("ttl_days must be between 1 and %d", monitorMaxTTLDays)), nil, nil
		}

		userID := auth.UserIDFromContext(ctx)
		if userID == "" || userID == "anonymous" {
			auditToolDenial(ctx, deps, "monitor_query_save", time.Since(start), "unauthenticated")
			return structuredResult(mustJSON(map[string]any{"status": "unavailable", "reason": "query monitoring requires an authenticated user"})), nil, nil
		}
		if deps.Consent == nil || !deps.Consent.HasConsent(ctx, consent.PurposeMonitoring) {
			auditToolDenial(ctx, deps, "monitor_query_save", time.Since(start), "no_consent")
			return structuredResult(mustJSON(map[string]any{"status": "no_consent", "reason": "no recorded consent for the 'monitoring' purpose; nothing is stored"})), nil, nil
		}

		key := monitorStoreKey(userID, input.Query, input.Provider)
		index := loadMonitorIndex(ctx, deps.Monitor, userID)
		alreadySaved := false
		for _, k := range index {
			if k == key {
				alreadySaved = true
				break
			}
		}
		if !alreadySaved && len(index) >= monitorMaxPerUser {
			auditToolDenial(ctx, deps, "monitor_query_save", time.Since(start), "limit_reached")
			return structuredResult(mustJSON(map[string]any{"status": "limit_reached", "reason": fmt.Sprintf("you have reached the %d-monitor limit; delete an old one by letting it expire, or reuse an existing query", monitorMaxPerUser)})), nil, nil
		}

		provider, errResult := resolveProvider(deps, input.Provider)
		if errResult != nil {
			return errResult, nil, nil
		}
		results, err := provider.Web(ctx, search.WebSearchParams{Query: input.Query, NumResults: maxNumResults})
		if err != nil {
			recordToolCall(deps, "monitor_query_save", time.Since(start), err, "upstream_error", false)
			return upstreamErrorResponse("monitor_query_save", err), nil, nil
		}

		ttlDays := input.TTLDays
		if ttlDays == 0 {
			ttlDays = int(monitorTTLDefault / (24 * time.Hour))
		}
		ttl := time.Duration(ttlDays) * 24 * time.Hour

		seen := make([]string, 0, len(results))
		for _, r := range results {
			seen = append(seen, r.URL)
		}
		rec := monitorRecord{
			Query:      input.Query,
			Provider:   input.Provider,
			SeenURLs:   seen,
			SavedAt:    time.Now(),
			TTLSeconds: int64(ttl.Seconds()),
		}
		deps.Monitor.Set(ctx, key, mustJSON(rec), ttl)

		if !alreadySaved {
			index = append(index, key)
			deps.Monitor.Set(ctx, monitorIndexKey(userID), mustJSON(index), 0)
		}

		recordToolCall(deps, "monitor_query_save", time.Since(start), nil, "", false)
		auditToolCallQuery(ctx, deps, "monitor_query_save", time.Since(start), nil, "", input.Query, map[string]any{"event": "monitor.save", "seen_count": len(seen)})
		return structuredResult(mustJSON(map[string]any{
			"status":    "ok",
			"query":     input.Query,
			"provider":  input.Provider,
			"seenCount": len(seen),
			"savedAt":   rec.SavedAt.Format(time.RFC3339),
			"ttlDays":   ttlDays,
		})), nil, nil
	})
}

// registerMonitorQueryCheck registers the read tool that re-runs a saved
// query and returns only results not already in its baseline (#273). It is
// NOT idempotent: each call mutates the stored baseline with any new URLs
// found, so a second consecutive call with no upstream change returns zero
// new results.
func registerMonitorQueryCheck(srv *mcp.Server, deps Dependencies) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "monitor_query_check",
		Description:  "Check a query saved with monitor_query_save for new results since the last check (or since it was saved, on the first check). Re-runs the query live and returns only results whose URL hasn't been seen before, then updates the baseline — calling this twice in a row with no upstream change returns zero new results the second time. Opt-in and consent-gated: requires 'monitoring' purpose consent. Returns status \"not_found\" if the query/provider pair was never saved.",
		Annotations:  readOnlyAnnotations(false, true),
		OutputSchema: monitorQueryCheckOutputSchema,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input monitorQueryCheckInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		input.Query = strings.TrimSpace(input.Query)
		if input.Query == "" {
			return toolError("query is required"), nil, nil
		}

		userID := auth.UserIDFromContext(ctx)
		if userID == "" || userID == "anonymous" {
			auditToolDenial(ctx, deps, "monitor_query_check", time.Since(start), "unauthenticated")
			return structuredResult(mustJSON(map[string]any{"status": "unavailable", "reason": "query monitoring requires an authenticated user"})), nil, nil
		}
		if deps.Consent == nil || !deps.Consent.HasConsent(ctx, consent.PurposeMonitoring) {
			auditToolDenial(ctx, deps, "monitor_query_check", time.Since(start), "no_consent")
			return structuredResult(mustJSON(map[string]any{"status": "no_consent", "reason": "no recorded consent for the 'monitoring' purpose"})), nil, nil
		}

		key := monitorStoreKey(userID, input.Query, input.Provider)
		raw, ok := deps.Monitor.Get(ctx, key)
		if !ok {
			auditToolDenial(ctx, deps, "monitor_query_check", time.Since(start), "not_found")
			return structuredResult(mustJSON(map[string]any{"status": "not_found", "reason": "no monitor saved for this query/provider — call monitor_query_save first"})), nil, nil
		}
		var rec monitorRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			auditToolDenial(ctx, deps, "monitor_query_check", time.Since(start), "not_found")
			return structuredResult(mustJSON(map[string]any{"status": "not_found", "reason": "stored monitor is corrupt — call monitor_query_save again"})), nil, nil
		}

		provider, errResult := resolveProvider(deps, input.Provider)
		if errResult != nil {
			return errResult, nil, nil
		}
		results, err := provider.Web(ctx, search.WebSearchParams{Query: input.Query, NumResults: maxNumResults})
		if err != nil {
			recordToolCall(deps, "monitor_query_check", time.Since(start), err, "upstream_error", false)
			return upstreamErrorResponse("monitor_query_check", err), nil, nil
		}

		seen := make(map[string]bool, len(rec.SeenURLs))
		for _, u := range rec.SeenURLs {
			seen[u] = true
		}
		var newResults []search.SearchResult
		for _, r := range results {
			if !seen[r.URL] {
				newResults = append(newResults, r)
				seen[r.URL] = true
				rec.SeenURLs = append(rec.SeenURLs, r.URL)
			}
		}

		lastRunAt := rec.LastRunAt
		if lastRunAt.IsZero() {
			lastRunAt = rec.SavedAt
		}
		rec.LastRunAt = time.Now()
		ttl := time.Duration(rec.TTLSeconds) * time.Second
		if ttl <= 0 {
			ttl = monitorTTLDefault
		}
		deps.Monitor.Set(ctx, key, mustJSON(rec), ttl)

		recordToolCall(deps, "monitor_query_check", time.Since(start), nil, "", false)
		auditToolCallQuery(ctx, deps, "monitor_query_check", time.Since(start), nil, "", input.Query, map[string]any{"event": "monitor.check", "new_count": len(newResults)})
		return structuredResult(mustJSON(map[string]any{
			"status":     "ok",
			"query":      input.Query,
			"provider":   input.Provider,
			"newCount":   len(newResults),
			"lastRunAt":  lastRunAt.Format(time.RFC3339),
			"newResults": newResults,
			"trust":      untrustedContentTrust,
		})), nil, nil
	})
}
