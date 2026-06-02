package workspace

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/zoharbabin/web-researcher-mcp/internal/persist"
)

// Noop is the default store when workspaces are disabled: no membership, no
// contributions, so per-tenant isolation is byte-for-byte unchanged.
type Noop struct{}

func NewNoop() *Noop { return &Noop{} }

func (Noop) AddMember(context.Context, string, Member) error    { return nil }
func (Noop) RemoveMember(context.Context, string, Member) error { return nil }
func (Noop) IsMember(context.Context, string, Member) bool      { return false }
func (Noop) Contribute(context.Context, string, Member, Contribution) (Contribution, error) {
	return Contribution{}, ErrNotMember
}
func (Noop) Read(context.Context, string, Member) ([]Contribution, error)  { return nil, ErrNotMember }
func (Noop) EraseContributor(context.Context, string, string) (int, error) { return 0, nil }
func (Noop) ExportContributor(context.Context, string, string) ([]Contribution, error) {
	return nil, nil
}

// StoreImpl persists workspace state in the shared encrypted persist.Store.
// Keys: membership set, contribution index, per-contribution blobs, and a
// per-contributor index (for cross-workspace export/erasure).
type StoreImpl struct {
	store      persist.Store
	ttl        time.Duration
	maxContrib int // per-workspace contribution cap; oldest-evicted. 0 → defaultMaxContrib.
	clock      func() time.Time
	newID      func() string
}

// defaultMaxContrib bounds contributions per workspace so a looping agent or
// busy team cannot grow the encrypted store without limit (OWASP Agentic ASI06).
// Generous; oldest contributions are evicted past it.
const defaultMaxContrib = 5000

// NewStore builds a workspace store with the given workspace TTL (0 → 30 days).
func NewStore(store persist.Store, ttl time.Duration) *StoreImpl {
	if ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}
	return &StoreImpl{store: store, ttl: ttl, maxContrib: defaultMaxContrib, clock: time.Now, newID: func() string { return uuid.New().String() }}
}

// WithMaxContrib overrides the per-workspace contribution cap (≤0 keeps default).
func (s *StoreImpl) WithMaxContrib(n int) *StoreImpl {
	if n > 0 {
		s.maxContrib = n
	}
	return s
}

func memberKey(workspaceID string, m Member) string {
	return "workspace:member:" + workspaceID + ":" + m.TenantID + ":" + m.UserID
}
func contribIndexKey(workspaceID string) string { return "workspace:contrib-index:" + workspaceID }
func contribKey(workspaceID, id string) string  { return "workspace:contrib:" + workspaceID + ":" + id }
func contributorIndexKey(tenantID, userID string) string {
	return "workspace:by-contributor:" + tenantID + ":" + userID
}

func (s *StoreImpl) loadStrings(ctx context.Context, key string) []string {
	data, ok := s.store.Get(ctx, key)
	if !ok {
		return nil
	}
	var out []string
	_ = json.Unmarshal(data, &out)
	return out
}

func (s *StoreImpl) saveStrings(ctx context.Context, key string, vals []string) {
	if data, err := json.Marshal(vals); err == nil {
		s.store.Set(ctx, key, data, s.ttl)
	}
}

func (s *StoreImpl) AddMember(ctx context.Context, workspaceID string, m Member) error {
	s.store.Set(ctx, memberKey(workspaceID, m), []byte("1"), s.ttl)
	return nil
}

func (s *StoreImpl) RemoveMember(ctx context.Context, workspaceID string, m Member) error {
	s.store.Delete(ctx, memberKey(workspaceID, m))
	return nil
}

func (s *StoreImpl) IsMember(ctx context.Context, workspaceID string, m Member) bool {
	if m.UserID == "" || m.UserID == "anonymous" {
		return false
	}
	_, ok := s.store.Get(ctx, memberKey(workspaceID, m))
	return ok
}

func (s *StoreImpl) Contribute(ctx context.Context, workspaceID string, caller Member, c Contribution) (Contribution, error) {
	if !s.IsMember(ctx, workspaceID, caller) {
		return Contribution{}, ErrNotMember
	}
	// Stamp immutable provenance from the CALLER's validated identity — never
	// trust caller-supplied contributor fields.
	c.ID = s.newID()
	c.WorkspaceID = workspaceID
	c.ContributorTenant = caller.TenantID
	c.ContributorUser = caller.UserID
	c.CreatedAt = s.clock().UTC().Format(time.RFC3339)

	data, err := json.Marshal(c)
	if err != nil {
		return Contribution{}, err
	}
	s.store.Set(ctx, contribKey(workspaceID, c.ID), data, s.ttl)

	idx := s.loadStrings(ctx, contribIndexKey(workspaceID))
	idx = append(idx, c.ID)
	// Bound per-workspace growth: evict the oldest contributions (index is in
	// append order, front = oldest) until within the cap.
	for len(idx) > s.maxContrib {
		s.store.Delete(ctx, contribKey(workspaceID, idx[0]))
		idx = idx[1:]
	}
	s.saveStrings(ctx, contribIndexKey(workspaceID), idx)

	// Per-contributor index for cross-workspace export/erasure (#85). Entry is
	// "workspaceID/contribID".
	ck := contributorIndexKey(caller.TenantID, caller.UserID)
	cidx := s.loadStrings(ctx, ck)
	s.saveStrings(ctx, ck, append(cidx, workspaceID+"/"+c.ID))
	return c, nil
}

func (s *StoreImpl) Read(ctx context.Context, workspaceID string, caller Member) ([]Contribution, error) {
	// Defense-in-depth: membership re-verified on every read; a non-member gets
	// zero bytes, never a partial leak.
	if !s.IsMember(ctx, workspaceID, caller) {
		return nil, ErrNotMember
	}
	ids := s.loadStrings(ctx, contribIndexKey(workspaceID))
	var out []Contribution
	var live []string
	for _, id := range ids {
		data, ok := s.store.Get(ctx, contribKey(workspaceID, id))
		if !ok {
			continue
		}
		var c Contribution
		if err := json.Unmarshal(data, &c); err != nil {
			continue
		}
		live = append(live, id)
		out = append(out, c)
	}
	if len(live) != len(ids) {
		s.saveStrings(ctx, contribIndexKey(workspaceID), live)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// contributorRefs returns the contributor's (workspaceID, contribID) refs.
func (s *StoreImpl) contributorRefs(ctx context.Context, tenantID, userID string) [][2]string {
	raw := s.loadStrings(ctx, contributorIndexKey(tenantID, userID))
	refs := make([][2]string, 0, len(raw))
	for _, r := range raw {
		for i := 0; i < len(r); i++ {
			if r[i] == '/' {
				refs = append(refs, [2]string{r[:i], r[i+1:]})
				break
			}
		}
	}
	return refs
}

func (s *StoreImpl) ExportContributor(ctx context.Context, tenantID, userID string) ([]Contribution, error) {
	if userID == "" {
		return nil, nil
	}
	var out []Contribution
	for _, ref := range s.contributorRefs(ctx, tenantID, userID) {
		if data, ok := s.store.Get(ctx, contribKey(ref[0], ref[1])); ok {
			var c Contribution
			if json.Unmarshal(data, &c) == nil {
				out = append(out, c)
			}
		}
	}
	return out, nil
}

func (s *StoreImpl) EraseContributor(ctx context.Context, tenantID, userID string) (int, error) {
	if userID == "" {
		return 0, nil
	}
	refs := s.contributorRefs(ctx, tenantID, userID)
	for _, ref := range refs {
		s.store.Delete(ctx, contribKey(ref[0], ref[1]))
	}
	s.store.Delete(ctx, contributorIndexKey(tenantID, userID))
	return len(refs), nil
}

var _ Store = (*StoreImpl)(nil)
