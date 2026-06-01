// Package datasubject implements GDPR/Law 25 data-subject rights (access,
// portability, erasure) over a pluggable registry. Each subsystem that holds
// personal data registers an Exporter and an Eraser under a namespace; the
// registry fans a single (tenantID, userID) request out to all of them.
//
// This is the keystone seam (#85): every per-user store (long-term memory #88,
// user analytics #92, workspace contributions #96) registers here, so a
// subject's export/erasure reaches every namespace with no central code
// knowing the feature set. A namespace that does not register leaves data
// unreachable by a subject request — the release gate is a round-trip test per
// store.
package datasubject

import (
	"context"
	"sort"
	"sync"
)

// Subject identifies the data subject. UserID may be empty for stores that are
// only tenant-scoped (e.g. sessions, which carry no per-user field); such a
// store erases/export at tenant granularity and says so in its result.
type Subject struct {
	TenantID string
	UserID   string
}

// Exporter returns this namespace's data for a subject as a JSON-serializable
// value (access + portability, GDPR Art. 15/20). Returning (nil, nil) means
// "nothing held for this subject".
type Exporter interface {
	ExportSubject(ctx context.Context, s Subject) (any, error)
}

// Eraser deletes this namespace's data for a subject and reports how many
// items were removed (erasure, GDPR Art. 17).
type Eraser interface {
	EraseSubject(ctx context.Context, s Subject) (deleted int, err error)
}

// ExporterFunc / EraserFunc adapt plain functions to the interfaces.
type ExporterFunc func(ctx context.Context, s Subject) (any, error)

func (f ExporterFunc) ExportSubject(ctx context.Context, s Subject) (any, error) { return f(ctx, s) }

type EraserFunc func(ctx context.Context, s Subject) (int, error)

func (f EraserFunc) EraseSubject(ctx context.Context, s Subject) (int, error) { return f(ctx, s) }

type entry struct {
	exporter Exporter
	eraser   Eraser
}

// Registry holds the registered per-namespace exporters/erasers. Safe for
// concurrent use; registration happens at startup, fan-out at request time.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]entry
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]entry)}
}

// Register adds a namespace's exporter and eraser. Either may be nil if the
// namespace only supports one direction (though both are expected for a
// personal-data store). Re-registering a namespace overwrites it.
func (r *Registry) Register(namespace string, exporter Exporter, eraser Eraser) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[namespace] = entry{exporter: exporter, eraser: eraser}
}

// Namespaces returns the registered namespace names, sorted, for diagnostics.
func (r *Registry) Namespaces() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.entries))
	for n := range r.entries {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ExportResult is the assembled access/portability payload for a subject.
type ExportResult struct {
	TenantID   string            `json:"tenantId"`
	UserID     string            `json:"userId,omitempty"`
	Namespaces map[string]any    `json:"namespaces"`
	Errors     map[string]string `json:"errors,omitempty"`
}

// Export fans the subject out to every registered exporter, assembling a
// per-namespace payload. A namespace error is recorded (not fatal) so one
// failing store does not deny the subject the rest of their data.
func (r *Registry) Export(ctx context.Context, s Subject) ExportResult {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := ExportResult{TenantID: s.TenantID, UserID: s.UserID, Namespaces: map[string]any{}}
	for ns, e := range r.entries {
		if e.exporter == nil {
			continue
		}
		data, err := e.exporter.ExportSubject(ctx, s)
		if err != nil {
			if res.Errors == nil {
				res.Errors = map[string]string{}
			}
			res.Errors[ns] = err.Error()
			continue
		}
		if data != nil {
			res.Namespaces[ns] = data
		}
	}
	return res
}

// EraseResult reports erasure outcomes per namespace.
type EraseResult struct {
	TenantID string            `json:"tenantId"`
	UserID   string            `json:"userId,omitempty"`
	Deleted  map[string]int    `json:"deleted"`
	Errors   map[string]string `json:"errors,omitempty"`
}

// Erase fans the subject out to every registered eraser. Errors are recorded
// per namespace; the operation is best-effort across namespaces so one failure
// does not strand data in the others.
func (r *Registry) Erase(ctx context.Context, s Subject) EraseResult {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := EraseResult{TenantID: s.TenantID, UserID: s.UserID, Deleted: map[string]int{}}
	for ns, e := range r.entries {
		if e.eraser == nil {
			continue
		}
		n, err := e.eraser.EraseSubject(ctx, s)
		if err != nil {
			if res.Errors == nil {
				res.Errors = map[string]string{}
			}
			res.Errors[ns] = err.Error()
			continue
		}
		res.Deleted[ns] = n
	}
	return res
}
