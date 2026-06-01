package consent

import (
	"context"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/auth"
	"github.com/zoharbabin/web-researcher-mcp/internal/persist"
)

func ctxWith(tenantID, userID string) context.Context {
	ctx := context.WithValue(context.Background(), auth.ContextKeyTenantID, tenantID)
	return context.WithValue(ctx, auth.ContextKeyUserID, userID)
}

func newManager() *StoreManager {
	return NewStoreManager(persist.NewMemoryStore())
}

func TestRecordQueryRoundTrip(t *testing.T) {
	m := newManager()
	ctx := context.Background()

	rec := Record{TenantID: "t1", UserID: "u1", Purpose: PurposeMemory, Granted: true, DecidedAt: "2026-05-31T00:00:00Z"}
	if err := m.Record(ctx, rec); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got, ok := m.Query(ctx, "t1", "u1", PurposeMemory)
	if !ok {
		t.Fatal("expected a stored record")
	}
	if !got.Granted || got.UserID != "u1" || got.Purpose != PurposeMemory {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestRecordUnknownPurpose(t *testing.T) {
	m := newManager()
	err := m.Record(context.Background(), Record{TenantID: "t1", UserID: "u1", Purpose: Purpose("bogus"), Granted: true})
	if err != ErrUnknownPurpose {
		t.Fatalf("expected ErrUnknownPurpose, got %v", err)
	}
}

func TestHasConsentFailClosed(t *testing.T) {
	m := newManager()

	// No record → false.
	if m.HasConsent(ctxWith("t1", "u1"), PurposeMemory) {
		t.Error("expected false with no record")
	}

	// Granted → true.
	_ = m.Record(context.Background(), Record{TenantID: "t1", UserID: "u1", Purpose: PurposeMemory, Granted: true})
	if !m.HasConsent(ctxWith("t1", "u1"), PurposeMemory) {
		t.Error("expected true after grant")
	}

	// Different purpose still false.
	if m.HasConsent(ctxWith("t1", "u1"), PurposeAnalytics) {
		t.Error("expected false for an ungranted purpose")
	}

	// Anonymous user → always false even if a record somehow exists.
	if m.HasConsent(ctxWith("t1", "anonymous"), PurposeMemory) {
		t.Error("expected false for anonymous user")
	}
	if m.HasConsent(context.Background(), PurposeMemory) {
		t.Error("expected false when no user in context")
	}
}

func TestWithdrawRevokes(t *testing.T) {
	m := newManager()
	_ = m.Record(context.Background(), Record{TenantID: "t1", UserID: "u1", Purpose: PurposeMemory, Granted: true})
	if !m.HasConsent(ctxWith("t1", "u1"), PurposeMemory) {
		t.Fatal("precondition: expected granted")
	}

	if err := m.Withdraw(context.Background(), "t1", "u1", PurposeMemory, "2026-05-31T01:00:00Z"); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if m.HasConsent(ctxWith("t1", "u1"), PurposeMemory) {
		t.Error("expected consent revoked after withdrawal")
	}
	// The withdrawal record is retained (auditability), with Granted=false.
	rec, ok := m.Query(context.Background(), "t1", "u1", PurposeMemory)
	if !ok || rec.Granted {
		t.Errorf("expected a retained withdrawal record, got ok=%v rec=%+v", ok, rec)
	}
}

func TestTenantIsolation(t *testing.T) {
	m := newManager()
	_ = m.Record(context.Background(), Record{TenantID: "t1", UserID: "u1", Purpose: PurposeMemory, Granted: true})

	// Same user id, different tenant → no consent (per-tenant isolation).
	if m.HasConsent(ctxWith("t2", "u1"), PurposeMemory) {
		t.Error("consent must not cross tenant boundaries")
	}
}

func TestNoopGrantsNothing(t *testing.T) {
	n := NewNoop()
	_ = n.Record(context.Background(), Record{TenantID: "t1", UserID: "u1", Purpose: PurposeMemory, Granted: true})
	if n.HasConsent(ctxWith("t1", "u1"), PurposeMemory) {
		t.Error("Noop must never grant consent")
	}
	if _, ok := n.Query(context.Background(), "t1", "u1", PurposeMemory); ok {
		t.Error("Noop must store nothing")
	}
}

func TestPurposeValid(t *testing.T) {
	for _, p := range AllPurposes {
		if !p.Valid() {
			t.Errorf("expected %q to be valid", p)
		}
	}
	if Purpose("nope").Valid() {
		t.Error("expected unknown purpose to be invalid")
	}
}
