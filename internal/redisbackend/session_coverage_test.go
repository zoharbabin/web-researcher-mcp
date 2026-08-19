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

	_, err = m.GetStep("tenant-1", "u1", idx.ID, 99)
	var stepErr *session.StepNotFoundError
	if !errors.As(err, &stepErr) {
		t.Fatalf("expected *session.StepNotFoundError for an out-of-range step on a valid session, got %T: %v", err, err)
	}
	if stepErr.StepID != 99 || stepErr.StepCount != 3 {
		t.Errorf("expected StepID=99 StepCount=3, got StepID=%d StepCount=%d", stepErr.StepID, stepErr.StepCount)
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
