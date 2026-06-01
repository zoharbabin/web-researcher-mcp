package workspace

import (
	"context"

	"github.com/zoharbabin/web-researcher-mcp/internal/datasubject"
)

// SubjectAdapter exposes a contributor's workspace contributions as a
// data-subject Exporter/Eraser (#96 → #85). A contributor retains erasure
// rights over their OWN contributions across all workspaces; this is the
// "contributors can erase their contributions" guarantee.
type SubjectAdapter struct {
	store Store
}

// AsDataSubject wraps a workspace Store for registration into the #85 registry.
func AsDataSubject(store Store) *SubjectAdapter { return &SubjectAdapter{store: store} }

func (a *SubjectAdapter) ExportSubject(ctx context.Context, s datasubject.Subject) (any, error) {
	if s.UserID == "" {
		return nil, nil
	}
	items, err := a.store.ExportContributor(ctx, s.TenantID, s.UserID)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return map[string]any{"contributions": items}, nil
}

func (a *SubjectAdapter) EraseSubject(ctx context.Context, s datasubject.Subject) (int, error) {
	if s.UserID == "" {
		return 0, nil
	}
	return a.store.EraseContributor(ctx, s.TenantID, s.UserID)
}

var (
	_ datasubject.Exporter = (*SubjectAdapter)(nil)
	_ datasubject.Eraser   = (*SubjectAdapter)(nil)
)
