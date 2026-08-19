package redisbackend

import (
	"errors"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/session"
)

// TestSessionManagerSetResearchGoal verifies the goal is persisted and visible
// through GetFull, and that setting it on an unknown session surfaces the
// typed not-found error rather than silently no-op-ing.
func TestSessionManagerSetResearchGoal(t *testing.T) {
	b := newTestBackend(t)
	m := b.SessionManager()

	idx, err := m.Create("tenant-1", "u1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.SetResearchGoal("tenant-1", "u1", idx.ID, "find quantum benchmarks"); err != nil {
		t.Fatalf("SetResearchGoal: %v", err)
	}

	full, err := m.GetFull("tenant-1", "u1", idx.ID)
	if err != nil {
		t.Fatalf("GetFull: %v", err)
	}
	if full.ResearchGoal != "find quantum benchmarks" {
		t.Errorf("ResearchGoal = %q, want %q", full.ResearchGoal, "find quantum benchmarks")
	}

	err = m.SetResearchGoal("tenant-1", "u1", "missing-id", "goal")
	var nf *session.SessionNotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("expected typed SessionNotFoundError for unknown session, got %v", err)
	}
}

// TestSessionManagerAddSources verifies AddSources appends new sources and
// dedupes by URL against both the existing set and duplicates within the same
// call — parity with the in-memory manager's contract (internal/session).
func TestSessionManagerAddSources(t *testing.T) {
	b := newTestBackend(t)
	m := b.SessionManager()

	idx, err := m.Create("tenant-1", "u1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := m.AddSources("tenant-1", "u1", idx.ID, []session.ResearchSource{
		{URL: "https://a.example", Title: "A"},
		{URL: "https://b.example", Title: "B"},
	}); err != nil {
		t.Fatalf("AddSources: %v", err)
	}

	// Second call: one duplicate URL (must not double up), one new URL.
	if err := m.AddSources("tenant-1", "u1", idx.ID, []session.ResearchSource{
		{URL: "https://a.example", Title: "A-dup"},
		{URL: "https://c.example", Title: "C"},
	}); err != nil {
		t.Fatalf("AddSources (2nd call): %v", err)
	}

	full, err := m.GetFull("tenant-1", "u1", idx.ID)
	if err != nil {
		t.Fatalf("GetFull: %v", err)
	}
	if len(full.Sources) != 3 {
		t.Fatalf("expected 3 deduped sources, got %d: %+v", len(full.Sources), full.Sources)
	}
	seen := make(map[string]bool)
	for _, s := range full.Sources {
		seen[s.URL] = true
	}
	for _, want := range []string{"https://a.example", "https://b.example", "https://c.example"} {
		if !seen[want] {
			t.Errorf("missing expected source URL %q", want)
		}
	}
}

// TestSessionManagerAddSourcesNotFound verifies AddSources surfaces the typed
// not-found error for an unknown session rather than silently no-op-ing.
func TestSessionManagerAddSourcesNotFound(t *testing.T) {
	b := newTestBackend(t)
	m := b.SessionManager()

	err := m.AddSources("tenant-1", "u1", "missing-id", []session.ResearchSource{{URL: "https://a.example"}})
	var nf *session.SessionNotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("expected typed SessionNotFoundError, got %v", err)
	}
}

// TestSessionManagerRecordOutcome verifies RecordOutcome appends an outcome
// event visible via GetFull, and that recording against a missing/expired
// session is a silent no-op (by design — outcome telemetry must never fail
// the calling tool; see the doc comment on RecordOutcome).
func TestSessionManagerRecordOutcome(t *testing.T) {
	b := newTestBackend(t)
	m := b.SessionManager()

	idx, err := m.Create("tenant-1", "u1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ev := session.OutcomeEvent{Provider: "google", Success: true, StepNumber: 1}
	if err := m.RecordOutcome("tenant-1", "u1", idx.ID, ev); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}

	full, err := m.GetFull("tenant-1", "u1", idx.ID)
	if err != nil {
		t.Fatalf("GetFull: %v", err)
	}
	if len(full.Outcomes) != 1 || full.Outcomes[0].Provider != "google" {
		t.Fatalf("expected 1 outcome from google, got %+v", full.Outcomes)
	}

	// Missing session: must return nil (no-op), never an error.
	if err := m.RecordOutcome("tenant-1", "u1", "missing-id", ev); err != nil {
		t.Errorf("RecordOutcome on missing session must be a silent no-op, got err=%v", err)
	}
}

// TestSessionManagerGetStep verifies GetStep returns the requested step by
// number and a not-found error for a step number that was never appended.
func TestSessionManagerGetStep(t *testing.T) {
	b := newTestBackend(t)
	m := b.SessionManager()

	idx, err := m.Create("tenant-1", "u1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for i := 1; i <= 3; i++ {
		if _, err := m.AppendStep("tenant-1", "u1", idx.ID, session.ResearchStep{StepNumber: i, Description: "step"}, nil, ""); err != nil {
			t.Fatalf("AppendStep %d: %v", i, err)
		}
	}

	step, err := m.GetStep("tenant-1", "u1", idx.ID, 2)
	if err != nil {
		t.Fatalf("GetStep: %v", err)
	}
	if step.StepNumber != 2 {
		t.Errorf("StepNumber = %d, want 2", step.StepNumber)
	}

	if _, err := m.GetStep("tenant-1", "u1", idx.ID, 99); err == nil {
		t.Error("expected error for nonexistent step number")
	}

	if _, err := m.GetStep("tenant-1", "u1", "missing-id", 1); err == nil {
		t.Error("expected error for a missing session")
	}
}

// TestSessionManagerActiveCountAndDeleteAll verifies ActiveCount reflects the
// number of live sessions across tenants, and DeleteAll wipes every session
// and tenant index so ActiveCount drops back to 0 — the admin-flush path.
func TestSessionManagerActiveCountAndDeleteAll(t *testing.T) {
	b := newTestBackend(t)
	m := b.SessionManager()

	if got := m.ActiveCount(); got != 0 {
		t.Fatalf("ActiveCount before any session = %d, want 0", got)
	}

	if _, err := m.Create("tenant-1", "u1"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := m.Create("tenant-2", "u1"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := m.Create("tenant-3", "u1"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got := m.ActiveCount(); got != 3 {
		t.Fatalf("ActiveCount = %d, want 3", got)
	}

	m.DeleteAll()

	if got := m.ActiveCount(); got != 0 {
		t.Errorf("ActiveCount after DeleteAll = %d, want 0", got)
	}
	if got := m.ListByTenant("tenant-1"); len(got) != 0 {
		t.Errorf("tenant-1 should have no sessions after DeleteAll, got %d", len(got))
	}
}

// TestSessionManagerActiveCountExcludesCompleted proves the #622 fix on the
// Redis backend: ActiveCount decrements when a session is marked complete,
// across multiple tenants (guarding against a fix that only works for a
// single tenant's index/completed-set pairing), while the session itself
// stays readable via GetFull — completion is a flag, not a deletion.
func TestSessionManagerActiveCountExcludesCompleted(t *testing.T) {
	b := newTestBackend(t)
	m := b.SessionManager()

	idxA1, err := m.Create("tenant-A", "u1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := m.Create("tenant-A", "u1"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	idxB1, err := m.Create("tenant-B", "u1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got := m.ActiveCount(); got != 3 {
		t.Fatalf("ActiveCount before completion = %d, want 3", got)
	}

	if err := m.MarkComplete("tenant-A", "u1", idxA1.ID); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}
	if err := m.MarkComplete("tenant-B", "u1", idxB1.ID); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}

	if got := m.ActiveCount(); got != 1 {
		t.Errorf("ActiveCount after completing 2 of 3 = %d, want 1", got)
	}

	full, err := m.GetFull("tenant-A", "u1", idxA1.ID)
	if err != nil {
		t.Fatalf("completed session should still load via GetFull: %v", err)
	}
	if !full.Completed {
		t.Error("Session.Completed should be true after MarkComplete")
	}

	idx, ok := m.GetIndex("tenant-A", "u1", idxA1.ID)
	if !ok || !idx.Completed {
		t.Errorf("GetIndex should report Completed=true, got ok=%v idx=%+v", ok, idx)
	}
}

// TestSessionManagerMarkCompleteNotFound proves MarkComplete surfaces the
// typed not-found error for an unknown session, same convention as
// SetResearchGoal.
func TestSessionManagerMarkCompleteNotFound(t *testing.T) {
	b := newTestBackend(t)
	m := b.SessionManager()

	err := m.MarkComplete("tenant-1", "u1", "missing-id")
	var nf *session.SessionNotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("expected typed SessionNotFoundError for unknown session, got %v", err)
	}
}

// TestSessionManagerDeleteRemovesFromCompletedSet proves Delete cleans up the
// completed-set membership too, so a deleted-then-recreated session in the
// same tenant doesn't inherit a stale "completed" mark from an unrelated
// former session that happened to reuse the set (#622 bookkeeping).
func TestSessionManagerDeleteRemovesFromCompletedSet(t *testing.T) {
	b := newTestBackend(t)
	m := b.SessionManager()

	idx, err := m.Create("tenant-1", "u1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.MarkComplete("tenant-1", "u1", idx.ID); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}
	if got := m.ActiveCount(); got != 0 {
		t.Fatalf("ActiveCount after completing the only session = %d, want 0", got)
	}

	m.Delete("tenant-1", "u1", idx.ID)

	idx2, err := m.Create("tenant-1", "u1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := m.ActiveCount(); got != 1 {
		t.Errorf("ActiveCount after deleting the completed session and creating a fresh one = %d, want 1", got)
	}
	got, ok := m.GetIndex("tenant-1", "u1", idx2.ID)
	if !ok || got.Completed {
		t.Errorf("fresh session must not inherit completed state, ok=%v idx=%+v", ok, got)
	}
}

// TestSessionManagerClose verifies Close is a safe no-op on the Redis-backed
// manager — the underlying client's lifecycle is owned by Backends.Close, not
// by the SessionManager, so this must never close the shared client out from
// under other consumers (e.g. SharedCache/PersistStore on the same Backends).
func TestSessionManagerClose(t *testing.T) {
	b := newTestBackend(t)
	m := b.SessionManager()

	idx, err := m.Create("tenant-1", "u1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	m.Close()

	// The shared client must still work after SessionManager.Close — proof that
	// it did not close the underlying Redis connection.
	if _, ok := m.GetIndex("tenant-1", "u1", idx.ID); !ok {
		t.Error("session manager unusable after Close — Close must not affect the shared client")
	}
}
