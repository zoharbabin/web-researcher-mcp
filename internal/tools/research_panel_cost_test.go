package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zoharbabin/web-researcher-mcp/internal/config"
	"github.com/zoharbabin/web-researcher-mcp/internal/persist"
)

func writeTempPriceTable(t *testing.T, table map[string]modelPrice) string {
	t.Helper()
	data, err := json.Marshal(table)
	if err != nil {
		t.Fatalf("marshal price table: %v", err)
	}
	path := filepath.Join(t.TempDir(), "prices.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write price table: %v", err)
	}
	return path
}

func TestLoadPriceTable(t *testing.T) {
	t.Run("empty path returns empty table, no error", func(t *testing.T) {
		pt, err := loadPriceTable("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(pt) != 0 {
			t.Errorf("expected empty table, got %v", pt)
		}
	})

	t.Run("missing file returns error", func(t *testing.T) {
		if _, err := loadPriceTable(filepath.Join(t.TempDir(), "does-not-exist.json")); err == nil {
			t.Error("expected an error for a missing price table file")
		}
	})

	t.Run("valid JSON loads", func(t *testing.T) {
		path := writeTempPriceTable(t, map[string]modelPrice{
			"openrouter/anthropic/claude-sonnet-4-6": {InputPer1K: 0.003, OutputPer1K: 0.015},
		})
		pt, err := loadPriceTable(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := pt["openrouter/anthropic/claude-sonnet-4-6"]; got.InputPer1K != 0.003 || got.OutputPer1K != 0.015 {
			t.Errorf("unexpected price entry: %+v", got)
		}
	})

	t.Run("malformed JSON returns error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := loadPriceTable(path); err == nil {
			t.Error("expected an error for malformed JSON")
		}
	})
}

func TestPriceTableCost(t *testing.T) {
	pt := PriceTable{"mock-a/model-a": {InputPer1K: 0.003, OutputPer1K: 0.015}}

	if got := pt.cost("mock-a/model-a", 1000, 1000); got != 0.018 {
		t.Errorf("cost = %v, want 0.018", got)
	}
	if got := pt.cost("unpriced/model", 1000, 1000); got != 0 {
		t.Errorf("unpriced model cost = %v, want 0", got)
	}
}

func TestPanelCostGuardEstimateCall(t *testing.T) {
	pt := PriceTable{"mock-a/model-a": {InputPer1K: 0.01, OutputPer1K: 0.02}}
	guard := &PanelCostGuard{Prices: pt}
	panel := []ModelProvider{&mockModelProvider{name: "mock-a", modelID: "model-a"}}

	question := "abcd" // 4 chars -> estimateInputTokens = ceil(4/4) = 1
	got := guard.EstimateCall(panel, question)
	want := float64(1)/1000*0.01 + float64(researchPanelEstimatedOutputTokens)/1000*0.02
	if got != want {
		t.Errorf("EstimateCall = %v, want %v", got, want)
	}

	// A model absent from the price table contributes $0.
	unpriced := []ModelProvider{&mockModelProvider{name: "unpriced", modelID: "x"}}
	if got := guard.EstimateCall(unpriced, question); got != 0 {
		t.Errorf("EstimateCall for unpriced model = %v, want 0", got)
	}
}

func TestPanelCostGuardExceedsCallCap(t *testing.T) {
	guard := &PanelCostGuard{}
	if guard.ExceedsCallCap(1000) {
		t.Error("no cap configured (MaxCallUSD=0) should never block")
	}

	guard.MaxCallUSD = 0.01
	if !guard.ExceedsCallCap(0.02) {
		t.Error("estimate above cap should be blocked")
	}
	if guard.ExceedsCallCap(0.005) {
		t.Error("estimate below cap should not be blocked")
	}
}

// TestPanelCostGuardNilSafe proves every PanelCostGuard method tolerates a nil
// receiver — research_panel calls these unconditionally on deps.ResearchPanelCost,
// which is nil whenever cost tracking isn't configured (the zero-config default).
func TestPanelCostGuardNilSafe(t *testing.T) {
	var guard *PanelCostGuard

	if guard.isDryRun() {
		t.Error("nil guard should never report dry-run")
	}
	if guard.modelCost("x/y", 10, 10) != 0 {
		t.Error("nil guard modelCost should be 0")
	}
	if guard.EstimateCall(nil, "q") != 0 {
		t.Error("nil guard EstimateCall should be 0")
	}
	if guard.ExceedsCallCap(1000) {
		t.Error("nil guard should never exceed a call cap")
	}
	if guard.ExceedsDailyCap("tenant", 1000) {
		t.Error("nil guard should never exceed a daily cap")
	}
	guard.RecordSpend("tenant", 1000) // must not panic
	snap := guard.Snapshot("tenant")
	if s, ok := snap.(PanelSpendSnapshot); !ok || s.Configured {
		t.Errorf("nil guard Snapshot should report Configured:false, got %+v", snap)
	}
}

func TestPanelCostGuardDailyCapAndRecordSpend(t *testing.T) {
	store := persist.NewMemoryStore()
	guard, err := NewPanelCostGuard(config.ResearchPanelConfig{MaxDailyCostUSD: 1.0}, store)
	if err != nil {
		t.Fatalf("NewPanelCostGuard: %v", err)
	}

	if guard.ExceedsDailyCap("tenant-a", 0.5) {
		t.Error("first $0.50 of a $1.00 cap should not exceed it")
	}
	guard.RecordSpend("tenant-a", 0.7)
	if !guard.ExceedsDailyCap("tenant-a", 0.5) {
		t.Error("$0.70 spent + $0.50 more should exceed a $1.00 cap")
	}

	// A different tenant's cap is tracked independently.
	if guard.ExceedsDailyCap("tenant-b", 0.9) {
		t.Error("tenant-b has spent nothing yet, $0.90 should not exceed a $1.00 cap")
	}

	// Spend persists through the store: a fresh guard sharing the same store
	// picks up tenant-a's total (H7-style durability).
	guard2, err := NewPanelCostGuard(config.ResearchPanelConfig{MaxDailyCostUSD: 1.0}, store)
	if err != nil {
		t.Fatalf("NewPanelCostGuard: %v", err)
	}
	if !guard2.ExceedsDailyCap("tenant-a", 0.5) {
		t.Error("a new guard sharing the same persist.Store should see tenant-a's persisted $0.70 spend")
	}
}

// TestPanelCostGuardDailyCapResetIsRolling24h proves the daily-spend window
// is a true rolling 24h period anchored to "now" — not truncated to a fixed
// calendar boundary, which would make the window's actual length depend on
// when the first request of the "day" lands.
func TestPanelCostGuardDailyCapResetIsRolling24h(t *testing.T) {
	guard, err := NewPanelCostGuard(config.ResearchPanelConfig{MaxDailyCostUSD: 1.0}, persist.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewPanelCostGuard: %v", err)
	}

	before := time.Now()
	_, ts := guard.dailySpent("tenant-a")
	after := time.Now()

	ts.mu.Lock()
	reset := ts.reset
	ts.mu.Unlock()

	if reset.Before(before.Add(24*time.Hour)) || reset.After(after.Add(24*time.Hour)) {
		t.Errorf("reset = %v, want within [now+24h] at call time (between %v and %v) — got a truncated boundary instead of a rolling window", reset, before.Add(24*time.Hour), after.Add(24*time.Hour))
	}

	// Force a rollover and confirm it re-anchors to now+24h too, not to the
	// next calendar-truncated boundary.
	ts.mu.Lock()
	ts.total = 5
	ts.reset = time.Now().Add(-time.Second) // already elapsed
	ts.mu.Unlock()

	before = time.Now()
	spent, ts2 := guard.dailySpent("tenant-a")
	after = time.Now()
	if spent != 0 {
		t.Errorf("rollover should reset total to 0, got %v", spent)
	}
	ts2.mu.Lock()
	reset2 := ts2.reset
	ts2.mu.Unlock()
	if reset2.Before(before.Add(24*time.Hour)) || reset2.After(after.Add(24*time.Hour)) {
		t.Errorf("post-rollover reset = %v, want within [now+24h] at rollover time", reset2)
	}
}

func TestPanelCostGuardSnapshot(t *testing.T) {
	t.Run("zero-value guard reports not configured", func(t *testing.T) {
		guard := &PanelCostGuard{}
		snap, ok := guard.Snapshot("tenant-a").(PanelSpendSnapshot)
		if !ok || snap.Configured {
			t.Errorf("expected Configured:false, got %+v", snap)
		}
	})

	t.Run("configured guard reports spend and remaining budget", func(t *testing.T) {
		guard, err := NewPanelCostGuard(config.ResearchPanelConfig{MaxDailyCostUSD: 1.0}, persist.NewMemoryStore())
		if err != nil {
			t.Fatalf("NewPanelCostGuard: %v", err)
		}
		guard.RecordSpend("tenant-a", 0.4)
		snap, ok := guard.Snapshot("tenant-a").(PanelSpendSnapshot)
		if !ok {
			t.Fatalf("Snapshot did not return a PanelSpendSnapshot")
		}
		if !snap.Configured || snap.SpentUSD != 0.4 || snap.CapUSD != 1.0 || snap.RemainingUSD != 0.6 {
			t.Errorf("unexpected snapshot: %+v", snap)
		}
	})
}

// --- Tool-level integration tests -------------------------------------------

func TestResearchPanelDryRun(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int64
	deps := setupTestDeps()
	deps.ResearchPanelProviders = []ModelProvider{
		&mockModelProvider{name: "mock-a", modelID: "model-a", text: "answer", calls: &calls},
	}
	deps.ResearchPanelCost = &PanelCostGuard{DryRun: true}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "research_panel",
		Arguments: map[string]any{"query": "What color is the sky?"},
	})
	if err != nil || res.IsError {
		t.Fatalf("CallTool failed: err=%v isError=%v content=%v", err, res.IsError, res.Content)
	}
	if calls.Load() != 0 {
		t.Errorf("dry run must call no model, got %d Ask() calls", calls.Load())
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["dry_run"] != true {
		t.Errorf("expected dry_run:true, got %v", out["dry_run"])
	}
	wouldCall, _ := out["would_call"].([]any)
	if len(wouldCall) != 1 {
		t.Errorf("expected 1 would_call entry, got %v", out["would_call"])
	}
	meta, _ := out["_meta"].(map[string]any)
	if _, ok := meta["estimated_cost_usd"]; !ok {
		t.Error("expected _meta.estimated_cost_usd on a dry-run response")
	}
}

func TestResearchPanelCallCostCapBlocksBeforeAnyCall(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int64
	deps := setupTestDeps()
	deps.ResearchPanelProviders = []ModelProvider{
		&mockModelProvider{name: "mock-a", modelID: "model-a", text: "answer", calls: &calls},
	}
	deps.ResearchPanelCost = &PanelCostGuard{
		Prices:     PriceTable{"mock-a/model-a": {InputPer1K: 100, OutputPer1K: 100}},
		MaxCallUSD: 0.01,
	}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "research_panel",
		Arguments: map[string]any{"query": "What color is the sky?", "use_cache": false},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected the per-call cost cap to block the call, got success: %v", res.Content)
	}
	if calls.Load() != 0 {
		t.Errorf("a call blocked by the cost cap must not query any model, got %d Ask() calls", calls.Load())
	}
}

func TestResearchPanelDailyCostCapBlocksBeforeAnyCall(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int64
	deps := setupTestDeps()
	deps.ResearchPanelProviders = []ModelProvider{
		&mockModelProvider{name: "mock-a", modelID: "model-a", text: "answer", calls: &calls},
	}
	guard, err := NewPanelCostGuard(config.ResearchPanelConfig{MaxDailyCostUSD: 0.01}, persist.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewPanelCostGuard: %v", err)
	}
	guard.RecordSpend("default", 0.02) // auth.TenantIDFromContext defaults to "default" when unauthenticated
	deps.ResearchPanelCost = guard
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "research_panel",
		Arguments: map[string]any{"query": "What color is the sky?", "use_cache": false},
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected the daily cost cap to block the call, got success: %v", res.Content)
	}
	if calls.Load() != 0 {
		t.Errorf("a call blocked by the daily cap must not query any model, got %d Ask() calls", calls.Load())
	}
}

func TestResearchPanelCostBreakdownInMeta(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.ResearchPanelProviders = []ModelProvider{
		&mockModelProvider{name: "mock-a", modelID: "model-a", text: "answer"}, // 10 in / 20 out tokens, fixed by the mock
	}
	deps.ResearchPanelCost = &PanelCostGuard{
		Prices: PriceTable{"mock-a/model-a": {InputPer1K: 1, OutputPer1K: 1}}, // $1/1k tokens each way
	}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "research_panel",
		Arguments: map[string]any{"query": "What color is the sky?", "use_cache": false},
	})
	if err != nil || res.IsError {
		t.Fatalf("CallTool failed: err=%v isError=%v content=%v", err, res.IsError, res.Content)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	meta, _ := out["_meta"].(map[string]any)
	// 10 input tokens + 20 output tokens at $1/1k each = 0.01 + 0.02 = 0.03.
	if got := meta["estimated_cost_usd"]; got != 0.03 {
		t.Errorf("estimated_cost_usd = %v, want 0.03", got)
	}
	breakdown, _ := meta["cost_breakdown"].([]any)
	if len(breakdown) != 1 {
		t.Fatalf("expected 1 cost_breakdown entry, got %v", breakdown)
	}
	entry, _ := breakdown[0].(map[string]any)
	if entry["model_id"] != "model-a" || entry["tokens_in"].(float64) != 10 || entry["tokens_out"].(float64) != 20 || entry["usd"] != 0.03 {
		t.Errorf("unexpected cost_breakdown entry: %+v", entry)
	}
}

// TestResearchPanelPartialFailureExcludesFailedModelFromCost proves a failed
// panel member (which incurred no billable output) contributes nothing to
// cost_breakdown/estimated_cost_usd.
func TestResearchPanelPartialFailureExcludesFailedModelFromCost(t *testing.T) {
	ctx := context.Background()
	deps := setupTestDeps()
	deps.ResearchPanelProviders = []ModelProvider{
		&mockModelProvider{name: "mock-a", modelID: "model-a", text: "answer"},
		&mockModelProvider{name: "mock-b", modelID: "model-b", err: errors.New("simulated timeout")},
	}
	deps.ResearchPanelCost = &PanelCostGuard{
		Prices: PriceTable{
			"mock-a/model-a": {InputPer1K: 1, OutputPer1K: 1},
			"mock-b/model-b": {InputPer1K: 1, OutputPer1K: 1},
		},
	}
	srv := createTestServer(deps)
	session := connectTestClient(ctx, t, srv)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "research_panel",
		Arguments: map[string]any{"query": "What color is the sky?", "use_cache": false},
	})
	if err != nil || res.IsError {
		t.Fatalf("CallTool failed: err=%v isError=%v content=%v", err, res.IsError, res.Content)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	meta, _ := out["_meta"].(map[string]any)
	breakdown, _ := meta["cost_breakdown"].([]any)
	if len(breakdown) != 1 {
		t.Errorf("expected only the successful model in cost_breakdown, got %v", breakdown)
	}
}
