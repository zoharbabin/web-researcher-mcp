package tools

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/consent"
	"github.com/zoharbabin/web-researcher-mcp/internal/persist"
	"github.com/zoharbabin/web-researcher-mcp/internal/search"
)

// createIdentityTestServer is createTestServer plus a receiving middleware that
// stamps a fixed (tenantID, userID) identity onto every request — the monitor
// tools gate on auth.UserIDFromContext, which the plain in-memory test
// transport otherwise leaves at "anonymous".
func createIdentityTestServer(deps Dependencies, tenantID, userID string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	RegisterAll(srv, deps)
	srv.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			return next(auth.WithIdentity(ctx, tenantID, userID), method, req)
		}
	})
	return srv
}

func callMonitorTool(ctx context.Context, t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s) failed: %v", name, err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("CallTool(%s): no content", name)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("CallTool(%s): content is not text: %T", name, res.Content[0])
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
		t.Fatalf("CallTool(%s): response is not JSON: %v (%s)", name, err, tc.Text)
	}
	return out
}

// monitorDepsWithConsent builds test deps with a real (non-Noop) persist-backed
// consent manager and monitor store, and grants the monitoring purpose to
// (tenantID, userID) up front.
func monitorDepsWithConsent(t *testing.T, tenantID, userID string) Dependencies {
	t.Helper()
	deps := setupTestDeps()
	cm := consent.NewStoreManager(persist.NewMemoryStore())
	deps.Consent = cm
	deps.Monitor = persist.NewMemoryStore()
	if err := cm.Record(context.Background(), consent.Record{
		TenantID: tenantID, UserID: userID, Purpose: consent.PurposeMonitoring,
		Granted: true, DecidedAt: "2026-07-27T00:00:00Z",
	}); err != nil {
		t.Fatalf("Record consent: %v", err)
	}
	return deps
}

func TestMonitorQuerySave_RequiresAuth(t *testing.T) {
	deps := setupTestDeps()
	deps.Monitor = persist.NewMemoryStore()
	ctx := context.Background()
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	out := callMonitorTool(ctx, t, sess, "monitor_query_save", map[string]any{"query": "golang release notes"})
	if out["status"] != "unavailable" {
		t.Errorf("status = %v, want unavailable (anonymous caller)", out["status"])
	}
}

func TestMonitorQuerySave_RequiresConsent(t *testing.T) {
	deps := setupTestDeps()
	deps.Consent = consent.NewStoreManager(persist.NewMemoryStore()) // no grant recorded
	deps.Monitor = persist.NewMemoryStore()
	ctx := context.Background()
	srv := createIdentityTestServer(deps, "default", "user-1")
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	out := callMonitorTool(ctx, t, sess, "monitor_query_save", map[string]any{"query": "golang release notes"})
	if out["status"] != "no_consent" {
		t.Errorf("status = %v, want no_consent", out["status"])
	}
}

func TestMonitorQuerySave_SavesRecord(t *testing.T) {
	deps := monitorDepsWithConsent(t, "default", "user-1")
	ctx := context.Background()
	srv := createIdentityTestServer(deps, "default", "user-1")
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	out := callMonitorTool(ctx, t, sess, "monitor_query_save", map[string]any{"query": "golang release notes"})
	if out["status"] != "ok" {
		t.Fatalf("status = %v, want ok; full=%v", out["status"], out)
	}
	if out["seenCount"].(float64) != 1 {
		t.Errorf("seenCount = %v, want 1 (mockProvider returns 1 result)", out["seenCount"])
	}
	if out["ttlDays"].(float64) != 30 {
		t.Errorf("ttlDays = %v, want default 30", out["ttlDays"])
	}
	if out["savedAt"] == nil || out["savedAt"] == "" {
		t.Error("savedAt should be set")
	}
}

func TestMonitorQuerySave_EmptyQuery(t *testing.T) {
	deps := monitorDepsWithConsent(t, "default", "user-1")
	ctx := context.Background()
	srv := createIdentityTestServer(deps, "default", "user-1")
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "monitor_query_save", Arguments: map[string]any{"query": "   "}})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Error("whitespace-only query should be rejected")
	}
}

func TestMonitorQuerySave_QueryTooLong(t *testing.T) {
	deps := monitorDepsWithConsent(t, "default", "user-1")
	ctx := context.Background()
	srv := createIdentityTestServer(deps, "default", "user-1")
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	longQuery := make([]byte, monitorMaxQueryLen+1)
	for i := range longQuery {
		longQuery[i] = 'a'
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "monitor_query_save", Arguments: map[string]any{"query": string(longQuery)}})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Error("over-length query should be rejected")
	}
}

func TestMonitorQuerySave_TTLDaysOutOfRange(t *testing.T) {
	deps := monitorDepsWithConsent(t, "default", "user-1")
	ctx := context.Background()
	srv := createIdentityTestServer(deps, "default", "user-1")
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "monitor_query_save",
		Arguments: map[string]any{"query": "x", "ttl_days": monitorMaxTTLDays + 1},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Error("ttl_days beyond the max should be rejected")
	}
}

func TestMonitorQuerySave_PerUserCap(t *testing.T) {
	deps := monitorDepsWithConsent(t, "default", "user-1")
	ctx := context.Background()
	srv := createIdentityTestServer(deps, "default", "user-1")
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	for i := 0; i < monitorMaxPerUser; i++ {
		q := "query " + string(rune('a'+i%26)) + string(rune('0'+i/26))
		out := callMonitorTool(ctx, t, sess, "monitor_query_save", map[string]any{"query": q})
		if out["status"] != "ok" {
			t.Fatalf("monitor %d: status = %v, want ok", i, out["status"])
		}
	}

	out := callMonitorTool(ctx, t, sess, "monitor_query_save", map[string]any{"query": "one too many"})
	if out["status"] != "limit_reached" {
		t.Errorf("status = %v, want limit_reached after %d monitors", out["status"], monitorMaxPerUser)
	}
}

func TestMonitorQueryCheck_NotFound(t *testing.T) {
	deps := monitorDepsWithConsent(t, "default", "user-1")
	ctx := context.Background()
	srv := createIdentityTestServer(deps, "default", "user-1")
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	out := callMonitorTool(ctx, t, sess, "monitor_query_check", map[string]any{"query": "never saved"})
	if out["status"] != "not_found" {
		t.Errorf("status = %v, want not_found", out["status"])
	}
}

func TestMonitorQueryCheck_ReturnsOnlyNewResults(t *testing.T) {
	deps := monitorDepsWithConsent(t, "default", "user-1")
	provider := &monitorSequenceProvider{
		batches: [][]string{
			{"https://example.com/a"},
			{"https://example.com/a", "https://example.com/b"},
		},
	}
	deps.Search = provider
	ctx := context.Background()
	srv := createIdentityTestServer(deps, "default", "user-1")
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	save := callMonitorTool(ctx, t, sess, "monitor_query_save", map[string]any{"query": "watched topic"})
	if save["status"] != "ok" {
		t.Fatalf("save status = %v", save["status"])
	}

	check := callMonitorTool(ctx, t, sess, "monitor_query_check", map[string]any{"query": "watched topic"})
	if check["status"] != "ok" {
		t.Fatalf("check status = %v", check["status"])
	}
	if check["newCount"].(float64) != 1 {
		t.Fatalf("newCount = %v, want 1 (only b is new)", check["newCount"])
	}
	newResults, ok := check["newResults"].([]any)
	if !ok || len(newResults) != 1 {
		t.Fatalf("newResults = %v, want exactly 1 entry", check["newResults"])
	}
	entry := newResults[0].(map[string]any)
	if entry["url"] != "https://example.com/b" {
		t.Errorf("new result url = %v, want https://example.com/b", entry["url"])
	}
	if check["trust"] != untrustedContentTrust {
		t.Errorf("trust = %v, want %q", check["trust"], untrustedContentTrust)
	}
}

func TestMonitorQueryCheck_IdempotentOnNoNew(t *testing.T) {
	deps := monitorDepsWithConsent(t, "default", "user-1")
	ctx := context.Background()
	srv := createIdentityTestServer(deps, "default", "user-1")
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	save := callMonitorTool(ctx, t, sess, "monitor_query_save", map[string]any{"query": "steady topic"})
	if save["status"] != "ok" {
		t.Fatalf("save status = %v", save["status"])
	}

	// mockProvider returns the same single result every call, so both checks
	// should report zero new results — a repeat call finds nothing new.
	first := callMonitorTool(ctx, t, sess, "monitor_query_check", map[string]any{"query": "steady topic"})
	if first["newCount"].(float64) != 0 {
		t.Fatalf("first check newCount = %v, want 0 (already seen at save time)", first["newCount"])
	}
	second := callMonitorTool(ctx, t, sess, "monitor_query_check", map[string]any{"query": "steady topic"})
	if second["newCount"].(float64) != 0 {
		t.Fatalf("second check newCount = %v, want 0", second["newCount"])
	}
}

func TestMonitorQueryCheck_RequiresAuth(t *testing.T) {
	deps := setupTestDeps()
	deps.Monitor = persist.NewMemoryStore()
	ctx := context.Background()
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	out := callMonitorTool(ctx, t, sess, "monitor_query_check", map[string]any{"query": "anything"})
	if out["status"] != "unavailable" {
		t.Errorf("status = %v, want unavailable (anonymous caller)", out["status"])
	}
}

func TestMonitorQueryCheck_RequiresConsent(t *testing.T) {
	deps := setupTestDeps()
	deps.Consent = consent.NewStoreManager(persist.NewMemoryStore()) // no grant recorded
	deps.Monitor = persist.NewMemoryStore()
	ctx := context.Background()
	srv := createIdentityTestServer(deps, "default", "user-1")
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	out := callMonitorTool(ctx, t, sess, "monitor_query_check", map[string]any{"query": "anything"})
	if out["status"] != "no_consent" {
		t.Errorf("status = %v, want no_consent", out["status"])
	}
}

func TestMonitorNotRegisteredWhenMonitorNil(t *testing.T) {
	deps := setupTestDeps()
	deps.Monitor = nil
	ctx := context.Background()
	srv := createTestServer(deps)
	sess := connectTestClient(ctx, t, srv)
	defer sess.Close()

	result, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	for _, tool := range result.Tools {
		if tool.Name == "monitor_query_save" || tool.Name == "monitor_query_check" {
			t.Errorf("%s should not be registered when deps.Monitor is nil", tool.Name)
		}
	}
}

// monitorSequenceProvider returns a different batch of URLs on each successive
// Web() call, letting a test simulate new results appearing between a save and
// a later check.
type monitorSequenceProvider struct {
	mu      sync.Mutex
	batches [][]string
	calls   int
}

func (p *monitorSequenceProvider) Web(_ context.Context, _ search.WebSearchParams) ([]search.SearchResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := p.calls
	if idx >= len(p.batches) {
		idx = len(p.batches) - 1
	}
	p.calls++
	urls := p.batches[idx]
	results := make([]search.SearchResult, 0, len(urls))
	for _, u := range urls {
		results = append(results, search.SearchResult{Title: "t", URL: u, Snippet: "s"})
	}
	return results, nil
}

func (p *monitorSequenceProvider) Images(_ context.Context, _ search.ImageSearchParams) ([]search.ImageResult, error) {
	return nil, nil
}
func (p *monitorSequenceProvider) News(_ context.Context, _ search.NewsSearchParams) ([]search.NewsResult, error) {
	return nil, nil
}
func (p *monitorSequenceProvider) Name() string { return "monitor-sequence" }
