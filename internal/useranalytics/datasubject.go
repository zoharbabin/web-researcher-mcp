package useranalytics

import (
	"context"

	"github.com/zoharbabin/web-researcher-mcp/internal/datasubject"
)

// SubjectAdapter exposes per-user analytics as a data-subject Exporter/Eraser
// (#85). Unlike sessions, analytics are genuinely per-user, so both operations
// require a user_id; a tenant-only subject (empty user_id) holds nothing here.
type SubjectAdapter struct {
	rec Recorder
}

// AsDataSubject wraps a Recorder for registration into the #85 registry.
func AsDataSubject(rec Recorder) *SubjectAdapter { return &SubjectAdapter{rec: rec} }

func (a *SubjectAdapter) ExportSubject(ctx context.Context, s datasubject.Subject) (any, error) {
	if s.UserID == "" {
		return nil, nil // per-user store: nothing for a tenant-only subject
	}
	summary, ok := a.rec.Get(ctx, s.TenantID, s.UserID)
	if !ok {
		return nil, nil
	}
	return summary, nil
}

func (a *SubjectAdapter) EraseSubject(ctx context.Context, s datasubject.Subject) (int, error) {
	if s.UserID == "" {
		return 0, nil
	}
	return a.rec.Erase(ctx, s.TenantID, s.UserID)
}

var (
	_ datasubject.Exporter = (*SubjectAdapter)(nil)
	_ datasubject.Eraser   = (*SubjectAdapter)(nil)
)
