// Package memory implements opt-in, consent-gated, cross-session long-term
// research memory (#88). Today's sessions are TTL-bounded (4h) and scoped to a
// single conversation; this lets a user persist sources/conclusions and recall
// them across sessions ("what did I find about X", "continue where I left off").
//
// It is a REGULATED capability — persistent per-user research history carries
// data-retention and data-subject-rights obligations the TTL-only design
// deliberately avoids — so it is:
//
//   - OFF by default (MEMORY_ENABLED=false); Noop store → byte-for-byte no-op.
//   - Gated on consent for the "memory" purpose (#89); save/recall no-op or
//     refuse without it.
//   - Per-user, per-tenant isolated, encrypted at rest (shared persist.Store).
//   - Retention-bounded: each entry carries a configurable max lifetime
//     (MEMORY_RETENTION) after which it auto-expires — "data doesn't exist after
//     TTL" remains the default safety property unless the operator extends it.
//   - Covered by data-subject export + erasure (#85). Per decision, there is no
//     separate memory_forget tool — erasure flows through the #85 endpoint.
//   - Recall is user-initiated only; memory is never auto-injected.
package memory

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/zoharbabin/web-researcher-mcp/internal/persist"
)

// Entry is a single remembered item: a source the user found notable, or a
// conclusion they recorded, tagged for later recall.
type Entry struct {
	ID        string   `json:"id"`
	TenantID  string   `json:"tenantId"`
	UserID    string   `json:"userId"`
	Topic     string   `json:"topic,omitempty"`
	Note      string   `json:"note"`
	URL       string   `json:"url,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	CreatedAt string   `json:"createdAt"`
}

// Store persists per-user memory entries. Save appends; Recall returns recent
// entries (optionally filtered by topic); export/erase back the #85 rights.
type Store interface {
	Save(ctx context.Context, e Entry) (Entry, error)
	Recall(ctx context.Context, tenantID, userID, topic string, limit int) ([]Entry, error)
	ExportUser(ctx context.Context, tenantID, userID string) ([]Entry, error)
	EraseUser(ctx context.Context, tenantID, userID string) (int, error)
}

// Noop is the default store when memory is disabled: saves nothing, recalls
// nothing, so the path is a clean no-op.
type Noop struct{}

func NewNoop() *Noop { return &Noop{} }

func (Noop) Save(_ context.Context, e Entry) (Entry, error)                       { return e, nil }
func (Noop) Recall(context.Context, string, string, string, int) ([]Entry, error) { return nil, nil }
func (Noop) ExportUser(context.Context, string, string) ([]Entry, error)          { return nil, nil }
func (Noop) EraseUser(context.Context, string, string) (int, error)               { return 0, nil }

// StoreImpl is the persist-backed implementation. Each user's entries are kept
// in a single JSON index blob per (tenant,user); each Save also writes the
// entry under its own key with the retention TTL so the store can expire it,
// and prunes expired IDs from the index lazily on read.
type StoreImpl struct {
	store     persist.Store
	retention time.Duration
	clock     func() time.Time
	newID     func() string
}

// NewStore builds a memory store with the given retention window (0 → 90 days).
func NewStore(store persist.Store, retention time.Duration) *StoreImpl {
	if retention <= 0 {
		retention = 90 * 24 * time.Hour
	}
	return &StoreImpl{store: store, retention: retention, clock: time.Now, newID: func() string { return uuid.New().String() }}
}

func indexKey(tenantID, userID string) string { return "memory:index:" + tenantID + ":" + userID }
func entryKey(tenantID, userID, id string) string {
	return "memory:entry:" + tenantID + ":" + userID + ":" + id
}

// loadIndex returns the user's live entry IDs, pruning any that have expired
// out of the underlying store (lazy cleanup — retention is enforced by the
// per-entry TTL; the index just stops referencing vanished entries).
func (s *StoreImpl) loadIndex(ctx context.Context, tenantID, userID string) []string {
	data, ok := s.store.Get(ctx, indexKey(tenantID, userID))
	if !ok {
		return nil
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		return nil
	}
	return ids
}

func (s *StoreImpl) saveIndex(ctx context.Context, tenantID, userID string, ids []string) {
	if data, err := json.Marshal(ids); err == nil {
		// Index lives at least as long as the longest-lived entry; refresh on write.
		s.store.Set(ctx, indexKey(tenantID, userID), data, s.retention)
	}
}

func (s *StoreImpl) Save(ctx context.Context, e Entry) (Entry, error) {
	if e.ID == "" {
		e.ID = s.newID()
	}
	if e.CreatedAt == "" {
		e.CreatedAt = s.clock().UTC().Format(time.RFC3339)
	}
	data, err := json.Marshal(e)
	if err != nil {
		return Entry{}, err
	}
	s.store.Set(ctx, entryKey(e.TenantID, e.UserID, e.ID), data, s.retention)

	ids := s.loadIndex(ctx, e.TenantID, e.UserID)
	ids = append(ids, e.ID)
	s.saveIndex(ctx, e.TenantID, e.UserID, ids)
	return e, nil
}

func (s *StoreImpl) Recall(ctx context.Context, tenantID, userID, topic string, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 20
	}
	ids := s.loadIndex(ctx, tenantID, userID)
	var entries []Entry
	var live []string
	for _, id := range ids {
		data, ok := s.store.Get(ctx, entryKey(tenantID, userID, id))
		if !ok {
			continue // expired or erased; drop from the index below
		}
		var e Entry
		if err := json.Unmarshal(data, &e); err != nil {
			continue
		}
		live = append(live, id)
		if topic != "" && e.Topic != topic {
			continue
		}
		entries = append(entries, e)
	}
	// Lazy prune: rewrite the index without vanished entries.
	if len(live) != len(ids) {
		s.saveIndex(ctx, tenantID, userID, live)
	}
	// Most recent first.
	sort.Slice(entries, func(i, j int) bool { return entries[i].CreatedAt > entries[j].CreatedAt })
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

func (s *StoreImpl) ExportUser(ctx context.Context, tenantID, userID string) ([]Entry, error) {
	return s.Recall(ctx, tenantID, userID, "", 1<<30)
}

func (s *StoreImpl) EraseUser(ctx context.Context, tenantID, userID string) (int, error) {
	ids := s.loadIndex(ctx, tenantID, userID)
	for _, id := range ids {
		s.store.Delete(ctx, entryKey(tenantID, userID, id))
	}
	s.store.Delete(ctx, indexKey(tenantID, userID))
	return len(ids), nil
}

var _ Store = (*StoreImpl)(nil)
