package consent

import "context"

// Noop is the default Manager used when no regulated feature is enabled. It
// records nothing and grants nothing — HasConsent always returns false, so any
// regulated processing guarded by it is a clean no-op. This keeps the
// zero-config / no-regulated-feature path byte-for-byte unchanged.
type Noop struct{}

// NewNoop returns a consent manager that never grants and never stores.
func NewNoop() *Noop { return &Noop{} }

func (Noop) HasConsent(context.Context, Purpose) bool { return false }

func (Noop) Record(context.Context, Record) error { return nil }

func (Noop) Query(context.Context, string, string, Purpose) (Record, bool) {
	return Record{}, false
}

func (Noop) Withdraw(context.Context, string, string, Purpose, string) error { return nil }

var _ Manager = (*Noop)(nil)
