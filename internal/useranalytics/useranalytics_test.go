package useranalytics

import (
	"context"
	"testing"

	"github.com/zoharbabin/web-researcher-mcp/internal/datasubject"
	"github.com/zoharbabin/web-researcher-mcp/internal/persist"
)

func newRec() *StoreRecorder { return NewStoreRecorder(persist.NewMemoryStore()) }

func TestRecordAndGet(t *testing.T) {
	r := newRec()
	ctx := context.Background()
	r.Record(ctx, "t1", "u1", "web_search")
	r.Record(ctx, "t1", "u1", "web_search")
	r.Record(ctx, "t1", "u1", "scrape_page")

	s, ok := r.Get(ctx, "t1", "u1")
	if !ok {
		t.Fatal("expected a recorded summary")
	}
	if s.TotalCalls != 3 {
		t.Errorf("expected 3 total calls, got %d", s.TotalCalls)
	}
	if s.ToolCounts["web_search"] != 2 || s.ToolCounts["scrape_page"] != 1 {
		t.Errorf("unexpected tool counts: %+v", s.ToolCounts)
	}
	if s.FirstSeen == "" || s.LastSeen == "" {
		t.Error("expected first/last seen timestamps")
	}
}

func TestRecordIgnoresAnonymous(t *testing.T) {
	r := newRec()
	ctx := context.Background()
	r.Record(ctx, "t1", "anonymous", "web_search")
	r.Record(ctx, "t1", "", "web_search")
	r.Record(ctx, "", "u1", "web_search")
	if _, ok := r.Get(ctx, "t1", "anonymous"); ok {
		t.Error("must not record for anonymous user")
	}
}

func TestEraseRemovesUser(t *testing.T) {
	r := newRec()
	ctx := context.Background()
	r.Record(ctx, "t1", "u1", "web_search")

	n, err := r.Erase(ctx, "t1", "u1")
	if err != nil || n != 1 {
		t.Fatalf("expected 1 erased, got %d err=%v", n, err)
	}
	if _, ok := r.Get(ctx, "t1", "u1"); ok {
		t.Error("expected user analytics gone after erase")
	}
	// Erasing again is a clean no-op.
	if n, _ := r.Erase(ctx, "t1", "u1"); n != 0 {
		t.Errorf("expected 0 on second erase, got %d", n)
	}
}

func TestTenantIsolation(t *testing.T) {
	r := newRec()
	ctx := context.Background()
	r.Record(ctx, "t1", "u1", "web_search")
	if _, ok := r.Get(ctx, "t2", "u1"); ok {
		t.Error("same user id in a different tenant must not see data")
	}
}

func TestNoopCollectsNothing(t *testing.T) {
	n := NewNoop()
	ctx := context.Background()
	n.Record(ctx, "t1", "u1", "web_search")
	if _, ok := n.Get(ctx, "t1", "u1"); ok {
		t.Error("Noop must collect nothing")
	}
	if c, _ := n.Erase(ctx, "t1", "u1"); c != 0 {
		t.Error("Noop erase must report 0")
	}
}

// TestDataSubjectRoundTrip is the #85 release gate for user analytics: data is
// reachable by export and removed by erasure, scoped per user.
func TestDataSubjectRoundTrip(t *testing.T) {
	r := newRec()
	ctx := context.Background()
	r.Record(ctx, "t1", "u1", "web_search")
	adapter := AsDataSubject(r)

	// Tenant-only subject (no user) holds nothing here.
	if out, _ := adapter.ExportSubject(ctx, datasubject.Subject{TenantID: "t1"}); out != nil {
		t.Error("per-user store must return nil for a tenant-only subject")
	}

	out, err := adapter.ExportSubject(ctx, datasubject.Subject{TenantID: "t1", UserID: "u1"})
	if err != nil || out == nil {
		t.Fatalf("expected export data, got %v err=%v", out, err)
	}
	deleted, err := adapter.EraseSubject(ctx, datasubject.Subject{TenantID: "t1", UserID: "u1"})
	if err != nil || deleted != 1 {
		t.Fatalf("expected 1 erased, got %d err=%v", deleted, err)
	}
}
