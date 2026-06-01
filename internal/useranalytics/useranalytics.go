// Package useranalytics implements opt-in, consent-gated, per-user usage
// analytics (#92). Unlike tenant-level aggregate analytics (#91, no consent
// needed), this is per-user profiling under GDPR / Quebec Law 25, so it is:
//
//   - OFF by default (USER_ANALYTICS_ENABLED=false).
//   - Collected ONLY after consent for the "analytics" purpose is recorded
//     (#89); the Recorder no-ops when consent is absent.
//   - Per-user, per-tenant isolated, encrypted at rest (shared persist.Store).
//   - Covered by data-subject export + erasure (#85): the store registers an
//     Exporter/Eraser so a subject request reaches it.
//   - Surfaced ONLY to the user it belongs to (a read-only tool returns the
//     caller's own analytics; never another user's).
//
// When disabled or consent-absent, zero data is collected — the Noop recorder
// makes the path a byte-for-byte no-op.
package useranalytics

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/persist"
)

// Summary is the per-user analytics view returned to the owning user.
type Summary struct {
	TenantID    string           `json:"tenantId"`
	UserID      string           `json:"userId"`
	TotalCalls  int64            `json:"totalCalls"`
	ToolCounts  map[string]int64 `json:"toolCounts"`
	FirstSeen   string           `json:"firstSeen,omitempty"`
	LastSeen    string           `json:"lastSeen,omitempty"`
	RecentTools []string         `json:"recentTools,omitempty"`
}

// Recorder records per-user tool usage. Record is a no-op unless analytics is
// enabled AND the user has consented (the consuming code checks consent before
// calling, and the Noop recorder is used when the feature is off).
type Recorder interface {
	Record(ctx context.Context, tenantID, userID, tool string)
	Get(ctx context.Context, tenantID, userID string) (Summary, bool)
	Erase(ctx context.Context, tenantID, userID string) (int, error)
}

// Noop is the default recorder when the feature is disabled. It collects
// nothing and returns nothing, so the path is a clean no-op.
type Noop struct{}

func NewNoop() *Noop { return &Noop{} }

func (Noop) Record(context.Context, string, string, string) {}

func (Noop) Get(context.Context, string, string) (Summary, bool) { return Summary{}, false }

func (Noop) Erase(context.Context, string, string) (int, error) { return 0, nil }

// StoreRecorder persists per-user analytics in the shared encrypted persist.Store.
// One record per (tenant,user); reads/writes are last-writer-wins on the small
// summary blob (usage analytics tolerate the rare lost concurrent increment).
type StoreRecorder struct {
	store persist.Store
	clock func() time.Time
}

// NewStoreRecorder builds a persist-backed recorder.
func NewStoreRecorder(store persist.Store) *StoreRecorder {
	return &StoreRecorder{store: store, clock: time.Now}
}

const recentToolsCap = 10

func key(tenantID, userID string) string { return "useranalytics:" + tenantID + ":" + userID }

func (r *StoreRecorder) load(ctx context.Context, tenantID, userID string) Summary {
	s := Summary{TenantID: tenantID, UserID: userID, ToolCounts: map[string]int64{}}
	if data, ok := r.store.Get(ctx, key(tenantID, userID)); ok {
		_ = json.Unmarshal(data, &s)
		if s.ToolCounts == nil {
			s.ToolCounts = map[string]int64{}
		}
	}
	return s
}

func (r *StoreRecorder) Record(ctx context.Context, tenantID, userID, tool string) {
	if tenantID == "" || userID == "" || userID == "anonymous" {
		return // never record without an identified subject
	}
	s := r.load(ctx, tenantID, userID)
	now := r.clock().UTC().Format(time.RFC3339)
	if s.TotalCalls == 0 {
		s.FirstSeen = now
	}
	s.TotalCalls++
	s.ToolCounts[tool]++
	s.LastSeen = now
	s.RecentTools = append(s.RecentTools, tool)
	if len(s.RecentTools) > recentToolsCap {
		s.RecentTools = s.RecentTools[len(s.RecentTools)-recentToolsCap:]
	}
	if data, err := json.Marshal(s); err == nil {
		r.store.Set(ctx, key(tenantID, userID), data, 0) // no TTL; erased on request
	}
}

func (r *StoreRecorder) Get(ctx context.Context, tenantID, userID string) (Summary, bool) {
	data, ok := r.store.Get(ctx, key(tenantID, userID))
	if !ok {
		return Summary{}, false
	}
	var s Summary
	if err := json.Unmarshal(data, &s); err != nil {
		return Summary{}, false
	}
	// RecentTools is stored oldest→newest (append order in Record); return it
	// most-recent-first so the "recent" semantics hold for the caller.
	for i, j := 0, len(s.RecentTools)-1; i < j; i, j = i+1, j-1 {
		s.RecentTools[i], s.RecentTools[j] = s.RecentTools[j], s.RecentTools[i]
	}
	return s, true
}

func (r *StoreRecorder) Erase(ctx context.Context, tenantID, userID string) (int, error) {
	if _, ok := r.store.Get(ctx, key(tenantID, userID)); !ok {
		return 0, nil
	}
	r.store.Delete(ctx, key(tenantID, userID))
	return 1, nil
}

var _ Recorder = (*StoreRecorder)(nil)
