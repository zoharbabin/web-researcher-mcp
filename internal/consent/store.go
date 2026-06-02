package consent

import (
	"context"
	"encoding/json"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/persist"
)

// StoreManager is the persist-backed Manager. It reuses the shared persist.Store
// (encrypted disk or memory, identical to sessions/quota), so consent records
// inherit AES-256-GCM-at-rest, per-key AAD binding, and local/distributed
// parity with no bespoke storage. Records are per (tenant,user,purpose) and
// never expire on their own — a consent decision persists until explicitly
// changed (withdrawal is a new record, not a deletion).
type StoreManager struct {
	store persist.Store
}

// NewStoreManager wraps a persist.Store as a consent Manager.
func NewStoreManager(store persist.Store) *StoreManager {
	return &StoreManager{store: store}
}

// key namespaces consent records so they never collide with other persist
// users (revocation, quota) sharing the same store.
func consentKey(tenantID, userID string, purpose Purpose) string {
	return "consent:" + tenantID + ":" + userID + ":" + string(purpose)
}

func (m *StoreManager) Record(ctx context.Context, rec Record) error {
	if !rec.Purpose.Valid() {
		return ErrUnknownPurpose
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	// ttl<=0 → no expiry: a consent decision stands until explicitly changed.
	m.store.Set(ctx, consentKey(rec.TenantID, rec.UserID, rec.Purpose), data, 0)
	return nil
}

// GrantIfAbsent records a granted decision for (tenantID,userID,purpose) ONLY
// when NO record currently exists, and reports whether it wrote one. It is the
// safe primitive for bootstrap auto-grants (e.g. STDIO single-user identity):
// because Record OVERWRITES and a withdrawal is stored as a Granted=false
// record, an unconditional re-grant on every startup would silently resurrect a
// withdrawn consent. GrantIfAbsent never touches an existing record — granted or
// withdrawn — so a user's withdrawal sticks across restarts. decidedFrom labels
// the provenance (e.g. "stdio_bootstrap"). It returns wrote=true ONLY when a
// granted record is actually readable back afterward — so a pre-existing record
// or an inert manager (Noop, which persists nothing) return (false, nil). An
// unknown purpose returns (false, ErrUnknownPurpose) — the error is propagated,
// not silent. Callers can gate a consent.grant audit event on wrote==true
// without emitting a phantom grant.
func GrantIfAbsent(ctx context.Context, m Manager, tenantID, userID string, purpose Purpose, decidedFrom, decidedAt string) (bool, error) {
	// Validate up front so the unknown-purpose contract holds for EVERY Manager,
	// not just StoreManager (Noop.Record returns nil and would otherwise mask it).
	if !purpose.Valid() {
		return false, ErrUnknownPurpose
	}
	if _, ok := m.Query(ctx, tenantID, userID, purpose); ok {
		return false, nil // a decision already exists (granted OR withdrawn) — leave it
	}
	if err := m.Record(ctx, Record{
		TenantID: tenantID, UserID: userID, Purpose: purpose,
		Granted: true, DecidedAt: decidedAt, DecidedFrom: decidedFrom,
	}); err != nil {
		return false, err
	}
	// Confirm the write persisted before reporting it: a Noop manager (or any
	// inert impl) accepts Record but stores nothing, so the re-read is what makes
	// the "no phantom grant / no phantom audit event" contract true.
	rec, ok := m.Query(ctx, tenantID, userID, purpose)
	return ok && rec.Granted, nil
}

func (m *StoreManager) Query(ctx context.Context, tenantID, userID string, purpose Purpose) (Record, bool) {
	data, ok := m.store.Get(ctx, consentKey(tenantID, userID, purpose))
	if !ok {
		return Record{}, false
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return Record{}, false
	}
	return rec, true
}

func (m *StoreManager) Withdraw(ctx context.Context, tenantID, userID string, purpose Purpose, decidedAt string) error {
	return m.Record(ctx, Record{
		TenantID:  tenantID,
		UserID:    userID,
		Purpose:   purpose,
		Granted:   false,
		DecidedAt: decidedAt,
	})
}

// HasConsent is fail-closed: an anonymous user, missing record, or withdrawn
// record all yield false. The subject is taken from the request context
// (validated OAuth claims), never from caller-supplied arguments.
func (m *StoreManager) HasConsent(ctx context.Context, purpose Purpose) bool {
	userID := auth.UserIDFromContext(ctx)
	if userID == "" || userID == "anonymous" {
		return false
	}
	rec, ok := m.Query(ctx, auth.TenantIDFromContext(ctx), userID, purpose)
	return ok && rec.Granted
}

var _ Manager = (*StoreManager)(nil)
