package session

import (
	"context"

	"github.com/zoharbabin/web-researcher-mcp/internal/datasubject"
)

// SubjectAdapter exposes the session store as a data-subject Exporter/Eraser
// (#85). Sessions carry no per-user field, so both operations are TENANT-scoped
// — they cover every session for the subject's tenant. The exported payload and
// the erasure both make that scope explicit so the subject understands what was
// returned/removed.
type SubjectAdapter struct {
	mgr Manager
}

// AsDataSubject wraps a Manager as a data-subject Exporter/Eraser.
func AsDataSubject(mgr Manager) *SubjectAdapter { return &SubjectAdapter{mgr: mgr} }

// ExportSubject returns the tenant's session index entries. Scope is tenant
// (sessions have no user dimension); the wrapper notes that to the subject.
func (a *SubjectAdapter) ExportSubject(_ context.Context, s datasubject.Subject) (any, error) {
	indexes := a.mgr.ListByTenant(s.TenantID)
	return map[string]any{
		"scope":    "tenant",
		"note":     "Sessions are tenant-scoped (no per-user field); this covers all sessions for the tenant.",
		"sessions": indexes,
	}, nil
}

// EraseSubject deletes the tenant's sessions, returning the count removed.
func (a *SubjectAdapter) EraseSubject(_ context.Context, s datasubject.Subject) (int, error) {
	return a.mgr.DeleteByTenant(s.TenantID), nil
}

var (
	_ datasubject.Exporter = (*SubjectAdapter)(nil)
	_ datasubject.Eraser   = (*SubjectAdapter)(nil)
)
