package tools

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"sync"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/config"
	"github.com/zoharbabin/web-researcher-mcp/internal/persist"
)

// modelPrice is one entry of the operator-managed price table (#303):
// {"<provider>/<model-id>": {"input_per_1k": 0.003, "output_per_1k": 0.015}}.
// The key matches a panel member's "<provider>/<model-id>" identity — the same
// format resolveResearchPanel matches an explicit models override against —
// e.g. "openrouter/anthropic/claude-sonnet-4-6".
type modelPrice struct {
	InputPer1K  float64 `json:"input_per_1k"`
	OutputPer1K float64 `json:"output_per_1k"`
}

// PriceTable maps a panel member's "<provider>/<model-id>" key to its price.
// A model absent from the table prices at $0 — an unpriced model degrades to
// "no cost estimate available" rather than blocking the call.
type PriceTable map[string]modelPrice

// loadPriceTable reads the operator-managed JSON price table from path. An
// empty path returns an empty table (no pricing data, every estimate is $0)
// rather than an error — cost tracking is opt-in.
func loadPriceTable(path string) (PriceTable, error) {
	if path == "" {
		return PriceTable{}, nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- operator-configured startup path, not request input
	if err != nil {
		return nil, err
	}
	var pt PriceTable
	if err := json.Unmarshal(data, &pt); err != nil {
		return nil, err
	}
	return pt, nil
}

// cost estimates the USD cost of inputTokens/outputTokens for modelKey. Zero
// when modelKey has no price-table entry.
func (pt PriceTable) cost(modelKey string, inputTokens, outputTokens int) float64 {
	price, ok := pt[modelKey]
	if !ok {
		return 0
	}
	return float64(inputTokens)/1000*price.InputPer1K + float64(outputTokens)/1000*price.OutputPer1K
}

// researchPanelEstimatedOutputTokens is the assumed output-token budget for
// the PRE-FLIGHT cost estimate only (RESEARCH_PANEL_MAX_CALL_COST_USD /
// RESEARCH_PANEL_MAX_DAILY_COST_USD gates and dry-run mode): real output
// length is unknown before any model responds, so the pre-flight estimate
// uses this conservative fixed assumption per panel member. The post-call
// `_meta.cost_breakdown` always uses each model's real token usage.
const researchPanelEstimatedOutputTokens = 1000

// estimateInputTokens is a rough chars/4 heuristic (no tokenizer dependency),
// used only for the pre-flight estimate above.
func estimateInputTokens(question string) int {
	return int(math.Ceil(float64(len(question)) / 4))
}

// panelSpendStorePrefix namespaces research_panel's daily-spend counters in
// the shared persist.Store so they cannot collide with other subsystems (e.g.
// rate-limit daily quotas) backed by the same store.
const panelSpendStorePrefix = "research_panel:spend:"

// PanelCostGuard (#303) is research_panel's cost-tracking dependency: an
// operator price table, per-call and per-tenant-daily USD caps, a dry-run
// switch, and the persisted daily-spend tracker. All methods are safe to call
// on a nil *PanelCostGuard (every guard becomes a no-op) so callers never need
// a separate nil check — cost tracking is entirely opt-in.
type PanelCostGuard struct {
	Prices      PriceTable
	MaxCallUSD  float64 // 0 = no per-call cap
	MaxDailyUSD float64 // 0 = no per-tenant daily cap
	DryRun      bool

	store persist.Store
	mu    sync.Mutex
	// tenants is populated lazily; each entry mirrors ratelimit.tenantLimiter's
	// pattern (in-memory value, hydrated from/persisted to store on change).
	tenants map[string]*tenantSpend
}

type tenantSpend struct {
	mu    sync.Mutex
	total float64
	reset time.Time
}

// NewPanelCostGuard constructs the guard from ResearchPanelConfig, loading the
// operator price table from cfg.PriceTablePath. A nil store keeps daily-spend
// tracking in-process-memory only (resets on restart) — mirrors
// ratelimit.NewWithStore's optional persist.Store pattern.
func NewPanelCostGuard(cfg config.ResearchPanelConfig, store persist.Store) (*PanelCostGuard, error) {
	prices, err := loadPriceTable(cfg.PriceTablePath)
	if err != nil {
		return nil, err
	}
	return &PanelCostGuard{
		Prices:      prices,
		MaxCallUSD:  cfg.MaxCallCostUSD,
		MaxDailyUSD: cfg.MaxDailyCostUSD,
		DryRun:      cfg.DryRun,
		store:       store,
		tenants:     make(map[string]*tenantSpend),
	}, nil
}

// isDryRun reports whether dry-run mode is active. nil-safe.
func (g *PanelCostGuard) isDryRun() bool { return g != nil && g.DryRun }

// modelCost estimates the USD cost of modelKey's real token usage. nil-safe
// (returns 0 — an unconfigured guard has no pricing data).
func (g *PanelCostGuard) modelCost(modelKey string, inputTokens, outputTokens int) float64 {
	if g == nil {
		return 0
	}
	return g.Prices.cost(modelKey, inputTokens, outputTokens)
}

// EstimateCall returns the pre-flight USD estimate for asking every member of
// panel, using estimateInputTokens(question) and
// researchPanelEstimatedOutputTokens as the per-member token assumption. Used
// for the per-call/daily cap checks and the dry-run response. nil-safe.
func (g *PanelCostGuard) EstimateCall(panel []ModelProvider, question string) float64 {
	if g == nil {
		return 0
	}
	inTok := estimateInputTokens(question)
	var total float64
	for _, m := range panel {
		total += g.Prices.cost(m.Name()+"/"+m.ModelID(), inTok, researchPanelEstimatedOutputTokens)
	}
	return total
}

// ExceedsCallCap reports whether estimatedUSD exceeds the configured per-call
// cap. Always false when no cap is configured. nil-safe.
func (g *PanelCostGuard) ExceedsCallCap(estimatedUSD float64) bool {
	return g != nil && g.MaxCallUSD > 0 && estimatedUSD > g.MaxCallUSD
}

// getTenantSpend returns (lazily creating and hydrating from the store) the
// in-memory spend tracker for tenantID. Caller must not hold g.mu.
func (g *PanelCostGuard) getTenantSpend(tenantID string) *tenantSpend {
	g.mu.Lock()
	if g.tenants == nil {
		g.tenants = make(map[string]*tenantSpend)
	}
	ts, ok := g.tenants[tenantID]
	if ok {
		g.mu.Unlock()
		return ts
	}
	ts = &tenantSpend{reset: time.Now().Add(24 * time.Hour)}
	g.tenants[tenantID] = ts
	g.mu.Unlock()
	g.hydrateSpend(tenantID, ts)
	return ts
}

// hydrateSpend loads tenantID's persisted daily spend into ts exactly once, if
// it belongs to the current 24h window. No-op when no store is configured.
func (g *PanelCostGuard) hydrateSpend(tenantID string, ts *tenantSpend) {
	if g.store == nil {
		return
	}
	raw, ok := g.store.Get(context.Background(), panelSpendStorePrefix+tenantID)
	if !ok || len(raw) < 16 {
		return
	}
	total := math.Float64frombits(binary.BigEndian.Uint64(raw[0:8]))
	// #nosec G115 -- stored Unix seconds, always representable as int64
	reset := time.Unix(int64(binary.BigEndian.Uint64(raw[8:16])), 0)
	if time.Now().After(reset) {
		return // stale window — leave ts at its fresh zero value
	}
	ts.mu.Lock()
	ts.total = total
	ts.reset = reset
	ts.mu.Unlock()
}

// persistSpend writes total/reset through to the store under tenantID, TTL'd
// to 24h. No-op when no store is configured.
func (g *PanelCostGuard) persistSpend(tenantID string, total float64, reset time.Time) {
	if g.store == nil {
		return
	}
	buf := make([]byte, 16)
	binary.BigEndian.PutUint64(buf[0:8], math.Float64bits(total))
	// #nosec G115 -- Unix seconds of a 24h-bounded reset time, never negative
	binary.BigEndian.PutUint64(buf[8:16], uint64(reset.Unix()))
	g.store.Set(context.Background(), panelSpendStorePrefix+tenantID, buf, 24*time.Hour)
}

// dailySpent returns tenantID's spend total for the current 24h window,
// rolling it over to zero first if the window has elapsed.
func (g *PanelCostGuard) dailySpent(tenantID string) (float64, *tenantSpend) {
	ts := g.getTenantSpend(tenantID)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if time.Now().After(ts.reset) {
		ts.total = 0
		ts.reset = time.Now().Add(24 * time.Hour)
	}
	return ts.total, ts
}

// ExceedsDailyCap reports whether tenantID's already-spent total today plus
// additionalUSD would exceed the configured per-tenant daily cap. Always
// false when no cap is configured. nil-safe.
func (g *PanelCostGuard) ExceedsDailyCap(tenantID string, additionalUSD float64) bool {
	if g == nil || g.MaxDailyUSD <= 0 {
		return false
	}
	spent, _ := g.dailySpent(tenantID)
	return spent+additionalUSD > g.MaxDailyUSD
}

// RecordSpend adds usdSpent to tenantID's daily total and persists it. No-op
// for usdSpent<=0 (e.g. every panel member failed, or no pricing data). nil-safe.
func (g *PanelCostGuard) RecordSpend(tenantID string, usdSpent float64) {
	if g == nil || usdSpent <= 0 {
		return
	}
	_, ts := g.dailySpent(tenantID) // rolls the window over first if elapsed
	ts.mu.Lock()
	ts.total += usdSpent
	total, reset := ts.total, ts.reset
	ts.mu.Unlock()
	g.persistSpend(tenantID, total, reset)
}

// PanelSpendSnapshot is the admin-visible daily-spend view for one tenant,
// returned by diagnostics://panel/spend (#303).
type PanelSpendSnapshot struct {
	TenantID     string    `json:"tenant_id"`
	SpentUSD     float64   `json:"spent_usd"`
	CapUSD       float64   `json:"cap_usd,omitempty"`
	RemainingUSD float64   `json:"remaining_usd,omitempty"`
	ResetsAt     time.Time `json:"resets_at"`
	Configured   bool      `json:"configured"`
}

// Snapshot returns tenantID's current daily-spend view. Configured reports
// whether an operator has actually set up cost tracking (a price table or a
// cap) — a guard that merely exists as a zero-value dependency (the common
// case when no RESEARCH_PANEL_* cost env var is set) still reports
// Configured:false, satisfies resources.PanelSpendProvider directly, no
// adapter needed.
func (g *PanelCostGuard) Snapshot(tenantID string) any {
	if g == nil || (len(g.Prices) == 0 && g.MaxDailyUSD <= 0 && g.MaxCallUSD <= 0) {
		return PanelSpendSnapshot{TenantID: tenantID, Configured: false}
	}
	spent, ts := g.dailySpent(tenantID)
	ts.mu.Lock()
	resetsAt := ts.reset
	ts.mu.Unlock()
	snap := PanelSpendSnapshot{TenantID: tenantID, SpentUSD: spent, ResetsAt: resetsAt, Configured: true}
	if g.MaxDailyUSD > 0 {
		snap.CapUSD = g.MaxDailyUSD
		remaining := g.MaxDailyUSD - spent
		if remaining < 0 {
			remaining = 0
		}
		snap.RemainingUSD = remaining
	}
	return snap
}
