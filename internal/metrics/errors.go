package metrics

import (
	"sync"
	"time"

	"github.com/zoharbabin/web-researcher-mcp/internal/audit"
)

// recentErrorsCap bounds the in-memory recent-errors ring buffer. Small and
// fixed: this is an at-a-glance operator drill-down ("what's failing now"), not
// a log store. Oldest entries are overwritten — no unbounded growth, no disk,
// consistent with the server's no-retention posture (issue #81).
const recentErrorsCap = 50

// ErrorRecord is one redacted, operator-facing error sample for the
// diagnostics://errors/recent Resource. It carries NO secrets (Cause is passed
// through audit.MaskSecrets at insert time), NO user query, and NO full URLs —
// only a typed kind, the tool, the optional provider, and a masked cause.
type ErrorRecord struct {
	Timestamp string `json:"timestamp"`
	Tool      string `json:"tool"`
	Kind      string `json:"kind"`
	Provider  string `json:"provider,omitempty"`
	Tier      string `json:"tier,omitempty"`
	TenantID  string `json:"tenantId,omitempty"`
	Cause     string `json:"cause,omitempty"`
}

// ErrorRing is a fixed-capacity, concurrency-safe ring buffer of recent errors.
// Memory-only and bounded by recentErrorsCap. It is tenant-aware: Recent(tid)
// filters to a single tenant for the per-caller MCP Resource, while Recent("")
// returns the global view for the operator dashboard.
type ErrorRing struct {
	mu      sync.Mutex
	buf     []ErrorRecord
	next    int
	total   int
	nowFunc func() time.Time // injectable clock for deterministic tests
}

// NewErrorRing returns an empty ring sized to recentErrorsCap.
func NewErrorRing() *ErrorRing {
	return &ErrorRing{
		buf:     make([]ErrorRecord, recentErrorsCap),
		nowFunc: time.Now,
	}
}

// Record inserts one error sample, redacting the cause through audit.MaskSecrets
// at the boundary so a credential echoed by an upstream error can never persist
// even in memory. Empty tool is ignored (nothing to attribute). The oldest entry
// is overwritten once the ring is full.
func (r *ErrorRing) Record(rec ErrorRecord) {
	if r == nil || rec.Tool == "" {
		return
	}
	rec.Cause = audit.MaskSecrets(rec.Cause)
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec.Timestamp == "" {
		rec.Timestamp = r.nowFunc().UTC().Format(time.RFC3339)
	}
	r.buf[r.next] = rec
	r.next = (r.next + 1) % len(r.buf)
	r.total++
}

// Recent returns up to recentErrorsCap recent errors, NEWEST FIRST. When
// tenantID is non-empty the result is filtered to that tenant (the per-caller
// Resource view); empty returns every tenant's errors (operator view).
func (r *ErrorRing) Recent(tenantID string) []ErrorRecord {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	n := r.total
	if n > len(r.buf) {
		n = len(r.buf)
	}
	out := make([]ErrorRecord, 0, n)
	// Walk backwards from the most recently written slot.
	for i := 0; i < n; i++ {
		idx := (r.next - 1 - i + len(r.buf)) % len(r.buf)
		rec := r.buf[idx]
		if tenantID != "" && rec.TenantID != tenantID {
			continue
		}
		out = append(out, rec)
	}
	return out
}
