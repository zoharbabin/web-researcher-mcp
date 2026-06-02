package memory

import (
	"context"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/datasubject"
	"github.com/zoharbabin/web-researcher-mcp/internal/persist"
)

func newStore() *StoreImpl { return NewStore(persist.NewMemoryStore(), time.Hour) }

func TestSaveAndRecall(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	if _, err := s.Save(ctx, Entry{TenantID: "t1", UserID: "u1", Topic: "go", Note: "generics landed in 1.18"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := s.Save(ctx, Entry{TenantID: "t1", UserID: "u1", Topic: "rust", Note: "ownership model"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	all, _ := s.Recall(ctx, "t1", "u1", "", 20)
	if len(all) != 2 {
		t.Fatalf("expected 2 memories, got %d", len(all))
	}

	goOnly, _ := s.Recall(ctx, "t1", "u1", "go", 20)
	if len(goOnly) != 1 || goOnly[0].Topic != "go" {
		t.Errorf("topic filter failed: %+v", goOnly)
	}
}

func TestRecallLimit(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, _ = s.Save(ctx, Entry{TenantID: "t1", UserID: "u1", Note: "n"})
	}
	got, _ := s.Recall(ctx, "t1", "u1", "", 3)
	if len(got) != 3 {
		t.Errorf("expected limit 3, got %d", len(got))
	}
}

func TestTenantUserIsolation(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	_, _ = s.Save(ctx, Entry{TenantID: "t1", UserID: "u1", Note: "secret"})

	if got, _ := s.Recall(ctx, "t1", "u2", "", 20); len(got) != 0 {
		t.Error("different user must not see memories")
	}
	if got, _ := s.Recall(ctx, "t2", "u1", "", 20); len(got) != 0 {
		t.Error("different tenant must not see memories")
	}
}

func TestEraseUser(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	_, _ = s.Save(ctx, Entry{TenantID: "t1", UserID: "u1", Note: "a"})
	_, _ = s.Save(ctx, Entry{TenantID: "t1", UserID: "u1", Note: "b"})

	n, err := s.EraseUser(ctx, "t1", "u1")
	if err != nil || n != 2 {
		t.Fatalf("expected 2 erased, got %d err=%v", n, err)
	}
	if got, _ := s.Recall(ctx, "t1", "u1", "", 20); len(got) != 0 {
		t.Error("expected memories gone after erase")
	}
}

func TestRetentionExpiry(t *testing.T) {
	// A tiny retention means the entry is written with an already-short TTL;
	// the memory persist store honors TTL, so an expired entry is not recalled.
	s := NewStore(persist.NewMemoryStore(), time.Millisecond)
	ctx := context.Background()
	_, _ = s.Save(ctx, Entry{TenantID: "t1", UserID: "u1", Note: "ephemeral"})
	time.Sleep(10 * time.Millisecond)
	if got, _ := s.Recall(ctx, "t1", "u1", "", 20); len(got) != 0 {
		t.Errorf("expected expired memory to be gone, got %d", len(got))
	}
}

func TestNoopStoresNothing(t *testing.T) {
	n := NewNoop()
	ctx := context.Background()
	_, _ = n.Save(ctx, Entry{TenantID: "t1", UserID: "u1", Note: "x"})
	if got, _ := n.Recall(ctx, "t1", "u1", "", 20); got != nil {
		t.Error("Noop must recall nothing")
	}
}

// TestDataSubjectRoundTrip is the #85 release gate for memory.
func TestDataSubjectRoundTrip(t *testing.T) {
	s := newStore()
	ctx := context.Background()
	_, _ = s.Save(ctx, Entry{TenantID: "t1", UserID: "u1", Note: "remember me"})
	adapter := AsDataSubject(s)

	if out, _ := adapter.ExportSubject(ctx, datasubject.Subject{TenantID: "t1"}); out != nil {
		t.Error("per-user store must return nil for tenant-only subject")
	}
	out, err := adapter.ExportSubject(ctx, datasubject.Subject{TenantID: "t1", UserID: "u1"})
	if err != nil || out == nil {
		t.Fatalf("expected export, got %v err=%v", out, err)
	}
	deleted, err := adapter.EraseSubject(ctx, datasubject.Subject{TenantID: "t1", UserID: "u1"})
	if err != nil || deleted != 1 {
		t.Fatalf("expected 1 erased, got %d err=%v", deleted, err)
	}
}

// TestMaxEntriesEvictsOldest verifies the per-(tenant,user) cap evicts the
// oldest entries and never touches another principal's entries.
func TestMaxEntriesEvictsOldest(t *testing.T) {
	ctx := context.Background()
	s := NewStore(persist.NewMemoryStore(), time.Hour).WithMaxEntries(3)

	for _, n := range []string{"a", "b", "c", "d", "e"} {
		if _, err := s.Save(ctx, Entry{TenantID: "t1", UserID: "u1", Note: n}); err != nil {
			t.Fatalf("save %s: %v", n, err)
		}
	}
	got, _ := s.Recall(ctx, "t1", "u1", "", 100)
	if len(got) != 3 {
		t.Fatalf("expected cap of 3 entries, got %d", len(got))
	}
	// Oldest ("a","b") evicted; newest ("c","d","e") kept.
	notes := map[string]bool{}
	for _, e := range got {
		notes[e.Note] = true
	}
	if notes["a"] || notes["b"] {
		t.Errorf("oldest entries should be evicted, got %v", notes)
	}
	if !notes["c"] || !notes["d"] || !notes["e"] {
		t.Errorf("newest entries should survive, got %v", notes)
	}

	// A different user is unaffected by u1's eviction.
	_, _ = s.Save(ctx, Entry{TenantID: "t1", UserID: "u2", Note: "z"})
	other, _ := s.Recall(ctx, "t1", "u2", "", 100)
	if len(other) != 1 || other[0].Note != "z" {
		t.Errorf("another user's entries must be untouched, got %+v", other)
	}
}
