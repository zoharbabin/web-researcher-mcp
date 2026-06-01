package session

import (
	"context"
	"testing"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/datasubject"
)

// TestSubjectAdapterRoundTrip is the #85 release gate for the session store:
// data created for a subject must be reachable by export AND removed by erasure.
func TestSubjectAdapterRoundTrip(t *testing.T) {
	m := newTestManager(time.Hour, 50)
	defer m.Close()

	idx, _ := m.Create("tenant-1")
	_, _ = m.AppendStep("tenant-1", idx.ID, ResearchStep{StepNumber: 1, Description: "step"}, nil, "")
	_, _ = m.Create("tenant-2") // a different tenant's data must be untouched

	adapter := AsDataSubject(m)
	ctx := context.Background()
	subj := datasubject.Subject{TenantID: "tenant-1", UserID: "u1"}

	// Export reaches the data and reports tenant scope.
	out, err := adapter.ExportSubject(ctx, subj)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	payload, ok := out.(map[string]any)
	if !ok || payload["scope"] != "tenant" {
		t.Fatalf("expected tenant-scoped payload, got %#v", out)
	}
	sessions, _ := payload["sessions"].([]*SessionIndex)
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session for tenant-1, got %d", len(sessions))
	}

	// Erasure removes tenant-1's sessions and reports the count.
	deleted, err := adapter.EraseSubject(ctx, subj)
	if err != nil {
		t.Fatalf("erase: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}
	if got := m.ListByTenant("tenant-1"); len(got) != 0 {
		t.Errorf("expected tenant-1 sessions gone, got %d", len(got))
	}
	// Tenant isolation: tenant-2 is untouched by tenant-1's erasure.
	if got := m.ListByTenant("tenant-2"); len(got) != 1 {
		t.Errorf("expected tenant-2 session intact, got %d", len(got))
	}
}
