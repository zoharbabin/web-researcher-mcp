package memory

import (
	"context"

	"github.com/zoharbabin/web-researcher-mcp/internal/datasubject"
)

// SubjectAdapter exposes long-term memory as a data-subject Exporter/Eraser
// (#85). Memory is per-user, so a tenant-only subject (no user_id) holds
// nothing here. Erasure is the ONLY deletion path for memory (there is no
// separate memory_forget tool, per decision #12).
type SubjectAdapter struct {
	store Store
}

// AsDataSubject wraps a memory Store for registration into the #85 registry.
func AsDataSubject(store Store) *SubjectAdapter { return &SubjectAdapter{store: store} }

func (a *SubjectAdapter) ExportSubject(ctx context.Context, s datasubject.Subject) (any, error) {
	if s.UserID == "" {
		return nil, nil
	}
	entries, err := a.store.ExportUser(ctx, s.TenantID, s.UserID)
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	return map[string]any{"entries": entries}, nil
}

func (a *SubjectAdapter) EraseSubject(ctx context.Context, s datasubject.Subject) (int, error) {
	if s.UserID == "" {
		return 0, nil
	}
	return a.store.EraseUser(ctx, s.TenantID, s.UserID)
}

var (
	_ datasubject.Exporter = (*SubjectAdapter)(nil)
	_ datasubject.Eraser   = (*SubjectAdapter)(nil)
)
